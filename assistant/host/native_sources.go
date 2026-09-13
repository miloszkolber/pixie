package host

// This file owns the filesystem-backed defined-agent Markdown authoring
// operations (pi.sources.list/create/update/delete and
// pi.agent-mentions.list). They are not Pi RPC: the contract is the same native
// Markdown files the selected Pi reads itself, at
// <agentDir>/agents/*.md (global) and <project>/.pi/agents/*.md (project).
// The behavior preserves the agent-authoring contract the controller depends
// on so the web agent editor keeps working without duplicating any execution
// engine. All mutations are bounded, validated, atomic and revision-checked;
// unknown records are preserved and unrelated files are never touched.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

const (
	agentDocumentMaxBytes = 65536
	agentReadProbeBytes   = agentDocumentMaxBytes + 1
	agentNameMaxBytes     = 80
	agentRevisionMaxBytes = 128
)

// piAgentDefinition is the wire projection of one native agent Markdown file.
// Field names match the legacy Definition and the controller's
// parseAgentSource contract in web/internal/controller/pi_agents.go.
type piAgentDefinition struct {
	Type                 string         `json:"type"`
	Path                 string         `json:"path"`
	Name                 string         `json:"name"`
	Description          string         `json:"description"`
	Content              string         `json:"content"`
	Revision             string         `json:"revision"`
	Global               bool           `json:"global"`
	Writable             bool           `json:"writable"`
	ExecutionEligibility string         `json:"executionEligibility"`
	Properties           map[string]any `json:"properties"`
}

type agentDirectory struct {
	path   string
	global bool
}

// agentDirectories returns the global agent directory followed by the optional
// project directory, in the legacy order.
func (s *nativeSupervisor) agentDirectories(projectRoot string) []agentDirectory {
	directories := []agentDirectory{{path: filepath.Join(s.config.AgentDir, "agents"), global: true}}
	if projectRoot != "" {
		directories = append(directories, agentDirectory{path: filepath.Join(projectRoot, ".pi", "agents"), global: false})
	}
	return directories
}

// agentProjectRoot selects the project directory supplied for one operation.
// pi.sources.* admin calls carry projectDir; mention discovery carries cwd.
// Both are absolute in normal controller use; other values fail closed the same
// way the legacy realpath check skipped a non-rooted project directory.
func agentProjectRoot(params map[string]any) string {
	if projectDir, _ := params["projectDir"].(string); projectDir != "" {
		return projectDir
	}
	if cwd, _ := params["cwd"].(string); cwd != "" {
		return cwd
	}
	return ""
}

// listAgentSources mirrors the legacy list(): each directory is resolved and
// accepted only when it is its own real path, every .md file is read through a
// 65537-byte probe and re-verified, and malformed or escaping files degrade to
// a warning rather than failing the whole list. Unexpected directory failures
// stay fatal, as in the oracle.
func (s *nativeSupervisor) listAgentSources(projectRoot string) ([]piAgentDefinition, []string, error) {
	result := make([]piAgentDefinition, 0)
	warnings := make([]string, 0)
	for _, directory := range s.agentDirectories(projectRoot) {
		root, err := filepath.EvalSymlinks(directory.path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, nil, err
		}
		if root != directory.path {
			continue
		}
		entries, err := os.ReadDir(directory.path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, nil, err
		}
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".md") {
				names = append(names, entry.Name())
			}
		}
		sort.Strings(names)
		for _, name := range names {
			path := filepath.Join(directory.path, name)
			resolved, err := filepath.EvalSymlinks(path)
			if err != nil {
				warnings = append(warnings, "Cannot load agent: "+path)
				continue
			}
			if filepath.Dir(resolved) != root {
				continue
			}
			definition, err := readAgentDefinition(path, directory.global, root)
			if err != nil {
				warnings = append(warnings, "Cannot load agent: "+path)
				continue
			}
			result = append(result, definition)
		}
	}
	return result, warnings, nil
}

// readAgentDefinition reads and validates one .md file. The caller already
// confirmed the path resolves inside root; this re-verifies identity after the
// read so a path swapped mid-read is rejected rather than loaded.
func readAgentDefinition(path string, global bool, root string) (piAgentDefinition, error) {
	raw, err := readBoundedAgentFile(path)
	if err != nil {
		return piAgentDefinition{}, err
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || resolved != path || filepath.Dir(resolved) != root {
		return piAgentDefinition{}, errors.New("agent path changed while reading")
	}
	metadata, content, err := parseAgentDocument(raw)
	if err != nil {
		return piAgentDefinition{}, err
	}
	return piAgentDefinition{
		Type:                 "agent",
		Path:                 path,
		Name:                 agentTextOr(metadata["name"], strings.TrimSuffix(filepath.Base(path), ".md")),
		Description:          agentTextOr(metadata["description"], ""),
		Content:              content,
		Revision:             agentRevision(raw),
		Global:               global,
		Writable:             true,
		ExecutionEligibility: "unknown",
		Properties:           metadata,
	}, nil
}

// readBoundedAgentFile reads at most one byte more than the document bound so
// an oversized file is detected instead of silently truncated.
func readBoundedAgentFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, agentReadProbeBytes))
	if err != nil {
		return "", err
	}
	if len(data) > agentDocumentMaxBytes {
		return "", errors.New("Agent exceeds 65536 bytes")
	}
	return string(data), nil
}

