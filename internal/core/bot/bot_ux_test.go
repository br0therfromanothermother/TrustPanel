package bot

import (
	"context"
	"strings"
	"testing"

	"trustpanel/internal/core/model"
)

// The add-user wizard offers the existing groups and expiry presets as buttons;
// tapping them feeds the same flow that typing does.
func TestAddUserPickButtons(t *testing.T) {
	fs := nsStore()
	b := New(fs, "token", "")
	ctx := context.Background()

	if got, _ := b.Callback(ctx, op, "m:adduser"); !strings.Contains(got, "step 1") {
		t.Fatalf("m:adduser should start the wizard: %q", got)
	}
	// Typing the username advances to the group step, whose reply carries group buttons.
	_, kb := b.route(ctx, op, "newclient")
	if !strings.Contains(kb, "aug:g-op") {
		t.Fatalf("group step should offer existing groups as buttons: %q", kb)
	}
	// Tap a group -> expiry presets.
	_, kb = b.Callback(ctx, op, "aug:g-op")
	if !strings.Contains(kb, "aue:0") || !strings.Contains(kb, "aue:30") {
		t.Fatalf("group pick should offer expiry presets: %q", kb)
	}
	// Tap "never" -> created with no expiry, in the chosen group, owned by op.
	if got, _ := b.Callback(ctx, op, "aue:0"); !strings.Contains(got, "Created") {
		t.Fatalf("expiry pick should create the user: %q", got)
	}
	last := fs.upsert[len(fs.upsert)-1]
	if last.Username != "newclient" || last.GroupID != "g-op" || last.OwnerID != "op1" || last.ExpiresAt != nil {
		t.Fatalf("created user wrong: %+v", last)
	}
}

// An admin builds a route from buttons: tier -> match -> action -> exit, and the
// rule lands on the chosen (fleet) tier.
func TestRouteWizardButtonsAdmin(t *testing.T) {
	fs := nsStore()
	b := New(fs, "token", "")
	ctx := context.Background()

	if _, kb := b.Callback(ctx, admin, "radd"); !strings.Contains(kb, "rt:fleet") {
		t.Fatalf("admin route wizard should start with a tier pick: %q", kb)
	}
	if _, kb := b.Callback(ctx, admin, "rt:fleet"); !strings.Contains(kb, "rmk:geosite") {
		t.Fatalf("tier pick should open the match builder: %q", kb)
	}
	if _, kb := b.Callback(ctx, admin, "rmk:geosite"); !strings.Contains(kb, "rqa:geosite:google") {
		t.Fatalf("geosite pick should offer curated quick-adds: %q", kb)
	}
	// One-tap a curated category, then continue from the hub.
	b.Callback(ctx, admin, "rqa:geosite:google")
	if _, kb := b.Callback(ctx, admin, "rback"); !strings.Contains(kb, "rcont") {
		t.Fatalf("done should return to the hub with a Continue: %q", kb)
	}
	if _, kb := b.Callback(ctx, admin, "rcont"); !strings.Contains(kb, "ra:exit") {
		t.Fatalf("continue should offer action buttons: %q", kb)
	}
	if _, kb := b.Callback(ctx, admin, "ra:exit"); !strings.Contains(kb, "rxp:exit1") {
		t.Fatalf("exit action should offer the exit nodes: %q", kb)
	}
	if _, kb := b.Callback(ctx, admin, "rxp:exit1"); !strings.Contains(kb, "rok") {
		t.Fatalf("exit pick should show the confirm step: %q", kb)
	}
	if got, _ := b.Callback(ctx, admin, "rok"); !strings.Contains(got, "created") {
		t.Fatalf("confirm should create the route: %q", got)
	}
	var found bool
	for _, p := range fs.state.RoutePolicies {
		if p.Tier == model.TierFleet && len(p.MatchGeoSite) == 1 && p.MatchGeoSite[0] == "google" && p.ExitNodeID == "exit1" {
			found = true
		}
	}
	if !found {
		t.Fatalf("fleet-tier route not created: %+v", fs.state.RoutePolicies)
	}
}

