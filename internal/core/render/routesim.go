package render

import (
	"net/netip"
	"strings"

	"trustpanel/internal/core/journal"
	"trustpanel/internal/core/model"
)

// GeoMatcher evaluates geoip/geosite rule-set membership the way a node would
// (e.g. by shelling out to `sing-box rule-set match`). When a matcher is nil, or
// returns an error, the simulator marks geo conditions as "not evaluated" and
// warns; it never guesses a geo verdict it cannot verify.
type GeoMatcher interface {
	GeoIP(country string, ip netip.Addr) (bool, error)
	GeoSite(category, domain string) (bool, error)
}

// SimStep is one evaluated rule in top-down (first-match) order: the trace the
// UI shows so an operator can see exactly why a decision was reached.
type SimStep struct {
	Stage   string `json:"stage"`  // fleet | guard | exit | group-default | final
	Rule    Phrase `json:"rule"`   // rule name or synthetic label
	Action  string `json:"action"` // direct | exit | block | "" (non-deciding)
	Detail  Phrase `json:"detail"` // why it matched / didn't
	Matched bool   `json:"matched"`
}

// SimResult is the outcome of a route test for one (group, target) pair.
type SimResult struct {
	Target      string    `json:"target"`
	TargetIsIP  bool      `json:"target_is_ip"`
	ResolvedIPs []string  `json:"resolved_ips"`
	Decision    string    `json:"decision"` // exit | direct | block
	ExitNodeID  string    `json:"exit_node_id,omitempty"`
	Egress      Phrase    `json:"egress"`     // where it leaves by, in words
	DecidedBy   Phrase    `json:"decided_by"` // rule that decided
	Reason      Phrase    `json:"reason"`
	Trace       []SimStep `json:"trace"`
	Warnings    []Phrase  `json:"warnings,omitempty"`
}

// Warn records one thing the reader has to know about this answer, in both
// languages. The same warning twice is not twice as true: a rule that names both
// a country and a category, with no rule-set to check either against, has one
// caveat and not one per list.
func (r *SimResult) Warn(m journal.Msg) {
	said := say(m)
	for _, w := range r.Warnings {
		if w.Text == said.Text {
			return
		}
	}
	r.Warnings = append(r.Warnings, said)
}

// egress labels: the three places a connection can leave by, named the way the
// panel names them everywhere else.
func egressDirect() journal.Msg { return journal.Line("entry node (out directly)") }
func egressBlocked() journal.Msg {
	return journal.Line("blocked (connection rejected)")
}
func egressExit(state model.State, id string) journal.Msg {
	return journal.Line("exit node %s", nodeLabel(state, id))
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
		// clients, i.e. groups owned by the same namespace. Mirrors the entry
		// compiler's auth_user scoping (singbox.go compilePolicy).
		return p.OwnerID == "" || p.OwnerID == group.OwnerID
	}

	// answersForOthers reports the other half of a reserved rule: the same match,
	// for everyone it is not reserved for. It reaches as far as the rule itself:
	// the whole entry for an infra rule, one namespace for an operator's.
	answersForOthers := func(p model.RoutePolicy) bool {
		if p.OthersAction == "" || p.AppliesToGroupID == "" || p.AppliesToGroupID == groupID {
			return false
		}
		return p.OwnerID == "" || p.OwnerID == group.OwnerID
	}

	eval := func(stage string, pols []model.RoutePolicy) bool {
		for _, p := range pols {
			mine := applies(p)
			if !mine && !answersForOthers(p) {
				continue
			}
			eff, label := p, journal.Text(p.Name)
			if !mine {
				// The half written for everyone else: same match, its own
				// action, named so the trace says whose rule decided this.
				eff.Action, eff.ExitNodeID = p.OthersAction, p.OthersExitID
				label = journal.Line("%s (everyone outside %s)", p.Name, groupLabel(state, p.AppliesToGroupID))
			}
			matched, detail := matchPolicy(p, domain, ips, geo, &res)
			res.Trace = append(res.Trace, SimStep{Stage: stage, Rule: say(label), Action: string(eff.Action), Detail: say(detail), Matched: matched})
			if matched {
				applyAction(&res, state, eff, label)
				return true
			}
		}
		return false
	}

	if eval("fleet", policiesByTier(state.RoutePolicies, model.TierFleet)) {
		return res
	}
	if eval("exit", policiesByTier(state.RoutePolicies, model.TierExit)) {
		return res
	}

	// Per-group catch-all (every present group gets one; final is the fallback).
	if group.DefaultExitID != "" {
		setExit(&res, state, group.DefaultExitID, journal.Line("the group's own exit"),
			journal.Line("no rule matched — the group leaves by its own exit"))
		res.Trace = append(res.Trace, SimStep{Stage: "group-default", Rule: say(journal.Line("the group's own exit")), Action: "exit", Matched: true, Detail: res.Reason})
		return res
	}
	res.Decision = "direct"
	res.Egress = say(egressDirect())
	res.DecidedBy = say(journal.Line("the group's own exit (none set)"))
	res.Reason = say(journal.Line("no rule matched and the group has no exit of its own → it leaves directly from the entry node"))
	res.Trace = append(res.Trace, SimStep{Stage: "group-default", Rule: say(journal.Line("the group has no exit of its own")), Action: "direct", Matched: true, Detail: res.Reason})
	return res
}

