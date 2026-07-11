package cli

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"trustpanel/internal/core/agent"
	"trustpanel/internal/core/agent/v2raystats"
	"trustpanel/internal/core/agentapi"
	"trustpanel/internal/core/bootstrap"
	"trustpanel/internal/core/certmgr"
)

// RunAgent runs the per-node agent (mTLS control channel + reconcile).
func RunAgent(args []string) {
	fs := flag.NewFlagSet("agent", flag.ExitOnError)
	nodeID := fs.String("node-id", "", "this node's id (must match desired-state)")
	listen := fs.String("listen", "0.0.0.0:8443", "mTLS listen address")
	caFile := fs.String("ca-file", "", "fleet CA bundle (PEM) for verifying the controller")
	certFile := fs.String("cert-file", "", "this node's certificate (PEM)")
	keyFile := fs.String("key-file", "", "this node's private key (PEM)")
	stateFile := fs.String("state-file", "/var/lib/trustpanel-agent/state.json", "agent state path")
	rootsCSV := fs.String("roots", "/etc/trusttunnel,/etc/trustpanel/singbox", "allowlisted config roots (comma-separated)")
	servicesCSV := fs.String("services", "trusttunnel.service,trustpanel-singbox.service", "allowlisted systemd units (comma-separated)")
	singboxBin := fs.String("singbox-bin", "sing-box", "sing-box binary for config checks")
	ttBin := fs.String("trusttunnel-bin", "", "trusttunnel_endpoint binary for export checks")
	maxSkew := fs.Duration("max-clock-skew", 10*time.Minute, "reject desired-state issued further in the past")
	selfHealInterval := fs.Duration("self-heal-interval", time.Minute, "how often to re-verify managed services are running (0 disables); catches anything the one-shot boot reapply missed")
	devNoMTLS := fs.Bool("dev-no-mtls", false, "serve plain HTTP without mTLS (localhost development only)")
	v2rayAPI := fs.String("v2ray-api", "127.0.0.1:8088", "sing-box v2ray stats API address for per-user traffic (empty disables)")
	// ACME defaults come from env so provisioning can configure it via agent.env
	// (TRUSTPANEL_ACME_DOMAINS / _EMAIL / _STAGING) without a unit edit.
	acmeDomains := fs.String("acme-domains", envOr("TRUSTPANEL_ACME_DOMAINS", ""), "entry TrustTunnel cert domains (comma-separated); enables node-local ACME issuance/renewal")
	acmeEmail := fs.String("acme-email", envOr("TRUSTPANEL_ACME_EMAIL", ""), "ACME account contact email")
	acmeStaging := fs.Bool("acme-staging", envOr("TRUSTPANEL_ACME_STAGING", "") == "1", "use the Let's Encrypt staging directory (untrusted certs, high rate limits)")
	acmeDirectory := fs.String("acme-directory", "", "override ACME directory URL (default: LE production, or staging with --acme-staging)")
	acmeCert := fs.String("acme-cert-path", "/etc/trusttunnel/certs/cert.pem", "fullchain cert output (TrustTunnel cert_chain_path)")
	acmeKey := fs.String("acme-key-path", "/etc/trusttunnel/certs/key.pem", "private key output (TrustTunnel private_key_path)")
	acmeAccountKey := fs.String("acme-account-key", "/etc/trusttunnel/certs/acme_account.key", "persisted ACME account key path")
	acmeChallengeAddr := fs.String("acme-challenge-addr", ":80", "HTTP-01 challenge listen address")
	acmeRenewBefore := fs.Duration("acme-renew-before", 30*24*time.Hour, "renew when the cert expires within this window")
	acmeCheckInterval := fs.Duration("acme-check-interval", 12*time.Hour, "how often to check for renewal")
	pkiDir := fs.String("pki-dir", "/etc/trustpanel/pki", "control-plane PKI dir (ca/controller cert+key) used by panel-triggered add-standby")
	standbyCertValidity := fs.Duration("standby-cert-validity", 90*24*time.Hour, "lifetime of a standby's minted controller cert")
	_ = fs.Parse(args)

	if strings.TrimSpace(*nodeID) == "" {
		log.Fatal("agent: --node-id is required")
	}
	roots := splitCSV(*rootsCSV)
	services := splitCSV(*servicesCSV)

	store, err := agent.OpenStore(*stateFile)
	if err != nil {
		log.Fatalf("agent: open state: %v", err)
	}
	sysd := agent.SystemdManager{}
	reconciler := agent.NewReconciler(agent.Config{
		NodeID:           *nodeID,
		Roots:            roots,
		ServiceAllowlist: services,
		MaxClockSkew:     *maxSkew,
	}, store, sysd, agent.ExecChecker{SingBoxBin: *singboxBin, TrustTunnelBin: *ttBin})

	if res, ok := reconciler.ReapplyCached(context.Background()); ok {
		log.Printf("boot reapply: %s (changed=%v)", res.Outcome, res.Changed)
	}

	status := &nodeStatus{services: services, sysd: sysd, versions: detectVersions(*singboxBin, *ttBin)}
	if *v2rayAPI != "" {
		if c, err := v2raystats.Dial(*v2rayAPI); err != nil {
			log.Printf("agent: v2ray stats disabled: %v", err)
		} else {
			status.stats = c
			defer c.Close()
		}
	}
	// Node-local ACME: entry nodes own their TrustTunnel certificate. The
	// agent runs HTTP-01 on :80, writes cert/key locally, and reports expiry in
	// status. Enabled only when --acme-domains is set (i.e. on entry nodes).
	var certCtl *agent.CertController
	if doms := splitCSV(*acmeDomains); len(doms) > 0 {
		dir := *acmeDirectory
		if dir == "" && *acmeStaging {
			dir = certmgr.LetsEncryptStaging
		}
		// A prior operator "promote to production" wrote a marker next to the cert;
		// honour it across restarts even if agent.env still requests staging.
		if _, err := os.Stat(filepath.Join(filepath.Dir(*acmeCert), agent.ProductionMarker)); err == nil {
			dir = "" // empty => certmgr defaults to Let's Encrypt production
		}
		certCtl = agent.NewCertController(certmgr.Config{
			Domains:        doms,
			Email:          *acmeEmail,
			DirectoryURL:   dir,
			CertPath:       *acmeCert,
			KeyPath:        *acmeKey,
			AccountKeyPath: *acmeAccountKey,
			RenewBefore:    *acmeRenewBefore,
			ChallengeAddr:  *acmeChallengeAddr,
		}, *acmeCheckInterval, func(ctx context.Context) error {
			return exec.CommandContext(ctx, "systemctl", "reload-or-restart", "trusttunnel.service").Run()
		})
		status.cert = certCtl
		log.Printf("agent: node-local ACME enabled for %v (dir=%s)", doms, dirLabel(dir))
	}

	srv := agent.NewServer(*nodeID, reconciler, store, status)
	// Panel-triggered add-standby (HA): the primary's agent does the primary-side
	// Postgres work and forwards the secret bundle to the standby's agent; the
	// standby's agent receives it. Enabled on every node; each handler fails
	// gracefully where its material is absent (the primary side needs the locally
	// held CA key, which only the control-plane node has).
	srv.EnableStandbyProvisioning(agent.StandbyConfig{
		PKIDir:   *pkiDir,
		Layout:   bootstrap.DefaultLayout(),
		Validity: *standbyCertValidity,
	})

	httpServer := &http.Server{Addr: *listen, Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}
	if !*devNoMTLS {
		caPEM, err := os.ReadFile(*caFile)
		if err != nil {
			log.Fatalf("agent: read ca-file: %v", err)
		}
		cert, err := tls.LoadX509KeyPair(*certFile, *keyFile)
		if err != nil {
			log.Fatalf("agent: load node cert: %v", err)
		}
		tlsCfg, err := agent.BuildAgentTLSConfig(caPEM, cert)
		if err != nil {
			log.Fatalf("agent: build tls: %v", err)
		}
		httpServer.TLSConfig = tlsCfg
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if certCtl != nil {
		go certCtl.Run(ctx)
	}
	if *selfHealInterval > 0 {
		go runSelfHeal(ctx, reconciler, *selfHealInterval)
	}
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutCtx)
	}()

	log.Printf("agent for node %q listening on %s (mtls=%v)", *nodeID, *listen, !*devNoMTLS)
	if *devNoMTLS {
		err = httpServer.ListenAndServe()
	} else {
		err = httpServer.ListenAndServeTLS("", "")
	}
	if err != nil && err != http.ErrServerClosed {
		log.Fatalf("agent: serve: %v", err)
	}
}

