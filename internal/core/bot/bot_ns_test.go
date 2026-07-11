package bot

import (
	"context"
	"strings"
	"testing"

	"trustpanel/internal/core/model"
)

// nsStore builds a fakeStore with an operator (id 7, namespace "op1") that owns a
// group, a client and an exit-tier route, plus a fleet-tier infra policy and an
// exit node, and the bootstrap owner (id 42).
func nsStore() *fakeStore {
	return &fakeStore{
		state: model.State{
			ControlPlane: model.ControlPlane{ActiveNodeID: "exit1"},
			Nodes: []model.Node{
				{ID: "exit1", Name: "de-1", PublicRole: model.RoleExit, Health: model.HealthHealthy},
			},
			Groups: []model.Group{
				{ID: "g-op", Name: "op grp", OwnerID: "op1"},
				{ID: "g-other", Name: "other grp", OwnerID: "other"},
			},
			Users: []model.User{
				{ID: "u-op", Username: "opclient", GroupID: "g-op", OwnerID: "op1", Enabled: true},
				{ID: "u-other", Username: "otherclient", OwnerID: "other", Enabled: true},
			},
			RoutePolicies: []model.RoutePolicy{
				{ID: "fleet1", Name: "gov", Tier: model.TierFleet, Action: model.ActionExit, ExitNodeID: "exit1", MatchDomains: []string{"gov.example"}, OwnerID: ""},
				{ID: "exit-op", Name: "netflix", Tier: model.TierExit, Action: model.ActionDirect, MatchDomains: []string{"netflix.com"}, OwnerID: "op1"},
				{ID: "exit-other", Name: "x", Tier: model.TierExit, Action: model.ActionDirect, MatchDomains: []string{"x.com"}, OwnerID: "other"},
			},
		},
		accounts: map[int64]model.AdminAccount{
			42: {Username: "owner", Role: model.RoleAdmin, NamespaceID: ""},
			7:  {Username: "op1", Role: model.RoleOperator, NamespaceID: "op1"},
		},
	}
}

const op = int64(7)

func TestBotOperatorRootKeyboard(t *testing.T) {
	b := New(nsStore(), "token", "")
	ctx := context.Background()
	_, kb := b.menuRoot(ctx, op1Acct())
	if !strings.Contains(kb, "m:infra") || !strings.Contains(kb, "m:groups") || !strings.Contains(kb, "m:routes") || !strings.Contains(kb, "m:acct") {
		t.Fatalf("operator root must offer infra/groups/routes/account: %s", kb)
	}
	if strings.Contains(kb, "m:nodes") || strings.Contains(kb, "m:lens") {
		t.Fatalf("operator root must NOT offer nodes/lens: %s", kb)
	}
}

func op1Acct() model.AdminAccount {
	return model.AdminAccount{Username: "op1", Role: model.RoleOperator, NamespaceID: "op1"}
}

func TestBotGroupsScoping(t *testing.T) {
	b := New(nsStore(), "token", "")
	ctx := context.Background()
	// Operator sees only its own group as a button.
	_, kb := b.Callback(ctx, op, "m:groups")
	if !strings.Contains(kb, "g:g-op") || strings.Contains(kb, "g:g-other") {
		t.Fatalf("operator groups must be scoped to own: %s", kb)
	}
	// Deleting a group that has a client is blocked (the client u-op is in g-op).
	text, _ := b.Callback(ctx, op, "gdel:g-op")
	if !strings.Contains(text, "Cannot delete") {
		t.Fatalf("group with clients must not be deletable: %q", text)
	}
	// A forged delete of another namespace's group is refused (not found, scoped).
	text, _ = b.Callback(ctx, op, "gdz:g-other")
	if !strings.Contains(text, "No such group") {
		t.Fatalf("cross-namespace group delete must be refused: %q", text)
	}
}

