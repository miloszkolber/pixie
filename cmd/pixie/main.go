// pixie is the single host binary: bundled Pi TUI, Pi SDK, and assistant
// host connector. Bare `pixie` runs the native Pi TUI; `pixie serve
// --config ABS` starts the bundled assistant host. The TUI intentionally does
// not take the Pixie owner lock so it can run concurrently with the server
// against the same agent directory.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/miloszkolber/pixie/cmd/internal/assistantconfig"
	"github.com/miloszkolber/pixie/cmd/internal/processgroup"
	"github.com/miloszkolber/pixie/cmd/internal/runtimeexec"
	"github.com/miloszkolber/pixie/internal/diagnostics"
	"github.com/miloszkolber/pixie/internal/ownerlock"
)

const (
	piPackageName    = "@earendil-works/pi-coding-agent"
	piPackageVersion = "0.85.1"
)

const bundledPiPackageEnvironment = "PIXIE_BUNDLED_PI_PACKAGE"

const (
	// assistantSecretMinimum mirrors the assistant's own startup floor.
	assistantSecretMinimum = 32
	// assistantReadyTimeout bounds the /readyz handshake.
	assistantReadyTimeout = 25 * time.Second
)

// assistantEndpoint is the loopback readiness surface of the hosted assistant.
type assistantEndpoint struct {
	host string
	port int
}

func (endpoint assistantEndpoint) readyURL() string {
	return "http://" + net.JoinHostPort(endpoint.host, strconv.Itoa(endpoint.port)) + "/readyz"
}

// Release identity, injected with
// `-X main.version=<release-id> -X main.revision=<source-commit>`.
var (
	version  = "0.0.0-dev"
	revision = "unknown"
)

type serveArguments struct {
	configPath string
}

type archivePaths struct {
	bun       string
	cli       string
	assistant string
	piPackage string
}

type usageError struct{ message string }

func (err usageError) Error() string { return err.message }

func main() {
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		fmt.Println(versionLine())
		return
	}
	if len(os.Args) == 2 && os.Args[1] == "--pi-version" {
		fmt.Println(piPackageVersion)
		return
	}
	if len(os.Args) >= 2 && os.Args[1] == "serve" {
		parsed, err := parseServeArguments(os.Args[2:])
		if err != nil {
			fmt.Fprintln(os.Stderr, usage())
			if err.Error() != "" {
				fmt.Fprintf(os.Stderr, "pixie: %s\n", err)
			}
			os.Exit(2)
		}
		code, err := runServe(parsed, os.Environ())
		if err != nil {
			fmt.Fprintf(os.Stderr, "pixie: %s\n", err)
			os.Exit(ownerlock.ExitCode(err))
		}
		if code != 0 {
			os.Exit(code)
		}
		return
	}
	code, err := runTUI(os.Args[1:], os.Environ())
	if err != nil {
		fmt.Fprintf(os.Stderr, "pixie: %s\n", err)
		os.Exit(ownerlock.ExitCode(err))
	}
	if code != 0 {
		os.Exit(code)
	}
}

func usage() string {
	return "usage: pixie [--version|--pi-version] [pi args...] | pixie serve --config ABS"
}

// versionLine is the stable `--version` contract: the pixie release identity.
// Root `pixie --pi-version` reports the transparent bundled Pi version.
func versionLine() string {
	return fmt.Sprintf("pixie %s (revision %s)", version, revision)
}

func parseServeArguments(values []string) (serveArguments, error) {
	if len(values) != 2 || values[0] != "--config" {
		return serveArguments{}, usageError{message: "use `serve --config ABS`"}
	}
	configPath := strings.TrimSpace(values[1])
	if configPath == "" || !filepath.IsAbs(configPath) {
		return serveArguments{}, usageError{message: "--config must be an absolute path"}
	}
	return serveArguments{configPath: configPath}, nil
}

