package fallback

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"trustpanel/internal/paths"
)

const serviceName = "example-fallback"

type siteKind string

const (
	siteLanding siteKind = "landing"
	sitePortal  siteKind = "portal"
)

// siteConfig is the install-time branding of the camouflage site. Defaults keep
// the original ExampleCDN / example.com appearance; the installer overrides it
// via `trustpanel fallback --brand/--domain` so one set of templates serves any
// deployment's brand and domain.
type siteConfig struct {
	Brand  string // display name, e.g. "ExampleCDN"
	Domain string // apex domain, e.g. "example.com"
	Sub    string // login subdomain the portal lives on (default "vpn")
}

func (s siteConfig) sub() string {
	if s.Sub != "" {
		return s.Sub
	}
	return "vpn"
}
func (s siteConfig) PortalHost() string { return s.sub() + "." + s.Domain }
func (s siteConfig) BaseURL() string    { return "https://" + s.Domain }
func (s siteConfig) PortalURL() string  { return "https://" + s.PortalHost() }

var site = siteConfig{Brand: "ExampleCDN", Domain: "example.com", Sub: "vpn"}

// tmplFuncs exposes the brand/domain to the HTML templates at render time.
var tmplFuncs = template.FuncMap{
	"brand":  func() string { return site.Brand },
	"domain": func() string { return site.Domain },
	"portal": func() string { return site.PortalHost() },
}

func envOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