// runSelfHeal periodically re-verifies that every managed service the cached
// desired-state wants running is actually active, restarting whichever
// aren't. The one-shot ReapplyCached at boot only gets one chance to notice
// a dead service; a transient failure there (or a service crashing later)
// would otherwise sit dead until the panel next pushes an unrelated config
// change. This closes that gap without waiting for the controller.
func runSelfHeal(ctx context.Context, reconciler *agent.Reconciler, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			res, ok := reconciler.ReapplyCached(ctx)
			if ok && res.Changed {
				log.Printf("self-heal: %s (changed=%v, restarted=%v)", res.Outcome, res.Changed, res.Restarted)
			}
		}
	}
}

func envOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

type nodeStatus struct {
	services []string
	sysd     agent.SystemdManager
	versions map[string]string
	stats    v2raystats.Querier    // nil on non-entry nodes or when stats are disabled
	cert     *agent.CertController // nil unless node-local ACME is enabled
}

// CertStatus implements agent.CertStatusSource (entry nodes only).
func (n *nodeStatus) CertStatus() *agentapi.TLSCertStatus {
	if n.cert == nil {
		return nil
	}
	return n.cert.Status()
}

// PromoteToProduction implements agent.CertPromoter (entry nodes with ACME only).
func (n *nodeStatus) PromoteToProduction(ctx context.Context) error {
	if n.cert == nil {
		return fmt.Errorf("node has no ACME-managed certificate")
	}
	return n.cert.Promote(ctx)
}

