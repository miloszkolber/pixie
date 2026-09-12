package workspace

import (
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/sys/unix"
)

const (
	gitPreviewMaxBytes       = 1024 * 1024
	gitBranchLimit           = 200
	gitRawHashFileLimit      = fileReadLimit
	gitRawHashAggregateLimit = 64 * 1024 * 1024
	gitRawWorktreeTTL        = 30 * time.Second
	gitRawPreviewNote        = "Showing raw worktree bytes; Git clean/process and LFS conversion are not applied"
	gitRawHashLimitWarning   = "Some raw worktree files exceeded the 4 MiB per-file or 64 MiB aggregate comparison limit and were reported conservatively."
	gitRawHashReadWarning    = "Some raw worktree files could not be read and were reported conservatively."
	gitRawSubmoduleWarning   = "Some submodule metadata was missing or invalid and was reported conservatively."
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
	rawWorktree  bool
	originalRef  string
	modifiedRef  string
	comparisonID string
}

func resolveCommit(ctx context.Context, repository, ref string) string {
	result := runGit(ctx, repository, []string{"rev-parse", "--verify", "--quiet", "--end-of-options", ref + "^{commit}"}, gitOutputLimit)
	if !result.ok {
		return ""
	}
	resolved := strings.TrimSpace(result.out)
	if !fullGitOID(resolved) {
		return ""
	}
	return resolved
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
		return diffRange{prefix: []string{"diff"}, revisions: []string{head}, untracked: true, rawWorktree: true, originalRef: head}, nil
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
		return diffRange{prefix: []string{"diff"}, revisions: []string{resolved}, untracked: true, rawWorktree: true, originalRef: resolved}, nil
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
	args = append(args, "--no-ext-diff", "--no-textconv", "--find-renames", "--ignore-submodules=dirty", "--submodule=short", mode, "-z", "--end-of-options")
	args = append(args, value.revisions...)
	return append(args, "--")
}

type gitTreeEntry struct {
	mode string
	oid  string
}

type gitIndexEntry struct {
	mode            string
	oid             string
	stage           int
	assumeUnchanged bool
	skipWorktree    bool
}

// rawWorktreeSnapshot is deliberately independent of Git's convert_to_git
// path. A worktree filter is repository-controlled code, including filters
// selected from $GIT_DIR/info/attributes, which --attr-source does not hide.
type rawWorktreeSnapshot struct {
	exists          bool
	accessible      bool
	indeterminate   bool
	limit           bool
	metadataInvalid bool
	mode            string
	sha1            string
	sha256          string
	submoduleOID    string
}

type rawHashBudget struct {
	remaining int64
}

func newRawHashBudget() *rawHashBudget {
	return &rawHashBudget{remaining: gitRawHashAggregateLimit}
}

func (b *rawHashBudget) reserve(size int64) bool {
	if size < 0 || size > gitRawHashFileLimit || size > b.remaining {
		return false
	}
	b.remaining -= size
	return true
}

type rawChangeCandidate struct {
	path     string
	change   GitFileChange
	base     gitTreeEntry
	index    gitIndexEntry
	hasIndex bool
	snapshot rawWorktreeSnapshot
}

func gitTreeEntries(ctx context.Context, repository, ref string) (map[string]gitTreeEntry, error) {
	result := runGit(ctx, repository, []string{
		"ls-tree",
		"--full-tree",
		"-r",
		"-z",
		"-l",
		"--abbrev=64",
		ref,
		"--",
	}, gitOutputLimit)
	if !result.ok {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("could not read Git tree")
	}
	entries := make(map[string]gitTreeEntry)
	for _, record := range strings.Split(result.out, "\x00") {
		if record == "" {
			continue
		}
		metadata, path, ok := strings.Cut(record, "\t")
		if !ok {
			return nil, fmt.Errorf("could not read Git tree")
		}
		fields := strings.Fields(metadata)
		if len(fields) != 4 || !validGitTreeMode(fields[0]) || !fullGitOID(fields[2]) || (fields[1] != "blob" && fields[1] != "commit") || path == "" {
			return nil, fmt.Errorf("could not read Git tree")
		}
		entries[path] = gitTreeEntry{mode: fields[0], oid: fields[2]}
	}
	return entries, nil
}

func validGitTreeMode(mode string) bool {
	switch mode {
	case "100644", "100755", "120000", "160000":
		return true
	default:
		return false
	}
}

func gitIndexEntries(ctx context.Context, repository string) (map[string]gitIndexEntry, error) {
	result := runGit(ctx, repository, []string{"ls-files", "--cached", "--stage", "-v", "-z", "--full-name", "--abbrev=64", "--"}, gitOutputLimit)
	if !result.ok {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("could not read Git index")
	}
	entries := make(map[string]gitIndexEntry)
	for _, record := range strings.Split(result.out, "\x00") {
		if record == "" {
			continue
		}
		metadata, path, ok := strings.Cut(record, "\t")
		if !ok {
			return nil, fmt.Errorf("could not read Git index")
		}
		fields := strings.Fields(metadata)
		assumeUnchanged, skipWorktree := false, false
		if len(fields) == 4 && len(fields[0]) == 1 {
			assumeUnchanged, skipWorktree = fields[0] == "h", fields[0] == "S"
			fields = fields[1:]
		}
		if len(fields) != 3 || !validGitTreeMode(fields[0]) || !fullGitOID(fields[1]) || path == "" {
			return nil, fmt.Errorf("could not read Git index")
		}
		stage, err := strconv.Atoi(fields[2])
		if err != nil || stage < 0 || stage > 3 {
			return nil, fmt.Errorf("could not read Git index")
		}
		candidate := gitIndexEntry{mode: fields[0], oid: fields[1], stage: stage, assumeUnchanged: assumeUnchanged, skipWorktree: skipWorktree}
		previous, exists := entries[path]
		// Stage zero is the normal entry. For an unmerged path retain one
		// higher-stage entry rather than silently treating malformed metadata as
		// clean; the worktree comparison below will report it as modified.
		if !exists || previous.stage != 0 || stage == 0 {
			entries[path] = candidate
		}
	}
	return entries, nil
}

func rawWorktreePaths(ctx context.Context, repository string) ([]string, error) {
	result := runGit(ctx, repository, []string{"ls-files", "-z", "--others", "--exclude-standard", "--"}, gitOutputLimit)
	if !result.ok {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("could not read untracked files")
	}
	paths := make([]string, 0)
	for _, path := range strings.Split(result.out, "\x00") {
		if path != "" {
			paths = append(paths, path)
		}
	}
	return paths, nil
}

