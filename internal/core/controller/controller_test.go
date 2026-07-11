package controller

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"trustpanel/internal/core/agent"
	"trustpanel/internal/core/agentapi"
	"trustpanel/internal/core/model"
	"trustpanel/internal/core/render"
)

type fakeSvc struct{}

func (fakeSvc) Restart(context.Context, string) error          { return nil }
func (fakeSvc) Stop(context.Context, string) error             { return nil }
func (fakeSvc) Status(context.Context, string) (string, error) { return "active", nil }

type okChecker struct{}

func (okChecker) Check(context.Context, string, string) error { return nil }

// startAgent spins up an in-process agent whose allowlisted roots are the
// layout dirs, returns its base URL and the layout.
func startAgent(t *testing.T, nodeID string) (string, Layout) {
	t.Helper()
	layout := Layout{TrustTunnelDir: t.TempDir(), SingBoxDir: t.TempDir()}
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
	return ts.URL, layout
}

func TestEndToEndEntryPush(t *testing.T) {
	url, layout := startAgent(t, "entryA")
	state := model.WorkedExample()
	at := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)

	compiled, err := render.RenderNode(state, "entryA", at, render.Options{})
	if err != nil {
		t.Fatal(err)
	}
	dsr, err := BuildDesiredState(state, compiled, layout, nil, 1, at)
	if err != nil {
		t.Fatal(err)
	}
	// epoch comes from control plane; entry gets both services + a singbox check.
	if dsr.Epoch != state.ControlPlane.Epoch {
		t.Errorf("epoch = %d, want %d", dsr.Epoch, state.ControlPlane.Epoch)
	}
	if len(dsr.Services) != 2 {
		t.Errorf("entry should have 2 services, got %v", dsr.Services)
	}

	client := NewClient(nil)
	res, err := client.PushDesiredState(context.Background(), url, dsr)
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != agentapi.OutcomeApplied || !res.Changed {
		t.Fatalf("want applied/changed, got %+v", res)
	}

	// Files landed at the mapped layout paths with the rendered content.
	creds, err := os.ReadFile(filepath.Join(layout.TrustTunnelDir, "credentials.toml"))
	if err != nil || !strings.Contains(string(creds), `username = "admin-device"`) {
		t.Fatalf("credentials.toml not applied: err=%v content=%q", err, creds)
	}
	sb, err := os.ReadFile(filepath.Join(layout.SingBoxDir, "sing-box.json"))
	if err != nil || !strings.Contains(string(sb), `"auth_user"`) {
		t.Fatalf("sing-box.json not applied: err=%v", err)
	}

	// Status reflects the applied epoch/revision.
	st, err := client.Status(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	if st.NodeID != "entryA" || st.LastAcceptedEpoch != dsr.Epoch || st.AppliedRevision != 1 {
		t.Fatalf("unexpected status: %+v", st)
	}

	// Re-push identical content -> no-change (idempotent).
	res2, err := client.PushDesiredState(context.Background(), url, dsr)
	if err != nil {
		t.Fatal(err)
	}
	if res2.Outcome != agentapi.OutcomeNoChange {
		t.Errorf("re-push should be no-change, got %+v", res2)
	}
}

func TestServicesForNode(t *testing.T) {
	const tt, sb = ServiceTrustTunnel, ServiceSingBox
	// Map of name -> Want for easy assertions.
	want := func(svcs []agentapi.Service) map[string]string {
		m := map[string]string{}
		for _, s := range svcs {
			m[s.Name] = s.Want
		}
		return m
	}

	// Born entry (no ManagedServices): both units running.
	got := want(servicesForNode(model.Node{PublicRole: model.RoleEntry}, model.RoleEntry))
	if len(got) != 2 || got[tt] != agentapi.WantRunning || got[sb] != agentapi.WantRunning {
		t.Errorf("entry: %v", got)
	}

	// Born exit (no ManagedServices): only sing-box, no trusttunnel stop.
	got = want(servicesForNode(model.Node{PublicRole: model.RoleExit}, model.RoleExit))
	if len(got) != 1 || got[sb] != agentapi.WantRunning {
		t.Errorf("born exit: %v", got)
	}

	// Single->exit flip: managed {tt,sb}, now exit => sing-box running, trusttunnel
	// stopped (so :443 is freed for Reality), and the stop is ordered first.
	flipped := model.Node{PublicRole: model.RoleExit, ManagedServices: []string{tt, sb}}
	svcs := servicesForNode(flipped, model.RoleExit)
	got = want(svcs)
	if got[tt] != agentapi.WantStopped || got[sb] != agentapi.WantRunning {
		t.Errorf("flipped exit wants: %v", got)
	}
	if svcs[0].Name != tt || svcs[0].Want != agentapi.WantStopped {
		t.Errorf("trusttunnel stop must come first, got %v", svcs)
	}
}

func TestEndToEndExitPush(t *testing.T) {
	url, layout := startAgent(t, "node1")
	state := model.WorkedExample()
	at := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)

	compiled, err := render.RenderNode(state, "node1", at, render.Options{})
	if err != nil {
		t.Fatal(err)
	}
	dsr, err := BuildDesiredState(state, compiled, layout, nil, 1, at)
	if err != nil {
		t.Fatal(err)
	}
	if len(dsr.Services) != 1 || dsr.Services[0].Name != ServiceSingBox {
		t.Errorf("exit should have only the sing-box service, got %v", dsr.Services)
	}

	res, err := NewClient(nil).PushDesiredState(context.Background(), url, dsr)
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != agentapi.OutcomeApplied {
		t.Fatalf("want applied, got %+v", res)
	}
	if _, err := os.ReadFile(filepath.Join(layout.SingBoxDir, "sing-box.json")); err != nil {
		t.Fatalf("exit sing-box.json not applied: %v", err)
	}
	// Exit nodes carry no TrustTunnel credentials.
	if _, err := os.Stat(filepath.Join(layout.TrustTunnelDir, "credentials.toml")); !os.IsNotExist(err) {
		t.Error("exit node should not have credentials.toml")
	}
}

func TestRevisionHashEpochIndependent(t *testing.T) {
	state := model.WorkedExample()
	at := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	layout := DefaultLayout()
	compiled, _ := render.RenderNode(state, "entryA", at, render.Options{})

	a, _ := BuildDesiredState(state, compiled, layout, nil, 1, at)
	state.ControlPlane.Epoch = 99
	b, _ := BuildDesiredState(state, compiled, layout, nil, 2, at)
	if a.RevisionHash != b.RevisionHash {
		t.Error("revision hash must not depend on epoch")
	}
	if a.Epoch == b.Epoch {
		t.Error("epochs should differ in this test")
	}
}
