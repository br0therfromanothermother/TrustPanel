// Package cli implements the subcommands of the single trustpanel binary:
// serve (panel), agent, and ca. The fallback subcommand is delegated to
// internal/fallback.
package cli

import (
	"context"
	"crypto/tls"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"trustpanel/internal/core/controller"
	"trustpanel/internal/core/model"
	"trustpanel/internal/core/panel"
	"trustpanel/internal/core/pki"
	"trustpanel/internal/core/provision"
	"trustpanel/internal/core/render"
	"trustpanel/internal/core/rulesets"
	"trustpanel/internal/core/store"
	"trustpanel/internal/core/watchdog"
)

// RunServe runs the operator control plane (panel) on localhost.
func RunServe(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	dsn := fs.String("dsn", "", "Postgres DSN (or set TRUSTPANEL_DSN)")
	listen := fs.String("listen", "127.0.0.1:8787", "panel listen address (localhost only)")
	migrationsDir := fs.String("migrations-dir", "", "if set, run *.sql migrations from this dir on start")
	sessionTTL := fs.Duration("session-ttl", 12*time.Hour, "browser session lifetime")
	ttDir := fs.String("trusttunnel-dir", "/etc/trusttunnel", "entry TrustTunnel config dir on nodes")
	sbDir := fs.String("singbox-dir", "/etc/trustpanel/singbox", "sing-box config dir on nodes")
	caFile := fs.String("ca-file", "", "fleet CA bundle (PEM) for pushing to agents")
	certFile := fs.String("cert-file", "", "controller certificate (PEM)")
	keyFile := fs.String("key-file", "", "controller private key (PEM)")
	devNoMTLS := fs.Bool("dev-no-mtls", false, "push to agents over plain HTTP (localhost dev only; requires --insecure-bind)")
	insecureBind := fs.Bool("insecure-bind", false, "acknowledge running without the localhost+mTLS safety net (non-loopback --listen or --dev-no-mtls). DANGER: the panel has no TLS and authenticates by cookie")
	reconcileInterval := fs.Duration("reconcile-interval", 60*time.Second, "background fleet reconcile interval (0 disables)")
	autoSyncDebounce := fs.Duration("auto-sync-debounce", 2*time.Second, "coalesce window for auto-reconcile after a write (0 disables auto-sync)")
	statsInterval := fs.Duration("stats-interval", 60*time.Second, "background per-user traffic poll interval (0 disables)")
	caKeyFile := fs.String("ca-key-file", "", "fleet CA private key (PEM) — enables remote provisioning of nodes")
	provSingbox := fs.String("provision-singbox-bin", "", "local sing-box binary uploaded when provisioning")
	provTrusttunnel := fs.String("provision-trusttunnel-bin", "", "local trusttunnel_endpoint binary (entry nodes)")
	provTrustpanel := fs.String("provision-trustpanel-bin", "", "local trustpanel binary to upload (default: this binary)")
	provUnitsDir := fs.String("provision-units-dir", "deploy/systemd", "dir with systemd unit files to install on nodes")
	provKnownHosts := fs.String("provision-known-hosts", "", "known_hosts file for SSH host-key pinning")
	// Deployment branding + ACME, stamped onto provisioned entry nodes (defaults
	// from the bootstrap-written /etc/trustpanel/deployment.env).
	brand := fs.String("brand", envOr("TRUSTPANEL_BRAND", "ExampleCDN"), "fallback site brand applied to provisioned entries")
	domain := fs.String("domain", envOr("TRUSTPANEL_DOMAIN", ""), "deployment apex domain applied to provisioned entries")
	connectSub := fs.String("connect-subdomain", envOr("TRUSTPANEL_CONNECT_SUBDOMAIN", panel.DefaultConnectSubdomain), "login subdomain the VPN endpoint lives on for apex-domain entries")
	acmeEmail := fs.String("acme-email", envOr("TRUSTPANEL_ACME_EMAIL", ""), "ACME contact for provisioned entry certs (default admin@<domain>)")
	acmeStaging := fs.Bool("acme-staging", envOr("TRUSTPANEL_ACME_STAGING", "") == "1", "issue provisioned entry certs from Let's Encrypt staging")
	alertTGToken := fs.String("alert-telegram-token", envOr("TRUSTPANEL_ALERT_TG_TOKEN", ""), "Telegram bot token for one-way alerts (billing due, etc.)")
	alertTGChat := fs.String("alert-telegram-chat", envOr("TRUSTPANEL_ALERT_TG_CHAT", ""), "Telegram chat id for alerts")
	alertWebhook := fs.String("alert-webhook", envOr("TRUSTPANEL_ALERT_WEBHOOK", ""), "webhook URL for alerts (POST JSON {text})")
	billingAlertDays := fs.Int("billing-alert-days", 7, "alert when a node's VPS payment is due within this many days")
	billingAlertInterval := fs.Duration("billing-alert-interval", 24*time.Hour, "how often to check billing due dates (0 disables)")
	expiryAlertDays := fs.Int("expiry-alert-days", 3, "alert the owner when a client config expires within this many days")
	expiryAlertInterval := fs.Duration("expiry-alert-interval", 12*time.Hour, "how often to check client config expiry (0 disables)")
	edgeProbeInterval := fs.Duration("edge-probe-interval", 30*time.Second, "how often to probe each entry node's public :443 (0 disables)")
	botHealthInterval := fs.Duration("bot-health-interval", 5*time.Minute, "how often to check Telegram bot/alert token reachability (0 disables)")
	replProbeInterval := fs.Duration("repl-probe-interval", 30*time.Second, "how often to check standby replication-slot health on the primary (0 disables)")
	rulesetsCacheDir := fs.String("rulesets-cache-dir", "/var/lib/trustpanel/rulesets", "cache dir for downloaded geoip/geosite .srs rule-sets")
	rulesetsDisabled := fs.Bool("rulesets-disabled", false, "do not fetch/distribute geoip/geosite rule-sets (geo routing policies will fail)")
	_ = fs.Parse(args)

	dbDSN := connDSN(*dsn)
	if dbDSN == "" {
		log.Fatal("serve: --dsn, TRUSTPANEL_DSN, or /etc/trustpanel/serve.env is required")
	}
	// The panel has no TLS of its own and authenticates by cookie — it is meant to
	// be reached over an SSH tunnel on loopback. Refuse a non-loopback bind (or
	// plaintext mTLS-off pushes) unless the operator explicitly accepts the risk,
	// so a stray --listen 0.0.0.0 cannot silently expose cookie-auth to the network.
	loopback := strings.HasPrefix(*listen, "127.0.0.1:") || strings.HasPrefix(*listen, "localhost:") || strings.HasPrefix(*listen, "[::1]:")
	if !loopback && !*insecureBind {
		log.Fatalf("serve: refusing to listen on non-loopback %s — the panel has no TLS and authenticates by cookie; bind 127.0.0.1 and reach it over an SSH tunnel, or pass --insecure-bind if you front it with your own TLS terminator and access control", *listen)
	}
	if !loopback {
		log.Printf("DANGER: panel listening on non-loopback %s with --insecure-bind — it serves plaintext cookie auth; ensure an external TLS terminator and network access control", *listen)
	}
	if *devNoMTLS && !*insecureBind {
		log.Fatal("serve: --dev-no-mtls pushes desired-state and secrets to agents in plaintext; refused unless --insecure-bind is also set (localhost dev only)")
	}

	ctx := context.Background()
	st, err := store.Open(ctx, dbDSN)
	if err != nil {
		log.Fatalf("serve: open store: %v", err)
	}
	defer st.Close()

	if *migrationsDir != "" {
		if err := runMigrations(ctx, st, *migrationsDir); err != nil {
			log.Fatalf("serve: migrate: %v", err)
		}
		log.Printf("migrations applied from %s", *migrationsDir)
	}
	// The catch-all "default" group must always exist (policies/users default to it).
	if err := st.EnsureDefaultGroup(ctx); err != nil {
		log.Fatalf("serve: ensure default group: %v", err)
	}
	// A fresh panel ships with a starter rule set (RU split-routing guard + youtube).
	if err := st.SeedDefaultPolicies(ctx); err != nil {
		log.Fatalf("serve: seed default policies: %v", err)
	}

	layout := controller.Layout{TrustTunnelDir: *ttDir, SingBoxDir: *sbDir}
	var fleetTLS *tls.Config
	if !*devNoMTLS {
		caPEM, err := os.ReadFile(*caFile)
		if err != nil {
			log.Fatalf("serve: read ca-file: %v", err)
		}
		cert, err := tls.LoadX509KeyPair(*certFile, *keyFile)
		if err != nil {
			log.Fatalf("serve: load controller cert: %v", err)
		}
		fleetTLS, err = controller.BuildControllerTLSConfig(caPEM, cert)
		if err != nil {
			log.Fatalf("serve: build controller tls: %v", err)
		}
	}
	// Absolute rule-set dir so sing-box resolves .srs regardless of its working
	// directory; the fleet writes the .srs to the same absolute path.
	fleet := controller.NewFleet(controller.NewClient(fleetTLS), layout, render.Options{RuleSetDir: layout.SingBoxDir + "/rulesets"})
	var ruleSetProv *rulesets.Provider
	if !*rulesetsDisabled {
		ruleSetProv = rulesets.New(*rulesetsCacheDir)
		fleet.RuleSets = ruleSetProv
	}
	sessions := panel.NewSessionManager(*sessionTTL)
	// Honor a Settings-tab-configured session lifetime at startup; the flag is the
	// fallback when it is unset. Later edits apply live via handleSaveSettings.
	if s, e := st.GetSettings(ctx); e == nil && s.Panel.SessionHours > 0 {
		sessions.SetTTL(s.Panel.SessionTTL())
	}
	p := panel.New(st, fleet, sessions)
	// Route tester evaluates geoip/geosite with the real sing-box binary against
	// the cached .srs (fetching any missing category on demand), so its verdicts
	// match the nodes'.
	var rsFetch panel.RuleSetFetcher
	if ruleSetProv != nil {
		rsFetch = ruleSetProv
	}
	p.SetRouteTester(panel.NewSingboxGeo(*provSingbox, *rulesetsCacheDir, rsFetch))

	// Audience-agnostic alert legs: console log + event log always, plus any
	// explicit flag/env Telegram/webhook channel. The panel-managed alert bot
	// (Bots tab) is NOT added here — its Telegram delivery is routed by audience
	// (broadcast / owner / admin) inside the monitors and the billing/expiry loops.
	alerters := panel.MultiAlerter{watchdog.LogAlerter{}, p.NewEventAlerter()}
	if *alertTGToken != "" && *alertTGChat != "" {
		alerters = append(alerters, watchdog.TelegramAlerter{Token: *alertTGToken, ChatID: *alertTGChat})
	}
	if *alertWebhook != "" {
		alerters = append(alerters, watchdog.WebhookAlerter{URL: *alertWebhook})
	}
	p.SetBillingAlerts(alerters, *billingAlertDays)
	// Expiry names a client (a hidden resource), so it records its OWN namespaced
	// event + owner-only Telegram; its recorder is console-log only (no shared
	// event-log/webhook leg that would surface the client name in the admin namespace).
	p.SetExpiryAlerts(panel.MultiAlerter{watchdog.LogAlerter{}}, *expiryAlertDays)
	// Config-health names only a client COUNT (no identities), so console-log is
	// enough for the recorder; the Telegram leg is routed to the namespace owner.
	p.SetConfigHealthAlerts(panel.MultiAlerter{watchdog.LogAlerter{}})
	// Background-monitor alerts (node down/recovered, entry edge, replication, bot
	// health) — α, the primary-side sender; each monitor adds its routed Telegram leg.
	p.SetMonitorAlerts(alerters)

	// Remote node provisioning is enabled when the CA key + sing-box binary are
	// available (the panel CA signs node CSRs during enrollment).
	if *caFile != "" && *caKeyFile != "" && *provSingbox != "" {
		cfg, err := buildProvisionConfig(*caFile, *caKeyFile, layout, *provTrustpanel, *provSingbox, *provTrusttunnel, *provUnitsDir, *provKnownHosts)
		if err != nil {
			log.Printf("serve: remote provisioning disabled: %v", err)
		} else {
			cfg.Brand, cfg.Domain, cfg.ACMEEmail, cfg.ACMEStaging = *brand, *domain, *acmeEmail, *acmeStaging
			cfg.ConnectSubdomain = *connectSub
			p.EnableProvisioning(cfg)
			log.Printf("remote node provisioning enabled (units from %s; brand=%q domain=%q)", *provUnitsDir, *brand, *domain)
		}
	}

	server := &http.Server{Addr: *listen, Handler: p.Handler(), ReadHeaderTimeout: 10 * time.Second}
	sctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go p.RunReconcileLoop(sctx, *reconcileInterval)
	go p.RunAutoSyncLoop(sctx, *autoSyncDebounce)
	go p.RunStatsLoop(sctx, *statsInterval)
	go p.RunBillingAlertLoop(sctx, *billingAlertInterval)
	go p.RunExpiryAlertLoop(sctx, *expiryAlertInterval)
	go p.RunConfigHealthLoop(sctx, *expiryAlertInterval)
	go p.RunEdgeProbeLoop(sctx, *edgeProbeInterval)
	go p.RunBotHealthLoop(sctx, *botHealthInterval)
	go p.RunReplicationProbeLoop(sctx, *replProbeInterval)
	go func() {
		<-sctx.Done()
		shut, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shut)
	}()

	log.Printf("panel listening on %s (push mtls=%v)", *listen, !*devNoMTLS)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("serve: %v", err)
	}
}

