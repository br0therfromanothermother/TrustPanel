package render

import (
	"sort"
	"strings"

	"trustpanel/internal/core/model"
)

// RuleSetTagsInUse names every geo rule-set the routing depends on, across the
// whole network: the tags a compiled node config would reference, gathered from
// the rules rather than from whatever happens to be on disk.
//
// It is the state-wide twin of what the compiler works out per node
// (entryCompiler.requiredRuleSets), and it lives next to it on purpose. The
// panel needs the same answer for a different question: which categories must
// be held, which ones a withdrawal upstream actually matters for, which ones
// belong on the Geo databases tab. Two implementations of "in use" would
// eventually disagree, with the tab quietly hiding a set the nodes need.
//
// A disabled rule counts. Its categories are not compiled into anything today,
// but enabling it is one tap, and a category that had to be fetched at that
// moment is the failure this whole arrangement exists to remove.
func RuleSetTagsInUse(state model.State) []string {
	seen := map[string]bool{}
	for _, p := range state.RoutePolicies {
		for _, tag := range policyRuleSetTags(p) {
			seen[tag] = true
		}
	}
	out := make([]string, 0, len(seen))
	for tag := range seen {
		out = append(out, tag)
	}
	sort.Strings(out)
	return out
}

// PoliciesUsingRuleSet names the rules that reference one tag, so a category can
// be read back as the routing that depends on it rather than as a file name.
func PoliciesUsingRuleSet(state model.State, tag string) []model.RoutePolicy {
	var out []model.RoutePolicy
	for _, p := range state.RoutePolicies {
		for _, t := range policyRuleSetTags(p) {
			if t == tag {
				out = append(out, p)
				break
			}
		}
	}
	return out
}

// PolicyRuleSetTags is one rule's geo tags, for callers outside the package:
// the panel checks a rule against the source's catalogue before it is saved.
func PolicyRuleSetTags(p model.RoutePolicy) []string { return policyRuleSetTags(p) }

// policyRuleSetTags is one rule's geo tags, spelled exactly as the compiler
// spells them: trimmed and lower-cased, because that is the file name the node
// is given.
func policyRuleSetTags(p model.RoutePolicy) []string {
	var out []string
	for _, cond := range p.Match() {
		var prefix string
		switch cond.Kind {
		case model.CondGeoIP:
			prefix = "geoip-"
		case model.CondGeoSite:
			prefix = "geosite-"
		default:
			continue
		}
		for _, v := range cond.Values {
			if v = strings.ToLower(strings.TrimSpace(v)); v != "" {
				out = append(out, prefix+v)
			}
		}
	}
	return out
}