// runTUI delegates to upstream Pi without taking the Pixie owner lock, so the
// TUI can run concurrently with `pixie serve` against the same agent
// directory. It preserves the upstream Pi CLI boundary (including HOME and
// every native Pi environment variable).
func runTUI(args, environment []string) (int, error) {
	executable, err := os.Executable()
	if err != nil {
		return 0, errors.New("could not resolve the pixie executable")
	}
	paths, err := resolveArchivePaths(executable)
	if err != nil {
		return 0, err
	}
	if _, err := bundledPiVersion(paths.piPackage); err != nil {
		return 0, err
	}
	if isPiSelfUpdate(args) {
		return 0, errors.New("Pi self-update is disabled in Pixie archives; update the Pixie archive through its installer or package manager, then restart Pixie")
	}
	_ = environment
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(signals)
	code, err := processgroup.Run(piInvocation(paths, args), signals)
	if err != nil && code == 0 {
		return 0, errors.New("could not run bundled Pi")
	}
	return code, nil
}

// runServe starts the bundled assistant host. It is the only Pixie owner that
// takes the agent-directory lock.
func runServe(parsed serveArguments, inherited []string) (int, error) {
	parent := environmentMap(inherited)
	if err := rejectPublicPiSelection(parent); err != nil {
		return 0, err
	}
	config, err := assistantconfig.Load(parsed.configPath)
	if err != nil {
		return 0, err
	}
	lookup := func(key string) (string, bool) { value, ok := parent[key]; return value, ok }
	identity := diagnostics.ResolveProcessRunIdentity(lookup, time.Now())
	diagnostics.SetProcessRunIdentity(identity)
	agentDir, err := ownerlock.ResolveAgentDir(config.AgentDir, lookup)
	if err != nil {
		return 0, err
	}
	lock, err := ownerlock.Acquire(agentDir, "pixie")
	if err != nil {
		return 0, err
	}
	defer lock.Close()
	secret, err := requireAssistantSecret(lookup)
	if err != nil {
		return 0, err
	}
	// The launcher owns the child stderr boundary, so it records the configured
	// credentials before the assistant can write any diagnostic.
	diagnostics.ConfigureSanitizerSecrets(secret, parent["PIXIE_TOKEN"], parent["PIXIE_MCP_TOKEN"])
	executable, err := os.Executable()
	if err != nil {
		return 0, errors.New("could not resolve the pixie executable")
	}
	paths, err := resolveArchivePaths(executable)
	if err != nil {
		return 0, err
	}
	// A start is recorded only by the lock owner, so the bounded ledger has a
	// single writer. The restart window expires old starts on its own.
	recordRestartBudget(agentDir)
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(signals)
	stderr := assistantStderrSink(os.Stderr)
	invocation := assistantInvocation(paths, parsed, parent)
	invocation.Environment = withRunIdentity(invocation.Environment, identity)
	// Child stderr is line-buffered through one redaction sink, and the host
	// must answer /readyz before the launcher reports success.
	invocation.Stderr = stderr
	endpoint := assistantReadinessEndpoint(config)
	invocation.Ready = func(ctx context.Context) error {
		return waitForAssistantReady(ctx, endpoint, secret, assistantReadyTimeout)
	}
	code, err := processgroup.Run(invocation, signals)
	stderr.Flush()
	var readyErr processgroup.ReadyError
	if errors.As(err, &readyErr) {
		return 0, readyErr.Err
	}
	if err != nil && code == 0 {
		return 0, errors.New("could not run bundled pixie_assistant")
	}
	return code, nil
}

// piInvocation has no environment or cwd overrides, preserving the upstream
// Pi CLI boundary (including HOME and every native Pi environment variable).
func piInvocation(paths archivePaths, args []string) processgroup.Invocation {
	commandArgs := make([]string, 0, len(args)+1)
	commandArgs = append(commandArgs, paths.cli)
	commandArgs = append(commandArgs, args...)
	return processgroup.Invocation{Path: paths.bun, Args: commandArgs}
}

func environmentLookup(values []string) func(string) (string, bool) {
	environment := environmentMap(values)
	return func(key string) (string, bool) {
		value, ok := environment[key]
		return value, ok
	}
}

