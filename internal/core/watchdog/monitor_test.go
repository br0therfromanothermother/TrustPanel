package watchdog

import (
	"context"
	"testing"
)

func lit(s string) MsgFunc { return func(string) string { return s } }

func obs(m *Monitor, key string, healthy bool) {
	m.Observe(context.Background(), key, healthy, SeverityCritical, lit("DOWN "+key), lit("UP "+key))
}

func TestMonitorThresholdDedupAndRecovery(t *testing.T) {
	a := &recordAlerter{}
	m := NewMonitor(a, 2)

	obs(m, "n1", false) // 1 fail < threshold -> no alert
	if len(a.msgs) != 0 {
		t.Fatalf("no alert expected below threshold, got %v", a.msgs)
	}
	obs(m, "n1", false) // 2nd fail -> DOWN
	obs(m, "n1", false) // still down -> dedup, no repeat
	if len(a.msgs) != 1 || a.msgs[0] != "DOWN n1" {
		t.Fatalf("want one DOWN n1, got %v", a.msgs)
	}
	if a.sevs[0] != SeverityCritical {
		t.Fatalf("DOWN should be critical, got %v", a.sevs[0])
	}
	obs(m, "n1", true) // recovery -> UP (silent)
	if len(a.msgs) != 2 || a.msgs[1] != "UP n1" {
		t.Fatalf("want recovery UP n1, got %v", a.msgs)
	}
	if a.sevs[1] != SeverityLow {
		t.Fatalf("recovery must be silent (low), got %v", a.sevs[1])
	}
}

func TestMonitorHealthyNeverAlerts(t *testing.T) {
	a := &recordAlerter{}
	m := NewMonitor(a, 1)
	for i := 0; i < 3; i++ {
		obs(m, "n1", true)
	}
	if len(a.msgs) != 0 {
		t.Fatalf("healthy target must not alert, got %v", a.msgs)
	}
}

func TestMonitorIndependentKeys(t *testing.T) {
	a := &recordAlerter{}
	m := NewMonitor(a, 1)
	obs(m, "a", false)
	obs(m, "b", false)
	if len(a.msgs) != 2 {
		t.Fatalf("want one alert per key, got %v", a.msgs)
	}
}

func TestMonitorForgetResetsState(t *testing.T) {
	a := &recordAlerter{}
	m := NewMonitor(a, 1)
	obs(m, "n1", false) // DOWN
	m.Forget(map[string]bool{})
	// After forgetting, the same key going down again must re-alert (fresh state).
	obs(m, "n1", false)
	if len(a.msgs) != 2 {
		t.Fatalf("want re-alert after Forget, got %v", a.msgs)
	}
}

func TestMonitorNilAlerterIsSafe(t *testing.T) {
	m := NewMonitor(nil, 1)
	obs(m, "n1", false) // must not panic
	obs(m, "n1", true)
}
