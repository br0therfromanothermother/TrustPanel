package bot

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"trustpanel/internal/core/authz"
	"trustpanel/internal/core/model"
)

// This file holds the namespace-product screens the bot grew when it became the
// operator's only surface: groups and
// exit-tier routing CRUD, the account screen, the operator infra-health aggregate,
// the destructive-action confirm gate, and the bootstrap owner's read-only lens.
// Every read goes through scoped(); every write re-checks the authz Can* gates, so
// a forged callback from a lower-privilege account is refused at the server, not
// just hidden in the keyboard.

// ---- groups ----

// menuGroups lists the caller's groups as buttons plus a create entry.
func (b *Bot) menuGroups(ctx context.Context, acct model.AdminAccount) (string, string) {
	st, err := b.scoped(ctx, acct)
	if err != nil {
		return errMsg(err), inlineKeyboard([][]ikBtn{backRow(ctx)})
	}
	groups := append([]model.Group(nil), st.Groups...)
	sort.Slice(groups, func(i, j int) bool { return groups[i].Name < groups[j].Name })
	var rows [][]ikBtn
	for _, g := range groups {
		rows = append(rows, []ikBtn{{"👥 " + g.Name, "g:" + g.ID}})
	}
	rows = append(rows, []ikBtn{{tr(ctx, "➕ New group"), "gadd"}})
	rows = append(rows, backRow(ctx))
	text := tr(ctx, "Groups — tap to manage")
	if len(groups) == 0 {
		text = tr(ctx, "No groups yet.")
	}
	return text, inlineKeyboard(rows)
}

// groupCard shows one group's detail with management buttons (scoped lookup).
func (b *Bot) groupCard(ctx context.Context, acct model.AdminAccount, id string) (string, string) {
	st, err := b.scoped(ctx, acct)
	if err != nil {
		return errMsg(err), inlineKeyboard([][]ikBtn{backRow(ctx)})
	}
	g, ok := groupByID(st, id)
	if !ok {
		return tr(ctx, "No such group."), inlineKeyboard([][]ikBtn{{{tr(ctx, "⬅ Groups"), "m:groups"}}})
	}
	exit := tr(ctx, "not set")
	if g.DefaultExitID != "" {
		exit = b.exitName(ctx, g.DefaultExitID)
	}
	n := clientsInGroup(st, g.ID)
	text := trf(ctx, "👥 %s\ndefault exit: %s\nclients in group: %d", g.Name, exit, n)
	rows := [][]ikBtn{
		{{tr(ctx, "✏️ Rename"), "grn:" + g.ID}},
		{{tr(ctx, "🎯 Default exit"), "gdef:" + g.ID}},
		{{tr(ctx, "🗑 Delete"), "gdel:" + g.ID}},
		{{tr(ctx, "⬅ Groups"), "m:groups"}},
	}
	return text, inlineKeyboard(rows)
}

// groupDefaultPicker offers the available exit nodes as the group's default exit.
func (b *Bot) groupDefaultPicker(ctx context.Context, acct model.AdminAccount, id string) (string, string) {
	st, err := b.scoped(ctx, acct)
	if err != nil {
		return errMsg(err), inlineKeyboard([][]ikBtn{backRow(ctx)})
	}
	g, ok := groupByID(st, id)
	if !ok || !authz.CanWriteOwned(acct, g.OwnerID) {
		return tr(ctx, "No such group."), inlineKeyboard([][]ikBtn{{{tr(ctx, "⬅ Groups"), "m:groups"}}})
	}
	var rows [][]ikBtn
	for _, n := range b.exitNodes(ctx) {
		rows = append(rows, []ikBtn{{"🚪 " + n.Name, "gdefp:" + g.ID + ":" + n.ID}})
	}
	rows = append(rows, []ikBtn{{tr(ctx, "✖ Clear default"), "gdefp:" + g.ID + ":-"}})
	rows = append(rows, []ikBtn{{tr(ctx, "⬅ Back"), "g:" + g.ID}})
	return trf(ctx, "🎯 Default exit for %s", g.Name), inlineKeyboard(rows)
}

// groupSetDefault assigns (or clears, "-") a group's default exit.
func (b *Bot) groupSetDefault(ctx context.Context, acct model.AdminAccount, payload string) (string, string) {
	gid, exitID, ok := strings.Cut(payload, ":")
	if !ok {
		return tr(ctx, "Unknown action. Tap ⬅ Menu."), inlineKeyboard([][]ikBtn{backRow(ctx)})
	}
	st, err := b.scoped(ctx, acct)
	if err != nil {
		return errMsg(err), inlineKeyboard([][]ikBtn{backRow(ctx)})
	}
	g, ok := groupByID(st, gid)
	if !ok || !authz.CanWriteOwned(acct, g.OwnerID) {
		return tr(ctx, "No such group."), inlineKeyboard([][]ikBtn{{{tr(ctx, "⬅ Groups"), "m:groups"}}})
	}
	if exitID == "-" {
		g.DefaultExitID = ""
	} else {
		g.DefaultExitID = exitID
	}
	if err := b.store.UpsertGroup(ctx, g); err != nil {
		return errMsg(err), inlineKeyboard([][]ikBtn{backRow(ctx)})
	}
	return b.groupCard(ctx, acct, gid)
}

// startNewGroup begins the one-step "name a group" wizard.
func (b *Bot) startNewGroup(ctx context.Context, fromID int64) string {
	b.startConvo(fromID, &convo{kind: kNewGroup, step: 0})
	return tr(ctx, "➕ New group.\nSend a name.")
}

func (b *Bot) advanceNewGroup(ctx context.Context, acct model.AdminAccount, fromID int64, c *convo, input string) string {
	name := strings.TrimSpace(input)
	if name == "" {
		return tr(ctx, "Reply with a group name, or /cancel.")
	}
	g := model.Group{ID: slugID("g", name), Name: name, OwnerID: ownerFor(acct)}
	if err := g.Validate(); err != nil {
		return "⚠️ " + err.Error() + tr(ctx, "\nReply with another name, or /cancel.")
	}
	b.clearConvo(fromID)
	if err := b.store.UpsertGroup(ctx, g); err != nil {
		return errMsg(err)
	}
	return trf(ctx, "✅ Group %q created. Open /menu → Groups to manage it.", name)
}

// startRenameGroup begins the rename wizard after an ownership check.
func (b *Bot) startRenameGroup(ctx context.Context, acct model.AdminAccount, fromID int64, id string) (string, string) {
	st, err := b.scoped(ctx, acct)
	if err != nil {
		return errMsg(err), inlineKeyboard([][]ikBtn{backRow(ctx)})
	}
	g, ok := groupByID(st, id)
	if !ok || !authz.CanWriteOwned(acct, g.OwnerID) {
		return tr(ctx, "No such group."), inlineKeyboard([][]ikBtn{{{tr(ctx, "⬅ Groups"), "m:groups"}}})
	}
	b.startConvo(fromID, &convo{kind: kRenameGroup, step: 0, groupID: id})
	return trf(ctx, "✏️ Renaming %q.\nSend the new name.", g.Name),
		inlineKeyboard([][]ikBtn{cancelRow(ctx)})
}

