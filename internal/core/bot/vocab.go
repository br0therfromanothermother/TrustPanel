package bot

import (
	"context"
	"time"

	"trustpanel/internal/core/model"
)

// The words the bot is allowed to use for a state, and the labels it is allowed
// to use for an action, in one place.
//
// Assembling a state per screen out of whatever fields are handy gives a list
// with a green dot for "enabled", a card printing "enabled: yes" under it, and a
// client whose access ran out last week showing both: green, enabled, and unable
// to connect. Words chosen per screen drift per screen, and the reader is left
// to work out which of them is the answer.
//
// So each state is named once, here, glyph and words together, and every surface
// prints that. The same goes for the actions: one label per action, the same in
// the menu, on the card and in a notification, because a reader recognises
// "🔗 Connection" far faster than they re-read it.

// accessState is the one answer to "can this client connect right now". The
// glyph is the same mark the list puts beside the name, so the list and the card
// cannot disagree.
type accessState struct {
	glyph string
	words string // English; translated where it is printed
}

var (
	accessLive    = accessState{"🟢", "Access is live"}
	accessOff     = accessState{"⏸", "Switched off"}
	accessExpired = accessState{"🔴", "Access has expired"}
)

// accessOf reads a client's state in the order it matters: an expired client
// cannot connect however enabled it is, which is exactly the case the old green
// dot got wrong.
func accessOf(u model.User, now time.Time) accessState {
	switch {
	case u.Expired(now):
		return accessExpired
	case !u.Enabled:
		return accessOff
	default:
		return accessLive
	}
}

// line is the status as a heading: the glyph and the words, never one alone.
func (a accessState) line(ctx context.Context) string { return a.glyph + " " + tr(ctx, a.words) }

// nodeState is the same idea for a server: what it is doing, in words a reader
// does not have to be told the meaning of. "healthy" and "in rotation" were
// neither, and "in rotation" is not even true of a server in maintenance.
type nodeState struct {
	glyph string
	words string
}

func nodeStateOf(n model.Node) nodeState {
	switch {
	case n.Maintenance:
		return nodeState{"⏸", "In maintenance"}
	case n.Health == model.HealthHealthy:
		return nodeState{"🟢", "Working"}
	default:
		return nodeState{"🔴", "Not answering"}
	}
}

func (s nodeState) line(ctx context.Context) string { return s.glyph + " " + tr(ctx, s.words) }

// nodeRole says what a server does for the network in words. A deployment of one
// server is one server doing both jobs, clients arriving on it and their traffic
// leaving from it, rather than a deployment missing its second one, which is
// what "role: entry" beside an empty exit list would suggest.
func nodeRole(ctx context.Context, fleet []model.Node, n model.Node) string {
	if n.IsEntry() {
		for _, x := range fleet {
			if x.IsExit() {
				return tr(ctx, "clients connect here")
			}
		}
		return tr(ctx, "clients connect here, and their traffic leaves from here")
	}
	if n.IsExit() {
		return tr(ctx, "traffic leaves through it")
	}
	return tr(ctx, "server")
}

// The labels. One action, one label, everywhere it appears.
const (
	lblClients     = "👤 Clients"
	lblGroups      = "👥 Groups"
	lblRoutes      = "🧭 Routes"
	lblServers     = "🌐 Servers"
	lblConnection  = "🔗 Connection"
	lblAccess      = "📅 Access period"
	lblSearch      = "🔎 Search"
	lblMaintenance = "🛠 Maintenance"
	lblNewClient   = "➕ New client"
	lblChangeGroup = "👥 Change group"
	lblMore        = "⋯ More"
	lblDelete      = "🗑 Delete"
	lblSwitchOn    = "▶️ Switch on"
	lblSwitchOff   = "⏸ Switch off"
)

// The four navigation words, each with exactly one meaning: Back steps to the
// previous screen, Cancel drops the draft, Done finishes a selection inside it,
// Save writes it. A green tick is not one of them: it means an action
// succeeded, and pinning it to "delete" or "switch off" makes the two read alike
// at the moment they must not.
const (
	lblBack   = "⬅ Back"
	lblCancel = "✖ Cancel"
	lblDone   = "✓ Done"
	lblSave   = "✓ Save"
)

// backTo is the one back button, named after where it goes.
func backTo(ctx context.Context, what, data string) []ikBtn {
	return []ikBtn{{"⬅ " + tr(ctx, what), data}}
}
