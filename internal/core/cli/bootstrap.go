package cli

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"trustpanel/internal/core/bootstrap"
	"trustpanel/internal/core/idgen"
	"trustpanel/internal/core/panel"
	"trustpanel/internal/core/provision"
	"trustpanel/internal/core/store"
)

// RunBootstrap turns a bare server into the TrustPanel control-plane (exit) node
// in one command: Postgres, fleet CA, system user/dirs, binaries, systemd units,
// the panel admin, and the exit node registered with its Reality keys. Run as
// root on the target server after copying the trustpanel/sing-box/trusttunnel
// binaries onto it.
func RunBootstrap(args []string) {
	fs := flag.NewFlagSet("bootstrap", flag.ExitOnError)
	domain := fs.String("domain", "", "deployment apex domain for the camouflage brand (e.g. example.com)")
	brand := fs.String("brand", "ExampleCDN", "portal/brand name shown on entry fallback sites")
	publicIP := fs.String("public-ip", "", "this exit node's public IP (comma-separated if several) — required")
	realitySNI := fs.String("reality-sni", "", "borrowed third-party SNI for the Reality inbound (TLS1.3 + X25519 + h2) — required")
	nodeID := fs.String("node-id", "", "this node's internal id (auto-generated if omitted — normally no reason to set it)")
	nodeName := fs.String("node-name", "Exit 1", "exit node display name")
	adminUser := fs.String("admin-user", "admin", "panel admin username")
	adminPass := fs.String("admin-password", "", "panel admin password (generated if empty)")
	vpnUser := fs.String("vpn-user", "admin", "first VPN user to create (empty to skip; seeds credentials.toml so the entry comes up)")
	vpnPass := fs.String("vpn-password", "", "first VPN user's password (generated if empty)")
	dbName := fs.String("db-name", "trustpanel", "Postgres database name")
	dbUser := fs.String("db-user", "trustpanel", "Postgres role name")
	dbPass := fs.String("db-password", "", "Postgres role password (generated if empty)")
	singboxBin := fs.String("singbox-bin", "", "path to the sing-box binary to install (required)")
	ttBin := fs.String("trusttunnel-bin", "", "path to trusttunnel_endpoint to stage for provisioning entries")
	tpBin := fs.String("trustpanel-bin", "", "path to the trustpanel binary to install (default: this executable)")
	single := fs.Bool("single", false, "one-box deployment: control plane + a client-facing entry with local egress (no separate exit)")
	connectSub := fs.String("connect-subdomain", bootstrap.DefaultConnectSubdomain, "login subdomain the VPN endpoint lives on (single mode); the apex stays a landing legend")
	acmeEmail := fs.String("acme-email", "", "ACME contact for the entry TLS cert (single mode; default admin@<domain>)")
	acmeStaging := fs.Bool("acme-staging", false, "use the Let's Encrypt staging directory for the entry cert (single mode)")
	noReconcile := fs.Bool("no-reconcile", false, "do not trigger the first reconcile after start")
	harden := fs.Bool("harden", false, "lock the box down as the final step (sudo user + key, fail2ban, sshd drop-in, ufw)")
	hardenUser := fs.String("harden-sudo-user", "user", "hardening: sudo user to create (passwordless sudo)")
	hardenKeyFile := fs.String("harden-ssh-pubkey-file", "", "hardening: file with the SSH public key to install for the sudo user")
	hardenSSHPort := fs.Int("harden-ssh-port", 0, "hardening: new sshd port (0 = keep 22)")
	hardenNoRoot := fs.Bool("harden-disable-root", false, "hardening: set PermitRootLogin no (needs the sudo user reachable first)")
	hardenNoPass := fs.Bool("harden-disable-password", false, "hardening: disable SSH password auth (gated on a verified installed key)")
	hardenFirewall := fs.Bool("harden-firewall", true, "hardening: enable ufw (opens ssh + 443 + 80 + agent)")
	hardenFail2ban := fs.Bool("harden-fail2ban", true, "hardening: install + enable fail2ban")
	_ = fs.Parse(args)

	if strings.TrimSpace(*domain) == "" || strings.TrimSpace(*publicIP) == "" || strings.TrimSpace(*singboxBin) == "" {
		log.Fatal("bootstrap: --domain, --public-ip and --singbox-bin are required")
	}
	if *single {
		if strings.TrimSpace(*ttBin) == "" {
			log.Fatal("bootstrap: --single requires --trusttunnel-bin (the client-facing endpoint)")
		}
	} else if strings.TrimSpace(*realitySNI) == "" {
		log.Fatal("bootstrap: --reality-sni is required for an exit node")
	}
	if strings.TrimSpace(*nodeID) == "" {
		fb := "exit"
		if *single {
			fb = "entry"
		}
		*nodeID = idgen.New(*nodeName, fb)
	}
	tpPath := *tpBin
	if tpPath == "" {
		if exe, err := os.Executable(); err == nil {
			tpPath = exe
		} else {
			log.Fatalf("bootstrap: cannot find own binary: %v", err)
		}
	}
	adminPassword := orRandom(*adminPass, 12)
	dbPassword := orRandom(*dbPass, 16)
	vpnPassword := ""
	if strings.TrimSpace(*vpnUser) != "" {
		vpnPassword = orRandom(*vpnPass, 16)
	}

	var hardening *provision.Hardening
	if *harden {
		pubkey := ""
		if strings.TrimSpace(*hardenKeyFile) != "" {
			b, err := os.ReadFile(*hardenKeyFile)
			if err != nil {
				log.Fatalf("bootstrap: read --harden-ssh-pubkey-file: %v", err)
			}
			pubkey = strings.TrimSpace(string(b))
		}
		if *hardenNoPass && pubkey == "" {
			log.Fatal("bootstrap: --harden-disable-password needs --harden-ssh-pubkey-file (avoiding lock-out)")
		}
		hardening = &provision.Hardening{
			Enabled:             true,
			SudoUser:            strings.TrimSpace(*hardenUser),
			SSHPubKey:           pubkey,
			SSHPort:             *hardenSSHPort,
			DisableRootLogin:    *hardenNoRoot,
			DisablePasswordAuth: *hardenNoPass,
			Fail2ban:            *hardenFail2ban,
			Firewall:            *hardenFirewall,
		}
	}

	opts := bootstrap.Options{
		Layout:           bootstrap.DefaultLayout(),
		Brand:            *brand,
		Domain:           *domain,
		NodeID:           *nodeID,
		NodeName:         *nodeName,
		PublicIPs:        splitCSV(*publicIP),
		RealitySNI:       *realitySNI,
		DBName:           *dbName,
		DBUser:           *dbUser,
		DBPassword:       dbPassword,
		AdminUser:        *adminUser,
		AdminPassword:    adminPassword,
		VPNUser:          strings.TrimSpace(*vpnUser),
		VPNPassword:      vpnPassword,
		TrustPanelBin:    tpPath,
		SingBoxBin:       *singboxBin,
		TrustTunnelBin:   *ttBin,
		CertValidity:     90 * 24 * time.Hour,
		Single:           *single,
		ConnectSubdomain: *connectSub,
		ACMEEmail:        *acmeEmail,
		ACMEStaging:      *acmeStaging,
		Hardening:        hardening,
		HashPassword:     panel.HashPassword,
		OpenStore: func(ctx context.Context, dsn string) (bootstrap.Store, error) {
			return store.Open(ctx, dsn)
		},
	}

	ctx := context.Background()
	res, err := bootstrap.Run(ctx, localRunner{}, opts, func(s string) { log.Println("bootstrap:", s) })
	if err != nil {
		log.Fatalf("bootstrap failed: %v", err)
	}

	if !*noReconcile {
		if err := firstReconcile(opts.AdminUser, adminPassword); err != nil {
			log.Printf("bootstrap: first reconcile deferred to the background loop (%v)", err)
		} else {
			log.Println("bootstrap: first reconcile pushed to the node agent")
		}
	}

	fmt.Print(summary(opts, res, adminPassword, dbPassword))
}

