package render

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"sort"
	"strings"
	"time"

	"trustpanel/internal/core/journal"
	"trustpanel/internal/core/model"
)

// ---- sing-box config subset (JSON tags match sing-box v1.13.13) ----

type sbConfig struct {
	Log          map[string]any `json:"log,omitempty"`
	DNS          map[string]any `json:"dns,omitempty"`
	Inbounds     []any          `json:"inbounds"`
	Outbounds    []any          `json:"outbounds"`
	Route        sbRoute        `json:"route"`
	Experimental map[string]any `json:"experimental,omitempty"`
}

type sbRoute struct {
	Rules   []sbRule `json:"rules"`
	RuleSet []any    `json:"rule_set,omitempty"`
	Final   string   `json:"final,omitempty"`
}

// sbRule: action omitted => "route" (uses Outbound); action "reject" blocks.
type sbRule struct {
	// A logical rule carries no matchers of its own: it combines the rules
	// nested under it with Mode ("and" / "or"), and Invert flips the result.
	// That is how a condition list with alternatives and exclusions is
	// expressed, since a flat rule's fields can only intersect.
	Type   string   `json:"type,omitempty"`
	Mode   string   `json:"mode,omitempty"`
	Rules  []sbRule `json:"rules,omitempty"`
	Invert bool     `json:"invert,omitempty"`

	AuthUser     []string `json:"auth_user,omitempty"`
	DomainSuffix []string `json:"domain_suffix,omitempty"`
	IPCIDR       []string `json:"ip_cidr,omitempty"`
	RuleSet      []string `json:"rule_set,omitempty"`
	Action       string   `json:"action,omitempty"`
	Outbound     string   `json:"outbound,omitempty"`
}

// logical wraps two rules under one combiner.
func logical(mode string, a, b sbRule) sbRule {
	return sbRule{Type: "logical", Mode: mode, Rules: []sbRule{a, b}}
}

type sbUserPass struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type sbMixedInbound struct {
	Type       string       `json:"type"`
	Tag        string       `json:"tag"`
	Listen     string       `json:"listen"`
	ListenPort int          `json:"listen_port"`
	Users      []sbUserPass `json:"users,omitempty"`
}

type sbDirectOutbound struct {
	Type string `json:"type"`
	Tag  string `json:"tag"`
}

type sbVLESSOutbound struct {
	Type       string        `json:"type"`
	Tag        string        `json:"tag"`
	Server     string        `json:"server"`
	ServerPort int           `json:"server_port"`
	UUID       string        `json:"uuid"`
	Flow       string        `json:"flow,omitempty"`
	TLS        sbOutboundTLS `json:"tls"`
}

type sbOutboundTLS struct {
	Enabled    bool               `json:"enabled"`
	ServerName string             `json:"server_name"`
	UTLS       *sbUTLS            `json:"utls,omitempty"`
	Reality    *sbOutboundReality `json:"reality,omitempty"`
}

type sbUTLS struct {
	Enabled     bool   `json:"enabled"`
	Fingerprint string `json:"fingerprint"`
}

type sbOutboundReality struct {
	Enabled   bool   `json:"enabled"`
	PublicKey string `json:"public_key"`
	ShortID   string `json:"short_id"`
}

type sbVLESSUser struct {
	Name string `json:"name"`
	UUID string `json:"uuid"`
	Flow string `json:"flow,omitempty"`
}

type sbVLESSInbound struct {
	Type       string        `json:"type"`
	Tag        string        `json:"tag"`
	Listen     string        `json:"listen"`
	ListenPort int           `json:"listen_port"`
	Users      []sbVLESSUser `json:"users"`
	TLS        sbInboundTLS  `json:"tls"`
}

type sbInboundTLS struct {
	Enabled    bool              `json:"enabled"`
	ServerName string            `json:"server_name"`
	Reality    *sbInboundReality `json:"reality"`
}

type sbInboundReality struct {
	Enabled    bool               `json:"enabled"`
	Handshake  sbRealityHandshake `json:"handshake"`
	PrivateKey string             `json:"private_key"`
	ShortID    []string           `json:"short_id"`
}

