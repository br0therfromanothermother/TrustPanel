package fallback

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFallbackStatusEndpoint(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	rec := httptest.NewRecorder()
	newMux().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected ok, got %d: %s", rec.Code, rec.Body.String())
	}
	var payload map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if payload["service"] != serviceName {
		t.Fatalf("unexpected service: %v", payload["service"])
	}
	if payload["status"] != "ok" {
		t.Fatalf("unexpected status: %v", payload["status"])
	}
	if payload["auth_required"] != true {
		t.Fatalf("expected auth_required true: %+v", payload)
	}
}

func TestFallbackMediaPreview(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/media/campaign-cover.png", nil)
	rec := httptest.NewRecorder()
	newMux().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected ok, got %d: %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("expected image/png, got %q", rec.Header().Get("Content-Type"))
	}
	if rec.Header().Get("Accept-Ranges") != "bytes" {
		t.Fatalf("expected Accept-Ranges bytes")
	}
	if rec.Body.Len() == 0 {
		t.Fatal("expected generated media body")
	}
}

func TestFallbackHomeUsesClientPortalTheme(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "vpn.example.com"
	rec := httptest.NewRecorder()
	newMux().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected ok, got %d", rec.Code)
	}
	body := rec.Body.String()
	for _, expected := range []string{"ExampleCDN Client Portal", "Login to your account", `<a class="brand" href="https://example.com/">`, `href="https://example.com/"`} {
		if !strings.Contains(body, expected) {
			t.Fatalf("expected portal body to contain %q", expected)
		}
	}
	for _, forbidden := range []string{"playline", "game streams", "hit rate", "cache warm", "test object", "diagnostics", "edge-01"} {
		if strings.Contains(strings.ToLower(body), forbidden) {
			t.Fatalf("old or diagnostic theme leaked into portal page: %q", forbidden)
		}
	}
}

func TestFallbackLandingUsesExampleCDNHost(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "example.com"
	rec := httptest.NewRecorder()
	newMux().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected ok, got %d", rec.Code)
	}
	body := rec.Body.String()
	for _, expected := range []string{"Fast CDN delivery for media teams", "Managed content delivery for private media", `href="https://vpn.example.com/"`} {
		if !strings.Contains(body, expected) {
			t.Fatalf("expected landing body to contain %q", expected)
		}
	}
	for _, forbidden := range []string{"Sign in to portal", `id="login-form"`, "hit rate", "cache warm", "test object", "diagnostics", "edge-01"} {
		if strings.Contains(strings.ToLower(body), strings.ToLower(forbidden)) {
			t.Fatalf("portal or diagnostic theme leaked into landing page: %q", forbidden)
		}
	}
}

func TestFallbackLandingLoginRedirectsToPortal(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	req.Host = "example.com"
	rec := httptest.NewRecorder()
	newMux().ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("expected redirect, got %d", rec.Code)
	}
	if location := rec.Header().Get("Location"); location != "https://vpn.example.com/" {
		t.Fatalf("unexpected redirect location: %q", location)
	}
}

func TestFallbackDefaultHostUsesClientPortal(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "cdn.example.com" // a non-apex (endpoint/unknown) host falls through to the portal
	rec := httptest.NewRecorder()
	newMux().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected ok, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "ExampleCDN Client Portal") {
		t.Fatalf("expected client portal brand")
	}
	for _, forbidden := range []string{"playline", "game streams", "hit rate", "cache warm", "test object", "diagnostics", "edge-01"} {
		if strings.Contains(strings.ToLower(body), forbidden) {
			t.Fatalf("old or diagnostic theme leaked into home page: %q", forbidden)
		}
	}
}

func TestFallbackCrawlerEndpoints(t *testing.T) {
	cases := []struct {
		path        string
		contentType string
		contains    string
	}{
		{path: "/robots.txt", contentType: "text/plain", contains: "Sitemap:"},
		{path: "/sitemap.xml", contentType: "application/xml", contains: "<urlset"},
		{path: "/.well-known/security.txt", contentType: "text/plain", contains: "Contact:"},
		{path: "/site.webmanifest", contentType: "application/manifest+json", contains: "ExampleCDN Client Portal"},
	}
	for _, tt := range cases {
		req := httptest.NewRequest(http.MethodGet, tt.path, nil)
		req.Host = "cdn.example.com" // non-apex host → portal manifest ("<Brand> Client Portal")
		rec := httptest.NewRecorder()
		newMux().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: expected ok, got %d: %s", tt.path, rec.Code, rec.Body.String())
		}
		if !strings.HasPrefix(rec.Header().Get("Content-Type"), tt.contentType) {
			t.Fatalf("%s: unexpected content type %q", tt.path, rec.Header().Get("Content-Type"))
		}
		if !strings.Contains(rec.Body.String(), tt.contains) {
			t.Fatalf("%s: expected body to contain %q, got %q", tt.path, tt.contains, rec.Body.String())
		}
	}
}