func rawWorktreeSnapshotFor(ctx context.Context, root, name, expectedMode string, budget *rawHashBudget) rawWorktreeSnapshot {
	if budget == nil {
		budget = newRawHashBudget()
	}
	if expectedMode == "160000" {
		return rawSubmoduleSnapshot(ctx, root, name)
	}
	file, info, linkText, err := openProjectLinkPreview(root, filepath.FromSlash(name))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return rawWorktreeSnapshot{}
		}
		return rawWorktreeSnapshot{exists: true, indeterminate: true}
	}
	if file == nil {
		if budget != nil && !budget.reserve(int64(len(linkText))) {
			return rawWorktreeSnapshot{exists: true, indeterminate: true, limit: true, mode: "120000"}
		}
		sha1OID, sha256OID := hashGitBlobBytes([]byte(linkText))
		return rawWorktreeSnapshot{exists: true, accessible: true, mode: "120000", sha1: sha1OID, sha256: sha256OID}
	}
	defer file.Close()
	if budget != nil && !budget.reserve(info.Size()) {
		return rawWorktreeSnapshot{exists: true, indeterminate: true, limit: true, mode: gitWorktreeMode(info)}
	}
	sha1OID, sha256OID, hashErr := hashGitBlobReader(ctx, file, info.Size())
	if hashErr != nil {
		return rawWorktreeSnapshot{exists: true, indeterminate: true, mode: gitWorktreeMode(info)}
	}
	currentInfo, statErr := file.Stat()
	if statErr != nil || currentInfo.Size() != info.Size() {
		return rawWorktreeSnapshot{exists: true, indeterminate: true, mode: gitWorktreeMode(info)}
	}
	return rawWorktreeSnapshot{exists: true, accessible: true, mode: gitWorktreeMode(info), sha1: sha1OID, sha256: sha256OID}
}

func rawWorktreePresenceFor(root, name string) rawWorktreeSnapshot {
	file, info, _, err := openProjectLinkPreview(root, filepath.FromSlash(name))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return rawWorktreeSnapshot{}
		}
		return rawWorktreeSnapshot{exists: true, indeterminate: true}
	}
	if file == nil {
		return rawWorktreeSnapshot{exists: true, accessible: true, mode: "120000"}
	}
	defer file.Close()
	return rawWorktreeSnapshot{exists: true, accessible: true, mode: gitWorktreeMode(info)}
}

func gitWorktreeMode(info os.FileInfo) string {
	if info.Mode()&0o111 != 0 {
		return "100755"
	}
	return "100644"
}

func rawSubmoduleSnapshot(ctx context.Context, root, name string) rawWorktreeSnapshot {
	directory, err := openProjectDirectory(root, filepath.FromSlash(name))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return rawWorktreeSnapshot{metadataInvalid: true}
		}
		return rawWorktreeSnapshot{exists: true, indeterminate: true, metadataInvalid: true}
	}
	defer directory.Close()
	nested := filepath.Join(root, filepath.FromSlash(name))
	rootIdentity, rootErr := captureGitRepositoryIdentity(root)
	nestedIdentity, nestedErr := captureGitRepositoryIdentity(nested)
	if rootErr != nil || nestedErr != nil {
		return rawWorktreeSnapshot{exists: true, indeterminate: true, metadataInvalid: true}
	}
	metadata, metadataErr := readSubmoduleGitMetadata(directory, nested)
	if metadataErr != nil {
		if errors.Is(metadataErr, os.ErrNotExist) {
			return rawWorktreeSnapshot{exists: true, indeterminate: true, metadataInvalid: true}
		}
		return rawWorktreeSnapshot{exists: true, indeterminate: true, metadataInvalid: true}
	}
	allowedMetadata := admittedGitMetadataRoots(root)
	gitDir, err := canonicalGitMetadataPath(metadata.gitDir, nested)
	if err != nil || !pathWithinAny(allowedMetadata, gitDir) {
		return rawWorktreeSnapshot{exists: true, indeterminate: true, metadataInvalid: true}
	}
	gitDirIdentity, gitDirErr := captureGitRepositoryIdentity(gitDir)
	if gitDirErr != nil {
		return rawWorktreeSnapshot{exists: true, indeterminate: true, metadataInvalid: true}
	}
	// The opened worktree directory and metadata directory prove the admitted
	// paths immediately before the probe. The subprocess still uses a pathname,
	// so both identities are checked again afterwards; this is conservative
	// revalidation, not a claim that the descriptor is bound into Git.
	if err := revalidateGitRepositoryIdentity(root, rootIdentity); err != nil {
		return rawWorktreeSnapshot{exists: true, indeterminate: true, metadataInvalid: true}
	}
	if err := revalidateGitRepositoryIdentity(nested, nestedIdentity); err != nil {
		return rawWorktreeSnapshot{exists: true, indeterminate: true, metadataInvalid: true}
	}
	probe := runGit(ctx, nested, []string{"rev-parse", "--git-dir", "--show-toplevel", "--verify", "--quiet", "--end-of-options", "HEAD^{commit}"}, gitOutputLimit)
	if err := revalidateGitRepositoryIdentity(root, rootIdentity); err != nil {
		return rawWorktreeSnapshot{exists: true, indeterminate: true, metadataInvalid: true}
	}
	if err := revalidateGitRepositoryIdentity(nested, nestedIdentity); err != nil {
		return rawWorktreeSnapshot{exists: true, indeterminate: true, metadataInvalid: true}
	}
	if err := revalidateGitRepositoryIdentity(gitDir, gitDirIdentity); err != nil {
		return rawWorktreeSnapshot{exists: true, indeterminate: true, metadataInvalid: true}
	}
	metadataAfter, metadataAfterErr := readSubmoduleGitMetadata(directory, nested)
	if metadataAfterErr != nil {
		return rawWorktreeSnapshot{exists: true, indeterminate: true, metadataInvalid: true}
	}
	gitDirAfter, gitDirAfterErr := canonicalGitMetadataPath(metadataAfter.gitDir, nested)
	if gitDirAfterErr != nil || gitDirAfter != gitDir {
		return rawWorktreeSnapshot{exists: true, indeterminate: true, metadataInvalid: true}
	}
	lines := strings.Split(strings.TrimSpace(probe.out), "\n")
	if !probe.ok || len(lines) != 3 || !fullGitOID(strings.TrimSpace(lines[2])) {
		return rawWorktreeSnapshot{exists: true, indeterminate: true, metadataInvalid: true}
	}
	probedGitDir, err := canonicalGitMetadataPath(strings.TrimSpace(lines[0]), nested)
	if err != nil || probedGitDir != gitDir {
		return rawWorktreeSnapshot{exists: true, indeterminate: true, metadataInvalid: true}
	}
	probedTop, err := filepath.EvalSymlinks(strings.TrimSpace(lines[1]))
	if err != nil || probedTop != nestedIdentity.canonical {
		return rawWorktreeSnapshot{exists: true, indeterminate: true, metadataInvalid: true}
	}
	return rawWorktreeSnapshot{exists: true, accessible: true, mode: "160000", submoduleOID: strings.TrimSpace(lines[2])}
}

