package persist

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// MigrationInventoryVersion versions the additive STATE-01/MIG-01 inventory
// helpers. It is independent of every ledger schema version it reports.
const MigrationInventoryVersion = 1

// Handling classifies the required MIG-01 treatment for one inventory row.
type Handling string

const (
	HandlingPreserve     Handling = "preserve"
	HandlingConvert      Handling = "convert"
	HandlingRebuildCache Handling = "rebuild-cache"
	HandlingRetire       Handling = "explicitly-retire"
)

// InventoryEntry assigns one concrete file, schema or browser-storage key to
// its owner and required handling. Unknown files are never listed here; they
// stay untouched and are reported separately by InventoryDataDir.
type InventoryEntry struct {
	File     string
	Owner    string
	Handling Handling
	Schema   string
	Note     string
}

// KnownControllerInventory enumerates the concrete persistence in scope for
// STATE-01/MIG-01 from the implementation checkout. Location does not
// determine ownership: native files beneath the agent directory stay Pi-owned.
func KnownControllerInventory() []InventoryEntry {
	return []InventoryEntry{
		{File: "native sessions/**/*.jsonl", Owner: "Pi", Handling: HandlingPreserve, Schema: "native", Note: "No migration rewrite, move, truncation or normalization; retain native IDs, branches, summaries and unknown/custom records."},
		{File: "native auth/settings/models/resources/packages/trust", Owner: "Pi/operator", Handling: HandlingPreserve, Schema: "native", Note: "Not migration targets; no credential copy into the controller and no automatic trust change."},
		{File: "native agent-definition Markdown", Owner: "Operator", Handling: HandlingPreserve, Schema: "native", Note: "Preserve; migration is not an authoring or cleanup request."},
		{File: "agentDir/pixie/identity.json", Owner: "Pixie assistant", Handling: HandlingPreserve, Schema: "assistant/v1", Note: "Validate and preserve stable identity for the same native storage; never confuse with a fresh boot epoch."},
		{File: "agentDir/pixie/sessions.json", Owner: "Pixie assistant sidecar", Handling: HandlingConvert, Schema: "assistant/v1", Note: "Import validated archive/parent/catalog associations; cross-check native file identity/cwd without synthesizing missing transcripts."},
		{File: "agentDir/mcp.json and mcp-sessions.json", Owner: "Pixie-managed MCP where exact schema matches", Handling: HandlingConvert, Schema: "legacy-mcp", Note: "Preserve host-side secrets, connection intent and session membership; native {mcpServers: ...} is not a legacy map to convert; reject schema collisions."},
		{File: "config.json", Owner: "Pixie controller", Handling: HandlingPreserve, Schema: "unversioned object", Note: "Preserve IDs, admitted roots, user settings and model visibility; partial objects normalize per-field."},
		{File: "projects.json", Owner: "Pixie controller", Handling: HandlingPreserve, Schema: "array, single-root normalized", Note: "Preserve IDs, admitted roots and closed state; legacy multi-root entries split only through the coordinated migration journal."},
		{File: "pi-project-sessions.json", Owner: "Pixie controller", Handling: HandlingConvert, Schema: "v2/pi", Note: "Preserve session associations including parentSessionId, cwd and title; make project membership optional only where contracts permit it."},
		{File: "extensions/session-objectives/*.json", Owner: "Pixie controller", Handling: HandlingConvert, Schema: "v2/pi", Note: "Preserve goal/tasks/updatedAt per session; legacy extensions/session-goals/*.json v1 converts only on exact workspace/session match."},
		{File: "pi-session-queues.json", Owner: "Durable delivery authority", Handling: HandlingPreserve, Schema: "v1/pi", Note: "Preserve mutation/delivery IDs, attempted/accepted/uncertain claims and paused work; never restore an older runnable queue over dispatched effects."},
		{File: "schedules.json", Owner: "Schedules and execution ledger", Handling: HandlingPreserve, Schema: "v1", Note: "Preserve IDs, timezone, occurrence/run identities, native session links, replay records and interrupted/uncertain claims; migration does not dispatch missed jobs."},
		{File: "pi-session-deletions.json", Owner: "Deletion authority", Handling: HandlingPreserve, Schema: "v1/pi", Note: "Preserve requested/confirmed phases and tombstones; verify old binding before migration, otherwise retain recovery-blocked state."},
		{File: "project-root-migration.json", Owner: "Migration journal", Handling: HandlingConvert, Schema: "v1/prepared|migrating", Note: "Repeatable staged journal; re-running identical inputs is idempotent, changed inputs conflict; unresolved blocks completion."},
		{File: "mcp-modules.json", Owner: "Pixie modules", Handling: HandlingPreserve, Schema: "map", Note: "Preserve complete desired state including unknown entries without executing them; reconcile readiness independently."},
		{File: "browser-panels-*.json", Owner: "Browser/controller", Handling: HandlingPreserve, Schema: "v1", Note: "Revalidate transient handles on startup; saved IDs do not prove an old process or lease still exists."},
		{File: "browser/state and browser/artifacts", Owner: "Browser/controller", Handling: HandlingRebuildCache, Schema: "derived", Note: "Classify regenerable cache separately from retained panel/lease data; unavailable workers do not authorize source deletion."},
		{File: "browser-storage layout preferences and drafts", Owner: "Client presentation", Handling: HandlingConvert, Schema: "browser-storage", Note: "Map valid chat/file/diff/Browser tabs into independent selections; preserve unsent text separately from stale layout IDs."},
		{File: "canvas/design documents when introduced", Owner: "Respective module", Handling: HandlingPreserve, Schema: "versioned", Note: "Preserve durable source and deletion generations; rebuild or evict only declared derived data."},
	}
}

