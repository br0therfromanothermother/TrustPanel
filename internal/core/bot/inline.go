package bot

import (
	"context"
	"log"
	"sort"
	"strconv"
	"strings"

	"trustpanel/internal/core/model"
)

// The geo catalogue, searched from inside the chat.
//
// A source publishes on the order of two thousand categories. They have no
// sections, no meaningful ranking and no short list that is honestly "the
// common ones", and a keyboard cannot hold the rest. Telegram already has the
// interface for
// exactly this: the inline box, which filters a list as you type and hands the
// pick back to the bot.
//
// Two things about it are worth knowing before reading the code. The catalogue
// button opens the box pre-filled with the kind being chosen, so nobody has to
// remember a command. And a pick reaches the bot as chosen_inline_result, which
// Telegram only delivers when inline feedback is enabled for the bot; the
// message the pick leaves in the chat is not what adds the tag, because then the
// text of any message could.

// maxInlineResults is Telegram's cap on one answer.
const maxInlineResults = 50

// maxResultID is Telegram's cap on a result's id, which carries the
// draft binding and the tag itself.
const maxResultID = 64

// inlineCatalog answers one inline query: a page of the connected source's
// catalogue for the kind the button asked for, ranked against what was typed.
func (b *Bot) inlineCatalog(ctx context.Context, q *tgInlineQuery) {
	if q == nil || q.From == nil {
		return
	}
	if _, ok := b.authorize(ctx, q.From.ID); !ok {
		// The catalogue is panel data; an unbound account gets nothing to scroll.
		b.answerInline(ctx, q.ID, nil, "")
		return
	}
	kind, text := splitInlineQuery(q.Query)
	c := b.convoFor(q.From.ID)
	cat := b.geoCatalog(ctx, kind)
	if cat.err != "" {
		b.answerInline(ctx, q.ID, []inlineArticle{note(tr(ctx, "The category list could not be read"), cat.err)}, "")
		return
	}
	if len(cat.names) == 0 {
		b.answerInline(ctx, q.ID, []inlineArticle{note(
			tr(ctx, "There is no category list yet"),
			tr(ctx, "The panel writes it on its next source check."))}, "")
		return
	}
	names := rankCatalog(cat.sorted(), text)
	start := 0
	if q.Offset != "" {
		if n, err := strconv.Atoi(q.Offset); err == nil && n > 0 {
			start = n
		}
	}
	if start >= len(names) {
		b.answerInline(ctx, q.ID, nil, "")
		return
	}
	end := start + maxInlineResults
	if end > len(names) {
		end = len(names)
	}
	var out []inlineArticle
	for _, name := range names[start:end] {
		id := resultID(c, kind, name)
		if id == "" {
			continue // the binding plus this name does not fit in a result id
		}
		a := inlineArticle{ID: id, Type: "article", Title: name}
		if c != nil && c.kind == kNewRoute && c.rt.has(kind, name) {
			a.Description = tr(ctx, "✓ Added")
		}
		a.Content.Text = "🗂 " + kind + ": " + name
		out = append(out, a)
	}
	next := ""
	if end < len(names) {
		next = strconv.Itoa(end)
	}
	b.answerInline(ctx, q.ID, out, next)
}

// inlinePicked is one entry actually chosen. It is the only thing that writes a
// tag into a draft, and it checks the whole binding first: the same operator,
// the same open form, the same step, the same kind, and a tag the source still
// has. A result from a form that has since been cancelled does not resurrect it.
func (b *Bot) inlinePicked(ctx context.Context, r *tgChosenInlineResult) {
	if r == nil || r.From == nil {
		return
	}
	acct, ok := b.authorize(ctx, r.From.ID)
	if !ok {
		return
	}
	token, kind, name, ok := parseResultID(r.ResultID)
	if !ok {
		return // a note, or something we did not issue
	}
	c := b.convoFor(r.From.ID)
	if c == nil || c.kind != kNewRoute || c.token != token || c.step != rsMatchVals || c.rt.editKind != kind {
		b.say(ctx, r.From.ID, tr(ctx, "That form is no longer open, so nothing was added."))
		return
	}
	if c.rt.has(kind, name) {
		// Picking the same entry twice is one category, not two.
		b.sayValues(ctx, acct, kind, &c.rt, r.From.ID)
		return
	}
	// The same check a typed name goes through: the source is asked whether it
	// still publishes this category, because the source can be repointed between
	// the search and the pick.
	c.rt.addValues(kind, b.checkGeoValues(ctx, &c.rt, []string{name}))
	b.sayValues(ctx, acct, kind, &c.rt, r.From.ID)
}