type submoduleGitMetadata struct {
	gitDir string
}

func readSubmoduleGitMetadata(directory *os.File, nested string) (submoduleGitMetadata, error) {
	gitFD, err := unix.Openat(int(directory.Fd()), ".git", unix.O_PATH|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return submoduleGitMetadata{}, err
	}
	gitFile := os.NewFile(uintptr(gitFD), filepath.Join(nested, ".git"))
	defer gitFile.Close()
	info, err := gitFile.Stat()
	if err != nil {
		return submoduleGitMetadata{}, err
	}
	if info.IsDir() {
		return submoduleGitMetadata{gitDir: filepath.Join(nested, ".git")}, nil
	}
	if !info.Mode().IsRegular() {
		return submoduleGitMetadata{}, fmt.Errorf("invalid submodule Git metadata")
	}
	readFD, err := unix.Openat(int(directory.Fd()), ".git", unix.O_RDONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return submoduleGitMetadata{}, err
	}
	reader := os.NewFile(uintptr(readFD), filepath.Join(nested, ".git"))
	defer reader.Close()
	content, err := io.ReadAll(io.LimitReader(reader, 4097))
	if err != nil {
		return submoduleGitMetadata{}, err
	}
	if len(content) > 4096 {
		return submoduleGitMetadata{}, fmt.Errorf("submodule Git metadata is too large")
	}
	line := strings.TrimSpace(string(content))
	if !strings.HasPrefix(line, "gitdir:") {
		return submoduleGitMetadata{}, fmt.Errorf("invalid submodule Git metadata")
	}
	target := strings.TrimSpace(strings.TrimPrefix(line, "gitdir:"))
	if target == "" || containsNUL(target) || strings.ContainsAny(target, "\r\n") {
		return submoduleGitMetadata{}, fmt.Errorf("invalid submodule Git metadata")
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(nested, target)
	}
	return submoduleGitMetadata{gitDir: filepath.Clean(target)}, nil
}

func canonicalGitMetadataPath(path, relativeTo string) (string, error) {
	if !filepath.IsAbs(path) {
		path = filepath.Join(relativeTo, path)
	}
	canonical, err := filepath.EvalSymlinks(filepath.Clean(path))
	if err != nil {
		return "", err
	}
	info, err := os.Stat(canonical)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("invalid Git metadata directory")
	}
	return canonical, nil
}

func admittedGitMetadataRoots(root string) []string {
	roots := []string{root}
	gitPath := filepath.Join(root, ".git")
	info, err := os.Lstat(gitPath)
	if err != nil {
		return roots
	}
	if info.IsDir() {
		return append(roots, gitPath)
	}
	if !info.Mode().IsRegular() {
		return roots
	}
	fd, err := unix.Open(gitPath, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return roots
	}
	reader := os.NewFile(uintptr(fd), gitPath)
	content, readErr := io.ReadAll(io.LimitReader(reader, 4097))
	_ = reader.Close()
	if readErr != nil || len(content) > 4096 {
		return roots
	}
	line := strings.TrimSpace(string(content))
	if !strings.HasPrefix(line, "gitdir:") {
		return roots
	}
	target := strings.TrimSpace(strings.TrimPrefix(line, "gitdir:"))
	if target == "" || containsNUL(target) || strings.ContainsAny(target, "\r\n") {
		return roots
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(root, target)
	}
	canonical, err := filepath.EvalSymlinks(filepath.Clean(target))
	if err != nil {
		return roots
	}
	if info, statErr := os.Stat(canonical); statErr == nil && info.IsDir() {
		roots = append(roots, canonical)
		// A linked worktree's .git file points at common.git/worktrees/name;
		// submodule gitdirs live beside that worktree metadata under common.git.
		if filepath.Base(filepath.Dir(canonical)) == "worktrees" {
			roots = append(roots, filepath.Dir(filepath.Dir(canonical)))
		}
	}
	return roots
}

func pathWithinAny(roots []string, candidate string) bool {
	for _, root := range roots {
		if Within(root, candidate) {
			return true
		}
	}
	return false
}

func validateGitRepositoryMetadata(ctx context.Context, admittedRoot, repository string) error {
	if !Within(admittedRoot, repository) {
		return fmt.Errorf("Git repository is outside the admitted project root")
	}
	rootIdentity, err := captureGitRepositoryIdentity(admittedRoot)
	if err != nil {
		return fmt.Errorf("could not verify Git project root: %w", err)
	}
	repositoryIdentity, err := captureGitRepositoryIdentity(repository)
	if err != nil {
		return fmt.Errorf("could not verify Git repository: %w", err)
	}
	directory, err := openProjectDirectory(admittedRoot, repository)
	if err != nil {
		return fmt.Errorf("could not open Git repository metadata: %w", err)
	}
	defer directory.Close()
	metadata, err := readSubmoduleGitMetadata(directory, repository)
	if err != nil {
		return fmt.Errorf("invalid Git repository metadata: %w", err)
	}
	gitDir, err := canonicalGitMetadataPath(metadata.gitDir, repository)
	if err != nil || !pathWithinAny(admittedGitMetadataRoots(admittedRoot), gitDir) {
		return fmt.Errorf("Git repository metadata is outside the admitted project root")
	}
	gitDirIdentity, err := captureGitRepositoryIdentity(gitDir)
	if err != nil {
		return fmt.Errorf("could not verify Git repository metadata: %w", err)
	}
	if err := revalidateGitRepositoryIdentity(admittedRoot, rootIdentity); err != nil {
		return err
	}
	if err := revalidateGitRepositoryIdentity(repository, repositoryIdentity); err != nil {
		return err
	}
	probe := runGit(ctx, repository, []string{"rev-parse", "--git-dir", "--show-toplevel"}, gitOutputLimit)
	if err := revalidateGitRepositoryIdentity(admittedRoot, rootIdentity); err != nil {
		return err
	}
	if err := revalidateGitRepositoryIdentity(repository, repositoryIdentity); err != nil {
		return err
	}
	if err := revalidateGitRepositoryIdentity(gitDir, gitDirIdentity); err != nil {
		return err
	}
	metadataAfter, metadataAfterErr := readSubmoduleGitMetadata(directory, repository)
	if metadataAfterErr != nil {
		return fmt.Errorf("Git repository metadata changed while it was inspected")
	}
	gitDirAfter, gitDirAfterErr := canonicalGitMetadataPath(metadataAfter.gitDir, repository)
	if gitDirAfterErr != nil || gitDirAfter != gitDir {
		return fmt.Errorf("Git repository metadata changed while it was inspected")
	}
	lines := strings.Split(strings.TrimSpace(probe.out), "\n")
	if !probe.ok || len(lines) != 2 {
		return fmt.Errorf("invalid Git repository metadata")
	}
	probedGitDir, err := canonicalGitMetadataPath(strings.TrimSpace(lines[0]), repository)
	if err != nil || probedGitDir != gitDir {
		return fmt.Errorf("Git repository metadata changed while it was inspected")
	}
	probedTop, err := filepath.EvalSymlinks(strings.TrimSpace(lines[1]))
	if err != nil || probedTop != repositoryIdentity.canonical {
		return fmt.Errorf("Git repository metadata points at a different worktree")
	}
	return nil
}

