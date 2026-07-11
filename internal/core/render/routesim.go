package render

import (
	"net/netip"
	"strings"

	"trustpanel/internal/core/model"
)

// GeoMatcher evaluates geoip/geosite rule-set membership the way a node would
// (e.g. by shelling out to `sing-box rule-set match`). When a matcher is nil, or
// returns an error, the simulator marks geo conditions as "not evaluated" and
// warns — it never guesses a geo verdict it cannot verify.
type GeoMatcher interface {
	GeoIP(country string, ip netip.Addr) (bool, error)
	GeoSite(category, domain string) (bool, error)
}

// SimStep is one evaluated rule in top-down (first-match) order — the trace the
// UI shows so an operator can see exactly why a decision was reached.
type SimStep struct {
	Stage   string `json:"stage"`  // fleet | guard | exit | group-default | final
	Rule    string `json:"rule"`   // policy name or synthetic label
	Action  string `json:"action"` // direct | exit | block | "" (non-deciding)
	Detail  string `json:"detail"` // why it matched / didn't
	Matched bool   `json:"matched"`
}

// SimResult is the outcome of a route test for one (group, target) pair.
type SimResult struct {
	Target      string    `json:"target"`
	TargetIsIP  bool      `json:"target_is_ip"`
	ResolvedIPs []string  `json:"resolved_ips"`
	Decision    string    `json:"decision"` // exit | direct | block
	ExitNodeID  string    `json:"exit_node_id,omitempty"`
	Egress      string    `json:"egress"`     // human label: exit node / entry (direct) / blocked
	DecidedBy   string    `json:"decided_by"` // rule that decided
	Reason      string    `json:"reason"`
	Trace       []SimStep `json:"trace"`
	Warnings    []string  `json:"warnings,omitempty"`
}

// Simulate walks the same ordered rule list the entry compiler emits (resolve →
// fleet [except/policy] → guard [except/policy] → exit [except/policy] → group
// default → final) and
// reports, via first-match, which node a connection from a member of groupID to
// target would egress through and which rule decided it. ips are the already
// resolved destination addresses (the caller does DNS so this stays pure).
func Simulate(state model.State, groupID, target string, ips []netip.Addr, geo GeoMatcher) SimResult {
	res := SimResult{Target: target}
	domain := ""
	if addr, err := netip.ParseAddr(strings.TrimSpace(target)); err == nil {
		res.TargetIsIP = true
		if len(ips) == 0 {
			ips = []netip.Addr{addr}
		}
	} else {
		domain = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(target), "."))
	}
	for _, ip := range ips {
		res.ResolvedIPs = append(res.ResolvedIPs, ip.String())
	}

	var group model.Group
	for _, g := range state.Groups {
		if g.ID == groupID {
			group = g
			break
		}
	}

	applies := func(p model.RoutePolicy) bool {
		if p.AppliesToGroupID != "" {
			return p.AppliesToGroupID == groupID
		}
		// "All users" of the policy's namespace: an infra policy (owner "")
		// reaches every namespace; a namespace policy reaches only its own
		// clients — i.e. groups owned by the same namespace. Mirrors the entry
		// compiler's auth_user scoping (singbox.go compilePolicy).
		return p.OwnerID == "" || p.OwnerID == group.OwnerID
	}

	eval := func(stage string, pols []model.RoutePolicy) bool {
		for _, p := range pols {
			if !applies(p) {
				continue
			}
			// Exclusions are hoisted above the policy: an excluded domain takes the
			// group's normal path (its default exit, or direct when there is none),
			// mirroring entryCompiler.exclusionRule.
			if p.AppliesToGroupID == groupID && groupID != "" {
				if ex := cleanSuffixes(p.ExcludeDomains); len(ex) > 0 {
					if domain != "" && suffixMatchAny(domain, ex) {
						// An excluded domain takes the group's NORMAL path: its
						// default exit, or direct when the group has none.
						if group.DefaultExitID != "" {
							res.Trace = append(res.Trace, SimStep{
								Stage: stage, Rule: "except-domains of " + p.Name, Matched: true,
								Detail: domain + " is excluded from " + p.Name + " → takes the group default exit"})
							setExit(&res, state, group.DefaultExitID, "except-domains of "+p.Name,
								domain+" bypasses "+p.Name+" and takes the group default exit")
						} else {
							res.Trace = append(res.Trace, SimStep{
								Stage: stage, Rule: "except-domains of " + p.Name, Matched: true,
								Detail: domain + " is excluded from " + p.Name + " → direct (group has no default exit)"})
							res.Decision, res.Egress, res.DecidedBy = "direct", "entry node (local / direct)", "except-domains of "+p.Name
							res.Reason = domain + " bypasses " + p.Name + " and egresses locally (the group has no default exit)"
						}
						return true
					}
					res.Trace = append(res.Trace, SimStep{
						Stage: stage, Rule: "except-domains of " + p.Name, Matched: false,
						Detail: "no excluded domain matched the target"})
				}
			}
			matched, detail := matchPolicy(p, domain, ips, geo, &res)
			step := SimStep{Stage: stage, Rule: p.Name, Action: string(p.Action), Detail: detail, Matched: matched}
			res.Trace = append(res.Trace, step)
			if matched {
				applyAction(&res, state, p)
				return true
			}
		}
		return false
	}

	if eval("fleet", policiesByTier(state.RoutePolicies, model.TierFleet)) {
		return res
	}
	if eval("guard", policiesByTier(state.RoutePolicies, model.TierGuard)) {
		return res
	}
	if eval("exit", policiesByTier(state.RoutePolicies, model.TierExit)) {
		return res
	}

	// Per-group catch-all (every present group gets one; final is the fallback).
	if group.DefaultExitID != "" {
		setExit(&res, state, group.DefaultExitID, "group default", "no policy matched — group default exit")
		res.Trace = append(res.Trace, SimStep{Stage: "group-default", Rule: "group default exit", Action: "exit", Matched: true, Detail: res.Reason})
		return res
	}
	res.Decision, res.Egress, res.DecidedBy = "direct", "entry node (local / direct)", "group default (no default exit)"
	res.Reason = "no policy matched and the group has no default exit → direct egress from the entry node"
	res.Trace = append(res.Trace, SimStep{Stage: "group-default", Rule: "group default → direct", Action: "direct", Matched: true, Detail: res.Reason})
	return res
}

