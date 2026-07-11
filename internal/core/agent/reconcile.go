package agent

import (
	"context"
	"encoding/base64"
	"fmt"
	"log"
	"os"
	"sort"
	"sync"
	"time"

	"trustpanel/internal/core/agentapi"
)

// ServiceManager controls systemd units. The reconciler only ever touches
// units in its allowlist.
type ServiceManager interface {
	Restart(ctx context.Context, name string) error
	Stop(ctx context.Context, name string) error
	Status(ctx context.Context, name string) (string, error)
}

// Checker runs a post-write validation (e.g. `sing-box check`) against a file.
type Checker interface {
	Check(ctx context.Context, kind, absPath string) error
}

// Config configures a Reconciler.
type Config struct {
	NodeID           string
	Roots            []string // allowlisted file roots
	ServiceAllowlist []string
	MaxClockSkew     time.Duration // reject desired-state issued further in the past
	Now              func() time.Time
}

// Reconciler applies desired-state to the node. It is safe for concurrent use;
// Apply is serialized.
type Reconciler struct {
	cfg      Config
	store    *Store
	services ServiceManager
	checker  Checker
	allow    map[string]bool
	applyMu  sync.Mutex
}

// NewReconciler builds a reconciler. services/checker may be real or injected.
func NewReconciler(cfg Config, store *Store, services ServiceManager, checker Checker) *Reconciler {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	allow := map[string]bool{}
	for _, s := range cfg.ServiceAllowlist {
		allow[s] = true
	}
	return &Reconciler{
		cfg: cfg, store: store, services: services, checker: checker,
		allow: allow,
	}
}

// Apply reconciles the node to the desired-state. The payload must be
// freshly issued by the controller (subject to MaxClockSkew).
func (r *Reconciler) Apply(ctx context.Context, ds agentapi.DesiredState) agentapi.ReconcileResult {
	return r.apply(ctx, ds, false)
}

