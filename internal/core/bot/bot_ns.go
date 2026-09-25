package bot

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"trustpanel/internal/core/authz"
	"trustpanel/internal/core/journal"
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
	text := tr(ctx, "Groups — tap to open")
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
		return tr(ctx, "No such group."), inlineKeyboard([][]ikBtn{backTo(ctx, "Groups", "m:groups")})
	}
	text := trf(ctx, "👥 %s\nclients: %d\n%s", g.Name, clientsInGroup(st, g.ID), b.groupExitLine(ctx, g))
	rows := [][]ikBtn{
		{{tr(ctx, "👤 Clients in the group"), "gcl:" + g.ID}},
		{{tr(ctx, "🚪 Change the exit"), "gdef:" + g.ID}},
		{{tr(ctx, lblMore), "gmore:" + g.ID}},
		backTo(ctx, "Groups", "m:groups"),
	}
	return text, inlineKeyboard(rows)
}

// groupExitLine says where the group's traffic leaves by, in the terms the
// answer is actually in: a named exit, or the entry server itself. "not set" was
// a field's state, not a route.
func (b *Bot) groupExitLine(ctx context.Context, g model.Group) string {
	if g.DefaultExitID == "" {
		return tr(ctx, "traffic leaves: from the entry server itself")
	}
	line := trf(ctx, "traffic leaves: through %s", b.exitName(ctx, g.DefaultExitID))
	if n, ok := b.exitNode(ctx, g.DefaultExitID); ok && n.Maintenance {
		line += tr(ctx, " (in maintenance — until it returns, from the entry server)")
	}
	return line
}

// groupMore holds renaming and deleting: the group is read far more often than
// it is renamed, and deleting it is not something to keep under the thumb.
func (b *Bot) groupMore(ctx context.Context, acct model.AdminAccount, id string) (string, string) {
	st, err := b.scoped(ctx, acct)
	if err != nil {
		return errMsg(err), inlineKeyboard([][]ikBtn{backRow(ctx)})
	}
	g, ok := groupByID(st, id)
	if !ok {
		return tr(ctx, "No such group."), inlineKeyboard([][]ikBtn{backTo(ctx, "Groups", "m:groups")})
	}
	rows := [][]ikBtn{
		{{tr(ctx, "✏️ Rename"), "grn:" + g.ID}},
		{{tr(ctx, lblDelete), "gdel:" + g.ID}},
		backTo(ctx, "Back", "g:"+g.ID),
	}
	return trf(ctx, "👥 %s\nclients: %d\n%s", g.Name, clientsInGroup(st, g.ID), b.groupExitLine(ctx, g)), inlineKeyboard(rows)
}

// groupClients lists the clients of one group, letting "clients: 3" be read as
// three people and not a number.
func (b *Bot) groupClients(ctx context.Context, acct model.AdminAccount, id string) (string, string) {
	st, err := b.scoped(ctx, acct)
	if err != nil {
		return errMsg(err), inlineKeyboard([][]ikBtn{backRow(ctx)})
	}
	g, ok := groupByID(st, id)
	if !ok {
		return tr(ctx, "No such group."), inlineKeyboard([][]ikBtn{backTo(ctx, "Groups", "m:groups")})
	}
	var users []model.User
	for _, u := range st.Users {
		if u.GroupID == g.ID {
			users = append(users, u)
		}
	}
	sort.Slice(users, func(i, j int) bool { return displayName(users[i]) < displayName(users[j]) })
	var rows [][]ikBtn
	for _, u := range users {
		rows = append(rows, []ikBtn{{accessOf(u, b.now()).glyph + " " + displayName(u), "u:" + u.ID}})
	}
	rows = append(rows, backTo(ctx, "Back", "g:"+g.ID))
	if len(users) == 0 {
		return trf(ctx, "👥 %s — no clients in it yet.", g.Name), inlineKeyboard(rows)
	}
	return trf(ctx, "👥 %s — clients (%d):", g.Name, len(users)), inlineKeyboard(rows)
}

// groupDefaultPicker offers the available exit nodes as the group's default exit.
func (b *Bot) groupDefaultPicker(ctx context.Context, acct model.AdminAccount, id string) (string, string) {
	st, err := b.scoped(ctx, acct)
	if err != nil {
		return errMsg(err), inlineKeyboard([][]ikBtn{backRow(ctx)})
	}
	g, ok := groupByID(st, id)
	if !ok || !authz.CanWriteOwned(acct, g.OwnerID) {
		return tr(ctx, "No such group."), inlineKeyboard([][]ikBtn{backTo(ctx, "Groups", "m:groups")})
	}
	var rows, drained [][]ikBtn
	for _, n := range b.exitNodes(ctx) {
		label := "🚪 " + n.Name
		if n.ID == g.DefaultExitID {
			label = "✓ " + n.Name // where it leaves by now
		}
		if n.Maintenance {
			// A server in maintenance is not an ordinary choice: it is offered
			// last and says what it is, so picking it is a decision and not
			// a mistake.
			drained = append(drained, []ikBtn{{"⏸ " + n.Name + tr(ctx, " (in maintenance)"), "gdefp:" + g.ID + ":" + n.ID}})
			continue
		}
		rows = append(rows, []ikBtn{{label, "gdefp:" + g.ID + ":" + n.ID}})
	}
	rows = append(rows, drained...)
	rows = append(rows, []ikBtn{{tr(ctx, "🏠 From the entry server itself"), "gdefp:" + g.ID + ":-"}})
	rows = append(rows, backTo(ctx, "Back", "g:"+g.ID))
	return trf(ctx, "🚪 Where should the traffic of %s leave by?\n%s", g.Name, b.groupExitLine(ctx, g)), inlineKeyboard(rows)
}

