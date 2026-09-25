package bot

import (
	"context"
	"log"
	"sort"
	"strings"
	"time"

	"trustpanel/internal/core/model"
)

// The geo categories a rule may name are whatever the connected source offers,
// and that list is already in the database: every check writes down the
// categories the repository published, per kind (see store.RecordCatalog). The
// bot reads that same list and not one of its own.
//
// A short list of category names written into the source would be neither the
// catalogue (the SagerNet geosite repository publishes some 1900) nor a
// selection anyone had agreed on, and a name typed past it would go into the
// rule unexamined. Nothing rejects a category that does not exist at save time:
// it surfaces later, when the control plane cannot fetch that rule-set, and the
// node's whole configuration push fails with it (controller.ruleSetFiles). So
// the catalogue is also what checks the typing.

// maxGeoQuickAdds caps the one-tap categories on the value screen. They are a
// shortcut, not a way to read a catalogue of four figures; the typed name
// covers that.
const maxGeoQuickAdds = 12

// maxRefusedValues caps how many unrecognised names one reply is named back
// with, which keeps a pasted list from filling the screen with its own contents.
const maxRefusedValues = 5

// geoCat is one kind's catalogue as the last check left it.
type geoCat struct {
	kind  string
	names map[string]bool
	at    *time.Time
	// err holds what the last check failed with. The names outlive it deliberately,
	// so a source that cannot be reached keeps offering the categories it last
	// published and a screen can say the list is older than its date.
	err string
}

// geoCatalogs reads both kinds in one query. A catalogue that cannot be read is
// an empty one: the value screen then says it has no list to check against,
// which is the truth, instead of falling back to names of its own.
func (b *Bot) geoCatalogs(ctx context.Context) map[string]geoCat {
	out := map[string]geoCat{
		model.RuleSetGeoSite: {kind: model.RuleSetGeoSite},
		model.RuleSetGeoIP:   {kind: model.RuleSetGeoIP},
	}
	srcs, err := b.store.RuleSetSources(ctx)
	if err != nil {
		log.Printf("bot: geo catalogue: %v", err)
		return out
	}
	for _, s := range srcs {
		c := geoCat{kind: s.Kind, names: make(map[string]bool, len(s.Catalog)), at: s.CatalogAt, err: s.CheckError}
		for _, tag := range s.Catalog {
			// Stored tags carry the source's own prefix (geosite-openai); a rule
			// names the category (openai) and the renderer puts the prefix back.
			c.names[strings.TrimPrefix(tag, s.Kind+"-")] = true
		}
		out[s.Kind] = c
	}
	return out
}

// geoCatalog is geoCatalogs for one kind.
func (b *Bot) geoCatalog(ctx context.Context, kind string) geoCat { return b.geoCatalogs(ctx)[kind] }

// have reports whether a catalogue was ever recorded for this kind. Without one
// there is nothing to check a name against, and nothing to list.
func (c geoCat) have() bool { return len(c.names) > 0 }

// count is how many categories the source offered.
func (c geoCat) count() int { return len(c.names) }

// resolve returns the catalogue's own spelling of a typed name and whether the
// catalogue has it. A category name is a file name upstream, and its case is not
// a matter of taste; with no catalogue to consult, lower case is the form every
// source known here publishes.
func (c geoCat) resolve(v string) (string, bool) {
	v = strings.TrimSpace(v)
	if c.names[v] {
		return v, true
	}
	if l := strings.ToLower(v); c.names[l] {
		return l, true
	}
	return strings.ToLower(v), false
}

