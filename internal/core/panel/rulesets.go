package panel

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"trustpanel/internal/core/journal"
	"trustpanel/internal/core/model"
	"trustpanel/internal/core/render"
	"trustpanel/internal/core/rulesets"
	"trustpanel/internal/core/watchdog"
)

// RuleSetCatalog is the panel's view of the geo rule-set mirror, the copies in
// use, and the repositories both are filled from.
type RuleSetCatalog interface {
	// Mirror refreshes the local copy of one source and reports what changed.
	Mirror(ctx context.Context, kind string) (rulesets.MirrorResult, error)
	// MirrorTags is the catalogue: what the mirrored source offers right now.
	MirrorTags(kind string) ([]rulesets.CatalogEntry, error)
	// Catalog lists a source that cannot be mirrored, over the network.
	Catalog(ctx context.Context, kind string) ([]rulesets.CatalogEntry, error)
	// Pin puts a category in place as the copy its rules route by.
	Pin(ctx context.Context, tag string) (bool, error)
	Cached() ([]rulesets.CachedSet, error)
	SetBases(geoip, geosite string)
	Update(ctx context.Context, tag string) (rulesets.Change, error)
	Rollback(ctx context.Context, tag string) (rulesets.Change, error)
}

// SetRuleSets wires the geo rule-set provider so the panel can report what it
// holds and compare it against the source. Without it the Geo databases surface
// reports itself unavailable rather than pretending the cache is empty.
func (p *Panel) SetRuleSets(c RuleSetCatalog) { p.ruleSets = c }

// ruleSetsView is the browser's view: the two sources, everything cached, and
// the addresses of the sources the panel already knows, which lets the UI offer them
// without hardcoding a copy that could drift from the one downloads use.
type ruleSetsView struct {
	Sources []model.RuleSetSource        `json:"sources"`
	Sets    []model.RuleSet              `json:"sets"`
	Presets map[string][]rulesets.Preset `json:"presets"`
	// CheckHours is the effective cadence, not the stored zero: the UI shows the
	// interval that is actually in force.
	CheckHours int `json:"check_hours"`
	// NextCheckAt is when the schedule comes round again. It is derived from the
	// last check rather than from process start. A check run by hand pushes
	// the next one out, as anyone who just pressed the button expects.
	NextCheckAt *time.Time `json:"next_check_at,omitempty"`
}