type portalAsset struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Kind        string `json:"kind"`
	ContentType string `json:"content_type"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	Bytes       int    `json:"bytes"`
	Path        string `json:"path"`
	ThumbPath   string `json:"thumb_path"`
	Access      string `json:"access"`
}

type pageData struct {
	GeneratedAt time.Time
	Assets      []portalAsset
}

type authAttempt struct {
	Count int
	Reset time.Time
}

type authLimiter struct {
	mu       sync.Mutex
	attempts map[string]authAttempt
}

var loginLimiter = &authLimiter{attempts: map[string]authAttempt{}}

var portalAssets = []portalAsset{
	{
		ID:          "platform-overview",
		Title:       "Delivery control room",
		Kind:        "image",
		ContentType: "image/png",
		Width:       1800,
		Height:      1100,
		Bytes:       426_496,
		Path:        "/media/platform-overview.png",
		ThumbPath:   "/thumbs/platform-overview.png",
		Access:      "public",
	},
	{
		ID:          "delivery-map",
		Title:       "Regional delivery map",
		Kind:        "image",
		ContentType: "image/png",
		Width:       1800,
		Height:      1000,
		Bytes:       394_240,
		Path:        "/media/delivery-map.png",
		ThumbPath:   "/thumbs/delivery-map.png",
		Access:      "public",
	},
	{
		ID:          "campaign-cover",
		Title:       "Campaign cover set",
		Kind:        "image",
		ContentType: "image/png",
		Width:       1440,
		Height:      810,
		Bytes:       286_720,
		Path:        "/media/campaign-cover.png",
		ThumbPath:   "/thumbs/campaign-cover.png",
		Access:      "authenticated",
	},
	{
		ID:          "studio-preview",
		Title:       "Studio preview board",
		Kind:        "image",
		ContentType: "image/png",
		Width:       1280,
		Height:      720,
		Bytes:       241_664,
		Path:        "/media/studio-preview.png",
		ThumbPath:   "/thumbs/studio-preview.png",
		Access:      "authenticated",
	},
	{
		ID:          "product-release",
		Title:       "Product release pack",
		Kind:        "image",
		ContentType: "image/png",
		Width:       1600,
		Height:      900,
		Bytes:       314_368,
		Path:        "/media/product-release.png",
		ThumbPath:   "/thumbs/product-release.png",
		Access:      "authenticated",
	},
}

func Command(args []string) error {
	flags := flag.NewFlagSet("fallback", flag.ContinueOnError)
	listen := flags.String("listen", paths.DefaultFallbackListen, "HTTP listen address")
	brand := flags.String("brand", envOr("TRUSTPANEL_BRAND", "ExampleCDN"), "brand/portal name shown on the camouflage site")
	domain := flags.String("domain", envOr("TRUSTPANEL_DOMAIN", "example.com"), "apex domain shown on the camouflage site (portal host is <connect-subdomain>.<domain>)")
	connectSub := flags.String("connect-subdomain", envOr("TRUSTPANEL_CONNECT_SUBDOMAIN", "vpn"), "login subdomain the portal/endpoint lives on")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if b := strings.TrimSpace(*brand); b != "" {
		site.Brand = b
	}
	if d := strings.TrimSpace(strings.ToLower(*domain)); d != "" {
		site.Domain = d
	}
	if s := strings.TrimSpace(strings.ToLower(*connectSub)); s != "" {
		site.Sub = s
	}

	log.Printf("trustpanel fallback origin (%s / %s) listening on http://%s", site.Brand, site.Domain, *listen)
	return http.ListenAndServe(*listen, newMux())
}

func newMux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("OPTIONS /{path...}", handleOptions)
	mux.HandleFunc("GET /{$}", handleHome)
	mux.HandleFunc("GET /login", handleLogin)
	mux.HandleFunc("GET /password/reset", handlePasswordReset)
	mux.HandleFunc("GET /status", handleStatusPage)
	mux.HandleFunc("GET /docs", handleDocs)
	mux.HandleFunc("GET /terms", handleTerms)
	mux.HandleFunc("GET /privacy", handlePrivacy)
	mux.HandleFunc("GET /contact", handleContact)
	mux.HandleFunc("GET /healthz", handleHealth)
	mux.HandleFunc("GET /robots.txt", handleRobots)
	mux.HandleFunc("GET /sitemap.xml", handleSitemap)
	mux.HandleFunc("GET /favicon.ico", handleFavicon)
	mux.HandleFunc("GET /apple-touch-icon.png", handleTouchIcon)
	mux.HandleFunc("GET /site.webmanifest", handleManifest)
	mux.HandleFunc("GET /security.txt", handleSecurityTXT)
	mux.HandleFunc("GET /.well-known/security.txt", handleSecurityTXT)
	// Unguessable, unlisted client-side deep-link/QR renderer (see qrpage.go).
	mux.HandleFunc("GET "+qrPagePath(), handleQRPage)
	mux.HandleFunc("GET /api/status", handleAPIStatus)
	mux.HandleFunc("GET /api/v1/status", handleAPIStatus)
	mux.HandleFunc("GET /api/session", handleSession)
	mux.HandleFunc("GET /api/files", handleFiles)
	mux.HandleFunc("GET /api/v1/files", handleFiles)
	mux.HandleFunc("GET /api/v1/catalog", handleFiles)
	// Both GET and POST are accepted: the TrustTunnel camouflage edge reverse-
	// proxies GET to the fallback origin but not POST, so the portal forms submit
	// via GET (no secrets in the query — login sends only the email) to stay
	// functional behind the edge. POST is kept for direct/origin access.
	mux.HandleFunc("POST /api/auth", handleAuth)
	mux.HandleFunc("GET /api/auth", handleAuth)
	mux.HandleFunc("POST /api/v1/auth", handleAuth)
	mux.HandleFunc("GET /api/v1/auth", handleAuth)
	mux.HandleFunc("POST /api/password/reset", handlePasswordResetSubmit)
	mux.HandleFunc("GET /api/password/reset", handlePasswordResetSubmit)
	mux.HandleFunc("POST /api/v1/password/reset", handlePasswordResetSubmit)
	mux.HandleFunc("GET /api/v1/password/reset", handlePasswordResetSubmit)
	mux.HandleFunc("POST /api/contact", handleContactSubmit)
	mux.HandleFunc("GET /api/contact", handleContactSubmit)
	mux.HandleFunc("GET /media/{name}", handleMedia)
	mux.HandleFunc("GET /thumbs/{name}", handleThumb)
	mux.HandleFunc("GET /{path...}", handleNotFound)
	return securityHeaders(mux)
}

func kindForHost(rawHost string) siteKind {
	host := normalizeHost(rawHost)
	switch host {
	case site.Domain, "www." + site.Domain:
		return siteLanding
	case site.PortalHost():
		return sitePortal
	}
	if strings.HasPrefix(host, site.sub()+".") {
		return sitePortal
	}
	return sitePortal
}

func normalizeHost(rawHost string) string {
	host := strings.TrimSpace(strings.ToLower(rawHost))
	if host == "" {
		return ""
	}
	if strings.HasPrefix(host, "[") {
		if parsed, _, err := net.SplitHostPort(host); err == nil {
			return strings.Trim(parsed, "[]")
		}
		return strings.Trim(host, "[]")
	}
	if parsed, _, err := net.SplitHostPort(host); err == nil {
		host = parsed
	}
	return strings.TrimSuffix(host, ".")
}

func handleOptions(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Allow", "GET, HEAD, POST, OPTIONS")
	w.Header().Set("Cache-Control", "public, max-age=600")
	w.WriteHeader(http.StatusNoContent)
}

func handleHome(w http.ResponseWriter, r *http.Request) {
	if kindForHost(r.Host) == siteLanding {
		handleLandingHome(w, r)
		return
	}
	handlePortalHome(w, r)
}

func handleLogin(w http.ResponseWriter, r *http.Request) {
	if kindForHost(r.Host) == siteLanding {
		http.Redirect(w, r, site.PortalURL()+"/", http.StatusFound)
		return
	}
	handlePortalHome(w, r)
}

func handleLandingHome(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=180")
	_ = landingTemplate.Execute(w, pageData{
		GeneratedAt: time.Now().UTC(),
		Assets:      portalAssets,
	})
}

func handlePortalHome(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	http.SetCookie(w, &http.Cookie{
		Name:     "pc_session",
		Value:    sessionSeed(r),
		Path:     "/",
		MaxAge:   1800,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
	_ = homeTemplate.Execute(w, pageData{
		GeneratedAt: time.Now().UTC(),
		Assets:      portalAssets,
	})
}

func handleStatusPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=120")
	_ = statusTemplate.Execute(w, pageData{
		GeneratedAt: time.Now().UTC(),
		Assets:      portalAssets,
	})
}

func handleDocs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	_ = docsTemplate.Execute(w, pageData{
		GeneratedAt: time.Now().UTC(),
		Assets:      portalAssets,
	})
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"service": serviceName,
		"status":  "ok",
		"role":    "fallback-origin",
	})
}

func handleAPIStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"service":       serviceName,
		"status":        "ok",
		"auth_required": true,
		"generated_at":  time.Now().UTC(),
	})
}

func handleSession(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusUnauthorized, map[string]any{
		"status": "unauthenticated",
		"error":  "authentication required",
	})
}

func handleFiles(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusUnauthorized, map[string]any{
		"status": "unauthenticated",
		"error":  "authentication required",
	})
}

func handleAuth(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	_, _ = io.Copy(io.Discard, r.Body)

	remote := clientAddress(r)
	count, limited := loginLimiter.record(remote, time.Now().UTC())
	status := http.StatusUnauthorized
	payload := map[string]any{
		"status": "invalid_credentials",
		"error":  "invalid email or password",
	}
	if limited {
		status = http.StatusTooManyRequests
		payload = map[string]any{
			"status":      "rate_limited",
			"error":       "too many sign-in attempts",
			"retry_after": 900,
		}
		w.Header().Set("Retry-After", "900")
	} else {
		time.Sleep(350 * time.Millisecond)
	}
	log.Printf("example portal auth attempt remote=%s status=%d count=%d", remote, status, count)
	writeJSON(w, status, payload)
}

func (l *authLimiter) record(key string, now time.Time) (int, bool) {
	if strings.TrimSpace(key) == "" {
		key = "unknown"
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	current := l.attempts[key]
	if current.Reset.IsZero() || now.After(current.Reset) {
		current = authAttempt{Reset: now.Add(15 * time.Minute)}
	}
	current.Count++
	l.attempts[key] = current
	return current.Count, current.Count > 5
}

func handlePasswordReset(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = resetTemplate.Execute(w, pageData{GeneratedAt: time.Now().UTC()})
}

// handlePasswordResetSubmit always returns the same anti-enumeration response so
// the form cannot be used to probe which addresses have accounts.
func handlePasswordResetSubmit(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	_ = r.ParseForm()
	email := strings.TrimSpace(r.FormValue("email")) // query (GET via edge) or form (POST)

	remote := clientAddress(r)
	count, limited := loginLimiter.record("reset:"+remote, time.Now().UTC())
	if limited {
		w.Header().Set("Retry-After", "900")
		writeJSON(w, http.StatusTooManyRequests, map[string]any{
			"status":      "rate_limited",
			"error":       "too many reset requests",
			"retry_after": 900,
		})
		return
	}
	time.Sleep(300 * time.Millisecond)
	log.Printf("example portal reset request remote=%s has_email=%t count=%d", remote, email != "", count)
	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "accepted",
		"message": "If an account is associated with that email, we've sent password reset instructions.",
	})
}

func handleContact(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=600")
	_ = contactTemplate.Execute(w, pageData{GeneratedAt: time.Now().UTC()})
}

// handleContactSubmit accepts a sales/support inquiry. Like the other portal
// forms it is reachable over GET so it works behind the TrustTunnel edge, and it
// returns a generic acknowledgement.
func handleContactSubmit(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	_ = r.ParseForm()
	email := strings.TrimSpace(r.FormValue("email"))

	remote := clientAddress(r)
	count, limited := loginLimiter.record("contact:"+remote, time.Now().UTC())
	if limited {
		w.Header().Set("Retry-After", "900")
		writeJSON(w, http.StatusTooManyRequests, map[string]any{
			"status":      "rate_limited",
			"error":       "too many messages",
			"retry_after": 900,
		})
		return
	}
	time.Sleep(250 * time.Millisecond)
	log.Printf("example contact inquiry remote=%s has_email=%t count=%d", remote, email != "", count)
	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "received",
		"message": "Thanks for reaching out — our team will reply within one business day.",
	})
}

func handleTerms(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	_ = legalTemplate.Execute(w, legalData{
		Title:   "Terms of Service",
		Updated: "2026-02-18",
		Body:    fillSite(termsBody),
	})
}

func handlePrivacy(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	_ = legalTemplate.Execute(w, legalData{
		Title:   "Privacy Policy",
		Updated: "2026-02-18",
		Body:    fillSite(privacyBody),
	})
}

func handleRobots(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	base := site.PortalURL()
	if kindForHost(r.Host) == siteLanding {
		base = site.BaseURL()
	}
	_, _ = fmt.Fprintf(w, "User-agent: *\nAllow: /\nDisallow: /api/\nSitemap: %s/sitemap.xml\n", base)
}

func handleSitemap(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	now := time.Now().UTC().Format("2006-01-02")
	base := site.PortalURL()
	if kindForHost(r.Host) == siteLanding {
		base = site.BaseURL()
	}
	_, _ = fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <url><loc>%s/</loc><lastmod>%s</lastmod><priority>1.0</priority></url>
  <url><loc>%s/status</loc><lastmod>%s</lastmod><priority>0.7</priority></url>
  <url><loc>%s/docs</loc><lastmod>%s</lastmod><priority>0.6</priority></url>
  <url><loc>%s/contact</loc><lastmod>%s</lastmod><priority>0.5</priority></url>
  <url><loc>%s/terms</loc><lastmod>%s</lastmod><priority>0.3</priority></url>
  <url><loc>%s/privacy</loc><lastmod>%s</lastmod><priority>0.3</priority></url>
</urlset>
`, base, now, base, now, base, now, base, now, base, now, base, now)
}