type contextReader struct {
	ctx context.Context
	io.Reader
}

func (r contextReader) Read(value []byte) (int, error) {
	select {
	case <-r.ctx.Done():
		return 0, r.ctx.Err()
	default:
		return r.Reader.Read(value)
	}
}

func hashGitBlobReader(ctx context.Context, reader io.Reader, size int64) (string, string, error) {
	if size < 0 {
		return "", "", fmt.Errorf("invalid Git blob size")
	}
	header := []byte("blob " + strconv.FormatInt(size, 10) + "\x00")
	sha1Hash, sha256Hash := sha1.New(), sha256.New()
	if _, err := sha1Hash.Write(header); err != nil {
		return "", "", err
	}
	if _, err := sha256Hash.Write(header); err != nil {
		return "", "", err
	}
	limited := io.LimitReader(contextReader{ctx: ctx, Reader: reader}, size)
	read, err := io.Copy(io.MultiWriter(sha1Hash, sha256Hash), limited)
	if err != nil {
		return "", "", err
	}
	if read != size {
		return "", "", fmt.Errorf("Git worktree file changed while it was read")
	}
	return hex.EncodeToString(sha1Hash.Sum(nil)), hex.EncodeToString(sha256Hash.Sum(nil)), nil
}

func hashGitBlobBytes(value []byte) (string, string) {
	sha1OID, sha256OID, _ := hashGitBlobReader(context.Background(), bytes.NewReader(value), int64(len(value)))
	return sha1OID, sha256OID
}

func rawSnapshotMatches(entry gitTreeEntry, snapshot rawWorktreeSnapshot) bool {
	if !snapshot.exists || !snapshot.accessible || snapshot.indeterminate {
		return false
	}
	if entry.mode != snapshot.mode {
		return false
	}
	return rawBlobSnapshotMatches(entry, snapshot)
}

func rawBlobSnapshotMatches(entry gitTreeEntry, snapshot rawWorktreeSnapshot) bool {
	if !snapshot.exists || !snapshot.accessible || snapshot.indeterminate {
		return false
	}
	if entry.mode == "160000" {
		return entry.oid == snapshot.submoduleOID
	}
	return len(entry.oid) == 40 && snapshot.sha1 == entry.oid || len(entry.oid) == 64 && snapshot.sha256 == entry.oid
}

func rawSnapshotMatchesWithFileMode(entry gitTreeEntry, snapshot rawWorktreeSnapshot, fileMode bool) bool {
	if !snapshot.exists || !snapshot.accessible || snapshot.indeterminate {
		return false
	}
	if entry.mode != snapshot.mode && (fileMode || !regularGitMode(entry.mode) || !regularGitMode(snapshot.mode)) {
		return false
	}
	return rawBlobSnapshotMatches(entry, snapshot)
}

func regularGitMode(mode string) bool {
	return mode == "100644" || mode == "100755"
}

func gitFileModeEnabled(ctx context.Context, repository string) (bool, error) {
	result := runGit(ctx, repository, []string{"config", "--bool", "--get", "--default=true", "core.fileMode"}, gitOutputLimit)
	if !result.ok {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		return false, fmt.Errorf("could not read Git file mode setting")
	}
	switch strings.TrimSpace(result.out) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, fmt.Errorf("invalid Git file mode setting")
	}
}

