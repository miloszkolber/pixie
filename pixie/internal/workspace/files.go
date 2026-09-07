package workspace

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/sys/unix"
)

const (
	fileListLimit  = 2_000
	fileReadLimit  = 4 * 1024 * 1024
	directoryLimit = 10_000
	directoryBatch = 128
	pageLimit      = 99
	defaultPage    = 100
)

var (
	errPathEscapesProjectRoot = errors.New("path escapes the project root")
	errProjectFileTooLarge    = errors.New("project file exceeds size limit")
)

type Files struct {
	projects *Projects
	policy   *PathPolicy
}

func NewFiles(projects *Projects, policy *PathPolicy) *Files {
	return &Files{projects: projects, policy: policy}
}

func (f *Files) ReadDir(projectID, path string) (FileListing, error) {
	root, err := f.projects.Root(projectID)
	if err != nil {
		return FileListing{}, err
	}
	relative, absolute, err := relativePathInRootAllowRoot(root, path, true)
	if err != nil {
		return FileListing{}, err
	}
	directory, err := openProjectDirectory(root, path)
	if err != nil {
		return FileListing{}, err
	}
	defer directory.Close()
	// One overflow entry and the excluded .git entry are sufficient to decide
	// completeness without materializing the entire directory.
	entries, err := directory.ReadDir(fileListLimit + 2)
	if err != nil && !errors.Is(err, io.EOF) {
		return FileListing{}, err
	}
	visible := entries[:0]
	for _, entry := range entries {
		if entry.Name() != ".git" {
			visible = append(visible, entry)
		}
	}
	entries = visible
	complete := len(entries) <= fileListLimit
	if len(entries) > fileListLimit {
		entries = entries[:fileListLimit]
	}
	nodes := make([]FileNode, 0, len(entries))
	for _, entry := range entries {
		candidate := filepath.Join(absolute, entry.Name())
		kind := "file"
		if entry.IsDir() {
			kind = "dir"
		} else if entry.Type()&os.ModeSymlink != 0 {
			// DirEntry reports symlinks as files. Re-check them through the
			// descriptor walk so classification does not introduce a
			// resolve/open race or follow an unadmitted target.
			if directory, directoryErr := openProjectDirectory(root, filepath.Join(relative, entry.Name())); directoryErr == nil {
				kind = "dir"
				_ = directory.Close()
			} else if file, _, _, fileErr := openProjectRegularFile(root, filepath.Join(relative, entry.Name()), 1<<63-1); fileErr != nil {
				continue
			} else {
				_ = file.Close()
			}
		}
		relative, err := filepath.Rel(root, candidate)
		if err != nil {
			continue
		}
		nodes = append(nodes, FileNode{Path: relative, Name: entry.Name(), Kind: kind})
	}
	sort.Slice(nodes, func(i, j int) bool {
		if nodes[i].Kind != nodes[j].Kind {
			return nodes[i].Kind == "dir"
		}
		return nodes[i].Name < nodes[j].Name
	})
	warnings := []string{}
	if !complete {
		warnings = append(warnings, "File list reached its 2,000-entry safety limit.")
	}
	return FileListing{Nodes: nodes, Complete: complete, Warnings: warnings}, nil
}

func (f *Files) ReadFile(projectID, path string) (string, error) {
	root, err := f.projects.Root(projectID)
	if err != nil {
		return "", err
	}
	file, _, _, err := openProjectRegularFile(root, path, fileReadLimit)
	if err != nil {
		return "", err
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, fileReadLimit+1))
	if err != nil {
		return "", err
	}
	if int64(len(content)) > fileReadLimit {
		return "", fmt.Errorf("%w: %d-byte limit", errProjectFileTooLarge, fileReadLimit)
	}
	return string(content), nil
}

// OpenRegularFileInRoot opens path and proves each resolved component remains
// below root. The returned display path is lexical only and must not be opened.
func (f *Files) OpenRegularFileInRoot(root, path string, limit int64) (*os.File, os.FileInfo, string, error) {
	file, info, _, err := openProjectRegularFile(root, path, limit)
	if err != nil {
		return nil, nil, "", err
	}
	relative, _, err := relativePathInRoot(root, path)
	if err != nil {
		_ = file.Close()
		return nil, nil, "", err
	}
	return file, info, relative, nil
}

func relativePathInRoot(root, path string) (string, string, error) {
	return relativePathInRootAllowRoot(root, path, false)
}

func relativePathInRootAllowRoot(root, path string, allowRoot bool) (string, string, error) {
	if containsNUL(path) {
		return "", "", fmt.Errorf("invalid project file path")
	}
	candidate := filepath.Clean(path)
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(root, candidate)
	}
	if !Within(root, candidate) {
		return "", "", fmt.Errorf("%w", errPathEscapesProjectRoot)
	}
	relative, err := filepath.Rel(root, candidate)
	if err != nil || relative == "." && !allowRoot {
		return "", "", fmt.Errorf("project file is not a regular file")
	}
	return relative, candidate, nil
}