func TestBotRoutesReadOnlyInfra(t *testing.T) {
	b := New(nsStore(), "token", "")
	ctx := context.Background()
	// The routes list shows the own exit route as a button and the fleet rule as a
	// read-only label (no button).
	text, kb := b.Callback(ctx, op, "m:routes")
	if !strings.Contains(kb, "r:exit-op") {
		t.Fatalf("own exit route should be a button: %s", kb)
	}
	if strings.Contains(kb, "r:fleet1") {
		t.Fatalf("fleet rule must not be a button: %s", kb)
	}
	if !strings.Contains(text, "Infra baseline") {
		t.Fatalf("fleet rule should appear as a read-only label: %q", text)
	}
	// A forged toggle/delete of the fleet rule is refused at the gate.
	if t2, _ := b.Callback(ctx, op, "rx:fleet1"); !strings.Contains(t2, "read-only") {
		t.Fatalf("operator toggling fleet rule must be refused: %q", t2)
	}
	if t2, _ := b.Callback(ctx, op, "rdz:fleet1"); !strings.Contains(t2, "read-only") {
		t.Fatalf("operator deleting fleet rule must be refused: %q", t2)
	}
}

func TestBotRouteWizard(t *testing.T) {
	fs := nsStore()
	b := New(fs, "token", "")
	ctx := context.Background()
	// Start -> the match-builder hub.
	if got, _ := b.Callback(ctx, op, "radd"); !strings.Contains(got, "Build the match") {
		t.Fatalf("route wizard start: %q", got)
	}
	// Pick geosite -> value screen with curated one-tap quick-adds.
	if _, kb := b.Callback(ctx, op, "rmk:geosite"); !strings.Contains(kb, "rqa:geosite:google") {
		t.Fatalf("geosite pick should offer curated quick-adds: %q", kb)
	}
	// Typing a category adds it and stays on the value screen.
	if got := b.Dispatch(ctx, op, "google"); !strings.Contains(got, "added: google") {
		t.Fatalf("typed value should be added: %q", got)
	}
	// Done -> back to the hub, which shows the accumulated match.
	if got, _ := b.Callback(ctx, op, "rback"); !strings.Contains(got, "geosite: google") {
		t.Fatalf("hub should show the accumulated match: %q", got)
	}
	// Continue -> action(exit) -> exit -> confirm.
	if got, _ := b.Callback(ctx, op, "rcont"); !strings.Contains(got, "Action") {
		t.Fatalf("continue should move to the action step: %q", got)
	}
	if got := b.Dispatch(ctx, op, "exit"); !strings.Contains(got, "de-1") {
		t.Fatalf("action step should list exits: %q", got)
	}
	if got := b.Dispatch(ctx, op, "1"); !strings.Contains(got, "Review") {
		t.Fatalf("exit pick should show the confirm summary: %q", got)
	}
	if got, _ := b.Callback(ctx, op, "rok"); !strings.Contains(got, "created") {
		t.Fatalf("confirm should create the route: %q", got)
	}
	// The new exit-tier route is owned by the operator namespace.
	var found bool
	for _, p := range fs.state.RoutePolicies {
		if p.Tier == model.TierExit && p.OwnerID == "op1" && len(p.MatchGeoSite) == 1 && p.MatchGeoSite[0] == "google" {
			found = true
			if p.Action != model.ActionExit || p.ExitNodeID != "exit1" {
				t.Fatalf("route action/exit wrong: %+v", p)
			}
		}
	}
	if !found {
		t.Fatalf("wizard did not persist the route: %+v", fs.state.RoutePolicies)
	}
}