func (b *Bot) advanceRenameGroup(ctx context.Context, acct model.AdminAccount, fromID int64, c *convo, input string) string {
	name := strings.TrimSpace(input)
	if name == "" {
		return tr(ctx, "Reply with a new name, or /cancel.")
	}
	st, err := b.scoped(ctx, acct)
	if err != nil {
		b.clearConvo(fromID)
		return errMsg(err)
	}
	g, ok := groupByID(st, c.groupID)
	if !ok || !authz.CanWriteOwned(acct, g.OwnerID) {
		b.clearConvo(fromID)
		return tr(ctx, "No such group.")
	}
	g.Name = name
	if err := g.Validate(); err != nil {
		return "⚠️ " + err.Error() + tr(ctx, "\nReply with another name, or /cancel.")
	}
	b.clearConvo(fromID)
	if err := b.store.UpsertGroup(ctx, g); err != nil {
		return errMsg(err)
	}
	return trf(ctx, "✅ Group renamed to %q.", name)
}

// confirmDeleteGroup renders the confirm screen, or refuses when the group still
// has clients (deleting it would orphan them).
func (b *Bot) confirmDeleteGroup(ctx context.Context, acct model.AdminAccount, id string) (string, string) {
	st, err := b.scoped(ctx, acct)
	if err != nil {
		return errMsg(err), inlineKeyboard([][]ikBtn{backRow(ctx)})
	}
	g, ok := groupByID(st, id)
	if !ok || !authz.CanWriteOwned(acct, g.OwnerID) {
		return tr(ctx, "No such group."), inlineKeyboard([][]ikBtn{{{tr(ctx, "⬅ Groups"), "m:groups"}}})
	}
	if n := clientsInGroup(st, g.ID); n > 0 {
		return trf(ctx, "🚫 Cannot delete %q: %d client(s) still use it. Move them first.", g.Name, n),
			inlineKeyboard([][]ikBtn{{{tr(ctx, "⬅ Back"), "g:" + g.ID}}})
	}
	return trf(ctx, "⚠️ Delete group %q?", g.Name),
		inlineKeyboard([][]ikBtn{{{tr(ctx, "✅ Yes, delete"), "gdz:" + g.ID}, {tr(ctx, "✖ Cancel"), "g:" + g.ID}}})
}

func (b *Bot) groupDelete(ctx context.Context, acct model.AdminAccount, id string) (string, string) {
	st, err := b.scoped(ctx, acct)
	if err != nil {
		return errMsg(err), inlineKeyboard([][]ikBtn{backRow(ctx)})
	}
	g, ok := groupByID(st, id)
	if !ok || !authz.CanWriteOwned(acct, g.OwnerID) {
		return tr(ctx, "No such group."), inlineKeyboard([][]ikBtn{{{tr(ctx, "⬅ Groups"), "m:groups"}}})
	}
	if clientsInGroup(st, g.ID) > 0 { // re-check (defence vs a stale confirm)
		return b.confirmDeleteGroup(ctx, acct, id)
	}
	if err := b.store.DeleteGroup(ctx, g.ID); err != nil {
		return errMsg(err), inlineKeyboard([][]ikBtn{backRow(ctx)})
	}
	return b.menuGroups(ctx, acct)
}

// ---- routes (exit tier) ----

// menuRoutes lists the caller's own exit-tier routes as buttons, plus the
// fleet/guard infra baseline as read-only labels (so precedence is visible, not a
// black box).
func (b *Bot) menuRoutes(ctx context.Context, acct model.AdminAccount) (string, string) {
	st, err := b.scoped(ctx, acct)
	if err != nil {
		return errMsg(err), inlineKeyboard([][]ikBtn{backRow(ctx)})
	}
	var mine, infra []model.RoutePolicy
	for _, p := range st.RoutePolicies {
		if p.Tier == model.TierExit {
			mine = append(mine, p)
		} else {
			infra = append(infra, p)
		}
	}
	sort.Slice(mine, func(i, j int) bool { return mine[i].Priority < mine[j].Priority })
	sort.Slice(infra, func(i, j int) bool { return infra[i].Priority < infra[j].Priority })

	var sb strings.Builder
	sb.WriteString(tr(ctx, "🧭 Routes"))
	var rows [][]ikBtn
	for _, p := range mine {
		rows = append(rows, []ikBtn{{routeToggleIcon(p) + " " + routeLabel(ctx, b, p), "r:" + p.ID}})
	}
	if len(mine) == 0 {
		sb.WriteString(tr(ctx, "\n(no routes of your own yet)"))
	}
	if len(infra) > 0 {
		if acct.CanManageInfra() {
			// Admins own the baseline too, so list it as tappable cards to manage.
			sb.WriteString(tr(ctx, "\n\nInfra baseline:"))
			for _, p := range infra {
				rows = append(rows, []ikBtn{{"🔒 " + string(p.Tier) + " " + routeToggleIcon(p) + " " + routeLabel(ctx, b, p), "r:" + p.ID}})
			}
		} else {
			sb.WriteString(tr(ctx, "\n\nInfra baseline (read-only):"))
			for _, p := range infra {
				sb.WriteString("\n🔒 " + string(p.Tier) + ": " + routeLabel(ctx, b, p))
			}
		}
	}
	rows = append(rows, []ikBtn{{tr(ctx, "➕ New route"), "radd"}})
	rows = append(rows, backRow(ctx))
	return sb.String(), inlineKeyboard(rows)
}

// routeCard shows one route's detail; edit buttons appear only when writable.
func (b *Bot) routeCard(ctx context.Context, acct model.AdminAccount, id string) (string, string) {
	st, err := b.scoped(ctx, acct)
	if err != nil {
		return errMsg(err), inlineKeyboard([][]ikBtn{backRow(ctx)})
	}
	p, ok := policyByID(st, id)
	if !ok {
		return tr(ctx, "No such route."), inlineKeyboard([][]ikBtn{{{tr(ctx, "⬅ Routes"), "m:routes"}}})
	}
	status := tr(ctx, "on")
	if p.Disabled {
		status = tr(ctx, "off")
	}
	text := trf(ctx, "🧭 %s\nmatch: %s\naction: %s\nstatus: %s",
		p.Name, routeMatchText(p), b.routeActionText(ctx, p), status)
	if p.Tier != model.TierExit {
		text += trf(ctx, "\ntier: %s", string(p.Tier))
	}
	// Precedence is not a black box: show where this rule sits among its peers
	// (earlier = higher priority when two rules both match).
	peers := writablePeers(st, acct, p.Tier)
	pos := indexOfPolicy(peers, p.ID)
	if len(peers) > 1 && pos >= 0 {
		text += trf(ctx, "\norder: %d/%d (earlier wins)", pos+1, len(peers))
	}
	var rows [][]ikBtn
	if authz.CanWritePolicy(acct, p) {
		rows = append(rows, []ikBtn{{tr(ctx, "✏️ Edit"), "redit:" + p.ID}})
		if p.Disabled {
			rows = append(rows, []ikBtn{{tr(ctx, "🟢 Enable"), "re:" + p.ID}})
		} else {
			rows = append(rows, []ikBtn{{tr(ctx, "⚪ Disable"), "rx:" + p.ID}})
		}
		// Reorder only when there is a peer to swap with, and only toward a valid
		// neighbour (no dead ⬆ on the first rule).
		if len(peers) > 1 && pos >= 0 {
			var move []ikBtn
			if pos > 0 {
				move = append(move, ikBtn{tr(ctx, "⬆ Up"), "rup:" + p.ID})
			}
			if pos < len(peers)-1 {
				move = append(move, ikBtn{tr(ctx, "⬇ Down"), "rdn:" + p.ID})
			}
			if len(move) > 0 {
				rows = append(rows, move)
			}
		}
		rows = append(rows, []ikBtn{{tr(ctx, "🗑 Delete"), "rdel:" + p.ID}})
	} else {
		text += tr(ctx, "\n(read-only — infra baseline)")
	}
	rows = append(rows, []ikBtn{{tr(ctx, "⬅ Routes"), "m:routes"}})
	return text, inlineKeyboard(rows)
}