// groupExitConfirm is the last look before a group moves: what it was, what it
// becomes, and how many clients that is. The panel asks the same question for
// the same reason: a group's exit is not a preference, it is where a roomful of
// people appear from.
func (b *Bot) groupExitConfirm(ctx context.Context, acct model.AdminAccount, payload string) (string, string) {
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
		return tr(ctx, "No such group."), inlineKeyboard([][]ikBtn{backTo(ctx, "Groups", "m:groups")})
	}
	if (exitID == "-" && g.DefaultExitID == "") || exitID == g.DefaultExitID {
		return b.groupCard(ctx, acct, gid) // already there; nothing to confirm
	}
	if _, ok := b.exitNode(ctx, exitID); !ok && exitID != "-" {
		// The card the tap came from may be older than the network is: ask again
		// against the exits there are now, instead of naming an id at somebody.
		return b.groupDefaultPicker(ctx, acct, gid)
	}
	was := tr(ctx, "from the entry server itself")
	if g.DefaultExitID != "" {
		was = b.exitName(ctx, g.DefaultExitID)
	}
	becomes := tr(ctx, "from the entry server itself")
	if exitID != "-" {
		becomes = b.exitName(ctx, exitID)
	}
	text := trf(ctx, "🚪 Move the traffic of %s?\nnow: %s\nbecomes: %s\nclients affected: %d",
		g.Name, was, becomes, clientsInGroup(st, g.ID))
	if n, ok := b.exitNode(ctx, exitID); ok && n.Maintenance {
		text += tr(ctx, "\n\n⏸ That server is in maintenance: until it returns, this traffic leaves from the entry server.")
	}
	rows := [][]ikBtn{
		{{tr(ctx, "✓ Move it"), "gdefc:" + gid + ":" + exitID}, {tr(ctx, lblCancel), "g:" + gid}},
	}
	return text, inlineKeyboard(rows)
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
		return tr(ctx, "No such group."), inlineKeyboard([][]ikBtn{backTo(ctx, "Groups", "m:groups")})
	}
	if exitID == "-" {
		g.DefaultExitID = ""
	} else {
		g.DefaultExitID = exitID
	}
	if err := b.store.UpsertGroup(ctx, g); err != nil {
		return errMsg(err), inlineKeyboard([][]ikBtn{backRow(ctx)})
	}
	line := journal.Line("group %s no longer has an exit of its own", ref(g.Name, g.ID))
	if g.DefaultExitID != "" {
		// Node names are infra, which an operator's scoped state does not carry;
		// exitName reads them from the full state the same way the picker does.
		line = journal.Line("exit of group %s set to %s", ref(g.Name, g.ID), ref(b.exitName(ctx, g.DefaultExitID), g.DefaultExitID))
	}
	b.recordFor(ctx, model.SeverityInfo, line, acct)
	return b.groupCard(ctx, acct, gid)
}

// startNewGroup begins the one-step "name a group" wizard.
func (b *Bot) startNewGroup(ctx context.Context, fromID int64) string {
	b.startConvo(ctx, fromID, &convo{kind: kNewGroup, step: 0})
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
	b.recordFor(ctx, model.SeverityInfo, journal.Line("created group %s", ref(g.Name, g.ID)), acct)
	return trf(ctx, "✓ Group %s created. Open /menu → Groups to manage it.", name)
}

// startRenameGroup begins the rename wizard after an ownership check.
func (b *Bot) startRenameGroup(ctx context.Context, acct model.AdminAccount, fromID int64, id string) (string, string) {
	st, err := b.scoped(ctx, acct)
	if err != nil {
		return errMsg(err), inlineKeyboard([][]ikBtn{backRow(ctx)})
	}
	g, ok := groupByID(st, id)
	if !ok || !authz.CanWriteOwned(acct, g.OwnerID) {
		return tr(ctx, "No such group."), inlineKeyboard([][]ikBtn{backTo(ctx, "Groups", "m:groups")})
	}
	b.startConvo(ctx, fromID, &convo{kind: kRenameGroup, step: 0, groupID: id})
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
	was := g.Name
	g.Name = name
	if err := g.Validate(); err != nil {
		return "⚠️ " + err.Error() + tr(ctx, "\nReply with another name, or /cancel.")
	}
	b.clearConvo(fromID)
	if err := b.store.UpsertGroup(ctx, g); err != nil {
		return errMsg(err)
	}
	b.recordFor(ctx, model.SeverityInfo, journal.Line("group %s renamed to %q", ref(was, g.ID), name), acct)
	return trf(ctx, "✓ Group renamed to %s.", name)
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
		return tr(ctx, "No such group."), inlineKeyboard([][]ikBtn{backTo(ctx, "Groups", "m:groups")})
	}
	if n := clientsInGroup(st, g.ID); n > 0 {
		return trf(ctx, "🚫 Cannot delete %q: %d client(s) still use it. Move them first.", g.Name, n),
			inlineKeyboard([][]ikBtn{backTo(ctx, "Back", "g:"+g.ID)})
	}
	return trf(ctx, "⚠️ Delete group %s?", g.Name),
		inlineKeyboard([][]ikBtn{{{tr(ctx, "🗑 Delete group"), "gdz:" + g.ID}, {tr(ctx, lblCancel), "g:" + g.ID}}})
}

func (b *Bot) groupDelete(ctx context.Context, acct model.AdminAccount, id string) (string, string) {
	st, err := b.scoped(ctx, acct)
	if err != nil {
		return errMsg(err), inlineKeyboard([][]ikBtn{backRow(ctx)})
	}
	g, ok := groupByID(st, id)
	if !ok || !authz.CanWriteOwned(acct, g.OwnerID) {
		return tr(ctx, "No such group."), inlineKeyboard([][]ikBtn{backTo(ctx, "Groups", "m:groups")})
	}
	if clientsInGroup(st, g.ID) > 0 { // re-check (defence vs a stale confirm)
		return b.confirmDeleteGroup(ctx, acct, id)
	}
	if err := b.store.DeleteGroup(ctx, g.ID); err != nil {
		return errMsg(err), inlineKeyboard([][]ikBtn{backRow(ctx)})
	}
	b.recordFor(ctx, model.SeverityWarn, journal.Line("deleted group %s", ref(g.Name, g.ID)), acct)
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
	model.SortPolicies(mine)
	model.SortPolicies(infra)

	var sb strings.Builder
	sb.WriteString(tr(ctx, "🧭 Routes"))
	var rows [][]ikBtn
	for _, p := range mine {
		rows = append(rows, []ikBtn{{routeToggleIcon(p) + " " + routeName(ctx, p), "r:" + p.ID}})
	}
	if len(mine) == 0 {
		sb.WriteString(tr(ctx, "\n(no routes of your own yet)"))
	}
	if len(infra) > 0 {
		if acct.CanManageInfra() {
			// Admins own the baseline too. List it as tappable cards to manage.
			sb.WriteString(tr(ctx, "\n\nNetwork-wide rules:"))
			for _, p := range infra {
				rows = append(rows, []ikBtn{{"🔒 " + routeToggleIcon(p) + " " + routeName(ctx, p), "r:" + p.ID}})
			}
		} else {
			sb.WriteString(tr(ctx, "\n\nNetwork-wide rules (read-only):"))
			for _, p := range infra {
				sb.WriteString("\n🔒 " + routeName(ctx, p) + ": " + routeLabel(ctx, b, p))
			}
		}
	}
	rows = append(rows, []ikBtn{{tr(ctx, "➕ New rule"), "radd"}})
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
		return tr(ctx, "No such route."), inlineKeyboard([][]ikBtn{backTo(ctx, "Routes", "m:routes")})
	}
	text := b.routeBody(ctx, st, p)
	// A rule can name a category its source stopped offering, or never did, since
	// the source can be repointed under a rule that was valid when it was written.
	// Whether that costs anything depends entirely on whether a copy is held.
	frozen, missing := b.policyGeoGaps(ctx, p)
	if len(frozen) > 0 {
		text += "\n" + trf(ctx, "⚠ Withdrawn from the source: %s", strings.Join(frozen, ", "))
		text += "\n" + tr(ctx, "The rule works on the copy already downloaded, and that copy will not be updated again.")
	}
	if len(missing) > 0 {
		text += "\n" + trf(ctx, "⚠ Not in the source, and no copy is held: %s", strings.Join(missing, ", "))
		if !p.Disabled {
			text += "\n" + tr(ctx, "While this rule is on, the nodes it applies to receive no configuration updates.")
		}
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
		if wizardCanEdit(p) {
			rows = append(rows, []ikBtn{{tr(ctx, "✏️ Edit"), "redit:" + p.ID}})
		} else {
			// The wizard rebuilds a rule out of four match lists and would drop
			// whatever else this one carries. Editing it is left to the panel.
			// Everything that does not rewrite the rule's body stays available.
			text += tr(ctx, "\n(edited in the panel — this rule uses settings the bot has no screen for)")
		}
		if p.Disabled {
			rows = append(rows, []ikBtn{{tr(ctx, lblSwitchOn), "re:" + p.ID}})
		} else {
			rows = append(rows, []ikBtn{{tr(ctx, lblSwitchOff), "rx:" + p.ID}})
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
		rows = append(rows, []ikBtn{{tr(ctx, lblDelete), "rdel:" + p.ID}})
	} else {
		text += tr(ctx, "\n(read-only — a network-wide rule)")
	}
	rows = append(rows, backTo(ctx, "Routes", "m:routes"))
	return text, inlineKeyboard(rows)
}

