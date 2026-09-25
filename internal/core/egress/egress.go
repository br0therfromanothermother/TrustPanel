// Package egress holds the one description of where a group's traffic leaves by,
// and of moving it when that exit has to go away.
//
// It lives here rather than inside the panel because the bot drains nodes too,
// and a drain that flips the maintenance flag and nothing else leaves every
// group that egressed
// through the node pointing at a node in maintenance. Rather than grow a second
// copy of the move for the bot to get subtly wrong, the move lives here and both
// callers use it.
package egress

import (
	"context"
	"fmt"
	"strings"

	"trustpanel/internal/core/journal"
	"trustpanel/internal/core/model"
)

// Store is the slice of the store a move needs.
type Store interface {
	LoadState(ctx context.Context) (model.State, error)
	UpsertGroup(ctx context.Context, g model.Group) error
}

// Move is one group actually changing the exit it leaves by. The log needs each
// one by name: "3 groups moved" does not say which three, and an operator
// putting them back has nothing to put back from.
type Move struct {
	GroupID, GroupName string
	OwnerID            string
	From, To           journal.Msg
}

// Dependents returns the groups that leave by nodeID, in state order.
func Dependents(st model.State, nodeID string) []model.Group {
	if nodeID == "" {
		return nil
	}
	var out []model.Group
	for _, g := range st.Groups {
		if g.DefaultExitID == nodeID {
			out = append(out, g)
		}
	}
	return out
}

// RulesNaming returns the enabled rules that send traffic to nodeID, in any of
// the three ways a rule can name an exit: where its own matched traffic goes,
// where the same traffic goes for everyone the rule is not reserved for, and
// where it falls back when its first choice is unavailable.
//
// These are not moved when the node is drained; each rule follows what it
// declares for its exit being out. Naming them is the difference between an
// operator knowing that and finding out afterwards.
func RulesNaming(st model.State, nodeID string) []model.RoutePolicy {
	if nodeID == "" {
		return nil
	}
	var out []model.RoutePolicy
	for _, p := range st.RoutePolicies {
		if p.Disabled {
			continue
		}
		switch {
		case p.Action == model.ActionExit && p.ExitNodeID == nodeID,
			p.OthersAction == model.ActionExit && p.OthersExitID == nodeID,
			p.FallbackKind == model.FallbackExit && p.FallbackExitID == nodeID:
			out = append(out, p)
		}
	}
	return out
}

// Switch sets DefaultExitID=exitID on the given groups (all groups when ids is
// empty) and returns the ones that actually changed.
func Switch(ctx context.Context, s Store, ids []string, exitID string) ([]Move, error) {
	st, err := s.LoadState(ctx)
	if err != nil {
		return nil, err
	}
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	var moved []Move
	for _, g := range st.Groups {
		if len(ids) > 0 && !want[g.ID] {
			continue
		}
		if g.DefaultExitID == exitID {
			continue
		}
		from := g.DefaultExitID
		g.DefaultExitID = exitID
		if err := s.UpsertGroup(ctx, g); err != nil {
			// What already moved is reported with the error: a run that stops
			// half way still has to leave a record of the half that happened.
			return moved, fmt.Errorf("group %s: %w", g.ID, err)
		}
		moved = append(moved, Move{
			GroupID: g.ID, GroupName: NameOr(g.Name, g.ID), OwnerID: g.OwnerID,
			From: Label(st, from), To: Label(st, exitID),
		})
	}
	return moved, nil
}

// Label names an exit the way a log line should read it: the node's name with
// its id, or what the empty and direct choices actually mean.
func Label(st model.State, id string) journal.Msg {
	switch id {
	case "":
		return journal.Line("no exit (out of the entry node)")
	case model.ExitDirect:
		return journal.Line("direct (out of the entry node)")
	}
	if n, ok := st.NodeByID(id); ok {
		return journal.Text(fmt.Sprintf("%s (%s)", NameOr(n.Name, n.ID), n.ID))
	}
	return journal.Text(id)
}

// NameOr falls back to the id when a record has no name worth printing.
func NameOr(name, id string) string {
	if strings.TrimSpace(name) == "" {
		return id
	}
	return name
}