func rawWorktreeChanges(ctx context.Context, repository string, value diffRange) ([]GitFileChange, string, []string, error) {
	bounded, cancel := context.WithTimeout(ctx, gitRawWorktreeTTL)
	defer cancel()
	ctx = bounded
	base, err := gitTreeEntries(ctx, repository, value.originalRef)
	if err != nil {
		return nil, "", nil, err
	}
	index, err := gitIndexEntries(ctx, repository)
	if err != nil {
		return nil, "", nil, err
	}
	untracked, err := rawWorktreePaths(ctx, repository)
	if err != nil {
		return nil, "", nil, err
	}
	fileMode, fileModeErr := gitFileModeEnabled(ctx, repository)
	if fileModeErr != nil {
		if err := ctx.Err(); err != nil {
			return nil, "", nil, err
		}
		// Git's default is true. Keep the comparison conservative if the local
		// setting could not be read, and tell the caller that mode handling was
		// not fully verified.
		fileMode = true
	}
	names := make(map[string]struct{}, len(base)+len(index)+len(untracked))
	for name := range base {
		names[name] = struct{}{}
	}
	for name := range index {
		names[name] = struct{}{}
	}
	for _, name := range untracked {
		names[name] = struct{}{}
	}
	ordered := make([]string, 0, len(names))
	for name := range names {
		ordered = append(ordered, name)
	}
	sort.Strings(ordered)

	changes := make([]GitFileChange, 0)
	deleted := make([]rawChangeCandidate, 0)
	added := make([]rawChangeCandidate, 0)
	untrackedCount := 0
	budget := newRawHashBudget()
	readLimitReached := false
	readUnavailable := false
	submoduleMetadataInvalid := false
	for _, name := range ordered {
		if err := ctx.Err(); err != nil {
			return nil, "", nil, err
		}
		baseEntry, hasBase := base[name]
		indexEntry, hasIndex := index[name]
		if !hasBase && !hasIndex {
			snapshot := rawWorktreePresenceFor(repository, name)
			if !snapshot.exists {
				continue
			}
			change := GitFileChange{Path: name, Status: "untracked"}
			if untrackedCount < 64 {
				if addUntrackedCounts(repository, name, &change, budget) {
					readLimitReached = true
				}
			}
			untrackedCount++
			changes = append(changes, change)
			continue
		}
		// A path missing from the index is a staged deletion. Any restored
		// worktree entry is an untracked path with the same name and is not part
		// of `diff HEAD`, so do not let it turn the deletion into a modification.
		if hasBase && !hasIndex {
			addedLines, removedLines, limited := rawFileCountsWithBudget(ctx, repository, value.originalRef, name, "", budget)
			if limited {
				readLimitReached = true
			}
			candidate := rawChangeCandidate{path: name, base: baseEntry, snapshot: rawWorktreeSnapshot{}, change: GitFileChange{Path: name, Status: "deleted"}}
			candidate.change.Added, candidate.change.Removed = addedLines, removedLines
			deleted = append(deleted, candidate)
			continue
		}

		// The assume-unchanged and skip-worktree bits suppress worktree reads in
		// native `diff HEAD`. A staged index change remains visible, using the
		// index entry rather than whatever bytes happen to be on disk.
		if hasBase && hasIndex && (indexEntry.assumeUnchanged || indexEntry.skipWorktree) {
			if indexEntry.stage != 0 || !sameGitIndexEntry(baseEntry, indexEntry) {
				changes = append(changes, GitFileChange{Path: name, Status: "modified"})
			}
			continue
		}

		expectedMode := baseEntry.mode
		if !hasBase && hasIndex {
			expectedMode = indexEntry.mode
		}
		snapshot := rawWorktreeSnapshotFor(ctx, repository, name, expectedMode, budget)
		if snapshot.indeterminate {
			if snapshot.metadataInvalid {
				submoduleMetadataInvalid = true
			} else if snapshot.limit {
				readLimitReached = true
			} else {
				readUnavailable = true
			}
		}
		if snapshot.metadataInvalid {
			submoduleMetadataInvalid = true
		}
		if !snapshot.exists {
			// An indexed addition whose worktree file disappeared is absent from
			// the final `diff HEAD` result. A tracked file with a normal index entry
			// is a deletion, handled below.
			if hasBase {
				addedLines, removedLines, limited := rawFileCountsWithBudget(ctx, repository, value.originalRef, name, "", budget)
				if limited {
					readLimitReached = true
				}
				candidate := rawChangeCandidate{path: name, base: baseEntry, hasIndex: hasIndex, snapshot: snapshot, change: GitFileChange{Path: name, Status: "deleted"}}
				candidate.change.Added, candidate.change.Removed = addedLines, removedLines
				deleted = append(deleted, candidate)
			}
			continue
		}
		if !hasBase {
			addedLines, removedLines, limited := rawFileCountsWithBudget(ctx, repository, value.originalRef, "", name, budget)
			if limited {
				readLimitReached = true
			}
			candidate := rawChangeCandidate{path: name, hasIndex: hasIndex, snapshot: snapshot, change: GitFileChange{Path: name, Status: "added"}}
			candidate.change.Added, candidate.change.Removed = addedLines, removedLines
			if hasIndex {
				candidate.index = indexEntry
			}
			added = append(added, candidate)
			continue
		}
		if hasIndex && indexEntry.stage != 0 {
			changes = append(changes, GitFileChange{Path: name, Status: "modified"})
			continue
		}
		// `diff HEAD` includes the index. If the worktree was restored to the
		// HEAD bytes after staging a change, comparing only the worktree would
		// incorrectly report this path as clean. Compare the index mode/OID to
		// HEAD first, then use the index bytes for the staged-only accounting.
		if hasIndex && !sameGitIndexEntry(baseEntry, indexEntry) && rawBlobSnapshotMatches(baseEntry, snapshot) {
			addedLines, removedLines, limited := rawIndexFileCountsWithBudget(ctx, repository, value.originalRef, name, indexEntry, budget)
			if limited {
				readLimitReached = true
			}
			changes = append(changes, GitFileChange{Path: name, Status: "modified", Added: addedLines, Removed: removedLines})
			continue
		}
		if !rawSnapshotMatchesWithFileMode(baseEntry, snapshot, fileMode) {
			addedLines, removedLines, limited := rawFileCountsWithBudget(ctx, repository, value.originalRef, name, name, budget)
			if limited {
				readLimitReached = true
			}
			changes = append(changes, GitFileChange{Path: name, Status: "modified", Added: addedLines, Removed: removedLines})
		}
	}

	renameSources := make(map[string]bool)
	for index := range added {
		for source := range deleted {
			if renameSources[deleted[source].path] || !rawRenameMatch(deleted[source], added[index]) {
				continue
			}
			added[index].change.Status = "renamed"
			added[index].change.OriginalPath = deleted[source].path
			var limited bool
			added[index].change.Added, added[index].change.Removed, limited = rawFileCountsWithBudget(ctx, repository, value.originalRef, deleted[source].path, added[index].path, budget)
			if limited {
				readLimitReached = true
			}
			renameSources[deleted[source].path] = true
			break
		}
	}
	for _, candidate := range deleted {
		if !renameSources[candidate.path] {
			changes = append(changes, candidate.change)
		}
	}
	for _, candidate := range added {
		changes = append(changes, candidate.change)
	}
	if err := ctx.Err(); err != nil {
		return nil, "", nil, err
	}
	sort.Slice(changes, func(left, right int) bool { return changes[left].Path < changes[right].Path })
	warnings := make([]string, 0, 2)
	if readLimitReached {
		warnings = append(warnings, gitRawHashLimitWarning)
	}
	if readUnavailable || fileModeErr != nil {
		warnings = append(warnings, gitRawHashReadWarning)
	}
	if submoduleMetadataInvalid {
		warnings = append(warnings, gitRawSubmoduleWarning)
	}
	// Raw comparison deliberately does not run Git's clean/process/LFS
	// conversion machinery. Keep that limitation visible on repository status,
	// not only on an individual file preview.
	warnings = append(warnings, gitRawPreviewNote)
	return changes, value.comparisonID, warnings, nil
}

func sameGitIndexEntry(base gitTreeEntry, index gitIndexEntry) bool {
	return base.mode == index.mode && base.oid == index.oid
}

func rawRenameMatch(source, destination rawChangeCandidate) bool {
	if source.base.mode == "160000" || !renameModesCompatible(source.base.mode, destination.snapshot.mode) {
		return false
	}
	if destination.hasIndex && renameModesCompatible(source.base.mode, destination.index.mode) && destination.index.oid == source.base.oid {
		return true
	}
	return rawBlobSnapshotMatches(source.base, destination.snapshot)
}

func renameModesCompatible(source, destination string) bool {
	return source == destination || regularGitMode(source) && regularGitMode(destination)
}

func addUntrackedCounts(repository, name string, change *GitFileChange, budget *rawHashBudget) bool {
	preview, limited, err := readWorktreePreviewWithBudget(repository, filepath.FromSlash(name), budget)
	if limited {
		return true
	}
	if err == nil && preview.issue == "" {
		added := lineCount(preview.content)
		removed := 0
		change.Added, change.Removed = &added, &removed
	}
	return false
}

