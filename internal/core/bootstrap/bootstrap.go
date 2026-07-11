// Package bootstrap turns a bare server into a TrustPanel control-plane (exit)
// node in one step: Postgres, the fleet CA, system user/dirs, binaries, systemd
// units, the panel admin, and the exit node registered with its Reality keys.
// It mirrors the manual steps an operator would otherwise run by hand. System
// actions go through a Runner (real local runner in the CLI; a fake in tests);
// database actions go through a small Store interface. The whole flow is
// idempotent so a re-run converges rather than failing.
package bootstrap

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io/fs"
	"net"
	"strings"
	"time"

	"trustpanel/internal/core/model"
	"trustpanel/internal/core/pki"
	"trustpanel/internal/core/provision"
)

// Runner performs privileged local system actions on the target server.
type Runner interface {
	Run(ctx context.Context, cmd string) (string, error)
	WriteFile(path string, data []byte, mode fs.FileMode) error
	Exists(path string) bool
}

// hardenRunner adapts a bootstrap Runner to the provision.Runner interface
// (Put -> WriteFile) so hardening can run through the same local runner.
type hardenRunner struct{ r Runner }

func (h hardenRunner) Run(ctx context.Context, cmd string) (string, error) {
	return h.r.Run(ctx, cmd)
}

func (h hardenRunner) Put(_ context.Context, content []byte, remotePath string, mode fs.FileMode) error {
	return h.r.WriteFile(remotePath, content, mode)
}

// Store is the slice of the panel store bootstrap needs (after Postgres is up).
type Store interface {
	MigrateOnce(ctx context.Context, name, sqlText string) (bool, error)
	AdminCount(ctx context.Context) (int, error)
	UpsertAdmin(ctx context.Context, username, passwordHash string) error
	UpsertNode(ctx context.Context, n model.Node) error
	// NodeByID reads one node so a re-run can reuse its stored dial-in.
	NodeByID(ctx context.Context, id string) (model.Node, bool, error)
	UpsertDomain(ctx context.Context, d model.Domain) error
	EnsureDefaultGroup(ctx context.Context) error
	BackfillDefaultExit(ctx context.Context, exitID string) error
	UserCount(ctx context.Context) (int, error)
	UpsertUser(ctx context.Context, u model.User) error
	Promote(ctx context.Context, activeNodeID string) (int64, error)
}

// Options configures a bootstrap run.
type Options struct {
	Layout Layout

	// Branding for the deployment (consumed by entry fallback sites later).
	Brand  string
	Domain string

	// Exit node identity.
	NodeID     string
	NodeName   string
	PublicIPs  []string
	RealitySNI string // borrowed third-party SNI for the Reality inbound

	// Postgres.
	DBName     string
	DBUser     string
	DBPassword string

	// Admin login to create (panel operator).
	AdminUser     string
	AdminPassword string

	// First VPN user to seed so the entry data plane is usable immediately
	// (TrustTunnel refuses to start with an empty credentials.toml). VPNPassword
	// is generated when empty; the CLI surfaces it in the summary.
	VPNUser     string
	VPNPassword string

	// Local source paths of the binaries to install.
	TrustPanelBin  string
	SingBoxBin     string
	TrustTunnelBin string

	CACommonName string
	CertValidity time.Duration

	// Single makes a one-box deployment: the control plane PLUS a client-facing
	// entry with local egress (no separate exit, no Reality inbound). The node is
	// registered as an entry; the client endpoint lives on the login subdomain
	// (<ConnectSubdomain>.<Domain>) and the apex carries a landing-only legend.
	Single bool
	// ConnectSubdomain is the login-justifying subdomain the VPN endpoint lives on
	// (default "vpn"). Heavy traffic on a login page reads as normal, unlike
	// heavy traffic on an empty landing; the apex stays a plain landing legend.
	ConnectSubdomain string
	ACMEEmail        string // entry TLS contact (single mode); defaults to admin@<Domain>
	ACMEStaging      bool   // use the Let's Encrypt staging directory (single mode)

	// Hardening optionally locks the box down as the final step (sudo user + key,
	// fail2ban, sshd drop-in, ufw). Same logic as the SSH provisioning path, run
	// locally here. Nil/disabled = skip. The firewall opens the (possibly new) SSH
	// port plus 443/80/agent so the box stays reachable and serviceable.
	Hardening *provision.Hardening

	// HashPassword hashes the admin password (panel.HashPassword in the CLI).
	HashPassword func(string) (string, error)
	// OpenStore connects to Postgres once it is up (store.Open in the CLI).
	OpenStore func(ctx context.Context, dsn string) (Store, error)
}