func TestFallbackIcons(t *testing.T) {
	for _, path := range []string{"/favicon.ico", "/apple-touch-icon.png"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		newMux().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: expected ok, got %d", path, rec.Code)
		}
		if rec.Header().Get("Content-Type") != "image/png" {
			t.Fatalf("%s: expected image/png, got %q", path, rec.Header().Get("Content-Type"))
		}
		if rec.Body.Len() == 0 {
			t.Fatalf("%s: expected icon body", path)
		}
	}
}

func TestFallbackProbeBehavior(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/wp-login.php", nil)
	rec := httptest.NewRecorder()
	newMux().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected not found, got %d", rec.Code)
	}
	body := strings.ToLower(rec.Body.String())
	for _, forbidden := range []string{"trustpanel", "trusttunnel", "vpn", "stack trace"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("not found body leaks %q: %s", forbidden, rec.Body.String())
		}
	}

	optionsReq := httptest.NewRequest(http.MethodOptions, "/wp-login.php", nil)
	optionsRec := httptest.NewRecorder()
	newMux().ServeHTTP(optionsRec, optionsReq)
	if optionsRec.Code != http.StatusNoContent {
		t.Fatalf("expected options no content, got %d", optionsRec.Code)
	}
	if optionsRec.Header().Get("Allow") != "GET, HEAD, POST, OPTIONS" {
		t.Fatalf("unexpected Allow header: %q", optionsRec.Header().Get("Allow"))
	}

	postReq := httptest.NewRequest(http.MethodPost, "/api/status", nil)
	postRec := httptest.NewRecorder()
	newMux().ServeHTTP(postRec, postReq)
	if postRec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected method not allowed, got %d", postRec.Code)
	}
}

func TestFallbackAuthDoesNotAcceptCredentials(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/auth", strings.NewReader("email=user@example.com&password=secret"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr = "203.0.113.20:49152"
	rec := httptest.NewRecorder()
	newMux().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected unauthorized, got %d: %s", rec.Code, rec.Body.String())
	}
	body := strings.ToLower(rec.Body.String())
	if strings.Contains(body, "secret") || strings.Contains(body, "user@example.com") {
		t.Fatalf("auth response leaked submitted credentials: %s", rec.Body.String())
	}
}

func TestFallbackFilesRequireAuth(t *testing.T) {
	for _, path := range []string{"/api/session", "/api/files", "/api/v1/catalog"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		newMux().ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s: expected unauthorized, got %d", path, rec.Code)
		}
	}
}

func TestFallbackPasswordResetPage(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/password/reset", nil)
	req.Host = "vpn.example.com"
	rec := httptest.NewRecorder()
	newMux().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("reset page: want 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Reset your password") || !strings.Contains(body, `action="/api/password/reset"`) {
		t.Errorf("reset page missing form:\n%s", body[:min(len(body), 400)])
	}
}

func TestFallbackPasswordResetIsAntiEnumeration(t *testing.T) {
	// Both a likely-existing and a random address must get the identical reply.
	var bodies []string
	for _, email := range []string{"admin@example.com", "nobody-xyz@example.com"} {
		req := httptest.NewRequest(http.MethodPost, "/api/password/reset", strings.NewReader("email="+email))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.RemoteAddr = "203.0.113." + email[:1] + ":1234" // distinct keys so neither rate-limits
		rec := httptest.NewRecorder()
		newMux().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("reset submit %s: want 200, got %d", email, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "If an account") {
			t.Errorf("reset submit %s: missing anti-enumeration message: %s", email, rec.Body.String())
		}
		bodies = append(bodies, rec.Body.String())
	}
	if bodies[0] != bodies[1] {
		t.Errorf("reset response leaks account existence:\n%s\nvs\n%s", bodies[0], bodies[1])
	}
}

