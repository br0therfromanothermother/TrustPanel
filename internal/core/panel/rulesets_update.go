package panel

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"trustpanel/internal/core/journal"
	"trustpanel/internal/core/model"
)

// updateRuleSet fetches one rule-set from its source, puts it in place of the
// cached copy, and asks the fleet to converge.
//
// The nodes are what matter here: until a reconcile carries the new file out,
// an update has changed nothing anyone routes by. markDirty schedules that, and
// the periodic reconcile is the backstop if auto-sync is off.
func (p *Panel) updateRuleSet(ctx context.Context, tag, actor string) (string, error) {
	ch, err := p.ruleSets.Update(ctx, tag)
	if err != nil {
		return "", err
	}
	if ch.NewID == ch.OldID {
		return "", nil // the source holds what we already have
	}
	if err := p.store.RecordRuleSetUpdated(ctx, tag, ch.NewSize, ch.NewID, ch.At); err != nil {
		return "", err
	}
	line := tag + ": " + sizeDelta(ch.OldSize, ch.NewSize)
	p.recordEvent(ctx, model.EventAdmin, model.SeverityInfo,
		journal.Line("geo database updated — %s: %s", tag, sizeDeltaMsg(ch.OldSize, ch.NewSize)), actor, "")
	p.markDirty()
	return line, nil
}

// sizeDelta renders a size change the way it reads in a journal line, because
// "updated" alone tells nobody whether a list grew by four domains or lost half
// of itself.
//
// Equal sizes are the case worth naming: a rebuild can change which domains are
// in a list without changing how many bytes it takes, and "1.3 KiB → 1.3 KiB"
// reads as a bug in the logging rather than as the fact it is.
// sizeDeltaMsg is sizeDelta written for the journal: the sizes read the same in
// any language, the sentence between them does not.
func sizeDeltaMsg(from, to int64) journal.Msg {
	switch {
	case from == 0:
		return journal.Text(humanSize(to))
	case from == to:
		return journal.Line("%s, same size, different contents", humanSize(to))
	}
	return journal.Text(humanSize(from) + " → " + humanSize(to))
}

func sizeDelta(from, to int64) string {
	switch {
	case from == 0:
		return humanSize(to)
	case from == to:
		return humanSize(to) + ", same size, different contents"
	}
	return humanSize(from) + " → " + humanSize(to)
}

func humanSize(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KiB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

func (p *Panel) handleUpdateRuleSet(w http.ResponseWriter, r *http.Request) {
	if p.ruleSets == nil {
		writeErr(w, http.StatusServiceUnavailable, "geo databases are not enabled on this panel")
		return
	}
	tag := r.PathValue("tag")
	line, err := p.updateRuleSet(r.Context(), tag, p.account(r).Username)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	if line == "" {
		writeErr(w, http.StatusConflict, "the source holds the same copy — nothing to update")
		return
	}
	p.writeRuleSetsView(w, r)
}

// handleUpdateAllRuleSets updates every rule-set the last check found to be
// behind. A set nobody has checked is left alone: updating on no evidence would
// re-download the whole cache for nothing.
func (p *Panel) handleUpdateAllRuleSets(w http.ResponseWriter, r *http.Request) {
	if p.ruleSets == nil {
		writeErr(w, http.StatusServiceUnavailable, "geo databases are not enabled on this panel")
		return
	}
	sets, err := p.store.RuleSets(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	actor := p.account(r).Username
	var done int
	var failed []string
	for _, s := range sets {
		if !s.Stale() {
			continue
		}
		if line, err := p.updateRuleSet(r.Context(), s.Tag, actor); err != nil {
			// One bad set must not strand the rest: the whole point of the button
			// is to finish the list.
			failed = append(failed, s.Tag)
		} else if line != "" {
			done++
		}
	}
	if len(failed) > 0 {
		sort.Strings(failed)
		p.recordEvent(r.Context(), model.EventAlert, model.SeverityWarn,
			journal.Line("geo databases that could not be updated: %s", strings.Join(failed, ", ")), actor, "")
	}
	if done == 0 && len(failed) > 0 {
		writeErr(w, http.StatusBadGateway, "none of the databases could be updated")
		return
	}
	p.writeRuleSetsView(w, r)
}

func (p *Panel) handleRollbackRuleSet(w http.ResponseWriter, r *http.Request) {
	if p.ruleSets == nil {
		writeErr(w, http.StatusServiceUnavailable, "geo databases are not enabled on this panel")
		return
	}
	tag := r.PathValue("tag")
	ch, err := p.ruleSets.Rollback(r.Context(), tag)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := p.store.RecordRuleSetRolledBack(r.Context(), tag, ch.NewSize, ch.NewID, ch.At); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	p.recordEvent(r.Context(), model.EventAdmin, model.SeverityWarn,
		journal.Line("geo database rolled back — %s: %s", tag, sizeDeltaMsg(ch.OldSize, ch.NewSize)), p.account(r).Username, "")
	p.markDirty()
	p.writeRuleSetsView(w, r)
}

func (p *Panel) handleSetRuleSetAutoUpdate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		On bool `json:"on"`
	}
	if !decode(w, r, &in) {
		return
	}
	tag := r.PathValue("tag")
	if err := p.store.SetRuleSetAutoUpdate(r.Context(), tag, in.On); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	line := journal.Line("%s no longer follows its source automatically", tag)
	if in.On {
		line = journal.Line("%s now follows its source automatically", tag)
	}
	p.recordEvent(r.Context(), model.EventAdmin, model.SeverityInfo, line, p.account(r).Username, "")
	p.writeRuleSetsView(w, r)
}

// applyAutoUpdates brings the sets that follow their source up to date, after a
// check has established which ones are behind.
//
// It runs on manual checks too. "Follow this source" is a standing instruction,
// and honouring it only on the scheduled run would make the button mean
// something different from the schedule for no reason anyone could guess.
func (p *Panel) applyAutoUpdates(ctx context.Context, actor string) {
	tags, err := p.store.AutoUpdateRuleSets(ctx)
	if err != nil || len(tags) == 0 {
		return
	}
	follow := make(map[string]bool, len(tags))
	for _, t := range tags {
		follow[t] = true
	}
	sets, err := p.store.RuleSets(ctx)
	if err != nil {
		return
	}
	var failed []string
	for _, s := range sets {
		if !follow[s.Tag] || !s.Stale() {
			continue
		}
		if _, err := p.updateRuleSet(ctx, s.Tag, actor); err != nil {
			failed = append(failed, s.Tag)
		}
	}
	if len(failed) > 0 {
		sort.Strings(failed)
		p.recordEvent(ctx, model.EventAlert, model.SeverityWarn,
			journal.Line("geo databases set to follow their source could not be updated: %s", strings.Join(failed, ", ")), actor, "")
	}
}

func (p *Panel) writeRuleSetsView(w http.ResponseWriter, r *http.Request) {
	v, err := p.ruleSetsView(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, v)
}