// writablePeers returns the caller's writable policies in the given tier, sorted by
// evaluation order (ascending priority), so the route card can show a rule's rank
// and offer reorder against a real neighbour.
func writablePeers(st model.State, acct model.AdminAccount, tier model.RuleTier) []model.RoutePolicy {
	var peers []model.RoutePolicy
	for _, p := range st.RoutePolicies {
		if p.Tier == tier && authz.CanWritePolicy(acct, p) {
			peers = append(peers, p)
		}
	}
	sort.Slice(peers, func(i, j int) bool { return peers[i].Priority < peers[j].Priority })
	return peers
}

func indexOfPolicy(peers []model.RoutePolicy, id string) int {
	for i, p := range peers {
		if p.ID == id {
			return i
		}
	}
	return -1
}

// routeReorder swaps a rule's evaluation priority with its neighbour (up = earlier),
// so precedence is adjustable from the card rather than only in the panel.
func (b *Bot) routeReorder(ctx context.Context, acct model.AdminAccount, id string, up bool) (string, string) {
	st, err := b.scoped(ctx, acct)
	if err != nil {
		return errMsg(err), inlineKeyboard([][]ikBtn{backRow(ctx)})
	}
	p, ok := policyByID(st, id)
	if !ok || !authz.CanWritePolicy(acct, p) {
		return tr(ctx, "⛔ That route is read-only."), inlineKeyboard([][]ikBtn{{{tr(ctx, "⬅ Routes"), "m:routes"}}})
	}
	peers := writablePeers(st, acct, p.Tier)
	pos := indexOfPolicy(peers, id)
	swap := pos - 1
	if !up {
		swap = pos + 1
	}
	if pos < 0 || swap < 0 || swap >= len(peers) {
		return b.routeCard(ctx, acct, id) // already at the edge — no-op
	}
	a, bb := peers[pos], peers[swap]
	a.Priority, bb.Priority = bb.Priority, a.Priority
	if err := b.store.UpsertRoutePolicy(ctx, a); err != nil {
		return errMsg(err), inlineKeyboard([][]ikBtn{backRow(ctx)})
	}
	if err := b.store.UpsertRoutePolicy(ctx, bb); err != nil {
		return errMsg(err), inlineKeyboard([][]ikBtn{backRow(ctx)})
	}
	return b.routeCard(ctx, acct, id)
}

func (b *Bot) routeToggle(ctx context.Context, acct model.AdminAccount, id string, enable bool) (string, string) {
	st, err := b.scoped(ctx, acct)
	if err != nil {
		return errMsg(err), inlineKeyboard([][]ikBtn{backRow(ctx)})
	}
	p, ok := policyByID(st, id)
	if !ok || !authz.CanWritePolicy(acct, p) {
		return tr(ctx, "⛔ That route is read-only."), inlineKeyboard([][]ikBtn{{{tr(ctx, "⬅ Routes"), "m:routes"}}})
	}
	p.Disabled = !enable
	if err := b.store.UpsertRoutePolicy(ctx, p); err != nil {
		return errMsg(err), inlineKeyboard([][]ikBtn{backRow(ctx)})
	}
	return b.routeCard(ctx, acct, id)
}

func (b *Bot) confirmDeleteRoute(ctx context.Context, acct model.AdminAccount, id string) (string, string) {
	st, err := b.scoped(ctx, acct)
	if err != nil {
		return errMsg(err), inlineKeyboard([][]ikBtn{backRow(ctx)})
	}
	p, ok := policyByID(st, id)
	if !ok || !authz.CanWritePolicy(acct, p) {
		return tr(ctx, "⛔ That route is read-only."), inlineKeyboard([][]ikBtn{{{tr(ctx, "⬅ Routes"), "m:routes"}}})
	}
	return trf(ctx, "⚠️ Delete route %q?", p.Name),
		inlineKeyboard([][]ikBtn{{{tr(ctx, "✅ Yes, delete"), "rdz:" + p.ID}, {tr(ctx, "✖ Cancel"), "r:" + p.ID}}})
}

func (b *Bot) routeDelete(ctx context.Context, acct model.AdminAccount, id string) (string, string) {
	st, err := b.scoped(ctx, acct)
	if err != nil {
		return errMsg(err), inlineKeyboard([][]ikBtn{backRow(ctx)})
	}
	p, ok := policyByID(st, id)
	if !ok || !authz.CanWritePolicy(acct, p) {
		return tr(ctx, "⛔ That route is read-only."), inlineKeyboard([][]ikBtn{{{tr(ctx, "⬅ Routes"), "m:routes"}}})
	}
	if err := b.store.DeleteRoutePolicy(ctx, p.ID); err != nil {
		return errMsg(err), inlineKeyboard([][]ikBtn{backRow(ctx)})
	}
	return b.menuRoutes(ctx, acct)
}

// startNewRoute begins the route wizard. Admins choose the tier first (so they can
// author the shared infra baseline); operators skip straight to the match builder
// since their routes are always exit-tier. The match builder is a hub: the operator
// adds one or more kinds (domain/geosite/geoip/cidr), which combine in one rule just
// like the panel, then continues to the action and a confirm summary.
func (b *Bot) startNewRoute(ctx context.Context, acct model.AdminAccount, fromID int64) (string, string) {
	if acct.CanManageInfra() {
		b.startConvo(fromID, &convo{kind: kNewRoute, step: rsTier})
		return routeTierPrompt(ctx), routeTierKeyboard(ctx)
	}
	b.startConvo(fromID, &convo{kind: kNewRoute, step: rsMatch, rt: routeDraft{tier: model.TierExit}})
	return b.routeHub(ctx, &routeDraft{tier: model.TierExit}, false)
}

