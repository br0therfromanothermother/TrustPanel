package panel

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"trustpanel/internal/core/agentapi"
	"trustpanel/internal/core/cluster"
	"trustpanel/internal/core/model"
)

// AddStandbyRequest is the body of POST /api/cluster/add-standby. Confirm must
// equal NodeID: the value is formed by the UI's "are you sure?" step, so the
// privileged command cannot be assembled without an explicit human confirmation
// naming the specific node.
type AddStandbyRequest struct {
	NodeID  string `json:"node_id"`
	Confirm string `json:"confirm"`
}

// handleAddStandby triggers HA standby provisioning for a registered exit node.
// The privileged work runs in the per-node root agents: the panel only validates,
// authorizes (session + CSRF + confirmation — the "weak variant"), routes to the
// primary's agent, and records the resulting topology. The CA private key is
// never handled here — the primary agent forwards it straight to the standby's
// agent over mTLS.
//
// Auth (session cookie + the per-session CSRF token on this state-changing POST)
// is enforced uniformly by protected(); on top of that the confirm field below
// must name the specific node, so the privileged command cannot be assembled by
// an accidental click.
func (p *Panel) handleAddStandby(w http.ResponseWriter, r *http.Request) {
	var req AddStandbyRequest
	if !decode(w, r, &req) {
		return
	}
	nodeID := strings.TrimSpace(req.NodeID)
	if nodeID == "" {
		writeErr(w, http.StatusBadRequest, "node_id is required")
		return
	}
	if req.Confirm != nodeID {
		writeErr(w, http.StatusBadRequest, "confirm must equal node_id (the UI confirmation step was not completed)")
		return
	}

	ctx := r.Context()
	st, err := p.store.LoadState(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	sb, ok := st.NodeByID(nodeID)
	if !ok {
		writeErr(w, http.StatusBadRequest, "node "+nodeID+" is not registered")
		return
	}
	if !sb.IsExit() {
		writeErr(w, http.StatusBadRequest, "a control-plane standby must be an exit node")
		return
	}
	if sb.PGRole == model.PGReplica || sb.PGRole == model.PGPrimary {
		writeErr(w, http.StatusBadRequest, "node "+nodeID+" already has a Postgres role ("+string(sb.PGRole)+")")
		return
	}
	primary, ok := findPrimary(st)
	if !ok {
		writeErr(w, http.StatusBadRequest, "could not resolve the control-plane primary node")
		return
	}
	if primary.ID == nodeID {
		writeErr(w, http.StatusBadRequest, "the standby must differ from the primary")
		return
	}
	if len(primary.PublicIPs) == 0 || len(sb.PublicIPs) == 0 {
		writeErr(w, http.StatusBadRequest, "primary and standby must both have a public IP")
		return
	}
	if strings.TrimSpace(sb.AgentAddr) == "" {
		writeErr(w, http.StatusBadRequest, "standby node has no agent address")
		return
	}

	primaryNode := *primary
	standbyNode := sb

	jobID := p.jobs.Start("add control-plane standby ("+sb.Name+")", func(log func(string)) error {
		return p.provisionStandby(context.Background(), log, primaryNode, standbyNode)
	})

	writeJSON(w, http.StatusAccepted, map[string]string{"job_id": jobID})
}

// provisionStandby drives the standby provisioning via the primary's agent and
// records the resulting topology. Shared by the standalone add-standby endpoint
// and the install-as-standby flow (a freshly provisioned exit). The caller
// guarantees sb is a registered exit with an agent address, a public IP, and that
// it differs from the primary.
func (p *Panel) provisionStandby(ctx context.Context, log func(string), primary, sb model.Node) error {
	in := agentapi.PrepareStandbyRequest{
		PrimaryID:   primary.ID,
		StandbyID:   sb.ID,
		PrimaryIP:   primary.PublicIPs[0],
		StandbyIP:   sb.PublicIPs[0],
		StandbyIPs:  append(append([]string{}, sb.PublicIPs...), "127.0.0.1"),
		StandbyAddr: sb.AgentAddr,
		ReplUser:    "replicator",
		Confirm:     sb.ID,
	}
	log(fmt.Sprintf("provisioning standby %q via primary agent %s…", sb.Name, primary.ID))
	res, err := p.fleet.PrepareStandby(ctx, primary, in)
	if err != nil {
		return fmt.Errorf("primary agent: %w", err)
	}
	if !res.OK {
		return fmt.Errorf("primary agent: %s", res.Error)
	}
	log("standby is a streaming replica (slot " + res.ReplSlot + ")")
	if res.Replication != "" {
		log("primary pg_stat_replication: " + res.Replication)
	}
	// Record topology (panel holds the DB; the agents do not touch it).
	if err := cluster.RecordTopology(ctx, p.store, cluster.Params{
		PrimaryID: primary.ID, StandbyID: sb.ID,
	}, log); err != nil {
		return fmt.Errorf("record topology: %w", err)
	}
	log("done — serve is staged DISABLED on the standby; watchdog + backup + verify-restore timers are ENABLED (backup/verify cadence is set in the panel).")
	log("FAILOVER IS MANUAL: when the primary is confirmed gone, run on the standby:")
	log("  trustpanel promote --node-id " + sb.ID + " --pg-promote --start-serve")
	return nil
}

// waitAgentReady polls the node's agent until a status call succeeds or timeout.
// A freshly provisioned node's agent takes a moment to come up; the add-standby
// step then needs it reachable (the primary agent pushes the bundle to it).
func (p *Panel) waitAgentReady(ctx context.Context, log func(string), n model.Node, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for attempt := 1; ; attempt++ {
		if _, err := p.fleet.Status(ctx, n); err == nil {
			log("standby agent is up")
			return nil
		} else if time.Now().After(deadline) {
			return fmt.Errorf("standby agent %s not reachable within %s: %w", n.AgentAddr, timeout, err)
		}
		log(fmt.Sprintf("waiting for the standby agent to come up (attempt %d)…", attempt))
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(4 * time.Second):
		}
	}
}

// findPrimary resolves the control-plane primary: the active node, else the sole
// mgmt-capable node.
func findPrimary(st model.State) (*model.Node, bool) {
	pick := func(id string) *model.Node {
		for i := range st.Nodes {
			if st.Nodes[i].ID == id {
				return &st.Nodes[i]
			}
		}
		return nil
	}
	if a := pick(st.ControlPlane.ActiveNodeID); a != nil {
		return a, true
	}
	var found *model.Node
	for i := range st.Nodes {
		if st.Nodes[i].MgmtCapable {
			if found != nil {
				return nil, false // ambiguous
			}
			found = &st.Nodes[i]
		}
	}
	return found, found != nil
}
