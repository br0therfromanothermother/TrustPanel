package cluster

import (
	"context"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"trustpanel/internal/core/agentapi"
	"trustpanel/internal/core/bootstrap"
	"trustpanel/internal/core/model"
	"trustpanel/internal/core/pki"
)

// fakePrimary records commands and answers the psql SHOW/SELECT probes
// add-standby issues on the primary.
type fakePrimary struct{ cmds []string }

func (f *fakePrimary) Run(_ context.Context, cmd string) (string, error) {
	f.cmds = append(f.cmds, cmd)
	switch {
	case strings.Contains(cmd, "listen_addresses"):
		return "localhost\n", nil // forces ALTER SYSTEM + restart
	case strings.Contains(cmd, "wal_level"):
		return "replica\n", nil
	case strings.Contains(cmd, "SHOW ssl"):
		return "on\n", nil
	case strings.Contains(cmd, "hba_file"):
		return "/etc/postgresql/14/main/pg_hba.conf\n", nil
	case strings.Contains(cmd, "pg_stat_replication"):
		return "10.0.0.2 streaming async\n", nil
	}
	return "", nil
}

// fakeStandby records commands + files written over the (fake) runner.
type fakeStandby struct {
	cmds  []string
	files map[string][]byte
}

func newFakeStandby() *fakeStandby { return &fakeStandby{files: map[string][]byte{}} }

func (f *fakeStandby) Run(_ context.Context, cmd string) (string, error) {
	f.cmds = append(f.cmds, cmd)
	if strings.Contains(cmd, "pg_is_in_recovery") {
		return "t\n", nil
	}
	return "", nil
}

func (f *fakeStandby) Put(_ context.Context, content []byte, remotePath string, _ os.FileMode) error {
	f.files[remotePath] = append([]byte(nil), content...)
	return nil
}

// fakeStore is an in-memory cluster.Store.
type fakeStore struct {
	nodes    map[string]model.Node
	cp       model.ControlPlane
	standbys []string
}

func (s *fakeStore) ControlPlane(context.Context) (model.ControlPlane, error) { return s.cp, nil }
func (s *fakeStore) LoadState(context.Context) (model.State, error) {
	var st model.State
	st.ControlPlane = s.cp
	for _, n := range s.nodes {
		st.Nodes = append(st.Nodes, n)
	}
	return st, nil
}
func (s *fakeStore) UpsertNode(_ context.Context, n model.Node) error {
	if err := n.Validate(); err != nil {
		return err
	}
	s.nodes[n.ID] = n
	return nil
}
func (s *fakeStore) SetStandbys(_ context.Context, ids []string) error { s.standbys = ids; return nil }