// startEditRoute re-enters the wizard pre-pointed at an existing policy: the tier,
// id, owner and priority are preserved; the existing match set is pre-loaded into
// the builder so the admin adjusts rather than re-types it; the finish step upserts
// the same rule. Reuses the create flow end to end.
func (b *Bot) startEditRoute(ctx context.Context, acct model.AdminAccount, fromID int64, id string) (string, string) {
	st, err := b.scoped(ctx, acct)
	if err != nil {
		return errMsg(err), inlineKeyboard([][]ikBtn{backRow(ctx)})
	}
	p, ok := policyByID(st, id)
	if !ok || !authz.CanWritePolicy(acct, p) {
		return tr(ctx, "⛔ That route is read-only."), inlineKeyboard([][]ikBtn{{{tr(ctx, "⬅ Routes"), "m:routes"}}})
	}
	d := routeDraft{
		tier:    p.Tier,
		domains: append([]string(nil), p.MatchDomains...),
		geosite: append([]string(nil), p.MatchGeoSite...),
		geoip:   append([]string(nil), p.MatchGeoIP...),
		cidrs:   append([]string(nil), p.MatchCIDRs...),
	}
	b.startConvo(fromID, &convo{kind: kNewRoute, step: rsMatch, editID: p.ID, rt: d})
	text, kb := b.routeHub(ctx, &d, true)
	return trf(ctx, "✏️ Editing route %q.\n\n", p.Name) + text, kb
}

// routePickTier handles an rt: tap (admin tier choice) and moves to the match builder.
func (b *Bot) routePickTier(ctx context.Context, acct model.AdminAccount, fromID int64, tierStr string) (string, string) {
	c := b.convoFor(fromID)
	if c == nil || c.kind != kNewRoute {
		return tr(ctx, "That form expired. Tap ➕ New route to start over."), inlineKeyboard([][]ikBtn{backRow(ctx)})
	}
	tier, ok := parseTier(tierStr)
	if !ok {
		return routeTierPrompt(ctx), routeTierKeyboard(ctx)
	}
	c.rt.tier = tier
	c.step = rsMatch
	return b.routeHub(ctx, &c.rt, c.editID != "")
}

// routePickMatchKind handles an rmk: tap: open the value screen for that kind, where
// common values are one-tap quick-adds and anything else is typed.
func (b *Bot) routePickMatchKind(ctx context.Context, acct model.AdminAccount, fromID int64, kind string) (string, string) {
	c := b.convoFor(fromID)
	if c == nil || c.kind != kNewRoute {
		return tr(ctx, "That form expired. Tap ➕ New route to start over."), inlineKeyboard([][]ikBtn{backRow(ctx)})
	}
	if !validMatchKind(kind) {
		return b.routeHub(ctx, &c.rt, c.editID != "")
	}
	c.rt.editKind = kind
	c.step = rsMatchVals
	return routeMatchValuesPrompt(ctx, kind, &c.rt), routeMatchValuesKeyboard(ctx, kind, &c.rt)
}

// routeQuickAdd handles an rqa:<kind>:<value> tap: toggle a curated value in the
// draft and re-render the value screen so the ✓ marks update in place.
func (b *Bot) routeQuickAdd(ctx context.Context, acct model.AdminAccount, fromID int64, payload string) (string, string) {
	c := b.convoFor(fromID)
	if c == nil || c.kind != kNewRoute {
		return tr(ctx, "That form expired. Tap ➕ New route to start over."), inlineKeyboard([][]ikBtn{backRow(ctx)})
	}
	kind, val, ok := strings.Cut(payload, ":")
	if !ok || !validMatchKind(kind) || val == "" {
		return b.routeHub(ctx, &c.rt, c.editID != "")
	}
	c.rt.toggle(kind, val)
	c.rt.editKind = kind
	c.step = rsMatchVals
	return routeMatchValuesPrompt(ctx, kind, &c.rt), routeMatchValuesKeyboard(ctx, kind, &c.rt)
}

// routeContinue handles the rcont tap: the match set is done, move to the action step.
func (b *Bot) routeContinue(ctx context.Context, acct model.AdminAccount, fromID int64) (string, string) {
	c := b.convoFor(fromID)
	if c == nil || c.kind != kNewRoute {
		return tr(ctx, "That form expired. Tap ➕ New route to start over."), inlineKeyboard([][]ikBtn{backRow(ctx)})
	}
	if c.rt.total() == 0 {
		return b.routeHub(ctx, &c.rt, c.editID != "")
	}
	c.step = rsAction
	return routeActionPrompt(ctx), routeActionKeyboard(ctx, c.rt.tier)
}

// routeBack handles the rback tap: step back one screen, distinct from ✖ Cancel
// (which aborts to the main menu). It keeps the accumulated draft intact.
func (b *Bot) routeBack(ctx context.Context, acct model.AdminAccount, fromID int64) (string, string) {
	c := b.convoFor(fromID)
	if c == nil || c.kind != kNewRoute {
		return b.menuRoutes(ctx, acct)
	}
	switch c.step {
	case rsMatchVals: // value screen -> back to the match hub
		c.step = rsMatch
		return b.routeHub(ctx, &c.rt, c.editID != "")
	case rsMatch: // hub -> tier (admin) or leave the wizard (operator)
		if acct.CanManageInfra() && c.editID == "" {
			c.step = rsTier
			return routeTierPrompt(ctx), routeTierKeyboard(ctx)
		}
		b.clearConvo(fromID)
		return b.menuRoutes(ctx, acct)
	case rsAction: // action -> back to the hub
		c.step = rsMatch
		return b.routeHub(ctx, &c.rt, c.editID != "")
	case rsExit: // exit pick -> back to the action step
		c.step = rsAction
		return routeActionPrompt(ctx), routeActionKeyboard(ctx, c.rt.tier)
	case rsConfirm: // confirm -> back to the action (or exit) step
		if c.rt.action == model.ActionExit {
			c.step = rsExit
			return tr(ctx, "Choose an exit.\nTap one, or reply with its number:\n") + b.exitMenuText(ctx), b.routeExitKeyboard(ctx)
		}
		c.step = rsAction
		return routeActionPrompt(ctx), routeActionKeyboard(ctx, c.rt.tier)
	default: // rsTier -> leave the wizard
		b.clearConvo(fromID)
		return b.menuRoutes(ctx, acct)
	}
}

