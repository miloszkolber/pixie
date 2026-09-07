package workspace

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

const (
	gitPreviewMaxBytes = 1024 * 1024
	gitBranchLimit     = 200
)

var gitOIDPattern = regexp.MustCompile(`^[0-9a-f]{4,64}$`)

type GitDiffScope struct {
	Kind    string `json:"kind"`
	SHA     string `json:"sha,omitempty"`
	BaseRef string `json:"baseRef,omitempty"`
}

type GitBranch struct {
	Ref  string `json:"ref"`
	Name string `json:"name"`
}

type GitBranchList struct {
	Branches  []GitBranch `json:"branches"`
	Truncated bool        `json:"truncated"`
}

type GitDiffFile struct {
	Original     string `json:"original"`
	Modified     string `json:"modified"`
	OriginalPath string `json:"originalPath,omitempty"`
	ComparisonID string `json:"comparisonId,omitempty"`
	Unavailable  bool   `json:"unavailable,omitempty"`
	Binary       bool   `json:"binary,omitempty"`
	TooLarge     bool   `json:"tooLarge,omitempty"`
	Message      string `json:"message,omitempty"`
}

type GitCommit struct {
	SHA         string `json:"sha"`
	ShortSHA    string `json:"shortSha"`
	Subject     string `json:"subject"`
	Author      string `json:"author"`
	CommittedAt string `json:"committedAt"`
}

type diffRange struct {
	prefix       []string
	revisions    []string
	untracked    bool
	originalRef  string
	modifiedRef  string
	comparisonID string
}

func resolveCommit(ctx context.Context, repository, ref string) string {
	result := runGit(ctx, repository, []string{"rev-parse", "--verify", "--quiet", "--end-of-options", ref + "^{commit}"}, gitOutputLimit)
	if !result.ok {
		return ""
	}
	return strings.TrimSpace(result.out)
}

func unbornHead(ctx context.Context, repository string) bool {
	head := runGit(ctx, repository, []string{"symbolic-ref", "--quiet", "HEAD"}, gitOutputLimit)
	ref := strings.TrimSpace(head.out)
	if !head.ok || !strings.HasPrefix(ref, "refs/heads/") {
		return false
	}
	refs := runGit(ctx, repository, []string{"for-each-ref", "--format=%(refname)", "--", ref}, gitOutputLimit)
	if !refs.ok {
		return false
	}
	for _, candidate := range strings.Split(strings.TrimSpace(refs.out), "\n") {
		if candidate == ref {
			return false
		}
	}
	return true
}

func branchRef(ref string) bool {
	if len(ref) > 1024 || strings.ContainsRune(ref, '\x00') {
		return false
	}
	for _, prefix := range []string{"refs/heads/", "refs/remotes/"} {
		if strings.HasPrefix(ref, prefix) && len(ref) > len(prefix) {
			return true
		}
	}
	return false
}

func branchDisplayName(ref string) string {
	for _, prefix := range []string{"refs/heads/", "refs/remotes/"} {
		if strings.HasPrefix(ref, prefix) {
			return plainGitText(strings.TrimPrefix(ref, prefix))
		}
	}
	return plainGitText(ref)
}

func resolveBranchRef(ctx context.Context, repository, ref string) (string, error) {
	if !branchRef(ref) {
		return "", &codedError{code: "UNKNOWN_BRANCH", message: "Unknown branch reference"}
	}
	result := runGit(ctx, repository, []string{
		"for-each-ref",
		"--count=2",
		"--sort=refname",
		"--format=%(refname)%00%(symref)%00%(objectname)%00%(objecttype)",
		"--",
		ref,
	}, gitOutputLimit)
	if !result.ok {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		return "", fmt.Errorf("could not resolve branch reference")
	}
	for _, line := range strings.Split(strings.TrimSuffix(result.out, "\n"), "\n") {
		fields := strings.Split(line, "\x00")
		if len(fields) != 4 || fields[0] != ref {
			continue
		}
		if fields[1] != "" {
			return "", &codedError{code: "SYMBOLIC_BRANCH", message: "Symbolic branch references cannot be compared"}
		}
		if fields[3] != "commit" || !fullGitOID(fields[2]) {
			return "", &codedError{code: "UNKNOWN_BRANCH", message: "Branch reference does not resolve to a commit"}
		}
		return fields[2], nil
	}
	return "", &codedError{code: "UNKNOWN_BRANCH", message: "Unknown branch reference"}
}

