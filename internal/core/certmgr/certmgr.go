// Package certmgr is the node-local ACME (Let's Encrypt) certificate manager
// for entry nodes. The agent owns issuance: the private key never leaves the
// node, HTTP-01 is validated on the entry's :80,
// and only file paths (never keys) travel in desired-state. TrustTunnel itself
// does no ACME — it just reads cert_chain_path/private_key_path, so this package
// writes those two files and the panel triggers a reload via reconcile.
package certmgr

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/acme"
)

// LetsEncryptProduction and LetsEncryptStaging are the well-known ACMEv2
// directory endpoints. Staging issues untrusted certs but shares the same flow
// and has far higher rate limits — use it to validate a deployment first.
const (
	LetsEncryptProduction = "https://acme-v02.api.letsencrypt.org/directory"
	LetsEncryptStaging    = "https://acme-staging-v02.api.letsencrypt.org/directory"
)

// Config describes one certificate to keep valid.
type Config struct {
	Domains        []string      // SAN list; first is the common name
	Email          string        // ACME account contact (mailto:)
	DirectoryURL   string        // ACME directory; defaults to Let's Encrypt production
	CertPath       string        // fullchain PEM output (TrustTunnel cert_chain_path)
	KeyPath        string        // private key PEM output (TrustTunnel private_key_path)
	AccountKeyPath string        // persisted ACME account key (created if absent)
	RenewBefore    time.Duration // renew when the leaf expires within this window
	ChallengeAddr  string        // HTTP-01 listen address (default ":80")
}

func (c *Config) withDefaults() {
	if c.DirectoryURL == "" {
		c.DirectoryURL = LetsEncryptProduction
	}
	if c.RenewBefore <= 0 {
		c.RenewBefore = 30 * 24 * time.Hour
	}
	if c.ChallengeAddr == "" {
		c.ChallengeAddr = ":80"
	}
	if c.AccountKeyPath == "" {
		c.AccountKeyPath = filepath.Join(filepath.Dir(c.CertPath), "acme_account.key")
	}
}

// CertExpiry returns the NotAfter of the leaf certificate at path, or an error
// if the file is missing or unparseable. Used by the agent to report tls_expiry.
func CertExpiry(certPath string) (time.Time, error) {
	leaf, err := loadLeaf(certPath)
	if err != nil {
		return time.Time{}, err
	}
	return leaf.NotAfter, nil
}

// CertIssuer returns a short label for the leaf certificate's issuer (e.g.
// "Let's Encrypt" in production, "Let's Encrypt (STAGING) Counterfeit Cashew R10"
// from the staging directory) so the panel can show it and tell a staging cert
// from a real one. It prefers the issuer Organization but folds in the Common
// Name when that is where the staging marker lives: LE's staging intermediates
// share O="Let's Encrypt" with production and carry "(STAGING)" only in the CN,
// so returning O alone would make a staging leaf look browser-trusted.
func CertIssuer(certPath string) (string, error) {
	leaf, err := loadLeaf(certPath)
	if err != nil {
		return "", err
	}
	org := ""
	if orgs := leaf.Issuer.Organization; len(orgs) > 0 {
		org = orgs[0]
	}
	cn := leaf.Issuer.CommonName
	// The staging marker can sit in either field; never drop it.
	cnStaging := strings.Contains(strings.ToLower(cn), "staging")
	orgStaging := strings.Contains(strings.ToLower(org), "staging")
	if org != "" && cnStaging && !orgStaging {
		return org + " " + cn, nil
	}
	if org != "" {
		return org, nil
	}
	return cn, nil
}

// NeedsRenewal reports whether the cert at CertPath is missing, does not cover
// every configured domain, or expires within RenewBefore of now.
func (c *Config) NeedsRenewal(now time.Time) (bool, string) {
	c.withDefaults()
	leaf, err := loadLeaf(c.CertPath)
	if err != nil {
		return true, "no usable certificate: " + err.Error()
	}
	have := map[string]bool{}
	for _, d := range leaf.DNSNames {
		have[strings.ToLower(d)] = true
	}
	for _, want := range c.Domains {
		if !have[strings.ToLower(want)] {
			return true, "certificate is missing domain " + want
		}
	}
	if remaining := leaf.NotAfter.Sub(now); remaining < c.RenewBefore {
		return true, fmt.Sprintf("certificate expires in %s (< %s)", remaining.Round(time.Hour), c.RenewBefore)
	}
	return false, ""
}

