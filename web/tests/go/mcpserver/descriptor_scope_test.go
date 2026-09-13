package mcpserver_test

import (
	"testing"
	"time"

	"github.com/miloszkolber/pixie/internal/mcpserver"
)

func issueScopeFixture(t *testing.T, authority *mcpserver.ScopeAuthority, generation uint64) mcpserver.IssuedScope {
	t.Helper()
	fixture := mcpserver.SidebarOnlyFixture()
	issued, err := authority.Issue(fixture.ModuleID, fixture.ResourceNamespace+":sess-res-1", "sess-1", generation, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return issued
}

func TestScopeAuthorityRoundTrip(t *testing.T) {
	authority := mcpserver.NewScopeAuthority()
	issued := issueScopeFixture(t, authority, 7)
	if issued.Token == "" {
		t.Fatal("issued empty token")
	}
	if err := authority.Authorize(issued.Token, issued.ModuleID, issued.ResourceID, issued.SessionID, issued.Generation); err != nil {
		t.Fatalf("live binding rejected: %v", err)
	}
	if authority.Len() != 1 {
		t.Fatalf("authority size = %d", authority.Len())
	}
}

func TestScopeAuthorityForgedIDsGrantNothing(t *testing.T) {
	authority := mcpserver.NewScopeAuthority()
	issued := issueScopeFixture(t, authority, 1)
	viewer := mcpserver.ViewerOnlyFixture()

	// IDs alone grant nothing: empty and unknown tokens fail even with the
	// exact recorded IDs.
	if err := authority.Authorize("", issued.ModuleID, issued.ResourceID, issued.SessionID, issued.Generation); err == nil {
		t.Fatal("empty token was accepted")
	}
	if err := authority.Authorize("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", issued.ModuleID, issued.ResourceID, issued.SessionID, issued.Generation); err == nil {
		t.Fatal("forged token was accepted")
	}

	// A live token with any swapped caller-supplied ID fails.
	forged := []struct {
		name       string
		moduleID   string
		resourceID string
		sessionID  string
		generation uint64
	}{
		{"module", viewer.ModuleID, viewer.ResourceNamespace + ":sess-res-1", issued.SessionID, issued.Generation},
		{"resource", issued.ModuleID, issued.ModuleID + ":other-res", issued.SessionID, issued.Generation},
		{"cross-namespace resource", issued.ModuleID, viewer.ResourceNamespace + ":sess-res-1", issued.SessionID, issued.Generation},
		{"session", issued.ModuleID, issued.ResourceID, "sess-2", issued.Generation},
		{"generation", issued.ModuleID, issued.ResourceID, issued.SessionID, issued.Generation + 1},
	}
	for _, item := range forged {
		if err := authority.Authorize(issued.Token, item.moduleID, item.resourceID, item.sessionID, item.generation); err == nil {
			t.Fatalf("forged %s was accepted", item.name)
		}
	}

	// The original binding still works after forged attempts.
	if err := authority.Authorize(issued.Token, issued.ModuleID, issued.ResourceID, issued.SessionID, issued.Generation); err != nil {
		t.Fatalf("live binding rejected after forged attempts: %v", err)
	}

	// Tokens from one binding do not cross-authorize another binding.
	other, err := authority.Issue(viewer.ModuleID, viewer.ResourceNamespace+":view-res-9", "sess-9", 1, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := authority.Authorize(other.Token, issued.ModuleID, issued.ResourceID, issued.SessionID, issued.Generation); err == nil {
		t.Fatal("cross-binding token was accepted")
	}
	if err := authority.Authorize(issued.Token, other.ModuleID, other.ResourceID, other.SessionID, other.Generation); err == nil {
		t.Fatal("cross-binding IDs were accepted")
	}
}

func TestScopeAuthorityTokensAreUnguessable(t *testing.T) {
	authority := mcpserver.NewScopeAuthority()
	first := issueScopeFixture(t, authority, 1)
	second := issueScopeFixture(t, authority, 1)
	if first.Token == second.Token {
		t.Fatal("two issuances for the same binding produced the same token")
	}
	if err := authority.Authorize(first.Token, first.ModuleID, first.ResourceID, first.SessionID, first.Generation); err != nil {
		t.Fatalf("first token rejected: %v", err)
	}
	if err := authority.Authorize(second.Token, second.ModuleID, second.ResourceID, second.SessionID, second.Generation); err != nil {
		t.Fatalf("second token rejected: %v", err)
	}
}

func TestScopeAuthorityRevocation(t *testing.T) {
	authority := mcpserver.NewScopeAuthority()
	issued := issueScopeFixture(t, authority, 3)

	authority.Revoke(issued.Token)
	if err := authority.Authorize(issued.Token, issued.ModuleID, issued.ResourceID, issued.SessionID, issued.Generation); err == nil {
		t.Fatal("revoked token was accepted")
	}

	first := issueScopeFixture(t, authority, 3)
	second := issueScopeFixture(t, authority, 3)
	if revoked := authority.RevokeSession("sess-1"); revoked != 2 {
		t.Fatalf("revoked sessions = %d", revoked)
	}
	if err := authority.Authorize(first.Token, first.ModuleID, first.ResourceID, first.SessionID, first.Generation); err == nil {
		t.Fatal("session-revoked token was accepted")
	}
	if err := authority.Authorize(second.Token, second.ModuleID, second.ResourceID, second.SessionID, second.Generation); err == nil {
		t.Fatal("session-revoked token was accepted")
	}
	if authority.Len() != 0 {
		t.Fatalf("authority size after revocation = %d", authority.Len())
	}
}

func TestScopeAuthorityExpiryFailsClosed(t *testing.T) {
	authority := mcpserver.NewScopeAuthority()
	fixture := mcpserver.SidebarOnlyFixture()
	expired, err := authority.Issue(fixture.ModuleID, fixture.ResourceNamespace+":sess-res-1", "sess-1", 1, -time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := authority.Authorize(expired.Token, expired.ModuleID, expired.ResourceID, expired.SessionID, expired.Generation); err == nil {
		t.Fatal("expired credential was accepted")
	}
	live := issueScopeFixture(t, authority, 1)
	if err := authority.Authorize(live.Token, live.ModuleID, live.ResourceID, live.SessionID, live.Generation); err != nil {
		t.Fatalf("live credential rejected: %v", err)
	}
	// Expired entries do not linger as usable capacity.
	if authority.Len() != 1 {
		t.Fatalf("authority size = %d", authority.Len())
	}
}