// routeBody describes what a rule actually does, in full: the whole match, the group it
// is reserved for, what it does for everyone else and where it falls back to.
// Anything less and opening a rule is not enough to know what it will do.
func (b *Bot) routeBody(ctx context.Context, st model.State, p model.RoutePolicy) string {
	var sb strings.Builder
	sb.WriteString("🧭 " + routeName(ctx, p))
	if p.Disabled {
		sb.WriteString(tr(ctx, " — switched off"))
	}
	sb.WriteString("\n" + tr(ctx, "catches:"))
	for _, line := range conditionLines(ctx, p) {
		sb.WriteString("\n  " + line)
	}
	if g, ok := groupByID(st, p.AppliesToGroupID); ok {
		sb.WriteString(trf(ctx, "\nreserved for the group: %s", g.Name))
	}
	sb.WriteString(trf(ctx, "\nsends it: %s", b.routeActionText(ctx, p)))
	if p.Action == model.ActionExit && p.FallbackKind != "" {
		sb.WriteString(trf(ctx, "\nif that exit is out: %s", b.fallbackText(ctx, p)))
	}
	if p.OthersAction != "" {
		sb.WriteString(trf(ctx, "\nfor everyone else: %s", b.othersText(ctx, p)))
	}
	if p.Tier != model.TierExit {
		sb.WriteString(trf(ctx, "\nlevel: %s", tierWords(ctx, p.Tier)))
	}
	return sb.String()
}

// conditionLines renders the match as the sentence it is, one condition per
// line, joined by the words the panel's editor uses.
func conditionLines(ctx context.Context, p model.RoutePolicy) []string {
	conds := p.Match()
	if len(conds) == 0 {
		return []string{tr(ctx, "(nothing — this rule catches no traffic)")}
	}
	var out []string
	for i, c := range conds {
		head := condKindWords(ctx, c.Kind)
		if i > 0 {
			if c.Join == model.JoinExcept {
				head = exceptWords(ctx, c.Kind)
			} else {
				head = joinWords(ctx, c.Join) + " " + head
			}
		}
		out = append(out, head+": "+strings.Join(c.Values, ", "))
	}
	return out
}

func condKindWords(ctx context.Context, kind string) string {
	switch kind {
	case model.CondDomain:
		return tr(ctx, "domains")
	case model.CondGeoSite:
		return tr(ctx, "geosite categories")
	case model.CondGeoIP:
		return tr(ctx, "countries")
	case model.CondCIDR:
		return tr(ctx, "IP ranges")
	}
	return kind
}

// exceptWords is the whole "all but these" phrase and not a word glued to a
// noun: Russian declines the noun after it, English does not.
func exceptWords(ctx context.Context, kind string) string {
	switch kind {
	case model.CondDomain:
		return tr(ctx, "EXCEPT the domains")
	case model.CondGeoSite:
		return tr(ctx, "EXCEPT the geosite categories")
	case model.CondGeoIP:
		return tr(ctx, "EXCEPT the countries")
	case model.CondCIDR:
		return tr(ctx, "EXCEPT the IP ranges")
	}
	return tr(ctx, "EXCEPT") + " " + kind
}

func joinWords(ctx context.Context, join string) string {
	switch join {
	case model.JoinOr:
		return tr(ctx, "OR")
	case model.JoinExcept:
		return tr(ctx, "EXCEPT")
	}
	return tr(ctx, "AND")
}

// tierWords names a rule's level in the words the wizard offers it under. Fleet
// and guard are one level here, "the whole network", because they are one
// question to the reader: the difference between them is the action, which is
// printed on the next line anyway.
func tierWords(ctx context.Context, t model.RuleTier) string {
	if infraTier(t) {
		return tr(ctx, "the whole network")
	}
	return tr(ctx, "your own rules")
}

// infraTier reports whether a tier is one of the two the panel calls infra.
func infraTier(t model.RuleTier) bool {
	return t == model.TierFleet
}

// fallbackText says where the traffic goes when the chosen exit is out.
func (b *Bot) fallbackText(ctx context.Context, p model.RoutePolicy) string {
	switch p.FallbackKind {
	case model.FallbackExit:
		return trf(ctx, "through %s", b.exitName(ctx, p.FallbackExitID))
	case model.FallbackDirect:
		return tr(ctx, "out of the entry server itself")
	case model.FallbackBlock:
		return tr(ctx, "refused")
	}
	return string(p.FallbackKind)
}

// othersText says what the same traffic does for everyone the rule is not
// reserved for.
func (b *Bot) othersText(ctx context.Context, p model.RoutePolicy) string {
	return b.destination(ctx, p.OthersAction, p.OthersExitID)
}

// routeName gives a rule its name on a list or a card: its own, or what it
// catches when it has none.
func routeName(ctx context.Context, p model.RoutePolicy) string {
	if n := strings.TrimSpace(p.Name); n != "" {
		return n
	}
	return routeMatchText(p)
}

