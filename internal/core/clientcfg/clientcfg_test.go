package clientcfg

import (
	"strings"
	"testing"
	"time"

	"trustpanel/internal/core/model"
	"trustpanel/internal/paths"
)

func TestBuildForEntry(t *testing.T) {
	st := model.WorkedExample()
	at := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)

	cfg, err := Build(st, "u-alice", "entryA", Options{}, at)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Hostname != "cdn.example.com" || cfg.Username != "alice" {
		t.Fatalf("unexpected cfg: %+v", cfg)
	}
	if !cfg.Active {
		t.Error("alice should be active")
	}
	for _, want := range []string{
		`username = "alice"`,
		`password = "s-alice"`,
		`hostname = "cdn.example.com"`,
		`addresses = ["cdn.example.com:443"]`,
		`vpn_mode = "general"`,
		`[listener.tun]`,
	} {
		if !strings.Contains(cfg.TOML, want) {
			t.Errorf("client TOML missing %q:\n%s", want, cfg.TOML)
		}
	}
	// guard-ru direct policy -> client exclusions glob.
	if !strings.Contains(cfg.TOML, `"*.ru"`) {
		t.Errorf("expected *.ru in exclusions:\n%s", cfg.TOML)
	}
}

func TestSameCredsDifferentEntry(t *testing.T) {
	st := model.WorkedExample()
	// Add a second entry node + its domain.
	at := time.Now()
	st.Nodes = append(st.Nodes, model.Node{ID: "entryB", Name: "Entry B", PublicRole: model.RoleEntry,
		PublicIPs: []string{"203.0.113.11"}, AgentAddr: "203.0.113.11:8443"})
	st.Domains = append(st.Domains, model.Domain{ID: "d-entryB", Hostname: "cdn2.example.com",
		Purpose: model.PurposeMainFallback, NodeID: "entryB"})

	a, err := Build(st, "u-admin", "entryA", Options{}, at)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Build(st, "u-admin", "entryB", Options{}, at)
	if err != nil {
		t.Fatal(err)
	}
	// Same user credentials, different endpoint.
	if a.Username != b.Username {
		t.Error("username should be identical across entries")
	}
	if a.Hostname == b.Hostname {
		t.Error("hostname should differ across entries")
	}
	if !strings.Contains(a.TOML, "cdn.example.com") || !strings.Contains(b.TOML, "cdn2.example.com") {
		t.Error("each config should target its own entry hostname")
	}
}

func TestDeepLinkPayloadMatchesReference(t *testing.T) {
	// Byte-for-byte parity with a known-good tt://? payload (DEEP_LINK.md format).
	const want = "AAEBAQ92cG4uZXhhbXBsZS5uZXQFBGRlbW8GC2RlbW8tc2VjcmV0AhAyMDMuMC4xMTMuMTA6NDQzDRYHMS4xLjEuMQ10bHM6Ly84LjguOC44"
	got := buildDeepLinkPayload(
		model.User{Username: "demo", Secret: "demo-secret"},
		"vpn.example.net",
		[]string{"203.0.113.10:443"},
		[]string{"1.1.1.1", "tls://8.8.8.8"},
	)
	if got != want {
		t.Fatalf("deeplink payload mismatch:\n got=%s\nwant=%s", got, want)
	}
}

func TestBuildDeepLinkAndQRLink(t *testing.T) {
	st := model.WorkedExample()
	at := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	cfg, err := Build(st, "u-alice", "entryA", Options{}, at)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(cfg.DeepLink, "tt://?") {
		t.Errorf("deep link should start with tt://?: %q", cfg.DeepLink)
	}
	payload := strings.TrimPrefix(cfg.DeepLink, "tt://?")
	wantQR := "https://" + cfg.Hostname + paths.DefaultQRPath + "#tt=" + payload
	if cfg.QRLink != wantQR {
		t.Errorf("qr link mismatch:\n got=%s\nwant=%s", cfg.QRLink, wantQR)
	}
}