func (b *Bot) advanceNewRoute(ctx context.Context, acct model.AdminAccount, fromID int64, c *convo, input string) (string, string) {
	switch c.step {
	case rsTier: // admin must tap a tier
		return routeTierPrompt(ctx), routeTierKeyboard(ctx)
	case rsMatch: // hub: typed input is treated as domains (the most common case)
		vals := splitCSV(input)
		if len(vals) == 0 {
			return b.routeHub(ctx, &c.rt, c.editID != "")
		}
		c.rt.addValues("domain", vals)
		return b.routeHub(ctx, &c.rt, c.editID != "")
	case rsMatchVals: // value screen: typed values are added to the current kind
		vals := splitCSV(input)
		if len(vals) == 0 {
			return routeMatchValuesPrompt(ctx, c.rt.editKind, &c.rt), routeMatchValuesKeyboard(ctx, c.rt.editKind, &c.rt)
		}
		c.rt.addValues(c.rt.editKind, vals)
		return routeMatchValuesPrompt(ctx, c.rt.editKind, &c.rt), routeMatchValuesKeyboard(ctx, c.rt.editKind, &c.rt)
	case rsAction: // typed fallback for the action buttons
		switch strings.ToLower(strings.TrimSpace(input)) {
		case "exit", "direct", "block":
			return b.applyRouteAction(ctx, acct, fromID, c, strings.ToLower(strings.TrimSpace(input)))
		default:
			return tr(ctx, "Tap an action below, or reply `exit`, `direct` or `block`."), routeActionKeyboard(ctx, c.rt.tier)
		}
	case rsExit: // typed fallback for the exit buttons
		exits := b.exitNodes(ctx)
		n, err := strconv.Atoi(strings.TrimSpace(input))
		if err != nil || n < 1 || n > len(exits) {
			return tr(ctx, "Tap an exit below, or reply with its number.\n") + b.exitMenuText(ctx), b.routeExitKeyboard(ctx)
		}
		c.rt.exitID = exits[n-1].ID
		c.step = rsConfirm
		return b.routeSummary(ctx, c.rt, c.editID != ""), routeConfirmKeyboard(ctx, c.editID != "")
	default: // rsConfirm: typed fallback for the confirm button
		switch strings.ToLower(strings.TrimSpace(input)) {
		case "y", "yes", "ok", "create", "save", "да", "создать":
			return b.finishRoute(ctx, acct, fromID, c)
		default:
			return b.routeSummary(ctx, c.rt, c.editID != ""), routeConfirmKeyboard(ctx, c.editID != "")
		}
	}
}

// routePickAction handles an ra: tap.
func (b *Bot) routePickAction(ctx context.Context, acct model.AdminAccount, fromID int64, action string) (string, string) {
	c := b.convoFor(fromID)
	if c == nil || c.kind != kNewRoute {
		return tr(ctx, "That form expired. Tap ➕ New route to start over."), inlineKeyboard([][]ikBtn{backRow(ctx)})
	}
	return b.applyRouteAction(ctx, acct, fromID, c, action)
}

// applyRouteAction records the chosen action. An exit action needs an exit pick;
// direct/block go straight to the confirm step. Guard-tier rules keep traffic on
// the entry, so they cannot target an exit.
func (b *Bot) applyRouteAction(ctx context.Context, acct model.AdminAccount, fromID int64, c *convo, action string) (string, string) {
	switch action {
	case "exit":
		if c.rt.tier == model.TierGuard {
			return tr(ctx, "Guard-tier rules can't route to an exit. Pick direct or block."), routeActionKeyboard(ctx, c.rt.tier)
		}
		c.rt.action = model.ActionExit
		c.step = rsExit
		return tr(ctx, "Choose an exit.\nTap one, or reply with its number:\n") + b.exitMenuText(ctx), b.routeExitKeyboard(ctx)
	case "direct":
		c.rt.action = model.ActionDirect
	case "block":
		c.rt.action = model.ActionBlock
	default:
		return tr(ctx, "Tap an action below, or reply `exit`, `direct` or `block`."), routeActionKeyboard(ctx, c.rt.tier)
	}
	c.step = rsConfirm
	return b.routeSummary(ctx, c.rt, c.editID != ""), routeConfirmKeyboard(ctx, c.editID != "")
}

// routePickExit handles an rxp: tap: record the exit and move to the confirm step.
func (b *Bot) routePickExit(ctx context.Context, acct model.AdminAccount, fromID int64, nodeID string) (string, string) {
	c := b.convoFor(fromID)
	if c == nil || c.kind != kNewRoute {
		return tr(ctx, "That form expired. Tap ➕ New route to start over."), inlineKeyboard([][]ikBtn{backRow(ctx)})
	}
	c.rt.exitID = nodeID
	c.step = rsConfirm
	return b.routeSummary(ctx, c.rt, c.editID != ""), routeConfirmKeyboard(ctx, c.editID != "")
}

// routeConfirm handles the rok tap: create or save the route the wizard accumulated.
func (b *Bot) routeConfirm(ctx context.Context, acct model.AdminAccount, fromID int64) (string, string) {
	c := b.convoFor(fromID)
	if c == nil || c.kind != kNewRoute {
		return tr(ctx, "That form expired. Tap ➕ New route to start over."), inlineKeyboard([][]ikBtn{backRow(ctx)})
	}
	return b.finishRoute(ctx, acct, fromID, c)
}

// finishRoute persists the accumulated rule and returns the freshly-saved route's
// card (with a one-line banner) so the operator lands on something they can act on,
// rather than a dead-end "open the menu" line.
func (b *Bot) finishRoute(ctx context.Context, acct model.AdminAccount, fromID int64, c *convo) (string, string) {
	editing := c.editID != ""
	st, err := b.store.LoadState(ctx)
	if err != nil {
		b.clearConvo(fromID)
		return errMsg(err), inlineKeyboard([][]ikBtn{backRow(ctx)})
	}
	tier := c.rt.tier
	if tier == "" {
		tier = model.TierExit
	}
	p := model.RoutePolicy{
		Name:    routeAutoName(c.rt),
		Tier:    tier,
		Action:  c.rt.action,
		OwnerID: ownerFor(acct),
	}
	if c.editID != "" { // edit: keep identity/order, refresh the fields
		if old, ok := policyByID(st, c.editID); ok {
			p.ID, p.Priority, p.OwnerID, p.Tier, p.Disabled = old.ID, old.Priority, old.OwnerID, old.Tier, old.Disabled
		}
	}
	if p.ID == "" {
		p.ID = slugID("r", string(c.rt.action)+"-"+draftFirstValue(c.rt))
		p.Priority = nextPriority(st)
	}
	p.MatchDomains = c.rt.domains
	p.MatchGeoSite = c.rt.geosite
	p.MatchGeoIP = c.rt.geoip
	p.MatchCIDRs = c.rt.cidrs
	if c.rt.action == model.ActionExit {
		p.ExitNodeID = c.rt.exitID
	}
	if !authz.CanWritePolicy(acct, p) { // server-side gate (a forged tier can't slip through)
		b.clearConvo(fromID)
		return tr(ctx, "⛔ That route is read-only."), routesBackKeyboard(ctx)
	}
	if err := p.Validate(); err != nil {
		b.clearConvo(fromID)
		return errMsg(err), routesBackKeyboard(ctx)
	}
	b.clearConvo(fromID)
	if err := b.store.UpsertRoutePolicy(ctx, p); err != nil {
		return errMsg(err), inlineKeyboard([][]ikBtn{backRow(ctx)})
	}
	banner := tr(ctx, "✅ Route created.")
	if editing {
		banner = tr(ctx, "✅ Route updated.")
	}
	text, kb := b.routeCard(ctx, acct, p.ID)
	return banner + "\n\n" + text, kb
}

// ---- route wizard prompts & keyboards ----