type sbRealityHandshake struct {
	Server     string `json:"server"`
	ServerPort int    `json:"server_port"`
}

// ---- exit render ----

func renderExit(node model.Node, opts Options) (CompiledNode, error) {
	if node.DialIn == nil {
		return CompiledNode{}, fmt.Errorf("exit node %q has no dial_in", node.ID)
	}
	d := node.DialIn
	cfg := sbConfig{
		Log: map[string]any{"level": "warn"},
		Inbounds: []any{sbVLESSInbound{
			Type: "vless", Tag: "vless-in", Listen: "0.0.0.0", ListenPort: d.Port,
			Users: []sbVLESSUser{{Name: node.ID, UUID: d.UUID, Flow: realityFlow}},
			TLS: sbInboundTLS{
				Enabled: true, ServerName: d.TargetSNI,
				Reality: &sbInboundReality{
					Enabled:    true,
					Handshake:  sbRealityHandshake{Server: d.TargetSNI, ServerPort: 443},
					PrivateKey: d.PrivKey,
					ShortID:    []string{d.ShortID},
				},
			},
		}},
		Outbounds: []any{sbDirectOutbound{Type: "direct", Tag: "freedom"}},
		Route:     sbRoute{Rules: []sbRule{}, Final: "freedom"},
	}
	body, err := marshalConfig(cfg)
	if err != nil {
		return CompiledNode{}, err
	}
	return CompiledNode{
		NodeID: node.ID, Role: model.RoleExit,
		Files: []Artifact{newArtifact(singboxPath, 0o600, body)},
	}, nil
}

// ---- entry render ----

func renderEntry(state model.State, node model.Node, at time.Time, opts Options) (CompiledNode, error) {
	active := state.ActiveUsers(at)

	// One projection -> sing-box inbound users + v2ray stats users.
	inboundUsers := make([]sbUserPass, 0, len(active))
	statsUsers := make([]string, 0, len(active))
	membership := map[string][]string{}   // groupID -> usernames (active, sorted; active is pre-sorted)
	ownerMembers := map[string][]string{} // ownerID -> usernames (active, sorted)
	for _, u := range active {
		inboundUsers = append(inboundUsers, sbUserPass{Username: u.Username, Password: u.Secret})
		statsUsers = append(statsUsers, u.Username)
		membership[u.GroupID] = append(membership[u.GroupID], u.Username)
		ownerMembers[u.OwnerID] = append(ownerMembers[u.OwnerID], u.Username)
	}

	groupByID := map[string]model.Group{}
	for _, g := range state.Groups {
		groupByID[g.ID] = g
	}

	c := &entryCompiler{state: state, membership: membership, ownerMembers: ownerMembers, groupByID: groupByID}
	rules, requiredExits := c.compileRules()

	// Outbounds: direct + one vless+reality per referenced exit.
	outbounds := []any{sbDirectOutbound{Type: "direct", Tag: "direct"}}
	for _, exitID := range sortedKeys(requiredExits) {
		ob, err := vlessOutboundFor(state, exitID)
		if err != nil {
			return CompiledNode{}, err
		}
		outbounds = append(outbounds, ob)
	}

	// The mixed inbound authenticates every client against the active-user set.
	// With no active users that set is empty, and sing-box reads an empty users
	// list as "no auth required": an open proxy on the loopback listener. There
	// is nothing to carry in that state (TrustTunnel has no client to forward), so
	// the listener is dropped entirely rather than left open to local callers.
	inbounds := []any{}
	if len(inboundUsers) > 0 {
		inbounds = append(inbounds, sbMixedInbound{
			Type: "mixed", Tag: "trusttunnel-in",
			Listen: opts.MixedListen, ListenPort: opts.MixedPort, Users: inboundUsers,
		})
	}

	cfg := sbConfig{
		Log:       map[string]any{"level": "warn"},
		Inbounds:  inbounds,
		Outbounds: outbounds,
		Route:     sbRoute{Rules: rules, RuleSet: c.ruleSetDefs(opts.RuleSetDir), Final: "direct"},
		Experimental: map[string]any{
			"v2ray_api": map[string]any{
				"listen": opts.V2RayAPIListen,
				"stats":  map[string]any{"enabled": true, "users": statsUsers},
			},
		},
	}
	// The resolve action used by geoip/cidr policies needs a DNS server. Route it
	// over TLS so the node's routing lookups don't travel as plaintext queries an
	// on-path observer could read or fingerprint.
	if c.needsResolve() {
		cfg.DNS = map[string]any{"servers": []any{
			map[string]any{"type": "tls", "tag": "resolver", "server": "1.1.1.1",
				"tls": map[string]any{"enabled": true, "server_name": "one.one.one.one"}},
		}}
	}

	body, err := marshalConfig(cfg)
	if err != nil {
		return CompiledNode{}, err
	}
	files := []Artifact{
		newArtifact(credentialsPath, 0o600, renderCredentials(active)),
		newArtifact(singboxPath, 0o600, body),
	}
	ttFiles, ttWarnings := renderTrustTunnel(state, node, opts)
	files = append(files, ttFiles...)

	out := CompiledNode{
		NodeID: node.ID, Role: model.RoleEntry,
		Files:            files,
		RequiredRuleSets: c.requiredRuleSets(),
		Warnings:         append(c.warnings, ttWarnings...),
	}
	return out, nil
}

