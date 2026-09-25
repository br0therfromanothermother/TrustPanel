package bot

import (
	"context"
	"log"
	"strings"

	"trustpanel/internal/core/egress"
	"trustpanel/internal/core/journal"
	"trustpanel/internal/core/model"
)

// The bot is a second surface onto the same fleet, and the panel's Logs tab is
// where anyone goes to find out what happened. Until these calls existed the tab
// would hold only what the panel itself did, leaving a client deleted from a
// phone, a group moved to another exit or a rule switched off with no trace at
// all. Every write the bot makes writes a line here, filed in the namespace it
// belongs to, the way the panel files its own.

// record appends one line, best-effort: a logging failure must never undo or
// block the action that produced it. owner namespaces the line; "" is the admin
// namespace, which is where infra belongs too.
//
// Every line is marked as the bot's. The actor is the same account name the panel
// would write, so without the marker there would be nothing to tell an extension
// tapped on a phone from one typed into the panel, and that is usually the first
// thing anyone asks.
func (b *Bot) record(ctx context.Context, sev model.EventSeverity, msg journal.Msg, actor, owner string) {
	b.recordOp(ctx, sev, msg, actor, owner, "")
}

// recordOp is record for a line that is part of one operation. Draining a shared
// exit writes a line in every namespace whose group moved plus a summary of its
// own; opID makes that a set instead of a scatter of rows. It grants
// nothing: each line is still filed in, and read from, its own namespace.
func (b *Bot) recordOp(ctx context.Context, sev model.EventSeverity, msg journal.Msg, actor, owner, opID string) {
	line := journal.Join(msg, journal.Line(" (via Telegram)"))
	e := model.Event{
		Kind:     model.EventAdmin,
		Severity: sev,
		Message:  journal.Render(line, journal.Lang),
		Actor:    actor,
		OwnerID:  owner,
		OpID:     opID,
	}
	if ru := journal.Render(line, "ru"); ru != e.Message {
		e.MessageRU = ru
	}
	if err := b.store.AppendEvent(ctx, e); err != nil {
		log.Printf("bot: event log: %v", err)
	}
}

// recordFor files a line about the caller's own records, which is most of them:
// an operator's clients, groups and rules land in the operator's namespace, an
// admin's in the admin one. Infra (nodes) goes through record with "" instead.
func (b *Bot) recordFor(ctx context.Context, sev model.EventSeverity, msg journal.Msg, acct model.AdminAccount) {
	b.record(ctx, sev, msg, acct.Username, acct.Namespace())
}

// logEgressMoves writes one line per group that moved, in the namespace that
// owns it, so each operator sees their own groups move and nobody sees anybody
// else's names. The panel's own drain logs the same way (logEgressMoves there).
func (b *Bot) logEgressMoves(ctx context.Context, moves []egress.Move, reason journal.Msg, actor, opID string) {
	for _, m := range moves {
		b.recordOp(ctx, model.SeverityInfo,
			journal.Line("group %s (%s): egress %s → %s — %s", m.GroupName, m.GroupID, m.From, m.To, reason),
			actor, m.OwnerID, opID)
	}
}

// ref names a record the way the journal needs it: the name a person recognises
// and the id that survives a rename. "deleted client u-9f2" is a line nobody can
// match to anyone afterwards, which is why the panel writes the same shape.
func ref(name, id string) journal.Msg {
	if name = strings.TrimSpace(name); name == "" || name == id {
		return journal.Text(id)
	}
	return journal.Text(name + " (" + id + ")")
}

// expiryLine says where a client's access now ends and not by how much it
// just moved: two extensions in a row are two lines nobody can add up, and the
// date is the thing someone reading the log is actually after.
func expiryLine(u model.User) journal.Msg {
	who := ref(displayName(u), u.ID)
	if u.ExpiresAt == nil {
		return journal.Line("access for %s no longer expires", who)
	}
	return journal.Line("access for %s now ends %s", who, u.ExpiresAt.UTC().Format("2006-01-02"))
}

// nodeMaintLine reuses the panel's own two sentences for the same act, so a node
// drained from a phone and one drained from the panel read alike in the log.
func nodeMaintLine(n model.Node, drain bool) journal.Msg {
	if drain {
		return journal.Line("drained node %s", ref(n.Name, n.ID))
	}
	return journal.Line("resumed node %s", ref(n.Name, n.ID))
}

// enabledLine is the on/off line for a client, in the tense the log reads in.
func enabledLine(u model.User) journal.Msg {
	who := ref(displayName(u), u.ID)
	if u.Enabled {
		return journal.Line("client %s switched on", who)
	}
	return journal.Line("client %s switched off", who)
}
