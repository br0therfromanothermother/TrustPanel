// Package authz is the single enforcement point for the admin/operator role
// model. Both the panel and the bot route their reads
// through ScopeState and their writes through the Can* gates, so the owner_id
// namespace rule lives in exactly one place — a new UI section or bot command
// cannot accidentally leak another account's clients by forgetting a filter.
//
// It is pure (operates on an already-loaded model.State plus the caller's
// account); the raw store stays the unscoped infra gateway used by reconcile,
// the agent, and the watchdog.
package authz

import "trustpanel/internal/core/model"

// ScopeState returns the portion of st the account may see.
//
//   - The bootstrap owner (infra-namespace admin) sees everything — it is the
//     see-all lens (the panel may default it to its own namespace via a filter).
//   - Any other account (a co-owner admin OR an operator) is scoped to its own
//     namespace for users, groups and exit-tier policies, plus all fleet/guard
//     policies (the infra baseline, shown read-only) so precedence routing is not
//     a black box. Other namespaces' clients are removed entirely — the visible
//     aggregate is computed separately (OthersAggregate) so no names leak.
//   - Shared infrastructure (nodes, domains, control plane) stays visible to a
//     co-owner admin (infra is shared "by agreement") but is NULLED for an
//     operator, which gets zero infra visibility beyond the health aggregate.
func ScopeState(st model.State, acct model.AdminAccount) model.State {
	return ScopeStateView(st, acct, true)
}

// ScopeStateView is ScopeState with an explicit see-all switch for the bootstrap
// owner. The panel defaults the bootstrap to its OWN namespace (seeAll=false):
// cross-namespace visibility is a deliberate, buried opt-in, so a co-owner never
// browses peers' clients by accident and the cockpit stays scoped to one tenant.
// seeAll only affects the bootstrap owner; every other account is always scoped
// to its namespace regardless.
func ScopeStateView(st model.State, acct model.AdminAccount, seeAll bool) model.State {
	if acct.IsBootstrapOwner() && seeAll {
		return st
	}
	me := acct.Namespace()
	out := st // copies header/value fields; slices are replaced below, not mutated
	out.Users = filterUsers(st.Users, me)
	out.Groups = filterGroups(st.Groups, me)
	out.RoutePolicies = filterPolicies(st.RoutePolicies, me)
	if !acct.CanManageInfra() { // operator: no infra surface at all
		out.Nodes = nil
		out.Domains = nil
		out.ControlPlane = model.ControlPlane{}
	}
	return out
}

func filterUsers(in []model.User, owner string) []model.User {
	out := make([]model.User, 0, len(in))
	for _, u := range in {
		if u.OwnerID == owner {
			out = append(out, u)
		}
	}
	return out
}

func filterGroups(in []model.Group, owner string) []model.Group {
	out := make([]model.Group, 0, len(in))
	for _, g := range in {
		if g.OwnerID == owner {
			out = append(out, g)
		}
	}
	return out
}

// filterPolicies keeps the operator's own exit-tier rules plus every fleet- and
// guard-tier rule (the infra baseline is inherited and visible, but read-only).
func filterPolicies(in []model.RoutePolicy, owner string) []model.RoutePolicy {
	out := make([]model.RoutePolicy, 0, len(in))
	for _, p := range in {
		if p.Tier == model.TierFleet || p.Tier == model.TierGuard || p.OwnerID == owner {
			out = append(out, p)
		}
	}
	return out
}

// Aggregate is the non-identifying summary of the clients an account may NOT see
// (the other namespaces): how many there are and whether any is currently
// enabled. It carries no usernames.
type Aggregate struct {
	OtherUsers int  `json:"other_users"`
	AnyOnline  bool `json:"any_online"`
}

// OthersAggregate summarizes the users an account does not own. For the bootstrap
// owner it is empty (it sees everyone directly). A scoped account — co-owner admin
// or operator — gets a non-identifying count of the other namespaces' clients.
// "AnyOnline" is approximated by "any enabled" here; a live-activity signal can
// refine it once node telemetry lands.
func OthersAggregate(st model.State, acct model.AdminAccount) Aggregate {
	return OthersAggregateView(st, acct, true)
}

// OthersAggregateView is OthersAggregate with the bootstrap see-all switch. When
// the bootstrap owner is scoped to its own namespace (seeAll=false) it still gets
// the non-identifying "N clients in other namespaces" count, so the buried
// cross-namespace lens advertises that hidden data exists without naming it.
func OthersAggregateView(st model.State, acct model.AdminAccount, seeAll bool) Aggregate {
	if acct.IsBootstrapOwner() && seeAll {
		return Aggregate{}
	}
	var agg Aggregate
	me := acct.Namespace()
	for _, u := range st.Users {
		if u.OwnerID == me {
			continue
		}
		agg.OtherUsers++
		if u.Enabled {
			agg.AnyOnline = true
		}
	}
	return agg
}

// CanManageInfra reports whether the account may touch shared-infra surfaces: HA/
// promote/standby, backups, global settings, bot tokens, node provisioning,
// domains, and infra-tier routing. Any admin (co-owners included) may; operators
// may not. Infra is shared "by agreement" (redesign decisions 2/3/10).
func CanManageInfra(acct model.AdminAccount) bool { return acct.CanManageInfra() }

// CanWriteOwned reports whether the account may create/edit/delete a resource
// owned by ownerID (a user, group, or exit-tier policy). The bootstrap owner may
// write anything; everyone else — co-owner admin or operator — only their own
// namespace. A new resource is owned by its creator's namespace, so callers pass
// the creator's namespace as ownerID for creates.
func CanWriteOwned(acct model.AdminAccount, ownerID string) bool {
	if acct.IsBootstrapOwner() {
		return true
	}
	return ownerID == acct.Namespace()
}

// CanManageMembers reports whether the account may add/remove accounts or change
// roles. Account/namespace minting and role changes are the bootstrap owner's
// exclusive surface (redesign decisions 7/11) — a scoped co-owner has no reason
// to mint tenants it cannot see. targetNS is unused (kept for call-site clarity).
func CanManageMembers(acct model.AdminAccount, targetNS string) bool {
	return acct.IsBootstrapOwner()
}

// CanWritePolicy gates a route policy edit: fleet-tier (mandates) and guard-tier
// rules are the shared infra baseline and follow CanManageInfra (any admin, "by
// agreement"); exit-tier rules follow namespace ownership.
func CanWritePolicy(acct model.AdminAccount, p model.RoutePolicy) bool {
	if p.Tier == model.TierFleet || p.Tier == model.TierGuard {
		return CanManageInfra(acct)
	}
	return CanWriteOwned(acct, p.OwnerID)
}

// CanWriteNode gates node lifecycle (settings/drain/decommission). Nodes are
// shared infra, so this follows CanManageInfra rather than per-node ownership:
// any admin may manage any node, an operator none. The node arg is retained for
// call-site clarity.
func CanWriteNode(acct model.AdminAccount, n model.Node) bool {
	return CanManageInfra(acct)
}

// CanWriteDomain gates a domain edit. A domain has no owner of its own — it is
// owned by the node it attaches to — so ownership follows that node: the fleet
// owner may write any domain; an operator only domains on a node in its
// namespace.
func CanWriteDomain(acct model.AdminAccount, hostNode model.Node) bool {
	return CanWriteNode(acct, hostNode)
}