type entryCompiler struct {
	state         model.State
	membership    map[string][]string // groupID -> active usernames
	ownerMembers  map[string][]string // ownerID (namespace) -> active usernames
	groupByID     map[string]model.Group
	requiredExits map[string]bool
	ruleSets      map[string]bool
	drainWarned   map[string]bool // exits already warned about being in maintenance
	warnings      []Phrase
	resolveUsers  map[string]bool // users whose policies need dest IP resolution (geoip/cidr)
	resolveAll    bool            // a geoip/cidr policy applies to everyone (no group)
}

func (c *entryCompiler) needsResolve() bool { return c.resolveAll || len(c.resolveUsers) > 0 }

func (c *entryCompiler) compileRules() ([]sbRule, map[string]bool) {
	c.requiredExits = map[string]bool{}
	c.ruleSets = map[string]bool{}
	c.drainWarned = map[string]bool{}
	c.resolveUsers = map[string]bool{}
	var rules []sbRule

	fleet := policiesByTier(c.state.RoutePolicies, model.TierFleet)
	exit := policiesByTier(c.state.RoutePolicies, model.TierExit)

	// An exclusion is part of the rule it belongs to, as an inverted condition,
	// so an excluded target falls through to the rules below instead of jumping
	// past them to the group's exit. That is how "except" reads, and it lets a
	// lower rule catch what a higher one let go.
	emit := func(p model.RoutePolicy) {
		if r, ok := c.compilePolicy(p); ok {
			rules = append(rules, r)
		}
		// A reserved rule is two rules on the node: the group's route, then the
		// refusal for everyone else. Adjacent and in that order: the group is
		// already served by the time the refusal is reached, and nothing below
		// can hand the same destination to anyone else.
		if r, ok := c.compileReservation(p); ok {
			rules = append(rules, r)
		}
	}
	// The network's own rules first of all (they override every namespace), then
	// each namespace's. sing-box is first-match top-down.
	for _, p := range fleet {
		emit(p)
	}
	for _, p := range exit {
		emit(p)
	}
	// Per-group default routes (present groups only, sorted).
	for _, gid := range sortedKeys(presentGroups(c.membership)) {
		g := c.groupByID[gid]
		r := sbRule{AuthUser: c.membership[gid]}
		if g.DefaultExitID == "" || g.DefaultExitID == model.ExitDirect {
			r.Outbound = "direct"
		} else {
			r.Outbound = c.resolveExit(g.DefaultExitID)
		}
		rules = append(rules, r)
	}
	// Full-tunnel clients resolve DNS locally and connect by IP. A domain_suffix
	// and geosite rules would never see a hostname. A sniff action recovers the
	// destination domain from the payload (TLS SNI / HTTP Host) so those rules
	// match regardless of how the client connected. Needed only when a domain or
	// geosite matcher is actually emitted (geoip works on the raw IP).
	needsSniff := false
	for _, r := range rules {
		if len(r.DomainSuffix) > 0 {
			needsSniff = true
		}
		for _, rs := range r.RuleSet {
			if strings.HasPrefix(rs, "geosite-") {
				needsSniff = true
			}
		}
	}
	// geoip/ip_cidr rules only match once the destination is an IP, but a hostname
	// may arrive instead. Prepend a resolve rule (scoped to the affected users) so
	// destinations are resolved before the geo rules are evaluated.
	if c.needsResolve() {
		resolve := sbRule{Action: "resolve"}
		if !c.resolveAll {
			resolve.AuthUser = sortedKeys(c.resolveUsers)
		}
		rules = append([]sbRule{resolve}, rules...)
	}
	// Sniff must run first so the recovered domain is available to every rule
	// (including the resolve above): final order [sniff, resolve, …rules].
	if needsSniff {
		rules = append([]sbRule{{Action: "sniff"}}, rules...)
	}
	return rules, c.requiredExits
}

