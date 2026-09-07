package workspace

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

var excludedMountPaths = []string{
	"/", "/app", "/bin", "/boot", "/dev", "/etc", "/home/pixie", "/lib", "/lib64",
	"/proc", "/root", "/run", "/sbin", "/sys", "/tmp", "/usr", "/var",
	"/var/lib/pixie", "/home/pixie/.pi",
}

type PathPolicy struct {
	roots []string
}

func DiscoverPathPolicy() (*PathPolicy, error) {
	file, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var candidates []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 6 || !hasCSVValue(fields[5], "ro") {
			continue
		}
		decoded, ok := decodeMountPath(fields[4])
		if ok {
			candidates = append(candidates, decoded)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return NewPathPolicy(candidates, true)
}

func NewPathPolicy(paths []string, excludeSystem bool) (*PathPolicy, error) {
	unique := make(map[string]struct{})
	for _, candidate := range paths {
		if !filepath.IsAbs(candidate) {
			continue
		}
		lexical := filepath.Clean(candidate)
		if excludeSystem && overlapsExcluded(lexical) {
			continue
		}
		info, err := os.Stat(lexical)
		if err != nil || !info.IsDir() {
			continue
		}
		canonical, err := filepath.EvalSymlinks(lexical)
		if err != nil || (excludeSystem && overlapsExcluded(canonical)) {
			continue
		}
		unique[canonical] = struct{}{}
	}
	roots := make([]string, 0, len(unique))
	for root := range unique {
		roots = append(roots, root)
	}
	sort.Slice(roots, func(i, j int) bool {
		if len(roots[i]) != len(roots[j]) {
			return len(roots[i]) < len(roots[j])
		}
		return roots[i] < roots[j]
	})
	filtered := roots[:0]
	for _, root := range roots {
		covered := false
		for _, parent := range filtered {
			if Within(parent, root) {
				covered = true
				break
			}
		}
		if !covered {
			filtered = append(filtered, root)
		}
	}
	return &PathPolicy{roots: append([]string(nil), filtered...)}, nil
}

func (p *PathPolicy) Roots() []string { return append([]string{}, p.roots...) }

func (p *PathPolicy) Resolve(candidate string, directory, allowMissingLeaf bool, label string) (string, error) {
	if label == "" {
		label = "Path"
	}
	if !filepath.IsAbs(candidate) {
		return "", fmt.Errorf("%s must be an absolute path: %s", label, candidate)
	}
	absolute := filepath.Clean(candidate)
	canonical, err := filepath.EvalSymlinks(absolute)
	if err == nil {
		if err := p.assertUnderMount(canonical, label); err != nil {
			return "", err
		}
		if directory {
			info, statErr := os.Stat(canonical)
			if statErr != nil || !info.IsDir() {
				return "", fmt.Errorf("%s is not a directory: %s", label, candidate)
			}
		}
		return canonical, nil
	}
	if !os.IsNotExist(err) || !allowMissingLeaf {
		return "", fmt.Errorf("%s does not exist: %s", label, absolute)
	}
	ancestor := absolute
	var suffix []string
	for {
		if _, statErr := os.Lstat(ancestor); statErr == nil {
			break
		} else if !os.IsNotExist(statErr) {
			return "", statErr
		}
		parent := filepath.Dir(ancestor)
		if parent == ancestor {
			return "", fmt.Errorf("%s does not exist: %s", label, absolute)
		}
		suffix = append([]string{filepath.Base(ancestor)}, suffix...)
		ancestor = parent
	}
	canonicalAncestor, err := filepath.EvalSymlinks(ancestor)
	if err != nil {
		return "", err
	}
	if err := p.assertUnderMount(canonicalAncestor, label); err != nil {
		return "", err
	}
	info, err := os.Stat(canonicalAncestor)
	if err != nil || (len(suffix) > 0 && !info.IsDir()) || (directory && len(suffix) == 0 && !info.IsDir()) {
		return "", fmt.Errorf("%s parent is not a directory: %s", label, candidate)
	}
	parts := append([]string{canonicalAncestor}, suffix...)
	return filepath.Join(parts...), nil
}

func (p *PathPolicy) Directory(candidate, label string) (string, error) {
	return p.Resolve(candidate, true, false, label)
}

func (p *PathPolicy) assertUnderMount(candidate, label string) error {
	for _, root := range p.roots {
		if Within(root, candidate) {
			return nil
		}
	}
	return fmt.Errorf("%s is outside a discovered read-only project mount: %s", label, candidate)
}

func Within(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	return err == nil && (relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)))
}

func overlapsExcluded(path string) bool {
	if path == "/" {
		return true
	}
	for _, excluded := range excludedMountPaths[1:] {
		if Within(excluded, path) || Within(path, excluded) {
			return true
		}
	}
	return false
}

func hasCSVValue(value, wanted string) bool {
	for _, entry := range strings.Split(value, ",") {
		if entry == wanted {
			return true
		}
	}
	return false
}

func decodeMountPath(value string) (string, bool) {
	var decoded strings.Builder
	for index := 0; index < len(value); index++ {
		if value[index] != '\\' {
			decoded.WriteByte(value[index])
			continue
		}
		if index+3 >= len(value) {
			return "", false
		}
		octal := value[index+1 : index+4]
		parsed, err := strconv.ParseUint(octal, 8, 8)
		if err != nil {
			return "", false
		}
		decoded.WriteByte(byte(parsed))
		index += 3
	}
	result := decoded.String()
	return result, !strings.ContainsRune(result, 0)
}