// sayValues sends the value screen as a new message. A pick arrives without the
// chat it was made in, so the answer goes to the operator's own chat with the
// bot, which is where the form was opened from in any case.
func (b *Bot) sayValues(ctx context.Context, acct model.AdminAccount, kind string, d *routeDraft, to int64) {
	text, kb := b.routeValues(ctx, acct, kind, d)
	if err := b.client.sendMessage(ctx, to, text, kb); err != nil {
		log.Printf("bot: inline pick reply: %v", err)
	}
}

// say sends one plain line.
func (b *Bot) say(ctx context.Context, to int64, text string) {
	if err := b.client.sendMessage(ctx, to, text, ""); err != nil {
		log.Printf("bot: inline reply: %v", err)
	}
}

func (b *Bot) answerInline(ctx context.Context, id string, results []inlineArticle, next string) {
	if err := b.client.answerInlineQuery(ctx, id, results, next); err != nil {
		log.Printf("bot: answerInlineQuery: %v", err)
	}
}

// note is a result that says something instead of offering a category. Its id
// carries no binding, so picking it changes nothing.
func note(title, detail string) inlineArticle {
	a := inlineArticle{ID: "note", Type: "article", Title: title, Description: detail}
	a.Content.Text = title
	return a
}

// splitInlineQuery reads the kind the catalogue button pre-filled and the text
// typed after it. Anything else is treated as a geosite search, which is the
// catalogue the button offers.
func splitInlineQuery(q string) (kind, text string) {
	q = strings.TrimSpace(q)
	for _, k := range []string{model.CondGeoSite, model.CondGeoIP} {
		if rest, ok := strings.CutPrefix(q, k); ok && (rest == "" || strings.HasPrefix(rest, " ")) {
			return k, strings.TrimSpace(rest)
		}
	}
	return model.CondGeoSite, q
}

// rankCatalog orders the catalogue against a query: the exact name first, then
// the ones that start with it, then the ones that contain it, alphabetical
// within each. An empty query is the plain alphabetical catalogue: there is no
// "most popular" to promote, and inventing one would be the curated list this
// replaced.
func rankCatalog(names []string, query string) []string {
	sorted := append([]string(nil), names...)
	sort.Strings(sorted)
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return sorted
	}
	var exact, prefix, contains []string
	for _, n := range sorted {
		low := strings.ToLower(n)
		switch {
		case low == q:
			exact = append(exact, n)
		case strings.HasPrefix(low, q):
			prefix = append(prefix, n)
		case strings.Contains(low, q):
			contains = append(contains, n)
		}
	}
	return append(append(exact, prefix...), contains...)
}

// resultID binds a result to the operator's open draft: the form's token, the
// kind being chosen, and the exact name. A result issued for one form cannot be
// applied to another, and a name that does not fit the id is left out of the
// list instead of shortened into a different category.
func resultID(c *convo, kind, name string) string {
	if c == nil || c.kind != kNewRoute || c.token == "" {
		return ""
	}
	id := "a:" + c.token + ":" + kindLetter(kind) + ":" + name
	if len(id) > maxResultID {
		return ""
	}
	return id
}

func parseResultID(id string) (token, kind, name string, ok bool) {
	parts := strings.SplitN(id, ":", 4)
	if len(parts) != 4 || parts[0] != "a" {
		return "", "", "", false
	}
	switch parts[2] {
	case "s":
		kind = model.CondGeoSite
	case "i":
		kind = model.CondGeoIP
	default:
		return "", "", "", false
	}
	if parts[1] == "" || parts[3] == "" {
		return "", "", "", false
	}
	return parts[1], kind, parts[3], true
}

func kindLetter(kind string) string {
	if kind == model.CondGeoIP {
		return "i"
	}
	return "s"
}