// compilePolicy turns a RoutePolicy into a sing-box rule. Returns ok=false when
// the policy applies to a group (or namespace) with no active members, since
// emitting it with an empty auth_user would match everyone, including other
// namespaces (the cross-namespace leak this scoping prevents).
func (c *entryCompiler) compilePolicy(p model.RoutePolicy) (sbRule, bool) {
	var users []string
	switch {
	case p.AppliesToGroupID != "":
		users = c.membership[p.AppliesToGroupID]
		if len(users) == 0 {
			return sbRule{}, false
		}
	case p.OwnerID != "":
		// "All users" of a namespace policy means "all of this namespace's
		// clients", never the whole entry. Scope auth_user to the namespace so
		// an operator rule cannot reach across namespaces. An empty auth_user
		// (the entire entry, every namespace) is reserved for infra policies
		// (OwnerID == ""), handled by the default case below.
		users = c.ownerMembers[p.OwnerID]
		if len(users) == 0 {
			return sbRule{}, false
		}
	}

	match, ok := c.compileMatch(p, users)
	if !ok {
		return sbRule{}, false
	}
	r := match

	switch p.Action {
	case model.ActionDirect:
		r.Outbound = "direct"
	case model.ActionBlock:
		r.Action = "reject"
	case model.ActionExit:
		out, reject := c.policyExit(p, "", p.ExitNodeID)
		if reject {
			r.Action = "reject"
		} else {
			r.Outbound = out
		}
	}
	return r, true
}

// policyExit says where one half of a rule sends its traffic while the exit that
// half names is out of rotation: drained by an operator, or drained by egress
// failover once the node stopped answering. The rule's own answer to "if that
// exit is down" decides: another exit, out from the entry node, or refused. A
// rule that never answered keeps the old behaviour and egresses locally, which
// keeps clients online through a maintenance window.
//
// A reserved rule has two halves and one answer: the half written for its group
// and the half written for everyone else both follow it, each for the exit it
// names. Anything else would leave one half of the rule deciding nothing.
//
// Refusal is the one that earns its place: a rule written to put a destination
// behind a particular address should not quietly hand that traffic to the entry
// node's address the moment the exit dies.
func (c *entryCompiler) policyExit(p model.RoutePolicy, half, exitID string) (outbound string, reject bool) {
	if !c.drained(exitID) {
		return c.resolveExit(exitID), false
	}
	// The warning names the rule the operator knows it by, and says which half
	// of a reserved rule it is talking about.
	name := journal.Text(p.Name)
	if half != "" {
		name = journal.Line("%s (everyone else)", p.Name)
	}
	key := "policy:" + p.ID + ":" + half
	dead, back := nodeLabel(c.state, exitID), nodeLabel(c.state, p.FallbackExitID)
	switch p.FallbackKind {
	case model.FallbackExit:
		if !c.drained(p.FallbackExitID) {
			c.warnOnce(key, journal.Line(
				"exit %s is out of rotation; rule %s falls back to exit %s", dead, name, back))
			return c.resolveExit(p.FallbackExitID), false
		}
		// The fallback is down too. The rule asked for a named address and
		// there is none left to give it; handing that traffic to the entry
		// node's own address would break the single promise the rule makes,
		// so it is refused until one of the two returns.
		c.warnOnce(key, journal.Line(
			"exit %s and its fallback %s are both out of rotation; rule %s refuses its traffic until one of them returns", dead, back, name))
		return "", true
	case model.FallbackBlock:
		c.warnOnce(key, journal.Line(
			"exit %s is out of rotation; rule %s refuses its traffic until it returns", dead, name))
		return "", true
	}
	return c.resolveExit(exitID), false
}

