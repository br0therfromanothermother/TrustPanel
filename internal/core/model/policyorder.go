package model

import "sort"

// The order the nodes read routing rules in, written once.
//
// Written three times over (here in the renderer, again in the panel's
// JavaScript so the table can show the order, and once more in the bot) it only
// takes one of them backwards to turn "move up" into a move down, and two of
// three agreeing is no guarantee. A rule's precedence is not a matter of
// opinion, and an interface that shows a different order from the one the nodes
// enforce is worse than one that shows none.

// tierRank orders the tiers as the renderer emits them: the network's own rules
// first, then a scope's. An unknown tier sorts with the scope rules rather than
// jumping the queue.
func tierRank(t RuleTier) int {
	if t == TierFleet {
		return 0
	}
	return 1
}

// ComparePolicies reports which of two rules is evaluated first: negative when a
// is, positive when b is, zero when nothing distinguishes them.
//
// Within one tier a rule that also answers for everyone else leads. It says
// where a destination goes for the group it is reserved for and for everybody
// else, and a broader rule above it (a category that happens to list the same
// domain) would answer for everybody else first, which no priority number makes
// visible to whoever wrote the reservation. After that the higher priority wins,
// and an equal pair is settled by id so the order is at least stable.
func ComparePolicies(a, b RoutePolicy) int {
	if ra, rb := tierRank(a.Tier), tierRank(b.Tier); ra != rb {
		return ra - rb
	}
	if oa, ob := a.OthersAction != "", b.OthersAction != ""; oa != ob {
		if oa {
			return -1
		}
		return 1
	}
	if a.Priority != b.Priority {
		if a.Priority > b.Priority {
			return -1 // higher priority first
		}
		return 1
	}
	switch {
	case a.ID < b.ID:
		return -1
	case a.ID > b.ID:
		return 1
	}
	return 0
}

// SortPolicies puts rules in evaluation order, first one checked first.
func SortPolicies(ps []RoutePolicy) {
	sort.SliceStable(ps, func(i, j int) bool { return ComparePolicies(ps[i], ps[j]) < 0 })
}
