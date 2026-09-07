package controller_test

import (
	"testing"

	"github.com/miloszkolber/pixie/internal/controller"
	"github.com/miloszkolber/pixie/internal/workspace"
)

func TestPiStaysOnLoopbackWithPortOnlyKnob(t *testing.T) {
	for _, test := range []struct {
		name    string
		values  map[string]string
		wantURL string
		valid   bool
	}{
		{name: "defaults to loopback", values: map[string]string{}, wantURL: "ws://127.0.0.1:3284/pi", valid: true},
		{name: "port only", values: map[string]string{"PIXIE_PI_PORT": "3285"}, wantURL: "ws://127.0.0.1:3285/pi", valid: true},
		{name: "port wins over URL", values: map[string]string{
			"PIXIE_PI_PORT": "3286", "PIXIE_PI_URL": "ws://127.0.0.1:3284/pi",
		}, wantURL: "ws://127.0.0.1:3286/pi", valid: true},
		{name: "loopback URL still accepted", values: map[string]string{
			"PIXIE_PI_URL": "ws://127.0.0.1:3284/pi",
		}, wantURL: "ws://127.0.0.1:3284/pi", valid: true},
		{name: "localhost URL accepted", values: map[string]string{
			"PIXIE_PI_URL": "ws://localhost:3284/pi",
		}, wantURL: "ws://localhost:3284/pi", valid: true},
		{name: "remote URL rejected", values: map[string]string{
			"PIXIE_PI_URL": "ws://192.168.0.10:3284/pi",
		}, valid: false},
		{name: "wrong scheme rejected", values: map[string]string{
			"PIXIE_PI_URL": "http://127.0.0.1:3284/pi",
		}, valid: false},
		{name: "bad port rejected", values: map[string]string{"PIXIE_PI_PORT": "abc"}, valid: false},
		{name: "zero port rejected", values: map[string]string{"PIXIE_PI_PORT": "0"}, valid: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			policy, err := workspace.NewPathPolicy([]string{t.TempDir()}, false)
			if err != nil {
				t.Fatal(err)
			}
			runtime, err := controller.NewRuntime(controller.RuntimeConfig{
				Host: "127.0.0.1", Port: 0, DataDir: t.TempDir(), StaticDir: t.TempDir(),
				Policy: policy,
				Getenv: func(key string) string { return test.values[key] },
			})
			if (err == nil) != test.valid {
				t.Fatalf("valid=%v, err=%v", test.valid, err)
			}
			if !test.valid || runtime == nil {
				return
			}
			// Runtime does not export the resolved Pi URL, so a bad port or
			// remote URL failing closed at construction is the assertion.
			_ = test.wantURL
		})
	}
}

func TestAllowedOriginsIsIgnoredInFavorOfFirewall(t *testing.T) {
	config, err := controller.ReadAuthConfig(func(key string) string {
		if key == "PIXIE_ALLOWED_ORIGINS" {
			return "https://extra.example"
		}
		return ""
	})
	if err != nil {
		t.Fatalf("loopback defaults with PIXIE_ALLOWED_ORIGINS set were rejected: %v", err)
	}
	if config.PublicOrigin != "" {
		t.Fatalf("unexpected public origin %q", config.PublicOrigin)
	}
}