// FileFingerprint is a redacted content identity for dry-run and receipt use.
// It carries a hash, never file bytes, credentials or session text.
type FileFingerprint struct {
	Name string
	Size int64
	Perm os.FileMode
	Hash string
}

// FingerprintFile hashes one regular file without changing it. It is read-only
// and never follows the fail-open backup fallback: callers decide how to treat
// a missing primary.
func FingerprintFile(path string) (FileFingerprint, error) {
	raw, mode, err := ReadFile(path)
	if err != nil {
		return FileFingerprint{}, err
	}
	digest := sha256.Sum256(raw)
	return FileFingerprint{Name: filepath.Base(path), Size: int64(len(raw)), Perm: mode, Hash: hex.EncodeToString(digest[:])}, nil
}

// ObservedFile describes one data-dir file seen by a read-only inventory pass.
type ObservedFile struct {
	Name          string
	Present       bool
	Size          int64
	Perm          os.FileMode
	Hash          string
	Version       *int
	Engine        *string
	BackupPresent bool
	Unknown       bool
}

// InventoryReport is the read-only result of InventoryDataDir. Unknown lists
// files that must stay untouched; no wildcard cleanup is authorized.
type InventoryReport struct {
	Dir      string
	Files    []ObservedFile
	Unknown  []string
	Warnings []string
}

var knownDataFiles = []string{
	"config.json",
	"projects.json",
	"pi-project-sessions.json",
	"pi-session-queues.json",
	"schedules.json",
	"pi-session-deletions.json",
	"project-root-migration.json",
	"mcp-modules.json",
}

func peekVersionEngine(raw []byte) (*int, *string) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil || object == nil {
		return nil, nil
	}
	var version *int
	var engine *string
	var versionValue int
	if rawVersion, ok := object["version"]; ok && json.Unmarshal(rawVersion, &versionValue) == nil {
		version = &versionValue
	}
	var schemaValue int
	if version == nil {
		if rawSchema, ok := object["schemaVersion"]; ok && json.Unmarshal(rawSchema, &schemaValue) == nil {
			version = &schemaValue
		}
	}
	var engineValue string
	if rawEngine, ok := object["engine"]; ok && json.Unmarshal(rawEngine, &engineValue) == nil {
		engine = &engineValue
	}
	return version, engine
}

func observeKnownFile(dir, name string, unknown bool) ObservedFile {
	observed := ObservedFile{Name: name, Unknown: unknown}
	path := filepath.Join(dir, name)
	if _, err := os.Stat(path + ".bak"); err == nil {
		observed.BackupPresent = true
	}
	raw, mode, err := ReadFile(path)
	if err != nil {
		return observed
	}
	observed.Present = true
	observed.Size = int64(len(raw))
	observed.Perm = mode
	digest := sha256.Sum256(raw)
	observed.Hash = hex.EncodeToString(digest[:])
	observed.Version, observed.Engine = peekVersionEngine(raw)
	return observed
}

