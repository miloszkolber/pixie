package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	controller "github.com/miloszkolber/pixie/internal/controller"
	"github.com/miloszkolber/pixie/internal/persist"
)

// pairingSecretWarning is the operator instruction printed with every freshly
// issued ceremony secret. The raw secret exists only in memory and on stdout;
// only its verifier hash is durable.
const pairingSecretWarning = "store this secret out of band now; it is shown once and never stored in plaintext"

// pairingOutput is the machine-readable ceremony result. It intentionally
// carries no verifier hash: only the issued secret (once) plus durable status.
type pairingOutput struct {
	Status             string `json:"status"`
	AuthorityBindingID string `json:"authorityBindingId"`
	HostIdentity       string `json:"hostIdentity"`
	StorageKey         string `json:"storageKey"`
	Generation         uint64 `json:"generation"`
	Secret             string `json:"secret,omitempty"`
	Warning            string `json:"warning,omitempty"`
}

// pairingOptions carries one ceremony invocation before resolution.
type pairingOptions struct {
	configPath   string
	dataDir      string
	agentDir     string
	hostIdentity string
	storageKey   string
	secret       string
	json         bool
}

// handlePairingCommand dispatches the operator pairing ceremony when the first
// argument selects one. It is shared by the full-host and controller-only
// entrypoints so both builds expose the same commands.
func handlePairingCommand(args []string, stdout io.Writer) (bool, error) {
	if len(args) == 0 {
		return false, nil
	}
	switch args[0] {
	case "pair", "revoke-pairing", "rotate-pairing":
	default:
		return false, nil
	}
	return true, runPairingCommand(args[0], args[1:], stdout)
}

func runPairingCommand(command string, args []string, stdout io.Writer) error {
	options, err := parsePairingOptions(command, args)
	if err != nil {
		return err
	}
	store, hostIdentity, storageKey, err := resolvePairingInputs(options)
	if err != nil {
		return err
	}
	switch command {
	case "pair":
		secret := strings.TrimSpace(options.secret)
		if secret == "" {
			secret, err = persist.GeneratePairingSecret()
			if err != nil {
				return err
			}
		}
		pairing, err := controller.PairAuthority(store, hostIdentity, storageKey, secret)
		if err != nil {
			return fmt.Errorf("pair: %w", err)
		}
		return writePairingOutput(stdout, options.json, command, pairingOutput{
			Status:             pairing.Status,
			AuthorityBindingID: pairing.AuthorityBindingID,
			HostIdentity:       pairing.HostIdentity,
			StorageKey:         pairing.StorageKey,
			Generation:         pairing.Generation,
			Secret:             secret,
			Warning:            pairingSecretWarning,
		})
	case "revoke-pairing":
		pairing, err := controller.RevokePairingAuthority(store)
		if err != nil {
			return fmt.Errorf("revoke-pairing: %w", err)
		}
		return writePairingOutput(stdout, options.json, command, pairingOutput{
			Status:             pairing.Status,
			AuthorityBindingID: pairing.AuthorityBindingID,
			HostIdentity:       pairing.HostIdentity,
			StorageKey:         pairing.StorageKey,
			Generation:         pairing.Generation,
		})
	case "rotate-pairing":
		current := strings.TrimSpace(options.secret)
		if current == "" {
			return errors.New("rotate-pairing requires --secret with the current pairing secret")
		}
		newSecret, err := persist.GeneratePairingSecret()
		if err != nil {
			return err
		}
		pairing, err := controller.RotatePairingSecret(store, current, newSecret)
		if err != nil {
			return fmt.Errorf("rotate-pairing: %w", err)
		}
		return writePairingOutput(stdout, options.json, command, pairingOutput{
			Status:             pairing.Status,
			AuthorityBindingID: pairing.AuthorityBindingID,
			HostIdentity:       pairing.HostIdentity,
			StorageKey:         pairing.StorageKey,
			Generation:         pairing.Generation,
			Secret:             newSecret,
			Warning:            pairingSecretWarning,
		})
	default:
		return fmt.Errorf("unknown pairing command %q", command)
	}
}

func parsePairingOptions(command string, args []string) (pairingOptions, error) {
	var options pairingOptions
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&options.configPath, "config", "", "absolute path to a Pixie JSON configuration")
	flags.StringVar(&options.dataDir, "data-dir", "", "state directory holding the pairing record")
	flags.StringVar(&options.agentDir, "agent-dir", "", "absolute Pi agent directory")
	flags.StringVar(&options.hostIdentity, "host-identity", "", "explicit durable host identity")
	flags.StringVar(&options.storageKey, "storage-key", "", "explicit pairing storage key")
	flags.StringVar(&options.secret, "secret", "", "pairing secret")
	flags.BoolVar(&options.json, "json", false, "emit machine-readable output")
	if err := flags.Parse(args); err != nil {
		return pairingOptions{}, fmt.Errorf("%s: %w", command, err)
	}
	if flags.NArg() != 0 {
		return pairingOptions{}, fmt.Errorf("%s: unexpected argument %q", command, flags.Arg(0))
	}
	return options, nil
}

