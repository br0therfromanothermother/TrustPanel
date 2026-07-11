package authz

import (
	"testing"

	"trustpanel/internal/core/model"
)

func sampleState() model.State {
	return model.State{
		Nodes: []model.Node{
			{ID: "exit1", OwnerID: ""},      // admin/infra
			{ID: "exit-op", OwnerID: "op1"}, // operator-owned
		},
		Groups: []model.Group{
			{ID: "g-admin", OwnerID: ""},
			{ID: "g-op", OwnerID: "op1"},
			{ID: "g-op2", OwnerID: "op2"},
		},
		Users: []model.User{
			{ID: "u-admin", Username: "a", OwnerID: "", Enabled: true},
			{ID: "u-op", Username: "b", OwnerID: "op1", Enabled: true},
			{ID: "u-op2a", Username: "c", OwnerID: "op2", Enabled: false},
			{ID: "u-op2b", Username: "d", OwnerID: "op2", Enabled: true},
		},
		RoutePolicies: []model.RoutePolicy{
			{ID: "guard1", Tier: model.TierGuard, OwnerID: ""},
			{ID: "exit-admin", Tier: model.TierExit, OwnerID: ""},
			{ID: "exit-op", Tier: model.TierExit, OwnerID: "op1"},
			{ID: "exit-op2", Tier: model.TierExit, OwnerID: "op2"},
		},
	}
}

func ids[T any](xs []T, f func(T) string) []string {
	out := make([]string, len(xs))
	for i, x := range xs {
		out[i] = f(x)
	}
	return out
}

func TestScopeStateAdminSeesEverything(t *testing.T) {
	st := sampleState()
	admin := model.AdminAccount{Username: "boss", Role: model.RoleAdmin}
	got := ScopeState(st, admin)
	if len(got.Users) != 4 || len(got.Groups) != 3 || len(got.RoutePolicies) != 4 || len(got.Nodes) != 2 {
		t.Fatalf("admin should see all: %+v", got)
	}
}

func TestScopeStateViewBootstrapScopedByDefault(t *testing.T) {
	st := sampleState()
	// A bootstrap owner lives in the infra namespace ("").
	boot := model.AdminAccount{Username: "boss", Role: model.RoleAdmin}

	// seeAll=false is the panel default: the bootstrap owner is scoped to its own
	// (infra, "") namespace — it must NOT see the operators' clients/groups.
	scoped := ScopeStateView(st, boot, false)
	if u := ids(scoped.Users, func(u model.User) string { return u.ID }); len(u) != 1 || u[0] != "u-admin" {
		t.Fatalf("scoped bootstrap should see only its own users, got %v", u)
	}
	if g := ids(scoped.Groups, func(g model.Group) string { return g.ID }); len(g) != 1 || g[0] != "g-admin" {
		t.Fatalf("scoped bootstrap should see only its own groups, got %v", g)
	}
	// Infra stays visible (the bootstrap can manage infra even while scoped).
	if len(scoped.Nodes) != 2 {
		t.Fatalf("scoped bootstrap keeps infra nodes, got %d", len(scoped.Nodes))
	}
	// The hidden clients still surface as a non-identifying aggregate.
	if agg := OthersAggregateView(st, boot, false); agg.OtherUsers != 3 || !agg.AnyOnline {
		t.Fatalf("scoped bootstrap aggregate should count the 3 hidden clients, got %+v", agg)
	}

	// seeAll=true (lens on) restores the full cross-namespace view.
	if all := ScopeStateView(st, boot, true); len(all.Users) != 4 {
		t.Fatalf("bootstrap with lens on should see all users, got %d", len(all.Users))
	}
	if agg := OthersAggregateView(st, boot, true); agg != (Aggregate{}) {
		t.Fatalf("bootstrap with lens on has no hidden aggregate, got %+v", agg)
	}
}

