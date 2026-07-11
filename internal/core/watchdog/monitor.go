package watchdog

import (
	"context"
	"log"
	"sync"
)

// Monitor emits a single alert on each up→down and down→up transition for many
// named targets: it de-duplicates repeats and requires Threshold consecutive
// unhealthy samples before firing (anti-flap). It is the multi-key
// generalization of Watcher.Tick, used by the panel's background monitors (node
// liveness, entry edge, replication, bot health) and the watchdog's
// cross-observation checks. Recovery notices are always sent silently (Low);
// the DOWN severity is chosen by the caller. Safe for concurrent use.
type Monitor struct {
	alerter   Alerter
	threshold int

	mu      sync.Mutex
	fails   map[string]int
	alerted map[string]bool
}

// NewMonitor builds a Monitor delivering through a (nil-safe) alerter. threshold
// is the number of consecutive unhealthy samples required before a DOWN alert.
func NewMonitor(a Alerter, threshold int) *Monitor {
	if threshold < 1 {
		threshold = 1
	}
	return &Monitor{alerter: a, threshold: threshold, fails: map[string]int{}, alerted: map[string]bool{}}
}

// Observe records one health sample for key. After Threshold consecutive
// unhealthy samples it fires downMsg once (at downSev); when key becomes healthy
// again after having alerted it fires upMsg once (silently). Delivery happens
// outside the lock so a slow channel never blocks other observers.
func (m *Monitor) Observe(ctx context.Context, key string, healthy bool, downSev Severity, downMsg, upMsg MsgFunc) {
	m.mu.Lock()
	var (
		fireSev Severity
		fireMsg MsgFunc
		fire    bool
	)
	if !healthy {
		m.fails[key]++
		if m.fails[key] >= m.threshold && !m.alerted[key] {
			m.alerted[key] = true
			fireSev, fireMsg, fire = downSev, downMsg, true
		}
	} else {
		if m.alerted[key] {
			fireSev, fireMsg, fire = SeverityLow, upMsg, true
		}
		m.alerted[key] = false
		m.fails[key] = 0
	}
	m.mu.Unlock()
	if fire {
		m.send(ctx, fireSev, fireMsg)
	}
}

func (m *Monitor) send(ctx context.Context, sev Severity, msg MsgFunc) {
	if err := deliver(ctx, m.alerter, sev, msg); err != nil {
		log.Printf("monitor: alert delivery failed: %v", err)
	}
}

// Reset clears a key's failure/alert state WITHOUT sending any notice, so it
// re-arms cleanly. Used when a separate, higher-priority signal supersedes this
// key (e.g. the primary is already declared down, making the dead-man redundant):
// calling Observe(healthy=true) there would emit a spurious recovery notice for
// the superseded key.
func (m *Monitor) Reset(key string) {
	m.mu.Lock()
	delete(m.fails, key)
	delete(m.alerted, key)
	m.mu.Unlock()
}

// Forget drops tracking state for keys not present in keep, so a removed target
// that was down does not retain its alerted flag (a later re-add re-alerts
// cleanly) and stale state cannot linger.
func (m *Monitor) Forget(keep map[string]bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k := range m.fails {
		if !keep[k] {
			delete(m.fails, k)
			delete(m.alerted, k)
		}
	}
	for k := range m.alerted {
		if !keep[k] {
			delete(m.alerted, k)
		}
	}
}