// drained reports whether an exit is out of rotation. An unknown node counts as
// present: resolveExit turns a missing exit into an error.
func (c *entryCompiler) drained(exitID string) bool {
	n, ok := c.state.NodeByID(exitID)
	return ok && n.Maintenance
}

// warnOnce keeps one line per key. resolveExit keys by exit (one drain notice
// per node); a policy that answers for itself keys by its own id, leaving two rules
// leaving the same drained exit for different places each say so.
func (c *entryCompiler) warnOnce(key string, msg journal.Msg) {
	if c.drainWarned[key] {
		return
	}
	c.warnings = append(c.warnings, say(msg))
	c.drainWarned[key] = true
}

// compileReservation builds the other half of a reserved rule: the same match,
// for everyone the rule is not written for. Returns ok=false for an ordinary
// rule.
//
// It reaches as far as the rule itself does: an infra rule covers the whole
// entry, a namespace rule only that namespace's clients. An operator
// reserving a destination cannot decide it for somebody else's clients.
func (c *entryCompiler) compileReservation(p model.RoutePolicy) (sbRule, bool) {
	if p.OthersAction == "" || p.AppliesToGroupID == "" {
		return sbRule{}, false
	}
	var users []string
	if p.OwnerID != "" {
		users = c.ownerMembers[p.OwnerID]
		if len(users) == 0 {
			return sbRule{}, false
		}
	}
	r, ok := c.compileMatch(p, users)
	if !ok {
		return sbRule{}, false
	}
	switch p.OthersAction {
	case model.ActionDirect:
		r.Outbound = "direct"
	case model.ActionExit:
		out, reject := c.policyExit(p, "everyone else", p.OthersExitID)
		if reject {
			r.Action = "reject"
		} else {
			r.Outbound = out
		}
	default:
		r.Action = "reject"
	}
	return r, true
}

// compileMatch turns a rule's conditions into one sing-box rule, then scopes it
// to users.
//
// The conditions fold top to bottom, each line joining to everything above it
// (model.MatchTree). An ordinary sing-box rule says exactly one shape, one run
// of address alternatives and one run of rule-set alternatives. Whatever
// fits that is emitted flat, and the rest is nested under logical rules that
// mean what the operator wrote.
func (c *entryCompiler) compileMatch(p model.RoutePolicy, users []string) (sbRule, bool) {
	tree := model.MatchTree(p.Match())
	if tree == nil {
		return sbRule{}, false
	}
	r, ok := c.nodeRule(tree, p, users)
	if !ok {
		return sbRule{}, false
	}
	return c.scope(r, users), true
}

// nodeRule compiles one node of the match tree. Two flat rules merge into one
// whenever an ordinary rule already says what the join says (alternatives inside
// one matcher, an intersection across the two halves) and nest under a logical
// rule when it does not.
func (c *entryCompiler) nodeRule(n *model.MatchNode, p model.RoutePolicy, users []string) (sbRule, bool) {
	if n.IsCond() {
		return c.conditionRule(n.Cond, p, users)
	}
	l, lok := c.nodeRule(n.L, p, users)
	r, rok := c.nodeRule(n.R, p, users)
	switch {
	case !lok && !rok:
		return sbRule{}, false
	case !lok:
		// Nothing usable above it: an exclusion with nothing to exclude from
		// would invert the rule. It goes instead of the rule changing sides.
		if n.Op == model.JoinExcept {
			return sbRule{}, false
		}
		return r, true
	case !rok:
		return l, true
	}
	switch n.Op {
	case model.JoinOr:
		// One matcher already lists alternatives: two runs of the same half
		// are one run.
		if h := chainHalf(l); h != "" && h == chainHalf(r) {
			return mergeFlat(l, r), true
		}
		return logical("or", l, r), true
	case model.JoinExcept:
		return logical("and", l, invertRule(r)), true
	default:
		// The two halves of an ordinary rule are already intersected.
		if lh, rh := chainHalf(l), chainHalf(r); lh != "" && rh != "" && lh != rh {
			return mergeFlat(l, r), true
		}
		return logical("and", l, r), true
	}
}