// Result summarizes a completed bootstrap.
type Result struct {
	DSN         string
	Steps       []string
	RealityPub  string
	Epoch       int64
	PanelListen string
}

// DSN returns the local Postgres connection string for the configured DB.
func (o Options) DSN() string {
	return fmt.Sprintf("host=127.0.0.1 port=5432 user=%s password=%s dbname=%s sslmode=disable",
		o.DBUser, o.DBPassword, o.DBName)
}

// Run executes the full bootstrap. log receives progress lines (may be nil).
func Run(ctx context.Context, r Runner, opts Options, log func(string)) (Result, error) {
	l := opts.Layout
	res := Result{DSN: opts.DSN(), PanelListen: "127.0.0.1:8787"}
	step := func(s string) {
		res.Steps = append(res.Steps, s)
		if log != nil {
			log(s)
		}
	}
	validity := opts.CertValidity
	if validity <= 0 {
		validity = 90 * 24 * time.Hour
	}

	// 1. Preflight.
	if _, err := r.Run(ctx, "systemctl --version"); err != nil {
		return res, fmt.Errorf("preflight: systemd required: %w", err)
	}
	if out, err := r.Run(ctx, "id -u"); err != nil || strings.TrimSpace(out) != "0" {
		return res, fmt.Errorf("preflight: must run as root (id -u = %q)", strings.TrimSpace(out))
	}
	step("preflight ok")

	// 2. Postgres.
	if _, err := r.Run(ctx, "command -v psql >/dev/null"); err != nil {
		if _, err := r.Run(ctx, "DEBIAN_FRONTEND=noninteractive apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq postgresql"); err != nil {
			return res, fmt.Errorf("install postgresql: %w", err)
		}
		step("postgresql installed")
	} else {
		step("postgresql present")
	}
	// A fresh apt install auto-creates a running "main" cluster, but a box whose
	// cluster was dropped (e.g. a prior teardown) keeps the package with no server
	// — then the role/database steps below have no socket to connect to. Create a
	// cluster when none is initialized (idempotent: skipped when one exists).
	ensureCluster := "if ! ls /var/lib/postgresql/*/*/PG_VERSION >/dev/null 2>&1; then " +
		"ver=$(ls /usr/lib/postgresql/ 2>/dev/null | sort -V | tail -1); " +
		"if [ -z \"$ver\" ]; then echo 'no postgresql server package found' >&2; exit 1; fi; " +
		"pg_createcluster \"$ver\" main --start; fi"
	if _, err := r.Run(ctx, ensureCluster); err != nil {
		return res, fmt.Errorf("ensure postgres cluster: %w", err)
	}
	if _, err := r.Run(ctx, "systemctl enable --now postgresql"); err != nil {
		return res, fmt.Errorf("enable postgresql: %w", err)
	}
	// The postgresql.service above is a Debian meta-unit that starts whatever
	// clusters happen to be registered at the time it runs — it does not itself
	// persist across reboots. The actual server process runs under the versioned
	// per-cluster unit (postgresql@<ver>-main), which must be enabled separately
	// or the database silently doesn't come back after a reboot.
	enableClusterUnit := "ver=$(ls /etc/postgresql/ 2>/dev/null | sort -V | tail -1); " +
		"if [ -z \"$ver\" ]; then echo 'no postgresql cluster config found' >&2; exit 1; fi; " +
		"systemctl enable \"postgresql@${ver}-main\""
	if _, err := r.Run(ctx, enableClusterUnit); err != nil {
		return res, fmt.Errorf("enable postgresql cluster unit: %w", err)
	}
	// Role + database (idempotent). Always (re)set the password so a re-run with a
	// freshly generated password stays in sync with what the panel connects with —
	// otherwise an existing role keeps its old password and SASL auth fails (28P01).
	roleSQL := fmt.Sprintf(
		"DO $$ BEGIN IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname='%s') THEN CREATE ROLE %s LOGIN PASSWORD '%s'; ELSE ALTER ROLE %s WITH LOGIN PASSWORD '%s'; END IF; END $$;",
		opts.DBUser, opts.DBUser, opts.DBPassword, opts.DBUser, opts.DBPassword)
	if _, err := r.Run(ctx, "sudo -u postgres psql -v ON_ERROR_STOP=1 -c "+shq(roleSQL)); err != nil {
		return res, fmt.Errorf("create db role: %w", err)
	}
	if _, err := r.Run(ctx, fmt.Sprintf(
		"sudo -u postgres psql -tAc \"SELECT 1 FROM pg_database WHERE datname='%s'\" | grep -q 1 || sudo -u postgres createdb -O %s %s",
		opts.DBName, opts.DBUser, opts.DBName)); err != nil {
		return res, fmt.Errorf("create database: %w", err)
	}
	step("postgres role + database ready")

	// 3. System user + directories.
	if _, err := r.Run(ctx, "id trustpanel >/dev/null 2>&1 || useradd --system --home "+l.VarLib+" --shell /usr/sbin/nologin trustpanel"); err != nil {
		return res, fmt.Errorf("create trustpanel user: %w", err)
	}
	dirList := []string{l.PKIDir, l.SingBoxDir, l.VarLib, l.AgentState, l.LogDir, l.MigrationsDir(), l.UnitsStageDir(), "/var/backups/trustpanel"}
	if opts.Single {
		dirList = append(dirList, "/etc/trusttunnel", "/opt/trusttunnel")
	}
	dirs := strings.Join(dirList, " ")
	if _, err := r.Run(ctx, "mkdir -p "+dirs); err != nil {
		return res, fmt.Errorf("mkdir: %w", err)
	}
	if _, err := r.Run(ctx, "chown trustpanel:trustpanel "+l.VarLib+" "+l.LogDir); err != nil {
		return res, fmt.Errorf("chown: %w", err)
	}
	step("system user + directories ready")

	// 4. Binaries.
	for _, b := range []struct{ src, dst string }{
		{opts.TrustPanelBin, l.BinDir + "/trustpanel"},
		{opts.SingBoxBin, l.BinDir + "/sing-box"},
		{opts.TrustTunnelBin, l.ShareDir + "/trusttunnel_endpoint"},
	} {
		if b.src == "" {
			continue
		}
		// Skip when src and dst are the same file (-ef: same device+inode), e.g.
		// bootstrapping from a binary that already lives at the install path —
		// `install` errors out on copying a file onto itself.
		if _, err := r.Run(ctx, fmt.Sprintf("[ %s -ef %s ] || install -m 0755 %s %s", shq(b.src), shq(b.dst), shq(b.src), shq(b.dst))); err != nil {
			return res, fmt.Errorf("install binary %s: %w", b.dst, err)
		}
	}
	if opts.Single {
		if opts.TrustTunnelBin == "" {
			return res, fmt.Errorf("single mode requires a trusttunnel binary (--trusttunnel-bin)")
		}
		// The entry data-plane unit runs trusttunnel from /opt/trusttunnel.
		dst := "/opt/trusttunnel/trusttunnel_endpoint"
		if _, err := r.Run(ctx, fmt.Sprintf("[ %s -ef %s ] || install -m 0755 %s %s", shq(opts.TrustTunnelBin), shq(dst), shq(opts.TrustTunnelBin), shq(dst))); err != nil {
			return res, fmt.Errorf("install trusttunnel: %w", err)
		}
		// Entry nodes export per-user traffic via sing-box's v2ray stats API; the
		// stock upstream sing-box release omits it, which makes the agent's config
		// check fail and silently roll back. Fail fast with a clear message.
		if out, err := r.Run(ctx, shq(l.BinDir+"/sing-box")+" version"); err != nil || !strings.Contains(out, "with_v2ray_api") {
			return res, fmt.Errorf("single mode needs a sing-box built with the with_v2ray_api tag (per-user stats); the provided binary lacks it:\n%s", strings.TrimSpace(out))
		}
	}
	step("binaries installed")

	// 5. Migrations + entry unit templates (for later provisioning).
	for _, m := range Migrations() {
		if err := r.WriteFile(l.MigrationsDir()+"/"+m.Name, []byte(m.SQL), 0o644); err != nil {
			return res, fmt.Errorf("write migration %s: %w", m.Name, err)
		}
	}
	for name, content := range EntryUnitTemplates() {
		if err := r.WriteFile(l.UnitsStageDir()+"/"+name, content, 0o644); err != nil {
			return res, fmt.Errorf("stage unit %s: %w", name, err)
		}
	}
	step("migrations + provisioning unit templates staged")

	// 6. Fleet CA + controller/node certs (idempotent: keep an existing CA).
	if err := ensurePKI(ctx, r, l, opts, validity, step); err != nil {
		return res, err
	}

	// 7. Reality keypair for the exit data plane (exit mode only; an entry/single
	// node accepts no node-to-node inbound and has no dial-in).
	var dialIn *model.DialIn
	if !opts.Single {
		di, err := genReality(opts.RealitySNI)
		if err != nil {
			return res, fmt.Errorf("generate reality keys: %w", err)
		}
		dialIn = di
		res.RealityPub = di.PublicKey
	}

	// 8. Env files + systemd units.
	type fileSpec struct {
		path string
		body string
		mode fs.FileMode
	}
	files := []fileSpec{
		{l.EtcDir + "/serve.env", serveEnv(opts.DSN()), 0o640},
		{l.EtcDir + "/deployment.env", deploymentEnv(opts.Brand, opts.Domain, opts.connectSubdomain()), 0o644},
		{l.SystemdDir + "/trustpanel-serve.service", l.serveUnit(), 0o644},
		{l.SystemdDir + "/trustpanel-backup.service", l.backupUnit(), 0o644},
		{l.SystemdDir + "/trustpanel-backup.timer", l.backupTimer(), 0o644},
		{l.SystemdDir + "/trustpanel-verify-restore.service", l.verifyUnit(), 0o644},
		{l.SystemdDir + "/trustpanel-verify-restore.timer", l.verifyTimer(), 0o644},
		{l.SystemdDir + "/trustpanel-bot.service", l.botUnit(), 0o644},
	}
	if opts.Single {
		// Control plane + a client-facing entry on one box: localhost agent that
		// also runs node-local ACME, plus the entry data-plane units.
		files = append(files,
			fileSpec{l.SystemdDir + "/trustpanel-agent.service", l.singleAgentUnit(opts.NodeID), 0o644},
			fileSpec{l.EtcDir + "/agent.env", singleAgentEnv(opts), 0o644},
			fileSpec{l.EtcDir + "/fallback.env", deploymentEnv(opts.Brand, opts.Domain, opts.connectSubdomain()), 0o644},
		)
		for name, content := range EntryUnitTemplates() {
			if name == "trustpanel-agent.service" {
				continue // replaced by the localhost single-agent unit above
			}
			files = append(files, fileSpec{l.SystemdDir + "/" + name, string(content), 0o644})
		}
	} else {
		files = append(files,
			fileSpec{l.SystemdDir + "/trustpanel-agent.service", l.exitAgentUnit(opts.NodeID), 0o644},
			fileSpec{l.SystemdDir + "/trustpanel-singbox.service", l.singboxUnit(), 0o644},
		)
	}
	for _, f := range files {
		if err := r.WriteFile(f.path, []byte(f.body), f.mode); err != nil {
			return res, fmt.Errorf("write %s: %w", f.path, err)
		}
	}
	if _, err := r.Run(ctx, "chown root:trustpanel "+l.EtcDir+"/serve.env"); err != nil {
		return res, fmt.Errorf("chown serve.env: %w", err)
	}
	step("env + systemd units written")

	// 9. Database: schema, admin, exit node, activate.
	if err := opts.populateDB(ctx, res.DSN, dialIn, &res, step); err != nil {
		return res, err
	}

	// 10. Start services.
	if _, err := r.Run(ctx, "systemctl daemon-reload"); err != nil {
		return res, fmt.Errorf("daemon-reload: %w", err)
	}
	startUnits := "trustpanel-serve.service trustpanel-agent.service trustpanel-bot.service trustpanel-backup.timer trustpanel-verify-restore.timer"
	if opts.Single {
		// The camouflage origin runs standalone; sing-box + trusttunnel are started
		// by the agent once the first reconcile writes their config and ACME issues
		// the TLS cert.
		startUnits += " trustpanel-fallback.service"
	}
	if _, err := r.Run(ctx, "systemctl enable --now "+startUnits); err != nil {
		return res, fmt.Errorf("enable services: %w", err)
	}
	step("control plane started")

	// 11. Optional hardening — last, after everything is up and reachable. The
	// firewall + sshd reload run on the local box; established sessions survive,
	// and ufw opens the (new) SSH port so the operator stays in.
	if opts.Hardening != nil && opts.Hardening.Enabled {
		if err := provision.Harden(ctx, hardenRunner{r}, opts.Hardening, step); err != nil {
			return res, fmt.Errorf("hardening: %w", err)
		}
	}
	return res, nil
}