// rawFileCounts asks a Git process running outside the repository to compare
// already-read raw bytes. Running --no-index from a scratch directory keeps
// repository attributes, local includes and info/attributes out of the
// command's lookup path while retaining Git's normal numstat accounting.
func rawFileCounts(ctx context.Context, repository, baseRef, originalName, modifiedName string) (*int, *int) {
	added, removed, _ := rawFileCountsWithBudget(ctx, repository, baseRef, originalName, modifiedName, nil)
	return added, removed
}

func rawFileCountsWithBudget(ctx context.Context, repository, baseRef, originalName, modifiedName string, budget *rawHashBudget) (*int, *int, bool) {
	original := filePreview{issue: "missing"}
	if originalName != "" && baseRef != "" {
		var limited bool
		original, limited = readBlobPreviewWithBudget(ctx, repository, baseRef, originalName, budget)
		if limited {
			return nil, nil, true
		}
	}
	modified := filePreview{issue: "missing"}
	if modifiedName != "" {
		var err error
		var limited bool
		modified, limited, err = readWorktreePreviewWithBudget(repository, filepath.FromSlash(modifiedName), budget)
		if limited {
			return nil, nil, true
		}
		if err != nil {
			return nil, nil, false
		}
	}
	if (original.issue != "" && original.issue != "missing") || (modified.issue != "" && modified.issue != "missing") {
		return nil, nil, false
	}
	added, removed := rawPreviewCounts(ctx, original, modified)
	return added, removed, false
}

func rawIndexFileCounts(ctx context.Context, repository, baseRef, name string, index gitIndexEntry) (*int, *int) {
	added, removed, _ := rawIndexFileCountsWithBudget(ctx, repository, baseRef, name, index, nil)
	return added, removed
}

func rawIndexFileCountsWithBudget(ctx context.Context, repository, baseRef, name string, index gitIndexEntry, budget *rawHashBudget) (*int, *int, bool) {
	if baseRef == "" {
		return nil, nil, false
	}
	original, limited := readBlobPreviewWithBudget(ctx, repository, baseRef, name, budget)
	if limited {
		return nil, nil, true
	}
	modified, limited := readIndexBlobPreviewWithBudget(ctx, repository, index, budget)
	if limited {
		return nil, nil, true
	}
	if (original.issue != "" && original.issue != "missing") || (modified.issue != "" && modified.issue != "missing") {
		return nil, nil, false
	}
	added, removed := rawPreviewCounts(ctx, original, modified)
	return added, removed, false
}

func rawPreviewCounts(ctx context.Context, original, modified filePreview) (*int, *int) {
	scratch, err := os.MkdirTemp(os.TempDir(), "pixie-git-numstat-")
	if err != nil {
		return nil, nil
	}
	defer os.RemoveAll(scratch)
	oldPath, newPath := filepath.Join(scratch, "old"), filepath.Join(scratch, "new")
	if original.issue == "missing" {
		oldPath = os.DevNull
	} else if err := os.WriteFile(oldPath, []byte(original.content), 0o600); err != nil {
		return nil, nil
	}
	if modified.issue == "missing" {
		newPath = os.DevNull
	} else if err := os.WriteFile(newPath, []byte(modified.content), 0o600); err != nil {
		return nil, nil
	}
	result := runGit(ctx, scratch, []string{"diff", "--no-index", "--no-ext-diff", "--no-textconv", "--numstat", "-z", "--", oldPath, newPath}, gitOutputLimit)
	if result.failure != "" || result.out == "" {
		return nil, nil
	}
	counts := parseNumstat(result.out)
	for _, value := range counts {
		added, removed := value[0], value[1]
		return &added, &removed
	}
	return nil, nil
}

