package panel

import (
	"context"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"trustpanel/internal/core/journal"
	"trustpanel/internal/core/model"
	"trustpanel/internal/core/watchdog"
)

// recordEvent appends one entry to the operator event log, best-effort: a logging
// failure must never break the action that produced the event. owner namespaces
// the entry ("" = admin namespace) so each viewer sees only their own events.
//
// The line is written in both languages the panel speaks, because the values
// inside it (a group's name, how many rules followed it, why it moved) are part
// of the sentence by the time it reaches the table and cannot be picked back out
// of it to translate.
func (p *Panel) recordEvent(ctx context.Context, kind model.EventKind, sev model.EventSeverity, msg journal.Msg, actor, owner string) {
	p.recordOpEvent(ctx, kind, sev, msg, actor, owner, "")
}

// recordOpEvent is recordEvent for a line that is part of one operation. One
// action can write a line in every namespace it touched plus a summary of its
// own; opID makes that a set instead of a scatter of rows an hour of
// timestamps apart. It grants nothing: each line is still filed in, and read
// from, its own namespace.
func (p *Panel) recordOpEvent(ctx context.Context, kind model.EventKind, sev model.EventSeverity, msg journal.Msg, actor, owner, opID string) {
	e := model.Event{Kind: kind, Severity: sev, Message: journal.Render(msg, journal.Lang), Actor: actor, OwnerID: owner, OpID: opID}
	if ru := journal.Render(msg, "ru"); ru != e.Message {
		e.MessageRU = ru
	}
	if err := p.store.AppendEvent(ctx, e); err != nil {
		log.Printf("event log: %v", err)
	}
}

// newOpID names one operation. The generator lives in model now, next to the
// field it fills, because the bot writes operations of its own.
func newOpID() string { return model.NewOpID() }

// eventAlerter is a watchdog.Alerter that persists every alert into the event log,
// so the Logs tab shows the same node-down/recovery/billing/replication notices
// that go to Telegram, with history. It is added alongside the other alerters.
type eventAlerter struct{ p *Panel }

// Alert records an alert that has already been rendered. A channel that hands
// over text has nothing left to translate, so the line is stored as it came.
// AlertLocalized below is the way in that keeps both languages.
func (a eventAlerter) Alert(ctx context.Context, sev watchdog.Severity, msg string) error {
	// The event log stores plain text; strip the Telegram HTML the alert carries.
	a.p.recordEvent(ctx, model.EventAlert, alertSeverity(sev, msg),
		journal.Text(watchdog.PlainText(msg)), "", "") // monitor alerts are infra (admin namespace)
	return nil
}

// alertLine is an alert on its way into the journal: the text the alerts already
// speak, in both languages, with the Telegram markup taken off.
func alertLine(msg watchdog.MsgFunc) journal.Msg {
	return journal.Both(
		watchdog.PlainText(watchdog.Render(msg, watchdog.DefaultLang)),
		watchdog.PlainText(watchdog.Render(msg, "ru")))
}

// AlertLocalized records an alert that can still render itself. The alert
// vocabulary already knows both languages; taking the message before it is
// flattened keeps a node-down line readable for an operator reading the
// journal in Russian.
func (a eventAlerter) AlertLocalized(ctx context.Context, sev watchdog.Severity, msg watchdog.MsgFunc) error {
	a.p.recordEvent(ctx, model.EventAlert, alertSeverity(sev, watchdog.Render(msg, watchdog.DefaultLang)),
		alertLine(msg), "", "")
	return nil
}

// alertSeverity tiers an alert for the journal. The watchdog has two levels,
// silent and loud, and "silent" covers both a recovery and a real warning, so
// read the glyph the message opens with: a recovery is news, not something to
// act on, and marking it a warning turns the log into a wall of amber.
func alertSeverity(sev watchdog.Severity, msg string) model.EventSeverity {
	switch {
	case sev == watchdog.SeverityCritical:
		return model.SeverityCrit
	case strings.HasPrefix(msg, watchdog.GlyphUp):
		return model.SeverityInfo
	}
	return model.SeverityWarn
}

// NewEventAlerter builds an alerter that records into the event log.
func (p *Panel) NewEventAlerter() watchdog.Alerter { return eventAlerter{p: p} }

func (p *Panel) handleListEvents(w http.ResponseWriter, r *http.Request) {
	kind := r.URL.Query().Get("kind")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	// (before_at, before) is where the caller's last page ended, so the journal can
	// be read page by page instead of in one capped block.
	before, _ := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
	beforeAt, _ := time.Parse(time.RFC3339Nano, r.URL.Query().Get("before_at"))
	events, err := p.store.ListEventsBefore(r.Context(), kind, p.account(r).Namespace(), limit, beforeAt, before)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if events == nil {
		events = []model.Event{}
	}
	localizeEvents(events, r, p)
	writeJSON(w, http.StatusOK, map[string]any{"events": events})
}

// handleEventOperation returns the lines one operation wrote. An action that
// touched several namespaces writes one line per namespace plus a summary, and
// the name of the operation lets a reader ask which
// belonged together.
//
// The scoping is the journal's own: a reader sees the lines of their own
// namespace. The reader who can already see every namespace's records sees them
// all, because for them an operation that reads as one line with the rest of
// itself unreachable is the thing this is fixing. Anyone else is told how many
// lines they are not being shown, a number their own summary line already states
// in words, and not what is in them.
func (p *Panel) handleEventOperation(w http.ResponseWriter, r *http.Request) {
	opID := strings.TrimSpace(r.URL.Query().Get("id"))
	if opID == "" {
		writeErr(w, http.StatusBadRequest, "an operation id is required")
		return
	}
	acct := p.account(r)
	events, hidden, err := p.store.ListEventsOfOp(r.Context(), opID, acct.Namespace(), p.seesAllNamespaces(r, acct))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if events == nil {
		events = []model.Event{}
	}
	localizeEvents(events, r, p)
	writeJSON(w, http.StatusOK, map[string]any{"events": events, "hidden": hidden})
}

// localizeEvents serves each line in the language the panel is being read in:
// the tab says which, and the account's saved choice answers for anything that
// asks without saying (an older page, a script).
func localizeEvents(events []model.Event, r *http.Request, p *Panel) {
	lang := r.URL.Query().Get("lang")
	if lang != "ru" && lang != "en" {
		lang = localeLang(p.account(r).Locale)
	}
	if lang != "ru" {
		return
	}
	for i := range events {
		if events[i].MessageRU != "" {
			events[i].Message = events[i].MessageRU
		}
	}
}