// writablePeers returns the caller's writable policies in the given tier in
// evaluation order, which lets the route card show a rule's rank and offer reorder
// against the neighbour it actually has.
func writablePeers(st model.State, acct model.AdminAccount, tier model.RuleTier) []model.RoutePolicy {
	var peers []model.RoutePolicy
	for _, p := range st.RoutePolicies {
		if p.Tier == tier && authz.CanWritePolicy(acct, p) {
			peers = append(peers, p)
		}
	}
	model.SortPolicies(peers)
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
// so precedence is adjustable from the card and not only in the panel.
func (b *Bot) routeReorder(ctx context.Context, acct model.AdminAccount, id string, up bool) (string, string) {
	st, err := b.scoped(ctx, acct)
	if err != nil {
		return errMsg(err), inlineKeyboard([][]ikBtn{backRow(ctx)})
	}
	p, ok := policyByID(st, id)
	if !ok || !authz.CanWritePolicy(acct, p) {
		return tr(ctx, "⛔ That route is read-only."), inlineKeyboard([][]ikBtn{backTo(ctx, "Routes", "m:routes")})
	}
	peers := writablePeers(st, acct, p.Tier)
	pos := indexOfPolicy(peers, id)
	swap := pos - 1
	if !up {
		swap = pos + 1
	}
	if pos < 0 || swap < 0 || swap >= len(peers) {
		return b.routeCard(ctx, acct, id) // already at the edge, no-op
	}
	a, bb := peers[pos], peers[swap]
	if a.Priority != bb.Priority {
		// Trading the two numbers leaves the set of priorities alone. No rule
		// outside this pair (another scope's, in the same tier) changes place.
		a.Priority, bb.Priority = bb.Priority, a.Priority
	} else {
		// Equal priorities are what the panel hands out by default, and two of
		// them cannot be swapped into a different order: their order comes from
		// the id. Step over the neighbour instead, and write only the rule that
		// moved.
		if up {
			a.Priority = bb.Priority + 1
		} else {
			a.Priority = bb.Priority - 1
		}
		bb = model.RoutePolicy{}
	}
	if err := b.store.UpsertRoutePolicy(ctx, a); err != nil {
		return errMsg(err), inlineKeyboard([][]ikBtn{backRow(ctx)})
	}
	if bb.ID != "" {
		if err := b.store.UpsertRoutePolicy(ctx, bb); err != nil {
			return errMsg(err), inlineKeyboard([][]ikBtn{backRow(ctx)})
		}
	}
	line := journal.Line("rule %s now runs after %s", ref(a.Name, a.ID), ref(peers[swap].Name, peers[swap].ID))
	if up {
		line = journal.Line("rule %s now runs before %s", ref(a.Name, a.ID), ref(peers[swap].Name, peers[swap].ID))
	}
	b.recordFor(ctx, model.SeverityInfo, line, acct)
	return b.routeCard(ctx, acct, id)
}

func (b *Bot) routeToggle(ctx context.Context, acct model.AdminAccount, id string, enable bool) (string, string) {
	st, err := b.scoped(ctx, acct)
	if err != nil {
		return errMsg(err), inlineKeyboard([][]ikBtn{backRow(ctx)})
	}
	p, ok := policyByID(st, id)
	if !ok || !authz.CanWritePolicy(acct, p) {
		return tr(ctx, "⛔ That route is read-only."), inlineKeyboard([][]ikBtn{backTo(ctx, "Routes", "m:routes")})
	}
	p.Disabled = !enable
	if err := b.store.UpsertRoutePolicy(ctx, p); err != nil {
		return errMsg(err), inlineKeyboard([][]ikBtn{backRow(ctx)})
	}
	line := journal.Line("rule %s switched off", ref(p.Name, p.ID))
	if enable {
		line = journal.Line("rule %s switched on", ref(p.Name, p.ID))
	}
	b.recordFor(ctx, model.SeverityInfo, line, acct)
	return b.routeCard(ctx, acct, id)
}

func (b *Bot) confirmDeleteRoute(ctx context.Context, acct model.AdminAccount, id string) (string, string) {
	st, err := b.scoped(ctx, acct)
	if err != nil {
		return errMsg(err), inlineKeyboard([][]ikBtn{backRow(ctx)})
	}
	p, ok := policyByID(st, id)
	if !ok || !authz.CanWritePolicy(acct, p) {
		return tr(ctx, "⛔ That route is read-only."), inlineKeyboard([][]ikBtn{backTo(ctx, "Routes", "m:routes")})
	}
	return trf(ctx, "⚠️ Delete rule %s?", routeLabel(ctx, b, p)),
		inlineKeyboard([][]ikBtn{{{tr(ctx, "🗑 Delete rule"), "rdz:" + p.ID}, {tr(ctx, lblCancel), "r:" + p.ID}}})
}

func (b *Bot) routeDelete(ctx context.Context, acct model.AdminAccount, id string) (string, string) {
	st, err := b.scoped(ctx, acct)
	if err != nil {
		return errMsg(err), inlineKeyboard([][]ikBtn{backRow(ctx)})
	}
	p, ok := policyByID(st, id)
	if !ok || !authz.CanWritePolicy(acct, p) {
		return tr(ctx, "⛔ That route is read-only."), inlineKeyboard([][]ikBtn{backTo(ctx, "Routes", "m:routes")})
	}
	if err := b.store.DeleteRoutePolicy(ctx, p.ID); err != nil {
		return errMsg(err), inlineKeyboard([][]ikBtn{backRow(ctx)})
	}
	b.recordFor(ctx, model.SeverityWarn, journal.Line("deleted rule %s", ref(p.Name, p.ID)), acct)
	return b.menuRoutes(ctx, acct)
}

// startNewRoute begins the route wizard. Admins choose the level first (so they
// can author the shared baseline); operators skip straight to the match builder,
// since a rule of theirs only ever applies to their own clients. The match
// builder is a hub: the operator adds one or more kinds (domain/geosite/geoip/
// cidr), which combine in one rule just like the panel, then continues to the
// action and a confirm summary.
func (b *Bot) startNewRoute(ctx context.Context, acct model.AdminAccount, fromID int64) (string, string) {
	if acct.CanManageInfra() {
		b.startConvo(ctx, fromID, &convo{kind: kNewRoute, step: rsLevel})
		return routeLevelPrompt(ctx), routeLevelKeyboard(ctx)
	}
	b.startConvo(ctx, fromID, &convo{kind: kNewRoute, step: rsMatch})
	return b.routeHub(ctx, &routeDraft{}, false)
}

// startEditRoute re-enters the wizard pre-pointed at an existing policy: the level,
// id, owner and priority are preserved; the existing match set is pre-loaded into
// the builder so the admin adjusts it instead of re-typing; the finish step upserts
// the same rule. Reuses the create flow end to end.
func (b *Bot) startEditRoute(ctx context.Context, acct model.AdminAccount, fromID int64, id string) (string, string) {
	st, err := b.scoped(ctx, acct)
	if err != nil {
		return errMsg(err), inlineKeyboard([][]ikBtn{backRow(ctx)})
	}
	p, ok := policyByID(st, id)
	if !ok || !authz.CanWritePolicy(acct, p) {
		return tr(ctx, "⛔ That route is read-only."), inlineKeyboard([][]ikBtn{backTo(ctx, "Routes", "m:routes")})
	}
	if !wizardCanEdit(p) {
		return tr(ctx, "🔒 This rule is edited in the panel.\n\nIt carries settings this bot has no screen for — a group it is reserved for, an exception, a reserve exit, or a separate answer for everyone else — and saving it from here would drop them. Turning it on or off, moving it and deleting it still work from the rule's card."),
			inlineKeyboard([][]ikBtn{backTo(ctx, "Routes", "m:routes")})
	}
	d := routeDraft{
		infra:   infraTier(p.Tier),
		domains: append([]string(nil), p.MatchDomains...),
		geosite: append([]string(nil), p.MatchGeoSite...),
		geoip:   append([]string(nil), p.MatchGeoIP...),
		cidrs:   append([]string(nil), p.MatchCIDRs...),
	}
	b.startConvo(ctx, fromID, &convo{kind: kNewRoute, step: rsMatch, editID: p.ID, rt: d})
	text, kb := b.routeHub(ctx, &d, true)
	return trf(ctx, "✏️ Editing route %q.\n\n", p.Name) + text, kb
}

// wizardCanEdit reports whether the route wizard can re-create a rule exactly as
// it stands. The wizard knows one shape: four match lists that narrow each other,
// one action, one exit. finishRoute rebuilds the whole policy out of that draft,
// so a rule carrying anything else (a group it is reserved for, an exception, a
// reserve exit, a separate answer for everyone else, or a match those four lists
// cannot spell: alternatives, one kind used twice) would come back simplified,
// and the nodes would then enforce the simplified rule.
//
// The match test is a round trip through the same two functions the store uses to
// keep the legacy columns readable: flatten the conditions into the four lists,
// expand them again, and see whether the rule comes back unchanged.
func wizardCanEdit(p model.RoutePolicy) bool {
	if p.AppliesToGroupID != "" || len(p.ExcludeDomains) > 0 ||
		p.FallbackKind != "" || p.OthersAction != "" {
		return false
	}
	flat := model.RoutePolicy{
		MatchGeoIP:   p.MatchGeoIP,
		MatchGeoSite: p.MatchGeoSite,
		MatchDomains: p.MatchDomains,
		MatchCIDRs:   p.MatchCIDRs,
	}
	return sameConditions(p.Match(), model.LegacyConditions(flat))
}

// sameConditions compares two match expressions. The first condition's join is
// ignored, as it is everywhere else, since there is nothing to its left to join to.
func sameConditions(a, b []model.RouteCondition) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Kind != b[i].Kind {
			return false
		}
		if i > 0 && a[i].Join != b[i].Join {
			return false
		}
		av, bv := trimValues(a[i].Values), trimValues(b[i].Values)
		if len(av) != len(bv) {
			return false
		}
		for j := range av {
			if av[j] != bv[j] {
				return false
			}
		}
	}
	return true
}