// One rule can combine several match kinds (geosite AND geoip AND domain), built up
// from the hub with quick-adds and typing, just like the panel.
func TestRouteWizardCombinesKinds(t *testing.T) {
	fs := nsStore()
	b := New(fs, "token", "")
	ctx := context.Background()
	b.Callback(ctx, op, "radd")
	// geosite via a curated quick-add
	b.Callback(ctx, op, "rmk:geosite")
	b.Callback(ctx, op, "rqa:geosite:netflix")
	b.Callback(ctx, op, "rback")
	// geoip via a curated quick-add
	b.Callback(ctx, op, "rmk:geoip")
	b.Callback(ctx, op, "rqa:geoip:us")
	b.Callback(ctx, op, "rback")
	// a typed domain
	b.Callback(ctx, op, "rmk:domain")
	b.route(ctx, op, "example.com")
	// hub now shows all three kinds
	if got, _ := b.Callback(ctx, op, "rback"); !strings.Contains(got, "geosite: netflix") || !strings.Contains(got, "geoip: us") || !strings.Contains(got, "domains: example.com") {
		t.Fatalf("hub should combine all three kinds: %q", got)
	}
	b.Callback(ctx, op, "rcont")
	b.Callback(ctx, op, "ra:direct")
	if got, _ := b.Callback(ctx, op, "rok"); !strings.Contains(got, "created") {
		t.Fatalf("combined route should be created: %q", got)
	}
	var found bool
	for _, p := range fs.state.RoutePolicies {
		if p.Tier == model.TierExit && p.OwnerID == "op1" &&
			len(p.MatchGeoSite) == 1 && p.MatchGeoSite[0] == "netflix" &&
			len(p.MatchGeoIP) == 1 && p.MatchGeoIP[0] == "us" &&
			len(p.MatchDomains) == 1 && p.MatchDomains[0] == "example.com" {
			found = true
		}
	}
	if !found {
		t.Fatalf("combined route not persisted with all kinds: %+v", fs.state.RoutePolicies)
	}
}

// ✖ Cancel aborts the whole wizard; ⬅ Back steps back one screen keeping the draft.
func TestRouteWizardBackKeepsDraft(t *testing.T) {
	fs := nsStore()
	b := New(fs, "token", "")
	ctx := context.Background()
	b.Callback(ctx, op, "radd")
	b.Callback(ctx, op, "rmk:geosite")
	b.Callback(ctx, op, "rqa:geosite:google")
	// Back from the value screen returns to the hub with the value still there.
	if got, _ := b.Callback(ctx, op, "rback"); !strings.Contains(got, "geosite: google") {
		t.Fatalf("back should keep the draft: %q", got)
	}
	// Back from the hub (operator) leaves the wizard to the routes list and clears it.
	if got, _ := b.Callback(ctx, op, "rback"); !strings.Contains(got, "Routes") {
		t.Fatalf("back from the hub should return to routes: %q", got)
	}
	if b.convoFor(op) != nil {
		t.Fatalf("leaving the wizard should clear the conversation")
	}
}

// A route card shows its rank among peers and reorder swaps evaluation priority.
func TestRouteReorder(t *testing.T) {
	fs := nsStore()
	// Give the operator a second exit route (later priority) so reorder has a peer.
	fs.state.RoutePolicies = append(fs.state.RoutePolicies,
		model.RoutePolicy{ID: "exit-op2", Name: "hulu", Tier: model.TierExit, Action: model.ActionDirect, MatchDomains: []string{"hulu.com"}, OwnerID: "op1", Priority: 2},
	)
	b := New(fs, "token", "")
	ctx := context.Background()
	// exit-op (priority 0) is first of two: card shows the rank and only a ⬇ Down.
	got, kb := b.Callback(ctx, op, "r:exit-op")
	if !strings.Contains(got, "order: 1/2") {
		t.Fatalf("route card should show its rank: %q", got)
	}
	if !strings.Contains(kb, "rdn:exit-op") || strings.Contains(kb, "rup:exit-op") {
		t.Fatalf("first route should offer only ⬇ Down: %q", kb)
	}
	// Moving it down swaps priorities; now it is last and offers ⬆ Up.
	if _, kb := b.Callback(ctx, op, "rdn:exit-op"); !strings.Contains(kb, "rup:exit-op") {
		t.Fatalf("after moving down the route should offer ⬆ Up: %q", kb)
	}
	p1, _ := policyByID(fs.state, "exit-op")
	p2, _ := policyByID(fs.state, "exit-op2")
	if !(p2.Priority < p1.Priority) {
		t.Fatalf("reorder did not swap priorities: exit-op=%d exit-op2=%d", p1.Priority, p2.Priority)
	}
}