func ensurePKI(ctx context.Context, r Runner, l Layout, opts Options, validity time.Duration, step func(string)) error {
	ips := parseIPs(opts.PublicIPs)
	ips = append(ips, net.ParseIP("127.0.0.1"))

	var ca *pki.CA
	if r.Exists(l.PKIDir + "/ca.crt") {
		step("fleet CA already present — reusing")
		// Existing CA: controller/node certs are assumed in place too; do not
		// reissue (would need the CA key off disk). Idempotent no-op.
		return nil
	}
	cn := opts.CACommonName
	if cn == "" {
		cn = "TrustPanel Fleet CA"
	}
	caCert, caKey, err := pki.GenerateCA(cn, 10*365*24*time.Hour)
	if err != nil {
		return fmt.Errorf("generate CA: %w", err)
	}
	ca, err = pki.LoadCA(caCert, caKey)
	if err != nil {
		return fmt.Errorf("load CA: %w", err)
	}
	ctrlCert, ctrlKey, err := ca.IssueLeaf(pki.RoleController, opts.NodeID+".controller", ips, validity)
	if err != nil {
		return fmt.Errorf("issue controller cert: %w", err)
	}
	nodeCert, nodeKey, err := ca.IssueLeaf(pki.RoleNode, opts.NodeID, ips, validity)
	if err != nil {
		return fmt.Errorf("issue node cert: %w", err)
	}
	files := []struct {
		path string
		data []byte
		mode fs.FileMode
	}{
		{l.PKIDir + "/ca.crt", caCert, 0o644},
		{l.PKIDir + "/ca.key", caKey, 0o600},
		{l.PKIDir + "/controller.crt", ctrlCert, 0o644},
		{l.PKIDir + "/controller.key", ctrlKey, 0o600},
		{l.PKIDir + "/node.crt", nodeCert, 0o644},
		{l.PKIDir + "/node.key", nodeKey, 0o600},
	}
	for _, f := range files {
		if err := r.WriteFile(f.path, f.data, f.mode); err != nil {
			return fmt.Errorf("write %s: %w", f.path, err)
		}
	}
	if _, err := r.Run(ctx, "chown -R trustpanel:trustpanel "+l.PKIDir); err != nil {
		return fmt.Errorf("chown pki: %w", err)
	}
	step("fleet CA + controller/node certs issued")
	return nil
}

