package watchdog

import (
	"context"
	"fmt"
	"testing"
)

type scriptProber struct {
	results []error
	i       int
}

func (p *scriptProber) Probe(context.Context) error {
	if p.i >= len(p.results) {
		return nil
	}
	r := p.results[p.i]
	p.i++
	return r
}

type recordAlerter struct {
	msgs []string
	sevs []Severity
}

func (a *recordAlerter) Alert(_ context.Context, sev Severity, msg string) error {
	a.msgs = append(a.msgs, msg)
	a.sevs = append(a.sevs, sev)
	return nil
}

func down() error { return fmt.Errorf("down") }

func newWatcher(prober Prober, alerter Alerter, threshold int) *Watcher {
	return &Watcher{Target: "node2", Prober: prober, Alerter: alerter, Threshold: threshold}
}

func TestNoAlertBelowThreshold(t *testing.T) {
	a := &recordAlerter{}
	w := newWatcher(&scriptProber{results: []error{down(), down()}}, a, 3)
	w.Tick(context.Background())
	w.Tick(context.Background())
	if len(a.msgs) != 0 {
		t.Fatalf("no alert expected below threshold, got %v", a.msgs)
	}
}

func TestAlertOnceAtThreshold(t *testing.T) {
	a := &recordAlerter{}
	w := newWatcher(&scriptProber{results: []error{down(), down(), down(), down(), down()}}, a, 3)
	for i := 0; i < 5; i++ {
		w.Tick(context.Background())
	}
	if len(a.msgs) != 1 {
		t.Fatalf("expected exactly one DOWN alert (dedup), got %d: %v", len(a.msgs), a.msgs)
	}
	if got := a.msgs[0]; !contains(got, GlyphDown) || !contains(got, "node2") {
		t.Errorf("unexpected alert text: %q", got)
	}
	// The DOWN alert must carry the ready-to-run failover command (8B).
	if !contains(a.msgs[0], "trustpanel promote --pg-promote --start-serve") {
		t.Errorf("DOWN alert should include the promote command, got: %q", a.msgs[0])
	}
}

func TestRecoveryAlertAndReset(t *testing.T) {
	a := &recordAlerter{}
	// down x3 (alert), then healthy (recovered), then down x3 (alert again).
	w := newWatcher(&scriptProber{results: []error{down(), down(), down(), nil, down(), down(), down()}}, a, 3)
	for i := 0; i < 7; i++ {
		w.Tick(context.Background())
	}
	if len(a.msgs) != 3 {
		t.Fatalf("want DOWN, RECOVERED, DOWN (3 alerts), got %d: %v", len(a.msgs), a.msgs)
	}
	if !contains(a.msgs[0], GlyphDown) || !contains(a.msgs[1], GlyphUp) || !contains(a.msgs[2], GlyphDown) {
		t.Errorf("alert sequence wrong: %v", a.msgs)
	}
	// DOWN rings (critical), RECOVERED is silent (low).
	if a.sevs[0] != SeverityCritical || a.sevs[1] != SeverityLow || a.sevs[2] != SeverityCritical {
		t.Errorf("severity sequence wrong: %v", a.sevs)
	}
}

func TestHealthyNeverAlerts(t *testing.T) {
	a := &recordAlerter{}
	w := newWatcher(&scriptProber{results: []error{nil, nil, nil}}, a, 1)
	for i := 0; i < 3; i++ {
		w.Tick(context.Background())
	}
	if len(a.msgs) != 0 {
		t.Fatalf("healthy target must not alert, got %v", a.msgs)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