// matchPolicy reports whether a single policy matches the target, with a
// human-readable detail. Within a policy the matcher TYPES are AND-ed
// (domain_suffix AND ip_cidr AND rule_set); geoip+geosite share one rule_set so
// they OR with each other — exactly the sing-box semantics the compiler emits.
func matchPolicy(p model.RoutePolicy, domain string, ips []netip.Addr, geo GeoMatcher, res *SimResult) (bool, string) {
	var parts []string
	all := true
	has := false

	if len(p.MatchDomains) > 0 {
		has = true
		m := domain != "" && suffixMatchAny(domain, cleanSuffixes(p.MatchDomains))
		all = all && m
		parts = append(parts, cond("domains", m))
	}
	if len(p.MatchCIDRs) > 0 {
		has = true
		m := ipInAnyCIDR(ips, p.MatchCIDRs)
		all = all && m
		parts = append(parts, cond("cidr", m))
	}
	if len(p.MatchGeoIP) > 0 || len(p.MatchGeoSite) > 0 {
		has = true
		m, label := matchRuleSet(p, domain, ips, geo, res)
		all = all && m
		parts = append(parts, label)
	}
	if !has {
		return true, "no matchers set — matches everything"
	}
	return all, strings.Join(parts, " AND ")
}

// matchRuleSet evaluates the combined geoip/geosite rule_set (OR within).
func matchRuleSet(p model.RoutePolicy, domain string, ips []netip.Addr, geo GeoMatcher, res *SimResult) (bool, string) {
	unevaluated := false
	for _, raw := range p.MatchGeoIP {
		cc := strings.ToLower(strings.TrimSpace(raw))
		if cc == "" {
			continue
		}
		if geo == nil {
			unevaluated = true
			continue
		}
		for _, ip := range ips {
			ok, err := geo.GeoIP(cc, ip)
			if err != nil {
				unevaluated = true
				continue
			}
			if ok {
				return true, "geoip:" + cc + " ✓ (" + ip.String() + " is in " + strings.ToUpper(cc) + ")"
			}
		}
	}
	for _, raw := range p.MatchGeoSite {
		cat := strings.ToLower(strings.TrimSpace(raw))
		if cat == "" || domain == "" {
			continue
		}
		if geo == nil {
			unevaluated = true
			continue
		}
		ok, err := geo.GeoSite(cat, domain)
		if err != nil {
			unevaluated = true
			continue
		}
		if ok {
			return true, "geosite:" + cat + " ✓"
		}
	}
	if unevaluated {
		res.Warnings = append(res.Warnings,
			"policy \""+p.Name+"\": geoip/geosite could not be evaluated here (rule-set unavailable) — treated as no-match; the node may decide differently")
		return false, "geo(rule_set): ✗ not evaluable here"
	}
	if len(ips) == 0 && len(p.MatchGeoIP) > 0 {
		return false, "geoip: ✗ (target did not resolve to an IP)"
	}
	return false, "geo(rule_set): ✗ no geoip/geosite matched"
}

