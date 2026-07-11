// Package provision onboards a fresh node over SSH (enrollment + unit
// install). SSH is used only for provisioning; all ongoing control is mTLS.
// The orchestration is runner-agnostic so it can be unit-tested with a fake
// runner.
package provision

import (
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"trustpanel/internal/core/model"
	"trustpanel/internal/core/pki"
)

// Runner executes commands and writes files on the target node.
type Runner interface {
	Run(ctx context.Context, cmd string) (string, error)
	Put(ctx context.Context, content []byte, remotePath string, mode os.FileMode) error
}

// UnitFile is a systemd unit to install (and optionally enable+start).
type UnitFile struct {
	Name    string
	Content []byte
	Enable  bool
}

// Plan describes what to install on a node.
type Plan struct {
	NodeID    string
	Role      model.PublicRole
	IPs       []net.IP          // node public IPs -> node cert SANs
	Binaries  map[string][]byte // remote path -> contents (e.g. /usr/local/bin/trustpanel)
	AgentEnv  string            // /etc/trustpanel/agent.env contents
	Files     []PlannedFile     // extra config files (e.g. fallback.env)
	Units     []UnitFile
	Hardening *Hardening // optional server hardening (last step)
}

// PlannedFile is an extra file to drop on the node during provisioning.
type PlannedFile struct {
	Path string
	Mode os.FileMode
	Body []byte
}

// Result records what happened.
type Result struct {
	NodeID            string
	Steps             []string
	NodeCertInstalled bool
}

// Provisioner enrolls and configures nodes using the fleet CA.
type Provisioner struct {
	CA             *pki.CA
	TrustPanelPath string        // remote path of the trustpanel binary (to run gen-csr)
	CertValidity   time.Duration // node cert lifetime
	Log            func(string)  // optional progress callback (for async job logs)
}

const (
	pkiDir = "/etc/trustpanel/pki"
)

// Provision runs the full onboarding sequence on the node via runner.
func (p *Provisioner) Provision(ctx context.Context, r Runner, plan Plan) (Result, error) {
	res := Result{NodeID: plan.NodeID}
	step := func(s string) {
		res.Steps = append(res.Steps, s)
		if p.Log != nil {
			p.Log(s)
		}
	}

	tpPath := p.TrustPanelPath
	if tpPath == "" {
		tpPath = "/usr/local/bin/trustpanel"
	}
	validity := p.CertValidity
	if validity <= 0 {
		validity = 90 * 24 * time.Hour
	}

	// 1. Preflight: require systemd.
	if _, err := r.Run(ctx, "systemctl --version"); err != nil {
		return res, fmt.Errorf("preflight: systemd not available: %w", err)
	}
	step("preflight ok")

	// 2. Directory layout.
	if _, err := r.Run(ctx, "mkdir -p "+pkiDir+" /etc/trustpanel/singbox /etc/trusttunnel /var/lib/trustpanel-agent /opt/trusttunnel /usr/local/bin"); err != nil {
		return res, fmt.Errorf("mkdir: %w", err)
	}
	step("dirs created")

	// 3. Upload binaries.
	for path, content := range plan.Binaries {
		if err := r.Put(ctx, content, path, 0o755); err != nil {
			return res, fmt.Errorf("upload %s: %w", path, err)
		}
		step("uploaded " + path)
	}

	// 4. Install the CA bundle.
	if err := r.Put(ctx, p.CA.CertPEM(), pkiDir+"/ca.crt", 0o644); err != nil {
		return res, fmt.Errorf("install ca: %w", err)
	}
	step("ca installed")

	// 5. Enrollment: node generates its keypair + CSR; the CA signs it; the
	// private key never leaves the node.
	csr, err := r.Run(ctx, fmt.Sprintf("%s ca gen-csr --node-id %s --out %s", tpPath, plan.NodeID, pkiDir))
	if err != nil {
		return res, fmt.Errorf("gen-csr: %w", err)
	}
	certPEM, err := p.CA.SignCSR([]byte(csr), pki.RoleNode, plan.NodeID, plan.IPs, validity)
	if err != nil {
		return res, fmt.Errorf("sign csr: %w", err)
	}
	if err := r.Put(ctx, certPEM, pkiDir+"/node.crt", 0o644); err != nil {
		return res, fmt.Errorf("install node cert: %w", err)
	}
	res.NodeCertInstalled = true
	step("node enrolled")

	// Re-enrollment under this CA makes the panel this node's owner. Drop any
	// persisted agent leadership state left by a previous control plane so the
	// epoch fence (agent rejects ds.Epoch < last_accepted_epoch, reconcile.go)
	// can't carry a stale epoch across the move and silently reject every push
	// from the new fleet. A fresh enrollment legitimately restarts at epoch 0; on
	// a first install the file is absent and this is a no-op.
	if _, err := r.Run(ctx, "rm -f /var/lib/trustpanel-agent/state.json"); err != nil {
		return res, fmt.Errorf("reset agent state: %w", err)
	}
	step("agent leadership state reset")

	// 6. Agent env + extra config files + systemd units.
	if plan.AgentEnv != "" {
		if err := r.Put(ctx, []byte(plan.AgentEnv), "/etc/trustpanel/agent.env", 0o600); err != nil {
			return res, fmt.Errorf("install agent.env: %w", err)
		}
		step("agent.env installed")
	}
	for _, f := range plan.Files {
		mode := f.Mode
		if mode == 0 {
			mode = 0o644
		}
		if err := r.Put(ctx, f.Body, f.Path, mode); err != nil {
			return res, fmt.Errorf("install %s: %w", f.Path, err)
		}
		step("installed " + f.Path)
	}
	for _, u := range plan.Units {
		if err := r.Put(ctx, u.Content, "/etc/systemd/system/"+u.Name, 0o644); err != nil {
			return res, fmt.Errorf("install unit %s: %w", u.Name, err)
		}
		step("unit installed " + u.Name)
	}

	// 7. Enable + start.
	if _, err := r.Run(ctx, "systemctl daemon-reload"); err != nil {
		return res, fmt.Errorf("daemon-reload: %w", err)
	}
	for _, u := range plan.Units {
		if !u.Enable {
			continue
		}
		if _, err := r.Run(ctx, "systemctl enable --now "+u.Name); err != nil {
			return res, fmt.Errorf("enable %s: %w", u.Name, err)
		}
		step("enabled " + u.Name)
	}
	// enable --now starts a stopped unit but won't restart an already-running one,
	// so re-provisioning onto a live box would keep the old in-memory agent (stale
	// epoch, old agent.env). Restart the agent explicitly to adopt the reset
	// leadership state and new env; the data-plane units follow from its reconcile.
	for _, u := range plan.Units {
		if u.Enable && strings.Contains(u.Name, "trustpanel-agent") {
			if _, err := r.Run(ctx, "systemctl restart "+u.Name); err != nil {
				return res, fmt.Errorf("restart %s: %w", u.Name, err)
			}
			step("restarted " + u.Name)
		}
	}

	// 8. Hardening (last, so a hardening misstep cannot block a working node;
	// the agent already runs over mTLS independent of SSH).
	if plan.Hardening != nil && plan.Hardening.Enabled {
		if err := harden(ctx, r, plan.Hardening, step); err != nil {
			return res, fmt.Errorf("hardening: %w", err)
		}
	}
	return res, nil
}

