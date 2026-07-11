package bootstrap

import (
	"context"
	"io/fs"
	"os"
	"strings"
	"testing"

	"trustpanel/internal/core/model"
	"trustpanel/internal/core/provision"
)

// --- drift: embedded copies must match the source-of-truth files on disk ---

func TestEmbeddedMigrationsMatchDisk(t *testing.T) {
	for _, m := range Migrations() {
		disk, err := os.ReadFile("../../../migrations/pg/" + m.Name)
		if err != nil {
			t.Fatalf("read disk migration %s: %v", m.Name, err)
		}
		if string(disk) != m.SQL {
			t.Errorf("embedded migration %s drifted from migrations/pg/%s — re-copy it", m.Name, m.Name)
		}
	}
}

func TestEmbeddedUnitsMatchDisk(t *testing.T) {
	for name, content := range EntryUnitTemplates() {
		disk, err := os.ReadFile("../../../deploy/systemd/" + name)
		if err != nil {
			t.Fatalf("read disk unit %s: %v", name, err)
		}
		if string(disk) != string(content) {
			t.Errorf("embedded unit %s drifted from deploy/systemd/%s — re-copy it", name, name)
		}
	}
}

// --- fakes ---

type fakeRunner struct {
	cmds  []string
	files map[string][]byte
	exist map[string]bool
}

func newFakeRunner() *fakeRunner {
	return &fakeRunner{files: map[string][]byte{}, exist: map[string]bool{}}
}

func (f *fakeRunner) Run(_ context.Context, cmd string) (string, error) {
	f.cmds = append(f.cmds, cmd)
	if strings.Contains(cmd, "id -u") {
		return "0\n", nil
	}
	if strings.Contains(cmd, "sing-box") && strings.Contains(cmd, "version") {
		return "sing-box version 1.13.13\n\nTags: with_v2ray_api,with_utls\n", nil
	}
	// Hardening's lock-out guard verifies the installed key before disabling
	// password auth; the fake reports the key present (WriteFile recorded it).
	if strings.Contains(cmd, "authorized_keys && echo OK") {
		return "OK\n", nil
	}
	return "", nil
}
func (f *fakeRunner) WriteFile(path string, data []byte, _ fs.FileMode) error {
	f.files[path] = data
	return nil
}
func (f *fakeRunner) Exists(path string) bool { return f.exist[path] }

func (f *fakeRunner) ran(substr string) bool {
	for _, c := range f.cmds {
		if strings.Contains(c, substr) {
			return true
		}
	}
	return false
}

type fakeStore struct {
	migrated         []string
	admins           map[string]string
	nodes            map[string]model.Node
	domains          map[string]model.Domain
	users            map[string]model.User
	groups           map[string]bool
	groupDefaultExit map[string]string
	active           string
	epoch            int64
}

func newFakeStore() *fakeStore {
	return &fakeStore{admins: map[string]string{}, nodes: map[string]model.Node{}, domains: map[string]model.Domain{}, users: map[string]model.User{}, groups: map[string]bool{}, groupDefaultExit: map[string]string{}}
}

func (s *fakeStore) MigrateOnce(_ context.Context, name, _ string) (bool, error) {
	s.migrated = append(s.migrated, name)
	return true, nil
}
func (s *fakeStore) AdminCount(context.Context) (int, error)          { return len(s.admins), nil }
func (s *fakeStore) UpsertAdmin(_ context.Context, u, h string) error { s.admins[u] = h; return nil }
func (s *fakeStore) UpsertNode(_ context.Context, n model.Node) error { s.nodes[n.ID] = n; return nil }
func (s *fakeStore) NodeByID(_ context.Context, id string) (model.Node, bool, error) {
	n, ok := s.nodes[id]
	return n, ok, nil
}
func (s *fakeStore) UpsertDomain(_ context.Context, d model.Domain) error {
	s.domains[d.ID] = d
	return nil
}
func (s *fakeStore) EnsureDefaultGroup(context.Context) error { s.groups["default"] = true; return nil }
func (s *fakeStore) BackfillDefaultExit(_ context.Context, exitID string) error {
	for g := range s.groups {
		if s.groupDefaultExit[g] == "" {
			s.groupDefaultExit[g] = exitID
		}
	}
	return nil
}
func (s *fakeStore) UserCount(context.Context) (int, error) { return len(s.users), nil }
func (s *fakeStore) UpsertUser(_ context.Context, u model.User) error {
	s.users[u.ID] = u
	return nil
}
func (s *fakeStore) Promote(_ context.Context, id string) (int64, error) {
	s.active = id
	s.epoch++
	return s.epoch, nil
}

