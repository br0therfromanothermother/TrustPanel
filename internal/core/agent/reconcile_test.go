package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"trustpanel/internal/core/agentapi"
)

type fakeServices struct {
	restarted   []string
	stopped     []string
	failRestart map[string]bool
}

func (f *fakeServices) Restart(_ context.Context, name string) error {
	if f.failRestart[name] {
		return fmt.Errorf("restart failed: %s", name)
	}
	f.restarted = append(f.restarted, name)
	return nil
}
func (f *fakeServices) Stop(_ context.Context, name string) error {
	f.stopped = append(f.stopped, name)
	return nil
}
func (f *fakeServices) Status(context.Context, string) (string, error) { return "active", nil }

type fakeChecker struct {
	failKind map[string]bool
}

func (f *fakeChecker) Check(_ context.Context, kind, _ string) error {
	if f.failKind[kind] {
		return fmt.Errorf("check %s failed", kind)
	}
	return nil
}

func setup(t *testing.T) (*Reconciler, string, *fakeServices, *fakeChecker, *Store) {
	t.Helper()
	root := t.TempDir()
	store, err := OpenStore(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	fs := &fakeServices{failRestart: map[string]bool{}}
	fc := &fakeChecker{failKind: map[string]bool{}}
	r := NewReconciler(Config{
		NodeID: "n1", Roots: []string{root},
		ServiceAllowlist: []string{"svc-a", "svc-b"},
	}, store, fs, fc)
	return r, root, fs, fc, store
}

func mkFile(path, body string) agentapi.File {
	return agentapi.File{Path: path, Mode: 0o600, SHA256: sha256Hex([]byte(body)), Body: body}
}

func ds(nodeID string, epoch, rev int64, files []agentapi.File, services []agentapi.Service, checks []agentapi.Check) agentapi.DesiredState {
	d := agentapi.DesiredState{
		Epoch: epoch, NodeID: nodeID, RevisionID: rev,
		Files: files, Services: services, Checks: checks, IssuedAt: time.Now(),
	}
	h := ""
	for _, f := range files {
		h += f.SHA256
	}
	for _, s := range services {
		h += s.Name + s.Want
	}
	d.RevisionHash = sha256Hex([]byte(h))
	return d
}

func TestApplyWritesAndRestarts(t *testing.T) {
	r, root, fs, _, store := setup(t)
	p := filepath.Join(root, "sing-box.json")
	d := ds("n1", 1, 1,
		[]agentapi.File{mkFile(p, "config-v1")},
		[]agentapi.Service{{Name: "svc-a", Want: agentapi.WantRunning}},
		[]agentapi.Check{{Kind: agentapi.CheckSingBox, Path: p}})

	res := r.Apply(context.Background(), d)
	if res.Outcome != agentapi.OutcomeApplied || !res.Changed {
		t.Fatalf("want applied/changed, got %+v", res)
	}
	got, _ := os.ReadFile(p)
	if string(got) != "config-v1" {
		t.Errorf("file not written: %q", got)
	}
	if len(fs.restarted) != 1 || fs.restarted[0] != "svc-a" {
		t.Errorf("svc-a should be restarted, got %v", fs.restarted)
	}
	if store.AppliedRevision() != 1 || store.LastAcceptedEpoch() != 1 {
		t.Errorf("store not updated: rev=%d epoch=%d", store.AppliedRevision(), store.LastAcceptedEpoch())
	}
}

func TestIdempotentReapply(t *testing.T) {
	r, root, fs, _, _ := setup(t)
	p := filepath.Join(root, "c.json")
	d := ds("n1", 1, 1, []agentapi.File{mkFile(p, "x")}, []agentapi.Service{{Name: "svc-a", Want: agentapi.WantRunning}}, nil)
	r.Apply(context.Background(), d)
	fs.restarted = nil

	res := r.Apply(context.Background(), d)
	if res.Outcome != agentapi.OutcomeNoChange || res.Changed {
		t.Fatalf("want no-change, got %+v", res)
	}
	if len(fs.restarted) != 0 {
		t.Errorf("no restart expected on idempotent reapply, got %v", fs.restarted)
	}
}

func TestEpochFence(t *testing.T) {
	r, root, _, _, store := setup(t)
	p := filepath.Join(root, "c.json")
	r.Apply(context.Background(), ds("n1", 5, 1, []agentapi.File{mkFile(p, "a")}, nil, nil))

	// Lower epoch is a stale leader -> 409, no state change.
	res := r.Apply(context.Background(), ds("n1", 4, 2, []agentapi.File{mkFile(p, "b")}, nil, nil))
	if res.Outcome != agentapi.OutcomeStaleLeader {
		t.Fatalf("want stale-leader, got %+v", res)
	}
	if store.LastAcceptedEpoch() != 5 {
		t.Errorf("epoch must not regress, got %d", store.LastAcceptedEpoch())
	}
	if b, _ := os.ReadFile(p); string(b) != "a" {
		t.Errorf("stale leader must not change files, got %q", b)
	}

	// Higher epoch accepted.
	res = r.Apply(context.Background(), ds("n1", 6, 2, []agentapi.File{mkFile(p, "b")}, nil, nil))
	if res.Outcome != agentapi.OutcomeApplied {
		t.Fatalf("higher epoch should apply, got %+v", res)
	}
	if store.LastAcceptedEpoch() != 6 {
		t.Errorf("epoch should advance to 6, got %d", store.LastAcceptedEpoch())
	}
}

func TestPathAllowlist(t *testing.T) {
	r, _, _, _, _ := setup(t)
	res := r.Apply(context.Background(), ds("n1", 1, 1,
		[]agentapi.File{mkFile("/etc/passwd", "evil")}, nil, nil))
	if res.Outcome != agentapi.OutcomeRejected {
		t.Fatalf("path outside roots should be rejected, got %+v", res)
	}
}

func TestPayloadHashMismatch(t *testing.T) {
	r, root, _, _, _ := setup(t)
	p := filepath.Join(root, "c.json")
	d := ds("n1", 1, 1, []agentapi.File{mkFile(p, "x")}, nil, nil)
	d.Files[0].SHA256 = "deadbeef" // corrupt
	if res := r.Apply(context.Background(), d); res.Outcome != agentapi.OutcomeRejected {
		t.Fatalf("corrupt file hash should be rejected, got %+v", res)
	}
}

func TestNonMonotonicRevisionRejected(t *testing.T) {
	r, root, _, _, _ := setup(t)
	p := filepath.Join(root, "c.json")
	r.Apply(context.Background(), ds("n1", 1, 5, []agentapi.File{mkFile(p, "a")}, nil, nil))
	// New content but an older revision id = replay.
	res := r.Apply(context.Background(), ds("n1", 1, 4, []agentapi.File{mkFile(p, "b")}, nil, nil))
	if res.Outcome != agentapi.OutcomeRejected {
		t.Fatalf("non-monotonic revision with changed content should be rejected, got %+v", res)
	}
}

func TestCheckFailureRollsBack(t *testing.T) {
	r, root, _, fc, _ := setup(t)
	p := filepath.Join(root, "c.json")
	r.Apply(context.Background(), ds("n1", 1, 1, []agentapi.File{mkFile(p, "good")}, nil, nil))

	fc.failKind[agentapi.CheckSingBox] = true
	res := r.Apply(context.Background(), ds("n1", 2, 2,
		[]agentapi.File{mkFile(p, "bad")}, nil,
		[]agentapi.Check{{Kind: agentapi.CheckSingBox, Path: p}}))
	if res.Outcome != agentapi.OutcomeRolledBack {
		t.Fatalf("want rolled-back, got %+v", res)
	}
	if b, _ := os.ReadFile(p); string(b) != "good" {
		t.Errorf("file should be restored to 'good', got %q", b)
	}
}

func TestRestartFailureRollsBack(t *testing.T) {
	r, root, fs, _, _ := setup(t)
	p := filepath.Join(root, "c.json")
	r.Apply(context.Background(), ds("n1", 1, 1, []agentapi.File{mkFile(p, "good")}, nil, nil))

	fs.failRestart["svc-a"] = true
	res := r.Apply(context.Background(), ds("n1", 2, 2,
		[]agentapi.File{mkFile(p, "bad")},
		[]agentapi.Service{{Name: "svc-a", Want: agentapi.WantRunning}}, nil))
	if res.Outcome != agentapi.OutcomeRolledBack {
		t.Fatalf("want rolled-back, got %+v", res)
	}
	if b, _ := os.ReadFile(p); string(b) != "good" {
		t.Errorf("file should be restored, got %q", b)
	}
}

func TestServiceNotInAllowlist(t *testing.T) {
	r, root, _, _, _ := setup(t)
	p := filepath.Join(root, "c.json")
	res := r.Apply(context.Background(), ds("n1", 1, 1,
		[]agentapi.File{mkFile(p, "x")},
		[]agentapi.Service{{Name: "evil", Want: agentapi.WantRunning}}, nil))
	if res.Outcome != agentapi.OutcomeRejected {
		t.Fatalf("non-allowlisted service should be rejected, got %+v", res)
	}
}

func TestWantStopped(t *testing.T) {
	r, root, fs, _, _ := setup(t)
	p := filepath.Join(root, "c.json")
	res := r.Apply(context.Background(), ds("n1", 1, 1,
		[]agentapi.File{mkFile(p, "x")},
		[]agentapi.Service{{Name: "svc-b", Want: agentapi.WantStopped}}, nil))
	if res.Outcome != agentapi.OutcomeApplied {
		t.Fatalf("want applied, got %+v", res)
	}
	if len(fs.stopped) != 1 || fs.stopped[0] != "svc-b" {
		t.Errorf("svc-b should be stopped, got %v", fs.stopped)
	}
	if len(fs.restarted) != 0 {
		t.Errorf("stopped service should not be restarted, got %v", fs.restarted)
	}
}

func TestBootReapplyRewritesMissingFiles(t *testing.T) {
	r, root, _, _, store := setup(t)
	p := filepath.Join(root, "c.json")
	r.Apply(context.Background(), ds("n1", 1, 1, []agentapi.File{mkFile(p, "cached")}, nil, nil))

	// Simulate a reboot wiping the file; the agent should rewrite from cache.
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	// Reopen the store from disk to model a fresh agent process.
	store2, err := OpenStore(store.path)
	if err != nil {
		t.Fatal(err)
	}
	r2 := NewReconciler(Config{NodeID: "n1", Roots: []string{root}, ServiceAllowlist: []string{"svc-a"}}, store2, &fakeServices{failRestart: map[string]bool{}}, &fakeChecker{failKind: map[string]bool{}})
	res, ok := r2.ReapplyCached(context.Background())
	if !ok {
		t.Fatal("expected cached desired-state to be present")
	}
	if res.Outcome != agentapi.OutcomeApplied {
		t.Fatalf("boot reapply should rewrite the missing file, got %+v", res)
	}
	if b, _ := os.ReadFile(p); string(b) != "cached" {
		t.Errorf("file should be restored from cache, got %q", b)
	}
}

// TestBootReapplyStartsDeadServices covers the reboot-survival bug: files on
// disk are byte-identical to the cache (so Apply's file-diff short-circuits
// to OutcomeNoChange), but the service itself isn't running — e.g. it was
// never enabled at boot, or crashed. ReapplyCached must still notice and
// start it instead of silently doing nothing.
func TestBootReapplyStartsDeadServices(t *testing.T) {
	r, root, fs, _, store := setup(t)
	p := filepath.Join(root, "c.json")
	d := ds("n1", 1, 1, []agentapi.File{mkFile(p, "cached")},
		[]agentapi.Service{{Name: "svc-a", Want: agentapi.WantRunning}}, nil)
	r.Apply(context.Background(), d)
	fs.restarted = nil

	// Reopen the store from disk (fresh agent process) with a services
	// manager that reports svc-a as dead, e.g. never enabled / crashed.
	store2, err := OpenStore(store.path)
	if err != nil {
		t.Fatal(err)
	}
	fs2 := &deadServiceManager{fakeServices: &fakeServices{failRestart: map[string]bool{}}, dead: map[string]bool{"svc-a": true}}
	r2 := NewReconciler(Config{NodeID: "n1", Roots: []string{root}, ServiceAllowlist: []string{"svc-a"}}, store2, fs2, &fakeChecker{failKind: map[string]bool{}})

	res, ok := r2.ReapplyCached(context.Background())
	if !ok {
		t.Fatal("expected cached desired-state to be present")
	}
	if !res.Changed {
		t.Fatalf("boot reapply should report changed when it had to start a dead service, got %+v", res)
	}
	if len(fs2.restarted) != 1 || fs2.restarted[0] != "svc-a" {
		t.Errorf("svc-a should have been started on boot despite unchanged files, got %v", fs2.restarted)
	}
}

// TestNoOpApplyStillUpdatesCachedEpoch covers a real production bug: a
// controller push that bumps the epoch (e.g. after an HA promote) but
// carries identical file/service content took the OutcomeNoChange
// fast-path, which used to return before calling commitApplied — so
// LastAcceptedEpoch advanced but the boot-recovery cache's epoch didn't.
// The next reboot's ReapplyCached would then replay the stale-epoch cache
// and trip the epoch fence against the node's own accepted state,
// permanently blocking boot recovery until an unrelated config change
// happened to land. The cache must track every accepted epoch, not just
// ones that changed content.
func TestNoOpApplyStillUpdatesCachedEpoch(t *testing.T) {
	r, root, _, _, store := setup(t)
	p := filepath.Join(root, "c.json")
	svc := []agentapi.Service{{Name: "svc-a", Want: agentapi.WantRunning}}
	files := []agentapi.File{mkFile(p, "same")}

	r.Apply(context.Background(), ds("n1", 1, 1, files, svc, nil))
	if got := store.Cached().Epoch; got != 1 {
		t.Fatalf("cached epoch after first apply = %d, want 1", got)
	}

	// Same content, higher epoch: must take the no-change path but still
	// persist the new epoch into the cache.
	res := r.Apply(context.Background(), ds("n1", 2, 1, files, svc, nil))
	if res.Outcome != agentapi.OutcomeNoChange {
		t.Fatalf("want no-change, got %+v", res)
	}
	if got := store.Cached().Epoch; got != 2 {
		t.Fatalf("cached epoch after no-op higher-epoch apply = %d, want 2 (epoch/cache desync bug)", got)
	}

	// A fresh agent process booting now must not see its own cache as stale.
	store2, err := OpenStore(store.path)
	if err != nil {
		t.Fatal(err)
	}
	r2 := NewReconciler(Config{NodeID: "n1", Roots: []string{root}, ServiceAllowlist: []string{"svc-a"}}, store2, &fakeServices{failRestart: map[string]bool{}}, &fakeChecker{failKind: map[string]bool{}})
	res2, ok := r2.ReapplyCached(context.Background())
	if !ok {
		t.Fatal("expected cached desired-state")
	}
	if res2.Outcome == agentapi.OutcomeStaleLeader {
		t.Fatalf("boot reapply rejected its own cached state as stale-leader: %+v", res2)
	}
}

// TestReapplyCachedIgnoresClockSkew covers the other half of the reboot-
// recovery bug: MaxClockSkew exists to reject old/replayed controller
// pushes, but ReapplyCached replays the node's own already-validated cache,
// which is inherently "old" the moment more than MaxClockSkew has passed
// since the last real config change — an entirely normal, common situation
// for a stable node. A live Apply with a stale IssuedAt should still be
// rejected; a boot/self-heal ReapplyCached of the same payload must not be.
func TestReapplyCachedIgnoresClockSkew(t *testing.T) {
	root := t.TempDir()
	store, err := OpenStore(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	clock := func() time.Time { return now }
	fs := &fakeServices{failRestart: map[string]bool{}}
	fc := &fakeChecker{failKind: map[string]bool{}}
	r := NewReconciler(Config{
		NodeID: "n1", Roots: []string{root},
		ServiceAllowlist: []string{"svc-a"},
		MaxClockSkew:     10 * time.Minute,
		Now:              clock,
	}, store, fs, fc)

	p := filepath.Join(root, "c.json")
	d := ds("n1", 1, 1, []agentapi.File{mkFile(p, "x")},
		[]agentapi.Service{{Name: "svc-a", Want: agentapi.WantRunning}}, nil)
	d.IssuedAt = now
	if res := r.Apply(context.Background(), d); res.Outcome != agentapi.OutcomeApplied {
		t.Fatalf("seed apply should succeed, got %+v", res)
	}

	// An hour passes with the config perfectly stable (the common case for an
	// entry node) — well past MaxClockSkew, but nothing has changed.
	now = now.Add(time.Hour)

	// A fresh live push replaying the same old IssuedAt must still be rejected.
	if res := r.Apply(context.Background(), d); res.Outcome != agentapi.OutcomeRejected {
		t.Fatalf("live Apply with stale IssuedAt should be rejected, got %+v", res)
	}

	// But booting and replaying the cache must succeed despite the same age.
	store2, err := OpenStore(store.path)
	if err != nil {
		t.Fatal(err)
	}
	r2 := NewReconciler(Config{
		NodeID: "n1", Roots: []string{root},
		ServiceAllowlist: []string{"svc-a"},
		MaxClockSkew:     10 * time.Minute,
		Now:              clock,
	}, store2, &fakeServices{failRestart: map[string]bool{}}, &fakeChecker{failKind: map[string]bool{}})
	res, ok := r2.ReapplyCached(context.Background())
	if !ok {
		t.Fatal("expected cached desired-state")
	}
	if res.Outcome == agentapi.OutcomeRejected {
		t.Fatalf("boot reapply rejected its own stable cache as clock-skewed: %+v", res)
	}
}

// deadServiceManager reports the given services as inactive regardless of
// what fakeServices.Restart records, so ReapplyCached's verify step has
// something to detect.
type deadServiceManager struct {
	*fakeServices
	dead map[string]bool
}

func (d *deadServiceManager) Status(ctx context.Context, name string) (string, error) {
	if d.dead[name] {
		return "inactive", nil
	}
	return d.fakeServices.Status(ctx, name)
}
