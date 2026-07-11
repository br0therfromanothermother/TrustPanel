package watchdog

import (
	"strings"
	"testing"
)

// TestCodeEscapesAndPlainTextStrips guards the two invariants the HTML alert path
// relies on: Code escapes HTML-hostile characters so a stray '<' (e.g. an error
// printed as "<nil>") can't break Telegram parsing, and PlainText fully reverses
// the formatting so the log/event-log sinks store a clean, tag-free line.
func TestCodeEscapesAndPlainTextStrips(t *testing.T) {
	c := Code("<nil> & bad")
	if strings.Contains(c, "<nil>") || !strings.Contains(c, "&lt;nil&gt;") || !strings.Contains(c, "&amp;") {
		t.Fatalf("Code must escape HTML metacharacters, got %q", c)
	}
	if got := PlainText(c); got != "<nil> & bad" {
		t.Fatalf("PlainText must round-trip to the original text, got %q", got)
	}
}

// TestMessagesLeadWithOneGlyph checks the unified emoji vocabulary: a down alert
// leads with ❗️, its recovery with ✅, a degraded condition with ⚠️, and each
// carries the (escaped) node name, never a bare id.
func TestMessagesLeadWithOneGlyph(t *testing.T) {
	if d := MsgNodeDown("exit1", "exit", "boom")("en"); !strings.HasPrefix(d, GlyphDown) || !strings.Contains(d, "<code>exit1</code>") {
		t.Errorf("node-down: %q", d)
	}
	if u := MsgNodeUp("exit1", "exit")("en"); !strings.HasPrefix(u, GlyphUp) {
		t.Errorf("node-up: %q", u)
	}
	if b := MsgBilling("exit1", -3, "2026-07-01")("en"); !strings.HasPrefix(b, GlyphDegraded) || strings.Contains(b, "exit1-id") {
		t.Errorf("billing should be degraded and carry the display name only: %q", b)
	}
}

// TestLocalizationFollowsLang checks that a builder renders Russian for "ru" and
// the English source for "en"/unknown, so the alert follows the reader's UI
// language rather than being hardcoded.
func TestLocalizationFollowsLang(t *testing.T) {
	m := MsgNodeDown("exit1", "exit", "boom")
	if en := m("en"); !strings.Contains(en, "is DOWN") {
		t.Errorf("en render should be English: %q", en)
	}
	if ru := m("ru"); !strings.Contains(ru, "НЕДОСТУПЕН") {
		t.Errorf("ru render should be Russian: %q", ru)
	}
	if fb := m("de"); fb != m("en") {
		t.Errorf("unknown lang should fall back to English source, got %q", fb)
	}
}
