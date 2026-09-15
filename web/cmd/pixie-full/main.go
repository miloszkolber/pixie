// pixie_full supervises the internal bundled assistant and controller topology.
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/miloszkolber/pixie/cmd/internal/assistantconfig"
	"github.com/miloszkolber/pixie/cmd/internal/runtimeexec"
	"github.com/miloszkolber/pixie/internal/ownerlock"
)

const (
	minimumSecretLength = 32
	readyTimeout        = 25 * time.Second
	shutdownTimeout     = 25 * time.Second
	shutdownGrace       = 24 * time.Second
)

var (
	version  = "0.0.0-dev"
	revision = "unknown"
)

type commandKind uint8

const (
	commandServe commandKind = iota
	commandVersion
	commandHelp
)

type arguments struct {
	command         commandKind
	assistantConfig string
	webConfig       string
}

type assistantEndpoint struct {
	host     string
	port     int
	agentDir string
}

func (endpoint assistantEndpoint) readyURL() string {
	return fmt.Sprintf("http://%s:%d/readyz", endpoint.host, endpoint.port)
}

type archivePaths struct {
	bun        string
	assistant  string
	controller string
	piPackage  string
}

type childInvocation struct {
	path string
	args []string
	env  map[string]string
}

type childProcess struct {
	cmd  *exec.Cmd
	done chan struct{}
}

func main() {
	parsed, err := parseArguments(os.Args[1:])
	if err != nil {
		fail(err)
	}
	switch parsed.command {
	case commandVersion:
		fmt.Printf("pixie_full %s (revision %s)\n", version, revision)
		return
	case commandHelp:
		fmt.Fprintln(os.Stdout, usage())
		return
	}

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(signals)

	cleanSignal, err := supervise(parsed, os.Environ(), signals)
	if err != nil {
		fail(err)
	}
	if cleanSignal {
		return
	}
}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "pixie_full: %s\n", err)
	os.Exit(ownerlock.ExitCode(err))
}

func usage() string {
	return "usage: pixie_full serve --assistant-config ABS --web-config ABS"
}

// parseArguments deliberately keeps the internal full-service surface small.
func parseArguments(values []string) (arguments, error) {
	if len(values) == 0 {
		return arguments{}, errors.New("use `serve --assistant-config ABS --web-config ABS`, `--version`, or `--help`")
	}
	if len(values) == 1 {
		switch values[0] {
		case "--version":
			return arguments{command: commandVersion}, nil
		case "--help":
			return arguments{command: commandHelp}, nil
		}
	}
	if values[0] != "serve" {
		return arguments{}, fmt.Errorf(
			"unknown command %q; use `serve --assistant-config ABS --web-config ABS`, `--version`, or `--help`",
			values[0],
		)
	}
	if len(values) != 5 {
		return arguments{}, errors.New("use `serve --assistant-config ABS --web-config ABS`, `--version`, or `--help`")
	}

	var parsed arguments
	parsed.command = commandServe
	for index := 1; index < len(values); index += 2 {
		flag, value := values[index], values[index+1]
		if strings.TrimSpace(value) == "" {
			return arguments{}, fmt.Errorf("%s requires an absolute path", flag)
		}
		if !filepath.IsAbs(value) {
			return arguments{}, fmt.Errorf("%s must be an absolute path", flag)
		}
		switch flag {
		case "--assistant-config":
			if parsed.assistantConfig != "" {
				return arguments{}, errors.New("multiple --assistant-config values supplied")
			}
			parsed.assistantConfig = value
		case "--web-config":
			if parsed.webConfig != "" {
				return arguments{}, errors.New("multiple --web-config values supplied")
			}
			parsed.webConfig = value
		default:
			return arguments{}, fmt.Errorf("unknown serve argument %q", flag)
		}
	}
	if parsed.assistantConfig == "" || parsed.webConfig == "" {
		return arguments{}, errors.New("serve requires --assistant-config and --web-config")
	}
	return parsed, nil
}

// readAssistantEndpoint reads the same strict v2 file the hosted assistant will
// accept. Pi package selection is intentionally absent from that public file.
func readAssistantEndpoint(path string, lookup func(string) (string, bool)) (assistantEndpoint, error) {
	config, err := assistantconfig.Load(path)
	if err != nil {
		return assistantEndpoint{}, err
	}
	if _, err := literalLoopbackHost(config.Host); err != nil {
		return assistantEndpoint{}, err
	}

	// This mirrors pixie_assistant's host normalization. Its v2 port is an
	// explicit config field and therefore cannot be silently redirected.
	effectiveHost := config.Host
	if configured, ok := lookup("PIXIE_ASSISTANT_HOST"); ok && strings.TrimSpace(configured) != "" {
		effectiveHost = configured
	}
	canonicalHost, err := resolvedLoopbackHost(effectiveHost)
	if err != nil {
		return assistantEndpoint{}, err
	}
	return assistantEndpoint{host: canonicalHost, port: config.Port, agentDir: config.AgentDir}, nil
}