// validMatchKind reports whether s is one of the four match classes.
func validMatchKind(s string) bool {
	switch s {
	case "domain", "geosite", "geoip", "cidr":
		return true
	}
	return false
}

// splitCSV trims a comma-separated reply into its non-empty values.
func splitCSV(input string) []string {
	var vals []string
	for _, part := range strings.Split(input, ",") {
		if v := strings.TrimSpace(part); v != "" {
			vals = append(vals, v)
		}
	}
	return vals
}

// routeTierPrompt explains the three tiers in plain words so "tier" isn't jargon the
// admin has to already understand.
func routeTierPrompt(ctx context.Context) string {
	return tr(ctx, "🧭 New route — where should this rule apply?\n🚪 Exit — a routing rule of your own (most common)\n🌐 Network — applies to the whole fleet\n🛡 Guard — keeps matched traffic on the entry node")
}

func routeTierKeyboard(ctx context.Context) string {
	return inlineKeyboard([][]ikBtn{
		{{tr(ctx, "🚪 Exit"), "rt:exit"}},
		{{tr(ctx, "🌐 Network"), "rt:fleet"}, {tr(ctx, "🛡 Guard"), "rt:guard"}},
		{{tr(ctx, "⬅ Back"), "rback"}, {tr(ctx, "✖ Cancel"), "cx"}},
	})
}

// botGeoSites are the most common sing-geosite categories offered as one-tap
// quick-adds in the route wizard — a curated subset of the panel's full catalogue,
// so the operator doesn't have to know the category names. Any other category can
// still be typed.
var botGeoSites = []string{
	"google", "youtube", "telegram", "whatsapp",
	"instagram", "twitter", "facebook", "tiktok",
	"netflix", "spotify", "openai", "github",
	"category-ads", "category-porn", "geolocation-cn", "geolocation-!cn",
}

// botCountries are common geoip country codes offered as one-tap quick-adds; any
// ISO code can still be typed.
var botCountries = []string{
	"ru", "us", "de", "nl",
	"gb", "fr", "ir", "cn",
	"ua", "fi", "se", "pl",
	"tr", "jp", "sg", "hk",
}

// routeHub renders the match-builder hub: the match set accumulated so far plus a
// button to add each kind (they combine in one rule, like the panel) and a Continue
// once at least one value is set. Typed input at the hub is added as domains.
func (b *Bot) routeHub(ctx context.Context, d *routeDraft, editing bool) (string, string) {
	var sb strings.Builder
	sb.WriteString(tr(ctx, "🧭 Build the match"))
	if d.total() == 0 {
		sb.WriteString(tr(ctx, "\nAdd one or more kinds below. Several kinds combine in one rule (domain AND geosite AND …)."))
	} else {
		sb.WriteString(tr(ctx, " — so far:\n"))
		sb.WriteString(draftMatchLines(d))
	}
	rows := [][]ikBtn{
		{{routeAddLabel(ctx, "🌐 Domain", len(d.domains)), "rmk:domain"}, {routeAddLabel(ctx, "🗂 Geosite", len(d.geosite)), "rmk:geosite"}},
		{{routeAddLabel(ctx, "📍 Geo-IP", len(d.geoip)), "rmk:geoip"}, {routeAddLabel(ctx, "🔢 CIDR", len(d.cidrs)), "rmk:cidr"}},
	}
	if d.total() > 0 {
		rows = append(rows, []ikBtn{{tr(ctx, "➡ Continue"), "rcont"}})
	}
	rows = append(rows, []ikBtn{{tr(ctx, "⬅ Back"), "rback"}, {tr(ctx, "✖ Cancel"), "cx"}})
	return sb.String(), inlineKeyboard(rows)
}

// routeAddLabel tags an add-kind button with the count already chosen for it.
func routeAddLabel(ctx context.Context, s string, n int) string {
	if n > 0 {
		return fmt.Sprintf("%s (%d)", tr(ctx, s), n)
	}
	return tr(ctx, s)
}

// draftMatchLines lists the draft's match set, one line per non-empty kind (the
// field names are technical, shown verbatim as in the panel).
func draftMatchLines(d *routeDraft) string {
	var lines []string
	if len(d.domains) > 0 {
		lines = append(lines, "  domains: "+strings.Join(d.domains, ", "))
	}
	if len(d.geosite) > 0 {
		lines = append(lines, "  geosite: "+strings.Join(d.geosite, ", "))
	}
	if len(d.geoip) > 0 {
		lines = append(lines, "  geoip: "+strings.Join(d.geoip, ", "))
	}
	if len(d.cidrs) > 0 {
		lines = append(lines, "  cidr: "+strings.Join(d.cidrs, ", "))
	}
	return strings.Join(lines, "\n")
}

// routeMatchValuesPrompt asks for values for the chosen kind. For geosite/geoip the
// common values are one-tap buttons below; anything else is typed, comma-separated —
// no "geosite:"/"cidr:" prefix to remember.
func routeMatchValuesPrompt(ctx context.Context, kind string, d *routeDraft) string {
	var head string
	switch kind {
	case "geosite":
		head = tr(ctx, "🗂 Geosite — tap the common categories below, or type any category (comma-separated).\ne.g. google, netflix, category-ads")
	case "geoip":
		head = tr(ctx, "📍 Geo-IP — tap the common countries below, or type any ISO code (comma-separated).\ne.g. ru, us")
	case "cidr":
		head = tr(ctx, "🔢 Enter CIDR range(s), comma-separated for several.\ne.g. 10.0.0.0/8, 192.168.0.0/16")
	default:
		head = tr(ctx, "🌐 Enter domain(s), comma-separated for several.\ne.g. netflix.com, *.google.com")
	}
	if sel := *d.slot(kind); len(sel) > 0 {
		head += "\n\n" + trf(ctx, "added: %s", strings.Join(sel, ", "))
	}
	return head
}

// routeMatchValuesKeyboard renders the curated quick-adds for a kind (a ✓ marks the
// ones already chosen), then ✅ Done (back to the hub) and ✖ Cancel.
func routeMatchValuesKeyboard(ctx context.Context, kind string, d *routeDraft) string {
	var quick []string
	switch kind {
	case "geosite":
		quick = botGeoSites
	case "geoip":
		quick = botCountries
	}
	var rows [][]ikBtn
	for i := 0; i < len(quick); i += 2 {
		var row []ikBtn
		for j := i; j < i+2 && j < len(quick); j++ {
			v := quick[j]
			label := v
			if d.has(kind, v) {
				label = "✓ " + v
			}
			row = append(row, ikBtn{label, "rqa:" + kind + ":" + v})
		}
		rows = append(rows, row)
	}
	rows = append(rows, []ikBtn{{tr(ctx, "✅ Done"), "rback"}})
	rows = append(rows, []ikBtn{{tr(ctx, "✖ Cancel"), "cx"}})
	return inlineKeyboard(rows)
}

func routeActionPrompt(ctx context.Context) string {
	return tr(ctx, "Action?\nTap one, or reply `exit`, `direct` or `block`.")
}