// A guard-tier rule cannot route to an exit, so the action keyboard hides the exit
// option and a typed "exit" is rejected.
func TestRouteWizardGuardNoExit(t *testing.T) {
	fs := nsStore()
	b := New(fs, "token", "")
	ctx := context.Background()
	b.Callback(ctx, admin, "radd")
	b.Callback(ctx, admin, "rt:guard")
	b.Callback(ctx, admin, "rmk:geosite")
	b.Callback(ctx, admin, "rqa:geosite:category-ads")
	b.Callback(ctx, admin, "rback") // value screen -> hub
	_, kb := b.Callback(ctx, admin, "rcont")
	if strings.Contains(kb, "ra:exit") {
		t.Fatalf("guard tier should not offer an exit action: %q", kb)
	}
	if got, _ := b.Callback(ctx, admin, "ra:exit"); !strings.Contains(got, "can't route to an exit") {
		t.Fatalf("guard+exit should be refused: %q", got)
	}
}

// Admins see the infra baseline as tappable cards (to manage it); operators see it
// read-only.
func TestRoutesInfraTappableForAdmin(t *testing.T) {
	fs := nsStore()
	b := New(fs, "token", "")
	ctx := context.Background()
	if _, kb := b.Callback(ctx, admin, "m:routes"); !strings.Contains(kb, "r:fleet1") {
		t.Fatalf("admin should be able to open the infra rule: %q", kb)
	}
	if _, kb := b.Callback(ctx, op, "m:routes"); strings.Contains(kb, "r:fleet1") {
		t.Fatalf("operator infra baseline should stay read-only: %q", kb)
	}
}

// Editing a route re-enters the wizard and upserts the same policy id.
func TestRouteEdit(t *testing.T) {
	fs := nsStore()
	b := New(fs, "token", "")
	ctx := context.Background()
	// Edit opens the builder hub pre-loaded with the rule's current match set.
	if got, kb := b.Callback(ctx, op, "redit:exit-op"); !strings.Contains(kb, "rmk:domain") || !strings.Contains(got, "netflix.com") {
		t.Fatalf("edit should open the builder pre-loaded with the current match: %q / %q", got, kb)
	}
	// Add a second domain to the existing one, then continue and save as direct.
	b.Callback(ctx, op, "rmk:domain")
	b.route(ctx, op, "youtube.com")
	b.Callback(ctx, op, "rback") // value screen -> hub
	b.Callback(ctx, op, "rcont") // hub -> action
	if got, _ := b.Callback(ctx, op, "ra:direct"); !strings.Contains(got, "Save these changes") {
		t.Fatalf("edit action should reach the confirm step: %q", got)
	}
	if got, _ := b.Callback(ctx, op, "rok"); !strings.Contains(got, "updated") {
		t.Fatalf("edit should update, not create: %q", got)
	}
	p, ok := policyByID(fs.state, "exit-op")
	if !ok || len(p.MatchDomains) != 2 || p.MatchDomains[0] != "netflix.com" || p.MatchDomains[1] != "youtube.com" || p.Action != model.ActionDirect {
		t.Fatalf("edited policy wrong: %+v", p)
	}
}

// The node card reveals addresses to admins and withholds them from operators.
func TestNodeCardAddresses(t *testing.T) {
	fs := nsStore()
	fs.state.Nodes[0].PublicIPs = []string{"203.0.113.7"}
	fs.state.Nodes[0].AgentAddr = "203.0.113.7:8443"
	b := New(fs, "token", "")
	ctx := context.Background()
	if got, _ := b.Callback(ctx, admin, "n:exit1"); !strings.Contains(got, "203.0.113.7") {
		t.Fatalf("admin node card should show addresses: %q", got)
	}
	if got, _ := b.Callback(ctx, op, "n:exit1"); strings.Contains(got, "203.0.113.7") {
		t.Fatalf("operator node card must not show addresses: %q", got)
	}
}

// Cancelling a guided flow from its inline button clears the conversation.
func TestWizardCancelButton(t *testing.T) {
	fs := nsStore()
	b := New(fs, "token", "")
	ctx := context.Background()
	b.Callback(ctx, op, "m:adduser")
	if b.convoFor(op) == nil {
		t.Fatalf("wizard should have armed a conversation")
	}
	b.Callback(ctx, op, "cx")
	if b.convoFor(op) != nil {
		t.Fatalf("cancel button should clear the conversation")
	}
}
