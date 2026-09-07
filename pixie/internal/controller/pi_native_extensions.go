package controller

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

type nativeSource struct {
	Source string `json:"source"`
	Scope  string `json:"scope"`
	Origin string `json:"origin"`
}
type nativePackage struct {
	Source    string  `json:"source"`
	Scope     string  `json:"scope"`
	Filtered  bool    `json:"filtered"`
	Installed bool    `json:"installed"`
	Name      *string `json:"name"`
	Version   *string `json:"version"`
	State     string  `json:"state"`
}
type nativePath struct {
	Path  string `json:"path"`
	Scope string `json:"scope"`
}
type nativeResource struct {
	nativeSource
	ConfigurationSupported bool   `json:"configurationSupported"`
	ResourceKey            string `json:"resourceKey,omitempty"`
	Path                   string `json:"path"`
	Enabled                bool   `json:"enabled"`
	State                  string `json:"state"`
}
type nativeLoadedExtension struct {
	Path             string       `json:"path"`
	ResolvedPath     string       `json:"resolvedPath"`
	Source           nativeSource `json:"source"`
	Name             *string      `json:"name"`
	Version          *string      `json:"version"`
	Tools            []string     `json:"tools"`
	Commands         []string     `json:"commands"`
	InterfaceSupport string       `json:"interfaceSupport"`
}
type nativeInventory struct {
	Version                int `json:"version"`
	ConfigurationRevisions *struct {
		User    string `json:"user"`
		Project string `json:"project"`
	} `json:"configurationRevisions,omitempty"`
	Context struct {
		CWD       string  `json:"cwd"`
		SessionID *string `json:"sessionId"`
		Reader    string  `json:"reader"`
	} `json:"context"`
	Packages   []nativePackage         `json:"packages"`
	Paths      []nativePath            `json:"paths"`
	Resources  []nativeResource        `json:"resources"`
	Extensions []nativeLoadedExtension `json:"extensions"`
	Errors     []struct {
		Path string `json:"path"`
		Code string `json:"code"`
	} `json:"errors"`
	Warnings []string `json:"warnings"`
	Trust    *struct {
		ProjectTrusted   bool  `json:"projectTrusted"`
		Decision         *bool `json:"decision"`
		RequiresDecision bool  `json:"requiresDecision"`
	} `json:"trust,omitempty"`
}

var nativeURLCredentials = regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*://)[^/\s]*@`)

func nativeReference(value string) string {
	value = nativeURLCredentials.ReplaceAllString(value, "${1}")
	if index := strings.IndexAny(value, "?#"); index >= 0 {
		value = value[:index]
	}
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, value)
	runes := []rune(value)
	if len(runes) > 1024 {
		return string(runes[:1024])
	}
	return value
}

func (a *PiAdmin) nativeExtensionParams(request map[string]any) (map[string]any, error) {
	params := map[string]any{}
	projectID, root, sessionID := textValue(request["projectId"]), textValue(request["root"]), textValue(request["sessionId"])
	if projectID != "" || root != "" || sessionID != "" {
		if projectID == "" || root == "" || a.sessions == nil {
			return nil, fmt.Errorf("select a project and root together")
		}
		cwd, err := a.sessions.projects.AssertRoot(projectID, root)
		if err != nil {
			return nil, err
		}
		params["cwd"] = cwd
		if sessionID != "" {
			recorded, err := a.sessions.RecordedCWD(projectID, sessionID)
			if err != nil || recorded != cwd {
				return nil, fmt.Errorf("session does not belong to the selected project root")
			}
			params["sessionId"] = sessionID
		}
	}
	return params, nil
}

