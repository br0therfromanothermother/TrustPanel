// Package watchdog implements the mutual watchdog: each exit-candidate
// watches the other and alerts an operator over a one-way channel when the
// watched node looks down, so the operator can run `trustpanel promote`. The
// watchdog never promotes automatically.
package watchdog

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"time"
)

// Prober reports the health of the watched target; nil means healthy.
type Prober interface {
	Probe(ctx context.Context) error
}

// Severity tiers an alert. Low alerts are delivered silently (Telegram
// disable_notification) — HA degraded, recovery notices, a stopped bot; Critical
// alerts ring — a node carrying traffic is down, the control plane needs a
// promote, or the alert system itself is degraded.
type Severity int

const (
	SeverityLow      Severity = iota // delivered silently (no sound)
	SeverityCritical                 // delivered with sound
)

// Silent reports whether this severity should suppress the notification sound.
func (s Severity) Silent() bool { return s == SeverityLow }

func (s Severity) label() string {
	if s == SeverityCritical {
		return "critical"
	}
	return "low"
}

// Alerter delivers an operator alert over a channel independent of the watched
// (and possibly co-located) services. sev selects sound vs silent delivery.
type Alerter interface {
	Alert(ctx context.Context, sev Severity, msg string) error
}

// LocalizedAlerter is an Alerter that can render a per-recipient message in each
// recipient's interface language (the Telegram fan-out implements it). Callers
// that have a MsgFunc prefer this so an operator reads alerts in their own
// language; an Alerter that does not implement it receives the DefaultLang
// rendering instead.
type LocalizedAlerter interface {
	AlertLocalized(ctx context.Context, sev Severity, msg MsgFunc) error
}

// deliver sends msg through a, using per-recipient localization when a supports
// it and the DefaultLang rendering otherwise. A nil alerter is a no-op.
func deliver(ctx context.Context, a Alerter, sev Severity, msg MsgFunc) error {
	if a == nil {
		return nil
	}
	if la, ok := a.(LocalizedAlerter); ok {
		return la.AlertLocalized(ctx, sev, msg)
	}
	return a.Alert(ctx, sev, Render(msg, DefaultLang))
}

// Watcher polls a Prober and alerts on sustained failure, with de-duplication
// and a recovery notice. It does not act on the fleet itself.
type Watcher struct {
	Target    string
	Prober    Prober
	Alerter   Alerter
	Threshold int
	Interval  time.Duration
	// PromoteCmd is the ready-to-run failover command put into the DOWN alert so
	// the operator can act without looking anything up. Empty uses the default
	// self-resolving promote (the binary fills in this node's id from agent.env).
	PromoteCmd string
	Now        func() time.Time

	consecFails int
	alerted     bool
}

// defaultPromoteCmd is the failover command shown in the DOWN alert. node-id is
// omitted on purpose: `promote` self-resolves it on the standby, so the operator
// pastes one line with nothing to fill in.
const defaultPromoteCmd = "sudo trustpanel promote --pg-promote --start-serve"

// Tick performs a single probe and evaluates alert/recovery transitions.
func (w *Watcher) Tick(ctx context.Context) {
	if w.Now == nil {
		w.Now = time.Now
	}
	if err := w.Prober.Probe(ctx); err != nil {
		w.consecFails++
		if w.consecFails >= w.Threshold && !w.alerted {
			w.alerted = true
			cmd := w.PromoteCmd
			if cmd == "" {
				cmd = defaultPromoteCmd
			}
			w.fire(ctx, SeverityCritical, MsgWatchdogDown(w.Target, w.consecFails, fmt.Sprint(err), cmd))
		}
		return
	}
	if w.alerted {
		w.fire(ctx, SeverityLow, MsgWatchdogUp(w.Target))
	}
	w.alerted = false
	w.consecFails = 0
}

// Down reports whether the watched target is currently in the alerted (down)
// state. Used to suppress the redundant dead-man check while the primary is
// already declared down. Call only from the same goroutine that drives Tick.
func (w *Watcher) Down() bool { return w.alerted }

func (w *Watcher) fire(ctx context.Context, sev Severity, msg MsgFunc) {
	if err := deliver(ctx, w.Alerter, sev, msg); err != nil {
		log.Printf("watchdog: alert delivery failed: %v", err)
	}
}

// HTTPProber probes an HTTP health endpoint; 2xx is healthy.
type HTTPProber struct {
	URL    string
	Client *http.Client
}

func (p HTTPProber) Probe(ctx context.Context) error {
	client := p.Client
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.URL, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("health %d", resp.StatusCode)
	}
	return nil
}

// TCPProber probes liveness by establishing a TCP connection. Useful for
// watching the active node's Postgres replication port from the standby, since
// the panel itself binds localhost only.
type TCPProber struct {
	Address string
	Timeout time.Duration
}

func (p TCPProber) Probe(ctx context.Context) error {
	timeout := p.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	d := net.Dialer{Timeout: timeout}
	conn, err := d.DialContext(ctx, "tcp", p.Address)
	if err != nil {
		return err
	}
	return conn.Close()
}

// LogAlerter writes alerts to the process log (fallback channel).
type LogAlerter struct{}

func (LogAlerter) Alert(_ context.Context, sev Severity, msg string) error {
	tag := "ALERT"
	if sev == SeverityCritical {
		tag = "ALERT(critical)"
	}
	log.Printf("%s: %s", tag, PlainText(msg)) // strip the Telegram HTML for the log
	return nil
}

// TelegramAlerter sends one-way alerts via a dedicated bot token (separate from
// the main service bot).
type TelegramAlerter struct {
	Token  string
	ChatID string
	Client *http.Client
}

func (a TelegramAlerter) Alert(ctx context.Context, sev Severity, msg string) error {
	client := a.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	api := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", a.Token)
	// Alert messages are built as Telegram HTML (see message.go) so node names,
	// commands and identifiers render as monospace code; every dynamic value in
	// those builders is escaped, so parse_mode=HTML is safe.
	form := url.Values{"chat_id": {a.ChatID}, "text": {msg}, "parse_mode": {"HTML"}}
	if sev.Silent() {
		form.Set("disable_notification", "true")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, api, bytes.NewBufferString(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("telegram sendMessage %d", resp.StatusCode)
	}
	return nil
}

// WebhookAlerter POSTs the alert as JSON to a generic webhook.
type WebhookAlerter struct {
	URL    string
	Client *http.Client
}

func (a WebhookAlerter) Alert(ctx context.Context, sev Severity, msg string) error {
	client := a.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	body, _ := json.Marshal(map[string]string{"text": msg, "severity": sev.label()})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook %d", resp.StatusCode)
	}
	return nil
}