// matchPolicy reports whether a single policy matches the target, with a
// human-readable detail. It reads the conditions the way the form builds them,
// top to bottom with each line joining to everything above it, and brackets the
// result so the grouping is visible rather than assumed. The tester and the
// renderer must agree: a tester that decides differently from the node is worse
// than no tester, because it is believed.
func matchPolicy(p model.RoutePolicy, domain string, ips []netip.Addr, geo GeoMatcher, res *SimResult) (bool, journal.Msg) {
	tree := model.MatchTree(p.Match())
	if tree == nil {
		return true, journal.Line("no conditions set — everything matches")
	}
	return matchNode(tree, p, domain, ips, geo, res)
}

// matchNode evaluates one node of the match tree. Both sides are always
// evaluated, since a short circuit would drop the warning that says a geo lookup
// could not be made, which is the one thing the operator has to know.
func matchNode(n *model.MatchNode, p model.RoutePolicy, domain string, ips []netip.Addr, geo GeoMatcher, res *SimResult) (bool, journal.Msg) {
	if n.IsCond() {
		return matchCondition(p, n.Cond, domain, ips, geo, res)
	}
	l, lt := matchNode(n.L, p, domain, ips, geo, res)
	r, rt := matchNode(n.R, p, domain, ips, geo, res)
	// Brackets are written where the reading turns: a run of the same word needs
	// none, a change of word needs one. Nothing is left to precedence.
	if !n.L.IsCond() && (n.L.Op != n.Op || n.Op == model.JoinExcept) {
		lt = journal.Join(journal.Text("("), lt, journal.Text(")"))
	}
	var got bool
	word := "AND"
	switch n.Op {
	case model.JoinOr:
		got, word = l || r, "OR"
	case model.JoinExcept:
		got, word = l && !r, "EXCEPT"
	default:
		got = l && r
	}
	return got, journal.Join(lt, journal.Text(" "), journal.Term(word), journal.Text(" "), rt)
}

// matchCondition evaluates one condition and describes the outcome.
func matchCondition(p model.RoutePolicy, c model.RouteCondition, domain string, ips []netip.Addr, geo GeoMatcher, res *SimResult) (bool, journal.Msg) {
	switch c.Kind {
	case model.CondDomain:
		m := domain != "" && suffixMatchAny(domain, cleanSuffixes(c.Values))
		return m, cond("domains", m)
	case model.CondCIDR:
		m := ipInAnyCIDR(ips, c.Values)
		return m, cond("cidr", m)
	case model.CondGeoIP:
		return matchGeoIPValues(p, c.Values, ips, geo, res)
	case model.CondGeoSite:
		return matchGeoSiteValues(p, c.Values, domain, geo, res)
	}
	return false, cond(c.Kind, false)
}

func matchGeoIPValues(p model.RoutePolicy, values []string, ips []netip.Addr, geo GeoMatcher, res *SimResult) (bool, journal.Msg) {
	unevaluated := false
	for _, raw := range values {
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
				return true, journal.Line("geoip:%s ✓ (%s is in %s)", cc, ip.String(), strings.ToUpper(cc))
			}
		}
	}
	if unevaluated {
		return false, warnUnevaluated(p, res)
	}
	if len(ips) == 0 {
		return false, journal.Line("geoip: ✗ (the target did not resolve to an IP)")
	}
	return false, journal.Line("geoip: ✗ no country matched")
}

func matchGeoSiteValues(p model.RoutePolicy, values []string, domain string, geo GeoMatcher, res *SimResult) (bool, journal.Msg) {
	unevaluated := false
	for _, raw := range values {
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
			return true, journal.Text("geosite:" + cat + " ✓")
		}
	}
	if unevaluated {
		return false, warnUnevaluated(p, res)
	}
	return false, journal.Line("geosite: ✗ no category matched")
}

