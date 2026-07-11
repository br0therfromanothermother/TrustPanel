package render

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"sort"
	"strings"
	"time"

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
	AuthUser     []string `json:"auth_user,omitempty"`
	DomainSuffix []string `json:"domain_suffix,omitempty"`
	IPCIDR       []string `json:"ip_cidr,omitempty"`
	RuleSet      []string `json:"rule_set,omitempty"`
	Action       string   `json:"action,omitempty"`
	Outbound     string   `json:"outbound,omitempty"`
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

	cfg := sbConfig{
		Log: map[string]any{"level": "warn"},
		Inbounds: []any{sbMixedInbound{
			Type: "mixed", Tag: "trusttunnel-in",
			Listen: opts.MixedListen, ListenPort: opts.MixedPort, Users: inboundUsers,
		}},
		Outbounds: outbounds,
		Route:     sbRoute{Rules: rules, RuleSet: c.ruleSetDefs(opts.RuleSetDir), Final: "direct"},
		Experimental: map[string]any{
			"v2ray_api": map[string]any{
				"listen": opts.V2RayAPIListen,
				"stats":  map[string]any{"enabled": true, "users": statsUsers},
			},
		},
	}
	// A DNS server is required for the resolve action used by geoip/cidr policies.
	if c.needsResolve() {
		cfg.DNS = map[string]any{"servers": []any{
			map[string]any{"type": "udp", "tag": "resolver", "server": "1.1.1.1"},
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
	warnings      []string
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
	guard := policiesByTier(c.state.RoutePolicies, model.TierGuard)
	exit := policiesByTier(c.state.RoutePolicies, model.TierExit)

	emit := func(p model.RoutePolicy) {
		// Exclusions first: listed domains bypass the policy and take the group's
		// normal path (its default exit), so e.g. ".ru -> direct except bank.ru".
		if ex, ok := c.exclusionRule(p); ok {
			rules = append(rules, ex)
		}
		if r, ok := c.compilePolicy(p); ok {
			rules = append(rules, r)
		}
	}
	// Fleet mandates first of all (override every namespace), then guard (safety
	// net), then exit (per-namespace). sing-box is first-match top-down.
	for _, p := range fleet {
		emit(p)
	}
	for _, p := range guard {
		emit(p)
	}
	for _, p := range exit {
		emit(p)
	}
	// Per-group default routes (present groups only, sorted).
	for _, gid := range sortedKeys(presentGroups(c.membership)) {
		g := c.groupByID[gid]
		r := sbRule{AuthUser: c.membership[gid]}
		if g.DefaultExitID == "" {
			r.Outbound = "direct"
		} else {
			r.Outbound = c.resolveExit(g.DefaultExitID)
		}
		rules = append(rules, r)
	}
	// Full-tunnel clients resolve DNS locally and connect by IP, so domain_suffix
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

// exclusionRule emits a rule that sends a policy's ExcludeDomains to the group's
// normal path (its default exit) instead of the policy action, placed before the
// policy rule. Requires a group default exit; otherwise it warns and is skipped.
func (c *entryCompiler) exclusionRule(p model.RoutePolicy) (sbRule, bool) {
	if len(p.ExcludeDomains) == 0 || p.AppliesToGroupID == "" {
		return sbRule{}, false
	}
	users := c.membership[p.AppliesToGroupID]
	if len(users) == 0 {
		return sbRule{}, false
	}
	var domains []string
	for _, d := range p.ExcludeDomains {
		if d = strings.TrimPrefix(strings.TrimSpace(strings.ToLower(d)), "."); d != "" {
			domains = append(domains, d)
		}
	}
	if len(domains) == 0 {
		return sbRule{}, false
	}
	// An excluded domain must take the group's NORMAL path so it bypasses this
	// policy — that path is the group's default exit, or `direct` (local egress)
	// when the group has no default exit. Skipping the rule instead would fall
	// through to the policy and block/exit it — the opposite of "exclude".
	g := c.groupByID[p.AppliesToGroupID]
	outbound := "direct"
	if g.DefaultExitID != "" {
		outbound = c.resolveExit(g.DefaultExitID)
	}
	return sbRule{AuthUser: users, DomainSuffix: domains, Outbound: outbound}, true
}

// compilePolicy turns a RoutePolicy into a sing-box rule. Returns ok=false when
// the policy applies to a group (or namespace) with no active members — emitting
// it with an empty auth_user would otherwise match everyone, including other
// namespaces (the cross-namespace leak this scoping prevents).
func (c *entryCompiler) compilePolicy(p model.RoutePolicy) (sbRule, bool) {
	var r sbRule
	switch {
	case p.AppliesToGroupID != "":
		users := c.membership[p.AppliesToGroupID]
		if len(users) == 0 {
			return sbRule{}, false
		}
		r.AuthUser = users
	case p.OwnerID != "":
		// "All users" of a namespace policy means "all of this namespace's
		// clients", never the whole entry. Scope auth_user to the namespace so
		// an operator rule cannot reach across namespaces. An empty auth_user
		// (the entire entry, every namespace) is reserved for infra policies
		// (OwnerID == ""), handled by the default case below.
		users := c.ownerMembers[p.OwnerID]
		if len(users) == 0 {
			return sbRule{}, false
		}
		r.AuthUser = users
	}
	for _, d := range p.MatchDomains {
		d = strings.TrimPrefix(strings.TrimSpace(strings.ToLower(d)), ".")
		if d != "" {
			r.DomainSuffix = append(r.DomainSuffix, d)
		}
	}
	for _, cidr := range p.MatchCIDRs {
		if n := normalizeCIDR(cidr); n != "" {
			r.IPCIDR = append(r.IPCIDR, n)
		}
	}
	for _, g := range p.MatchGeoIP {
		tag := "geoip-" + strings.ToLower(strings.TrimSpace(g))
		r.RuleSet = append(r.RuleSet, tag)
		c.ruleSets[tag] = true
	}
	// IP-based matchers need the destination resolved first (see compileRules).
	if len(p.MatchGeoIP) > 0 || len(p.MatchCIDRs) > 0 {
		if len(r.AuthUser) == 0 {
			c.resolveAll = true
		} else {
			for _, u := range r.AuthUser {
				c.resolveUsers[u] = true
			}
		}
	}
	for _, g := range p.MatchGeoSite {
		tag := "geosite-" + strings.ToLower(strings.TrimSpace(g))
		r.RuleSet = append(r.RuleSet, tag)
		c.ruleSets[tag] = true
	}

	switch p.Action {
	case model.ActionDirect:
		r.Outbound = "direct"
	case model.ActionBlock:
		r.Action = "reject"
	case model.ActionExit:
		r.Outbound = c.resolveExit(p.ExitNodeID)
		if p.FallbackKind != "" && p.FallbackKind != model.FallbackBlock {
			c.warnings = append(c.warnings, fmt.Sprintf(
				"policy %q fallback=%s is not yet enforced at runtime; traffic routes to exit %q while it is up (urltest failover is a follow-up)",
				p.ID, p.FallbackKind, p.ExitNodeID))
		}
	}
	return r, true
}

// resolveExit returns the outbound tag for routing to exitID and registers it as
// a required outbound — unless the exit is in maintenance/drain, in which case it
// falls back to local "direct" egress (warning once). A drained exit stays
// provisioned and serves in-flight connections; new traffic that would target it
// egresses locally so clients stay online through the maintenance window.
func (c *entryCompiler) resolveExit(exitID string) string {
	if n, ok := c.state.NodeByID(exitID); ok && n.Maintenance {
		if !c.drainWarned[exitID] {
			c.warnings = append(c.warnings, fmt.Sprintf(
				"exit %q is in maintenance; traffic that targets it egresses locally (direct) until it returns", exitID))
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
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Priority != out[j].Priority {
			return out[i].Priority > out[j].Priority // higher priority first
		}
		return out[i].ID < out[j].ID
	})
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
