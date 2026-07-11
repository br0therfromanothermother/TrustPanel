package model

import "time"

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
	Actor    string        `json:"actor,omitempty"`
	// OwnerID namespaces the event: "" is the admin namespace (infra + admin's own
	// clients); an operator username scopes the event to that operator. Each viewer
	// sees only their own namespace, so client identities never cross namespaces.
	OwnerID string `json:"owner_id,omitempty"`
}