// warnUnevaluated records that a geo lookup could not be made here, so the
// verdict is reported as a no-match with the reason attached rather than as a
// confident answer.
func warnUnevaluated(p model.RoutePolicy, res *SimResult) journal.Msg {
	res.Warn(journal.Line("rule %s: the country and category lists are not on this server, so they could not be checked — read here as no match; the node itself may decide otherwise", p.Name))
	return journal.Line("country/category lists: ✗ not available here")
}

func applyAction(res *SimResult, state model.State, p model.RoutePolicy, label journal.Msg) {
	switch p.Action {
	case model.ActionDirect:
		res.Decision, res.Egress = "direct", say(egressDirect())
	case model.ActionBlock:
		res.Decision, res.Egress = "block", say(egressBlocked())
	case model.ActionExit:
		// While the exit is out of rotation the rule's own answer to "if that
		// exit is down" decides, exactly as policyExit does for the node. Both
		// halves of a reserved rule follow that one answer, which is why the
		// caller may hand us the half written for everyone else.
		if drainedExit(state, p.ExitNodeID) {
			refuse := func(reason, warning journal.Msg) {
				res.Decision, res.ExitNodeID, res.Egress = "block", "", say(egressBlocked())
				res.DecidedBy, res.Reason = say(label), say(reason)
				res.Warn(warning)
			}
			dead, back := nodeLabel(state, p.ExitNodeID), nodeLabel(state, p.FallbackExitID)
			switch p.FallbackKind {
			case model.FallbackExit:
				if !drainedExit(state, p.FallbackExitID) {
					setExit(res, state, p.FallbackExitID, label,
						journal.Line("exit %s is out of rotation → rule %s falls back to exit %s", dead, p.Name, back))
					res.Warn(journal.Line("exit %s is out of rotation; rule %s falls back to exit %s", dead, p.Name, back))
					return
				}
				// No named address left to give it, so the rule refuses rather
				// than egress from the entry node it was written to avoid.
				refuse(journal.Line("exit %s and its fallback %s are both out of rotation → rule %s refuses its traffic", dead, back, p.Name),
					journal.Line("exit %s and its fallback %s are both out of rotation; rule %s refuses its traffic until one of them returns", dead, back, p.Name))
				return
			case model.FallbackBlock:
				refuse(journal.Line("exit %s is out of rotation → rule %s refuses its traffic", dead, p.Name),
					journal.Line("exit %s is out of rotation; rule %s refuses its traffic until it returns", dead, p.Name))
				return
			}
		}
		setExit(res, state, p.ExitNodeID, label, nil)
	}
	res.DecidedBy = say(label)
	if res.Reason.Text == "" {
		res.Reason = say(journal.Line("matched rule %s (%s)", p.Name, journal.Term(string(p.Action))))
	}
}

// drainedExit mirrors the renderer: a node in maintenance is out of rotation.
func drainedExit(state model.State, exitID string) bool {
	n, ok := state.NodeByID(exitID)
	return ok && n.Maintenance
}

func setExit(res *SimResult, state model.State, exitID string, decidedBy, reason journal.Msg) {
	// A drained exit egresses locally (direct) at runtime, since the renderer's
	// resolveExit falls back to direct for a node in maintenance. The simulator must
	// mirror that or it lies exactly when an operator is draining a node.
	if n, ok := state.NodeByID(exitID); ok && n.Maintenance {
		res.Decision, res.ExitNodeID, res.DecidedBy = "direct", "", say(decidedBy)
		res.Egress = say(egressDirect())
		res.Reason = say(journal.Line("exit %s is out for maintenance → its traffic leaves directly from the entry node until it returns", nodeLabel(state, exitID)))
		res.Warn(journal.Line("exit %s is out of rotation; traffic that targets it leaves directly from the entry node until it returns", nodeLabel(state, exitID)))
		return
	}
	res.Decision, res.ExitNodeID, res.DecidedBy = "exit", exitID, say(decidedBy)
	res.Egress = say(egressExit(state, exitID))
	if reason != nil {
		res.Reason = say(reason)
	} else if res.Reason.Text == "" {
		res.Reason = say(journal.Line("sent out through exit %s", nodeLabel(state, exitID)))
	}
}

func groupLabel(state model.State, id string) string {
	for _, g := range state.Groups {
		if g.ID == id && g.Name != "" {
			return g.Name
		}
	}
	return id
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

// cond names the kind of matcher and whether the target was in it. The kind is
// data, so it is translated as a word rather than as part of a sentence.
func cond(kind string, ok bool) journal.Msg {
	mark := ": ✗"
	if ok {
		mark = ": ✓"
	}
	return journal.Join(journal.Term(kind), journal.Text(mark))
}