// RefreshUnits re-pushes binaries, systemd unit files and agent.env onto an
// already-enrolled node and reloads/restarts as needed — the upgrade path for a
// running node. Unlike Provision it does NOT touch the PKI (no CA install, no
// re-enrollment) and, crucially, does NOT reset /var/lib/trustpanel-agent/
// state.json: the agent keeps its persisted leadership state so the epoch
// fence survives the upgrade. Only plan.Binaries, plan.AgentEnv, plan.Files and
// plan.Units are consulted.
//
// Restart policy: the agent is always restarted (it should be running, and must
// adopt the new binary + unit + env). Every other unit is `try-restart`ed, which
// restarts it only if it is currently active — so a stopped data-plane unit
// (sing-box/trusttunnel before its first reconcile) is not started here without
// its config. Units marked Enable are (re)enabled for boot, without --now.
func (p *Provisioner) RefreshUnits(ctx context.Context, r Runner, plan Plan) (Result, error) {
	res := Result{NodeID: plan.NodeID}
	step := func(s string) {
		res.Steps = append(res.Steps, s)
		if p.Log != nil {
			p.Log(s)
		}
	}

	// Preflight: require systemd.
	if _, err := r.Run(ctx, "systemctl --version"); err != nil {
		return res, fmt.Errorf("preflight: systemd not available: %w", err)
	}
	step("preflight ok")

	// Ensure the layout exists (idempotent) so a Put into a new dir can't fail.
	if _, err := r.Run(ctx, "mkdir -p "+pkiDir+" /etc/trustpanel/singbox /etc/trusttunnel /var/lib/trustpanel-agent /opt/trusttunnel /usr/local/bin"); err != nil {
		return res, fmt.Errorf("mkdir: %w", err)
	}

	// Upload binaries.
	for path, content := range plan.Binaries {
		if err := r.Put(ctx, content, path, 0o755); err != nil {
			return res, fmt.Errorf("upload %s: %w", path, err)
		}
		step("uploaded " + path)
	}

	// Agent env + extra config files + unit files.
	if plan.AgentEnv != "" {
		if err := r.Put(ctx, []byte(plan.AgentEnv), "/etc/trustpanel/agent.env", 0o600); err != nil {
			return res, fmt.Errorf("install agent.env: %w", err)
		}
		step("agent.env installed")
	}
	for _, f := range plan.Files {
		mode := f.Mode
		if mode == 0 {
			mode = 0o644
		}
		if err := r.Put(ctx, f.Body, f.Path, mode); err != nil {
			return res, fmt.Errorf("install %s: %w", f.Path, err)
		}
		step("installed " + f.Path)
	}
	for _, u := range plan.Units {
		if err := r.Put(ctx, u.Content, "/etc/systemd/system/"+u.Name, 0o644); err != nil {
			return res, fmt.Errorf("install unit %s: %w", u.Name, err)
		}
		step("unit installed " + u.Name)
	}

	if _, err := r.Run(ctx, "systemctl daemon-reload"); err != nil {
		return res, fmt.Errorf("daemon-reload: %w", err)
	}
	step("daemon-reload")

	// (Re)enable boot links without starting; restart the agent unconditionally
	// and try-restart everything else (active-only) to pick up the new files.
	for _, u := range plan.Units {
		if u.Enable {
			if _, err := r.Run(ctx, "systemctl enable "+u.Name); err != nil {
				return res, fmt.Errorf("enable %s: %w", u.Name, err)
			}
		}
		if strings.Contains(u.Name, "trustpanel-agent") {
			if _, err := r.Run(ctx, "systemctl restart "+u.Name); err != nil {
				return res, fmt.Errorf("restart %s: %w", u.Name, err)
			}
			step("restarted " + u.Name)
			continue
		}
		if _, err := r.Run(ctx, "systemctl try-restart "+u.Name); err != nil {
			return res, fmt.Errorf("try-restart %s: %w", u.Name, err)
		}
		step("try-restart " + u.Name)
	}
	return res, nil
}