// firstReconcile logs into the freshly-started panel on localhost and triggers a
// reconcile so the exit's sing-box comes up immediately instead of after the
// background interval. Best-effort: waits briefly for the panel to be healthy.
func firstReconcile(adminUser, adminPass string) error {
	base := "http://127.0.0.1:8787"
	deadline := time.Now().Add(30 * time.Second)
	for {
		if resp, err := http.Get(base + "/api/health"); err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				break
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("panel not healthy in time")
		}
		time.Sleep(time.Second)
	}
	jar := &cookieJar{}
	body, _ := json.Marshal(map[string]string{"username": adminUser, "password": adminPass})
	req, _ := http.NewRequest(http.MethodPost, base+"/api/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := jar.do(req)
	if err != nil {
		return err
	}
	if resp.StatusCode != 200 {
		resp.Body.Close()
		return fmt.Errorf("login HTTP %d", resp.StatusCode)
	}
	// The panel enforces a per-session CSRF token on every state-changing POST
	// (panel.protected); login returns it in the body. Without echoing it back in
	// the X-CSRF-Token header the reconcile POST 403s and the first reconcile is
	// silently deferred to the background loop.
	var login struct {
		CSRFToken string `json:"csrf_token"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&login)
	resp.Body.Close()
	rc, _ := http.NewRequest(http.MethodPost, base+"/api/reconcile", nil)
	rc.Header.Set("X-CSRF-Token", login.CSRFToken)
	resp, err = jar.do(rc)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("reconcile HTTP %d", resp.StatusCode)
	}
	return nil
}

// cookieJar is a tiny session-cookie carrier for the two localhost calls.
type cookieJar struct{ cookies []*http.Cookie }

func (j *cookieJar) do(req *http.Request) (*http.Response, error) {
	for _, c := range j.cookies {
		req.AddCookie(c)
	}
	resp, err := http.DefaultClient.Do(req)
	if err == nil {
		j.cookies = append(j.cookies, resp.Cookies()...)
	}
	return resp, err
}

func summary(opts bootstrap.Options, res bootstrap.Result, adminPass, dbPass string) string {
	var b strings.Builder
	fmt.Fprintln(&b, "\n========================================================================")
	fmt.Fprintln(&b, " TrustPanel control plane is up.")
	fmt.Fprintln(&b, "========================================================================")
	fmt.Fprintf(&b, " Panel:        %s (localhost only — reach it via an SSH tunnel:\n", res.PanelListen)
	fmt.Fprintf(&b, "                 ssh -L 8787:127.0.0.1:8787 <user>@%s )\n", firstIP(opts.PublicIPs))
	fmt.Fprintf(&b, " Admin login:  %s / %s  (panel operator)\n", opts.AdminUser, adminPass)
	if opts.VPNUser != "" {
		fmt.Fprintf(&b, " VPN user:     %s / %s  (first client; export its config/QR in the panel)\n", opts.VPNUser, opts.VPNPassword)
	}
	fmt.Fprintf(&b, " DB password:  %s  (role %q, db %q)\n", dbPass, opts.DBUser, opts.DBName)
	if opts.Single {
		ip := firstIP(opts.PublicIPs)
		connect := connectHostOf(opts)
		fmt.Fprintf(&b, " Mode:         single (entry + local egress) · node %s (%s)\n", opts.NodeName, opts.NodeID)
		fmt.Fprintf(&b, " Connect host: %s  (clients connect here on :443)\n", connect)
		fmt.Fprintf(&b, " Landing:      %s  (apex — camouflage legend only)\n", opts.Domain)
		fmt.Fprintf(&b, " Brand/domain: %s / %s\n", opts.Brand, opts.Domain)
		fmt.Fprintln(&b, "------------------------------------------------------------------------")
		fmt.Fprintf(&b, " Next: point DNS  %s  AND  %s  ->  %s  (two A/AAAA records).\n", connect, opts.Domain, ip)
		fmt.Fprintln(&b, " One TLS cert covers both; the client endpoint comes up automatically")
		fmt.Fprintln(&b, " once DNS resolves and ports 80/443 are reachable. Then add a user in the")
		fmt.Fprintln(&b, " panel and hand out its client config / QR. Grow later: Nodes → Add entry.")
	} else {
		fmt.Fprintf(&b, " Exit node:    %s (%s) · Reality SNI %s · pubkey %s\n", opts.NodeName, opts.NodeID, opts.RealitySNI, res.RealityPub)
		fmt.Fprintf(&b, " Brand/domain: %s / %s\n", opts.Brand, opts.Domain)
		fmt.Fprintln(&b, "------------------------------------------------------------------------")
		fmt.Fprintln(&b, " Next: provision an entry node from the panel (Nodes → Install on server),")
		fmt.Fprintln(&b, " after pointing its domain's DNS at the entry's public IP.")
	}
	if h := opts.Hardening; h != nil && h.Enabled {
		fmt.Fprintln(&b, "------------------------------------------------------------------------")
		port := 22
		if h.SSHPort != 0 {
			port = h.SSHPort
		}
		fmt.Fprintf(&b, " Hardened:     SSH now %s@%s:%d", h.SudoUser, firstIP(opts.PublicIPs), port)
		if h.DisablePasswordAuth {
			fmt.Fprint(&b, " (key-only)")
		}
		if h.DisableRootLogin {
			fmt.Fprint(&b, ", root login disabled")
		}
		fmt.Fprintln(&b)
		fmt.Fprintln(&b, "               ufw open: ssh + 443 + 80 + 8443 (agent mTLS). VERIFY the new")
		fmt.Fprintln(&b, "               SSH login works in a SECOND session before closing this one.")
	}
	fmt.Fprintln(&b, " SAVE THE CREDENTIALS ABOVE — they are not stored anywhere else.")
	fmt.Fprintln(&b, "========================================================================")
	return b.String()
}

// connectHostOf returns the single-mode client endpoint host (<sub>.<domain>).
func connectHostOf(opts bootstrap.Options) string {
	sub := strings.TrimSpace(opts.ConnectSubdomain)
	if sub == "" {
		sub = bootstrap.DefaultConnectSubdomain
	}
	return sub + "." + opts.Domain
}

func firstIP(ips []string) string {
	if len(ips) > 0 {
		return ips[0]
	}
	return "<exit-ip>"
}

func orRandom(v string, n int) string {
	if strings.TrimSpace(v) != "" {
		return v
	}
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		log.Fatalf("bootstrap: rng: %v", err)
	}
	return hex.EncodeToString(buf)
}

// localRunner executes bootstrap's system actions on the local machine.
type localRunner struct{}

func (localRunner) Run(ctx context.Context, cmd string) (string, error) {
	c := exec.CommandContext(ctx, "sh", "-c", cmd)
	out, err := c.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func (localRunner) WriteFile(path string, data []byte, mode fs.FileMode) error {
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

func (localRunner) Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