func TestScopeStateOperatorSeesOwnPlusGuardNoInfra(t *testing.T) {
	st := sampleState()
	op := model.AdminAccount{Username: "op1", Role: model.RoleOperator, NamespaceID: "op1"}
	got := ScopeState(st, op)

	if u := ids(got.Users, func(u model.User) string { return u.ID }); len(u) != 1 || u[0] != "u-op" {
		t.Fatalf("operator should see only own users, got %v", u)
	}
	if g := ids(got.Groups, func(g model.Group) string { return g.ID }); len(g) != 1 || g[0] != "g-op" {
		t.Fatalf("operator should see only own groups, got %v", g)
	}
	// Own exit-tier + ALL guard-tier (read-only baseline), but not other operators'.
	pol := ids(got.RoutePolicies, func(p model.RoutePolicy) string { return p.ID })
	if len(pol) != 2 || !has(pol, "guard1") || !has(pol, "exit-op") {
		t.Fatalf("operator should see own exit + all guard, got %v", pol)
	}
	// Operators get ZERO infra visibility now: nodes, domains and the
	// control plane are nulled — the bot shows only a health aggregate.
	if len(got.Nodes) != 0 {
		t.Fatalf("operator should see no nodes, got %d", len(got.Nodes))
	}
	if len(got.Domains) != 0 {
		t.Fatalf("operator should see no domains, got %d", len(got.Domains))
	}
	if got.ControlPlane.ActiveNodeID != "" {
		t.Fatalf("operator should see no control plane, got %+v", got.ControlPlane)
	}
}

// TestScopeStateCoOwnerKeepsInfra: a co-owner admin (scoped, non-empty namespace)
// is scoped to its own clients/groups but KEEPS shared infra (nodes/domains/
// control plane) — infra is shared among admins.
func TestScopeStateCoOwnerKeepsInfra(t *testing.T) {
	st := sampleState()
	co := model.AdminAccount{Username: "lead", Role: model.RoleAdmin, NamespaceID: "op1"}
	got := ScopeState(st, co)
	if u := ids(got.Users, func(u model.User) string { return u.ID }); len(u) != 1 || u[0] != "u-op" {
		t.Fatalf("co-owner should see only own users, got %v", u)
	}
	if len(got.Nodes) != 2 {
		t.Fatalf("co-owner should keep all shared nodes, got %d", len(got.Nodes))
	}
}

func TestScopeStateDoesNotMutateInput(t *testing.T) {
	st := sampleState()
	op := model.AdminAccount{Username: "op1", Role: model.RoleOperator, NamespaceID: "op1"}
	_ = ScopeState(st, op)
	if len(st.Users) != 4 {
		t.Fatalf("ScopeState mutated its input: %d users left", len(st.Users))
	}
}

func TestOthersAggregateHidesNamesButCounts(t *testing.T) {
	st := sampleState()
	op := model.AdminAccount{Username: "op1", Role: model.RoleOperator, NamespaceID: "op1"}
	agg := OthersAggregate(st, op)
	// Others = u-admin, u-op2a, u-op2b (3); at least one enabled => online.
	if agg.OtherUsers != 3 || !agg.AnyOnline {
		t.Fatalf("aggregate wrong: %+v", agg)
	}
	if a := OthersAggregate(st, model.AdminAccount{Username: "boss", Role: model.RoleAdmin}); a != (Aggregate{}) {
		t.Fatalf("admin aggregate should be empty, got %+v", a)
	}
}

func TestWriteGates(t *testing.T) {
	admin := model.AdminAccount{Username: "boss", Role: model.RoleAdmin}
	op := model.AdminAccount{Username: "op1", Role: model.RoleOperator, NamespaceID: "op1"}

	if !CanManageInfra(admin) || CanManageInfra(op) {
		t.Fatal("infra management is admin-only")
	}
	if !CanWriteOwned(op, "op1") || CanWriteOwned(op, "op2") {
		t.Fatal("operator may write only its own namespace")
	}
	if !CanWriteOwned(admin, "op2") {
		t.Fatal("admin may write any namespace")
	}
	// Guard-tier policy is admin-only even though an operator owns nothing here.
	guard := model.RoutePolicy{Tier: model.TierGuard}
	if CanWritePolicy(op, guard) || !CanWritePolicy(admin, guard) {
		t.Fatal("guard-tier policy must be admin-only")
	}
	opExit := model.RoutePolicy{Tier: model.TierExit, OwnerID: "op1"}
	if !CanWritePolicy(op, opExit) {
		t.Fatal("operator may write its own exit-tier policy")
	}
}