var agentFrontmatterPattern = regexp.MustCompile(`(?s)^---\r?\n(.*?)\r?\n---\r?\n?(.*)$`)

// parseAgentDocument splits the required YAML frontmatter from the body. A
// non-object frontmatter document is treated as empty metadata, matching the
// legacy object() projection.
func parseAgentDocument(raw string) (map[string]any, string, error) {
	match := agentFrontmatterPattern.FindStringSubmatch(raw)
	if match == nil {
		return nil, "", errors.New("Missing agent frontmatter")
	}
	metadata := map[string]any{}
	if parsed, err := parseAgentFrontmatter(match[1]); err == nil {
		metadata = parsed
	} else {
		return nil, "", err
	}
	return metadata, match[2], nil
}

// encodeAgentDocument renders properties as YAML frontmatter followed by the
// exact body. The legacy template always terminates the properties block with a
// newline before the closing fence.
func encodeAgentDocument(properties map[string]any, content string) (string, error) {
	encoded, err := marshalAgentFrontmatter(properties)
	if err != nil {
		return "", err
	}
	if encoded != "" && !strings.HasSuffix(encoded, "\n") {
		encoded += "\n"
	}
	return "---\n" + encoded + "---\n" + content, nil
}

func agentRevision(raw string) string {
	digest := sha256.Sum256([]byte(raw))
	return "sha256:" + hex.EncodeToString(digest[:])
}

// findAgentSource locates one listed source by its exact path. Paths are never
// trusted from the request: only paths produced by listAgentSources qualify.
func (s *nativeSupervisor) findAgentSource(projectRoot, path string) (*piAgentDefinition, error) {
	if path == "" {
		return nil, nil
	}
	sources, _, err := s.listAgentSources(projectRoot)
	if err != nil {
		return nil, err
	}
	for index := range sources {
		if sources[index].Path == path {
			return &sources[index], nil
		}
	}
	return nil, nil
}

// saveAgentSource implements pi.sources.create and pi.sources.update. The
// presence of a non-empty path selects update, matching the legacy save().
func (s *nativeSupervisor) saveAgentSource(params map[string]any) (json.RawMessage, error) {
	projectRoot := agentProjectRoot(params)
	rawName, err := requiredAgentText(params["name"], "agent name", agentNameMaxBytes)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(rawName)
	if name == "" || len(name) > agentNameMaxBytes || !validAgentName(name) {
		return nil, errors.New("Invalid agent name")
	}
	target := stringMap(params["target"])
	scope := ""
	if target["scope"] == "projectDir" {
		scope, _ = target["projectDir"].(string)
	}
	requestPath, _ := params["path"].(string)
	updating := requestPath != ""
	var previous map[string]any
	expectedRevision := ""
	path := requestPath
	if updating {
		existing, err := s.findAgentSource(projectRoot, requestPath)
		if err != nil {
			return nil, err
		}
		if existing == nil {
			return nil, errors.New("Unknown agent source")
		}
		expectedRevision, err = requiredAgentText(params["expectedRevision"], "agent revision", agentRevisionMaxBytes)
		if err != nil {
			return nil, err
		}
		if existing.Revision != expectedRevision {
			return nil, errors.New("Agent changed on disk; reload before editing")
		}
		path = existing.Path
		previous = existing.Properties
	} else {
		if _, present := params["expectedRevision"]; present {
			return nil, errors.New("Agent revision is not valid for create")
		}
		directory := filepath.Join(s.config.AgentDir, "agents")
		if scope != "" {
			resolvedScope, err := filepath.EvalSymlinks(scope)
			if err != nil {
				return nil, fmt.Errorf("agent directory is unavailable: %w", err)
			}
			directory = filepath.Join(resolvedScope, ".pi", "agents")
			projectRoot = scope
		}
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return nil, err
		}
		resolvedDirectory, err := filepath.EvalSymlinks(directory)
		if err != nil || resolvedDirectory != directory {
			return nil, errors.New("Agent directory is not rooted")
		}
		path = filepath.Join(directory, name+".md")
		if _, err := filepath.EvalSymlinks(path); err == nil {
			return nil, errors.New("Agent already exists")
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, errors.New("Agent already exists")
		}
	}
	current, err := s.findAgentSource(projectRoot, path)
	if err != nil {
		return nil, err
	}
	if updating {
		if current == nil || current.Revision != expectedRevision {
			return nil, errors.New("Agent changed on disk; reload before editing")
		}
		previous = current.Properties
	} else if current != nil {
		return nil, errors.New("Agent already exists")
	}
	properties := make(map[string]any, len(previous)+len(stringMap(params["properties"]))+2)
	for key, value := range previous {
		properties[key] = value
	}
	for key, value := range stringMap(params["properties"]) {
		properties[key] = value
	}
	properties["name"] = name
	properties["description"] = rawText(params["description"])
	for key, value := range properties {
		if value == nil {
			delete(properties, key)
		}
	}
	document, err := encodeAgentDocument(properties, rawText(params["content"]))
	if err != nil {
		return nil, err
	}
	if len(document) > agentDocumentMaxBytes {
		return nil, errors.New("Agent must fit within 65536 bytes including frontmatter")
	}
	if updating {
		err = atomicWriteAgentFile(path, []byte(document))
	} else {
		err = atomicCreateAgentFile(path, []byte(document))
		if errors.Is(err, os.ErrExist) {
			return nil, errors.New("Agent already exists")
		}
	}
	if err != nil {
		return nil, err
	}
	source, err := s.findAgentSource(projectRoot, path)
	if err != nil {
		return nil, err
	}
	if source == nil {
		return nil, errors.New("Saved agent could not be loaded")
	}
	return json.Marshal(map[string]any{"source": source})
}

