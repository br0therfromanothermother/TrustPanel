package model

import (
	"crypto/rand"
	"encoding/hex"
	"strconv"
	"time"
)

// EventKind classifies an entry in the operator event log.
type EventKind string

const (
	EventAlert  EventKind = "alert"  // watchdog/monitor alert (node down, billing, replication, bot)
	EventAdmin  EventKind = "admin"  // operator action (login changes, user/route/node edits)
	EventBackup EventKind = "backup" // backup/off-site delivery outcome
	EventSystem EventKind = "system" // panel lifecycle (started, reconcile error)
)

// EventSeverity tiers an event for filtering and colouring in the UI.
type EventSeverity string

const (
	SeverityInfo EventSeverity = "info"
	SeverityWarn EventSeverity = "warn"
	SeverityCrit EventSeverity = "critical"
)

// Event is one row in the persistent operator event log. It is intentionally
// flat (no structured payload): a human-readable message plus enough metadata to
// filter and colour it. Actor is the admin username for EventAdmin entries.
type Event struct {
	ID       int64         `json:"id"`
	At       time.Time     `json:"at"`
	Kind     EventKind     `json:"kind"`
	Severity EventSeverity `json:"severity"`
	Message  string        `json:"message"`
	// MessageRU is the same line written for a Russian-reading operator. It is
	// written when the event happens, because a sentence with names, counts and
	// reasons already set into it cannot be translated later; the panel serves
	// whichever of the two its reader asked for, so it never leaves here.
	MessageRU string `json:"-"`
	Actor     string `json:"actor,omitempty"`
	// OwnerID namespaces the event: "" is the admin namespace (infra + admin's own
	// clients); an operator username scopes the event to that operator. Each viewer
	// sees only their own namespace, so client identities never cross namespaces.
	OwnerID string `json:"owner_id,omitempty"`
	// OpID names the operation this line is part of, when it is part of one.
	// Draining a shared exit is one action that writes a line per group moved,
	// each into the namespace that owns the group, plus a summary line of its
	// own; they carry the same OpID so the set can be read as the one thing it
	// was. It grants no visibility: a line is still only readable from its own
	// namespace.
	OpID string `json:"op_id,omitempty"`
	// OpSize is how many lines that operation wrote in total, across every
	// namespace. It is read back with the row so the panel can tell an operation
	// worth opening from a single line that happens to have a name, and so a
	// reader who is shown only their own part is still told there is a whole.
	// It carries no content: the summary line already states a count in
	// words.
	OpSize int `json:"op_size,omitempty"`
}

// NewOpID names one operation. It is only ever compared for equality, so it has
// to be unguessable enough not to collide and short enough to read in a log.
func NewOpID() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		// A collision is worse than a long id; fall back to the clock, which is
		// still unique per operation on one host.
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return hex.EncodeToString(buf)
}
