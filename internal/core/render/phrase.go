package render

import "trustpanel/internal/core/journal"

// Phrase is one sentence the panel puts in front of an operator: the English it
// has always been, and the Russian it is read in. Both are produced where the
// sentence is built, because the names, counts and verdicts inside it stop being
// separable the moment it is flattened into a string, the same reason the event
// log stores two columns instead of translating rows afterwards.
//
// The compiler and the route tester speak in whole sentences with data in the
// middle ("exit Frankfurt (n-exit-02) is out of rotation → rule "ai" falls back
// to …"), which is exactly the shape a per-string dictionary in the browser
// cannot reach. So they build journal.Msg and hand over both renderings.
type Phrase struct {
	Text string `json:"text"`         // English: the source, and the key it was translated by
	RU   string `json:"ru,omitempty"` // empty when the Russian is the same sentence
}

// String is the English, so a Phrase logs and compares as the string it replaced.
func (p Phrase) String() string { return p.Text }

// say renders a built sentence into the pair.
func say(m journal.Msg) Phrase {
	p := Phrase{Text: journal.Render(m, journal.Lang)}
	if ru := journal.Render(m, "ru"); ru != p.Text {
		p.RU = ru
	}
	return p
}

// Say builds a Phrase for callers outside the package: the panel adds a line
// of its own to a tester result before it goes out.
func Say(m journal.Msg) Phrase { return say(m) }

// plain is a sentence with nothing to translate: a name, an id, an error from
// somewhere else.
func plain(s string) Phrase { return Phrase{Text: s} }

// SamePhrases reports whether two lists say the same thing, for callers that
// only act when a set of warnings changes.
func SamePhrases(a, b []Phrase) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