// Ensure issues or renews the certificate if needed. It returns whether a new
// certificate was written and the leaf's NotAfter. Issuance binds ChallengeAddr
// for the duration of the HTTP-01 validation only.
func Ensure(ctx context.Context, cfg Config) (issued bool, notAfter time.Time, err error) {
	cfg.withDefaults()
	if len(cfg.Domains) == 0 {
		return false, time.Time{}, fmt.Errorf("certmgr: no domains configured")
	}
	if need, _ := cfg.NeedsRenewal(time.Now()); !need {
		na, _ := CertExpiry(cfg.CertPath)
		return false, na, nil
	}
	na, err := issue(ctx, &cfg)
	if err != nil {
		return false, time.Time{}, err
	}
	return true, na, nil
}

func issue(ctx context.Context, cfg *Config) (time.Time, error) {
	accountKey, err := loadOrCreateAccountKey(cfg.AccountKeyPath)
	if err != nil {
		return time.Time{}, fmt.Errorf("account key: %w", err)
	}
	client := &acme.Client{Key: accountKey, DirectoryURL: cfg.DirectoryURL}

	acct := &acme.Account{}
	if cfg.Email != "" {
		acct.Contact = []string{"mailto:" + cfg.Email}
	}
	// Registering an already-registered key returns the existing account; both
	// are fine. Treat "account exists" as success.
	if _, err := client.Register(ctx, acct, acme.AcceptTOS); err != nil &&
		!strings.Contains(strings.ToLower(err.Error()), "already") {
		return time.Time{}, fmt.Errorf("acme register: %w", err)
	}

	order, err := client.AuthorizeOrder(ctx, acme.DomainIDs(cfg.Domains...))
	if err != nil {
		return time.Time{}, fmt.Errorf("authorize order: %w", err)
	}

	solver := &http01Solver{client: client}
	if err := solver.start(cfg.ChallengeAddr); err != nil {
		return time.Time{}, fmt.Errorf("http-01 listener on %s: %w", cfg.ChallengeAddr, err)
	}
	defer solver.stop()

	for _, authzURL := range order.AuthzURLs {
		authz, err := client.GetAuthorization(ctx, authzURL)
		if err != nil {
			return time.Time{}, fmt.Errorf("get authz: %w", err)
		}
		if authz.Status == acme.StatusValid {
			continue
		}
		chal := findChallenge(authz.Challenges, "http-01")
		if chal == nil {
			return time.Time{}, fmt.Errorf("no http-01 challenge for %v", authz.Identifier)
		}
		if err := solver.present(chal); err != nil {
			return time.Time{}, err
		}
		if _, err := client.Accept(ctx, chal); err != nil {
			return time.Time{}, fmt.Errorf("accept challenge: %w", err)
		}
		if _, err := client.WaitAuthorization(ctx, authz.URI); err != nil {
			return time.Time{}, fmt.Errorf("wait authorization for %v: %w", authz.Identifier, err)
		}
	}

	if _, err := client.WaitOrder(ctx, order.URI); err != nil {
		return time.Time{}, fmt.Errorf("wait order: %w", err)
	}

	certKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return time.Time{}, err
	}
	csr, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject:  pkix.Name{CommonName: cfg.Domains[0]},
		DNSNames: cfg.Domains,
	}, certKey)
	if err != nil {
		return time.Time{}, fmt.Errorf("create csr: %w", err)
	}
	der, _, err := client.CreateOrderCert(ctx, order.FinalizeURL, csr, true)
	if err != nil {
		return time.Time{}, fmt.Errorf("finalize order: %w", err)
	}

	leaf, err := x509.ParseCertificate(der[0])
	if err != nil {
		return time.Time{}, fmt.Errorf("parse issued cert: %w", err)
	}
	if err := writeChain(cfg.CertPath, der); err != nil {
		return time.Time{}, err
	}
	if err := writeKey(cfg.KeyPath, certKey); err != nil {
		return time.Time{}, err
	}
	return leaf.NotAfter, nil
}