func fullGitOID(value string) bool {
	return (len(value) == 40 || len(value) == 64) && gitOIDPattern.MatchString(value)
}

func resolveDiffRange(ctx context.Context, repository string, scope GitDiffScope) (diffRange, error) {
	if scope.Kind == "" || scope.Kind == "uncommitted" {
		head := resolveCommit(ctx, repository, "HEAD")
		if head == "" {
			if err := ctx.Err(); err != nil {
				return diffRange{}, err
			}
			if unbornHead(ctx, repository) {
				return diffRange{prefix: []string{"diff", "--cached"}, untracked: true}, nil
			}
			return diffRange{}, fmt.Errorf("could not resolve Git HEAD")
		}
		return diffRange{prefix: []string{"diff"}, revisions: []string{head}, untracked: true, originalRef: head}, nil
	}
	if scope.Kind == "branch" {
		head := resolveCommit(ctx, repository, "HEAD")
		if head == "" {
			if err := ctx.Err(); err != nil {
				return diffRange{}, err
			}
			if unbornHead(ctx, repository) {
				return diffRange{}, &codedError{code: "UNBORN_HEAD", message: "Cannot compare branches before HEAD has a commit"}
			}
			return diffRange{}, fmt.Errorf("could not resolve Git HEAD")
		}
		base, err := resolveBranchRef(ctx, repository, scope.BaseRef)
		if err != nil {
			return diffRange{}, err
		}
		merged := runGit(ctx, repository, []string{"merge-base", "--end-of-options", base, head}, gitOutputLimit)
		mergeBase := strings.TrimSpace(merged.out)
		if !merged.ok {
			if err := ctx.Err(); err != nil {
				return diffRange{}, err
			}
			if merged.failure != "" || merged.err != "" {
				return diffRange{}, fmt.Errorf("could not compare branches")
			}
			return diffRange{}, &codedError{code: "NO_MERGE_BASE", message: "The selected branch and HEAD have no common commit"}
		}
		if !fullGitOID(mergeBase) {
			return diffRange{}, fmt.Errorf("could not compare branches")
		}
		return diffRange{prefix: []string{"diff"}, revisions: []string{mergeBase, head}, originalRef: mergeBase, modifiedRef: head, comparisonID: mergeBase + ".." + head}, nil
	}
	requested := scope.SHA
	if scope.Kind == "pinned" {
		requested = scope.BaseRef
	}
	if (scope.Kind != "commit" && scope.Kind != "pinned") || !gitOIDPattern.MatchString(requested) {
		return diffRange{}, fmt.Errorf("unsupported Git diff scope")
	}
	resolved := resolveCommit(ctx, repository, requested)
	if resolved == "" {
		return diffRange{}, &codedError{code: "UNKNOWN_COMMIT", message: "Unknown commit: " + requested}
	}
	if scope.Kind == "pinned" {
		return diffRange{prefix: []string{"diff"}, revisions: []string{resolved}, untracked: true, originalRef: resolved}, nil
	}
	parent := resolveCommit(ctx, repository, resolved+"^")
	if parent == "" {
		return diffRange{prefix: []string{"show", "--format="}, revisions: []string{resolved}, modifiedRef: resolved}, nil
	}
	return diffRange{prefix: []string{"diff"}, revisions: []string{parent, resolved}, originalRef: parent, modifiedRef: resolved}, nil
}

type codedError struct {
	code    string
	message string
}

func (e *codedError) Error() string     { return e.message }
func (e *codedError) ErrorCode() string { return e.code }

func changedArgs(value diffRange, mode string) []string {
	args := append([]string(nil), value.prefix...)
	args = append(args, "--no-ext-diff", "--no-textconv", "--find-renames", mode, "-z", "--end-of-options")
	args = append(args, value.revisions...)
	return append(args, "--")
}