// apply is the shared implementation. skipClockSkew is set only by
// ReapplyCached: it is replaying the agent's own locally-cached, previously
// validated desired-state (not a fresh instruction from the controller), so
// the "issued too long ago" staleness check does not apply to it — that
// check exists to reject stale/replayed controller pushes, not to expire the
// node's own boot-recovery cache. The epoch fence still applies unchanged:
// it is what actually protects against a demoted controller regaining
// control.
func (r *Reconciler) apply(ctx context.Context, ds agentapi.DesiredState, skipClockSkew bool) agentapi.ReconcileResult {
	r.applyMu.Lock()
	defer r.applyMu.Unlock()

	// 1. Epoch fence: reject a controller older than the one we last saw.
	if ds.Epoch < r.store.LastAcceptedEpoch() {
		return r.result(agentapi.OutcomeStaleLeader, false, nil,
			fmt.Sprintf("epoch %d < last accepted %d", ds.Epoch, r.store.LastAcceptedEpoch()))
	}
	// The controller is current-or-newer; record the epoch even if the payload
	// is later rejected, so a stale leader cannot regain control afterwards.
	if err := r.store.bumpEpoch(ds.Epoch); err != nil {
		return r.result(agentapi.OutcomeRejected, false, nil, "persist epoch: "+err.Error())
	}

	if err := r.validate(ds, skipClockSkew); err != nil {
		return r.result(agentapi.OutcomeRejected, false, nil, err.Error())
	}

	// 2. Revision monotonicity / replay (different content with an old or equal
	// revision id is a replay attempt).
	if ds.RevisionHash != r.store.AppliedHash() && ds.RevisionID <= r.store.AppliedRevision() {
		return r.result(agentapi.OutcomeRejected, false, nil,
			fmt.Sprintf("non-monotonic revision %d <= applied %d with changed content", ds.RevisionID, r.store.AppliedRevision()))
	}

	// 3. Compute the set of files that actually differ on disk. This also makes
	// boot recovery work: if the cache hash matches but files drifted/are
	// missing, they are rewritten.
	changed, err := r.changedFiles(ds)
	if err != nil {
		return r.result(agentapi.OutcomeRejected, false, nil, err.Error())
	}
	if len(changed) == 0 {
		outcome := agentapi.OutcomeApplied
		if ds.RevisionHash == r.store.AppliedHash() {
			outcome = agentapi.OutcomeNoChange
		}
		// Re-cache even when content is unchanged: this is what keeps the
		// boot-recovery cache's epoch/issued-at in step with LastAcceptedEpoch.
		// Without it, an epoch bump that carries no file changes (e.g. a
		// promote/failover) is recorded in LastAcceptedEpoch but never reaches
		// the cache, so ReapplyCached's next boot replay permanently trips the
		// epoch fence against its own accepted state.
		if err := r.store.commitApplied(ds); err != nil {
			return r.result(agentapi.OutcomeRejected, false, nil, "persist applied: "+err.Error())
		}
		return r.result(outcome, false, nil, "")
	}

	// 4. Back up, then atomically write the changed files.
	backup := newBackupSet()
	for _, f := range changed {
		if err := backup.capture(f.abs); err != nil {
			return r.result(agentapi.OutcomeRejected, false, nil, "backup: "+err.Error())
		}
	}
	for _, f := range changed {
		if err := atomicWrite(f.abs, []byte(f.body), os.FileMode(f.mode)); err != nil {
			_ = backup.restore()
			return r.result(agentapi.OutcomeRolledBack, false, nil, "write: "+err.Error())
		}
	}

	// 5. Run checks; roll back on failure.
	for _, c := range ds.Checks {
		abs, err := resolveWithinRoots(r.cfg.Roots, c.Path)
		if err != nil {
			_ = backup.restore()
			return r.result(agentapi.OutcomeRolledBack, false, nil, "check path: "+err.Error())
		}
		if err := r.checker.Check(ctx, c.Kind, abs); err != nil {
			_ = backup.restore()
			return r.result(agentapi.OutcomeRolledBack, false, nil,
				fmt.Sprintf("check %s failed: %v", c.Kind, err))
		}
	}

	// 6. Restart changed running services / stop stopped ones; roll back on
	// failure (files only; services are left as systemd reports them).
	restarted, err := r.applyServices(ctx, ds.Services)
	if err != nil {
		_ = backup.restore()
		return r.result(agentapi.OutcomeRolledBack, false, nil, err.Error())
	}

	// 7. Commit applied state + cache.
	if err := r.store.commitApplied(ds); err != nil {
		_ = backup.restore()
		return r.result(agentapi.OutcomeRolledBack, false, nil, "persist applied: "+err.Error())
	}
	return r.result(agentapi.OutcomeApplied, true, restarted, "")
}

// ReapplyCached re-applies the cached desired-state, used on agent boot so the
// node converges without waiting for the panel. A reboot can leave a service
// dead (never enabled, crashed, etc.) while its config files on disk stay
// byte-identical to the cache — Apply's file-diff short-circuit would then
// return OutcomeNoChange without ever touching systemd. Boot recovery can't
// rely on that: it unconditionally verifies every service the cached
// desired-state wants running and starts whichever aren't active.
func (r *Reconciler) ReapplyCached(ctx context.Context) (agentapi.ReconcileResult, bool) {
	ds := r.store.Cached()
	if ds == nil {
		return agentapi.ReconcileResult{}, false
	}
	res := r.apply(ctx, *ds, true)
	if started := r.ensureServicesRunning(ctx, ds.Services); len(started) > 0 {
		res.Restarted = append(res.Restarted, started...)
		sort.Strings(res.Restarted)
		res.Restarted = dedupeSorted(res.Restarted)
		res.Changed = true
	}
	return res, true
}

// ensureServicesRunning starts any WantRunning service that isn't currently
// active, returning the names it had to start. Every check and every
// Restart failure is logged: this loop is the last line of defense that
// gets a node's data-plane services running again after a reboot, and its
// failures were previously silent — a node could stay dead indefinitely
// with zero trace of why.
func (r *Reconciler) ensureServicesRunning(ctx context.Context, services []agentapi.Service) []string {
	var started []string
	for _, s := range services {
		if s.Want != agentapi.WantRunning {
			continue
		}
		state, err := r.services.Status(ctx, s.Name)
		if err != nil {
			log.Printf("reconcile: status check for %s failed: %v", s.Name, err)
		} else if state == "active" {
			continue
		}
		if err := r.services.Restart(ctx, s.Name); err != nil {
			log.Printf("reconcile: restart %s failed (was %q): %v", s.Name, state, err)
			continue
		}
		log.Printf("reconcile: started %s (was %q)", s.Name, state)
		started = append(started, s.Name)
	}
	sort.Strings(started)
	return started
}