func (o Options) populateDB(ctx context.Context, dsn string, dialIn *model.DialIn, res *Result, step func(string)) error {
	if o.OpenStore == nil {
		return fmt.Errorf("OpenStore not configured")
	}
	st, err := o.OpenStore(ctx, dsn)
	if err != nil {
		return fmt.Errorf("connect store: %w", err)
	}
	for _, m := range Migrations() {
		if _, err := st.MigrateOnce(ctx, m.Name, m.SQL); err != nil {
			return fmt.Errorf("migrate %s: %w", m.Name, err)
		}
	}
	step("schema migrated")

	if n, err := st.AdminCount(ctx); err != nil {
		return fmt.Errorf("admin count: %w", err)
	} else if n == 0 {
		hash, err := o.HashPassword(o.AdminPassword)
		if err != nil {
			return fmt.Errorf("hash admin password: %w", err)
		}
		if err := st.UpsertAdmin(ctx, o.AdminUser, hash); err != nil {
			return fmt.Errorf("create admin: %w", err)
		}
		step("admin user created: " + o.AdminUser)
	} else {
		step("admin already exists — kept")
	}

	// Seed the first VPN user so the entry data plane comes up with no manual step
	// (TrustTunnel exits if credentials.toml has no clients). Idempotent: only on a
	// fresh install (no users yet).
	if o.VPNUser != "" {
		if err := st.EnsureDefaultGroup(ctx); err != nil {
			return fmt.Errorf("ensure default group: %w", err)
		}
		if n, err := st.UserCount(ctx); err != nil {
			return fmt.Errorf("user count: %w", err)
		} else if n == 0 {
			u := model.User{
				ID: o.VPNUser, Username: o.VPNUser, Secret: o.VPNPassword,
				DisplayName: o.VPNUser, Enabled: true, GroupID: "default",
			}
			if err := st.UpsertUser(ctx, u); err != nil {
				return fmt.Errorf("create first VPN user: %w", err)
			}
			step("first VPN user created: " + o.VPNUser)
		} else {
			step("VPN users already exist — kept")
		}
	}

	// Keep the exit's existing Reality identity on a re-run. genReality mints
	// a fresh keypair+UUID every run; overwriting the stored one would rotate the
	// public key every client pins + the inbound UUID and break ALL existing
	// configs. Mirror the CA/admin/user "reuse if present" idempotency: if the node
	// already exists with a dial-in, keep it (and report it) instead of the fresh one.
	if dialIn != nil {
		if existing, ok, err := st.NodeByID(ctx, o.NodeID); err != nil {
			return fmt.Errorf("look up existing node: %w", err)
		} else if ok && existing.DialIn != nil && existing.DialIn.PublicKey != "" {
			dialIn = existing.DialIn
			res.RealityPub = existing.DialIn.PublicKey
			step("exit Reality identity already present — kept (re-run safe)")
		}
	}

	role := model.RoleExit
	if o.Single {
		role = model.RoleEntry
	}
	// A single-box has no failover peer, so its agent stays localhost-only (the
	// co-located controller reaches it at 127.0.0.1). An exit control plane is an
	// HA primary candidate: register a reachable agent_addr so that after a
	// failover the controller on the promoted standby can still reach this node's
	// agent (its unit binds 0.0.0.0:8443 to match).
	agentAddr := "127.0.0.1:8443"
	if !o.Single && len(o.PublicIPs) > 0 {
		agentAddr = o.PublicIPs[0] + ":8443"
	}
	node := model.Node{
		ID: o.NodeID, Name: o.NodeName, PublicRole: role,
		MgmtCapable: true, PublicIPs: o.PublicIPs, AgentAddr: agentAddr,
		PGRole: model.PGPrimary, DialIn: dialIn, // dialIn is nil for entry/single
	}
	if o.Single {
		// The single agent manages both the entry data plane (trusttunnel) and
		// sing-box; recording that here lets a later single->exit flip push
		// trusttunnel WantStopped (sing-box then reclaims :443 for Reality).
		node.ManagedServices = []string{"trusttunnel.service", "trustpanel-singbox.service"}
	}
	if err := node.Validate(); err != nil {
		return fmt.Errorf("%s node invalid: %w", role, err)
	}
	if err := st.UpsertNode(ctx, node); err != nil {
		return fmt.Errorf("register %s node: %w", role, err)
	}
	if role == model.RoleExit {
		// Without a default exit, a group's traffic egresses direct from the
		// entry instead of through this exit's Reality tunnel (see the README's
		// Multi-node note). Point any group that doesn't have one yet at this
		// exit — harmless before any entry is provisioned, and it means an
		// operator never has to know this step exists for the common case.
		if err := st.EnsureDefaultGroup(ctx); err != nil {
			return fmt.Errorf("ensure default group: %w", err)
		}
		if err := st.BackfillDefaultExit(ctx, o.NodeID); err != nil {
			return fmt.Errorf("backfill default exit: %w", err)
		}
	}
	// A single node is the client-facing entry: register its TrustTunnel
	// connection host so reconcile renders hosts.toml and the agent issues its
	// ACME cert. (Exit nodes have no public web host.)
	if o.Single {
		for _, d := range singleDomains(o.Domain, o.NodeID, o.connectSubdomain()) {
			if err := st.UpsertDomain(ctx, d); err != nil {
				return fmt.Errorf("register domain %s: %w", d.Hostname, err)
			}
		}
	}
	epoch, err := st.Promote(ctx, o.NodeID)
	if err != nil {
		return fmt.Errorf("activate node: %w", err)
	}
	res.Epoch = epoch
	step(fmt.Sprintf("%s node %q registered and activated (epoch %d)", role, o.NodeID, epoch))
	return nil
}

