package workspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// maxRealPathLinks bounds symlink expansion during a containment walk-up. The
// kernel's own limit is typically 40; matching it keeps the walk finite without
// rejecting an ordinary chain.
const maxRealPathLinks = 40

// ErrTraversal marks a candidate that resolves outside the admitted workspace
// mount or project root. It is a typed, stable denial so an HTTP surface can
// map containment failures to 403 without re-deriving the reason from text.
var ErrTraversal error = &TraversalError{}

// TraversalError is a path-containment denial. ErrorCode is a stable wire code;
// its message deliberately contains no resolved path or candidate text.
type TraversalError struct{}

func (*TraversalError) Error() string { return "path escapes the admitted workspace" }

func (*TraversalError) ErrorCode() string { return "PATH_ESCAPES_PROJECT_ROOT" }

// IsTraversal reports whether err is a containment denial produced by the
// workspace walk-up.
func IsTraversal(err error) bool {
	return errors.Is(err, ErrTraversal)
}

// ResolveRealPath walks candidate one component at a time from root and
// resolves symlinks in place. Every resolved component must stay within root,
// so a symlink whose target leaves the admitted tree is rejected instead of
// followed. When allowMissingLeaf is set the final component may be absent;
// an intermediate missing component is always an error.
//
// The walk-up is intentionally explicit rather than a single EvalSymlinks call:
// it can report the boundary that was crossed with the typed TraversalError, and
// it refuses a symlink target that only later traversal would bring back inside
// root.
func ResolveRealPath(root, candidate string, allowMissingLeaf bool) (string, error) {
	if !filepath.IsAbs(root) || !filepath.IsAbs(candidate) {
		return "", fmt.Errorf("path resolution requires absolute paths")
	}
	links := 0
	return resolveRealPath(filepath.Clean(root), filepath.Clean(candidate), allowMissingLeaf, &links)
}

func resolveRealPath(root, candidate string, allowMissingLeaf bool, links *int) (string, error) {
	if !Within(root, candidate) {
		return "", fmt.Errorf("%w", ErrTraversal)
	}
	relative, err := filepath.Rel(root, candidate)
	if err != nil {
		return "", fmt.Errorf("%w", ErrTraversal)
	}
	if relative == "." {
		return root, nil
	}
	components := strings.Split(relative, string(filepath.Separator))
	resolved := root
	for index, component := range components {
		last := index == len(components)-1
		switch component {
		case "", ".":
			continue
		case "..":
			// filepath.Clean removed lexical traversal; a surviving ".." means
			// the candidate was malformed. Fail closed rather than guess.
			return "", fmt.Errorf("%w", ErrTraversal)
		}
		next := filepath.Join(resolved, component)
		info, statErr := os.Lstat(next)
		if statErr != nil {
			if os.IsNotExist(statErr) {
				if last && allowMissingLeaf {
					return next, nil
				}
				return "", fmt.Errorf("path does not exist: %s", candidate)
			}
			return "", statErr
		}
		if info.Mode()&os.ModeSymlink == 0 {
			if !last && !info.IsDir() {
				return "", fmt.Errorf("path contains a non-directory")
			}
			resolved = next
			continue
		}
		*links++
		if *links > maxRealPathLinks {
			return "", fmt.Errorf("too many symbolic links")
		}
		target, readErr := os.Readlink(next)
		if readErr != nil {
			return "", readErr
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(resolved, target)
		}
		// The link target is resolved through the same root boundary. Only the
		// link that terminates the requested path may tolerate a missing leaf.
		resolvedTarget, resolveErr := resolveRealPath(root, filepath.Clean(target), allowMissingLeaf && last, links)
		if resolveErr != nil {
			return "", resolveErr
		}
		targetInfo, targetStatErr := os.Lstat(resolvedTarget)
		if targetStatErr != nil {
			if os.IsNotExist(targetStatErr) && last && allowMissingLeaf {
				return resolvedTarget, nil
			}
			return "", targetStatErr
		}
		if !last && !targetInfo.IsDir() {
			return "", fmt.Errorf("path contains a non-directory")
		}
		resolved = resolvedTarget
	}
	return resolved, nil
}
