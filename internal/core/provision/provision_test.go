package provision

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"trustpanel/internal/core/model"
	"trustpanel/internal/core/pki"
)

// fakeRunner records commands and files. When it sees `ca gen-csr` it generates
// a real CSR (as the node binary would) and returns it on stdout, so the
// enrollment path is exercised end-to-end.
type fakeRunner struct {
	cmds  []string
	files map[string][]byte
}

func newFakeRunner() *fakeRunner { return &fakeRunner{files: map[string][]byte{}} }

func (f *fakeRunner) Run(_ context.Context, cmd string) (string, error) {
	f.cmds = append(f.cmds, cmd)
	if strings.Contains(cmd, "ca gen-csr") {
		// Extract --node-id value.
		nodeID := "unknown"
		fields := strings.Fields(cmd)
		for i, w := range fields {
			if w == "--node-id" && i+1 < len(fields) {
				nodeID = fields[i+1]
			}
		}
		csrPEM, _, err := pki.GenerateCSR(nodeID)
		if err != nil {
			return "", err
		}
		return string(csrPEM), nil
	}
	if strings.Contains(cmd, "echo OK") {
		return "OK", nil
	}
	return "", nil
}

func (f *fakeRunner) Put(_ context.Context, content []byte, remotePath string, _ os.FileMode) error {
	cp := append([]byte(nil), content...)
	f.files[remotePath] = cp
	return nil
}

