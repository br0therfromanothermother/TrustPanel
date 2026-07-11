package agent

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"trustpanel/internal/core/agentapi"
	"trustpanel/internal/core/certmgr"
)

// ProductionMarker is the node-local file that records an operator's "promote to
// production" choice so it survives an agent restart even though agent.env may
// still say staging. The agent honours it at startup.
const ProductionMarker = ".acme_production"

// CertController owns the entry node's TrustTunnel ACME certificate. The
// private key never leaves the node: this runs the
// HTTP-01 flow locally, writes cert_chain_path/private_key_path that TrustTunnel
// reads, and exposes the leaf's expiry for GET /v1/status. It degrades
// gracefully — a failed renewal keeps serving the existing cert and is reported
// as LastError rather than crashing the agent.
type CertController struct {
	cfg      certmgr.Config
	interval time.Duration
	reload   func(ctx context.Context) // optional: reload TrustTunnel after a new cert
	marker   string                    // production-marker path (sibling of the cert)

	mu        sync.Mutex
	notAfter  time.Time
	issuer    string
	lastError string
}

// NewCertController builds a controller for the given config. interval is how
// often the renewal check runs (issuance happens only when NeedsRenewal).
func NewCertController(cfg certmgr.Config, interval time.Duration, reload func(ctx context.Context) error) *CertController {
	if interval <= 0 {
		interval = 12 * time.Hour
	}
	c := &CertController{cfg: cfg, interval: interval, marker: filepath.Join(filepath.Dir(cfg.CertPath), ProductionMarker)}
	if reload != nil {
		c.reload = func(ctx context.Context) {
			if err := reload(ctx); err != nil {
				log.Printf("certmgr: post-renew reload failed: %v", err)
			}
		}
	}
	// Seed expiry/issuer from any cert already on disk so status is populated
	// before the first renewal tick.
	if na, err := certmgr.CertExpiry(cfg.CertPath); err == nil {
		c.notAfter = na
	}
	if iss, err := certmgr.CertIssuer(cfg.CertPath); err == nil {
		c.issuer = iss
	}
	return c
}

// Run checks the certificate immediately, then on every interval until ctx is
// done. Safe to launch in a goroutine.
func (c *CertController) Run(ctx context.Context) {
	c.checkOnce(ctx)
	t := time.NewTicker(c.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			c.checkOnce(ctx)
		}
	}
}

func (c *CertController) checkOnce(ctx context.Context) {
	issued, notAfter, err := certmgr.Ensure(ctx, c.cfg)
	c.mu.Lock()
	if err != nil {
		c.lastError = err.Error()
	} else {
		c.lastError = ""
		if !notAfter.IsZero() {
			c.notAfter = notAfter
		}
		// Refresh the observed issuer from the leaf now on disk so the panel can
		// tell a staging cert from a production one.
		if iss, ierr := certmgr.CertIssuer(c.cfg.CertPath); ierr == nil {
			c.issuer = iss
		}
	}
	c.mu.Unlock()
	if err != nil {
		log.Printf("certmgr: ensure %v failed: %v", c.cfg.Domains, err)
		return
	}
	if issued {
		log.Printf("certmgr: issued/renewed cert for %v (expires %s)", c.cfg.Domains, notAfter.Format(time.RFC3339))
		if c.reload != nil {
			c.reload(ctx)
		}
	}
}

// Promote switches issuance to Let's Encrypt production and reissues immediately.
// A staging ACME account can't be used against production, and the staging leaf
// (valid by dates+SANs) would otherwise block renewal — so the account key and
// cert are removed to force a clean prod enrollment. A marker file records the
// choice so it survives an agent restart. Returns the issuance error, if any.
func (c *CertController) Promote(ctx context.Context) error {
	c.mu.Lock()
	c.cfg.DirectoryURL = certmgr.LetsEncryptProduction
	_ = os.Remove(c.cfg.AccountKeyPath)
	_ = os.Remove(c.cfg.CertPath)
	_ = os.Remove(c.cfg.KeyPath)
	c.notAfter = time.Time{}
	c.issuer = ""
	marker := c.marker
	c.mu.Unlock()

	if marker != "" {
		if err := os.WriteFile(marker, []byte("production\n"), 0o644); err != nil {
			return fmt.Errorf("write production marker: %w", err)
		}
	}
	c.checkOnce(ctx) // cert removed => NeedsRenewal true => reissue from production
	c.mu.Lock()
	le := c.lastError
	c.mu.Unlock()
	if le != "" {
		return fmt.Errorf("reissue from production: %s", le)
	}
	return nil
}

// Status implements the CertStatusSource capability consulted by the agent
// server when building GET /v1/status.
func (c *CertController) Status() *agentapi.TLSCertStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	st := &agentapi.TLSCertStatus{Domains: c.cfg.Domains, Issuer: c.issuer, LastError: c.lastError}
	if !c.notAfter.IsZero() {
		na := c.notAfter
		st.NotAfter = &na
	}
	return st
}