// resolvePairingInputs applies the documented resolution order. The store is
// always resolved; the identity pair comes from the explicit overrides, with
// any still-missing value derived from the selected agent directory.
func resolvePairingInputs(options pairingOptions) (persist.Store, string, string, error) {
	config, err := decodeRuntimeConfig(options.configPath)
	if err != nil {
		return persist.Store{}, "", "", err
	}
	dataDir := expandHomePath(options.dataDir)
	if dataDir == "" {
		dataDir = config.DataDir
	}
	if dataDir == "" {
		dataDir = pairingDefaultDataDir()
	}
	agentDir := expandHomePath(options.agentDir)
	if agentDir == "" {
		agentDir = config.AgentDir
	}
	hostIdentity := normalizePairingHostIdentity(options.hostIdentity)
	storageKey := strings.TrimSpace(options.storageKey)
	if hostIdentity == "" || storageKey == "" {
		if agentDir == "" {
			return persist.Store{}, "", "", errors.New("pairing requires --agent-dir or explicit --host-identity and --storage-key")
		}
		if !filepath.IsAbs(agentDir) {
			return persist.Store{}, "", "", errors.New("--agent-dir must be an absolute path")
		}
		if storageKey == "" {
			storageKey, err = persist.DerivePairingStorageKey(agentDir)
			if err != nil {
				return persist.Store{}, "", "", err
			}
		}
		if hostIdentity == "" {
			hostIdentity, err = readAgentHostIdentity(agentDir)
			if err != nil {
				return persist.Store{}, "", "", err
			}
		}
	}
	return persist.Store{Dir: dataDir}, hostIdentity, storageKey, nil
}

// normalizePairingHostIdentity accepts either the raw RuntimeID or the full
// AgentProfile identity and always yields the full `pi:` form.
func normalizePairingHostIdentity(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || strings.HasPrefix(value, "pi:") {
		return value
	}
	return "pi:" + value
}

// readAgentHostIdentity reads the stable host identity the Go host persists in
// the selected agent directory and returns the AgentProfile form.
func readAgentHostIdentity(agentDir string) (string, error) {
	path := filepath.Join(agentDir, "pixie", "host-identity.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read host identity %s: %w", path, err)
	}
	var record struct {
		Version  int    `json:"version"`
		Identity string `json:"identity"`
	}
	if err := json.Unmarshal(raw, &record); err != nil {
		return "", fmt.Errorf("host identity %s is not valid JSON", path)
	}
	if record.Version != 1 || !validPairingIdentityHex(record.Identity) {
		return "", fmt.Errorf("host identity %s is invalid: expected version 1 with a 32-character lowercase hex identity", path)
	}
	return "pi:" + record.Identity, nil
}

func validPairingIdentityHex(value string) bool {
	if len(value) != 32 {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

// pairingDefaultDataDir mirrors the controller's last-resort state directory so
// the ceremony writes where the running controller reads.
func pairingDefaultDataDir() string {
	if xdg := strings.TrimSpace(os.Getenv("XDG_DATA_HOME")); filepath.IsAbs(xdg) {
		return filepath.Join(xdg, "pixie")
	}
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		return filepath.Join(home, ".local", "share", "pixie")
	}
	return controller.DefaultDataDir
}

func writePairingOutput(stdout io.Writer, asJSON bool, action string, output pairingOutput) error {
	if asJSON {
		encoded, err := json.Marshal(output)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(stdout, "%s\n", encoded)
		return err
	}
	switch action {
	case "pair", "rotate-pairing":
		secretLabel := "secret"
		if action == "rotate-pairing" {
			secretLabel = "new secret"
		}
		if _, err := fmt.Fprintf(stdout, "pixie %s: %s deletion authority %s (generation %d)\n  host:    %s\n  storage: %s\n", action, output.Status, output.AuthorityBindingID, output.Generation, output.HostIdentity, output.StorageKey); err != nil {
			return err
		}
		_, err := fmt.Fprintf(stdout, "Store this pairing %s out of band now. It is shown once and never stored in plaintext:\n%s\n", secretLabel, output.Secret)
		return err
	case "revoke-pairing":
		_, err := fmt.Fprintf(stdout, "pixie revoke-pairing: revoked deletion authority %s (generation %d)\ndestructive recovery stays blocked until an explicit `pixie pair` with the verified host and storage\n", output.AuthorityBindingID, output.Generation)
		return err
	default:
		return fmt.Errorf("unknown pairing command %q", action)
	}
}