func (g *Git) changes(ctx context.Context, repository string, scope GitDiffScope) ([]GitFileChange, string, error) {
	rangeValue, err := resolveDiffRange(ctx, repository, scope)
	if err != nil {
		return nil, "", err
	}
	countsResult := runGit(ctx, repository, changedArgs(rangeValue, "--numstat"), gitOutputLimit)
	tracked := runGit(ctx, repository, changedArgs(rangeValue, "--name-status"), gitOutputLimit)
	if !countsResult.ok || !tracked.ok {
		return nil, "", fmt.Errorf("could not read changed files")
	}
	counts := parseNumstat(countsResult.out)
	result := parseNameStatus(tracked.out, counts)
	if rangeValue.untracked {
		untracked := runGit(ctx, repository, []string{"ls-files", "-z", "--others", "--exclude-standard"}, gitOutputLimit)
		if !untracked.ok {
			return nil, "", fmt.Errorf("could not read untracked files: %s", untracked.err)
		}
		counted := 0
		for _, name := range strings.Split(untracked.out, "\x00") {
			if name == "" {
				continue
			}
			change := GitFileChange{Path: name, Status: "untracked"}
			if counted < 64 {
				if preview, err := readWorktreePreview(repository, filepath.FromSlash(name)); err == nil && preview.issue == "" {
					added, removed := lineCount(preview.content), 0
					change.Added, change.Removed = &added, &removed
				}
			}
			counted++
			result = append(result, change)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Path < result[j].Path })
	return result, rangeValue.comparisonID, nil
}

func parseNumstat(output string) map[string][2]int {
	result := make(map[string][2]int)
	records := strings.Split(output, "\x00")
	for index := 0; index < len(records); index++ {
		parts := strings.Split(records[index], "\t")
		if len(parts) < 3 {
			continue
		}
		added, addErr := strconv.Atoi(parts[0])
		removed, removeErr := strconv.Atoi(parts[1])
		if addErr != nil || removeErr != nil {
			continue
		}
		name := strings.Join(parts[2:], "\t")
		if name == "" && index+2 < len(records) {
			index += 2
			name = records[index]
		}
		if name != "" {
			result[name] = [2]int{added, removed}
		}
	}
	return result
}

func parseNameStatus(output string, counts map[string][2]int) []GitFileChange {
	records := strings.Split(output, "\x00")
	result := []GitFileChange{}
	for index := 0; index < len(records); index++ {
		record := records[index]
		if record == "" {
			continue
		}
		separator := strings.IndexByte(record, '\t')
		code, name := record, ""
		if separator >= 0 {
			code, name = record[:separator], record[separator+1:]
		} else if index+1 < len(records) {
			index++
			name = records[index]
		}
		originalPath := ""
		if (strings.HasPrefix(code, "R") || strings.HasPrefix(code, "C")) && index+1 < len(records) {
			originalPath = name
			index++
			name = records[index]
		}
		if name == "" {
			continue
		}
		status := "modified"
		switch code[0] {
		case 'A', 'C':
			status = "added"
		case 'D':
			status = "deleted"
		case 'R':
			status = "renamed"
		}
		change := GitFileChange{Path: name, OriginalPath: originalPath, Status: status}
		if value, ok := counts[name]; ok {
			added, removed := value[0], value[1]
			change.Added, change.Removed = &added, &removed
		}
		result = append(result, change)
	}
	return result
}