func (p *Panel) handleListRuleSets(w http.ResponseWriter, r *http.Request) {
	v, err := p.ruleSetsView(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (p *Panel) ruleSetsView(ctx context.Context) (ruleSetsView, error) {
	srcs, err := p.store.RuleSetSources(ctx)
	if err != nil {
		return ruleSetsView{}, err
	}
	sets, err := p.store.RuleSets(ctx)
	if err != nil {
		return ruleSetsView{}, err
	}
	hours := 24 * 7
	interval := 24 * 7 * time.Hour
	if st, err := p.store.GetSettings(ctx); err == nil {
		interval = st.Panel.GeoCheckInterval()
		hours = int(interval / time.Hour)
	}
	var next *time.Time
	if at, ok, _ := oldestCheck(srcs); ok {
		due := at.Add(interval)
		next = &due
	}
	return ruleSetsView{
		Sources: srcs,
		Sets:    sets,
		Presets: map[string][]rulesets.Preset{
			model.RuleSetGeoIP:   rulesets.Presets(model.RuleSetGeoIP),
			model.RuleSetGeoSite: rulesets.Presets(model.RuleSetGeoSite),
		},
		CheckHours:  hours,
		NextCheckAt: next,
	}, nil
}

// handleSetRuleSetSchedule changes how often the sources are asked what they
// offer. It lives here rather than under settings because it is the cadence of
// this one surface, and reads as a property of it.
func (p *Panel) handleSetRuleSetSchedule(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Hours int `json:"hours"`
	}
	if !decode(w, r, &in) {
		return
	}
	st, err := p.store.GetSettings(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	st.Panel.GeoCheckHours = in.Hours
	if err := p.store.SaveSettings(r.Context(), st); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	v, err := p.ruleSetsView(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, v)
}

type ruleSetSourceUpdate struct {
	Kind   string `json:"kind"`
	Preset string `json:"preset"`
	URL    string `json:"url"`
}

// handleSetRuleSetSource repoints one geo database at another repository.
//
// It does not re-download anything. Changing a source changes where the NEXT
// copy comes from; the files already on the nodes stay exactly as they are until
// someone updates them, because a source change is an editing action and a
// routing change is not something to make as a side effect of one.
func (p *Panel) handleSetRuleSetSource(w http.ResponseWriter, r *http.Request) {
	var in ruleSetSourceUpdate
	if !decode(w, r, &in) {
		return
	}
	kind := strings.TrimSpace(in.Kind)
	if kind != model.RuleSetGeoIP && kind != model.RuleSetGeoSite {
		writeErr(w, http.StatusBadRequest, "unknown database "+in.Kind)
		return
	}
	preset, addr := strings.TrimSpace(in.Preset), strings.TrimSpace(in.URL)
	if preset != "" && preset != rulesets.PresetCustom {
		addr = rulesets.PresetURL(kind, preset)
		if addr == "" {
			writeErr(w, http.StatusBadRequest, "unknown source "+preset)
			return
		}
	} else {
		preset = rulesets.PresetCustom
		if err := validSourceURL(addr); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if err := p.store.SetRuleSetSource(r.Context(), kind, preset, addr); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := p.ApplyRuleSetSources(r.Context()); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	p.recordEvent(r.Context(), model.EventAdmin, model.SeverityInfo,
		journal.Line("geo database %s now reads from %s", kind, addr), p.account(r).Username, "")
	v, err := p.ruleSetsView(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// validSourceURL rejects an address a tag cannot be appended to, and anything
// unencrypted. The panel is the only machine that reaches the source, and it is
// the one machine whose traffic must not describe the routing it is about to
// apply.
func validSourceURL(addr string) error {
	u, err := url.Parse(addr)
	if err != nil {
		return fmt.Errorf("that address cannot be parsed")
	}
	if u.Scheme != "https" {
		return fmt.Errorf("the address must start with https://")
	}
	if u.Host == "" {
		return fmt.Errorf("the address has no host")
	}
	if !strings.HasSuffix(u.Path, "/") {
		return fmt.Errorf("the address must end with /, a category name is appended to it")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("the address must be a plain directory, with no query or fragment")
	}
	return nil
}

// ApplyRuleSetSources points the live provider at the stored addresses. An
// edit takes effect without a restart and a chosen source survives one.
func (p *Panel) ApplyRuleSetSources(ctx context.Context) error {
	if p.ruleSets == nil {
		return nil
	}
	srcs, err := p.store.RuleSetSources(ctx)
	if err != nil {
		return err
	}
	var geoip, geosite string
	for _, s := range srcs {
		switch s.Kind {
		case model.RuleSetGeoIP:
			geoip = s.URL
		case model.RuleSetGeoSite:
			geosite = s.URL
		}
	}
	p.ruleSets.SetBases(geoip, geosite)
	return nil
}

func (p *Panel) handleCheckRuleSets(w http.ResponseWriter, r *http.Request) {
	if p.ruleSets == nil {
		writeErr(w, http.StatusServiceUnavailable, "geo databases are not enabled on this panel")
		return
	}
	if err := p.CheckRuleSets(r.Context(), p.account(r).Username); err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	v, err := p.ruleSetsView(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// CheckRuleSets refreshes the local mirror of each source and records what that
// changed.
//
// The mirror is the whole source: one refresh answers both questions at once:
// which categories exist, and which of the copies our rules route by are behind.
// Nothing the nodes run changes here: a copy in use is replaced only by an
// update, asked for or armed in advance.
func (p *Panel) CheckRuleSets(ctx context.Context, actor string) error {
	if p.ruleSets == nil {
		return fmt.Errorf("geo databases are not enabled on this panel")
	}
	now := p.now()
	// What we believed before this look, which lets the journal report what changed
	// rather than restating the same pending update every time the check runs.
	before := map[string]model.RuleSet{}
	if prev, err := p.store.RuleSets(ctx); err == nil {
		for _, s := range prev {
			before[s.Tag] = s
		}
	}

	var failed []string
	offered := map[string]map[string]rulesets.CatalogEntry{}
	for _, kind := range []string{model.RuleSetGeoIP, model.RuleSetGeoSite} {
		entries, err := p.refreshSource(ctx, kind)
		if err != nil {
			// A source that could not be refreshed leaves the last catalogue and
			// the last mirror standing: "we could not look" and "everything is
			// gone" must not look alike.
			_ = p.store.RecordCatalogError(ctx, kind, err.Error(), now)
			failed = append(failed, kind)
			continue
		}
		tags := make([]string, 0, len(entries))
		byTag := make(map[string]rulesets.CatalogEntry, len(entries))
		for _, e := range entries {
			tags = append(tags, e.Tag)
			byTag[e.Tag] = e
		}
		offered[kind] = byTag
		if err := p.store.RecordCatalog(ctx, kind, tags, now); err != nil {
			return err
		}
	}

	st, err := p.store.LoadState(ctx)
	if err != nil {
		return err
	}
	inUse := render.RuleSetTagsInUse(st)
	p.pinInUse(ctx, inUse)
	if err := p.recordInUse(ctx, inUse, offered, now); err != nil {
		return err
	}

	p.recordRuleSetFindings(ctx, actor, failed, before, st)
	// Sets told to follow their source do so now, while the comparison that
	// established which ones are behind is the freshest thing we know.
	p.applyAutoUpdates(ctx, actor)
	if len(failed) == 2 {
		return fmt.Errorf("neither geo database could be reached")
	}
	return nil
}

// refreshSource brings one source's local copy up to date and returns what it
// offers. A source that can be mirrored is mirrored, since that is one artifact
// and the categories it lists cannot disagree with the files it hands over. A source
// that cannot (a plain file server, which can neither be archived nor listed
// as a repository) falls back to asking it for a listing, as before.
func (p *Panel) refreshSource(ctx context.Context, kind string) ([]rulesets.CatalogEntry, error) {
	res, err := p.ruleSets.Mirror(ctx, kind)
	if err == nil {
		if !res.Unchanged {
			log.Printf("geo mirror: %s now holds %d categories (%d changed, %d new, %d withdrawn)",
				kind, res.Count, len(res.Changed), len(res.Added), len(res.Removed))
		}
		return p.ruleSets.MirrorTags(kind)
	}
	entries, listErr := p.ruleSets.Catalog(ctx, kind)
	if listErr == nil {
		return entries, nil
	}
	// Report the mirror's failure: for every source that can be mirrored it is
	// the real reason, and the listing's failure is a consequence of the same
	// thing being unreachable.
	return nil, err
}

// pinInUse puts every category the rules name on disk before anything needs it.
// This is the whole point of the mirror: a reconcile then reads a local file
// instead of reaching for the network at the moment a node is waiting for its
// configuration.
//
// A category that cannot be had at all is not an error here. The rule that names
// it is the problem, and it is reported where it can be acted on (the Geo tab,
// the rule's own card) rather than by failing the check that found it.
func (p *Panel) pinInUse(ctx context.Context, tags []string) {
	for _, tag := range tags {
		if _, err := p.ruleSets.Pin(ctx, tag); err != nil {
			log.Printf("geo: %s is named by a rule and cannot be fetched: %v", tag, err)
		}
	}
}

// recordInUse writes one row per category the rules name, and only those. The
// table never lists a set no rule references and then offers to update it. What it
// answers now is "what does the routing depend on, and is it current?".
//
// A category with no copy at all still gets its row: that a rule names something
// we do not hold is the single most important thing this surface can say.
func (p *Panel) recordInUse(ctx context.Context, inUse []string, offered map[string]map[string]rulesets.CatalogEntry, now time.Time) error {
	held := map[string]rulesets.CachedSet{}
	if cached, err := p.ruleSets.Cached(); err == nil {
		for _, c := range cached {
			held[c.Tag] = c
		}
	}
	for _, tag := range inUse {
		kind := ruleSetKind(tag)
		if kind == "" {
			continue
		}
		rs := model.RuleSet{Tag: tag, Kind: kind}
		if c, ok := held[tag]; ok {
			rs.Size, rs.ContentID = c.Size, c.ContentID
			if at := c.FetchedAt; !at.IsZero() {
				rs.FetchedAt = &at
			}
		}
		if err := p.store.RecordRuleSetLocal(ctx, rs); err != nil {
			return err
		}
		byTag, looked := offered[kind]
		if !looked {
			continue // this source could not be refreshed; last week's verdict stands
		}
		e, still := byTag[tag]
		if err := p.store.RecordRuleSetUpstream(ctx, tag, e.ContentID, e.Size, !still, now); err != nil {
			return err
		}
	}
	return p.store.PruneRuleSets(ctx, inUse)
}

// ruleSetKind reads which database a tag belongs to.
func ruleSetKind(tag string) string {
	switch {
	case strings.HasPrefix(tag, model.RuleSetGeoIP+"-"):
		return model.RuleSetGeoIP
	case strings.HasPrefix(tag, model.RuleSetGeoSite+"-"):
		return model.RuleSetGeoSite
	}
	return ""
}

// recordRuleSetFindings writes one journal line per kind of news, and only for
// what this check learned. A pending update that was already reported is not
// reported again: a weekly check would otherwise repeat the same line forever
// and train everyone to skip past it.
func (p *Panel) recordRuleSetFindings(ctx context.Context, actor string, failed []string, before map[string]model.RuleSet, st model.State) {
	sets, err := p.store.RuleSets(ctx)
	if err != nil {
		return
	}
	var stale, gone []string
	held := map[string]model.RuleSet{}
	for _, s := range sets {
		held[s.Tag] = s
		was, known := before[s.Tag]
		switch {
		case s.UpstreamGone:
			if !known || !was.UpstreamGone {
				gone = append(gone, s.Tag)
			}
		case s.Stale():
			// A different upstream id is a different update, and is news again.
			if !known || !was.Stale() || was.UpstreamID != s.UpstreamID {
				stale = append(stale, s.Tag)
			}
		}
	}
	sort.Strings(stale)
	sort.Strings(gone)
	if len(failed) > 0 {
		p.recordEvent(ctx, model.EventAlert, model.SeverityWarn,
			journal.Line("geo database %s could not be reached", journal.And(failed)), actor, "")
	}
	if len(gone) > 0 {
		p.recordEvent(ctx, model.EventAlert, model.SeverityWarn,
			journal.Line("no longer offered by the source: %s — routing keeps using the local copy", strings.Join(gone, ", ")), actor, "")
		p.alertWithdrawn(ctx, gone, held, st)
	}
	if len(stale) > 0 {
		p.recordEvent(ctx, model.EventAlert, model.SeverityInfo,
			journal.Line("a newer version is available for %s", strings.Join(stale, ", ")), actor, "")
	}
}

// alertWithdrawn pages about a category the routing depends on and the source
// has stopped publishing.
//
// Every row that reaches here is named by a rule, since the table holds nothing
// else. This is never noise about a file nobody uses. It is also not an
// emergency: the copy in use keeps working, which is precisely why it has to be
// said out loud. Nothing else would ever mention it again, and the rule is
// frozen at the version we hold: a domain added to that category upstream will
// never be matched by it.
//
// The one case that is an emergency is a category with no copy at all. A rule
// naming it stops every configuration update to the nodes it applies to. It
// is alerted as critical and worded as the thing to go and fix.
func (p *Panel) alertWithdrawn(ctx context.Context, gone []string, held map[string]model.RuleSet, st model.State) {
	for _, tag := range gone {
		rs := held[tag]
		byOwner := map[string][]string{}
		var all []string
		for _, pol := range render.PoliciesUsingRuleSet(st, tag) {
			name := policyLabel(pol)
			all = append(all, name)
			byOwner[pol.OwnerID] = append(byOwner[pol.OwnerID], name)
		}
		if len(all) == 0 {
			continue // the rule went away between the check and this line
		}
		haveCopy := rs.ContentID != ""
		since := "—"
		if rs.FetchedAt != nil {
			since = rs.FetchedAt.UTC().Format("2006-01-02")
		}
		sev := watchdog.SeverityLow
		if !haveCopy {
			sev = watchdog.SeverityCritical
		}
		// The admin leg names every affected rule; each owner is told about their
		// own rules only, because a rule name belongs to its namespace.
		if p.geoAlerts != nil {
			_ = p.geoAlerts.AlertLocalized(ctx, sev, watchdog.MsgGeoCategoryGone(tag, strings.Join(all, ", "), since, haveCopy))
		}
		for owner, names := range byOwner {
			if owner == "" {
				continue // the admin namespace is already covered by the leg above
			}
			p.sendOwnerAlert(ctx, owner, sev, watchdog.MsgGeoCategoryGone(tag, strings.Join(names, ", "), since, haveCopy))
		}
	}
}

// policyLabel names a rule the way its owner reads it, falling back to the id
// for a rule that was never given a name.
func policyLabel(p model.RoutePolicy) string {
	if n := strings.TrimSpace(p.Name); n != "" {
		return n
	}
	return p.ID
}

// RunRuleSetCheckLoop re-asks the geo sources what they offer, on a cadence read
// live from settings each cycle. interval is the fallback; <= 0 disables it.
func (p *Panel) RunRuleSetCheckLoop(ctx context.Context, interval time.Duration) {
	if interval <= 0 || p.ruleSets == nil {
		return
	}
	// Sleep until the next check is due rather than a fixed period from process
	// start. dynLoop re-reads this every cycle. A check run by hand, or an
	// edited cadence, moves the schedule instead of being ignored by it.
	dynLoop(ctx, func() time.Duration {
		every := p.liveInterval(model.PanelSettings.GeoCheckInterval, func(ps model.PanelSettings) int { return ps.GeoCheckHours }, interval)
		srcs, err := p.store.RuleSetSources(ctx)
		if err != nil {
			return every
		}
		at, ok, failing := oldestCheck(srcs)
		if !ok {
			return time.Minute // never looked; do it rather than wait a week to start
		}
		// A source that could not be listed is retried within the hour. Letting a
		// transient failure cost a whole cadence would leave the lists stale for
		// a week over a minute of network trouble.
		if failing && every > time.Hour {
			every = time.Hour
		}
		if left := at.Add(every).Sub(p.now()); left > time.Minute {
			return left
		}
		return time.Minute
	}, func() {
		if err := p.CheckRuleSets(ctx, "system"); err != nil {
			log.Printf("geo database check: %v", err)
		}
	})
}

// oldestCheck reports when every source had last been looked at, and whether any
// of them failed. The oldest of the two is the honest answer: a pass that
// reached one source and not the other has not answered the question.
func oldestCheck(srcs []model.RuleSetSource) (at time.Time, ok bool, failing bool) {
	for _, s := range srcs {
		if s.CheckError != "" {
			failing = true
		}
		if s.CheckedAt == nil {
			return time.Time{}, false, failing
		}
		if at.IsZero() || s.CheckedAt.Before(at) {
			at = *s.CheckedAt
		}
	}
	return at, !at.IsZero(), failing
}

// ensureRuleSetPins puts every category the rules name on disk, before a push
// needs it. The source check does the same thing on its own cadence; this is
// what covers a rule written between two checks, including one written from the
// bot, which reaches the database without passing through the panel at all.
//
// It does nothing while the set of categories in use is the same as last time,
// so the reconcile loop's steady state costs one comparison.
func (p *Panel) ensureRuleSetPins(ctx context.Context, st model.State) {
	if p.ruleSets == nil {
		return
	}
	tags := render.RuleSetTagsInUse(st)
	key := strings.Join(tags, ",")
	p.pinMu.Lock()
	same := key == p.pinnedKey
	p.pinnedKey = key
	p.pinMu.Unlock()
	if same {
		return
	}
	p.pinInUse(ctx, tags)
}