func (a *PiAdmin) nativeExtensions(ctx context.Context, request map[string]any) (any, error) {
	params, err := a.nativeExtensionParams(request)
	if err != nil {
		return nil, err
	}
	projectID, sessionID := textValue(request["projectId"]), textValue(request["sessionId"])
	// Inventory must not call EnsureAttached: that can reopen a native session
	// and execute installed extension lifecycle hooks merely to inspect it.
	var result nativeInventory
	if err := a.call(ctx, "pi.extensions.list", params, &result); err != nil {
		return nil, fmt.Errorf("native extension inventory unavailable")
	}
	gotID := ""
	if result.Context.SessionID != nil {
		gotID = *result.Context.SessionID
	}
	if result.Version != 1 || gotID != sessionID || (params["cwd"] != nil && result.Context.CWD != params["cwd"]) {
		return nil, fmt.Errorf("native extension inventory context mismatch")
	}
	reader := result.Context.Reader
	if (sessionID != "" && reader != "session" && reader != "not-resident") ||
		(sessionID == "" && projectID != "" && reader != "configured-only") ||
		(projectID == "" && reader != "service") {
		return nil, fmt.Errorf("invalid native inventory reader")
	}
	if result.Packages == nil || result.Paths == nil || result.Resources == nil || result.Extensions == nil || result.Errors == nil || result.Warnings == nil {
		return nil, fmt.Errorf("incomplete native extension inventory")
	}
	if (reader == "not-resident" || reader == "configured-only") && (len(result.Extensions) != 0 || len(result.Errors) != 0) {
		return nil, fmt.Errorf("loaded inventory is unavailable for this reader")
	}
	for _, count := range []int{len(result.Packages), len(result.Paths), len(result.Resources), len(result.Extensions), len(result.Errors), len(result.Warnings)} {
		if count > 1000 {
			return nil, fmt.Errorf("native extension inventory exceeds limit")
		}
	}
	cleanSource := func(source *nativeSource) {
		source.Source = nativeReference(source.Source)
		if source.Scope != "user" && source.Scope != "project" && source.Scope != "temporary" {
			source.Scope = "unknown"
		}
		if source.Origin != "package" && source.Origin != "top-level" {
			source.Origin = "unknown"
		}
	}
	cleanState := func(state string) string {
		switch state {
		case "loaded", "failed", "not-loaded", "missing", "not-observed":
			return state
		}
		return "not-observed"
	}
	cleanIdentity := func(value **string) {
		if *value != nil {
			safe := nativeReference(**value)
			*value = &safe
		}
	}
	result.Context.CWD = nativeReference(result.Context.CWD)
	if result.ConfigurationRevisions != nil && (!nativeConfigurationToken.MatchString(result.ConfigurationRevisions.User) || !nativeConfigurationToken.MatchString(result.ConfigurationRevisions.Project)) {
		result.ConfigurationRevisions = nil
	}
	for i := range result.Packages {
		p := &result.Packages[i]
		p.Source, p.State = nativeReference(p.Source), cleanState(p.State)
		if p.Scope != "user" && p.Scope != "project" {
			p.Scope = "unknown"
		}
		cleanIdentity(&p.Name)
		cleanIdentity(&p.Version)
	}
	for i := range result.Paths {
		p := &result.Paths[i]
		p.Path = nativeReference(p.Path)
		if p.Scope != "user" && p.Scope != "project" {
			p.Scope = "unknown"
		}
	}
	for i := range result.Resources {
		r := &result.Resources[i]
		if !nativeConfigurationToken.MatchString(r.ResourceKey) {
			r.ResourceKey = ""
		}
		cleanSource(&r.nativeSource)
		r.Path, r.State = nativeReference(r.Path), cleanState(r.State)
	}
	for i := range result.Extensions {
		e := &result.Extensions[i]
		cleanSource(&e.Source)
		cleanIdentity(&e.Name)
		cleanIdentity(&e.Version)
		e.Path, e.ResolvedPath, e.InterfaceSupport = nativeReference(e.Path), nativeReference(e.ResolvedPath), "unknown"
		if len(e.Tools) > 500 || len(e.Commands) > 500 {
			return nil, fmt.Errorf("native contribution inventory exceeds limit")
		}
		if e.Tools == nil {
			e.Tools = []string{}
		}
		if e.Commands == nil {
			e.Commands = []string{}
		}
		for j := range e.Tools {
			e.Tools[j] = nativeReference(e.Tools[j])
		}
		for j := range e.Commands {
			e.Commands[j] = nativeReference(e.Commands[j])
		}
	}
	for i := range result.Errors {
		result.Errors[i].Path = nativeReference(result.Errors[i].Path)
		result.Errors[i].Code = "load-failed"
	}
	for i, warning := range result.Warnings {
		switch warning {
		case "settings-read-failed", "package-discovery-failed", "resource-discovery-failed", "inventory-truncated":
		default:
			result.Warnings[i] = "discovery-failed"
		}
	}
	return result, nil
}

var nativeConfigurationToken = regexp.MustCompile(`^[a-f0-9]{64}$`)

func (a *PiAdmin) nativeExtensionChange(ctx context.Context, method string, request map[string]any) (any, error) {
	params, err := a.nativeExtensionParams(request)
	if err != nil {
		return nil, err
	}
	hostMethod := "pi.extensions.reload"
	if method == "pi.nativeExtensionConfigure" {
		scope := textValue(request["scope"])
		enabled, validEnabled := request["enabled"].(bool)
		if (scope != "user" && scope != "project") || !validEnabled || request["confirmed"] != true ||
			!nativeConfigurationToken.MatchString(textValue(request["resourceKey"])) ||
			!nativeConfigurationToken.MatchString(textValue(request["expectedRevision"])) {
			return nil, fmt.Errorf("confirm a current scoped native configuration change")
		}
		if scope == "project" && params["cwd"] == nil {
			return nil, fmt.Errorf("select a project root")
		}
		params["scope"], params["enabled"], params["confirmed"] = scope, enabled, true
		params["resourceKey"], params["expectedRevision"] = request["resourceKey"], request["expectedRevision"]
		hostMethod = "pi.extensions.configure"
	} else if params["sessionId"] == nil {
		return nil, fmt.Errorf("select a native session")
	}
	var response struct {
		Saved   *bool   `json:"saved,omitempty"`
		Loaded  bool    `json:"loaded"`
		Reload  string  `json:"reload"`
		Reason  string  `json:"reason"`
		Warning *string `json:"warning,omitempty"`
	}
	if err := a.call(ctx, hostMethod, params, &response); err != nil {
		return nil, fmt.Errorf("native configuration outcome not confirmed. Refresh inventory before retrying")
	}
	if response.Loaded || response.Reload != "deferred" ||
		(response.Reason != "session-busy" && response.Reason != "session-not-resident" && response.Reason != "sdk-loader-install-policy") ||
		(method == "pi.nativeExtensionConfigure" && (response.Saved == nil || !*response.Saved)) {
		return nil, fmt.Errorf("unexpected native configuration outcome. Refresh inventory")
	}
	if response.Warning != nil {
		safe := "settings-cleanup-failed"
		response.Warning = &safe
	}
	return response, nil
}