func handleFavicon(w http.ResponseWriter, r *http.Request) {
	writeIcon(w, "favicon", 32, 3600)
}

func handleTouchIcon(w http.ResponseWriter, r *http.Request) {
	writeIcon(w, "touch-icon", 180, 7200)
}

func handleManifest(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/manifest+json; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	name := site.Brand + " Client Portal"
	if kindForHost(r.Host) == siteLanding {
		name = site.Brand
	}
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"name":             name,
		"short_name":       site.Brand,
		"start_url":        "/",
		"display":          "standalone",
		"background_color": "#f6f7f9",
		"theme_color":      "#24436f",
		"icons": []map[string]string{
			{
				"src":   "/apple-touch-icon.png",
				"sizes": "180x180",
				"type":  "image/png",
			},
		},
	})
}

func handleSecurityTXT(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	expires := time.Now().UTC().Add(180 * 24 * time.Hour).Format(time.RFC3339)
	_, _ = fmt.Fprintf(w, "Contact: security@%s\nExpires: %s\nPreferred-Languages: en\n", site.Domain, expires)
}

func handleNotFound(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNotFound)
	_ = notFoundTemplate.Execute(w, map[string]string{
		"Path": r.URL.Path,
	})
}

func handleMedia(w http.ResponseWriter, r *http.Request) {
	id, ok := pngAssetID(r.PathValue("name"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	item, ok := assetByID(id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	writePNG(w, r, item.ID, item.Width, item.Height, 3600)
}

func handleThumb(w http.ResponseWriter, r *http.Request) {
	id, ok := pngAssetID(r.PathValue("name"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	item, ok := assetByID(id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	writePNG(w, r, item.ID+"-thumb", 420, 260, 7200)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'unsafe-inline' 'self'; script-src 'unsafe-inline' 'self'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

func clientAddress(r *http.Request) string {
	for _, header := range []string{"CF-Connecting-IP", "X-Real-IP", "X-Forwarded-For"} {
		value := strings.TrimSpace(r.Header.Get(header))
		if value == "" {
			continue
		}
		if header == "X-Forwarded-For" {
			value = strings.TrimSpace(strings.Split(value, ",")[0])
		}
		if value != "" {
			return value
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func sessionSeed(r *http.Request) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d:%s", time.Now().UnixNano(), clientAddress(r))))
	return hex.EncodeToString(sum[:12])
}
