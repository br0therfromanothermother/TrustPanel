package controller

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"trustpanel/internal/core/agent"
	"trustpanel/internal/core/agentapi"
	"trustpanel/internal/core/model"
	"trustpanel/internal/core/render"
)

// startAgentOn starts an in-process agent for nodeID rooted at the shared
// layout dirs and returns its base URL.
func startAgentOn(t *testing.T, nodeID string, layout Layout) string {
	t.Helper()
	store, err := agent.OpenStore(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	r := agent.NewReconciler(agent.Config{
		NodeID: nodeID, Roots: layout.Roots(),
		ServiceAllowlist: []string{ServiceTrustTunnel, ServiceSingBox},
	}, store, fakeSvc{}, okChecker{})
	ts := httptest.NewServer(agent.NewServer(nodeID, r, store, nil).Handler())
	t.Cleanup(ts.Close)
	return ts.URL
}

func TestFleetReconcileAllNodes(t *testing.T) {
	state := model.WorkedExample()
	layout := Layout{TrustTunnelDir: t.TempDir(), SingBoxDir: t.TempDir()}

	urls := map[string]string{}
	for _, n := range state.Nodes {
		urls[n.ID] = startAgentOn(t, n.ID, layout)
	}

	fleet := NewFleet(NewClient(nil), layout, render.Options{})
	fleet.URLFor = func(n model.Node) string { return urls[n.ID] }
	fleet.RuleSets = fakeRuleSets{}

	out := fleet.Reconcile(context.Background(), state, 1, time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC))

	if len(out) != len(state.Nodes) {
		t.Fatalf("expected %d node outcomes, got %d", len(state.Nodes), len(out))
	}
	for _, n := range state.Nodes {
		o := out[n.ID]
		if o.Err != nil {
			t.Errorf("node %s: push error: %v", n.ID, o.Err)
			continue
		}
		if o.Result.Outcome != agentapi.OutcomeApplied {
			t.Errorf("node %s: want applied, got %s (%s)", n.ID, o.Result.Outcome, o.Result.Error)
		}
		if o.Result.LastAcceptedEpoch != state.ControlPlane.Epoch {
			t.Errorf("node %s: epoch %d, want %d", n.ID, o.Result.LastAcceptedEpoch, state.ControlPlane.Epoch)
		}
	}
}

// TestReconcileSurfacesRenderWarnings checks that a render compile-time warning
// (here a drained exit falling back to local egress) must reach the operator via
// the NodeOutcome, not be silently discarded.
func TestReconcileSurfacesRenderWarnings(t *testing.T) {
	layout := Layout{TrustTunnelDir: t.TempDir(), SingBoxDir: t.TempDir()}
	state := model.State{
		Nodes: []model.Node{
			{ID: "en", Name: "Entry", PublicRole: model.RoleEntry, PublicIPs: []string{"1.1.1.1"}, AgentAddr: "1.1.1.1:8443"},
			{ID: "ex", Name: "Exit", PublicRole: model.RoleExit, PublicIPs: []string{"2.2.2.2"}, AgentAddr: "2.2.2.2:8443",
				Maintenance: true,
				DialIn:      &model.DialIn{Proto: model.DialInProtoVLESSReality, Port: 443, UUID: "u", TargetSNI: "www.cdn77.com", PublicKey: "k", PrivKey: "p", ShortID: "ab"}},
		},
		Groups: []model.Group{{ID: "g", Name: "G", DefaultExitID: "ex"}},
		Users:  []model.User{{ID: "u1", Username: "alice", Secret: "s", Enabled: true, GroupID: "g"}},
	}
	urls := map[string]string{}
	for _, n := range state.Nodes {
		urls[n.ID] = startAgentOn(t, n.ID, layout)
	}
	fleet := NewFleet(NewClient(nil), layout, render.Options{})
	fleet.URLFor = func(n model.Node) string { return urls[n.ID] }
	fleet.RuleSets = fakeRuleSets{}

	out := fleet.Reconcile(context.Background(), state, 1, time.Now())
	en := out["en"]
	if en.Err != nil {
		t.Fatalf("entry push error: %v", en.Err)
	}
	found := false
	for _, w := range en.Warnings {
		if strings.Contains(w, "maintenance") {
			found = true
		}
	}
	if !found {
		t.Errorf("entry outcome should carry the drained-exit warning, got %v", en.Warnings)
	}
}

// fakeRuleSets returns a valid (magic-prefixed) .srs for any tag.
type fakeRuleSets struct{}

func (fakeRuleSets) Get(_ context.Context, tag string) ([]byte, error) {
	return append([]byte("SRS\x01"), []byte(tag)...), nil
}