func newCA(t *testing.T) *pki.CA {
	t.Helper()
	certPEM, keyPEM, err := pki.GenerateCA("Test CA", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	ca, err := pki.LoadCA(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	return ca
}

func TestProvisionEnrollsAndInstalls(t *testing.T) {
	ca := newCA(t)
	p := &Provisioner{CA: ca, CertValidity: time.Hour}
	r := newFakeRunner()

	plan := Plan{
		NodeID: "entryA", Role: model.RoleEntry, IPs: []net.IP{net.ParseIP("203.0.113.10")},
		Binaries: map[string][]byte{"/usr/local/bin/trustpanel": []byte("ELF")},
		AgentEnv: "TRUSTPANEL_NODE_ID=entryA\n",
		Units: []UnitFile{
			{Name: "trustpanel-agent.service", Content: []byte("[Service]"), Enable: true},
			{Name: "trusttunnel.service", Content: []byte("[Service]"), Enable: false},
		},
	}

	res, err := p.Provision(context.Background(), r, plan)
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	if !res.NodeCertInstalled {
		t.Fatal("node cert should be installed")
	}

	// The CA bundle, node key (written by gen-csr --out), node cert, env, units.
	mustFile(t, r, "/etc/trustpanel/pki/ca.crt")
	mustFile(t, r, "/etc/trustpanel/pki/node.crt")
	mustFile(t, r, "/etc/trustpanel/agent.env")
	mustFile(t, r, "/etc/systemd/system/trustpanel-agent.service")
	mustFile(t, r, "/etc/systemd/system/trusttunnel.service")
	mustFile(t, r, "/usr/local/bin/trustpanel")

	// The installed node cert must chain to the CA and carry the node role + id.
	cert := parseCert(t, r.files["/etc/trustpanel/pki/node.crt"])
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(ca.CertPEM()) {
		t.Fatal("ca pool")
	}
	if _, err := cert.Verify(x509.VerifyOptions{Roots: pool, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil {
		t.Fatalf("node cert should chain to CA: %v", err)
	}
	if cert.Subject.CommonName != "entryA" {
		t.Errorf("node cert CN = %q, want entryA", cert.Subject.CommonName)
	}
	if !hasOU(cert, "node") {
		t.Errorf("node cert should have OU=node, got %v", cert.Subject.OrganizationalUnit)
	}

	// Enrollment ran gen-csr; only the Enable:true unit was started.
	if !anyContains(r.cmds, "ca gen-csr --node-id entryA") {
		t.Error("gen-csr should have run on the node")
	}
	if !anyContains(r.cmds, "systemctl enable --now trustpanel-agent.service") {
		t.Error("agent unit should be enabled+started")
	}
	if anyContains(r.cmds, "systemctl enable --now trusttunnel.service") {
		t.Error("trusttunnel unit had Enable:false and must not be auto-started")
	}
	if !anyContains(r.cmds, "systemctl daemon-reload") {
		t.Error("daemon-reload should run")
	}
}

func TestProvisionHardeningWithKey(t *testing.T) {
	ca := newCA(t)
	var logs []string
	p := &Provisioner{CA: ca, CertValidity: time.Hour, Log: func(s string) { logs = append(logs, s) }}
	r := newFakeRunner()
	plan := Plan{
		NodeID: "exit1", Role: model.RoleExit,
		Binaries: map[string][]byte{"/usr/local/bin/trustpanel": []byte("ELF")},
		Hardening: &Hardening{
			Enabled: true, SudoUser: "ops", SSHPubKey: "ssh-ed25519 AAAA...", SSHPort: 3222,
			DisableRootLogin: true, DisablePasswordAuth: true, Fail2ban: true, Firewall: true,
		},
	}
	if _, err := p.Provision(context.Background(), r, plan); err != nil {
		t.Fatalf("provision: %v", err)
	}
	if !anyContains(r.cmds, "useradd -m -s /bin/bash ops") {
		t.Error("sudo user should be created")
	}
	if _, ok := r.files["/home/ops/.ssh/authorized_keys"]; !ok {
		t.Error("authorized_keys should be installed")
	}
	conf := string(r.files["/etc/ssh/sshd_config.d/10-trustpanel.conf"])
	for _, want := range []string{"Port 3222", "PermitRootLogin no", "PasswordAuthentication no"} {
		if !strings.Contains(conf, want) {
			t.Errorf("sshd drop-in missing %q:\n%s", want, conf)
		}
	}
	if !anyContains(r.cmds, "fail2ban") || !anyContains(r.cmds, "ufw allow 3222/tcp") {
		t.Error("fail2ban + firewall (new ssh port) should be applied")
	}
	// progress log captured the hardening steps.
	if !anyContains(logs, "node enrolled") || !anyContains(logs, "hardening: firewall + sshd applied") {
		t.Errorf("progress log incomplete: %v", logs)
	}
}

func TestProvisionHardeningSkipsPasswordDisableWithoutKey(t *testing.T) {
	p := &Provisioner{CA: newCA(t), CertValidity: time.Hour}
	r := newFakeRunner()
	plan := Plan{
		NodeID: "n", Role: model.RoleExit,
		Hardening: &Hardening{Enabled: true, SudoUser: "ops", DisablePasswordAuth: true, DisableRootLogin: true},
	}
	if _, err := p.Provision(context.Background(), r, plan); err != nil {
		t.Fatal(err)
	}
	conf := string(r.files["/etc/ssh/sshd_config.d/10-trustpanel.conf"])
	if strings.Contains(conf, "PasswordAuthentication no") {
		t.Error("password auth must NOT be disabled without a verified key (lock-out guard)")
	}
	if !strings.Contains(conf, "PermitRootLogin no") {
		t.Error("root login disable is still safe to apply")
	}
}

func TestProvisionPreflightFails(t *testing.T) {
	p := &Provisioner{CA: newCA(t)}
	r := &failRunner{}
	if _, err := p.Provision(context.Background(), r, Plan{NodeID: "n"}); err == nil {
		t.Fatal("provision should fail when preflight (systemctl) errors")
	}
}

func TestSSHRunnerNeedsSudo(t *testing.T) {
	cases := map[string]bool{"root": false, "": false, "ops": true, "ubuntu": true, "admin": true}
	for user, want := range cases {
		if got := (SSHRunner{User: user}).needsSudo(); got != want {
			t.Errorf("needsSudo(user=%q) = %v, want %v", user, got, want)
		}
	}
}

type failRunner struct{}

func (failRunner) Run(context.Context, string) (string, error) {
	return "", os.ErrPermission
}
func (failRunner) Put(context.Context, []byte, string, os.FileMode) error { return nil }

func mustFile(t *testing.T, r *fakeRunner, path string) {
	t.Helper()
	if _, ok := r.files[path]; !ok {
		t.Errorf("expected file installed at %s", path)
	}
}

func TestRefreshUnitsUpgradesWithoutReenroll(t *testing.T) {
	p := &Provisioner{} // no CA needed for a refresh
	r := newFakeRunner()
	plan := Plan{
		NodeID: "entryA", Role: model.RoleEntry,
		Binaries: map[string][]byte{"/usr/local/bin/trustpanel": []byte("ELFv2")},
		AgentEnv: "TRUSTPANEL_NODE_ID=entryA\n",
		Units: []UnitFile{
			{Name: "trustpanel-agent.service", Content: []byte("[Service]v2"), Enable: true},
			{Name: "trustpanel-singbox.service", Content: []byte("[Service]"), Enable: false},
		},
	}
	res, err := p.RefreshUnits(context.Background(), r, plan)
	if err != nil {
		t.Fatalf("refresh-units: %v", err)
	}
	if res.NodeCertInstalled {
		t.Error("refresh must not (re)install a node cert")
	}

	// New binary, env and units are pushed.
	if string(r.files["/usr/local/bin/trustpanel"]) != "ELFv2" {
		t.Error("new binary should be uploaded")
	}
	mustFile(t, r, "/etc/trustpanel/agent.env")
	mustFile(t, r, "/etc/systemd/system/trustpanel-agent.service")
	mustFile(t, r, "/etc/systemd/system/trustpanel-singbox.service")

	// No enrollment and — critically — no leadership-state reset (epoch fence
	// must survive the upgrade).
	if anyContains(r.cmds, "ca gen-csr") {
		t.Error("refresh must not re-enroll")
	}
	if anyContains(r.cmds, "state.json") {
		t.Error("refresh must not reset agent state.json (would drop the epoch fence)")
	}

	// daemon-reload, agent restarted unconditionally, data-plane try-restarted
	// (active-only) and never enabled --now / hard-restarted.
	if !anyContains(r.cmds, "systemctl daemon-reload") {
		t.Error("daemon-reload should run")
	}
	if !anyContains(r.cmds, "systemctl restart trustpanel-agent.service") {
		t.Error("agent should be restarted to adopt the new binary/unit/env")
	}
	if !anyContains(r.cmds, "systemctl enable trustpanel-agent.service") {
		t.Error("enabled unit should be (re)enabled for boot")
	}
	if anyContains(r.cmds, "enable --now") {
		t.Error("refresh must not enable --now (no premature data-plane start)")
	}
	if !anyContains(r.cmds, "systemctl try-restart trustpanel-singbox.service") {
		t.Error("data-plane unit should be try-restarted (active-only)")
	}
	if anyContains(r.cmds, "systemctl restart trustpanel-singbox.service") {
		t.Error("data-plane unit must be try-restarted, not hard-restarted")
	}
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

func anyContains(cmds []string, sub string) bool {
	for _, c := range cmds {
		if strings.Contains(c, sub) {
			return true
		}
	}
	return false
}