func TestCanManageMembers(t *testing.T) {
	boot := model.AdminAccount{Username: "boss", Role: model.RoleAdmin} // ns "" = bootstrap owner
	coOwner := model.AdminAccount{Username: "lead", Role: model.RoleAdmin, NamespaceID: "team"}
	op := model.AdminAccount{Username: "op1", Role: model.RoleOperator, NamespaceID: "team"}

	// Account/member management is the bootstrap owner's exclusive surface
	// (decisions 7/11): a co-owner admin and an operator manage no members.
	if !CanManageMembers(boot, "") || !CanManageMembers(boot, "team") || !CanManageMembers(boot, "other") {
		t.Fatal("bootstrap owner must manage members in every namespace")
	}
	if CanManageMembers(coOwner, "team") || CanManageMembers(coOwner, "other") {
		t.Fatal("a co-owner admin must not manage members")
	}
	if CanManageMembers(op, "team") {
		t.Fatal("operator must not manage members")
	}

	// IsBootstrapOwner is true only for an admin in the infra namespace; a co-owner
	// admin shares infra (CanManageInfra) but is not the bootstrap owner; an
	// operator has neither.
	if coOwner.IsBootstrapOwner() || !boot.IsBootstrapOwner() {
		t.Fatal("IsBootstrapOwner must be true only for an admin in the infra namespace")
	}
	if !coOwner.CanManageInfra() || !boot.CanManageInfra() || op.CanManageInfra() {
		t.Fatal("CanManageInfra must be true for any admin and false for an operator")
	}
}

func TestMultiMemberNamespaceScoping(t *testing.T) {
	st := model.State{
		Users: []model.User{
			{ID: "u-team-a", OwnerID: "team", Enabled: true},
			{ID: "u-team-b", OwnerID: "team"},
			{ID: "u-other", OwnerID: "other", Enabled: true},
			{ID: "u-infra", OwnerID: ""},
		},
	}
	lead := model.AdminAccount{Username: "lead", Role: model.RoleAdmin, NamespaceID: "team"}
	member := model.AdminAccount{Username: "op1", Role: model.RoleOperator, NamespaceID: "team"}

	// Two accounts in the same namespace both see exactly that namespace's clients.
	for _, acct := range []model.AdminAccount{lead, member} {
		got := ScopeState(st, acct)
		seen := ids(got.Users, func(u model.User) string { return u.ID })
		if len(seen) != 2 || !has(seen, "u-team-a") || !has(seen, "u-team-b") {
			t.Fatalf("%s should see only team clients, got %v", acct.Username, seen)
		}
		// Neither sees the other namespace; the aggregate counts it without names.
		agg := OthersAggregate(st, acct)
		if agg.OtherUsers != 2 { // u-other + u-infra
			t.Fatalf("%s aggregate should hide 2 foreign clients, got %+v", acct.Username, agg)
		}
	}
}

func TestFleetTierIsInfraBaseline(t *testing.T) {
	fleet := model.AdminAccount{Username: "boss", Role: model.RoleAdmin} // ns ""
	op := model.AdminAccount{Username: "op1", Role: model.RoleOperator, NamespaceID: "op1"}

	mandate := model.RoutePolicy{ID: "m1", Tier: model.TierFleet, Action: model.ActionExit, ExitNodeID: "exit1", OwnerID: ""}
	// Only the fleet owner may write a fleet mandate; operators may not.
	if !CanWritePolicy(fleet, mandate) || CanWritePolicy(op, mandate) {
		t.Fatal("fleet mandates must be fleet-owner-only")
	}

	// An operator sees fleet (and guard) mandates read-only, alongside its own.
	st := model.State{RoutePolicies: []model.RoutePolicy{
		mandate,
		{ID: "g1", Tier: model.TierGuard, OwnerID: ""},
		{ID: "e-op", Tier: model.TierExit, OwnerID: "op1"},
		{ID: "e-op2", Tier: model.TierExit, OwnerID: "op2"},
	}}
	got := ScopeState(st, op)
	pol := ids(got.RoutePolicies, func(p model.RoutePolicy) string { return p.ID })
	if len(pol) != 3 || !has(pol, "m1") || !has(pol, "g1") || !has(pol, "e-op") || has(pol, "e-op2") {
		t.Fatalf("operator should see fleet+guard baseline plus own exit, got %v", pol)
	}
}

func has(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}