// DefaultConnectSubdomain is the login-justifying subdomain the VPN endpoint
// lives on when none is configured.
const DefaultConnectSubdomain = "vpn"

func (o Options) connectSubdomain() string {
	if s := strings.TrimSpace(o.ConnectSubdomain); s != "" {
		return s
	}
	return DefaultConnectSubdomain
}

// connectHost is the login subdomain the client endpoint lives on.
func (o Options) connectHost() string { return o.connectSubdomain() + "." + o.Domain }

// singleDomains returns the two TLS hosts for a single-box deploy: the client
// endpoint on the login subdomain (<sub>.<domain>, main-fallback = a login page,
// so heavy VPN traffic blends with sign-in traffic), and the apex as a
// landing-only legend. Both share one cert + fallback routing.
func singleDomains(domain, nodeID, sub string) []model.Domain {
	connect := sub + "." + domain
	return []model.Domain{
		{
			ID: "dom-" + strings.ReplaceAll(connect, ".", "-"), Hostname: connect,
			Purpose: model.PurposeMainFallback, NodeID: nodeID,
		},
		{
			ID: "dom-" + strings.ReplaceAll(domain, ".", "-"), Hostname: domain,
			Purpose: model.PurposeFallbackSite, NodeID: nodeID,
		},
	}
}