func routeActionKeyboard(ctx context.Context, tier model.RuleTier) string {
	var rows [][]ikBtn
	if tier != model.TierGuard { // guard rules keep traffic on the entry — no exit action
		rows = append(rows, []ikBtn{{tr(ctx, "🚪 Exit"), "ra:exit"}})
	}
	rows = append(rows, []ikBtn{{tr(ctx, "➡ Direct"), "ra:direct"}, {tr(ctx, "🚫 Block"), "ra:block"}})
	rows = append(rows, []ikBtn{{tr(ctx, "⬅ Back"), "rback"}, {tr(ctx, "✖ Cancel"), "cx"}})
	return inlineKeyboard(rows)
}

func (b *Bot) routeExitKeyboard(ctx context.Context) string {
	var rows [][]ikBtn
	for _, n := range b.exitNodes(ctx) {
		rows = append(rows, []ikBtn{{"🚪 " + n.Name, "rxp:" + n.ID}})
	}
	rows = append(rows, []ikBtn{{tr(ctx, "⬅ Back"), "rback"}, {tr(ctx, "✖ Cancel"), "cx"}})
	return inlineKeyboard(rows)
}

// routeSummary renders the review card shown before the rule is written, so the
// operator confirms the whole rule at once instead of it being created silently.
func (b *Bot) routeSummary(ctx context.Context, d routeDraft, editing bool) string {
	var action string
	switch d.action {
	case model.ActionExit:
		action = trf(ctx, "exit %q", b.exitName(ctx, d.exitID))
	case model.ActionDirect:
		action = tr(ctx, "direct")
	case model.ActionBlock:
		action = tr(ctx, "block")
	}
	tier := d.tier
	if tier == "" {
		tier = model.TierExit
	}
	body := trf(ctx, "🧭 Review the route:\nmatch:\n%s\naction: %s\ntier: %s", draftMatchLines(&d), action, string(tier))
	if editing {
		return body + tr(ctx, "\n\nSave these changes?")
	}
	return body + tr(ctx, "\n\nCreate this route?")
}

func routeConfirmKeyboard(ctx context.Context, editing bool) string {
	label := tr(ctx, "✅ Create route")
	if editing {
		label = tr(ctx, "✅ Save changes")
	}
	return inlineKeyboard([][]ikBtn{
		{{label, "rok"}},
		{{tr(ctx, "⬅ Back"), "rback"}, {tr(ctx, "✖ Cancel"), "cx"}},
	})
}

func routesBackKeyboard(ctx context.Context) string {
	return inlineKeyboard([][]ikBtn{{{tr(ctx, "⬅ Routes"), "m:routes"}}})
}

// parseTier maps a tier button payload to a RuleTier.
func parseTier(s string) (model.RuleTier, bool) {
	switch s {
	case "exit":
		return model.TierExit, true
	case "fleet":
		return model.TierFleet, true
	case "guard":
		return model.TierGuard, true
	}
	return "", false
}

// ---- account ----

func (b *Bot) menuAccount(ctx context.Context, acct model.AdminAccount) (string, string) {
	lang := normalizeLang(acct.Locale)
	alert := tr(ctx, "not set")
	if acct.AlertChatID != "" {
		alert = tr(ctx, "this chat")
	}
	text := trf(ctx, "⚙️ Account\nlanguage: %s\nalert chat: %s", lang, alert)
	rows := [][]ikBtn{
		{{"🇷🇺 RU", "lang:ru"}, {"🇬🇧 EN", "lang:en"}},
		{{tr(ctx, "🔔 Send alerts here"), "alert:here"}, {tr(ctx, "🔕 Off"), "alert:clear"}},
		backRow(ctx),
	}
	return text, inlineKeyboard(rows)
}

func (b *Bot) accountSetLang(ctx context.Context, acct model.AdminAccount, lang string) (string, string) {
	lang = normalizeLang(lang)
	if err := b.store.SetAccountLocale(ctx, acct.Username, lang); err != nil {
		return errMsg(err), inlineKeyboard([][]ikBtn{backRow(ctx)})
	}
	acct.Locale = lang
	return b.menuAccount(withLang(ctx, lang), acct)
}

func (b *Bot) accountSetAlert(ctx context.Context, acct model.AdminAccount, fromID int64, which string) (string, string) {
	chat := ""
	if which == "here" {
		chat = strconv.FormatInt(fromID, 10)
	}
	if err := b.store.SetAccountTelegram(ctx, acct.Username, acct.TelegramID, chat); err != nil {
		return errMsg(err), inlineKeyboard([][]ikBtn{backRow(ctx)})
	}
	acct.AlertChatID = chat
	return b.menuAccount(ctx, acct)
}

// ---- operator infra-health aggregate ----

func (b *Bot) menuInfra(ctx context.Context, acct model.AdminAccount) (string, string) {
	agg := b.infraAgg(ctx)
	active := tr(ctx, "✅ ok")
	if !agg.ActiveOK {
		active = tr(ctx, "⚠️ degraded")
	}
	text := trf(ctx, "🩺 Infra\nnodes: %d (healthy %d · problems %d)\nactive exit: %s",
		agg.Total, agg.Healthy, agg.Unhealthy, active)
	return text, inlineKeyboard([][]ikBtn{backRow(ctx)})
}

// ---- client delete confirm ----

func (b *Bot) confirmDeleteUser(ctx context.Context, acct model.AdminAccount, id string) (string, string) {
	st, err := b.scoped(ctx, acct)
	if err != nil {
		return errMsg(err), inlineKeyboard([][]ikBtn{backRow(ctx)})
	}
	u, ok := userByID(st, id)
	if !ok || !authz.CanWriteOwned(acct, u.OwnerID) {
		return tr(ctx, "No such user."), inlineKeyboard([][]ikBtn{{{tr(ctx, "⬅ Users"), "m:users"}}})
	}
	return trf(ctx, "⚠️ Delete client %q?\nThis revokes its config for good.", displayName(u)),
		inlineKeyboard([][]ikBtn{{{tr(ctx, "✅ Yes, delete"), "udz:" + u.ID}, {tr(ctx, "✖ Cancel"), "u:" + u.ID}}})
}

func (b *Bot) userDelete(ctx context.Context, acct model.AdminAccount, id string) (string, string) {
	st, err := b.scoped(ctx, acct)
	if err != nil {
		return errMsg(err), inlineKeyboard([][]ikBtn{backRow(ctx)})
	}
	u, ok := userByID(st, id)
	if !ok || !authz.CanWriteOwned(acct, u.OwnerID) {
		return tr(ctx, "No such user."), inlineKeyboard([][]ikBtn{{{tr(ctx, "⬅ Users"), "m:users"}}})
	}
	if err := b.store.DeleteUser(ctx, u.ID); err != nil {
		return errMsg(err), inlineKeyboard([][]ikBtn{backRow(ctx)})
	}
	return b.menuUsers(ctx, acct)
}

// ---- bootstrap lens (read-only, all namespaces) ----

