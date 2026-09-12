package mcpserver_test

import (
	"testing"
	"time"

	"github.com/miloszkolber/pixie/internal/mcpserver"
)

func scopedNativeFixture(t *testing.T, registry *mcpserver.NativeMCPRegistry, session string, generation uint64) mcpserver.NativeMCPRegistration {
	t.Helper()
	registration, err := registry.Register(mcpserver.NativeMCPRegistrationRequest{
		ModuleID: "fixture-native", ServerID: "server-a", SessionID: session,
		Generation: generation, TTL: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	return registration
}

func authorizeNativeFixture(registration mcpserver.NativeMCPRegistration) error {
	return (&mcpserver.NativeMCPRegistry{}).Authorize(mcpserver.NativeMCPAuthorization{
		RegistrationID: registration.RegistrationID,
		Credential:     registration.Credential,
		ModuleID:       registration.ModuleID,
		ServerID:       registration.ServerID,
		SessionID:      registration.SessionID,
		Generation:     registration.Generation,
	})
}

func authorizeRegistryFixture(registry *mcpserver.Registry, registration mcpserver.NativeMCPRegistration) error {
	return registry.Authorize(mcpserver.NativeMCPAuthorization{
		RegistrationID: registration.RegistrationID,
		Credential:     registration.Credential,
		ModuleID:       registration.ModuleID,
		ServerID:       registration.ServerID,
		SessionID:      registration.SessionID,
		Generation:     registration.Generation,
	})
}

func TestNativeMCPScopedRegistrationRoundTrip(t *testing.T) {
	registry := mcpserver.NewNativeMCPRegistry()
	registration := scopedNativeFixture(t, registry, "session-a", 1)
	if registration.RegistrationID == "" || registration.Credential == "" {
		t.Fatal("registration did not issue opaque ID and credential")
	}
	if registration.RegistrationID == registration.Credential {
		t.Fatal("registration ID and credential unexpectedly share a value")
	}
	if err := registry.Authorize(mcpserver.NativeMCPAuthorization{
		RegistrationID: registration.RegistrationID,
		Credential:     registration.Credential,
		ModuleID:       registration.ModuleID,
		ServerID:       registration.ServerID,
		SessionID:      registration.SessionID,
		Generation:     registration.Generation,
	}); err != nil {
		t.Fatalf("live scoped registration rejected: %v", err)
	}
	if registry.Len() != 1 {
		t.Fatalf("registry size = %d, want 1", registry.Len())
	}
}

func TestNativeMCPForgedIDsAndCredentialsFailClosed(t *testing.T) {
	registry := mcpserver.NewNativeMCPRegistry()
	registration := scopedNativeFixture(t, registry, "session-a", 7)
	base := mcpserver.NativeMCPAuthorization{
		RegistrationID: registration.RegistrationID,
		Credential:     registration.Credential,
		ModuleID:       registration.ModuleID,
		ServerID:       registration.ServerID,
		SessionID:      registration.SessionID,
		Generation:     registration.Generation,
	}
	for name, mutate := range map[string]func(*mcpserver.NativeMCPAuthorization){
		"forged registration ID": func(request *mcpserver.NativeMCPAuthorization) { request.RegistrationID = "forged-registration" },
		"forged credential":      func(request *mcpserver.NativeMCPAuthorization) { request.Credential = "forged-credential" },
		"swapped module":         func(request *mcpserver.NativeMCPAuthorization) { request.ModuleID = "fixture-other" },
		"swapped server":         func(request *mcpserver.NativeMCPAuthorization) { request.ServerID = "server-other" },
		"swapped session":        func(request *mcpserver.NativeMCPAuthorization) { request.SessionID = "session-b" },
		"stale generation":       func(request *mcpserver.NativeMCPAuthorization) { request.Generation++ },
	} {
		t.Run(name, func(t *testing.T) {
			request := base
			mutate(&request)
			if err := registry.Authorize(request); err == nil {
				t.Fatalf("forged %s was accepted", name)
			}
		})
	}
	if err := registry.Authorize(base); err != nil {
		t.Fatalf("original binding rejected after forged attempts: %v", err)
	}
	if err := authorizeNativeFixture(registration); err == nil {
		t.Fatal("credential was accepted by a different registry")
	}
}

func TestNativeMCPRevocationInvalidatesCredentialsAndSessions(t *testing.T) {
	registry := mcpserver.NewNativeMCPRegistry()
	first := scopedNativeFixture(t, registry, "session-a", 1)
	second := scopedNativeFixture(t, registry, "session-a", 2)
	other := scopedNativeFixture(t, registry, "session-b", 1)

	registry.Revoke(first.RegistrationID)
	if err := registry.Authorize(mcpserver.NativeMCPAuthorization{
		RegistrationID: first.RegistrationID, Credential: first.Credential,
		ModuleID: first.ModuleID, ServerID: first.ServerID,
		SessionID: first.SessionID, Generation: first.Generation,
	}); err == nil {
		t.Fatal("revoked registration was accepted")
	}
	if revoked := registry.RevokeSession("session-a"); revoked != 1 {
		t.Fatalf("session revocations = %d, want 1", revoked)
	}
	if err := registry.Authorize(mcpserver.NativeMCPAuthorization{
		RegistrationID: second.RegistrationID, Credential: second.Credential,
		ModuleID: second.ModuleID, ServerID: second.ServerID,
		SessionID: second.SessionID, Generation: second.Generation,
	}); err == nil {
		t.Fatal("session-revoked registration was accepted")
	}
	if err := registry.Authorize(mcpserver.NativeMCPAuthorization{
		RegistrationID: other.RegistrationID, Credential: other.Credential,
		ModuleID: other.ModuleID, ServerID: other.ServerID,
		SessionID: other.SessionID, Generation: other.Generation,
	}); err != nil {
		t.Fatalf("unrelated session was revoked: %v", err)
	}
	if revoked := registry.RevokeModule("fixture-native"); revoked != 1 {
		t.Fatalf("module revocations = %d, want 1", revoked)
	}
	if registry.Len() != 0 {
		t.Fatalf("registry size after revocation = %d", registry.Len())
	}
}

func TestNativeMCPGenerationAdvanceRevokesOlderRegistrations(t *testing.T) {
	registry := mcpserver.NewNativeMCPRegistry()
	old := scopedNativeFixture(t, registry, "session-a", 1)
	if revoked, err := registry.AdvanceGeneration("fixture-native", "server-a", "session-a", 2); err != nil || revoked != 1 {
		t.Fatalf("advance generation revoked=%d err=%v, want one revocation", revoked, err)
	}
	if err := registry.Authorize(mcpserver.NativeMCPAuthorization{
		RegistrationID: old.RegistrationID, Credential: old.Credential,
		ModuleID: old.ModuleID, ServerID: old.ServerID,
		SessionID: old.SessionID, Generation: old.Generation,
	}); err == nil {
		t.Fatal("older generation registration survived generation advance")
	}
	newer := scopedNativeFixture(t, registry, "session-a", 2)
	if err := registry.Authorize(mcpserver.NativeMCPAuthorization{
		RegistrationID: newer.RegistrationID, Credential: newer.Credential,
		ModuleID: newer.ModuleID, ServerID: newer.ServerID,
		SessionID: newer.SessionID, Generation: newer.Generation,
	}); err != nil {
		t.Fatalf("new generation registration rejected: %v", err)
	}
	if _, err := registry.Register(mcpserver.NativeMCPRegistrationRequest{
		ModuleID: "fixture-native", ServerID: "server-a", SessionID: "session-a", Generation: 1, TTL: time.Hour,
	}); err == nil {
		t.Fatal("stale generation registration was accepted")
	}
}

func TestNativeMCPExpiryAndInvalidRegistrationFailClosed(t *testing.T) {
	registry := mcpserver.NewNativeMCPRegistry()
	if _, err := registry.Register(mcpserver.NativeMCPRegistrationRequest{ModuleID: "fixture-native", ServerID: "server-a", SessionID: "session-a", Generation: 0, TTL: time.Hour}); err == nil {
		t.Fatal("zero generation was accepted")
	}
	if _, err := registry.Register(mcpserver.NativeMCPRegistrationRequest{ModuleID: "fixture-native", ServerID: "server/a", SessionID: "session-a", Generation: 1, TTL: time.Hour}); err == nil {
		t.Fatal("malformed server ID was accepted")
	}
	if _, err := registry.Register(mcpserver.NativeMCPRegistrationRequest{ModuleID: "fixture-native", ServerID: "server-a", SessionID: "session-a", Generation: 1, TTL: -time.Second}); err == nil {
		t.Fatal("negative credential TTL was accepted")
	}
	expired, err := registry.Register(mcpserver.NativeMCPRegistrationRequest{ModuleID: "fixture-native", ServerID: "server-a", SessionID: "session-a", Generation: 1, TTL: time.Nanosecond})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond)
	if err := registry.Authorize(mcpserver.NativeMCPAuthorization{
		RegistrationID: expired.RegistrationID, Credential: expired.Credential,
		ModuleID: expired.ModuleID, ServerID: expired.ServerID,
		SessionID: expired.SessionID, Generation: expired.Generation,
	}); err == nil {
		t.Fatal("expired registration was accepted")
	}
}

func TestRegistryOwnsNativeMCPRegistrationsAcrossModuleAndShutdownLifecycle(t *testing.T) {
	registry := testRegistry(t, nil)
	registry.SetWorkerBoundaryVerified(true)
	registration, err := registry.Register(mcpserver.NativeMCPRegistrationRequest{
		ModuleID: "browser", ServerID: "server-a", SessionID: "session-live",
		Generation: 1, TTL: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := authorizeRegistryFixture(registry, registration); err != nil {
		t.Fatalf("live Registry registration rejected: %v", err)
	}
	if err := registry.SetEnabled("browser", false); err != nil {
		t.Fatal(err)
	}
	if err := authorizeRegistryFixture(registry, registration); err == nil {
		t.Fatal("module disable left a native MCP registration usable")
	}

	if err := registry.SetEnabled("browser", true); err != nil {
		t.Fatal(err)
	}
	replacement, err := registry.Register(mcpserver.NativeMCPRegistrationRequest{
		ModuleID: "browser", ServerID: "server-a", SessionID: "session-live",
		Generation: 2, TTL: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	registry.Shutdown()
	if err := authorizeRegistryFixture(registry, replacement); err == nil {
		t.Fatal("Registry shutdown left a native MCP registration usable")
	}
}

func TestRegistryNativeMCPGenerationAndSessionScopes(t *testing.T) {
	registry := testRegistry(t, nil)
	old, err := registry.Register(mcpserver.NativeMCPRegistrationRequest{
		ModuleID: "fixture-native", ServerID: "server-a", SessionID: "session-a",
		Generation: 1, TTL: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	otherSession, err := registry.Register(mcpserver.NativeMCPRegistrationRequest{
		ModuleID: "fixture-native", ServerID: "server-a", SessionID: "session-b",
		Generation: 1, TTL: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.AdvanceGeneration("fixture-native", "server-a", "session-a", 2); err != nil {
		t.Fatal(err)
	}
	if err := authorizeRegistryFixture(registry, old); err == nil {
		t.Fatal("older generation registration survived the live Registry advance")
	}
	if err := authorizeRegistryFixture(registry, otherSession); err != nil {
		t.Fatalf("generation advance crossed into another session: %v", err)
	}
	if err := registry.Authorize(mcpserver.NativeMCPAuthorization{
		RegistrationID: otherSession.RegistrationID,
		Credential:     otherSession.Credential,
		ModuleID:       "fixture-other",
		ServerID:       otherSession.ServerID,
		SessionID:      otherSession.SessionID,
		Generation:     otherSession.Generation,
	}); err == nil {
		t.Fatal("registration authorized for a forged module")
	}
	registry.RevokeSession("session-b")
	if err := authorizeRegistryFixture(registry, otherSession); err == nil {
		t.Fatal("session revocation left a native MCP registration usable")
	}
}
