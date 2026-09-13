package controller

import (
	"io/fs"
	"os"

	pixieui "github.com/miloszkolber/pixie/webui"
)

// staticFiles serves the web interface from one rooted filesystem: either an
// explicit disk directory or the UI embedded in the binary.
type staticFiles struct {
	fsys fs.FS
}

// resolveStaticFiles picks the web asset source. An explicit directory always
// wins; otherwise the embedded build is used when it holds a real index.html,
// with the container asset path as the last resort (a Docker binary is built
// before the web bundle, so it keeps serving /app/web from disk).
func resolveStaticFiles(staticDir string) staticFiles {
	if staticDir != "" {
		return staticFiles{fsys: os.DirFS(staticDir)}
	}
	if embedded, ok := EmbeddedWebUI(); ok {
		return staticFiles{fsys: embedded}
	}
	return staticFiles{fsys: os.DirFS(DefaultStaticDir)}
}

// EmbeddedWebUI returns the embedded web bundle and whether it holds a real
// build. Binaries compiled before `bun run build:web` embed only the tracked
// placeholder and report false, so disk assets apply instead.
func EmbeddedWebUI() (fs.FS, bool) {
	bundle, err := fs.Sub(pixieui.Dist, "dist")
	if err != nil {
		return nil, false
	}
	if info, err := fs.Stat(bundle, "index.html"); err != nil || !info.Mode().IsRegular() {
		return bundle, false
	}
	return bundle, true
}

func (s staticFiles) stat(name string) (fs.FileInfo, bool) {
	info, err := fs.Stat(s.fsys, name)
	if err != nil || !info.Mode().IsRegular() {
		return nil, false
	}
	return info, true
}

func (s staticFiles) read(name string) ([]byte, bool) {
	content, err := fs.ReadFile(s.fsys, name)
	if err != nil {
		return nil, false
	}
	return content, true
}