func (b *Bot) lensList(ctx context.Context, acct model.AdminAccount) (string, string) {
	if !acct.IsBootstrapOwner() {
		return tr(ctx, "Unknown action. Tap ⬅ Menu."), inlineKeyboard([][]ikBtn{backRow(ctx)})
	}
	st, err := b.store.LoadState(ctx)
	if err != nil {
		return errMsg(err), inlineKeyboard([][]ikBtn{backRow(ctx)})
	}
	seen := map[string]bool{}
	var nss []string
	for _, u := range st.Users {
		if !seen[u.OwnerID] {
			seen[u.OwnerID] = true
			nss = append(nss, u.OwnerID)
		}
	}
	sort.Strings(nss)
	var rows [][]ikBtn
	for _, ns := range nss {
		label := ns
		if ns == "" {
			label = "private"
		}
		rows = append(rows, []ikBtn{{"🔭 " + label, "lens:" + ns}})
	}
	rows = append(rows, backRow(ctx))
	text := tr(ctx, "All namespaces — tap to inspect (read-only)")
	if len(nss) == 0 {
		text = tr(ctx, "No clients in any namespace yet.")
	}
	return text, inlineKeyboard(rows)
}

func (b *Bot) lensView(ctx context.Context, acct model.AdminAccount, ns string) (string, string) {
	if !acct.IsBootstrapOwner() {
		return tr(ctx, "Unknown action. Tap ⬅ Menu."), inlineKeyboard([][]ikBtn{backRow(ctx)})
	}
	st, err := b.store.LoadState(ctx)
	if err != nil {
		return errMsg(err), inlineKeyboard([][]ikBtn{backRow(ctx)})
	}
	var users []model.User
	groups, routes := 0, 0
	for _, u := range st.Users {
		if u.OwnerID == ns {
			users = append(users, u)
		}
	}
	for _, g := range st.Groups {
		if g.OwnerID == ns {
			groups++
		}
	}
	for _, p := range st.RoutePolicies {
		if p.Tier == model.TierExit && p.OwnerID == ns {
			routes++
		}
	}
	sort.Slice(users, func(i, j int) bool { return displayName(users[i]) < displayName(users[j]) })
	label := ns
	if ns == "" {
		label = "private"
	}
	var sb strings.Builder
	sb.WriteString(trf(ctx, "🔭 %s — clients %d · groups %d · routes %d", label, len(users), groups, routes))
	for _, u := range users {
		sb.WriteString("\n" + enabledIcon(u.Enabled) + " " + displayName(u))
	}
	return sb.String(), inlineKeyboard([][]ikBtn{{{tr(ctx, "⬅ Back"), "m:lens"}}})
}

// ---- helpers ----

func groupByID(st model.State, id string) (model.Group, bool) {
	for _, g := range st.Groups {
		if g.ID == id {
			return g, true
		}
	}
	return model.Group{}, false
}

func policyByID(st model.State, id string) (model.RoutePolicy, bool) {
	for _, p := range st.RoutePolicies {
		if p.ID == id {
			return p, true
		}
	}
	return model.RoutePolicy{}, false
}

func clientsInGroup(st model.State, gid string) int {
	n := 0
	for _, u := range st.Users {
		if u.GroupID == gid {
			n++
		}
	}
	return n
}

// exitNodes lists the fleet's exit nodes (id + name only) from the raw state.
// This is a deliberate, routing-only disclosure to a scoped account: to author an
// exit-tier route or pick a group's default exit, the namespace owner must be able
// to name the exits it targets. It exposes no entry nodes, IPs, health, or control
// plane (cf. infraAggregate, the other ScopeState bypass).
func (b *Bot) exitNodes(ctx context.Context) []model.Node {
	st, err := b.store.LoadState(ctx)
	if err != nil {
		return nil
	}
	var out []model.Node
	for _, n := range st.Nodes {
		if n.IsExit() {
			out = append(out, model.Node{ID: n.ID, Name: n.Name})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (b *Bot) exitName(ctx context.Context, id string) string {
	for _, n := range b.exitNodes(ctx) {
		if n.ID == id {
			return n.Name
		}
	}
	return id
}

// exitMenuText renders the numbered exit list for the route wizard's exit step.
func (b *Bot) exitMenuText(ctx context.Context) string {
	exits := b.exitNodes(ctx)
	if len(exits) == 0 {
		return tr(ctx, "(no exit nodes in the fleet yet — ask an admin)")
	}
	var sb strings.Builder
	for i, n := range exits {
		fmt.Fprintf(&sb, "%d. %s\n", i+1, n.Name)
	}
	return strings.TrimRight(sb.String(), "\n")
}

func routeAutoName(d routeDraft) string {
	return string(d.action) + " " + draftFirstValue(d)
}

// draftFirstValue returns the first match value across the draft's kinds (for the
// auto-generated route name/id), or "rule" when the draft is somehow empty.
func draftFirstValue(d routeDraft) string {
	for _, s := range [][]string{d.domains, d.geosite, d.geoip, d.cidrs} {
		if len(s) > 0 {
			return s[0]
		}
	}
	return "rule"
}

// nextPriority returns a priority above every existing policy, so a new exit rule
// is appended at the end of evaluation order.
func nextPriority(st model.State) int {
	max := 0
	for _, p := range st.RoutePolicies {
		if p.Priority > max {
			max = p.Priority
		}
	}
	return max + 1
}

func routeToggleIcon(p model.RoutePolicy) string {
	if p.Disabled {
		return "⚪"
	}
	return "🟢"
}

// routeLabel is the short one-line description used in lists.
func routeLabel(ctx context.Context, b *Bot, p model.RoutePolicy) string {
	return routeMatchText(p) + " → " + b.routeActionText(ctx, p)
}

func routeMatchText(p model.RoutePolicy) string {
	var parts []string
	parts = append(parts, p.MatchDomains...)
	for _, g := range p.MatchGeoSite {
		parts = append(parts, "geosite:"+g)
	}
	for _, g := range p.MatchGeoIP {
		parts = append(parts, "geoip:"+g)
	}
	for _, c := range p.MatchCIDRs {
		parts = append(parts, "cidr:"+c)
	}
	if len(parts) == 0 {
		return "(no match)"
	}
	if len(parts) > 3 {
		return strings.Join(parts[:3], ", ") + " …"
	}
	return strings.Join(parts, ", ")
}

func (b *Bot) routeActionText(ctx context.Context, p model.RoutePolicy) string {
	switch p.Action {
	case model.ActionExit:
		return trf(ctx, "exit %q", b.exitName(ctx, p.ExitNodeID))
	case model.ActionDirect:
		return tr(ctx, "direct")
	case model.ActionBlock:
		return tr(ctx, "block")
	}
	return string(p.Action)
}

// slugID builds a stable-ish id "<prefix>-<slug>-<rand>" from a display name.
func slugID(prefix, name string) string {
	var sb strings.Builder
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			sb.WriteRune(r)
		case r == ' ' || r == '-' || r == '_' || r == '.':
			sb.WriteByte('-')
		}
	}
	slug := strings.Trim(sb.String(), "-")
	if slug == "" {
		slug = prefix
	}
	return prefix + "-" + slug + "-" + randomHex(3)
}
