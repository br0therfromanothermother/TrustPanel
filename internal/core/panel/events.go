package panel

import (
	"context"
	"log"
	"net/http"
	"strconv"

	"trustpanel/internal/core/model"
	"trustpanel/internal/core/watchdog"
)

// recordEvent appends one entry to the operator event log, best-effort: a logging
// failure must never break the action that produced the event. owner namespaces
// the entry ("" = admin namespace) so each viewer sees only their own events.
func (p *Panel) recordEvent(ctx context.Context, kind model.EventKind, sev model.EventSeverity, msg, actor, owner string) {
	if err := p.store.AppendEvent(ctx, model.Event{Kind: kind, Severity: sev, Message: msg, Actor: actor, OwnerID: owner}); err != nil {
		log.Printf("event log: %v", err)
	}
}

// eventAlerter is a watchdog.Alerter that persists every alert into the event log,
// so the Logs tab shows the same node-down/recovery/billing/replication notices
// that go to Telegram — with history. It is added alongside the other alerters.
type eventAlerter struct{ p *Panel }

func (a eventAlerter) Alert(ctx context.Context, sev watchdog.Severity, msg string) error {
	es := model.SeverityWarn
	if sev == watchdog.SeverityCritical {
		es = model.SeverityCrit
	}
	// The event log stores plain text; strip the Telegram HTML the alert carries.
	a.p.recordEvent(ctx, model.EventAlert, es, watchdog.PlainText(msg), "", "") // monitor alerts are infra (admin namespace)
	return nil
}

// NewEventAlerter builds an alerter that records into the event log.
func (p *Panel) NewEventAlerter() watchdog.Alerter { return eventAlerter{p: p} }

func (p *Panel) handleListEvents(w http.ResponseWriter, r *http.Request) {
	kind := r.URL.Query().Get("kind")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	events, err := p.store.ListEvents(r.Context(), kind, p.account(r).Namespace(), limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if events == nil {
		events = []model.Event{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events})
}