// trimValues drops blanks and surrounding space, matching what the model does to
// a condition's values, which keeps a stray comma from failing the round trip.
func trimValues(in []string) []string {
	out := make([]string, 0, len(in))
	for _, v := range in {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// routePickLevel handles an rt: tap (the admin's level choice) and moves to the
// match builder.
func (b *Bot) routePickLevel(ctx context.Context, acct model.AdminAccount, fromID int64, levelStr string) (string, string) {
	c := b.convoFor(fromID)
	if c == nil || c.kind != kNewRoute {
		return tr(ctx, "That form expired. Tap ➕ New route to start over."), inlineKeyboard([][]ikBtn{backRow(ctx)})
	}
	infra, ok := parseLevel(levelStr)
	if !ok {
		return routeLevelPrompt(ctx), routeLevelKeyboard(ctx)
	}
	c.rt.infra = infra
	c.step = rsMatch
	return b.routeHub(ctx, &c.rt, c.editID != "")
}

// routePickMatchKind handles an rmk: tap: open the value screen for that kind, where
// the values already in use are one-tap buttons and anything else is typed.
func (b *Bot) routePickMatchKind(ctx context.Context, acct model.AdminAccount, fromID int64, kind string) (string, string) {
	c := b.convoFor(fromID)
	if c == nil || c.kind != kNewRoute {
		return tr(ctx, "That form expired. Tap ➕ New route to start over."), inlineKeyboard([][]ikBtn{backRow(ctx)})
	}
	if !validMatchKind(kind) {
		return b.routeHub(ctx, &c.rt, c.editID != "")
	}
	c.rt.editKind = kind
	c.rt.refused, c.rt.refusedKind = nil, "" // a name refused on one screen is not news on the next
	c.step = rsMatchVals
	return b.routeValues(ctx, acct, kind, &c.rt)
}

// routeQuickAdd handles an rqa:<kind>:<value> tap: toggle a one-tap value in the
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
	c.rt.editKind = kind
	c.step = rsMatchVals
	if c.rt.has(kind, val) {
		// Taking a value out is always allowed: a rule that already names a
		// withdrawn category is exactly the one that has to be edited.
		c.rt.refused, c.rt.refusedKind = nil, ""
		c.rt.toggle(kind, val)
		return b.routeValues(ctx, acct, kind, &c.rt)
	}
	// The buttons only ever carry names out of the catalogue, but a callback is
	// whatever arrives: a tap goes through the same check as a typed name.
	for _, v := range b.checkGeoValues(ctx, &c.rt, []string{val}) {
		c.rt.toggle(kind, v)
	}
	return b.routeValues(ctx, acct, kind, &c.rt)
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
	return routeActionPrompt(ctx), routeActionKeyboard(ctx)
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
		c.rt.refused, c.rt.refusedKind = nil, ""
		return b.routeHub(ctx, &c.rt, c.editID != "")
	case rsMatch: // hub -> level (admin) or leave the wizard (operator)
		if acct.CanManageInfra() && c.editID == "" {
			c.step = rsLevel
			return routeLevelPrompt(ctx), routeLevelKeyboard(ctx)
		}
		b.clearConvo(fromID)
		return b.menuRoutes(ctx, acct)
	case rsAction: // action -> back to the hub
		c.step = rsMatch
		return b.routeHub(ctx, &c.rt, c.editID != "")
	case rsExit: // exit pick -> back to the action step
		c.step = rsAction
		return routeActionPrompt(ctx), routeActionKeyboard(ctx)
	case rsConfirm: // confirm -> back to the action (or exit) step
		if c.rt.action == model.ActionExit {
			c.step = rsExit
			return tr(ctx, "Choose an exit.\nTap one, or reply with its number:\n") + b.exitMenuText(ctx), b.routeExitKeyboard(ctx)
		}
		c.step = rsAction
		return routeActionPrompt(ctx), routeActionKeyboard(ctx)
	default: // rsLevel -> leave the wizard
		b.clearConvo(fromID)
		return b.menuRoutes(ctx, acct)
	}
}