func (g *Git) DiffFile(ctx context.Context, projectID, repository, name string, scope GitDiffScope) (GitDiffFile, error) {
	_, admitted, err := g.repositoryFor(ctx, projectID, repository)
	if err != nil {
		return GitDiffFile{}, err
	}
	rangeValue, err := resolveDiffRange(ctx, admitted, scope)
	if err != nil {
		return GitDiffFile{}, err
	}
	name, _, err = relativePathInRoot(admitted, filepath.FromSlash(name))
	if err != nil {
		return GitDiffFile{}, err
	}
	name = filepath.ToSlash(name)
	originalPath := ""
	originalName := name
	original := filePreview{issue: "missing"}
	if rangeValue.originalRef != "" {
		// Rename detection needs both paths: filtering the diff to the destination
		// alone would make Git report it as an added file.
		changed := runGit(ctx, admitted, changedArgs(rangeValue, "--name-status"), gitOutputLimit)
		if !changed.ok {
			return unavailableDiff(filePreview{issue: "unavailable"}, "", rangeValue.comparisonID), nil
		}
		for _, change := range parseNameStatus(changed.out, nil) {
			if change.Path == name && change.OriginalPath != "" {
				originalPath, originalName = change.OriginalPath, change.OriginalPath
				break
			}
		}
		if _, _, err := relativePathInRoot(admitted, filepath.FromSlash(originalName)); err != nil {
			return GitDiffFile{}, err
		}
		original = readBlobPreview(ctx, admitted, rangeValue.originalRef, originalName)
	}
	if original.issue != "" && original.issue != "missing" {
		return unavailableDiff(original, originalPath, rangeValue.comparisonID), nil
	}
	modified := filePreview{}
	if rangeValue.modifiedRef != "" {
		modified = readBlobPreview(ctx, admitted, rangeValue.modifiedRef, name)
	} else {
		modified, err = readWorktreePreview(admitted, filepath.FromSlash(name))
		if err != nil && errors.Is(err, errPathEscapesProjectRoot) {
			return GitDiffFile{}, err
		}
	}
	if modified.issue == "missing" {
		if original.issue == "missing" {
			return unavailableDiff(modified, originalPath, rangeValue.comparisonID), nil
		}
		return GitDiffFile{Original: original.content, OriginalPath: originalPath, ComparisonID: rangeValue.comparisonID}, nil
	}
	if modified.issue != "" {
		return unavailableDiff(modified, originalPath, rangeValue.comparisonID), nil
	}
	return GitDiffFile{Original: original.content, Modified: modified.content, OriginalPath: originalPath, ComparisonID: rangeValue.comparisonID}, nil
}

func (g *Git) ListCommits(ctx context.Context, projectID, repository string) (map[string]any, error) {
	_, admitted, err := g.repositoryFor(ctx, projectID, repository)
	if err != nil {
		return nil, err
	}
	log := runGit(ctx, admitted, []string{"log", "--max-count=200", "--format=%H%x00%h%x00%cI%x00%an%x00%s", "--"}, gitOutputLimit)
	commits := []GitCommit{}
	if !log.ok {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		// A broken repository, rejected command or output limit is not an empty history.
		if unbornHead(ctx, admitted) {
			return map[string]any{"commits": commits}, nil
		}
		return nil, &codedError{code: "GIT_LOG_UNAVAILABLE", message: "Could not read commit history"}
	}
	for _, line := range strings.Split(strings.TrimSpace(log.out), "\n") {
		parts := strings.Split(line, "\x00")
		if len(parts) >= 5 && parts[0] != "" && parts[1] != "" {
			commits = append(commits, GitCommit{SHA: parts[0], ShortSHA: parts[1], CommittedAt: parts[2], Author: plainGitText(parts[3]), Subject: plainGitText(strings.Join(parts[4:], "\x00"))})
		}
	}
	return map[string]any{"commits": commits}, nil
}

func (g *Git) ListBranches(ctx context.Context, projectID, repository string) (GitBranchList, error) {
	_, admitted, err := g.repositoryFor(ctx, projectID, repository)
	if err != nil {
		return GitBranchList{}, err
	}
	result := runGit(ctx, admitted, []string{
		"for-each-ref",
		fmt.Sprintf("--count=%d", gitBranchLimit+1),
		"--sort=refname",
		"--format=%(refname)%00%(symref)%00%(objecttype)",
		"--",
		"refs/heads",
		"refs/remotes",
	}, gitOutputLimit)
	if !result.ok {
		if err := ctx.Err(); err != nil {
			return GitBranchList{}, err
		}
		return GitBranchList{}, &codedError{code: "GIT_BRANCHES_UNAVAILABLE", message: "Could not read branches"}
	}
	listed := GitBranchList{Branches: []GitBranch{}}
	records := strings.Split(strings.TrimSuffix(result.out, "\n"), "\n")
	if len(records) > gitBranchLimit {
		listed.Truncated = true
	}
	for _, line := range records {
		fields := strings.Split(line, "\x00")
		if len(fields) != 3 || !branchRef(fields[0]) || fields[1] != "" || fields[2] != "commit" {
			continue
		}
		if len(listed.Branches) == gitBranchLimit {
			listed.Truncated = true
			break
		}
		listed.Branches = append(listed.Branches, GitBranch{Ref: fields[0], Name: branchDisplayName(fields[0])})
	}
	return listed, nil
}

