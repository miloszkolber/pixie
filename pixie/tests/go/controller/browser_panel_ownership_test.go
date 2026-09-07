package controller_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/miloszkolber/pixie/internal/controller"
	"github.com/miloszkolber/pixie/internal/persist"
)

func TestBrowserPanelOwnershipRecoversOnlyItsEndpointAfterRestart(t *testing.T) {
	closed := make(chan string, 16)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Session, Command string }
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Command != "close" {
			t.Errorf("unexpected cleanup request: %+v, %v", body, err)
		}
		closed <- body.Session
		_ = json.NewEncoder(w).Encode(map[string]any{"outcome": "completed", "command": "close", "code": 0})
	}))
	defer server.Close()
	store := persist.Store{Dir: t.TempDir()}
	auth := controller.AuthConfig{BrowserEnabled: true, BrowserURL: server.URL, BrowserToken: browserPanelToken}
	first, err := controller.NewPersistentBrowserPanels(auth, server.Client(), store)
	if err != nil {
		t.Fatal(err)
	}
	id, err := first.Open("lost-client", "project-a")
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a crash: discard the controller without graceful cleanup.
	otherAuth := auth
	otherAuth.BrowserURL += "/different-service"
	other, err := controller.NewPersistentBrowserPanels(otherAuth, server.Client(), store)
	if err != nil {
		t.Fatal(err)
	}
	other.CloseAll(context.Background())
	select {
	case unexpected := <-closed:
		t.Fatalf("changed endpoint adopted session %s", unexpected)
	default:
	}
	// Token rotation at the same endpoint retains ownership without storing it.
	auth.BrowserToken = "rotated-token-01234567890123456789"
	restarted, err := controller.NewPersistentBrowserPanels(auth, server.Client(), store)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.CloseAll(context.Background())
	if err := restarted.Close(context.Background(), "lost-client", id); err == nil {
		t.Fatal("recovered panel could be adopted by a client")
	}
	restarted.ResumeCleanup()
	select {
	case got := <-closed:
		if got != id {
			t.Fatalf("closed %s instead of owned %s", got, id)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("startup did not reclaim the owned panel")
	}
	// Wait for the local close acknowledgement, then restart once more.
	paths, _ := filepath.Glob(filepath.Join(store.Dir, "browser-panels-*.json"))
	deadline := time.Now().Add(2 * time.Second)
	for {
		raw, err := os.ReadFile(paths[0])
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), browserPanelToken) || strings.Contains(string(raw), auth.BrowserToken) {
			t.Fatal("ownership journal contains a credential")
		}
		if !strings.Contains(string(raw), id) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("successful cleanup was not persisted")
		}
		time.Sleep(10 * time.Millisecond)
	}
	final, err := controller.NewPersistentBrowserPanels(auth, server.Client(), store)
	if err != nil {
		t.Fatal(err)
	}
	final.CloseAll(context.Background())
	select {
	case got := <-closed:
		t.Fatalf("acknowledged session was reclaimed twice: %s", got)
	default:
	}
}

func TestBrowserPanelOwnershipRejectsCorruptionAndFailedWrites(t *testing.T) {
	auth := controller.AuthConfig{BrowserURL: "http://127.0.0.1:7313"}
	for _, damage := range []string{"corrupt", "missing-primary", "write-failure"} {
		t.Run(damage, func(t *testing.T) {
			store := persist.Store{Dir: t.TempDir()}
			panels, err := controller.NewPersistentBrowserPanels(auth, nil, store)
			if err != nil {
				t.Fatal(err)
			}
			for range 2 { // Establish a primary and a valid, older backup.
				if _, err := panels.Open("client", "project"); err != nil {
					t.Fatal(err)
				}
			}
			paths, _ := filepath.Glob(filepath.Join(store.Dir, "browser-panels-*.json"))
			if damage == "corrupt" {
				err = os.WriteFile(paths[0], []byte(`{"version":1,"ids":["external-session"]}`), 0600)
			} else {
				err = os.Remove(paths[0])
			}
			if err != nil {
				t.Fatal(err)
			}
			if damage == "write-failure" {
				if err := os.Mkdir(paths[0], 0700); err != nil {
					t.Fatal(err)
				}
				if id, err := panels.Open("client", "project"); err == nil || id != "" {
					t.Fatal("panel exposed before ownership was durable")
				}
			} else if _, err := controller.NewPersistentBrowserPanels(auth, nil, store); err == nil {
				t.Fatal("damaged ownership was silently restored from stale backup")
			}
		})
	}
}

func TestBrowserPanelFailedCleanupSurvivesAnotherRestart(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	store := persist.Store{Dir: t.TempDir()}
	auth := controller.AuthConfig{BrowserEnabled: true, BrowserURL: server.URL, BrowserToken: browserPanelToken}
	panels, err := controller.NewPersistentBrowserPanels(auth, server.Client(), store)
	if err != nil {
		t.Fatal(err)
	}
	id, err := panels.Open("client", "project")
	if err != nil {
		t.Fatal(err)
	}
	panels.CloseAll(context.Background())
	restarted, err := controller.NewPersistentBrowserPanels(auth, server.Client(), store)
	if err != nil {
		t.Fatal(err)
	}
	// A surviving entry refuses client ownership, unlike an absent ID.
	if err := restarted.Close(context.Background(), "client", id); err == nil {
		t.Fatal("failed shutdown cleanup lost durable ownership")
	}
	restarted.CloseAll(context.Background())
}
