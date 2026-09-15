// pixie is the archive-local native Pi command. It deliberately delegates to
// upstream Pi rather than growing a Pixie command namespace.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/miloszkolber/pixie/cmd/internal/processgroup"
	"github.com/miloszkolber/pixie/cmd/internal/runtimeexec"
	"github.com/miloszkolber/pixie/internal/ownerlock"
)

const (
	piPackageName    = "@earendil-works/pi-coding-agent"
	piPackageVersion = "0.85.1"
)

type archivePaths struct {
	bun       string
	cli       string
	piPackage string
}

func main() {
	code, err := run(os.Args[1:], os.Environ())
	if err != nil {
		fmt.Fprintf(os.Stderr, "pixie: %s\n", err)
		os.Exit(ownerlock.ExitCode(err))
	}
	if code != 0 {
		os.Exit(code)
	}
}

func run(args, environment []string) (int, error) {
	executable, err := os.Executable()
	if err != nil {
		return 0, errors.New("could not resolve the pixie executable")
	}
	paths, err := resolveArchivePaths(executable)
	if err != nil {
		return 0, err
	}
	version, err := bundledPiVersion(paths.piPackage)
	if err != nil {
		return 0, err
	}
	if len(args) == 1 && args[0] == "--version" {
		fmt.Fprintln(os.Stdout, version)
		return 0, nil
	}
	if isPiSelfUpdate(args) {
		return 0, errors.New("Pi self-update is disabled in Pixie archives; update the Pixie archive through its installer or package manager, then restart Pixie")
	}
	lookup := environmentLookup(environment)
	agentDir, err := ownerlock.ResolveAgentDir("", lookup)
	if err != nil {
		return 0, err
	}
	lock, err := ownerlock.Acquire(agentDir, "pixie")
	if err != nil {
		return 0, err
	}
	defer lock.Close()

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(signals)
	code, err := processgroup.Run(piInvocation(paths, args), signals)
	if err != nil && code == 0 {
		return 0, errors.New("could not run bundled Pi")
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
	environment := make(map[string]string, len(values))
	for _, value := range values {
		key, entry, ok := strings.Cut(value, "=")
		if ok && key != "" {
			environment[key] = entry
		}
	}
	return func(key string) (string, bool) {
		value, ok := environment[key]
		return value, ok
	}
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
	piPackage, err := archiveRelativeDirectory(root, "runtime/node_modules/@earendil-works/pi-coding-agent")
	if err != nil {
		return archivePaths{}, err
	}
	return archivePaths{bun: bun, cli: cli, piPackage: piPackage}, nil
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