// singleAgentEnv builds /etc/trustpanel/agent.env for a single node: the node id
// plus node-local ACME settings so the agent issues one TLS cert covering BOTH
// the login subdomain (the endpoint) and the apex (the landing legend).
func singleAgentEnv(o Options) string {
	var b strings.Builder
	fmt.Fprintf(&b, "TRUSTPANEL_NODE_ID=%s\n", o.NodeID)
	if o.Domain != "" {
		fmt.Fprintf(&b, "TRUSTPANEL_ACME_DOMAINS=%s,%s\n", o.connectHost(), o.Domain)
		email := o.ACMEEmail
		if email == "" {
			email = "admin@" + o.Domain
		}
		fmt.Fprintf(&b, "TRUSTPANEL_ACME_EMAIL=%s\n", email)
		if o.ACMEStaging {
			b.WriteString("TRUSTPANEL_ACME_STAGING=1\n")
		}
	}
	return b.String()
}

// genReality builds the exit's VLESS+Reality dial-in material.
func genReality(sni string) (*model.DialIn, error) {
	k, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	uuid, err := genUUID()
	if err != nil {
		return nil, err
	}
	sid := make([]byte, 4)
	if _, err := rand.Read(sid); err != nil {
		return nil, err
	}
	return &model.DialIn{
		Proto:     model.DialInProtoVLESSReality,
		Port:      443,
		UUID:      uuid,
		TargetSNI: sni,
		PublicKey: base64.RawURLEncoding.EncodeToString(k.PublicKey().Bytes()),
		PrivKey:   base64.RawURLEncoding.EncodeToString(k.Bytes()),
		ShortID:   hex.EncodeToString(sid),
	}, nil
}

func genUUID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

func parseIPs(ss []string) []net.IP {
	var out []net.IP
	for _, s := range ss {
		if ip := net.ParseIP(strings.TrimSpace(s)); ip != nil {
			out = append(out, ip)
		}
	}
	return out
}

// shq single-quotes a string for safe use as one shell argument.
func shq(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