func rawOriginalPath(ctx context.Context, repository, baseRef, name string) (string, error) {
	bounded, cancel := context.WithTimeout(ctx, gitRawWorktreeTTL)
	defer cancel()
	ctx = bounded
	base, err := gitTreeEntries(ctx, repository, baseRef)
	if err != nil {
		return "", err
	}
	if _, exists := base[name]; exists {
		return "", nil
	}
	index, err := gitIndexEntries(ctx, repository)
	if err != nil {
		return "", err
	}
	destinationIndex, ok := index[name]
	if !ok || destinationIndex.stage != 0 {
		// A destination absent from the index is untracked. Git does not infer a
		// rename for it, even if its bytes equal a deleted tracked file.
		return "", nil
	}
	budget := newRawHashBudget()
	destination := rawWorktreeSnapshotFor(ctx, repository, name, destinationIndex.mode, budget)
	if !destination.exists || destination.indeterminate {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		return "", nil
	}
	paths := make([]string, 0, len(base))
	for path := range base {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		source := base[path]
		if source.mode == "160000" {
			continue
		}
		old := rawWorktreeSnapshotFor(ctx, repository, path, source.mode, budget)
		if old.exists || old.indeterminate {
			continue
		}
		candidate := rawChangeCandidate{base: source, snapshot: destination}
		candidate.hasIndex, candidate.index = true, destinationIndex
		if rawRenameMatch(rawChangeCandidate{base: source}, candidate) {
			return path, nil
		}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return "", nil
}

func (g *Git) changes(ctx context.Context, repository string, scope GitDiffScope) ([]GitFileChange, string, []string, error) {
	rangeValue, err := resolveDiffRange(ctx, repository, scope)
	if err != nil {
		return nil, "", nil, err
	}
	if rangeValue.rawWorktree {
		return rawWorktreeChanges(ctx, repository, rangeValue)
	}
	countsResult := runGit(ctx, repository, changedArgs(rangeValue, "--numstat"), gitOutputLimit)
	tracked := runGit(ctx, repository, changedArgs(rangeValue, "--name-status"), gitOutputLimit)
	if !countsResult.ok || !tracked.ok {
		return nil, "", nil, fmt.Errorf("could not read changed files")
	}
	counts := parseNumstat(countsResult.out)
	result := parseNameStatus(tracked.out, counts)
	if rangeValue.untracked {
		untracked := runGit(ctx, repository, []string{"ls-files", "-z", "--others", "--exclude-standard"}, gitOutputLimit)
		if !untracked.ok {
			return nil, "", nil, fmt.Errorf("could not read untracked files: %s", untracked.err)
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
	return result, rangeValue.comparisonID, nil, nil
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

func (g *Git) DiffFile(ctx context.Context, projectID, repository, name string, scope GitDiffScope) (result GitDiffFile, err error) {
	_, admitted, err := g.repositoryFor(ctx, projectID, repository)
	if err != nil {
		return GitDiffFile{}, err
	}
	identity, err := captureGitRepositoryIdentity(admitted)
	if err != nil {
		return GitDiffFile{}, fmt.Errorf("could not verify Git repository: %w", err)
	}
	defer func() {
		if err == nil {
			if identityErr := revalidateGitRepositoryIdentity(admitted, identity); identityErr != nil {
				result = GitDiffFile{}
				err = identityErr
			}
		}
	}()
	rangeValue, err := resolveDiffRange(ctx, admitted, scope)
	if err != nil {
		return GitDiffFile{}, err
	}
	if rangeValue.rawWorktree {
		bounded, cancel := context.WithTimeout(ctx, gitRawWorktreeTTL)
		defer cancel()
		ctx = bounded
	}
	finish := func(result GitDiffFile) GitDiffFile {
		if rangeValue.rawWorktree && result.Message == "" {
			result.Message = gitRawPreviewNote
		}
		return result
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
		if rangeValue.rawWorktree {
			originalPath, err = rawOriginalPath(ctx, admitted, rangeValue.originalRef, name)
			if err != nil {
				return GitDiffFile{}, err
			}
			if originalPath != "" {
				originalName = originalPath
			}
		} else {
			changed := runGit(ctx, admitted, changedArgs(rangeValue, "--name-status"), gitOutputLimit)
			if !changed.ok {
				return finish(unavailableDiff(filePreview{issue: "unavailable"}, "", rangeValue.comparisonID)), nil
			}
			for _, change := range parseNameStatus(changed.out, nil) {
				if change.Path == name && change.OriginalPath != "" {
					originalPath, originalName = change.OriginalPath, change.OriginalPath
					break
				}
			}
		}
		if _, _, err := relativePathInRoot(admitted, filepath.FromSlash(originalName)); err != nil {
			return GitDiffFile{}, err
		}
		original = readBlobPreview(ctx, admitted, rangeValue.originalRef, originalName)
	}
	if original.issue != "" && original.issue != "missing" {
		return finish(unavailableDiff(original, originalPath, rangeValue.comparisonID)), nil
	}
	modified := filePreview{}
	if rangeValue.modifiedRef != "" {
		modified = readBlobPreview(ctx, admitted, rangeValue.modifiedRef, name)
	} else if rangeValue.rawWorktree {
		modified, err = rawWorktreeDiffPreview(ctx, admitted, rangeValue.originalRef, name)
		if err != nil && errors.Is(err, errPathEscapesProjectRoot) {
			return GitDiffFile{}, err
		}
		if err != nil {
			modified = filePreview{issue: "unavailable"}
		}
	} else {
		modified, err = readWorktreePreview(admitted, filepath.FromSlash(name))
		if err != nil && errors.Is(err, errPathEscapesProjectRoot) {
			return GitDiffFile{}, err
		}
	}
	if modified.issue == "missing" {
		if original.issue == "missing" {
			return finish(unavailableDiff(modified, originalPath, rangeValue.comparisonID)), nil
		}
		return finish(GitDiffFile{Original: original.content, OriginalPath: originalPath, ComparisonID: rangeValue.comparisonID}), nil
	}
	if modified.issue != "" {
		return finish(unavailableDiff(modified, originalPath, rangeValue.comparisonID)), nil
	}
	return finish(GitDiffFile{Original: original.content, Modified: modified.content, OriginalPath: originalPath, ComparisonID: rangeValue.comparisonID}), nil
}

func rawWorktreeDiffPreview(ctx context.Context, repository, baseRef, name string) (filePreview, error) {
	base, err := gitTreeEntries(ctx, repository, baseRef)
	if err != nil {
		return filePreview{}, err
	}
	index, err := gitIndexEntries(ctx, repository)
	if err != nil {
		return filePreview{}, err
	}
	baseEntry, hasBase := base[name]
	indexEntry, hasIndex := index[name]
	if hasBase && !hasIndex {
		// A staged deletion removes the path from the final diff, even if an
		// untracked file with the same name was restored in the worktree.
		return filePreview{issue: "missing"}, nil
	}
	if hasIndex && indexEntry.stage != 0 {
		return readIndexBlobPreview(ctx, repository, indexEntry), nil
	}
	if hasBase && hasIndex && (indexEntry.assumeUnchanged || indexEntry.skipWorktree) {
		if sameGitIndexEntry(baseEntry, indexEntry) {
			return readBlobPreview(ctx, repository, baseRef, name), nil
		}
		return readIndexBlobPreview(ctx, repository, indexEntry), nil
	}
	if hasBase && hasIndex && !sameGitIndexEntry(baseEntry, indexEntry) {
		// If the worktree still contains the HEAD bytes, this is a staged-only
		// change and the preview must come from the index. Otherwise the final
		// diff is against the current worktree bytes.
		snapshot := rawWorktreeSnapshotFor(ctx, repository, name, baseEntry.mode, newRawHashBudget())
		if rawBlobSnapshotMatches(baseEntry, snapshot) {
			return readIndexBlobPreview(ctx, repository, indexEntry), nil
		}
	}
	return readWorktreePreview(repository, filepath.FromSlash(name))
}

func readIndexBlobPreview(ctx context.Context, repository string, entry gitIndexEntry) filePreview {
	preview, _ := readIndexBlobPreviewWithBudget(ctx, repository, entry, nil)
	return preview
}

func readIndexBlobPreviewWithBudget(ctx context.Context, repository string, entry gitIndexEntry, budget *rawHashBudget) (filePreview, bool) {
	if entry.mode == "160000" {
		return filePreview{issue: "unavailable"}, false
	}
	sizeResult := runGit(ctx, repository, []string{"cat-file", "-s", entry.oid}, gitOutputLimit)
	if !sizeResult.ok {
		return filePreview{issue: "unavailable"}, false
	}
	bytesCount, err := strconv.ParseInt(strings.TrimSpace(sizeResult.out), 10, 64)
	if err != nil || bytesCount < 0 {
		return filePreview{issue: "unavailable"}, false
	}
	if bytesCount > gitPreviewMaxBytes {
		return filePreview{issue: "tooLarge"}, false
	}
	readSize := bytesCount + 1
	if readSize < 1 {
		readSize = 1
	}
	if budget != nil && !budget.reserve(readSize) {
		return filePreview{issue: "tooLarge"}, true
	}
	shown := runGit(ctx, repository, []string{"cat-file", "blob", entry.oid}, gitPreviewMaxBytes)
	if shown.failure == "output-limit" {
		return filePreview{issue: "tooLarge"}, false
	}
	if !shown.ok {
		return filePreview{issue: "unavailable"}, false
	}
	if strings.IndexByte(shown.out, 0) >= 0 {
		return filePreview{issue: "binary"}, false
	}
	if !utf8.ValidString(shown.out) {
		return filePreview{issue: "encoding"}, false
	}
	return filePreview{content: shown.out}, false
}

func (g *Git) ListCommits(ctx context.Context, projectID, repository string) (result map[string]any, err error) {
	_, admitted, err := g.repositoryFor(ctx, projectID, repository)
	if err != nil {
		return nil, err
	}
	identity, err := captureGitRepositoryIdentity(admitted)
	if err != nil {
		return nil, fmt.Errorf("could not verify Git repository: %w", err)
	}
	defer func() {
		if err == nil {
			if identityErr := revalidateGitRepositoryIdentity(admitted, identity); identityErr != nil {
				result = nil
				err = identityErr
			}
		}
	}()
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

func (g *Git) ListBranches(ctx context.Context, projectID, repository string) (result GitBranchList, err error) {
	_, admitted, err := g.repositoryFor(ctx, projectID, repository)
	if err != nil {
		return GitBranchList{}, err
	}
	identity, err := captureGitRepositoryIdentity(admitted)
	if err != nil {
		return GitBranchList{}, fmt.Errorf("could not verify Git repository: %w", err)
	}
	defer func() {
		if err == nil {
			if identityErr := revalidateGitRepositoryIdentity(admitted, identity); identityErr != nil {
				result = GitBranchList{}
				err = identityErr
			}
		}
	}()
	gitResult := runGit(ctx, admitted, []string{
		"for-each-ref",
		fmt.Sprintf("--count=%d", gitBranchLimit+1),
		"--sort=refname",
		"--format=%(refname)%00%(symref)%00%(objecttype)",
		"--",
		"refs/heads",
		"refs/remotes",
	}, gitOutputLimit)
	if !gitResult.ok {
		if err := ctx.Err(); err != nil {
			return GitBranchList{}, err
		}
		return GitBranchList{}, &codedError{code: "GIT_BRANCHES_UNAVAILABLE", message: "Could not read branches"}
	}
	listed := GitBranchList{Branches: []GitBranch{}}
	records := strings.Split(strings.TrimSuffix(gitResult.out, "\n"), "\n")
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
	preview, _, err := readWorktreePreviewWithBudget(root, name, nil)
	return preview, err
}

func readWorktreePreviewWithBudget(root, name string, budget *rawHashBudget) (filePreview, bool, error) {
	// A requested symlink previews its bounded link text, not target contents.
	// Direct files are still opened and checked through the same descriptor walk.
	file, info, linkText, err := openProjectLinkPreview(root, name)
	if err != nil {
		if errors.Is(err, errPathEscapesProjectRoot) {
			return filePreview{}, false, err
		}
		if errors.Is(err, errProjectFileTooLarge) {
			return filePreview{issue: "tooLarge"}, false, nil
		}
		if errors.Is(err, os.ErrNotExist) {
			return filePreview{issue: "missing"}, false, nil
		}
		return filePreview{issue: "unavailable"}, false, nil
	}
	if file == nil {
		if !utf8.ValidString(linkText) {
			return filePreview{issue: "encoding"}, false, nil
		}
		readSize := int64(len(linkText))
		if budget != nil && !budget.reserve(readSize) {
			return filePreview{issue: "tooLarge"}, true, nil
		}
		return filePreview{content: linkText}, false, nil
	}
	defer file.Close()
	if linkText != "" {
		if !utf8.ValidString(linkText) {
			return filePreview{issue: "encoding"}, false, nil
		}
		readSize := int64(len(linkText))
		if budget != nil && !budget.reserve(readSize) {
			return filePreview{issue: "tooLarge"}, true, nil
		}
		return filePreview{content: linkText}, false, nil
	}
	if info.Size() > gitPreviewMaxBytes {
		return filePreview{issue: "tooLarge"}, false, nil
	}
	readSize := info.Size() + 1
	if readSize < 1 {
		readSize = 1
	}
	if budget != nil && !budget.reserve(readSize) {
		return filePreview{issue: "tooLarge"}, true, nil
	}
	content, err := io.ReadAll(io.LimitReader(file, gitPreviewMaxBytes+1))
	if err != nil {
		return filePreview{issue: "unavailable"}, false, nil
	}
	if len(content) > gitPreviewMaxBytes {
		return filePreview{issue: "tooLarge"}, false, nil
	}
	if bytes.IndexByte(content, 0) >= 0 {
		return filePreview{issue: "binary"}, false, nil
	}
	if !utf8.Valid(content) {
		return filePreview{issue: "encoding"}, false, nil
	}
	return filePreview{content: string(content)}, false, nil
}

func readBlobPreview(ctx context.Context, repository, ref, name string) filePreview {
	preview, _ := readBlobPreviewWithBudget(ctx, repository, ref, name, nil)
	return preview
}

func readBlobPreviewWithBudget(ctx context.Context, repository, ref, name string, budget *rawHashBudget) (filePreview, bool) {
	entry := runGit(ctx, repository, []string{"ls-tree", "--full-tree", "-l", "-z", "--abbrev=64", ref, "--", ":(literal)" + name}, gitOutputLimit)
	if !entry.ok {
		return filePreview{issue: "unavailable"}, false
	}
	if entry.out == "" {
		return filePreview{issue: "missing"}, false
	}
	metadata, entryName, _ := strings.Cut(entry.out, "\t")
	fields := strings.Fields(metadata)
	if len(fields) != 4 || fields[1] != "blob" || !fullGitOID(fields[2]) || entryName != name+"\x00" {
		return filePreview{issue: "unavailable"}, false
	}
	bytesCount, err := strconv.Atoi(fields[3])
	if err != nil || bytesCount < 0 {
		return filePreview{issue: "unavailable"}, false
	}
	if bytesCount > gitPreviewMaxBytes {
		return filePreview{issue: "tooLarge"}, false
	}
	readSize := int64(bytesCount) + 1
	if budget != nil && !budget.reserve(readSize) {
		return filePreview{issue: "tooLarge"}, true
	}
	shown := runGit(ctx, repository, []string{"cat-file", "blob", fields[2]}, gitPreviewMaxBytes)
	if shown.failure == "output-limit" {
		return filePreview{issue: "tooLarge"}, false
	}
	if !shown.ok {
		return filePreview{issue: "unavailable"}, false
	}
	if strings.IndexByte(shown.out, 0) >= 0 {
		return filePreview{issue: "binary"}, false
	}
	if !utf8.ValidString(shown.out) {
		return filePreview{issue: "encoding"}, false
	}
	return filePreview{content: shown.out}, false
}

func unavailableDiff(preview filePreview, originalPath, comparisonID string) GitDiffFile {
	result := GitDiffFile{Unavailable: true, OriginalPath: originalPath, ComparisonID: comparisonID}
	switch preview.issue {
	case "binary":
		result.Binary, result.Message = true, "Binary files cannot be previewed"
	case "tooLarge":
		result.TooLarge, result.Message = true, "File is too large to preview"
	case "encoding":
		result.Message = "File is not valid UTF-8"
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