func dedupeSorted(ss []string) []string {
	out := ss[:0]
	for i, s := range ss {
		if i == 0 || s != ss[i-1] {
			out = append(out, s)
		}
	}
	return out
}

type resolvedFile struct {
	abs  string
	mode uint32
	body string
}

func (r *Reconciler) changedFiles(ds agentapi.DesiredState) ([]resolvedFile, error) {
	var out []resolvedFile
	for _, f := range ds.Files {
		abs, err := resolveWithinRoots(r.cfg.Roots, f.Path)
		if err != nil {
			return nil, err
		}
		cur, err := fileSHA256(abs)
		if err != nil {
			return nil, err
		}
		if cur == f.SHA256 {
			continue
		}
		mode := f.Mode
		if mode == 0 {
			mode = 0o600
		}
		body, err := fileBody(f)
		if err != nil {
			return nil, fmt.Errorf("file %q: %w", f.Path, err)
		}
		out = append(out, resolvedFile{abs: abs, mode: mode, body: string(body)})
	}
	return out, nil
}

// fileBody returns the decoded content of a desired-state file.
func fileBody(f agentapi.File) ([]byte, error) {
	if f.Encoding == "base64" {
		return base64.StdEncoding.DecodeString(f.Body)
	}
	return []byte(f.Body), nil
}

func (r *Reconciler) validate(ds agentapi.DesiredState, skipClockSkew bool) error {
	if ds.NodeID != r.cfg.NodeID {
		return fmt.Errorf("desired-state node_id %q != agent node %q", ds.NodeID, r.cfg.NodeID)
	}
	if !skipClockSkew && r.cfg.MaxClockSkew > 0 && !ds.IssuedAt.IsZero() {
		if age := r.cfg.Now().Sub(ds.IssuedAt); age > r.cfg.MaxClockSkew {
			return fmt.Errorf("desired-state too old: issued %s ago", age)
		}
	}
	for _, f := range ds.Files {
		if _, err := resolveWithinRoots(r.cfg.Roots, f.Path); err != nil {
			return err
		}
		body, err := fileBody(f)
		if err != nil {
			return fmt.Errorf("file %q: %w", f.Path, err)
		}
		if sha256Hex(body) != f.SHA256 {
			return fmt.Errorf("file %q sha256 mismatch", f.Path)
		}
	}
	for _, s := range ds.Services {
		if !r.allow[s.Name] {
			return fmt.Errorf("service %q not in allowlist", s.Name)
		}
		if s.Want != agentapi.WantRunning && s.Want != agentapi.WantStopped {
			return fmt.Errorf("service %q invalid want %q", s.Name, s.Want)
		}
	}
	return nil
}

func (r *Reconciler) applyServices(ctx context.Context, services []agentapi.Service) ([]string, error) {
	var restarted []string
	for _, s := range services {
		switch s.Want {
		case agentapi.WantRunning:
			if err := r.services.Restart(ctx, s.Name); err != nil {
				return restarted, fmt.Errorf("restart %s: %w", s.Name, err)
			}
			restarted = append(restarted, s.Name)
		case agentapi.WantStopped:
			if err := r.services.Stop(ctx, s.Name); err != nil {
				return restarted, fmt.Errorf("stop %s: %w", s.Name, err)
			}
		}
	}
	sort.Strings(restarted)
	return restarted, nil
}

func (r *Reconciler) result(outcome agentapi.Outcome, changed bool, restarted []string, errMsg string) agentapi.ReconcileResult {
	return agentapi.ReconcileResult{
		Outcome:           outcome,
		Changed:           changed,
		Restarted:         restarted,
		AppliedRevision:   r.store.AppliedRevision(),
		LastAcceptedEpoch: r.store.LastAcceptedEpoch(),
		Error:             errMsg,
	}
}
