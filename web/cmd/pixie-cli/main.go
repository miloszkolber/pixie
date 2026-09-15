// pixie_cli starts the bundled assistant host. It does not expose a second Pi
// CLI namespace; the archive's root pixie command remains upstream Pi.
package main

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/miloszkolber/pixie/cmd/internal/assistantconfig"
	"github.com/miloszkolber/pixie/cmd/internal/processgroup"
	"github.com/miloszkolber/pixie/cmd/internal/runtimeexec"
	"github.com/miloszkolber/pixie/internal/ownerlock"
)

const bundledPiPackageEnvironment = "PIXIE_BUNDLED_PI_PACKAGE"

// Release identity, injected with
// `-X main.version=<release-id> -X main.revision=<source-commit>` so the host
// launcher can report its archive identity without taking over the root `pixie`
// command namespace. An unstamped dev build reports the defaults below.
var (
	version  = "0.0.0-dev"
	revision = "unknown"
)

type arguments struct {
	configPath string
}

type archivePaths struct {
	bun       string
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
	parsed, err := parseArguments(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, usage())
		if err.Error() != "" {
			fmt.Fprintf(os.Stderr, "pixie_cli: %s\n", err)
		}
		os.Exit(2)
	}
	code, err := run(parsed, os.Environ())
	if err != nil {
		fmt.Fprintf(os.Stderr, "pixie_cli: %s\n", err)
		os.Exit(ownerlock.ExitCode(err))
	}
	if code != 0 {
		os.Exit(code)
	}
}

func usage() string { return "usage: pixie_cli serve --config ABS" }

// versionLine is the stable `--version` contract the release evidence producer
// matches against; root `pixie --version` remains the transparent Pi version.
func versionLine() string {
	return fmt.Sprintf("pixie_cli %s (revision %s)", version, revision)
}

func parseArguments(values []string) (arguments, error) {
	if len(values) == 0 {
		return arguments{}, usageError{}
	}
	if values[0] != "serve" {
		// A launcher without a `doctor`/other command reports it as unknown so
		// the evidence producer records a blocked probe rather than a failure.
		return arguments{}, usageError{
			message: fmt.Sprintf("unknown command %q; use `serve --config ABS`", values[0]),
		}
	}
	if len(values) != 3 || values[1] != "--config" {
		return arguments{}, usageError{message: "use `serve --config ABS`"}
	}
	configPath := strings.TrimSpace(values[2])
	if configPath == "" || !filepath.IsAbs(configPath) {
		return arguments{}, usageError{message: "--config must be an absolute path"}
	}
	return arguments{configPath: configPath}, nil
}

func run(parsed arguments, inherited []string) (int, error) {
	parent := environmentMap(inherited)
	if err := rejectPublicPiSelection(parent); err != nil {
		return 0, err
	}
	config, err := assistantconfig.Load(parsed.configPath)
	if err != nil {
		return 0, err
	}
	lookup := func(key string) (string, bool) { value, ok := parent[key]; return value, ok }
	agentDir, err := ownerlock.ResolveAgentDir(config.AgentDir, lookup)
	if err != nil {
		return 0, err
	}
	lock, err := ownerlock.Acquire(agentDir, "pixie_cli")
	if err != nil {
		return 0, err
	}
	defer lock.Close()
	executable, err := os.Executable()
	if err != nil {
		return 0, errors.New("could not resolve the pixie_cli executable")
	}
	paths, err := resolveArchivePaths(executable)
	if err != nil {
		return 0, err
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(signals)
	code, err := processgroup.Run(assistantInvocation(paths, parsed, parent), signals)
	if err != nil && code == 0 {
		return 0, errors.New("could not run bundled pixie_assistant")
	}
	return code, nil
}

func assistantInvocation(paths archivePaths, parsed arguments, parent map[string]string) processgroup.Invocation {
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
		return errors.New("PIXIE_PI_PACKAGE is not accepted; pixie_cli always uses its bundled Pi archive")
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

func resolveArchivePaths(executable string) (archivePaths, error) {
	if !filepath.IsAbs(executable) {
		return archivePaths{}, errors.New("pixie_cli executable path must be absolute")
	}
	executable = filepath.Clean(executable)
	if err := validateRegularExecutable(executable); err != nil {
		return archivePaths{}, errors.New("pixie_cli executable is not a regular executable")
	}
	root := filepath.Dir(executable)
	bun, err := archiveRelativeRuntimeExecutable(root, "runtime/bin/bun")
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
	return archivePaths{bun: bun, assistant: assistant, piPackage: piPackage}, nil
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

func archiveRelativePath(root, relative string, directory bool) (string, error) {
	if !filepath.IsAbs(root) || filepath.IsAbs(relative) {
		return "", errors.New("archive payload path is invalid")
	}
	info, err := os.Lstat(root)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", errors.New("archive root is invalid")
	}
	current := filepath.Clean(root)
	parts := strings.FieldsFunc(relative, func(character rune) bool {
		return character == filepath.Separator || character == '/'
	})
	for index, part := range parts {
		if part == "" || part == "." || part == ".." {
			return "", errors.New("archive payload path is invalid")
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("archive payload is unavailable")
		}
		if index != len(parts)-1 || directory {
			if !info.IsDir() {
				return "", errors.New("archive payload is invalid")
			}
		}
	}
	if len(parts) == 0 {
		return "", errors.New("archive payload path is invalid")
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
