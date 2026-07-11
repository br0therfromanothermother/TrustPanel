package panel

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"trustpanel/internal/core/idgen"
	"trustpanel/internal/core/model"
	"trustpanel/internal/core/provision"
)

// AddEntryRequest is the body of POST /api/cluster/add-entry: grow a single-box
// deployment into the canonical two-node topology. A new entry B is provisioned
// in front; the original box A keeps the control plane (Postgres + CA + panel)
// and is flipped to the EXIT, so nothing physically moves. See the plan's
// "Convert to two-node" section.
type AddEntryRequest struct {
	// New entry B.
	Name      string              `json:"name"`
	PublicIPs []string            `json:"public_ips"`
	Domain    string              `json:"domain"`            // variant 1: single client domain (apex => login-subdomain split); legacy
	Domains   []string            `json:"domains,omitempty"` // variant 1: explicit cert SANs (first = client endpoint). Preferred over Domain.
	SSH       provision.SSHParams `json:"ssh"`
	Hardening provision.Hardening `json:"hardening"`

	// A's new dial-in (it becomes the exit): borrowed third-party Reality SNI.
	RealitySNI string `json:"reality_sni"`

	// Groups whose egress switches to A (the new exit). Empty => all groups.
	GroupIDs []string `json:"group_ids,omitempty"`

	// Variant 1 (default): B uses a NEW client hostname; A's old domain rows are
	// dropped and clients are re-issued configs pointing at B. Variant 2: B reuses
	// A's existing client domain (the user moved DNS to B first); A's domain rows
	// are reassigned to B and configs need no re-issue.
	Variant int `json:"variant,omitempty"`

	// HealthTimeout caps the wait for B to come up (cert + :443) before flipping A.
	// Zero uses a sane default.
	HealthTimeoutSec int `json:"health_timeout_sec,omitempty"`
}

// literalEntryDomains builds B's domain rows + ACME SANs from operator-supplied
// hostnames used verbatim (variant 1): the first is the client endpoint
// (main-fallback / connection point), the rest are extra served names; all go
// into the single certificate.
func literalEntryDomains(nodeID string, hosts []string) (domains []model.Domain, acme []string) {
	for i, h := range hosts {
		purpose := model.PurposeFallbackSite
		if i == 0 {
			purpose = model.PurposeMainFallback
		}
		domains = append(domains, model.Domain{ID: idgen.New(h, "dom"), Hostname: h, Purpose: purpose, NodeID: nodeID})
		acme = append(acme, h)
	}
	return domains, acme
}

// nodeHostnames returns the hostnames of all domain rows currently owned by a node.
func nodeHostnames(st model.State, nodeID string) []string {
	var out []string
	for _, d := range st.Domains {
		if d.NodeID == nodeID {
			out = append(out, d.Hostname)
		}
	}
	return out
}

