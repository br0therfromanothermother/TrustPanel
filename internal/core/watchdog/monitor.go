package watchdog

import (
	"context"
	"log"
	"sync"
	"time"
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
	minDown   time.Duration
	now       func() time.Time

	mu      sync.Mutex
	fails   map[string]int
	since   map[string]time.Time
	alerted map[string]bool
}

// NewMonitor builds a Monitor delivering through a (nil-safe) alerter. threshold
// is the number of consecutive unhealthy samples required before a DOWN alert.
func NewMonitor(a Alerter, threshold int) *Monitor {
	if threshold < 1 {
		threshold = 1
	}
	return &Monitor{
		alerter: a, threshold: threshold, now: time.Now,
		fails: map[string]int{}, since: map[string]time.Time{}, alerted: map[string]bool{},
	}
}

// RequireDownFor makes a target stay unhealthy for at least d before any DOWN
// alert is sent, and returns the monitor so it can be set up in one expression.
//
// A count of samples on its own does not say how long anything was broken: the
// same "two consecutive failures" is thirty-five seconds for a probe that runs
// every half minute and most of a day for one that runs every twelve hours. The
// edge probe was the half-minute kind, so a blip too short to notice produced a
// critical page saying clients could not connect and, twenty-five seconds later,
// a notice saying they could. Both were true, and together they were noise.
func (m *Monitor) RequireDownFor(d time.Duration) *Monitor {
	m.mu.Lock()
	m.minDown = d
	m.mu.Unlock()
	return m
}

// WithClock points the duration floor at another clock, so a caller that already
// has a controllable one (and its tests) does not end up with two notions of now.
func (m *Monitor) WithClock(now func() time.Time) *Monitor {
	if now == nil {
		return m
	}
	m.mu.Lock()
	m.now = now
	m.mu.Unlock()
	return m
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
		if m.since[key].IsZero() {
			m.since[key] = m.now()
		}
		longEnough := m.minDown <= 0 || m.now().Sub(m.since[key]) >= m.minDown
		if m.fails[key] >= m.threshold && longEnough && !m.alerted[key] {
			m.alerted[key] = true
			fireSev, fireMsg, fire = downSev, downMsg, true
		}
	} else {
		if m.alerted[key] {
			fireSev, fireMsg, fire = SeverityLow, upMsg, true
		}
		m.alerted[key] = false
		m.fails[key] = 0
		delete(m.since, key)
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

// Reset clears a key's failure/alert state without sending any notice, so it
// re-arms cleanly. Used when a separate, higher-priority signal supersedes this
// key (e.g. the primary is already declared down, making the dead-man redundant):
// calling Observe(healthy=true) there would emit a spurious recovery notice for
// the superseded key.
func (m *Monitor) Reset(key string) {
	m.mu.Lock()
	delete(m.fails, key)
	delete(m.since, key)
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
			delete(m.since, k)
			delete(m.alerted, k)
		}
	}
	for k := range m.since {
		if !keep[k] {
			delete(m.since, k)
		}
	}
	for k := range m.alerted {
		if !keep[k] {
			delete(m.alerted, k)
		}
	}
}