// mergeFlat folds one flat rule into another. Only ever called for two rules
// that an ordinary sing-box rule can hold together.
func mergeFlat(a, b sbRule) sbRule {
	a.DomainSuffix = append(a.DomainSuffix, b.DomainSuffix...)
	a.IPCIDR = append(a.IPCIDR, b.IPCIDR...)
	a.RuleSet = append(a.RuleSet, b.RuleSet...)
	return a
}

// invertRule turns a rule into "everything this does not match". A flat rule
// carries its own invert; a logical one has to be wrapped to carry it.
func invertRule(r sbRule) sbRule {
	if r.Type == "" {
		r.Invert = true
		return r
	}
	return sbRule{Type: "logical", Mode: "or", Rules: []sbRule{r}, Invert: true}
}

// flatHalf says which half of an ordinary sing-box rule a condition lands in.
// The address matchers are alternatives to each other:
//
//	(domain_suffix || ip_cidr) && rule_set
//
// with different rule-sets alternatives among themselves. geoip and geosite are
// both compiled as rule-sets, and they share the second half.
func flatHalf(kind string) string {
	if kind == model.CondGeoIP || kind == model.CondGeoSite {
		return "rule_set"
	}
	return "address"
}

// chainHalf reports the half a flat rule occupies, or "" once it holds both (or
// is logical, or inverted: an inverted rule cannot be merged into another).
func chainHalf(r sbRule) string {
	if r.Type != "" || r.Invert {
		return ""
	}
	addr := len(r.DomainSuffix)+len(r.IPCIDR) > 0
	rs := len(r.RuleSet) > 0
	switch {
	case addr && !rs:
		return "address"
	case rs && !addr:
		return "rule_set"
	}
	return ""
}

// scope attaches auth_user. A logical rule has no auth_user of its own, and the
// scoping goes in beside it as a rule of its own.
func (c *entryCompiler) scope(r sbRule, users []string) sbRule {
	if len(users) == 0 {
		return r
	}
	if r.Type == "" {
		r.AuthUser = users
		return r
	}
	return logical("and", sbRule{AuthUser: users}, r)
}

// conditionRule turns one condition into the flat rule that matches it.
func (c *entryCompiler) conditionRule(cond model.RouteCondition, p model.RoutePolicy, users []string) (sbRule, bool) {
	var r sbRule
	switch cond.Kind {
	case model.CondDomain:
		for _, d := range cond.Values {
			if d = strings.TrimPrefix(strings.TrimSpace(strings.ToLower(d)), "."); d != "" {
				r.DomainSuffix = append(r.DomainSuffix, d)
			}
		}
	case model.CondCIDR:
		for _, cidr := range cond.Values {
			if n := normalizeCIDR(cidr); n != "" {
				r.IPCIDR = append(r.IPCIDR, n)
			}
		}
	case model.CondGeoIP:
		for _, g := range cond.Values {
			if g = strings.ToLower(strings.TrimSpace(g)); g != "" {
				tag := "geoip-" + g
				r.RuleSet = append(r.RuleSet, tag)
				c.ruleSets[tag] = true
			}
		}
	case model.CondGeoSite:
		for _, g := range cond.Values {
			if g = strings.ToLower(strings.TrimSpace(g)); g != "" {
				tag := "geosite-" + g
				r.RuleSet = append(r.RuleSet, tag)
				c.ruleSets[tag] = true
			}
		}
	}
	if len(r.DomainSuffix)+len(r.IPCIDR)+len(r.RuleSet) == 0 {
		return sbRule{}, false
	}
	// Matching on an address needs the destination resolved first, wherever in
	// the rule the condition sits.
	if cond.Kind == model.CondGeoIP || cond.Kind == model.CondCIDR {
		if len(users) == 0 {
			c.resolveAll = true
		} else {
			for _, u := range users {
				c.resolveUsers[u] = true
			}
		}
	}
	return r, true
}