func literalLoopbackHost(value string) (string, error) {
	if value == "127.0.0.1" || value == "localhost" {
		return "127.0.0.1", nil
	}
	return "", errors.New("combined pixie requires assistant host 127.0.0.1 or localhost")
}

// resolvedLoopbackHost mirrors pixie_assistant's environment normalization.
func resolvedLoopbackHost(value string) (string, error) {
	normalized := strings.TrimSpace(value)
	if normalized == "127.0.0.1" || strings.EqualFold(normalized, "localhost") {
		return "127.0.0.1", nil
	}
	return "", errors.New("combined pixie requires assistant host 127.0.0.1 or localhost")
}

func requireSecret(lookup func(string) (string, bool)) (string, error) {
	value, ok := lookup("PIXIE_PI_SECRET_KEY")
	secret := ""
	if ok {
		secret = strings.TrimSpace(value)
	}
	if len(secret) < minimumSecretLength {
		return "", fmt.Errorf("PIXIE_PI_SECRET_KEY must be inherited and contain at least %d characters", minimumSecretLength)
	}
	return secret, nil
}

func resolveArchivePaths(executable string) (archivePaths, error) {
	if !filepath.IsAbs(executable) {
		return archivePaths{}, errors.New("combined pixie executable path must be absolute")
	}
	executable = filepath.Clean(executable)
	if err := validateRegularExecutable(executable); err != nil {
		return archivePaths{}, errors.New("combined pixie executable is not a regular executable")
	}
	libexec := filepath.Dir(executable)
	if filepath.Base(libexec) != "libexec" {
		return archivePaths{}, errors.New("combined pixie executable must be installed in archive libexec")
	}
	if err := validateDirectory(libexec); err != nil {
		return archivePaths{}, errors.New("combined pixie libexec directory is invalid")
	}
	root := filepath.Dir(libexec)
	if err := validateDirectory(root); err != nil {
		return archivePaths{}, errors.New("combined pixie archive root is invalid")
	}
	bun, err := archiveRelativeRuntimeExecutable(root, "runtime/bin/bun")
	if err != nil {
		return archivePaths{}, err
	}
	assistant, err := archiveRelativeFile(root, "libexec/pixie_assistant.js")
	if err != nil {
		return archivePaths{}, err
	}
	controller, err := archiveRelativeExecutable(root, "libexec/pixie_web")
	if err != nil {
		return archivePaths{}, err
	}
	piPackage, err := archiveRelativeDirectory(root, "runtime/node_modules/@earendil-works/pi-coding-agent")
	if err != nil {
		return archivePaths{}, err
	}
	return archivePaths{bun: bun, assistant: assistant, controller: controller, piPackage: piPackage}, nil
}

func archiveRelativeRuntimeExecutable(root, relative string) (string, error) {
	path, err := archiveRelativePath(root, relative, false)
	if err != nil {
		return "", err
	}
	if err := runtimeexec.Validate(path); err != nil {
		return "", fmt.Errorf("archive Bun runtime is invalid: %w", err)
	}
	return path, nil
}

func archiveRelativeFile(root, relative string) (string, error) {
	path, err := archiveRelativePath(root, relative, false)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", errors.New("archive file payload is invalid")
	}
	return path, nil
}

func archiveRelativeExecutable(root, relative string) (string, error) {
	path, err := archiveRelativePath(root, relative, false)
	if err != nil {
		return "", err
	}
	if err := validateRegularExecutable(path); err != nil {
		return "", errors.New("archive executable payload is invalid")
	}
	return path, nil
}

func archiveRelativeDirectory(root, relative string) (string, error) {
	path, err := archiveRelativePath(root, relative, true)
	if err != nil {
		return "", err
	}
	return path, nil
}

// archiveRelativePath rejects traversal and symlinked archive components before
// a private archive payload can be passed to exec.
func archiveRelativePath(root, relative string, directory bool) (string, error) {
	if !filepath.IsAbs(root) || filepath.IsAbs(relative) {
		return "", errors.New("archive payload path is invalid")
	}
	root = filepath.Clean(root)
	if err := validateDirectory(root); err != nil {
		return "", errors.New("archive root is invalid")
	}
	parts := strings.FieldsFunc(relative, func(character rune) bool {
		return character == filepath.Separator || character == '/'
	})
	if len(parts) == 0 {
		return "", errors.New("archive payload path is invalid")
	}
	current := root
	for index, part := range parts {
		if part == "" || part == "." || part == ".." {
			return "", errors.New("archive payload path is invalid")
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return "", errors.New("archive payload is unavailable")
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("archive payload must not be a symlink")
		}
		last := index == len(parts)-1
		if !last || directory {
			if !info.IsDir() {
				return "", errors.New("archive payload is invalid")
			}
		}
	}
	relativeToRoot, err := filepath.Rel(root, current)
	if err != nil || relativeToRoot == ".." || strings.HasPrefix(relativeToRoot, ".."+string(filepath.Separator)) {
		return "", errors.New("archive payload path escapes the archive")
	}
	return current, nil
}

func validateDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("not a directory")
	}
	return nil
}

func validateRegularExecutable(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode()&0o111 == 0 {
		return errors.New("not a regular executable")
	}
	return nil
}

func childInvocations(paths archivePaths, parsed arguments, endpoint assistantEndpoint, parent map[string]string) (childInvocation, childInvocation) {
	assistantEnvironment := cloneEnvironment(parent)
	delete(assistantEnvironment, "PIXIE_PI_PACKAGE")
	delete(assistantEnvironment, "PIXIE_BUNDLED_PI_PACKAGE")
	delete(assistantEnvironment, "PIXIE_BUNDLED_PI_TEST_MODE")
	assistantEnvironment["PIXIE_BUNDLED_PI_PACKAGE"] = paths.piPackage

	controllerEnvironment := make(map[string]string, len(parent)+1)
	for key, value := range parent {
		if key == "PIXIE_PI_PACKAGE" || key == "PI_CODING_AGENT_DIR" || key == "PIXIE_PI_URL" ||
			key == "PIXIE_PI_EXECUTABLE" || key == "PIXIE_PI_ARGS" || key == "PIXIE_LLAMA" ||
			key == "LLAMA_BASE_URL" || key == "PIXIE_ALLOW_SELF_RESTART" ||
			key == "PIXIE_BUNDLED_PI_PACKAGE" || key == "PIXIE_BUNDLED_PI_TEST_MODE" ||
			strings.HasPrefix(key, "PIXIE_ASSISTANT_") {
			continue
		}
		controllerEnvironment[key] = value
	}
	controllerEnvironment["PIXIE_PI_PORT"] = fmt.Sprint(endpoint.port)

	return childInvocation{
			path: paths.bun,
			args: []string{paths.assistant, "serve", "--config", parsed.assistantConfig},
			env:  assistantEnvironment,
		}, childInvocation{
			path: paths.controller,
			args: []string{"serve", "--config", parsed.webConfig},
			env:  controllerEnvironment,
		}
}

func cloneEnvironment(parent map[string]string) map[string]string {
	result := make(map[string]string, len(parent))
	for key, value := range parent {
		result[key] = value
	}
	return result
}

func environmentMap(values []string) map[string]string {
	environment := make(map[string]string, len(values))
	for _, entry := range values {
		key, value, ok := strings.Cut(entry, "=")
		if ok && key != "" {
			environment[key] = value
		}
	}
	return environment
}

func environmentSlice(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, key+"="+values[key])
	}
	return result
}

func rejectPublicPiSelection(parent map[string]string) error {
	if _, configured := parent["PIXIE_PI_PACKAGE"]; configured {
		return errors.New("PIXIE_PI_PACKAGE is not accepted; pixie_full always uses its bundled Pi archive")
	}
	return nil
}

func readinessRequest(ctx context.Context, endpoint assistantEndpoint, secret string) (*http.Request, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.readyURL(), nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+secret)
	return request, nil
}

