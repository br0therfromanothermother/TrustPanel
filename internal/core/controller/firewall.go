package controller

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"sort"
	"strings"

	"trustpanel/internal/core/agentapi"
	"trustpanel/internal/core/model"
)

// firewallAllowBase is the file the node's firewall applier reads to learn which
// source IPs may reach its mTLS control port. It lives under the sing-box config
// root, already allowlisted for every role, so pushing it can never fall
// outside the agent's roots and stall a reconcile.
//
// The trustpanel-firewall.service unit hardcodes this path at the default layout
// (/etc/trustpanel/singbox/control-allow.conf); a non-default --singbox-dir would
// push the list somewhere the unit does not read, silently disabling scoping
// (fail-open, no lock-out). Keep the two in lock-step if the layout is made
// configurable on the node side.
const firewallAllowBase = "control-allow.conf"

// ControlPlaneAllowIPs returns the sorted, de-duplicated public IPs permitted to
// reach a node's control port: every control-plane candidate, meaning the active
// controller, all standbys and any mgmt-capable node, because any of them may
// become the controller after a promote. Baking the whole candidate set, not
// just the current active, lets a failover reach every agent without a
// firewall change having to land first.
//
// An empty result is meaningful: the applier reads an empty list as "leave the
// port open" (fail-open), so a deployment whose control-plane IPs are not yet
// known never fences itself off from its own agents.
func ControlPlaneAllowIPs(state model.State) []string {
	candidate := map[string]bool{}
	if id := state.ControlPlane.ActiveNodeID; id != "" {
		candidate[id] = true
	}
	for _, id := range state.ControlPlane.StandbyNodeIDs {
		candidate[id] = true
	}
	for _, n := range state.Nodes {
		if n.MgmtCapable {
			candidate[n.ID] = true
		}
	}

	ips := map[string]bool{}
	for _, n := range state.Nodes {
		if !candidate[n.ID] {
			continue
		}
		for _, ip := range n.PublicIPs {
			if ip = strings.TrimSpace(ip); ip != "" {
				ips[ip] = true
			}
		}
	}
	out := make([]string, 0, len(ips))
	for ip := range ips {
		out = append(out, ip)
	}
	sort.Strings(out)
	return out
}

// firewallAllowArtifact renders the allow-list file for one node's control port.
// The body is the sorted IP list, one per line; the applier on the node turns it
// into scoped firewall rules (or leaves the port open when the list is empty).
func firewallAllowArtifact(singBoxDir string, ips []string) agentapi.File {
	var body string
	if len(ips) > 0 {
		body = strings.Join(ips, "\n") + "\n"
	}
	sum := sha256.Sum256([]byte(body))
	return agentapi.File{
		Path:   filepath.Join(singBoxDir, firewallAllowBase),
		Mode:   0o644,
		SHA256: hex.EncodeToString(sum[:]),
		Body:   body,
	}
}