func testCA(t *testing.T) (*pki.CA, []byte) {
	t.Helper()
	certPEM, keyPEM, err := pki.GenerateCA("Test CA", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	ca, err := pki.LoadCA(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	return ca, keyPEM
}

func exitNode(id, ip string) model.Node {
	return model.Node{
		ID: id, Name: id, PublicRole: model.RoleExit, PublicIPs: []string{ip},
		AgentAddr: "127.0.0.1:8443",
		DialIn:    &model.DialIn{Proto: model.DialInProtoVLESSReality, Port: 443, UUID: "u-" + id, TargetSNI: "www.example.com"},
	}
}

// mintParams builds Params with a freshly-minted standby controller cert, the
// way the real callers (CLI / primary agent) do.
func mintParams(t *testing.T, ca *pki.CA, caKey []byte, p Params) Params {
	t.Helper()
	cert, key, err := ca.IssueLeaf(pki.RoleController, p.StandbyID+".controller", p.StandbyIPs, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	p.CACertPEM = ca.CertPEM()
	p.CAKeyPEM = caKey
	p.ControllerCert = cert
	p.ControllerKey = key
	return p
}

func TestAddStandby(t *testing.T) {
	ca, caKey := testCA(t)
	prim := &fakePrimary{}
	sb := newFakeStandby()
	st := &fakeStore{
		nodes: map[string]model.Node{
			"exit-primary": exitNode("exit-primary", "10.0.0.1"),
			"exit-standby": exitNode("exit-standby", "10.0.0.2"),
		},
		cp: model.ControlPlane{ActiveNodeID: "exit-primary"},
	}
	p := mintParams(t, ca, caKey, Params{
		PrimaryID: "exit-primary", StandbyID: "exit-standby",
		PrimaryIP: "10.0.0.1", StandbyIP: "10.0.0.2",
		StandbyIPs: []net.IP{net.ParseIP("10.0.0.2"), net.ParseIP("127.0.0.1")},
		ReplUser:   "replicator", ReplPass: "s3cret", ReplSlot: "standby_exit_standby",
		ServeEnv:  []byte("TRUSTPANEL_DSN=host=127.0.0.1 user=trustpanel password=x dbname=trustpanel sslmode=disable\n"),
		DeployEnv: []byte("TRUSTPANEL_BRAND=Example\n"),
		Binary:    []byte("ELF"),
		Layout:    bootstrap.DefaultLayout(),
		Validity:  time.Hour,
	})

	if err := AddStandby(context.Background(), prim, sb, st, p, nil); err != nil {
		t.Fatalf("AddStandby: %v", err)
	}

	// ---- primary-side ----
	wantPrim := []string{
		"ALTER SYSTEM SET listen_addresses",
		"WITH REPLICATION LOGIN PASSWORD",
		"pg_create_physical_replication_slot",
		"hostssl replication replicator 10.0.0.2/32",
		"ufw allow from 10.0.0.2 to any port 5432",
		"systemctl restart postgresql",
	}
	for _, w := range wantPrim {
		if !anyContains(prim.cmds, w) {
			t.Errorf("primary missing command %q\ngot: %v", w, prim.cmds)
		}
	}
	if !anyContains(prim.cmds, "grep -qxF") || !anyContains(prim.cmds, "/etc/postgresql/14/main/pg_hba.conf") {
		t.Errorf("pg_hba rule not appended to hba_file: %v", prim.cmds)
	}

	// ---- standby-side files ----
	l := bootstrap.DefaultLayout()
	for _, f := range []string{
		l.BinDir + "/trustpanel",
		l.PKIDir + "/ca.crt", l.PKIDir + "/ca.key",
		l.PKIDir + "/controller.crt", l.PKIDir + "/controller.key",
		l.EtcDir + "/serve.env", l.EtcDir + "/watchdog.env", l.EtcDir + "/deployment.env",
		l.EtcDir + "/agent.env",
		l.VarLib + "/known_hosts",
		l.SystemdDir + "/trustpanel-serve.service",
		l.SystemdDir + "/trustpanel-watchdog.service",
		l.SystemdDir + "/trustpanel-backup.service",
		l.SystemdDir + "/trustpanel-backup.timer",
		l.SystemdDir + "/trustpanel-verify-restore.service",
		l.SystemdDir + "/trustpanel-verify-restore.timer",
	} {
		if _, ok := sb.files[f]; !ok {
			t.Errorf("standby missing staged file %s", f)
		}
	}
	if string(sb.files[l.PKIDir+"/ca.key"]) != string(caKey) {
		t.Error("ca.key on standby should equal the CA private key")
	}
	if _, ok := sb.files[l.MigrationsDir()+"/"+bootstrap.Migrations()[0].Name]; !ok {
		t.Error("migrations should be staged on the standby")
	}
	// agent.env must carry the standby's own node-id so a bare `trustpanel promote`
	// (as the failover alert suggests) can self-resolve --node-id — an exit ships
	// no agent.env otherwise, so the bare command aborts on a real standby.
	if aenv := string(sb.files[l.EtcDir+"/agent.env"]); !strings.Contains(aenv, "TRUSTPANEL_NODE_ID=exit-standby") {
		t.Errorf("agent.env should carry the standby node-id, got %q", aenv)
	}
	wenv := string(sb.files[l.EtcDir+"/watchdog.env"])
	if !strings.Contains(wenv, "TRUSTPANEL_WATCH_TCP=10.0.0.1:5432") {
		t.Errorf("watchdog.env should watch the primary: %q", wenv)
	}
	if !strings.Contains(wenv, "TRUSTPANEL_DEADMAN_DSN=host=127.0.0.1 user=trustpanel") {
		t.Errorf("watchdog.env should carry the local replica dead-man DSN: %q", wenv)
	}

	// controller cert chains to the CA, role=controller, CN=<standby>.controller.
	cert := parseCert(t, sb.files[l.PKIDir+"/controller.crt"])
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(ca.CertPEM()) {
		t.Fatal("ca pool")
	}
	if _, err := cert.Verify(x509.VerifyOptions{Roots: pool, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil {
		t.Fatalf("controller cert should chain to CA: %v", err)
	}
	if cert.Subject.CommonName != "exit-standby.controller" {
		t.Errorf("controller CN = %q", cert.Subject.CommonName)
	}
	if !hasOU(cert, "controller") {
		t.Errorf("controller cert should have OU=controller, got %v", cert.Subject.OrganizationalUnit)
	}

	// ---- standby-side commands: replica + units ----
	wantSB := []string{
		"pg_basebackup",
		"sslmode=require",
		"-S 'standby_exit_standby'",
		"systemctl disable trustpanel-serve.service",
		"systemctl enable --now trustpanel-watchdog.service",
		"systemctl enable --now trustpanel-backup.timer",
		"systemctl enable --now trustpanel-verify-restore.timer",
		"install -d -m 0700 /var/backups/trustpanel",
		"chown -R trustpanel:trustpanel " + l.PKIDir,
	}
	for _, w := range wantSB {
		if !anyContains(sb.cmds, w) {
			t.Errorf("standby missing command %q\ngot: %v", w, sb.cmds)
		}
	}
	if anyContains(sb.cmds, "enable --now trustpanel-serve") {
		t.Error("serve must NOT be enabled/started on the standby (replica is read-only)")
	}

	// ---- DB bookkeeping ----
	if got := st.nodes["exit-primary"].PGRole; got != model.PGPrimary {
		t.Errorf("primary pg_role = %q, want primary", got)
	}
	sbn := st.nodes["exit-standby"]
	if sbn.PGRole != model.PGReplica {
		t.Errorf("standby pg_role = %q, want replica", sbn.PGRole)
	}
	if !sbn.MgmtCapable {
		t.Error("standby should be marked mgmt_capable")
	}
	if len(st.standbys) != 1 || st.standbys[0] != "exit-standby" {
		t.Errorf("standby_node_ids = %v, want [exit-standby]", st.standbys)
	}
}

// TestConfigurePrimaryRejectsBadInput checks that a malformed slot name or IP
// is refused before any value reaches psql/the shell.
func TestConfigurePrimaryRejectsBadInput(t *testing.T) {
	cases := []struct {
		name string
		p    Params
	}{
		{"slot injection", Params{ReplSlot: "x'; DROP TABLE y; --", PrimaryIP: "10.0.0.1", StandbyIP: "10.0.0.2", ReplUser: "replicator"}},
		{"bad primary ip", Params{ReplSlot: "good_slot", PrimaryIP: "10.0.0.1; rm -rf /", StandbyIP: "10.0.0.2", ReplUser: "replicator"}},
		{"bad standby ip", Params{ReplSlot: "good_slot", PrimaryIP: "10.0.0.1", StandbyIP: "$(curl evil)", ReplUser: "replicator"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			prim := &fakePrimary{}
			if err := ConfigurePrimary(context.Background(), prim, c.p, nil); err == nil {
				t.Fatalf("ConfigurePrimary accepted bad input %+v", c.p)
			}
			if len(prim.cmds) != 0 {
				t.Errorf("no command should run on rejected input, got %v", prim.cmds)
			}
		})
	}
}

func TestAddStandbySSLOffFallsBackToHost(t *testing.T) {
	ca, caKey := testCA(t)
	prim := &fakePrimary{}
	sslOff := &sslOffPrimary{fakePrimary: prim}
	sb := newFakeStandby()
	st := &fakeStore{
		nodes: map[string]model.Node{
			"p": exitNode("p", "10.0.0.1"), "s": exitNode("s", "10.0.0.2"),
		},
		cp: model.ControlPlane{ActiveNodeID: "p"},
	}
	p := mintParams(t, ca, caKey, Params{
		PrimaryID: "p", StandbyID: "s", PrimaryIP: "10.0.0.1", StandbyIP: "10.0.0.2",
		StandbyIPs: []net.IP{net.ParseIP("10.0.0.2")},
		ReplUser:   "replicator", ReplPass: "x", ReplSlot: "standby_s",
		ServeEnv: []byte("TRUSTPANEL_DSN=x\n"), Binary: []byte("ELF"),
		Layout: bootstrap.DefaultLayout(), Validity: time.Hour,
	})
	if err := AddStandby(context.Background(), sslOff, sb, st, p, nil); err != nil {
		t.Fatalf("AddStandby: %v", err)
	}
	if !anyContains(sslOff.cmds, "host replication replicator 10.0.0.2/32") {
		t.Errorf("ssl off should fall back to 'host' (not hostssl): %v", sslOff.cmds)
	}
	if anyContains(sslOff.cmds, "hostssl replication") {
		t.Error("must not use hostssl when ssl is off")
	}
}

type sslOffPrimary struct{ *fakePrimary }

func (s *sslOffPrimary) Run(ctx context.Context, cmd string) (string, error) {
	if strings.Contains(cmd, "SHOW ssl") {
		s.cmds = append(s.cmds, cmd)
		return "off\n", nil
	}
	return s.fakePrimary.Run(ctx, cmd)
}

// TestApplyReplica covers the standby-side entry point used by both the agent's
// transient root helper and its in-process fallback: it decodes the secret
// bundle, stages the CA material, and runs pg_basebackup.
func TestApplyReplica(t *testing.T) {
	ca, caKey := testCA(t)
	cert, key, err := ca.IssueLeaf(pki.RoleController, "s.controller", []net.IP{net.ParseIP("10.0.0.2")}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	b64 := base64.StdEncoding.EncodeToString
	in := agentapi.PrepareReplicaRequest{
		PrimaryID: "p", StandbyID: "s", PrimaryIP: "10.0.0.1", StandbyIP: "10.0.0.2",
		ReplUser: "replicator", ReplPass: "x", ReplSlot: "standby_s",
		ServeEnv:  b64([]byte("TRUSTPANEL_DSN=x\n")),
		DeployEnv: b64([]byte("")),
		CACertPEM: b64(ca.CertPEM()),
		CAKeyPEM:  b64(caKey),
		CtrlCert:  b64(cert),
		CtrlKey:   b64(key),
		Confirm:   "s",
	}
	sb := newFakeStandby()
	if err := ApplyReplica(context.Background(), sb, in, bootstrap.DefaultLayout(), nil); err != nil {
		t.Fatalf("ApplyReplica: %v", err)
	}
	l := bootstrap.DefaultLayout()
	if string(sb.files[l.PKIDir+"/ca.key"]) != string(caKey) {
		t.Error("ca.key should be staged from the bundle")
	}
	if !anyContains(sb.cmds, "pg_basebackup") || !anyContains(sb.cmds, "-S 'standby_s'") {
		t.Errorf("expected pg_basebackup with the slot, got %v", sb.cmds)
	}
}

func TestApplyReplicaBadBase64(t *testing.T) {
	in := agentapi.PrepareReplicaRequest{StandbyID: "s", CAKeyPEM: "!!!not-base64!!!"}
	if err := ApplyReplica(context.Background(), newFakeStandby(), in, bootstrap.DefaultLayout(), nil); err == nil {
		t.Error("ApplyReplica should reject an unparseable base64 field")
	}
}

func TestParamsFromPrimaryApply(t *testing.T) {
	in := agentapi.PrimaryApplyInput{
		PrimaryID: "p", StandbyID: "s", PrimaryIP: "1.1.1.1", StandbyIP: "2.2.2.2",
		ReplUser: "r", ReplPass: "pw", ReplSlot: "slot",
	}
	p := ParamsFromPrimaryApply(in, bootstrap.DefaultLayout())
	if p.PrimaryID != "p" || p.StandbyIP != "2.2.2.2" || p.ReplPass != "pw" || p.ReplSlot != "slot" {
		t.Errorf("mapping wrong: %+v", p)
	}
}

// TestRecordTopologyPrunesOldPrimary checks that after a role swap
// the new standby is the node that used to be primary. RecordTopology must add the
// new standby AND prune the new primary from standby_node_ids, so the primary is
// never left lingering as its own standby.
func TestRecordTopologyPrunesOldPrimary(t *testing.T) {
	st := &fakeStore{
		nodes: map[string]model.Node{
			"a": exitNode("a", "10.0.0.1"),
			"b": exitNode("b", "10.0.0.2"),
		},
		// Roles just swapped: b is now primary, a is being (re)built as standby.
		// standby_node_ids still carries the stale entry "b" from before the swap.
		cp: model.ControlPlane{ActiveNodeID: "b", StandbyNodeIDs: []string{"b"}},
	}
	p := Params{PrimaryID: "b", StandbyID: "a"}
	if err := RecordTopology(context.Background(), st, p, nil); err != nil {
		t.Fatalf("RecordTopology: %v", err)
	}
	if len(st.standbys) != 1 || st.standbys[0] != "a" {
		t.Errorf("standby_node_ids = %v, want [a] (b pruned, a added)", st.standbys)
	}
	if st.nodes["b"].PGRole != model.PGPrimary {
		t.Errorf("b pg_role = %q, want primary", st.nodes["b"].PGRole)
	}
	if st.nodes["a"].PGRole != model.PGReplica {
		t.Errorf("a pg_role = %q, want replica", st.nodes["a"].PGRole)
	}
}

func TestSlotNameSanitises(t *testing.T) {
	for in, want := range map[string]string{
		"exit-de.1": "standby_exit_de_1",
		"Node ABC":  "standby_node_abc",
		"east-eu-1": "standby_east_eu_1",
	} {
		if got := SlotName(in); got != want {
			t.Errorf("SlotName(%q) = %q, want %q", in, got, want)
		}
	}
}

// ---- test helpers ----

func anyContains(cmds []string, sub string) bool {
	for _, c := range cmds {
		if strings.Contains(c, sub) {
			return true
		}
	}
	return false
}

func parseCert(t *testing.T, pemBytes []byte) *x509.Certificate {
	t.Helper()
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		t.Fatal("not a PEM cert")
	}
	c, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func hasOU(c *x509.Certificate, ou string) bool {
	for _, o := range c.Subject.OrganizationalUnit {
		if o == ou {
			return true
		}
	}
	return false
}