// The config card offers the QR-image and TOML-file export buttons, scoped to the
// caller's own clients.
func TestConfigCardButtons(t *testing.T) {
	b := New(nsStore(), "token", "")
	ctx := context.Background()
	_, kb := b.Callback(ctx, op, "cfg:u-op")
	if !strings.Contains(kb, "cfgqr:u-op") || !strings.Contains(kb, "cfgtoml:u-op") {
		t.Fatalf("config card should offer QR + TOML buttons: %q", kb)
	}
	// A cross-namespace client id resolves as not found (no export buttons).
	if _, kb := b.Callback(ctx, op, "cfg:u-other"); strings.Contains(kb, "cfgqr:") {
		t.Fatalf("operator must not export another namespace's client: %q", kb)
	}
}

func TestBotClientDeleteConfirm(t *testing.T) {
	fs := nsStore()
	b := New(fs, "token", "")
	ctx := context.Background()
	// Confirm screen carries the mutating token; the card does not delete directly.
	if _, kb := b.Callback(ctx, op, "udel:u-op"); !strings.Contains(kb, "udz:u-op") {
		t.Fatalf("delete confirm should carry udz token: %s", kb)
	}
	// Cross-namespace delete is refused (scoped lookup).
	if got, _ := b.Callback(ctx, op, "udz:u-other"); !strings.Contains(got, "No such user") {
		t.Fatalf("cross-namespace client delete must be refused: %q", got)
	}
	// Own delete succeeds.
	b.Callback(ctx, op, "udz:u-op")
	for _, u := range fs.state.Users {
		if u.ID == "u-op" {
			t.Fatalf("client should have been deleted: %+v", fs.state.Users)
		}
	}
}

func TestBotAccountScreen(t *testing.T) {
	fs := nsStore()
	b := New(fs, "token", "")
	ctx := context.Background()
	// Language change persists to the account.
	b.Callback(ctx, op, "lang:ru")
	if fs.accounts[op].Locale != "ru" {
		t.Fatalf("locale not saved: %+v", fs.accounts[op])
	}
	// alert:here binds this chat (the DM id == the sender id).
	b.Callback(ctx, op, "alert:here")
	if fs.accounts[op].AlertChatID != "7" {
		t.Fatalf("alert chat not bound: %+v", fs.accounts[op])
	}
	b.Callback(ctx, op, "alert:clear")
	if fs.accounts[op].AlertChatID != "" {
		t.Fatalf("alert chat not cleared: %+v", fs.accounts[op])
	}
}

func TestBotInfraAggregateNoNames(t *testing.T) {
	b := New(nsStore(), "token", "")
	ctx := context.Background()
	text, _ := b.Callback(ctx, op, "m:infra")
	if strings.Contains(text, "de-1") || strings.Contains(text, "exit1") {
		t.Fatalf("infra aggregate must not leak node names/ids: %q", text)
	}
	if !strings.Contains(text, "nodes:") {
		t.Fatalf("infra aggregate should report node counts: %q", text)
	}
}

func TestBotLensBootstrapOnly(t *testing.T) {
	b := New(nsStore(), "token", "")
	ctx := context.Background()
	// Operator forging m:lens is refused.
	if got, _ := b.Callback(ctx, op, "m:lens"); strings.Contains(got, "tap to inspect") {
		t.Fatalf("operator must not reach the lens: %q", got)
	}
	// Bootstrap owner (id 42) gets the namespace list.
	if got, _ := b.Callback(ctx, admin, "m:lens"); !strings.Contains(got, "tap to inspect") {
		t.Fatalf("bootstrap owner should see the lens: %q", got)
	}
}