func TestFallbackLegalPages(t *testing.T) {
	for _, p := range []string{"/terms", "/privacy"} {
		req := httptest.NewRequest(http.MethodGet, p, nil)
		rec := httptest.NewRecorder()
		newMux().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("%s: want 200, got %d", p, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "ExampleCDN") {
			t.Errorf("%s: missing brand", p)
		}
	}
}

func TestFallbackContactPageAndForm(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/contact", nil)
	rec := httptest.NewRecorder()
	newMux().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "sales@example.com") {
		t.Fatalf("contact page: code=%d", rec.Code)
	}
	// Form submits via GET behind the edge.
	req = httptest.NewRequest(http.MethodGet, "/api/contact?email=a@b.com&topic=Sales", nil)
	rec = httptest.NewRecorder()
	newMux().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "reply within one business day") {
		t.Fatalf("contact submit: code=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestFallbackLandingPricingEUR(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "example.com"
	rec := httptest.NewRecorder()
	newMux().ServeHTTP(rec, req)
	body := rec.Body.String()
	for _, want := range []string{"€49", "€199", "€890", "All prices in EUR"} {
		if !strings.Contains(body, want) {
			t.Errorf("landing pricing missing %q", want)
		}
	}
}

func TestFallbackBrandConfigurable(t *testing.T) {
	saved := site
	defer func() { site = saved }()
	site = siteConfig{Brand: "Acme CDN", Domain: "acme.example"}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "acme.example"
	rec := httptest.NewRecorder()
	newMux().ServeHTTP(rec, req)
	body := rec.Body.String()
	for _, want := range []string{"Acme CDN", "https://vpn.acme.example/"} {
		if !strings.Contains(body, want) {
			t.Errorf("landing missing %q after rebrand", want)
		}
	}
	if strings.Contains(body, "ExampleCDN") || strings.Contains(body, "example.com") {
		t.Errorf("rebranded landing still leaks default brand/domain")
	}
	// Legal body (raw HTML, filled via fillSite) is rebranded too.
	rec = httptest.NewRecorder()
	newMux().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/terms", nil))
	if !strings.Contains(rec.Body.String(), "legal@acme.example") {
		t.Errorf("terms not rebranded: %s", rec.Body.String()[:200])
	}
}

func TestQRPageServedAndWellFormed(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, qrPagePath(), nil)
	rec := httptest.NewRecorder()
	newMux().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("qr page: got %d", rec.Code)
	}
	body := rec.Body.String()

	// Regression: the app script must come AFTER the form nodes it queries, or
	// getElementById('go') is null and the whole page is dead.
	formIdx := strings.Index(body, `id="go"`)
	appIdx := strings.Index(body, "addEventListener('click', run)")
	if formIdx < 0 || appIdx < 0 {
		t.Fatalf("missing form button or app wiring (form=%d app=%d)", formIdx, appIdx)
	}
	if formIdx > appIdx {
		t.Fatalf("app script (%d) runs before the form (%d) — button would be dead", appIdx, formIdx)
	}

	// Landing affordances and configurable store links must be present.
	for _, want := range []string{
		"Open in app", "App Store (iOS)", "Google Play (Android)",
		"https://agrd.io/ios_trusttunnel", "https://agrd.io/android_trusttunnel",
		"createSvgTag", // QR library inlined
	} {
		if !strings.Contains(body, want) {
			t.Errorf("qr page missing %q", want)
		}
	}
	if !strings.Contains(rec.Header().Get("X-Robots-Tag"), "noindex") {
		t.Errorf("qr page should be noindex")
	}

	// Two-faced cover: with no payload the page must look like a bland diagnostic,
	// and the landing styling must be gated behind a valid payload (injected by
	// JS, applied via body.className='landing') — never present in the static
	// cover markup, so a stray visitor sees only the probe.
	if !strings.Contains(body, "<title>tlv-probe</title>") {
		t.Errorf("cover should be the unbranded tlv-probe diagnostic")
	}
	if strings.Contains(body, "config") {
		t.Errorf("cover must not leak the word 'config'")
	}
	if !strings.Contains(body, "body.className='landing'") {
		t.Errorf("landing styling should be gated behind a valid payload")
	}
	// The rich card style must travel as injected JS data, not as live CSS that a
	// stray visitor's browser would apply.
	headEnd := strings.Index(body, "</head>")
	if headEnd >= 0 && strings.Contains(body[:headEnd], ".card{") {
		t.Errorf("landing card CSS must not be live in <head>; inject it on demand")
	}
}