// sorted is the catalogue as a list, for the screens that show all of it.
func (c geoCat) sorted() []string {
	out := make([]string, 0, len(c.names))
	for name := range c.names {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// missing returns the values this catalogue does not offer, in the order given.
// With no catalogue it returns nothing: an unchecked name is not a wrong one.
func (c geoCat) missing(vals []string) []string {
	if !c.have() {
		return nil
	}
	var out []string
	for _, v := range vals {
		if v = strings.TrimSpace(v); v == "" {
			continue
		}
		if _, ok := c.resolve(v); !ok {
			out = append(out, v)
		}
	}
	return out
}

// geoInUse lists the categories of one kind that rules in this view already
// name. It is the one-tap list on the value screen: the category another rule
// uses is the one reached for next, and unlike a hand-written shortlist it is
// the panel's own data and not a guess about what people want. Names the
// catalogue does not offer are left out: they are the bug, not a shortcut.
func geoInUse(st model.State, kind string, cat geoCat, max int) []string {
	seen := map[string]bool{}
	for _, p := range st.RoutePolicies {
		for _, cond := range p.Match() {
			if cond.Kind != kind {
				continue
			}
			for _, v := range cond.Values {
				if v = strings.TrimSpace(v); v == "" {
					continue
				}
				if !cat.have() {
					seen[v] = true
					continue
				}
				if canon, ok := cat.resolve(v); ok {
					seen[canon] = true
				}
			}
		}
	}
	out := make([]string, 0, len(seen))
	for v := range seen {
		out = append(out, v)
	}
	sort.Strings(out)
	if len(out) > max {
		out = out[:max]
	}
	return out
}

// policyGeoGaps splits the categories a saved rule names and its source no
// longer offers into the two things that can mean, because they are not the same
// news at all.
//
// frozen: the panel holds a copy, and the rule works exactly as it did and always
// will. That version of the category is permanent, and a domain added to it
// upstream will never be matched. Worth saying, nothing to fix today.
//
// missing: there is no copy. The rule cannot be compiled onto a node, and every
// server it applies to stops receiving configuration of any kind until the rule
// is edited. That is the one that has to read like an alarm.
func (b *Bot) policyGeoGaps(ctx context.Context, p model.RoutePolicy) (frozen, missing []string) {
	var site, ip []string
	for _, cond := range p.Match() {
		switch cond.Kind {
		case model.CondGeoSite:
			site = append(site, cond.Values...)
		case model.CondGeoIP:
			ip = append(ip, cond.Values...)
		}
	}
	if len(site) == 0 && len(ip) == 0 {
		return nil, nil
	}
	return b.geoGaps(ctx, site, ip)
}

// geoGaps is the split itself, over one set of geosite and geoip values.
func (b *Bot) geoGaps(ctx context.Context, site, ip []string) (frozen, missing []string) {
	cats := b.geoCatalogs(ctx)
	gone := map[string]string{} // value -> tag
	for _, v := range cats[model.RuleSetGeoSite].missing(site) {
		gone[v] = model.RuleSetGeoSite + "-" + strings.ToLower(v)
	}
	for _, v := range cats[model.RuleSetGeoIP].missing(ip) {
		gone[v] = model.RuleSetGeoIP + "-" + strings.ToLower(v)
	}
	if len(gone) == 0 {
		return nil, nil
	}
	held := b.heldRuleSets(ctx)
	for _, v := range sortedKeys(gone) {
		if held[gone[v]] {
			frozen = append(frozen, v)
			continue
		}
		missing = append(missing, v)
	}
	return frozen, missing
}

// heldRuleSets is the set of categories the panel has a copy of. The rows are
// the panel's record of what the routing depends on and what it holds for each;
// a row with no content is a category it could not get.
func (b *Bot) heldRuleSets(ctx context.Context) map[string]bool {
	out := map[string]bool{}
	sets, err := b.store.RuleSets(ctx)
	if err != nil {
		log.Printf("bot: rule-sets: %v", err)
		return out
	}
	for _, s := range sets {
		if s.ContentID != "" {
			out[s.Tag] = true
		}
	}
	return out
}

// sortedKeys is the map's keys in order, which lists the same names on a screen in the
// same order every time it is drawn.
func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// checkGeoValues filters one reply through the catalogue for the kind being
// edited: a recognised name comes back in the catalogue's own spelling, an
// unrecognised one is refused and kept only to be named back on the screen.
// Kinds with no catalogue behind them, domains and CIDRs, pass through.
//
// There is no way past the refusal, because there is nothing on the other side
// of it. The catalogue is the listing of the same repository the .srs is later
// fetched from. A name it does not have is a name no node can be given: the
// rule would be written, saved, reported created, and then hold up every
// configuration push to the nodes it applies to. A category that is genuinely
// newer than the list is a stale list, and the answer to that is another check
// of the source, not a rule written against a guess.
//
// A kind with no catalogue at all is a different matter: nothing has been read,
// so nothing is known, and refusing a name would be a verdict from ignorance.
// Those go in as typed, with the screen saying they could not be checked.
func (b *Bot) checkGeoValues(ctx context.Context, d *routeDraft, vals []string) []string {
	d.refused, d.refusedKind = nil, d.editKind
	if d.editKind != model.CondGeoSite && d.editKind != model.CondGeoIP {
		return vals
	}
	cat := b.geoCatalog(ctx, d.editKind)
	out := make([]string, 0, len(vals))
	for _, v := range vals {
		canon, ok := cat.resolve(v)
		if ok || !cat.have() {
			out = append(out, canon)
			continue
		}
		if !containsStr(d.refused, canon) && len(d.refused) < maxRefusedValues {
			d.refused = append(d.refused, canon)
		}
	}
	return out
}

// draftGeoGaps is policyGeoGaps for a rule still being written. Nothing typed
// here can reach it, since the value screen refuses a name the source does not
// have, but an edited rule arrives carrying whatever it was written with.
func (b *Bot) draftGeoGaps(ctx context.Context, d routeDraft) (frozen, missing []string) {
	if len(d.geosite) == 0 && len(d.geoip) == 0 {
		return nil, nil
	}
	return b.geoGaps(ctx, d.geosite, d.geoip)
}
