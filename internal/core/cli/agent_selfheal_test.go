package cli

import (
	"context"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"trustpanel/internal/core/agent"
	"trustpanel/internal/core/agentapi"
)

// flakyServices reports a service dead until it has been restarted once,
// modelling a service that failed to come up during the one-shot boot
// reapply (e.g. a transient race) but would recover if retried.
type flakyServices struct {
	mu       sync.Mutex
	restarts int32
}

func (f *flakyServices) Restart(context.Context, string) error {
	atomic.AddInt32(&f.restarts, 1)
	return nil
}
func (f *flakyServices) Stop(context.Context, string) error { return nil }
func (f *flakyServices) Status(context.Context, string) (string, error) {
	if atomic.LoadInt32(&f.restarts) > 0 {
		return "active", nil
	}
	return "inactive", nil
}

type noopChecker struct{}

func (noopChecker) Check(context.Context, string, string) error { return nil }

// TestRunSelfHealRestartsDeadService verifies the periodic self-heal loop
// (added alongside the one-shot boot ReapplyCached) actually notices and
// restarts a service that stayed dead after boot, without needing a fresh
// controller push.
func TestRunSelfHealRestartsDeadService(t *testing.T) {
	root := t.TempDir()
	store, err := agent.OpenStore(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	svcs := &flakyServices{}
	r := agent.NewReconciler(agent.Config{
		NodeID: "n1", Roots: []string{root}, ServiceAllowlist: []string{"svc-a"},
	}, store, svcs, noopChecker{})

	// Seed the cache the way a real controller push would.
	r.Apply(context.Background(), agentapi.DesiredState{
		NodeID: "n1", Epoch: 1, RevisionID: 1, RevisionHash: "h1",
		Services: []agentapi.Service{{Name: "svc-a", Want: agentapi.WantRunning}},
	})
	if state, _ := svcs.Status(context.Background(), "svc-a"); state != "inactive" {
		t.Fatalf("precondition: svc-a should still be inactive, got %q", state)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	go runSelfHeal(ctx, r, 20*time.Millisecond)
	<-ctx.Done()

	if state, _ := svcs.Status(context.Background(), "svc-a"); state != "active" {
		t.Fatalf("self-heal should have restarted svc-a, still %q", state)
	}
}
