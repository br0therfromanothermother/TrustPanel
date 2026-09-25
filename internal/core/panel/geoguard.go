package panel

import (
	"sort"
	"strings"

	"trustpanel/internal/core/model"
	"trustpanel/internal/core/render"
)

// A rule names its geo categories by the file name they have upstream, and a
// name the source does not publish cannot be turned into a rule-set for a node.
// The controller then fails that node's whole configuration push, not the one
// rule, so the server stops receiving updates until somebody notices.
//
// The form refuses such a name while it is being typed and so does the bot, but
// the panel's own API does the writing, and a rule can arrive from
// an older client or a script. This is the check that decides.
//
// Two things it deliberately does not do. It does not judge a value the rule
// already had: a category can be withdrawn upstream long after the rule was
// written, the copy already held keeps working, and refusing to save a rename
// because of it would leave the rule unfixable from here. And with no catalogue
// for that kind, a source never checked, it says nothing, because a name
// cannot be called wrong against a list nobody has.
func unknownGeoValues(incoming, cur model.RoutePolicy, existed bool, srcs []model.RuleSetSource) []string {
	tags := render.PolicyRuleSetTags(incoming)
	if len(tags) == 0 {
		return nil
	}
	had := map[string]bool{}
	if existed {
		for _, t := range render.PolicyRuleSetTags(cur) {
			had[t] = true
		}
	}
	catalog := map[string]map[string]bool{}
	for _, s := range srcs {
		if len(s.Catalog) == 0 {
			continue
		}
		set := make(map[string]bool, len(s.Catalog))
		for _, name := range s.Catalog {
			set[strings.ToLower(strings.TrimSpace(name))] = true
		}
		catalog[string(s.Kind)] = set
	}
	seen := map[string]bool{}
	var out []string
	for _, tag := range tags {
		if had[tag] || seen[tag] {
			continue
		}
		known := catalog[kindOfTag(tag)]
		if len(known) == 0 || known[tag] {
			continue
		}
		seen[tag] = true
		out = append(out, tag)
	}
	sort.Strings(out)
	return out
}

// kindOfTag reads which catalogue a tag belongs to from its own prefix, which is
// how the sources name their files.
func kindOfTag(tag string) string {
	switch {
	case strings.HasPrefix(tag, "geosite-"):
		return string(model.RuleSetGeoSite)
	case strings.HasPrefix(tag, "geoip-"):
		return string(model.RuleSetGeoIP)
	}
	return ""
}