// deleteAgentSource implements pi.sources.delete with a double revision check
// matching the oracle: the path must resolve to a listed source and the bytes
// must still carry the expected revision immediately before removal.
func (s *nativeSupervisor) deleteAgentSource(params map[string]any) (json.RawMessage, error) {
	projectRoot := agentProjectRoot(params)
	requestPath, _ := params["path"].(string)
	source, err := s.findAgentSource(projectRoot, requestPath)
	if err != nil {
		return nil, err
	}
	if source == nil {
		return nil, errors.New("Unknown agent source")
	}
	expectedRevision, err := requiredAgentText(params["expectedRevision"], "agent revision", agentRevisionMaxBytes)
	if err != nil {
		return nil, err
	}
	if source.Revision != expectedRevision {
		return nil, errors.New("Agent changed on disk; reload before deleting")
	}
	current, err := s.findAgentSource(projectRoot, source.Path)
	if err != nil {
		return nil, err
	}
	if current == nil || current.Revision != expectedRevision {
		return nil, errors.New("Agent changed on disk; reload before deleting")
	}
	if err := os.Remove(source.Path); err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{"ok": true})
}

// listAgentSourcesOperation implements pi.sources.list.
func (s *nativeSupervisor) listAgentSourcesOperation(params map[string]any) (json.RawMessage, error) {
	sources, warnings, err := s.listAgentSources(agentProjectRoot(params))
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{"sources": sources, "warnings": warnings})
}

// agentMentionsOperation implements pi.agent-mentions.list. Only the fields the
// legacy host served are returned; the controller's projectAgentMentions
// validates sourceType.
func (s *nativeSupervisor) agentMentionsOperation(params map[string]any) (json.RawMessage, error) {
	sources, _, err := s.listAgentSources(agentProjectRoot(params))
	if err != nil {
		return nil, err
	}
	agents := make([]map[string]any, 0, len(sources))
	for _, source := range sources {
		agents = append(agents, map[string]any{
			"name":        source.Name,
			"description": source.Description,
			"sourceType":  "agent",
			"mention":     "@" + source.Name,
		})
	}
	return json.Marshal(map[string]any{"agents": agents})
}

// atomicWriteAgentFile replaces path through a same-directory temporary file and
// rename, fsyncing the file and its directory.
func atomicWriteAgentFile(path string, data []byte) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := writeAndSync(temporary, data); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	return syncDirectory(directory)
}

// atomicCreateAgentFile links a temporary file into place so a concurrent
// creator loses with EEXIST instead of overwriting the winner.
func atomicCreateAgentFile(path string, data []byte) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := writeAndSync(temporary, data); err != nil {
		return err
	}
	if err := os.Link(temporaryPath, path); err != nil {
		return err
	}
	return syncDirectory(directory)
}

func writeAndSync(file *os.File, data []byte) error {
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	return file.Close()
}

func syncDirectory(directory string) error {
	handle, err := os.Open(directory)
	if err != nil {
		return err
	}
	defer handle.Close()
	return handle.Sync()
}

func requiredAgentText(value any, label string, maxBytes int) (string, error) {
	text, _ := value.(string)
	if text == "" || len(text) > maxBytes || strings.ContainsRune(text, 0) {
		return "", fmt.Errorf("Invalid %s", label)
	}
	return text, nil
}

func validAgentName(name string) bool {
	for _, character := range name {
		if !unicode.IsLetter(character) && !unicode.IsNumber(character) && character != ' ' && character != '_' && character != '-' {
			return false
		}
	}
	return true
}

func rawText(value any) string {
	text, _ := value.(string)
	return text
}

func agentTextOr(value any, fallback string) string {
	if text, ok := value.(string); ok && text != "" {
		return text
	}
	return fallback
}

func stringMap(value any) map[string]any {
	if object, ok := value.(map[string]any); ok {
		return object
	}
	return map[string]any{}
}
