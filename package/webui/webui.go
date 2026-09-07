package webui

import "embed"

// Dist holds the built web interface from package/webui/dist. Release builds
// embed the real output of `bun run build:web`; plain `go build` before the
// first web build embeds only the tracked placeholder, and the server falls
// back to disk assets in that case.
//
//go:embed all:dist
var Dist embed.FS