func newReadinessClient() *http.Client {
	return &http.Client{
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func supervise(parsed arguments, inherited []string, signals <-chan os.Signal) (bool, error) {
	if runtime.GOOS != "linux" {
		return false, errors.New("combined pixie requires Linux process-group isolation")
	}
	parent := environmentMap(inherited)
	if err := rejectPublicPiSelection(parent); err != nil {
		return false, err
	}
	lookup := func(key string) (string, bool) { value, ok := parent[key]; return value, ok }
	secret, err := requireSecret(lookup)
	if err != nil {
		return false, err
	}
	endpoint, err := readAssistantEndpoint(parsed.assistantConfig, lookup)
	if err != nil {
		return false, err
	}
	agentDir, err := ownerlock.ResolveAgentDir(endpoint.agentDir, lookup)
	if err != nil {
		return false, err
	}
	lock, err := ownerlock.Acquire(agentDir, "pixie_full")
	if err != nil {
		return false, err
	}
	defer lock.Close()
	executable, err := os.Executable()
	if err != nil {
		return false, errors.New("could not resolve the combined pixie executable")
	}
	paths, err := resolveArchivePaths(executable)
	if err != nil {
		return false, err
	}
	assistantInvocation, controllerInvocation := childInvocations(paths, parsed, endpoint, parent)

	select {
	case <-signals:
		return true, nil
	default:
	}

	assistant, err := startChild(assistantInvocation)
	if err != nil {
		return false, errors.New("could not start bundled assistant")
	}
	children := []*childProcess{assistant}
	shutdown := func(initial syscall.Signal) error {
		return stopAndReap(children, initial)
	}

	receivedSignal, err := waitForAssistantReady(endpoint, secret, assistant, signals)
	if err != nil {
		if drainErr := shutdown(syscall.SIGTERM); drainErr != nil {
			return false, drainErr
		}
		return false, err
	}
	if receivedSignal != nil {
		if drainErr := shutdown(operatorSignal(receivedSignal)); drainErr != nil {
			return false, drainErr
		}
		return true, nil
	}
	if assistant.exited() {
		if drainErr := shutdown(syscall.SIGTERM); drainErr != nil {
			return false, drainErr
		}
		return false, errors.New("bundled assistant exited before controller startup")
	}

	controller, err := startChild(controllerInvocation)
	if err != nil {
		if drainErr := shutdown(syscall.SIGTERM); drainErr != nil {
			return false, drainErr
		}
		return false, errors.New("could not start bundled controller")
	}
	children = append(children, controller)

	for {
		select {
		case <-assistant.done:
			if err := shutdown(syscall.SIGTERM); err != nil {
				return false, err
			}
			return false, errors.New("bundled assistant exited")
		case <-controller.done:
			if err := shutdown(syscall.SIGTERM); err != nil {
				return false, err
			}
			return false, errors.New("bundled controller exited")
		case receivedSignal := <-signals:
			if err := shutdown(operatorSignal(receivedSignal)); err != nil {
				return false, err
			}
			return true, nil
		}
	}
}

func startChild(invocation childInvocation) (*childProcess, error) {
	command := exec.Command(invocation.path, invocation.args...)
	command.Env = environmentSlice(invocation.env)
	command.Stdin = os.Stdin
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := command.Start(); err != nil {
		return nil, err
	}
	child := &childProcess{cmd: command, done: make(chan struct{})}
	go func() {
		_ = command.Wait()
		close(child.done)
	}()
	return child, nil
}

func (child *childProcess) exited() bool {
	select {
	case <-child.done:
		return true
	default:
		return false
	}
}

func waitForAssistantReady(endpoint assistantEndpoint, secret string, assistant *childProcess, signals <-chan os.Signal) (os.Signal, error) {
	client := newReadinessClient()
	deadline := time.Now().Add(readyTimeout)
	for {
		select {
		case <-assistant.done:
			return nil, errors.New("bundled assistant exited before readiness")
		case receivedSignal := <-signals:
			return receivedSignal, nil
		default:
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil, errors.New("bundled assistant readiness timed out")
		}
		requestTimeout := remaining
		if requestTimeout > time.Second {
			requestTimeout = time.Second
		}
		requestContext, cancel := context.WithTimeout(context.Background(), requestTimeout)
		request, err := readinessRequest(requestContext, endpoint, secret)
		if err == nil {
			response, requestErr := client.Do(request)
			if response != nil {
				response.Body.Close()
			}
			if requestErr == nil && response.StatusCode == http.StatusOK {
				cancel()
				return nil, nil
			}
		}
		cancel()
		pause := 100 * time.Millisecond
		if remaining < pause {
			pause = remaining
		}
		timer := time.NewTimer(pause)
		select {
		case <-assistant.done:
			timer.Stop()
			return nil, errors.New("bundled assistant exited before readiness")
		case receivedSignal := <-signals:
			timer.Stop()
			return receivedSignal, nil
		case <-timer.C:
		}
	}
}

func stopAndReap(children []*childProcess, initial syscall.Signal) error {
	var signalErrors []error
	for _, child := range children {
		if err := signalProcessGroup(child, initial); err != nil {
			signalErrors = append(signalErrors, err)
		}
	}
	if waitForChildren(children, shutdownGrace) {
		return errors.Join(signalErrors...)
	}
	for _, child := range children {
		if err := signalProcessGroup(child, syscall.SIGKILL); err != nil {
			signalErrors = append(signalErrors, err)
		}
	}
	if waitForChildren(children, shutdownTimeout-shutdownGrace) {
		return errors.Join(signalErrors...)
	}
	return errors.Join(errors.Join(signalErrors...), errors.New("bundled child processes did not exit during shutdown"))
}

func operatorSignal(received os.Signal) syscall.Signal {
	if received == syscall.SIGINT {
		return syscall.SIGINT
	}
	return syscall.SIGTERM
}

func signalProcessGroup(child *childProcess, signal syscall.Signal) error {
	if err := syscall.Kill(-child.cmd.Process.Pid, signal); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}

func waitForChildren(children []*childProcess, timeout time.Duration) bool {
	allDone := make(chan struct{})
	go func() {
		for _, child := range children {
			<-child.done
		}
		close(allDone)
	}()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-allDone:
		return true
	case <-timer.C:
		return false
	}
}