// A user with no secret must not get a silently-broken deep link (one that
// ParseDeepLink rejects); Build omits the link/QR and warns instead.
func TestBuildEmptySecretOmitsDeepLink(t *testing.T) {
	st := model.WorkedExample()
	for i := range st.Users {
		if st.Users[i].ID == "u-alice" {
			st.Users[i].Secret = ""
		}
	}
	at := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	cfg, err := Build(st, "u-alice", "entryA", Options{}, at)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DeepLink != "" || cfg.QRLink != "" {
		t.Errorf("empty-secret user must not get a deep link/QR, got %q / %q", cfg.DeepLink, cfg.QRLink)
	}
	warned := false
	for _, w := range cfg.Warnings {
		if strings.Contains(w, "no secret") {
			warned = true
		}
	}
	if !warned {
		t.Errorf("expected a no-secret warning, got %v", cfg.Warnings)
	}
}

func TestParseDeepLinkRoundTrip(t *testing.T) {
	// Reference payload from DEEP_LINK.md (also used by the encoder test).
	const ref = "tt://?AAEBAQ92cG4uZXhhbXBsZS5uZXQFBGRlbW8GC2RlbW8tc2VjcmV0AhAyMDMuMC4xMTMuMTA6NDQzDRYHMS4xLjEuMQ10bHM6Ly84LjguOC44"
	dl, err := ParseDeepLink(ref)
	if err != nil {
		t.Fatal(err)
	}
	if dl.Hostname != "vpn.example.net" || dl.Username != "demo" || dl.Password != "demo-secret" {
		t.Fatalf("decoded wrong fields: %+v", dl)
	}
	if len(dl.Addresses) != 1 || dl.Addresses[0] != "203.0.113.10:443" {
		t.Fatalf("decoded wrong addresses: %+v", dl.Addresses)
	}

	// A landing link (fragment form) and a bare payload must decode identically.
	for _, in := range []string{
		"https://example.com/_int/x#tt=" + dl.payloadOnly(),
		dl.payloadOnly(),
	} {
		got, err := ParseDeepLink(in)
		if err != nil {
			t.Fatalf("parse %q: %v", in, err)
		}
		if got.Username != "demo" {
			t.Fatalf("parse %q: username=%q", in, got.Username)
		}
	}
}

// payloadOnly re-encodes for the round-trip variants above.
func (d DeepLink) payloadOnly() string {
	return buildDeepLinkPayload(
		model.User{Username: d.Username, Secret: d.Password},
		d.Hostname, d.Addresses, nil)
}

func TestParseDeepLinkRejectsGarbage(t *testing.T) {
	for _, in := range []string{"", "tt://?", "not base64!!!", "tt://?AAEB"} {
		if _, err := ParseDeepLink(in); err == nil {
			t.Errorf("expected error for %q", in)
		}
	}
}

func TestQRLinkPrefersFallbackSiteApex(t *testing.T) {
	st := model.WorkedExample()
	// Give entryA a public apex (fallback-site) domain distinct from the portal.
	st.Domains = append(st.Domains, model.Domain{ID: "d-apex", Hostname: "example.com",
		Purpose: model.PurposeFallbackSite, NodeID: "entryA"})
	cfg, err := Build(st, "u-alice", "entryA", Options{}, time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	// Endpoint host stays the portal (main-fallback); the landing link uses the apex.
	if cfg.Hostname != "cdn.example.com" {
		t.Fatalf("endpoint host should stay the portal: %q", cfg.Hostname)
	}
	if !strings.HasPrefix(cfg.QRLink, "https://example.com"+paths.DefaultQRPath+"#tt=") {
		t.Fatalf("QR link should be on the apex: %q", cfg.QRLink)
	}
}

func TestBuildRejectsNonEntry(t *testing.T) {
	st := model.WorkedExample()
	if _, err := Build(st, "u-admin", "node1", Options{}, time.Now()); err == nil {
		t.Error("building against an exit node should fail")
	}
	if _, err := Build(st, "ghost", "entryA", Options{}, time.Now()); err == nil {
		t.Error("building for unknown user should fail")
	}
}

func TestInactiveUserWarns(t *testing.T) {
	st := model.WorkedExample()
	for i := range st.Users {
		if st.Users[i].ID == "u-bob" {
			st.Users[i].Enabled = false
		}
	}
	cfg, err := Build(st, "u-bob", "entryA", Options{}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Active {
		t.Error("disabled user should be inactive")
	}
	if len(cfg.Warnings) == 0 {
		t.Error("expected a warning for an inactive user")
	}
}
