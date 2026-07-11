// Package idgen generates short, readable, unique ids for panel-managed
// objects (nodes, domains, ...). Ids are internal — operators never need to
// type or remember one.
package idgen

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
)

// New makes an id from a base label: lowercased, restricted to [a-z0-9]
// (other separators collapse to '-'), capped at 24 characters, plus a random
// 4-hex-character suffix for uniqueness. Falls back to fb if base slugifies
// to nothing (e.g. an all-non-Latin name).
func New(base, fb string) string {
	s := strings.ToLower(strings.TrimSpace(base))
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ' || r == '-' || r == '_' || r == '.':
			b.WriteRune('-')
		}
	}
	slug := strings.Trim(b.String(), "-")
	if len(slug) > 24 {
		slug = slug[:24]
	}
	if slug == "" {
		slug = fb
	}
	suf := make([]byte, 2)
	_, _ = rand.Read(suf)
	return slug + "-" + hex.EncodeToString(suf)
}
