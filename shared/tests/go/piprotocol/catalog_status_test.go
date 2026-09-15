package piprotocol_test

import (
	"testing"

	"github.com/miloszkolber/pixie/shared/piprotocol"
)

// TestCatalogOperationAvailability pins the single capability source: the
// catalog marks each host route available or unavailable, and only available
// routes may be negotiated. A regression that re-advertises an unimplemented
// route fails here before it reaches the controller.
func TestCatalogOperationAvailability(t *testing.T) {
	available := []string{
		"session.list", "session.create", "session.load", "session.prompt",
		"session.cancel", "session.configure", "pi.providers.config.read",
		"pi.providers.list", "pi.mcp.servers.read",
	}
	for _, name := range available {
		if !piprotocol.CatalogHostOperationIsAvailable(name) {
			t.Errorf("catalog marks implemented operation %q unavailable", name)
		}
	}
	unavailable := []string{
		"pi.session.info", "pi.session.steer", "pi.tools.list", "pi.tools.call",
		"runtime.capabilities", "pi.subagent.execute", "pi.todo.plan",
		"pi.llama", "pi.native-extensions",
	}
	for _, name := range unavailable {
		if piprotocol.CatalogHostOperationIsAvailable(name) {
			t.Errorf("catalog advertises unimplemented operation %q as available", name)
		}
		if reason := piprotocol.CatalogHostOperationReasons[name]; reason == "" {
			t.Errorf("unavailable operation %q has no reason", name)
		}
	}
}

func TestCatalogOperationStatusIsExhaustive(t *testing.T) {
	if len(piprotocol.CatalogHostOperations) == 0 {
		t.Fatal("catalog host operations are empty")
	}
	if len(piprotocol.CatalogHostOperationStatus) != len(piprotocol.CatalogHostOperations) {
		t.Fatalf(
			"status map has %d entries for %d operations",
			len(piprotocol.CatalogHostOperationStatus),
			len(piprotocol.CatalogHostOperations),
		)
	}
	for _, name := range piprotocol.CatalogHostOperations {
		if !piprotocol.CatalogHostOperationSet[name] {
			t.Errorf("operation %q missing from the membership set", name)
		}
		status, present := piprotocol.CatalogHostOperationStatus[name]
		if !present {
			t.Errorf("operation %q has no implementation status", name)
			continue
		}
		switch status {
		case piprotocol.CatalogHostOperationAvailable:
			if !piprotocol.CatalogHostOperationIsAvailable(name) {
				t.Errorf("available operation %q is not in the available set", name)
			}
		case piprotocol.CatalogHostOperationUnavailable, piprotocol.CatalogHostOperationAbsent:
			if piprotocol.CatalogHostOperationIsAvailable(name) {
				t.Errorf("non-available operation %q is in the available set", name)
			}
			if piprotocol.CatalogHostOperationReasons[name] == "" {
				t.Errorf("non-available operation %q has no reason", name)
			}
		default:
			t.Errorf("operation %q has unknown status %q", name, status)
		}
	}
}