// InventoryDataDir inspects a controller data directory with no writes,
// package loading, extension execution, model calls or native configuration
// changes. Unknown source files are reported, never removed. Tombstones live
// inside the deletion ledger and are preserved by the controller inspectors;
// this pass never deletes a primary, a backup or a transient.
func InventoryDataDir(dir string) (InventoryReport, error) {
	report := InventoryReport{Dir: dir}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return report, nil
		}
		return report, err
	}
	known := make(map[string]bool, len(knownDataFiles))
	for _, name := range knownDataFiles {
		known[name] = true
	}
	for _, name := range knownDataFiles {
		observed := observeKnownFile(dir, name, false)
		if observed.Present || observed.BackupPresent {
			report.Files = append(report.Files, observed)
		}
		if !observed.Present && observed.BackupPresent {
			report.Warnings = append(report.Warnings, fmt.Sprintf("%s primary is missing while a backup remains; fail closed and do not replay the backup", name))
		}
	}
	matchedPanels, _ := filepath.Glob(filepath.Join(dir, "browser-panels-*.json"))
	for _, path := range matchedPanels {
		observed := observeKnownFile(dir, filepath.Base(path), false)
		report.Files = append(report.Files, observed)
	}
	for _, sub := range []string{filepath.Join("extensions", "session-objectives"), filepath.Join("extensions", "session-goals")} {
		subEntries, err := os.ReadDir(filepath.Join(dir, sub))
		if err != nil {
			continue
		}
		for _, entry := range subEntries {
			if entry.IsDir() {
				continue
			}
			rel := filepath.Join(sub, entry.Name())
			observed := observeKnownFile(dir, rel, false)
			if strings.HasSuffix(entry.Name(), ".bak") || strings.HasSuffix(entry.Name(), ".tmp") {
				continue
			}
			report.Files = append(report.Files, observed)
		}
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if known[name] || strings.HasSuffix(name, ".bak") || strings.HasSuffix(name, ".tmp") {
			continue
		}
		if matched, _ := filepath.Match("browser-panels-*.json", name); matched {
			continue
		}
		report.Unknown = append(report.Unknown, name)
	}
	sort.Strings(report.Unknown)
	sort.Slice(report.Files, func(i, j int) bool { return report.Files[i].Name < report.Files[j].Name })
	if len(report.Unknown) > 0 {
		report.Warnings = append(report.Warnings, fmt.Sprintf("%d unknown file(s) preserved untouched; no wildcard cleanup authorized", len(report.Unknown)))
	}
	return report, nil
}

// MigrationPhase names one repeatable staged-conversion phase. Re-running
// identical inputs in the same phase is idempotent; changed inputs conflict
// instead of being merged opportunistically.
type MigrationPhase string

const (
	PhasePrepared  MigrationPhase = "prepared"
	PhaseMigrating MigrationPhase = "migrating"
)

// ValidateMigrationPhase rejects unknown phases without touching storage.
func ValidateMigrationPhase(phase MigrationPhase) error {
	if phase != PhasePrepared && phase != PhaseMigrating {
		return fmt.Errorf("invalid migration phase")
	}
	return nil
}

// StagedPlan is a dry-run conversion description with no writes. It records
// source identity, input hashes, target schema and phase so staged files can
// be validated and published through explicit checkpoints.
type StagedPlan struct {
	SourceDir      string
	TargetDir      string
	SourceIdentity string
	InputHashes    map[string]string
	TargetSchema   string
	Phase          MigrationPhase
	Conversions    []string
	Conflicts      []string
	Backups        []string
}

// NewStagedPlan copies its inputs so later caller mutation cannot change the plan.
func NewStagedPlan(sourceDir, targetDir, sourceIdentity, targetSchema string, inputHashes map[string]string, phase MigrationPhase) StagedPlan {
	copied := make(map[string]string, len(inputHashes))
	for key, value := range inputHashes {
		copied[key] = value
	}
	return StagedPlan{SourceDir: sourceDir, TargetDir: targetDir, SourceIdentity: sourceIdentity, InputHashes: copied, TargetSchema: targetSchema, Phase: phase}
}

// InputsUnchanged reports whether re-running with current hashes is a safe
// idempotent repeat of the same plan.
func (plan StagedPlan) InputsUnchanged(current map[string]string) bool {
	if len(plan.InputHashes) != len(current) {
		return false
	}
	for key, value := range plan.InputHashes {
		if current[key] != value {
			return false
		}
	}
	return true
}

// DetectInputChange conflicts instead of merging when staged inputs changed
// after the plan was prepared.
func (plan StagedPlan) DetectInputChange(current map[string]string) error {
	if plan.InputsUnchanged(current) {
		return nil
	}
	return fmt.Errorf("migration inputs changed after planning; re-inspect instead of merging opportunistically")
}

// MigrationReceipt names the schemas, staged files, retained post-backup
// effects and unresolved work for one staged conversion.
type MigrationReceipt struct {
	Plan            StagedPlan
	CompletedPhase  MigrationPhase
	OutputHashes    map[string]string
	RetainedEffects []string
	Unresolved      []string
}

// Validate checks the receipt shape without touching storage.
func (receipt MigrationReceipt) Validate() error {
	if err := ValidateMigrationPhase(receipt.Plan.Phase); err != nil {
		return err
	}
	if err := ValidateMigrationPhase(receipt.CompletedPhase); err != nil {
		return err
	}
	if receipt.Plan.SourceIdentity == "" || receipt.Plan.TargetSchema == "" {
		return fmt.Errorf("migration receipt requires source identity and target schema")
	}
	return nil
}