// requireAssistantSecret fails closed before the child starts when the shared
// bearer secret is absent, so the operator gets remediation instead of an
// opaque readiness timeout.
func requireAssistantSecret(lookup func(string) (string, bool)) (string, error) {
	value, ok := lookup("PIXIE_PI_SECRET_KEY")
	secret := strings.TrimSpace(value)
	if !ok || len(secret) < assistantSecretMinimum {
		return "", fmt.Errorf("PIXIE_PI_SECRET_KEY must be inherited and contain at least %d characters before the bundled assistant can start", assistantSecretMinimum)
	}
	return secret, nil
}

// assistantReadinessEndpoint derives the loopback /readyz surface from the same
// v2 configuration the hosted assistant was given.
func assistantReadinessEndpoint(config assistantconfig.Config) assistantEndpoint {
	return assistantEndpoint{host: config.Host, port: config.Port}
}

// waitForAssistantReady polls the authenticated /readyz endpoint until it
// answers 200, the deadline expires or ctx ends. It never includes the bearer
// secret in its error.
func waitForAssistantReady(ctx context.Context, endpoint assistantEndpoint, secret string, timeout time.Duration) error {
	client := &http.Client{
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	deadline := time.Now().Add(timeout)
	for {
		select {
		case <-ctx.Done():
			return errors.New("bundled assistant readiness was aborted before it became ready")
		default:
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return fmt.Errorf("bundled assistant did not become ready within %s; verify PIXIE_PI_SECRET_KEY and the configured assistant host and port", timeout)
		}
		requestTimeout := remaining
		if requestTimeout > time.Second {
			requestTimeout = time.Second
		}
		requestContext, cancel := context.WithTimeout(ctx, requestTimeout)
		request, err := http.NewRequestWithContext(requestContext, http.MethodGet, endpoint.readyURL(), nil)
		if err == nil {
			request.Header.Set("Authorization", "Bearer "+secret)
			response, requestErr := client.Do(request)
			if response != nil {
				response.Body.Close()
			}
			if requestErr == nil && response.StatusCode == http.StatusOK {
				cancel()
				return nil
			}
		}
		cancel()
		pause := 100 * time.Millisecond
		if remaining < pause {
			pause = remaining
		}
		timer := time.NewTimer(pause)
		select {
		case <-ctx.Done():
			timer.Stop()
			return errors.New("bundled assistant readiness was aborted before it became ready")
		case <-timer.C:
		}
	}
}

// recordRestartBudget appends this start to the bounded, self-expiring ledger
// and returns the starts inside the window.
func recordRestartBudget(agentDir string) int {
	ledger := diagnostics.NewStartLedger(filepath.Join(agentDir, "pixie", "restarts.json"))
	return ledger.Record()
}

// assistantStderrSink builds one line-buffering, redacting sink for the hosted
// assistant's stderr. Partial lines are held until a newline or Flush so a
// fragment cannot bypass the same boundary as a complete line.
func assistantStderrSink(console io.Writer) *diagnostics.StderrRing {
	return diagnostics.NewStderrRingWithConsole(console)
}

func assistantInvocation(paths archivePaths, parsed serveArguments, parent map[string]string) processgroup.Invocation {
	environment := make(map[string]string, len(parent)+1)
	for key, value := range parent {
		if key == bundledPiPackageEnvironment {
			continue
		}
		environment[key] = value
	}
	environment[bundledPiPackageEnvironment] = paths.piPackage
	return processgroup.Invocation{
		Path:        paths.bun,
		Args:        []string{paths.assistant, "serve", "--config", parsed.configPath},
		Environment: environmentSlice(environment),
	}
}

func rejectPublicPiSelection(environment map[string]string) error {
	if _, configured := environment["PIXIE_PI_PACKAGE"]; configured {
		return errors.New("PIXIE_PI_PACKAGE is not accepted; pixie always uses its bundled Pi archive")
	}
	return nil
}

func environmentMap(values []string) map[string]string {
	result := make(map[string]string, len(values))
	for _, value := range values {
		key, entry, ok := strings.Cut(value, "=")
		if ok && key != "" {
			result[key] = entry
		}
	}
	return result
}

func environmentSlice(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, key+"="+values[key])
	}
	return result
}