// resolveExit returns the outbound tag for routing to exitID and registers it as
// a required outbound, unless the exit is in maintenance or drain, in which case
// it falls back to local "direct" egress (warning once). A drained exit stays
// provisioned and serves in-flight connections; new traffic that would target it
// egresses locally so clients stay online through the maintenance window.
func (c *entryCompiler) resolveExit(exitID string) string {
	if n, ok := c.state.NodeByID(exitID); ok && n.Maintenance {
		if !c.drainWarned[exitID] {
			c.warnings = append(c.warnings, say(journal.Line(
				"exit %s is out of rotation; traffic that targets it leaves directly from the entry node until it returns", nodeLabel(c.state, exitID))))
			c.drainWarned[exitID] = true
		}
		return "direct"
	}
	c.requiredExits[exitID] = true
	return exitTag(exitID)
}

func (c *entryCompiler) requiredRuleSets() []string {
	return sortedKeys(c.ruleSets)
}

// ruleSetDefs emits local rule-set references for every geoip/geosite tag used.
// The .srs files must be provisioned on the node (agent follow-up).
func (c *entryCompiler) ruleSetDefs(dir string) []any {
	tags := sortedKeys(c.ruleSets)
	if len(tags) == 0 {
		return nil
	}
	defs := make([]any, 0, len(tags))
	for _, tag := range tags {
		defs = append(defs, map[string]any{
			"type": "local", "tag": tag, "format": "binary",
			"path": strings.TrimRight(dir, "/") + "/" + tag + ".srs",
		})
	}
	return defs
}

func vlessOutboundFor(state model.State, exitID string) (sbVLESSOutbound, error) {
	node, ok := state.NodeByID(exitID)
	if !ok || !node.IsExit() || node.DialIn == nil {
		return sbVLESSOutbound{}, fmt.Errorf("exit %q is not a configured exit node", exitID)
	}
	if len(node.PublicIPs) == 0 {
		return sbVLESSOutbound{}, fmt.Errorf("exit %q has no public ip", exitID)
	}
	d := node.DialIn
	return sbVLESSOutbound{
		Type: "vless", Tag: exitTag(exitID), Server: node.PublicIPs[0], ServerPort: d.Port,
		UUID: d.UUID, Flow: realityFlow,
		TLS: sbOutboundTLS{
			Enabled: true, ServerName: d.TargetSNI,
			UTLS:    &sbUTLS{Enabled: true, Fingerprint: utlsFingerprint},
			Reality: &sbOutboundReality{Enabled: true, PublicKey: d.PublicKey, ShortID: d.ShortID},
		},
	}, nil
}

// ---- helpers ----

func exitTag(nodeID string) string { return "exit-" + nodeID }

func policiesByTier(policies []model.RoutePolicy, tier model.RuleTier) []model.RoutePolicy {
	var out []model.RoutePolicy
	for _, p := range policies {
		if p.Disabled {
			continue // kept in the DB but not evaluated
		}
		if p.Tier == tier {
			out = append(out, p)
		}
	}
	model.SortPolicies(out)
	return out
}

func presentGroups(membership map[string][]string) map[string]bool {
	out := map[string]bool{}
	for gid, users := range membership {
		if len(users) > 0 {
			out[gid] = true
		}
	}
	return out
}

func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func normalizeCIDR(value string) string {
	value = strings.TrimSpace(value)
	if prefix, err := netip.ParsePrefix(value); err == nil {
		return prefix.String()
	}
	if addr, err := netip.ParseAddr(value); err == nil {
		bits := 32
		if addr.Is6() {
			bits = 128
		}
		return netip.PrefixFrom(addr, bits).String()
	}
	return ""
}

func marshalConfig(cfg sbConfig) (string, error) {
	body, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return "", err
	}
	return string(body) + "\n", nil
}