func findChallenge(chals []*acme.Challenge, typ string) *acme.Challenge {
	for _, c := range chals {
		if c.Type == typ {
			return c
		}
	}
	return nil
}

// http01Solver answers ACME HTTP-01 challenges on a dedicated listener. The
// token→keyAuth map is guarded so multiple domain authorizations can be solved
// against one listener.
type http01Solver struct {
	client *acme.Client
	srv    *http.Server
	ln     net.Listener

	mu    sync.RWMutex
	resps map[string]string // challenge path -> response body
}

func (s *http01Solver) start(addr string) error {
	s.resps = map[string]string{}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	s.ln = ln
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/acme-challenge/", func(w http.ResponseWriter, r *http.Request) {
		s.mu.RLock()
		body, ok := s.resps[r.URL.Path]
		s.mu.RUnlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(body))
	})
	s.srv = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = s.srv.Serve(ln) }()
	return nil
}

func (s *http01Solver) present(chal *acme.Challenge) error {
	resp, err := s.client.HTTP01ChallengeResponse(chal.Token)
	if err != nil {
		return fmt.Errorf("challenge response: %w", err)
	}
	path := s.client.HTTP01ChallengePath(chal.Token)
	s.mu.Lock()
	s.resps[path] = resp
	s.mu.Unlock()
	return nil
}

func (s *http01Solver) stop() {
	if s.srv != nil {
		_ = s.srv.Close()
	}
}

// ---- file + key helpers ----

func loadLeaf(certPath string) (*x509.Certificate, error) {
	pemBytes, err := os.ReadFile(certPath)
	if err != nil {
		return nil, err
	}
	for {
		var block *pem.Block
		block, pemBytes = pem.Decode(pemBytes)
		if block == nil {
			break
		}
		if block.Type == "CERTIFICATE" {
			return x509.ParseCertificate(block.Bytes)
		}
	}
	return nil, fmt.Errorf("no CERTIFICATE block in %s", certPath)
}

func loadOrCreateAccountKey(path string) (*ecdsa.PrivateKey, error) {
	if b, err := os.ReadFile(path); err == nil {
		block, _ := pem.Decode(b)
		if block == nil {
			return nil, fmt.Errorf("account key %s: not PEM", path)
		}
		return x509.ParseECPrivateKey(block.Bytes)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	if err := writeKey(path, key); err != nil {
		return nil, err
	}
	return key, nil
}

// writeChain writes a PEM fullchain atomically with 0644 (public cert).
func writeChain(path string, der [][]byte) error {
	var buf strings.Builder
	for _, b := range der {
		_ = pem.Encode(&stringWriter{&buf}, &pem.Block{Type: "CERTIFICATE", Bytes: b})
	}
	return atomicWrite(path, []byte(buf.String()), 0o644)
}

// writeKey writes an EC private key PEM atomically with 0600 (secret).
func writeKey(path string, key *ecdsa.PrivateKey) error {
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	var buf strings.Builder
	if err := pem.Encode(&stringWriter{&buf}, &pem.Block{Type: "EC PRIVATE KEY", Bytes: der}); err != nil {
		return err
	}
	return atomicWrite(path, []byte(buf.String()), 0o600)
}

func atomicWrite(path string, data []byte, mode os.FileMode) error {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

type stringWriter struct{ b *strings.Builder }

func (w *stringWriter) Write(p []byte) (int, error) { return w.b.Write(p) }

// SortedDomains returns a stable, de-duplicated, lowercased copy — handy for
// comparing desired vs. issued SAN sets.
func SortedDomains(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, d := range in {
		d = strings.ToLower(strings.TrimSpace(d))
		if d != "" && !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	sort.Strings(out)
	return out
}
