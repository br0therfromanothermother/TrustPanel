package certmgr

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeTestLeaf writes a self-signed cert covering domains, expiring at notAfter.
func writeTestLeaf(t *testing.T, path string, domains []string, notAfter time.Time) {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: domains[0]},
		DNSNames:     domains,
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     notAfter,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeChain(path, [][]byte{der}); err != nil {
		t.Fatal(err)
	}
}

func TestNeedsRenewal(t *testing.T) {
	dir := t.TempDir()
	cert := filepath.Join(dir, "cert.pem")
	cfg := Config{Domains: []string{"a.example.com"}, CertPath: cert, KeyPath: filepath.Join(dir, "key.pem"), RenewBefore: 30 * 24 * time.Hour}

	// Missing file -> needs renewal.
	if need, _ := cfg.NeedsRenewal(time.Now()); !need {
		t.Fatal("missing cert should need renewal")
	}

	// Fresh, covering cert -> no renewal.
	writeTestLeaf(t, cert, []string{"a.example.com"}, time.Now().Add(90*24*time.Hour))
	if need, reason := cfg.NeedsRenewal(time.Now()); need {
		t.Fatalf("fresh cert should not need renewal: %s", reason)
	}

	// Expiring within window -> renew.
	writeTestLeaf(t, cert, []string{"a.example.com"}, time.Now().Add(10*24*time.Hour))
	if need, _ := cfg.NeedsRenewal(time.Now()); !need {
		t.Fatal("soon-expiring cert should need renewal")
	}

	// Missing a newly-requested domain -> renew even if not expiring.
	writeTestLeaf(t, cert, []string{"a.example.com"}, time.Now().Add(90*24*time.Hour))
	cfg.Domains = []string{"a.example.com", "b.example.com"}
	if need, reason := cfg.NeedsRenewal(time.Now()); !need {
		t.Fatal("added domain should force renewal")
	} else if reason == "" {
		t.Fatal("expected a reason")
	}
}

func TestCertExpiry(t *testing.T) {
	dir := t.TempDir()
	cert := filepath.Join(dir, "cert.pem")
	want := time.Now().Add(42 * 24 * time.Hour).Truncate(time.Second)
	writeTestLeaf(t, cert, []string{"x.example.com"}, want)
	got, err := CertExpiry(cert)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Equal(want.UTC().Truncate(time.Second)) && got.Sub(want).Abs() > time.Second {
		t.Fatalf("expiry mismatch: got %v want %v", got, want)
	}
	if _, err := CertExpiry(filepath.Join(dir, "missing.pem")); err == nil {
		t.Fatal("expected error for missing cert")
	}
}

func TestWriteKeyPermissions(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "key.pem")
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err := writeKey(keyPath, key); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("private key must be 0600, got %o", info.Mode().Perm())
	}
	// Round-trips as a PEM EC key.
	b, _ := os.ReadFile(keyPath)
	block, _ := pem.Decode(b)
	if block == nil || block.Type != "EC PRIVATE KEY" {
		t.Fatalf("unexpected key PEM: %v", block)
	}
	if _, err := x509.ParseECPrivateKey(block.Bytes); err != nil {
		t.Fatalf("key does not parse: %v", err)
	}
}

func TestHTTP01SolverServesToken(t *testing.T) {
	s := &http01Solver{resps: map[string]string{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/acme-challenge/", func(w http.ResponseWriter, r *http.Request) {
		s.mu.RLock()
		body, ok := s.resps[r.URL.Path]
		s.mu.RUnlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(body))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// Unknown token -> 404.
	resp, _ := http.Get(srv.URL + "/.well-known/acme-challenge/unknown")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown token should 404, got %d", resp.StatusCode)
	}

	// Present a token, then it is served verbatim.
	s.mu.Lock()
	s.resps["/.well-known/acme-challenge/tok123"] = "keyauth-value"
	s.mu.Unlock()
	resp, _ = http.Get(srv.URL + "/.well-known/acme-challenge/tok123")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("known token should 200, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "keyauth-value" {
		t.Fatalf("served body = %q, want keyauth-value", body)
	}
}

// writeTestLeafSubject writes a self-signed cert with a chosen Subject (which,
// being self-signed, is also the Issuer) so CertIssuer's label logic is testable.
func writeTestLeafSubject(t *testing.T, path string, subject pkix.Name) {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      subject,
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeChain(path, [][]byte{der}); err != nil {
		t.Fatal(err)
	}
}

func TestCertIssuer(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name    string
		subject pkix.Name
		want    string
	}{
		// LE production: O carries the label, CN is just the intermediate id.
		{"production", pkix.Name{Organization: []string{"Let's Encrypt"}, CommonName: "R10"}, "Let's Encrypt"},
		// LE staging: O matches production; the marker is only in the CN. Must not be dropped.
		{"staging-in-cn", pkix.Name{Organization: []string{"Let's Encrypt"}, CommonName: "(STAGING) Counterfeit Cashew R10"}, "Let's Encrypt (STAGING) Counterfeit Cashew R10"},
		// Marker already in O: don't duplicate it from the CN.
		{"staging-in-org", pkix.Name{Organization: []string{"(STAGING) Let's Encrypt"}, CommonName: "Artificial Apricot R3"}, "(STAGING) Let's Encrypt"},
		// No Organization: fall back to CN.
		{"cn-only", pkix.Name{CommonName: "Self-Signed CA"}, "Self-Signed CA"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cert := filepath.Join(dir, tc.name+".pem")
			writeTestLeafSubject(t, cert, tc.subject)
			got, err := CertIssuer(cert)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("CertIssuer = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSortedDomains(t *testing.T) {
	got := SortedDomains([]string{"B.com", "a.com", "b.com", "", " a.com "})
	if len(got) != 2 || got[0] != "a.com" || got[1] != "b.com" {
		t.Fatalf("dedup/sort/lower failed: %v", got)
	}
}