// applyAction records a matched policy's decision onto the result.
func applyAction(res *SimResult, state model.State, p model.RoutePolicy) {
	switch p.Action {
	case model.ActionDirect:
		res.Decision, res.Egress = "direct", "entry node (local / direct)"
	case model.ActionBlock:
		res.Decision, res.Egress = "block", "blocked (connection rejected)"
	case model.ActionExit:
		setExit(res, state, p.ExitNodeID, p.Name, "")
	}
	res.DecidedBy = p.Name
	if res.Reason == "" {
		res.Reason = "matched policy \"" + p.Name + "\" (" + string(p.Action) + ")"
	}
}

func setExit(res *SimResult, state model.State, exitID, decidedBy, reason string) {
	// A drained exit egresses locally (direct) at runtime — the renderer's
	// resolveExit falls back to direct for a node in maintenance. The simulator must
	// mirror that or it lies exactly when an operator is draining a node.
	if n, ok := state.NodeByID(exitID); ok && n.Maintenance {
		res.Decision, res.ExitNodeID, res.DecidedBy = "direct", "", decidedBy
		res.Egress = "entry node (local / direct)"
		res.Reason = "exit " + nodeLabel(state, exitID) + " is in maintenance (drained) → traffic egresses locally (direct) until it returns"
		res.Warnings = append(res.Warnings,
			"exit \""+exitID+"\" is drained; traffic that targets it egresses locally (direct) until it returns")
		return
	}
	res.Decision, res.ExitNodeID, res.DecidedBy = "exit", exitID, decidedBy
	res.Egress = "exit node " + nodeLabel(state, exitID)
	if reason != "" {
		res.Reason = reason
	} else if res.Reason == "" {
		res.Reason = "routed to exit " + nodeLabel(state, exitID)
	}
}

func nodeLabel(state model.State, id string) string {
	for _, n := range state.Nodes {
		if n.ID == id {
			if n.Name != "" {
				return n.Name + " (" + id + ")"
			}
			return id
		}
	}
	return id + " (unknown)"
}

func cleanSuffixes(in []string) []string {
	var out []string
	for _, d := range in {
		if d = strings.TrimPrefix(strings.TrimSpace(strings.ToLower(d)), "."); d != "" {
			out = append(out, d)
		}
	}
	return out
}

// suffixMatchAny mirrors sing-box domain_suffix (label boundary): a stored
// suffix "ru" matches "ru" and "*.ru" but not "guru" (verified against the live
// sing-box binary).
func suffixMatchAny(domain string, suffixes []string) bool {
	for _, s := range suffixes {
		if domain == s || strings.HasSuffix(domain, "."+s) {
			return true
		}
	}
	return false
}

func ipInAnyCIDR(ips []netip.Addr, cidrs []string) bool {
	for _, raw := range cidrs {
		norm := normalizeCIDR(raw)
		if norm == "" {
			continue
		}
		prefix, err := netip.ParsePrefix(norm)
		if err != nil {
			continue
		}
		for _, ip := range ips {
			if prefix.Contains(ip) {
				return true
			}
		}
	}
	return false
}

func cond(label string, ok bool) string {
	if ok {
		return label + ": ✓"
	}
	return label + ": ✗"
}