type filePreview struct {
	content string
	issue   string
}

func readWorktreePreview(root, name string) (filePreview, error) {
	// A requested symlink previews its bounded link text, not target contents.
	// Direct files are still opened and checked through the same descriptor walk.
	file, info, linkText, err := openProjectLinkPreview(root, name)
	if err != nil {
		if errors.Is(err, errPathEscapesProjectRoot) {
			return filePreview{}, err
		}
		if errors.Is(err, errProjectFileTooLarge) {
			return filePreview{issue: "tooLarge"}, nil
		}
		if errors.Is(err, os.ErrNotExist) {
			return filePreview{issue: "missing"}, nil
		}
		return filePreview{issue: "unavailable"}, nil
	}
	if file == nil {
		return filePreview{content: linkText}, nil
	}
	defer file.Close()
	if linkText != "" {
		return filePreview{content: linkText}, nil
	}
	if info.Size() > gitPreviewMaxBytes {
		return filePreview{issue: "tooLarge"}, nil
	}
	content, err := io.ReadAll(io.LimitReader(file, gitPreviewMaxBytes+1))
	if err != nil {
		return filePreview{issue: "unavailable"}, nil
	}
	if len(content) > gitPreviewMaxBytes {
		return filePreview{issue: "tooLarge"}, nil
	}
	if bytes.IndexByte(content, 0) >= 0 {
		return filePreview{issue: "binary"}, nil
	}
	return filePreview{content: string(content)}, nil
}

func readBlobPreview(ctx context.Context, repository, ref, name string) filePreview {
	entry := runGit(ctx, repository, []string{"ls-tree", "--full-tree", "-l", "-z", "--end-of-options", ref, "--", ":(literal)" + name}, gitOutputLimit)
	if !entry.ok {
		return filePreview{issue: "unavailable"}
	}
	if entry.out == "" {
		return filePreview{issue: "missing"}
	}
	metadata, entryName, _ := strings.Cut(entry.out, "\t")
	fields := strings.Fields(metadata)
	if len(fields) != 4 || fields[1] != "blob" || entryName != name+"\x00" {
		return filePreview{issue: "unavailable"}
	}
	bytesCount, err := strconv.Atoi(fields[3])
	if err != nil || bytesCount < 0 {
		return filePreview{issue: "unavailable"}
	}
	if bytesCount > gitPreviewMaxBytes {
		return filePreview{issue: "tooLarge"}
	}
	shown := runGit(ctx, repository, []string{"cat-file", "blob", fields[2]}, gitPreviewMaxBytes)
	if shown.failure == "output-limit" {
		return filePreview{issue: "tooLarge"}
	}
	if !shown.ok {
		return filePreview{issue: "unavailable"}
	}
	if strings.IndexByte(shown.out, 0) >= 0 {
		return filePreview{issue: "binary"}
	}
	return filePreview{content: shown.out}
}

func unavailableDiff(preview filePreview, originalPath, comparisonID string) GitDiffFile {
	result := GitDiffFile{Unavailable: true, OriginalPath: originalPath, ComparisonID: comparisonID}
	switch preview.issue {
	case "binary":
		result.Binary, result.Message = true, "Binary files cannot be previewed"
	case "tooLarge":
		result.TooLarge, result.Message = true, "File is too large to preview"
	case "missing":
		result.Message = "File does not exist"
	default:
		result.Message = "File is unavailable for preview"
	}
	return result
}

func lineCount(content string) int {
	if content == "" {
		return 0
	}
	count := strings.Count(content, "\n")
	if !strings.HasSuffix(content, "\n") {
		count++
	}
	return count
}

func plainGitText(value string) string {
	return strings.Map(func(character rune) rune {
		if unicode.IsControl(character) || character >= '\u200b' && character <= '\u200f' || character >= '\u202a' && character <= '\u202e' || character >= '\u2066' && character <= '\u2069' || character == '\u061c' || character == '\ufeff' || character == '\u00ad' {
			return -1
		}
		return character
	}, value)
}
