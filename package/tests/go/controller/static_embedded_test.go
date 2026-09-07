package controller_test

import (
	"io/fs"
	"testing"

	"github.com/miloszkolber/pixie/internal/controller"
)

// The supported non-Docker installation is a single self-contained binary:
// release builds embed the built web UI, while a plain `go build` before the
// first web build embeds only the tracked placeholder and the server falls
// back to disk assets. Either the real bundle or the placeholder must be
// present; otherwise the embed wiring is broken.
func TestEmbeddedWebUIBundleOrPlaceholderIsPresent(t *testing.T) {
	bundle, hasUI := controller.EmbeddedWebUI()
	if bundle == nil {
		t.Fatal("embedded web bundle is missing")
	}
	_, indexErr := fs.Stat(bundle, "index.html")
	if (indexErr == nil) != hasUI {
		t.Fatalf("EmbeddedWebUI() reported hasUI=%v but index.html lookup err=%v", hasUI, indexErr)
	}
	if indexErr != nil {
		if _, markerErr := fs.Stat(bundle, ".gitkeep"); markerErr != nil {
			t.Fatalf("embedded bundle holds neither a built index.html (%v) nor the placeholder marker (%v)", indexErr, markerErr)
		}
	}
}