// withRunIdentity exports the launcher's opaque boot/run identifiers to the
// managed host without touching any other environment value.
func withRunIdentity(environment []string, identity diagnostics.RunIdentity) []string {
	exported := identity.Environment()
	if len(exported) == 0 {
		return environment
	}
	values := environmentMap(environment)
	for key, value := range exported {
		values[key] = value
	}
	return environmentSlice(values)
}

func resolveArchivePaths(executable string) (archivePaths, error) {
	if !filepath.IsAbs(executable) {
		return archivePaths{}, errors.New("pixie executable path must be absolute")
	}
	executable = filepath.Clean(executable)
	if err := validateRegularExecutable(executable); err != nil {
		return archivePaths{}, errors.New("pixie executable is not a regular executable")
	}
	root := filepath.Dir(executable)
	bun, err := archiveRelativeRuntimeExecutable(root, "runtime/bin/bun")
	if err != nil {
		return archivePaths{}, err
	}
	cli, err := archiveRelativeFile(root, "runtime/node_modules/@earendil-works/pi-coding-agent/dist/bun/cli.js")
	if err != nil {
		return archivePaths{}, err
	}
	assistant, err := archiveRelativeFile(root, "libexec/pixie_assistant.js")
	if err != nil {
		return archivePaths{}, err
	}
	piPackage, err := archiveRelativeDirectory(root, "runtime/node_modules/@earendil-works/pi-coding-agent")
	if err != nil {
		return archivePaths{}, err
	}
	return archivePaths{bun: bun, cli: cli, assistant: assistant, piPackage: piPackage}, nil
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

func archiveRelativeDirectory(root, relative string) (string, error) {
	return archiveRelativePath(root, relative, true)
}

// archiveRelativePath rejects traversal and symlinked archive components before
// invoking a private archive payload.
func archiveRelativePath(root, relative string, directory bool) (string, error) {
	if !filepath.IsAbs(root) || filepath.IsAbs(relative) {
		return "", errors.New("archive payload path is invalid")
	}
	root = filepath.Clean(root)
	info, err := os.Lstat(root)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
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
	return current, nil
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

func bundledPiVersion(piPackage string) (string, error) {
	raw, err := os.ReadFile(filepath.Join(piPackage, "package.json"))
	if err != nil {
		return "", errors.New("bundled Pi package manifest is unavailable")
	}
	var manifest struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return "", errors.New("bundled Pi package manifest is invalid")
	}
	if manifest.Name != piPackageName || manifest.Version != piPackageVersion {
		return "", fmt.Errorf("bundled Pi must be %s@%s", piPackageName, piPackageVersion)
	}
	return manifest.Version, nil
}

// isPiSelfUpdate mirrors Pi 0.85.1's update-target rules only far enough to
// reject requests that would replace Pi itself. Extension and model updates are
// native Pi operations and remain untouched.
func isPiSelfUpdate(args []string) bool {
	if len(args) == 0 || args[0] != "update" {
		return false
	}
	var source string
	self, extensions, models, all, help, extensionTarget := false, false, false, false, false, false
	for index := 1; index < len(args); index++ {
		switch args[index] {
		case "-h", "--help":
			help = true
		case "--self":
			self = true
		case "--extensions":
			extensions = true
		case "--models":
			models = true
		case "--all":
			all = true
		case "--extension":
			extensionTarget = true
			if index+1 < len(args) {
				index++
			}
		default:
			if !strings.HasPrefix(args[index], "-") && source == "" {
				source = args[index]
			}
		}
	}
	if help || models || extensionTarget {
		return false
	}
	if source != "" {
		return source == "self" || source == "pi"
	}
	return self || all || !extensions
}