// SystemMetrics implements agent.SystemSource (every node).
func (n *nodeStatus) SystemMetrics() *agentapi.SystemMetrics {
	return agent.CollectSystemMetrics()
}

func dirLabel(dir string) string {
	if dir == certmgr.LetsEncryptStaging {
		return "staging"
	}
	if dir == "" || dir == certmgr.LetsEncryptProduction {
		return "production"
	}
	return dir
}

func (n *nodeStatus) Services(ctx context.Context) []agentapi.ServiceStatus {
	return n.sysd.ServicesStatus(ctx, n.services)
}
func (n *nodeStatus) InstalledVersions() map[string]string { return n.versions }

// UserTraffic reports per-user byte counters from the local sing-box v2ray
// stats API. It degrades gracefully: any error (API down, not an entry node)
// yields no entries rather than failing the whole status response.
func (n *nodeStatus) UserTraffic(ctx context.Context) []agentapi.UserTrafficStat {
	if n.stats == nil {
		return nil
	}
	users, err := n.stats.QueryUsers(ctx)
	if err != nil {
		return nil
	}
	out := make([]agentapi.UserTrafficStat, 0, len(users))
	for _, u := range users {
		out = append(out, agentapi.UserTrafficStat{
			Username:      u.Username,
			UplinkBytes:   u.Uplink,
			DownlinkBytes: u.Downlink,
		})
	}
	return out
}

func detectVersions(singboxBin, ttBin string) map[string]string {
	v := map[string]string{}
	if singboxBin != "" {
		if out, err := exec.Command(singboxBin, "version").Output(); err == nil {
			v["sing-box"] = firstLine(out)
		}
	}
	if ttBin != "" {
		if out, err := exec.Command(ttBin, "--version").Output(); err == nil {
			v["trusttunnel"] = firstLine(out)
		}
	}
	return v
}

func firstLine(b []byte) string {
	s := string(b)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}