func (b *Bot) advanceNewRoute(ctx context.Context, acct model.AdminAccount, fromID int64, c *convo, input string) (string, string) {
	switch c.step {
	case rsLevel: // admin must tap a level
		return routeLevelPrompt(ctx), routeLevelKeyboard(ctx)
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
			return b.routeValues(ctx, acct, c.rt.editKind, &c.rt)
		}
		c.rt.addValues(c.rt.editKind, b.checkGeoValues(ctx, &c.rt, vals))
		return b.routeValues(ctx, acct, c.rt.editKind, &c.rt)
	case rsAction: // typed fallback for the action buttons
		switch strings.ToLower(strings.TrimSpace(input)) {
		case "exit", "direct", "block":
			return b.applyRouteAction(ctx, acct, fromID, c, strings.ToLower(strings.TrimSpace(input)))
		default:
			return tr(ctx, "Tap an action below, or reply `exit`, `direct` or `block`."), routeActionKeyboard(ctx)
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
// direct/block go straight to the confirm step.
func (b *Bot) applyRouteAction(ctx context.Context, acct model.AdminAccount, fromID int64, c *convo, action string) (string, string) {
	switch action {
	case "exit":
		c.rt.action = model.ActionExit
		c.step = rsExit
		return tr(ctx, "Choose an exit.\nTap one, or reply with its number:\n") + b.exitMenuText(ctx), b.routeExitKeyboard(ctx)
	case "direct":
		c.rt.action = model.ActionDirect
	case "block":
		c.rt.action = model.ActionBlock
	default:
		return tr(ctx, "Tap an action below, or reply `exit`, `direct` or `block`."), routeActionKeyboard(ctx)
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
// instead of a dead-end "open the menu" line.
func (b *Bot) finishRoute(ctx context.Context, acct model.AdminAccount, fromID int64, c *convo) (string, string) {
	editing := c.editID != ""
	st, err := b.store.LoadState(ctx)
	if err != nil {
		b.clearConvo(fromID)
		return errMsg(err), inlineKeyboard([][]ikBtn{backRow(ctx)})
	}
	p := model.RoutePolicy{
		Name:    routeAutoName(c.rt),
		Tier:    c.rt.tier(),
		Action:  c.rt.action,
		OwnerID: ownerFor(acct),
	}
	if c.editID != "" { // edit: keep identity/order, refresh the fields
		// The tier is not carried over; it follows the level and the action, and an
		// infra rule re-pointed at an exit becomes a mandate for the whole network
		// and one that stops routing there becomes the safety net, the same way the
		// panel re-derives it on every save.
		if old, ok := policyByID(st, c.editID); ok {
			p.ID, p.Priority, p.OwnerID, p.Disabled = old.ID, old.Priority, old.OwnerID, old.Disabled
		}
	}
	if p.ID == "" {
		p.ID = slugID("r", string(c.rt.action)+"-"+draftFirstValue(c.rt))
		p.Priority = lastPriority(st, p.Tier)
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
	line := journal.Line("created rule %s", ref(p.Name, p.ID))
	if editing {
		line = journal.Line("updated rule %s", ref(p.Name, p.ID))
	}
	b.recordFor(ctx, model.SeverityInfo, line, acct)
	banner := tr(ctx, "✓ Rule created.")
	if editing {
		banner = tr(ctx, "✓ Rule updated.")
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

// routeLevelPrompt asks, in plain words, the one question the panel asks: who
// the rule applies to. It is the whole of what the admin decides here, since
// what the rule is stored as follows from this answer and the action.
func routeLevelPrompt(ctx context.Context) string {
	return tr(ctx, "🧭 New rule — who does it apply to?\n🚪 My rules — a rule of your own (most common)\n🌐 Whole network — applies to every group")
}

func routeLevelKeyboard(ctx context.Context) string {
	return inlineKeyboard([][]ikBtn{
		{{tr(ctx, "🚪 My rules"), "rt:mine"}},
		{{tr(ctx, "🌐 Whole network"), "rt:infra"}},
		{{tr(ctx, lblBack), "rback"}, {tr(ctx, lblCancel), "cx"}},
	})
}

// routeHub renders the match-builder hub: the match set accumulated so far plus a
// button to add each kind (they combine in one rule, like the panel) and a Continue
// once at least one value is set. Typed input at the hub is added as domains.
func (b *Bot) routeHub(ctx context.Context, d *routeDraft, editing bool) (string, string) {
	var sb strings.Builder
	sb.WriteString(tr(ctx, "🧭 What traffic should this rule catch?"))
	if d.total() == 0 {
		sb.WriteString(tr(ctx, "\nAdd one or more kinds below. Several kinds combine in one rule (domain AND geosite AND …)."))
	} else {
		sb.WriteString(tr(ctx, " — so far:\n"))
		sb.WriteString(draftMatchLines(ctx, d))
	}
	rows := [][]ikBtn{
		{{routeAddLabel(ctx, "🌐 Domain", len(d.domains)), "rmk:domain"}, {routeAddLabel(ctx, "🗂 Geosite", len(d.geosite)), "rmk:geosite"}},
		{{routeAddLabel(ctx, "📍 Geo-IP", len(d.geoip)), "rmk:geoip"}, {routeAddLabel(ctx, "🔢 CIDR", len(d.cidrs)), "rmk:cidr"}},
	}
	if d.total() > 0 {
		rows = append(rows, []ikBtn{{tr(ctx, "➡ Continue"), "rcont"}})
	}
	rows = append(rows, []ikBtn{{tr(ctx, lblBack), "rback"}, {tr(ctx, lblCancel), "cx"}})
	return sb.String(), inlineKeyboard(rows)
}

// routeAddLabel tags an add-kind button with the count already chosen for it.
// catalogLabel names the button that opens the catalogue for a kind.
func catalogLabel(kind string) string {
	if kind == model.CondGeoIP {
		return "📍 Country catalogue"
	}
	return "🗂 Geosite catalogue"
}

func routeAddLabel(ctx context.Context, s string, n int) string {
	if n > 0 {
		return fmt.Sprintf("%s (%d)", tr(ctx, s), n)
	}
	return tr(ctx, s)
}

// draftMatchLines lists the draft's match set, one line per non-empty kind (the
// field names are technical, shown verbatim as in the panel).
func draftMatchLines(ctx context.Context, d *routeDraft) string {
	var lines []string
	add := func(kind string, vals []string) {
		if len(vals) > 0 {
			lines = append(lines, "  "+condKindWords(ctx, kind)+": "+strings.Join(vals, ", "))
		}
	}
	add(model.CondDomain, d.domains)
	add(model.CondGeoSite, d.geosite)
	add(model.CondGeoIP, d.geoip)
	add(model.CondCIDR, d.cidrs)
	return strings.Join(lines, "\n")
}

// routeValues renders the value step for one match kind: the prompt, what the
// draft already holds, and the keyboard. For geosite/geoip it is built around
// the catalogue the connected source last published: the names that exist and
// the ones this panel's rules already use.
func (b *Bot) routeValues(ctx context.Context, acct model.AdminAccount, kind string, d *routeDraft) (string, string) {
	var cat geoCat
	var quick []string
	if kind == model.CondGeoSite || kind == model.CondGeoIP {
		cat = b.geoCatalog(ctx, kind)
		if st, err := b.scoped(ctx, acct); err == nil {
			quick = geoInUse(st, kind, cat, maxGeoQuickAdds)
		}
	}
	return routeMatchValuesPrompt(ctx, kind, d, cat), routeMatchValuesKeyboard(ctx, kind, d, cat, quick)
}

// routeMatchValuesPrompt asks for values for the chosen kind. Values are typed,
// comma-separated, with no "geosite:"/"cidr:" prefix to remember; for the two geo
// kinds the prompt also says what the source offers to choose from, because a
// name that is not in it cannot be routed by.
func routeMatchValuesPrompt(ctx context.Context, kind string, d *routeDraft, cat geoCat) string {
	var head string
	switch kind {
	case "geosite":
		head = tr(ctx, "🗂 Geosite — type a category name, comma-separated for several.\ne.g. google, netflix, category-ads")
	case "geoip":
		head = tr(ctx, "📍 Geo-IP — type a country code, comma-separated for several.\ne.g. ru, us")
	case "cidr":
		head = tr(ctx, "🔢 Enter CIDR range(s), comma-separated for several.\ne.g. 10.0.0.0/8, 192.168.0.0/16")
	default:
		head = tr(ctx, "🌐 Enter domain(s), comma-separated for several.\ne.g. netflix.com, *.google.com")
	}
	if line := catalogLine(ctx, kind, cat); line != "" {
		head += "\n" + line
	}
	if sel := *d.slot(kind); len(sel) > 0 {
		head += "\n\n" + trf(ctx, "added: %s", strings.Join(sel, ", "))
	}
	if len(d.refused) > 0 && d.refusedKind == kind {
		head += "\n\n" + trf(ctx, "⚠ Not added — the source has no %s.", strings.Join(d.refused, ", "))
		head += "\n" + tr(ctx, "Check the spelling. If the category is newer than the list, a check on the Geo databases tab refreshes it.")
	}
	return head
}

// catalogLine says where the names come from. An operator who cannot find a
// category has to be able to tell a list that is missing from a category that
// is: the panel says the same thing above its own rule form.
func catalogLine(ctx context.Context, kind string, cat geoCat) string {
	if kind != model.CondGeoSite && kind != model.CondGeoIP {
		return ""
	}
	if !cat.have() {
		if cat.err != "" {
			return tr(ctx, "The last check of the source failed, so the panel has no list to check a name against. The Geo databases tab says why.")
		}
		return tr(ctx, "The panel has no list from the source yet, so a name typed here cannot be checked against it. It takes one by itself within minutes of starting.")
	}
	var line string
	if kind == model.CondGeoSite {
		line = trf(ctx, "The source offers %d categories", cat.count())
	} else {
		line = trf(ctx, "The source offers %d countries", cat.count())
	}
	if cat.at != nil {
		line += trf(ctx, ", checked %s", cat.at.UTC().Format(dateOnly))
	}
	line += "."
	// A failed check leaves the last good list standing (store.RecordCatalogError),
	// which is the right thing to do and the one case where the date above is
	// older than it looks.
	if cat.err != "" {
		line += " " + tr(ctx, "The check since then did not get through, so the source may have moved on.")
	}
	return line
}

// routeMatchValuesKeyboard renders the one-tap values for a kind (a ✓ marks the
// ones already chosen), then ✓ Done (back to the hub) and ✖ Cancel.
func routeMatchValuesKeyboard(ctx context.Context, kind string, d *routeDraft, cat geoCat, quick []string) string {
	// What the draft holds comes first, each one a button that takes it back out:
	// a value that cannot be removed where it was added can only be escaped by
	// abandoning the whole rule, and an edited rule can arrive already naming a
	// category its source has since stopped offering, marked ⚠ here.
	var vals []string
	for _, v := range *d.slot(kind) {
		label := "✓ " + v
		if cat.have() {
			if _, ok := cat.resolve(v); !ok {
				label = "⚠ " + v
			}
		}
		vals = append(vals, label)
	}
	for _, v := range quick {
		if !d.has(kind, v) {
			vals = append(vals, v)
		}
	}
	var rows [][]ikBtn
	for i := 0; i < len(vals); i += 2 {
		var row []ikBtn
		for j := i; j < i+2 && j < len(vals); j++ {
			v := strings.TrimPrefix(strings.TrimPrefix(vals[j], "✓ "), "⚠ ")
			if len("rqa:"+kind+":"+v) > maxCallbackLen {
				continue // too long to address; the "added" line above still names it
			}
			row = append(row, ikBtn{vals[j], "rqa:" + kind + ":" + v})
		}
		if len(row) > 0 {
			rows = append(rows, row)
		}
	}
	// The catalogue itself, for the categories no keyboard can hold: the button
	// opens this chat's search box already pointed at the right list.
	if (kind == model.CondGeoSite || kind == model.CondGeoIP) && cat.have() {
		rows = append(rows, []ikBtn{searchBtn(tr(ctx, catalogLabel(kind)), kind+" ")})
	}
	done := tr(ctx, lblDone)
	if n := len(*d.slot(kind)); n > 0 {
		done = fmt.Sprintf("%s · %d", done, n)
	}
	rows = append(rows, []ikBtn{{done, "rback"}})
	rows = append(rows, []ikBtn{{tr(ctx, lblCancel), "cx"}})
	return inlineKeyboard(rows)
}

func routeActionPrompt(ctx context.Context) string {
	return tr(ctx, "Action?\nTap one, or reply `exit`, `direct` or `block`.")
}

func routeActionKeyboard(ctx context.Context) string {
	rows := [][]ikBtn{{{tr(ctx, "🚪 Exit"), "ra:exit"}}}
	rows = append(rows, []ikBtn{{tr(ctx, "➡ Direct"), "ra:direct"}, {tr(ctx, "🚫 Block"), "ra:block"}})
	rows = append(rows, []ikBtn{{tr(ctx, lblBack), "rback"}, {tr(ctx, lblCancel), "cx"}})
	return inlineKeyboard(rows)
}

func (b *Bot) routeExitKeyboard(ctx context.Context) string {
	var rows [][]ikBtn
	for _, n := range b.exitNodes(ctx) {
		rows = append(rows, []ikBtn{{"🚪 " + n.Name, "rxp:" + n.ID}})
	}
	rows = append(rows, []ikBtn{{tr(ctx, lblBack), "rback"}, {tr(ctx, lblCancel), "cx"}})
	return inlineKeyboard(rows)
}

// routeSummary renders the review card shown before the rule is written. The
// operator confirms the whole rule at once instead of it being created silently.
func (b *Bot) routeSummary(ctx context.Context, d routeDraft, editing bool) string {
	action := b.destination(ctx, d.action, d.exitID)
	tier := d.tier()
	body := trf(ctx, "🧭 Review the route:\nmatch:\n%s\nsends it: %s", draftMatchLines(ctx, &d), action)
	// The level only appears where it was asked for: an operator never chose one.
	if tier != model.TierExit {
		body += trf(ctx, "\nlevel: %s", tierWords(ctx, tier))
	}
	// The last look before the rule is written: a category the source does not
	// offer is named here, where it can still be taken back out. The source can
	// also have been repointed since an edited rule was written, which is the
	// other way a rule ends up naming a category that is no longer there.
	frozen, missing := b.draftGeoGaps(ctx, d)
	if len(frozen) > 0 {
		body += "\n\n" + trf(ctx, "⚠ Withdrawn from the source: %s", strings.Join(frozen, ", "))
		body += "\n" + tr(ctx, "The rule works on the copy already downloaded, and that copy will not be updated again.")
	}
	if len(missing) > 0 {
		body += "\n\n" + trf(ctx, "⚠ Not in the source, and no copy is held: %s", strings.Join(missing, ", "))
		body += "\n" + tr(ctx, "A node whose rule names it receives no configuration updates at all.")
	}
	if editing {
		return body + tr(ctx, "\n\nSave these changes?")
	}
	return body + tr(ctx, "\n\nCreate this route?")
}

func routeConfirmKeyboard(ctx context.Context, editing bool) string {
	label := tr(ctx, "✓ Create rule")
	if editing {
		label = tr(ctx, lblSave)
	}
	return inlineKeyboard([][]ikBtn{
		{{label, "rok"}},
		{{tr(ctx, lblBack), "rback"}, {tr(ctx, lblCancel), "cx"}},
	})
}

func routesBackKeyboard(ctx context.Context) string {
	return inlineKeyboard([][]ikBtn{backTo(ctx, "Routes", "m:routes")})
}

// parseLevel maps a level button payload to the draft's infra flag.
func parseLevel(s string) (infra bool, ok bool) {
	switch s {
	case "mine":
		return false, true
	case "infra":
		return true, true
	}
	return false, false
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
	}
	// Only an admin has a panel login to recover; an operator's sole surface is
	// this bot, and the button would offer them nothing.
	if acct.Role.IsAdmin() {
		rows = append(rows, []ikBtn{{tr(ctx, "🔑 Recover panel access"), "pwrec"}})
	}
	rows = append(rows, backRow(ctx))
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
	// Where a node-down alert lands is an operational setting, not a preference:
	// switched off here, nobody is told the next time an exit stops answering.
	line := journal.Line("Telegram alerts for %q switched off", acct.Username)
	if chat != "" {
		line = journal.Line("alerts for %q now go to a Telegram chat", acct.Username)
	}
	b.recordFor(ctx, model.SeverityInfo, line, acct)
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
		return tr(ctx, "No such user."), inlineKeyboard([][]ikBtn{backTo(ctx, "Clients", "m:users")})
	}
	return trf(ctx, "⚠️ Delete %s?\nThe access ends at once and the connection cannot be given back.", displayName(u)),
		inlineKeyboard([][]ikBtn{{{tr(ctx, "🗑 Delete client"), "udz:" + u.ID}, {tr(ctx, lblCancel), "u:" + u.ID}}})
}

func (b *Bot) userDelete(ctx context.Context, acct model.AdminAccount, id string) (string, string) {
	st, err := b.scoped(ctx, acct)
	if err != nil {
		return errMsg(err), inlineKeyboard([][]ikBtn{backRow(ctx)})
	}
	u, ok := userByID(st, id)
	if !ok || !authz.CanWriteOwned(acct, u.OwnerID) {
		return tr(ctx, "No such user."), inlineKeyboard([][]ikBtn{backTo(ctx, "Clients", "m:users")})
	}
	if err := b.store.DeleteUser(ctx, u.ID); err != nil {
		return errMsg(err), inlineKeyboard([][]ikBtn{backRow(ctx)})
	}
	b.recordFor(ctx, model.SeverityWarn, journal.Line("deleted client %s", ref(displayName(u), u.ID)), acct)
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
	text := tr(ctx, "All scopes — tap to inspect (read-only)")
	if len(nss) == 0 {
		text = tr(ctx, "No clients in any scope yet.")
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
	sb.WriteString(trf(ctx, "🔭 %s — clients %d · groups %d · rules %d", label, len(users), groups, routes))
	for _, u := range users {
		sb.WriteString("\n" + accessOf(u, b.now()).glyph + " " + displayName(u))
	}
	return sb.String(), inlineKeyboard([][]ikBtn{backTo(ctx, "Back", "m:lens")})
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

// exitNodes lists the fleet's exit nodes (id, name and whether they are drained)
// from the raw state. This is a deliberate, routing-only disclosure to a scoped
// account: to author an exit-tier route or choose where a group's traffic leaves
// by, the namespace owner must be able to name the exits it targets. It exposes
// no entry nodes, IPs, health, or control plane (cf. infraAggregate, the other
// ScopeState bypass).
//
// The maintenance flag is part of that answer and not an extra: an exit in
// maintenance does not carry the group's traffic, it leaves from the entry
// server instead. Offering it as an ordinary choice and saying nothing is how
// somebody ends up wondering why their group appears from the wrong address.
func (b *Bot) exitNodes(ctx context.Context) []model.Node {
	st, err := b.store.LoadState(ctx)
	if err != nil {
		return nil
	}
	var out []model.Node
	for _, n := range st.Nodes {
		if n.IsExit() {
			out = append(out, model.Node{ID: n.ID, Name: n.Name, Maintenance: n.Maintenance})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// exitNode is one exit by id, for the screens that need to say something about
// the one that was chosen.
func (b *Bot) exitNode(ctx context.Context, id string) (model.Node, bool) {
	if id == "" || id == "-" {
		return model.Node{}, false
	}
	for _, n := range b.exitNodes(ctx) {
		if n.ID == id {
			return n, true
		}
	}
	return model.Node{}, false
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
		return tr(ctx, "(no exit nodes in the network yet — ask an admin)")
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

// lastPriority returns a priority below every rule of the same tier, putting a new
// rule is checked after the ones already there.
//
// Evaluation runs from the highest priority down. Landing above the highest
// would preempt every rule in the tier, reservations somebody else wrote
// included. Landing at the end is the safer end to be wrong on: a new rule that
// answers too late does nothing, while one that answers too early quietly takes
// traffic from everything below it.
func lastPriority(st model.State, tier model.RuleTier) int {
	min, seen := 0, false
	for _, p := range st.RoutePolicies {
		if p.Tier != tier {
			continue
		}
		if !seen || p.Priority < min {
			min, seen = p.Priority, true
		}
	}
	if !seen {
		return defaultPriority
	}
	return min - 1
}

// defaultPriority is where the panel's rule form starts a new rule, and what
// the first rule of a tier gets here; a rule created in either place lands on
// the same number.
const defaultPriority = 100

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
	return b.destination(ctx, p.Action, p.ExitNodeID)
}

// destination is where traffic ends up, in the one set of words the card, the
// summary and the list all use.
func (b *Bot) destination(ctx context.Context, action model.RuleAction, exitID string) string {
	switch action {
	case model.ActionExit:
		return trf(ctx, "through %s", b.exitName(ctx, exitID))
	case model.ActionDirect:
		return tr(ctx, "out of the entry server itself")
	case model.ActionBlock:
		return tr(ctx, "refused")
	}
	return string(action)
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
	return prefix + "-" + clampSlug(slug) + "-" + randomHex(3)
}