func testOptions(fsStore Store) Options {
	return Options{
		Layout:        DefaultLayout(),
		Brand:         "Acme CDN",
		Domain:        "acme.example",
		NodeID:        "exit1",
		NodeName:      "Exit 1",
		PublicIPs:     []string{"203.0.113.10"},
		RealitySNI:    "www.microsoft.com",
		DBName:        "trustpanel",
		DBUser:        "trustpanel",
		DBPassword:    "secretpw",
		AdminUser:     "admin",
		AdminPassword: "adminpw12",
		VPNUser:       "admin",
		VPNPassword:   "vpnpw123456",
		TrustPanelBin: "/tmp/trustpanel",
		SingBoxBin:    "/tmp/sing-box",
		CACommonName:  "Acme Fleet CA",
		HashPassword:  func(p string) (string, error) { return "hash:" + p, nil },
		OpenStore:     func(context.Context, string) (Store, error) { return fsStore, nil },
	}
}

func TestRunOrchestration(t *testing.T) {
	r := newFakeRunner()
	st := newFakeStore()
	res, err := Run(context.Background(), r, testOptions(st), nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// System steps issued.
	for _, want := range []string{"systemctl --version", "useradd --system", "install -m 0755", "daemon-reload",
		"enable --now trustpanel-serve.service trustpanel-agent.service", "createdb", "CREATE ROLE"} {
		if !r.ran(want) {
			t.Errorf("expected a command containing %q", want)
		}
	}

	// Key files written.
	l := DefaultLayout()
	for _, p := range []string{
		l.PKIDir + "/ca.crt", l.PKIDir + "/ca.key", l.PKIDir + "/controller.crt", l.PKIDir + "/node.crt",
		l.EtcDir + "/serve.env", l.EtcDir + "/deployment.env",
		l.SystemdDir + "/trustpanel-serve.service", l.SystemdDir + "/trustpanel-agent.service", l.SystemdDir + "/trustpanel-singbox.service",
		l.MigrationsDir() + "/0001_init.sql", l.UnitsStageDir() + "/trusttunnel.service",
	} {
		if _, ok := r.files[p]; !ok {
			t.Errorf("expected file written: %s", p)
		}
	}

	// serve.env has the DSN; deployment.env has the brand.
	if !strings.Contains(string(r.files[l.EtcDir+"/serve.env"]), "dbname=trustpanel") {
		t.Errorf("serve.env missing DSN: %s", r.files[l.EtcDir+"/serve.env"])
	}
	if !strings.Contains(string(r.files[l.EtcDir+"/deployment.env"]), "TRUSTPANEL_BRAND=Acme CDN") {
		t.Errorf("deployment.env missing brand")
	}
	// Agent unit carries the node id and no trusttunnel/acme on the exit. As an
	// HA primary candidate it binds 0.0.0.0:8443 (reachable from a promoted
	// standby's controller), not localhost.
	agentUnit := string(r.files[l.SystemdDir+"/trustpanel-agent.service"])
	if !strings.Contains(agentUnit, "--node-id exit1") || strings.Contains(agentUnit, "trusttunnel-bin") || strings.Contains(agentUnit, "acme-domains") {
		t.Errorf("exit agent unit wrong:\n%s", agentUnit)
	}
	if !strings.Contains(agentUnit, "--listen 0.0.0.0:8443") {
		t.Errorf("exit-CP agent must bind a reachable address for post-failover management:\n%s", agentUnit)
	}

	// DB populated: every embedded migration ran, whatever that count is today.
	if len(st.migrated) != len(Migrations()) {
		t.Errorf("want %d migrations, got %v", len(Migrations()), st.migrated)
	}
	if st.admins["admin"] != "hash:adminpw12" {
		t.Errorf("admin not created: %v", st.admins)
	}
	n, ok := st.nodes["exit1"]
	if !ok || n.PublicRole != model.RoleExit || n.DialIn == nil || n.DialIn.TargetSNI != "www.microsoft.com" || n.DialIn.PublicKey == "" {
		t.Errorf("exit node not registered correctly: %+v", n)
	}
	// agent_addr must be reachable (public IP), so a controller on a promoted
	// standby can reach this node's agent after a failover — not 127.0.0.1.
	if n.AgentAddr != "203.0.113.10:8443" {
		t.Errorf("exit-CP agent_addr must be reachable, got %q", n.AgentAddr)
	}
	if st.active != "exit1" || res.Epoch != 1 {
		t.Errorf("exit not activated: active=%s epoch=%d", st.active, res.Epoch)
	}
	if res.RealityPub == "" || res.RealityPub != n.DialIn.PublicKey {
		t.Errorf("reality pub mismatch: %q vs %q", res.RealityPub, n.DialIn.PublicKey)
	}

	// The default group must come out pointed at this exit — otherwise a
	// freshly provisioned entry silently egresses direct instead of through
	// the Reality tunnel until an operator manually sets it.
	if !st.groups["default"] {
		t.Fatal("default group was not created")
	}
	if got := st.groupDefaultExit["default"]; got != "exit1" {
		t.Errorf("default group's default exit = %q, want %q (backfilled at bootstrap)", got, "exit1")
	}
}

func TestRunRequiresRoot(t *testing.T) {
	r := newFakeRunner()
	r.cmds = nil
	// Override id -u to non-root by wrapping.
	nr := &nonRootRunner{fakeRunner: r}
	_, err := Run(context.Background(), nr, testOptions(newFakeStore()), nil)
	if err == nil || !strings.Contains(err.Error(), "root") {
		t.Fatalf("expected root preflight error, got %v", err)
	}
}

type nonRootRunner struct{ *fakeRunner }

func (n *nonRootRunner) Run(ctx context.Context, cmd string) (string, error) {
	if strings.Contains(cmd, "id -u") {
		return "1000\n", nil
	}
	return n.fakeRunner.Run(ctx, cmd)
}

func TestRunSingleMode(t *testing.T) {
	r := newFakeRunner()
	st := newFakeStore()
	opts := testOptions(st)
	opts.Single = true
	opts.NodeID = "node1"
	opts.NodeName = "Node 1"
	opts.TrustTunnelBin = "/tmp/trusttunnel_endpoint"
	opts.ACMEStaging = true

	res, err := Run(context.Background(), r, opts, nil)
	if err != nil {
		t.Fatalf("Run(single): %v", err)
	}

	l := DefaultLayout()
	// Entry data-plane installed alongside the control plane.
	if !r.ran("install -m 0755 '/tmp/trusttunnel_endpoint' '/opt/trusttunnel/trusttunnel_endpoint'") {
		t.Errorf("trusttunnel not installed to /opt/trusttunnel")
	}
	if !r.ran("trustpanel-fallback.service") {
		t.Errorf("fallback service not started in single mode")
	}
	for _, p := range []string{
		l.SystemdDir + "/trustpanel-agent.service", l.SystemdDir + "/trustpanel-singbox.service",
		l.SystemdDir + "/trustpanel-fallback.service", l.SystemdDir + "/trusttunnel.service",
		l.EtcDir + "/agent.env", l.EtcDir + "/fallback.env",
	} {
		if _, ok := r.files[p]; !ok {
			t.Errorf("expected file written in single mode: %s", p)
		}
	}

	// Single agent: localhost bind, manages trusttunnel, ACME via agent.env.
	agentUnit := string(r.files[l.SystemdDir+"/trustpanel-agent.service"])
	if !strings.Contains(agentUnit, "--listen 127.0.0.1:8443") || !strings.Contains(agentUnit, "trusttunnel-bin") {
		t.Errorf("single agent unit wrong:\n%s", agentUnit)
	}
	// ACME must cover BOTH the login subdomain (endpoint) and the apex (landing).
	agentEnv := string(r.files[l.EtcDir+"/agent.env"])
	if !strings.Contains(agentEnv, "TRUSTPANEL_ACME_DOMAINS=vpn.acme.example,acme.example") || !strings.Contains(agentEnv, "TRUSTPANEL_ACME_STAGING=1") {
		t.Errorf("agent.env missing two-host ACME config:\n%s", agentEnv)
	}
	// fallback.env carries the login subdomain so the origin routes it to the portal.
	fbEnv := string(r.files[l.EtcDir+"/fallback.env"])
	if !strings.Contains(fbEnv, "TRUSTPANEL_CONNECT_SUBDOMAIN=vpn") {
		t.Errorf("fallback.env missing connect subdomain:\n%s", fbEnv)
	}

	// Node registered as an entry with NO dial-in/Reality, managing both data-plane
	// units so a later flip can stop trusttunnel.
	n, ok := st.nodes["node1"]
	if !ok || n.PublicRole != model.RoleEntry || n.DialIn != nil || !n.MgmtCapable {
		t.Errorf("single node not registered as a mgmt entry: %+v", n)
	}
	// A single box has no failover peer, so its agent stays localhost-only.
	if n.AgentAddr != "127.0.0.1:8443" {
		t.Errorf("single node agent_addr should stay localhost, got %q", n.AgentAddr)
	}
	if len(n.ManagedServices) != 2 {
		t.Errorf("single node should manage trusttunnel+singbox, got %v", n.ManagedServices)
	}
	// A first VPN user is seeded (default group) so credentials.toml is non-empty
	// and TrustTunnel starts without a manual step.
	if !st.groups["default"] {
		t.Errorf("default group not ensured")
	}
	// Single mode has no separate exit — routing stays direct, so nothing
	// should backfill a default_exit_id here (that's only for multi-node).
	if got := st.groupDefaultExit["default"]; got != "" {
		t.Errorf("single mode must not set a default exit, got %q", got)
	}
	if u, ok := st.users["admin"]; !ok || u.Username != "admin" || u.GroupID != "default" || !u.Enabled || u.Secret == "" {
		t.Errorf("first VPN user not seeded correctly: %+v", st.users)
	}
	if res.RealityPub != "" {
		t.Errorf("single mode must not generate Reality keys, got %q", res.RealityPub)
	}
	// The endpoint lives on the login subdomain (main-fallback); the apex is a
	// landing-only legend.
	var connectOK, apexOK bool
	for _, d := range st.domains {
		if d.Hostname == "vpn.acme.example" && d.Purpose == model.PurposeMainFallback && d.NodeID == "node1" {
			connectOK = true
		}
		if d.Hostname == "acme.example" && d.Purpose == model.PurposeFallbackSite && d.NodeID == "node1" {
			apexOK = true
		}
	}
	if !connectOK || !apexOK {
		t.Errorf("expected login-subdomain endpoint + apex landing, got: %+v", st.domains)
	}
	// No exit-only Reality SNI should leak into the agent/units.
	if strings.Contains(agentUnit, "reality") {
		t.Errorf("entry agent unit should not mention reality")
	}
}

func TestExistingCAReused(t *testing.T) {
	r := newFakeRunner()
	r.exist[DefaultLayout().PKIDir+"/ca.crt"] = true
	_, err := Run(context.Background(), r, testOptions(newFakeStore()), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := r.files[DefaultLayout().PKIDir+"/ca.key"]; ok {
		t.Errorf("must not overwrite CA key when a CA already exists")
	}
}

// TestRerunReusesExitDialIn checks that re-running bootstrap against a store
// that already has the exit node must NOT rotate its Reality keys/UUID (that
// would break every existing client). The stored dial-in is kept.
func TestRerunReusesExitDialIn(t *testing.T) {
	fs := newFakeStore()
	opts := testOptions(fs)

	res1, err := Run(context.Background(), newFakeRunner(), opts, nil)
	if err != nil {
		t.Fatal(err)
	}
	first := fs.nodes["exit1"].DialIn
	if first == nil || first.PublicKey == "" || first.UUID == "" {
		t.Fatalf("first run should have set a reality dial-in, got %+v", first)
	}

	res2, err := Run(context.Background(), newFakeRunner(), opts, nil)
	if err != nil {
		t.Fatal(err)
	}
	second := fs.nodes["exit1"].DialIn
	if second.PublicKey != first.PublicKey || second.UUID != first.UUID || second.PrivKey != first.PrivKey {
		t.Errorf("re-run rotated the exit's reality identity: %+v -> %+v", first, second)
	}
	if res2.RealityPub != res1.RealityPub {
		t.Errorf("reported reality pubkey changed on re-run: %q -> %q", res1.RealityPub, res2.RealityPub)
	}
}

func TestRunHardening(t *testing.T) {
	r := newFakeRunner()
	opts := testOptions(newFakeStore())
	opts.Hardening = &provision.Hardening{
		Enabled:             true,
		SudoUser:            "ops",
		SSHPubKey:           "ssh-ed25519 AAAA test",
		SSHPort:             3222,
		DisableRootLogin:    true,
		DisablePasswordAuth: true,
		Fail2ban:            true,
		Firewall:            true,
	}
	if _, err := Run(context.Background(), r, opts, nil); err != nil {
		t.Fatal(err)
	}
	// Hardening runs through the same logic as the SSH path, over the local runner.
	if !r.ran("useradd -m -s /bin/bash ops") {
		t.Error("expected sudo user creation")
	}
	if !r.ran("fail2ban") {
		t.Error("expected fail2ban install")
	}
	if !r.ran("ufw allow 3222/tcp") || !r.ran("ufw --force enable") {
		t.Error("expected ufw to open the new SSH port and enable")
	}
	if _, ok := r.files["/home/ops/.ssh/authorized_keys"]; !ok {
		t.Error("expected authorized_keys written for the sudo user")
	}
	conf, ok := r.files["/etc/ssh/sshd_config.d/10-trustpanel.conf"]
	if !ok {
		t.Fatal("expected sshd drop-in written")
	}
	for _, want := range []string{"Port 3222", "PermitRootLogin no", "PasswordAuthentication no"} {
		if !strings.Contains(string(conf), want) {
			t.Errorf("sshd drop-in missing %q:\n%s", want, conf)
		}
	}
}

func TestRunNoHardeningByDefault(t *testing.T) {
	r := newFakeRunner()
	if _, err := Run(context.Background(), r, testOptions(newFakeStore()), nil); err != nil {
		t.Fatal(err)
	}
	if r.ran("ufw --force enable") || r.ran("/etc/sudoers.d/90-") {
		t.Error("hardening must not run unless explicitly enabled")
	}
}
