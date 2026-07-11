package panel

import (
	"testing"
	"time"
)

func TestSessionCSRF(t *testing.T) {
	m := NewSessionManager(time.Hour)
	token, csrf, err := m.Create("admin")
	if err != nil {
		t.Fatal(err)
	}
	if token == "" || csrf == "" || token == csrf {
		t.Fatalf("expected distinct non-empty token+csrf, got %q / %q", token, csrf)
	}

	got, ok := m.CSRF(token)
	if !ok || got != csrf {
		t.Fatalf("CSRF(token) = %q,%v want %q,true", got, ok, csrf)
	}
	if !m.ValidCSRF(token, csrf) {
		t.Error("ValidCSRF should accept the matching token")
	}
	if m.ValidCSRF(token, "nope") {
		t.Error("ValidCSRF should reject a wrong token")
	}
	if m.ValidCSRF("no-session", csrf) {
		t.Error("ValidCSRF should reject an unknown session")
	}
	if m.ValidCSRF(token, "") {
		t.Error("ValidCSRF should reject an empty presented token")
	}

	// Revoked session yields no CSRF.
	m.Revoke(token)
	if _, ok := m.CSRF(token); ok {
		t.Error("CSRF should be gone after Revoke")
	}
}

func TestSessionCSRFExpiry(t *testing.T) {
	m := NewSessionManager(time.Hour)
	now := time.Now()
	m.now = func() time.Time { return now }
	token, csrf, _ := m.Create("admin")
	if !m.ValidCSRF(token, csrf) {
		t.Fatal("fresh session csrf should be valid")
	}
	now = now.Add(2 * time.Hour) // past TTL
	if m.ValidCSRF(token, csrf) {
		t.Error("expired session csrf should be invalid")
	}
}