// openProjectRegularFile uses a descriptor walk rather than resolving a path
// and opening it later. Every lookup is relative to an already-opened parent
// descriptor and uses O_NOFOLLOW. Symlinks are read as links, normalized below
// the root descriptor, and then traversed from descriptors again.
func openProjectRegularFile(root, path string, limit int64) (*os.File, os.FileInfo, string, error) {
	return openProjectPath(root, path, limit, false, false)
}

func openProjectLinkPreview(root, path string) (*os.File, os.FileInfo, string, error) {
	return openProjectPath(root, path, 1<<63-1, false, true)
}

func openProjectDirectory(root, path string) (*os.File, error) {
	directory, _, _, err := openProjectPath(root, path, 0, true, false)
	return directory, err
}

func openProjectPath(root, path string, limit int64, directory, previewLinks bool) (*os.File, os.FileInfo, string, error) {
	relative, display, err := relativePathInRootAllowRoot(root, path, directory)
	if err != nil {
		return nil, nil, "", err
	}
	rootFD, err := unix.Open(root, unix.O_PATH|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, nil, "", err
	}
	rootFile := os.NewFile(uintptr(rootFD), root)
	stack := []*os.File{rootFile}
	defer func() {
		for index := len(stack) - 1; index >= 0; index-- {
			_ = stack[index].Close()
		}
	}()

	pending := splitProjectPath(relative)
	links := 0
	linkText := ""
	hasFinalLink := false
	if len(pending) == 0 {
		if !directory {
			return nil, nil, "", fmt.Errorf("project file is not a regular file")
		}
		return openDirectoryAt(rootFile, ".", display)
	}
	for len(pending) > 0 {
		component := pending[0]
		pending = pending[1:]
		switch component {
		case "", ".":
			continue
		case "..":
			if len(stack) == 1 {
				return nil, nil, "", fmt.Errorf("%w", errPathEscapesProjectRoot)
			}
			_ = stack[len(stack)-1].Close()
			stack = stack[:len(stack)-1]
			continue
		}

		parent := stack[len(stack)-1]
		entryFD, openErr := unix.Openat(int(parent.Fd()), component, unix.O_PATH|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if openErr != nil {
			return nil, nil, "", openErr
		}
		entry := os.NewFile(uintptr(entryFD), component)
		info, statErr := entry.Stat()
		if statErr != nil {
			_ = entry.Close()
			return nil, nil, "", statErr
		}
		if info.Mode()&os.ModeSymlink != 0 {
			target, readErr := readLinkAt(int(parent.Fd()), component)
			_ = entry.Close()
			if readErr != nil {
				return nil, nil, "", readErr
			}
			links++
			if links > 40 {
				return nil, nil, "", fmt.Errorf("too many symbolic links")
			}
			if previewLinks && len(pending) == 0 {
				if validateErr := validatePreviewLinkTarget(root, stack, target); validateErr != nil {
					return nil, nil, "", validateErr
				}
				return nil, nil, target, nil
			}
			if len(pending) == 0 && !hasFinalLink {
				linkText, hasFinalLink = target, true
			}
			if filepath.IsAbs(target) {
				target = filepath.Clean(target)
				if !Within(root, target) {
					return nil, nil, "", fmt.Errorf("%w", errPathEscapesProjectRoot)
				}
				target, readErr = filepath.Rel(root, target)
				if readErr != nil {
					return nil, nil, "", readErr
				}
				for len(stack) > 1 {
					_ = stack[len(stack)-1].Close()
					stack = stack[:len(stack)-1]
				}
			}
			pending = append(splitProjectPath(target), pending...)
			continue
		}
		if len(pending) == 0 {
			_ = entry.Close()
			if directory {
				return openDirectoryAt(parent, component, display)
			}
			fileFD, openErr := unix.Openat(int(parent.Fd()), component, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_NOCTTY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
			if openErr != nil {
				return nil, nil, "", openErr
			}
			file := os.NewFile(uintptr(fileFD), display)
			fileInfo, fileStatErr := file.Stat()
			if fileStatErr != nil {
				_ = file.Close()
				return nil, nil, "", fileStatErr
			}
			if !fileInfo.Mode().IsRegular() {
				_ = file.Close()
				return nil, nil, "", fmt.Errorf("only regular files can be read")
			}
			if fileInfo.Size() > limit {
				_ = file.Close()
				return nil, nil, "", fmt.Errorf("%w: %d-byte limit", errProjectFileTooLarge, limit)
			}
			return file, fileInfo, linkText, nil
		}
		if !info.IsDir() {
			_ = entry.Close()
			return nil, nil, "", fmt.Errorf("project file path contains a non-directory")
		}
		stack = append(stack, entry)
	}
	if directory {
		return openDirectoryAt(stack[len(stack)-1], ".", display)
	}
	return nil, nil, "", fmt.Errorf("project file is not a regular file")
}

func openDirectoryAt(parent *os.File, name, display string) (*os.File, os.FileInfo, string, error) {
	directoryFD, err := unix.Openat(int(parent.Fd()), name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NONBLOCK|unix.O_NOCTTY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, nil, "", err
	}
	directory := os.NewFile(uintptr(directoryFD), display)
	info, err := directory.Stat()
	if err != nil {
		_ = directory.Close()
		return nil, nil, "", err
	}
	if !info.IsDir() {
		_ = directory.Close()
		return nil, nil, "", fmt.Errorf("project path is not a directory")
	}
	return directory, info, "", nil
}

func splitProjectPath(path string) []string {
	if path == "" || path == "." {
		return nil
	}
	return strings.Split(filepath.Clean(path), string(filepath.Separator))
}

func validatePreviewLinkTarget(root string, parentStack []*os.File, target string) error {
	type descriptor struct {
		file  *os.File
		owned bool
	}
	stack := make([]descriptor, len(parentStack))
	for index, file := range parentStack {
		stack[index] = descriptor{file: file}
	}
	closeOwned := func(entries []descriptor) {
		for index := len(entries) - 1; index >= 0; index-- {
			if entries[index].owned {
				_ = entries[index].file.Close()
			}
		}
	}
	defer func() {
		closeOwned(stack)
	}()

	pending, err := previewTargetComponents(root, target)
	if err != nil {
		return err
	}
	if filepath.IsAbs(target) {
		stack = stack[:1]
	}
	links := 0
	for len(pending) > 0 {
		component := pending[0]
		pending = pending[1:]
		switch component {
		case "", ".":
			continue
		case "..":
			if len(stack) == 1 {
				return fmt.Errorf("%w", errPathEscapesProjectRoot)
			}
			if stack[len(stack)-1].owned {
				_ = stack[len(stack)-1].file.Close()
			}
			stack = stack[:len(stack)-1]
			continue
		}

		parent := stack[len(stack)-1].file
		entryFD, openErr := unix.Openat(int(parent.Fd()), component, unix.O_PATH|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if errors.Is(openErr, unix.ENOENT) || errors.Is(openErr, unix.ENOTDIR) {
			if previewUnresolvedTargetEscapes(len(stack)-1, append([]string{component}, pending...)) {
				return fmt.Errorf("%w", errPathEscapesProjectRoot)
			}
			return nil
		}
		if openErr != nil {
			return openErr
		}
		entry := os.NewFile(uintptr(entryFD), component)
		info, statErr := entry.Stat()
		if statErr != nil {
			_ = entry.Close()
			return statErr
		}
		if info.Mode()&os.ModeSymlink != 0 {
			nestedTarget, readErr := readLinkAt(int(parent.Fd()), component)
			_ = entry.Close()
			if readErr != nil {
				return readErr
			}
			links++
			if links > 40 {
				return fmt.Errorf("too many symbolic links")
			}
			nested, targetErr := previewTargetComponents(root, nestedTarget)
			if targetErr != nil {
				return targetErr
			}
			if filepath.IsAbs(nestedTarget) {
				closeOwned(stack[1:])
				stack = stack[:1]
			}
			pending = append(nested, pending...)
			continue
		}
		if len(pending) == 0 || !info.IsDir() {
			_ = entry.Close()
			return nil
		}
		stack = append(stack, descriptor{file: entry, owned: true})
	}
	return nil
}

func previewTargetComponents(root, target string) ([]string, error) {
	if !filepath.IsAbs(target) {
		return strings.Split(target, string(filepath.Separator)), nil
	}
	cleanRoot := filepath.Clean(root)
	if !Within(cleanRoot, filepath.Clean(target)) {
		return nil, fmt.Errorf("%w", errPathEscapesProjectRoot)
	}
	if cleanRoot == string(filepath.Separator) {
		return strings.Split(strings.TrimPrefix(target, cleanRoot), string(filepath.Separator)), nil
	}
	if target == cleanRoot {
		return nil, nil
	}
	prefix := cleanRoot + string(filepath.Separator)
	if !strings.HasPrefix(target, prefix) {
		return nil, fmt.Errorf("%w", errPathEscapesProjectRoot)
	}
	return strings.Split(strings.TrimPrefix(target, prefix), string(filepath.Separator)), nil
}

func previewUnresolvedTargetEscapes(depth int, components []string) bool {
	for _, component := range components {
		switch component {
		case "", ".":
			continue
		case "..":
			if depth == 0 {
				return true
			}
			depth--
		default:
			depth++
		}
	}
	return false
}

func readLinkAt(directoryFD int, name string) (string, error) {
	buffer := make([]byte, 4*1024+1)
	count, err := unix.Readlinkat(directoryFD, name, buffer)
	if err != nil {
		return "", err
	}
	if count == len(buffer) {
		return "", fmt.Errorf("symbolic link target is too long")
	}
	return string(buffer[:count]), nil
}

// root must come from a freshly authorized project snapshot.
func (f *Files) ResolveInRoot(root, path string) (string, string, error) {
	candidate := filepath.Clean(path)
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(root, candidate)
	}
	if !Within(root, candidate) {
		return "", "", fmt.Errorf("path escapes the project root")
	}
	absolute, err := f.policy.Resolve(candidate, false, false, "Project file")
	if err != nil {
		return "", "", err
	}
	if !Within(root, absolute) {
		return "", "", fmt.Errorf("path escapes the project root")
	}
	return root, absolute, nil
}

type DirectoryRequest struct {
	Path          *string
	Page          int
	PageSize      int
	IncludeHidden bool
}

func (f *Files) ListDirectories(request DirectoryRequest) (DirectoryListing, error) {
	pageSize := request.PageSize
	if pageSize == 0 {
		pageSize = defaultPage
	}
	if request.Page < 0 || request.Page > pageLimit || pageSize < 1 || pageSize > defaultPage {
		return DirectoryListing{}, fmt.Errorf("invalid directory browser pagination")
	}
	roots := f.policy.Roots()
	if request.Path == nil {
		start := request.Page * pageSize
		if start > len(roots) {
			start = len(roots)
		}
		end := min(start+pageSize, len(roots))
		entries := make([]DirectoryEntry, 0, end-start)
		for _, root := range roots[start:end] {
			name := filepath.Base(root)
			if name == "." || name == string(filepath.Separator) {
				name = root
			}
			entries = append(entries, DirectoryEntry{Name: name, Path: root})
		}
		hasMore := request.Page < pageLimit && end < len(roots)
		return DirectoryListing{Roots: roots, Directories: entries, Page: request.Page, PageSize: pageSize, HasMore: hasMore, Complete: end >= len(roots), Warnings: []string{}, Cursor: nextCursor(request.Page, hasMore)}, nil
	}
	if *request.Path == "" || containsNUL(*request.Path) {
		return DirectoryListing{}, fmt.Errorf("invalid directory browser path")
	}
	current, err := f.policy.Directory(*request.Path, "Directory")
	if err != nil {
		return DirectoryListing{}, err
	}
	directory, err := f.openDirectoryUnderPolicy(current)
	if err != nil {
		return DirectoryListing{}, err
	}
	defer directory.Close()
	offset := request.Page * pageSize
	matched, scanned := 0, 0
	entries := make([]DirectoryEntry, 0, pageSize)
	hasMore := false
scan:
	for scanned < directoryLimit {
		batch, readErr := directory.ReadDir(min(directoryBatch, directoryLimit-scanned))
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return DirectoryListing{}, readErr
		}
		if len(batch) == 0 {
			break
		}
		for _, entry := range batch {
			scanned++
			if !request.IncludeHidden && len(entry.Name()) > 0 && entry.Name()[0] == '.' {
				continue
			}
			candidate, resolveErr := f.policy.Directory(filepath.Join(current, entry.Name()), "Directory")
			if resolveErr != nil {
				continue
			}
			if matched < offset {
				matched++
				continue
			}
			if len(entries) == pageSize {
				hasMore = request.Page < pageLimit
				break scan
			}
			entries = append(entries, DirectoryEntry{Name: entry.Name(), Path: candidate})
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
	}
	scanLimited := scanned == directoryLimit
	warnings := []string{}
	if scanLimited {
		warnings = append(warnings, "Directory scan reached its 10,000-entry safety limit.")
	}
	path := current
	return DirectoryListing{Path: &path, Roots: roots, Directories: entries, Page: request.Page, PageSize: pageSize, HasMore: hasMore, Complete: !hasMore && !scanLimited, Warnings: warnings, Cursor: nextCursor(request.Page, hasMore)}, nil
}

func (f *Files) openDirectoryUnderPolicy(path string) (*os.File, error) {
	for _, root := range f.policy.Roots() {
		if !Within(root, path) {
			continue
		}
		relative, _, err := relativePathInRootAllowRoot(root, path, true)
		if err != nil {
			return nil, err
		}
		return openProjectDirectory(root, relative)
	}
	return nil, fmt.Errorf("directory is outside a discovered read-only project mount")
}

func nextCursor(page int, more bool) *string {
	if !more {
		return nil
	}
	value := fmt.Sprintf("%d", page+1)
	return &value
}

func containsNUL(value string) bool {
	for _, character := range value {
		if character == 0 {
			return true
		}
	}
	return false
}
