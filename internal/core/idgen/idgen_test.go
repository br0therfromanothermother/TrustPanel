package idgen

import (
	"strings"
	"testing"
)

func TestNewSlugifiesAndSuffixes(t *testing.T) {
	id := New("Exit NL #1!", "node")
	if !strings.HasPrefix(id, "exit-nl-1-") {
		t.Fatalf("got %q, want a slug prefix of exit-nl-1-", id)
	}
	suf := id[strings.LastIndex(id, "-")+1:]
	if len(suf) != 4 {
		t.Fatalf("suffix %q should be 4 hex chars", suf)
	}
}

func TestNewFallsBackOnEmptySlug(t *testing.T) {
	id := New("привет", "node")
	if !strings.HasPrefix(id, "node-") {
		t.Fatalf("got %q, want fallback prefix node-", id)
	}
}

func TestNewCapsLength(t *testing.T) {
	id := New(strings.Repeat("a", 100), "node")
	slug := id[:strings.LastIndex(id, "-")]
	if len(slug) != 24 {
		t.Fatalf("slug len = %d, want 24", len(slug))
	}
}

func TestNewSuffixVariesAcrossCalls(t *testing.T) {
	// The 4-hex-char suffix is a collision-avoidance aid, not a uniqueness
	// guarantee (that's the DB's job) — just check it isn't a constant.
	a := New("exit", "node")
	b := New("exit", "node")
	if a == b {
		t.Fatalf("two calls produced the same id %q — suffix isn't randomizing", a)
	}
}