func buildProvisionConfig(caCertFile, caKeyFile string, layout controller.Layout, tpBin, sbBin, ttBin, unitsDir, knownHosts string) (*panel.ProvisionConfig, error) {
	caCert, err := os.ReadFile(caCertFile)
	if err != nil {
		return nil, err
	}
	caKey, err := os.ReadFile(caKeyFile)
	if err != nil {
		return nil, err
	}
	ca, err := pki.LoadCA(caCert, caKey)
	if err != nil {
		return nil, err
	}
	sb, err := os.ReadFile(sbBin)
	if err != nil {
		return nil, err
	}
	tp := []byte(nil)
	if tpBin != "" {
		if tp, err = os.ReadFile(tpBin); err != nil {
			return nil, err
		}
	} else if exe, e := os.Executable(); e == nil {
		tp, _ = os.ReadFile(exe)
	}
	var tt []byte
	if ttBin != "" {
		if tt, err = os.ReadFile(ttBin); err != nil {
			return nil, err
		}
	}
	if knownHosts == "" {
		knownHosts = filepath.Join(os.TempDir(), "trustpanel_known_hosts")
	}
	// Validate every unit template is readable and non-empty up front (the entry
	// role is the superset of all four units): an empty unit pushes a zero-byte
	// (systemd-"masked") file that fails to enable mid-provision. Surfacing it
	// here disables provisioning with a clear reason instead of failing every
	// install. provisionUnits is the single source of truth for the unit set and
	// enable flags, shared with the `trustpanel provision` CLI.
	if _, err := provisionUnits(unitsDir, model.RoleEntry); err != nil {
		return nil, err
	}
	return &panel.ProvisionConfig{
		CA: ca, Layout: layout, CertValidity: 90 * 24 * time.Hour,
		TrustPanelBin: tp, SingBoxBin: sb, TrustTunnelBin: tt,
		Units: func(role model.PublicRole) []provision.UnitFile {
			u, _ := provisionUnits(unitsDir, role) // pre-validated above
			return u
		},
		NewRunner: func(ctx context.Context, ssh provision.SSHParams) (provision.Runner, func(), error) {
			return provision.NewSSHRunner(ctx, ssh, knownHosts)
		},
	}, nil
}

func runMigrations(ctx context.Context, st *store.Store, dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)
	for _, f := range files {
		b, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil {
			return err
		}
		applied, err := st.MigrateOnce(ctx, f, string(b))
		if err != nil {
			return err
		}
		if applied {
			log.Printf("migration applied: %s", f)
		}
	}
	return nil
}
