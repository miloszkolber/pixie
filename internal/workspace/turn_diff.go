package workspace

import (
	"context"
	"fmt"
	"path/filepath"
)

// TurnDiff is the read-only diff produced for one write or edit tool call. It is
// a result, never a transport failure: a Git execution problem is reported as an
// unavailable diff with a bounded message so the browser renders a result rather
// than a request error. It carries no raw Git stderr, which can hold paths or
// credentials.
type TurnDiff struct {
	Path         string `json:"path"`
	Repository   string `json:"repository"`
	Original     string `json:"original,omitempty"`
	Modified     string `json:"modified,omitempty"`
	Patch        string `json:"patch,omitempty"`
	OriginalPath string `json:"originalPath,omitempty"`
	ComparisonID string `json:"comparisonId,omitempty"`
	Available    bool   `json:"available"`
	Fallback     bool   `json:"fallback,omitempty"`
	Binary       bool   `json:"binary,omitempty"`
	TooLarge     bool   `json:"tooLarge,omitempty"`
	Message      string `json:"message,omitempty"`
}

// TurnDiffForTool produces the per-turn diff for a write or edit tool call. The
// path-scoped raw worktree comparison is preferred; when that cannot be produced
// because Git failed, the bounded `git diff HEAD` patch is returned as a
// fallback. A containment denial is returned as a typed error because it is an
// authority decision, not a transient Git failure. No write path is touched.
func (g *Git) TurnDiffForTool(ctx context.Context, projectID, repository, toolName, path string, scope GitDiffScope) (TurnDiff, error) {
	switch toolName {
	case "write", "edit":
	default:
		return TurnDiff{}, fmt.Errorf("turn diff requires a write or edit tool call")
	}
	_, admitted, err := g.repositoryFor(ctx, projectID, repository)
	if err != nil {
		return TurnDiff{}, err
	}
	cleanPath, _, pathErr := relativePathInRoot(admitted, filepath.FromSlash(path))
	if pathErr != nil {
		return TurnDiff{}, pathErr
	}
	if scope.Kind == "" {
		scope = GitDiffScope{Kind: "uncommitted"}
	}
	preview, diffErr := g.DiffFile(ctx, projectID, admitted, cleanPath, scope)
	if diffErr != nil {
		if IsTraversal(diffErr) || ctx.Err() != nil {
			return TurnDiff{}, diffErr
		}
		return g.gitDiffHeadFallback(ctx, admitted, cleanPath)
	}
	return TurnDiff{
		Path:         cleanPath,
		Repository:   admitted,
		Original:     preview.Original,
		Modified:     preview.Modified,
		OriginalPath: preview.OriginalPath,
		ComparisonID: preview.ComparisonID,
		Available:    !preview.Unavailable,
		Binary:       preview.Binary,
		TooLarge:     preview.TooLarge,
		Message:      preview.Message,
	}, nil
}

// gitDiffHeadFallback returns a bounded `git diff HEAD` patch for one path. It
// never returns a raw Git error: the caller receives a result with a stable,
// bounded message so a Git failure renders as an unavailable diff.
func (g *Git) gitDiffHeadFallback(ctx context.Context, repository, name string) (TurnDiff, error) {
	result := runGit(ctx, repository, []string{
		"diff",
		"--no-ext-diff",
		"--no-textconv",
		"--no-color",
		"HEAD",
		"--",
		filepath.ToSlash(name),
	}, gitOutputLimit)
	if !result.ok {
		message := "Git could not produce a diff for this change."
		if result.failure == "busy" {
			message = "Git is busy with another operation in this directory."
		}
		return TurnDiff{Path: name, Repository: repository, Available: false, Fallback: true, Message: message}, nil
	}
	return TurnDiff{Path: name, Repository: repository, Patch: result.out, Available: true, Fallback: true}, nil
}