// trimmedNonEmpty trims each element and drops empties, preserving order.
func trimmedNonEmpty(in []string) []string {
	var out []string
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func (p *Panel) handleAddEntry(w http.ResponseWriter, r *http.Request) {
	if p.prov == nil {
		writeErr(w, http.StatusServiceUnavailable, "remote provisioning is not configured on this panel")
		return
	}
	var req AddEntryRequest
	if !decode(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Name) == "" || len(req.PublicIPs) == 0 || strings.TrimSpace(req.SSH.Host) == "" {
		writeErr(w, http.StatusBadRequest, "name, public_ips and ssh.host are required")
		return
	}
	if strings.TrimSpace(req.RealitySNI) == "" {
		writeErr(w, http.StatusBadRequest, "reality_sni is required (the SNI A's new Reality inbound borrows)")
		return
	}
	if req.Variant == 0 {
		req.Variant = 1
	}
	if req.Variant != 1 && req.Variant != 2 {
		writeErr(w, http.StatusBadRequest, "variant must be 1 (new hostname) or 2 (reuse domain)")
		return
	}

	ctx := r.Context()
	st, err := p.store.LoadState(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	// A is the control-plane node; it must currently be an entry (the single box).
	a, ok := findConvertSource(st)
	if !ok {
		writeErr(w, http.StatusBadRequest, "no single entry node to convert (expected one entry node holding the control plane)")
		return
	}
	nodeID := idgen.New(req.Name, "entry")
	entry := model.Node{
		ID: nodeID, Name: req.Name, PublicRole: model.RoleEntry,
		PublicIPs: req.PublicIPs, AgentAddr: req.PublicIPs[0] + ":8443",
		ManagedServices: []string{"trusttunnel.service", "trustpanel-singbox.service"},
	}
	if err := entry.Validate(); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	// Domain resolution differs by variant:
	//   variant 2 (reuse): B serves A's EXISTING hostnames — derive them from A's
	//     domain rows; no operator input. Rows stay A's until the post-flip reassign.
	//   variant 1 (new): B serves operator-supplied hostnames, used literally for
	//     the cert (first = client endpoint / main-fallback, rest = extra SANs).
	var bDomains []model.Domain
	var bACME, provDomains = []string{}, []model.Domain(nil)
	if req.Variant == 2 {
		hosts := nodeHostnames(st, a.ID)
		if len(hosts) == 0 {
			writeErr(w, http.StatusBadRequest, "variant 2 (reuse domain) needs A to already serve at least one domain to hand over")
			return
		}
		bACME = hosts // B's cert must cover the names it inherits; rows reassigned post-flip
	} else {
		hosts := req.Domains
		if len(hosts) == 0 && strings.TrimSpace(req.Domain) != "" {
			hosts = []string{req.Domain} // legacy single-domain body
		}
		hosts = trimmedNonEmpty(hosts)
		if len(hosts) == 0 {
			writeErr(w, http.StatusBadRequest, "variant 1 (new hostname) requires at least one domain for B's certificate")
			return
		}
		bDomains, bACME = literalEntryDomains(nodeID, hosts)
		provDomains = bDomains // create B's rows during provisioning
	}

	timeout := time.Duration(req.HealthTimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = 6 * time.Minute
	}

	aID := a.ID
	jobID := p.jobs.Start("convert to two-node (add entry "+req.Name+")", func(log func(string)) error {
		bg := context.Background()

		// 1. Provision B as an entry in front. A keeps serving throughout.
		log(fmt.Sprintf("provisioning new entry %q (%s) over SSH…", req.Name, req.PublicIPs[0]))
		if err := p.provisionNode(bg, log, entry, provDomains, bACME, req.SSH, req.Hardening); err != nil {
			return fmt.Errorf("provision entry B: %w (A left untouched and still serving)", err)
		}
		log("entry B provisioned + registered; pushing its config…")

		// 2. Reconcile so B renders its config and the agent issues its TLS cert.
		if _, _, err := p.ReconcileOnce(bg); err != nil {
			return fmt.Errorf("reconcile after provisioning B: %w", err)
		}

		// 3. GATE: do not touch A until B is actually serving (cert issued + :443
		//    reachable). If B never comes up we abort here, with A still serving.
		log("waiting for entry B to come up (TLS cert + :443)…")
		if err := p.waitEntryHealthy(bg, entry, timeout, log); err != nil {
			return fmt.Errorf("entry B did not become healthy: %w (A left untouched and still serving)", err)
		}
		log("entry B is healthy — flipping A to the exit role")

		// 4. Flip A entry->exit: give it a fresh Reality dial-in. The control plane
		//    (Postgres/CA/panel) stays on A; only its public :443 role changes.
		di, err := provision.NewRealityDialIn(strings.TrimSpace(req.RealitySNI))
		if err != nil {
			return fmt.Errorf("generate A's reality keys: %w", err)
		}
		aExit := *a
		aExit.PublicRole = model.RoleExit
		aExit.DialIn = di
		if err := p.store.UpsertNode(bg, aExit); err != nil {
			return fmt.Errorf("flip A to exit: %w", err)
		}
		log("A is now the exit (Reality pubkey " + di.PublicKey + ")")

		// 5. Switch egress: chosen groups (or all) now tunnel through A.
		groups, err := p.switchEgress(bg, req.GroupIDs, aID)
		if err != nil {
			return fmt.Errorf("switch egress to A: %w", err)
		}
		log(fmt.Sprintf("egress switched to A for %d group(s)", groups))

		// 6. Domain rows: B's client hostnames.
		if err := p.repointDomains(bg, req.Variant, aID, nodeID, bDomains); err != nil {
			return fmt.Errorf("repoint domains: %w", err)
		}

		// 7. Final reconcile: A re-renders as Reality inbound (its agent stops
		//    trusttunnel, freeing :443 for sing-box) and B tunnels to A.
		if _, _, err := p.ReconcileOnce(bg); err != nil {
			return fmt.Errorf("final reconcile: %w", err)
		}

		log("conversion complete. New client endpoint is on entry B.")
		if req.Variant == 1 {
			log("Re-issue client configs/QR pointing at entry B (Users → client config, entry=" + nodeID + ").")
		} else {
			log("Clients reconnect automatically (same hostname now served by B).")
		}
		return nil
	})
	writeJSON(w, http.StatusOK, map[string]string{"job_id": jobID, "entry_node_id": nodeID, "exit_node_id": aID})
}

// findConvertSource returns the node to flip into the exit: the control-plane
// node, which must currently be an entry (the single box).
func findConvertSource(st model.State) (*model.Node, bool) {
	pick := func(id string) *model.Node {
		for i := range st.Nodes {
			if st.Nodes[i].ID == id {
				return &st.Nodes[i]
			}
		}
		return nil
	}
	if a := pick(st.ControlPlane.ActiveNodeID); a != nil && a.IsEntry() {
		return a, true
	}
	// Fallback: the sole mgmt-capable entry.
	var found *model.Node
	for i := range st.Nodes {
		if st.Nodes[i].MgmtCapable && st.Nodes[i].IsEntry() {
			if found != nil {
				return nil, false // ambiguous
			}
			found = &st.Nodes[i]
		}
	}
	return found, found != nil
}

// waitEntryHealthy blocks until the entry's agent reports a valid TLS cert AND
// its public :443 accepts a TCP connection, or the timeout elapses.
func (p *Panel) waitEntryHealthy(ctx context.Context, n model.Node, timeout time.Duration, log func(string)) error {
	deadline := time.Now().Add(timeout)
	var lastMsg string
	note := func(s string) {
		if s != lastMsg {
			log("  … " + s)
			lastMsg = s
		}
	}
	for {
		status, err := p.fleet.Status(ctx, n)
		switch {
		case err != nil:
			note("agent not reachable yet: " + err.Error())
		case status.TLSCert == nil || status.TLSCert.NotAfter == nil:
			if status.TLSCert != nil && status.TLSCert.LastError != "" {
				note("ACME pending (" + status.TLSCert.LastError + ")")
			} else {
				note("waiting for TLS cert issuance")
			}
		case !status.TLSCert.NotAfter.After(time.Now()):
			note("cert present but expired")
		case !tcpReachable(n.PublicIPs[0]+":443", 5*time.Second):
			note("cert issued; waiting for :443 to accept connections")
		default:
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out after %s", timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(8 * time.Second):
		}
	}
}

func tcpReachable(addr string, timeout time.Duration) bool {
	c, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

// switchEgress sets DefaultExitID=exitID on the given groups (all groups when
// ids is empty) and returns how many were changed.
func (p *Panel) switchEgress(ctx context.Context, ids []string, exitID string) (int, error) {
	st, err := p.store.LoadState(ctx)
	if err != nil {
		return 0, err
	}
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	n := 0
	for _, g := range st.Groups {
		if len(ids) > 0 && !want[g.ID] {
			continue
		}
		if g.DefaultExitID == exitID {
			continue
		}
		g.DefaultExitID = exitID
		if err := p.store.UpsertGroup(ctx, g); err != nil {
			return n, fmt.Errorf("group %s: %w", g.ID, err)
		}
		n++
	}
	return n, nil
}

// repointDomains makes B own the client hostnames. Variant 1: drop A's old rows;
// B's new rows were created during provisioning. Variant 2: reassign A's existing
// rows to B (same hostnames, no config re-issue).
func (p *Panel) repointDomains(ctx context.Context, variant int, aID, bID string, bDomains []model.Domain) error {
	st, err := p.store.LoadState(ctx)
	if err != nil {
		return err
	}
	for _, d := range st.Domains {
		if d.NodeID != aID {
			continue
		}
		switch variant {
		case 2:
			d.NodeID = bID
			d.TLSStatus = "pending"
			if err := p.store.UpsertDomain(ctx, d); err != nil {
				return fmt.Errorf("reassign domain %s: %w", d.Hostname, err)
			}
		default: // variant 1
			if err := p.store.DeleteDomain(ctx, d.ID); err != nil {
				return fmt.Errorf("drop A domain %s: %w", d.Hostname, err)
			}
		}
	}
	return nil
}
