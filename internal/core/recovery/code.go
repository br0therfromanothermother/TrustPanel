// Package recovery mints and normalizes the one-time codes that let an operator
// who lost the panel password prove control of the bound Telegram account. The
// bot issues a code, the panel redeems it; both sides share this file so the two
// processes agree on the alphabet, the normalization and the hash.
package recovery

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// TTLMinutes is how long an issued code stays redeemable. Short enough that a
// code left sitting in a Telegram history is dead by the time anyone finds it,
// long enough to open an SSH tunnel and reach the panel.
const TTLMinutes = 10

// codeLen is the number of significant characters. With the 31-letter alphabet
// below that is ~39 bits, far past guessing range for a single-use secret that
// expires in minutes and sits behind the panel's per-account throttle.
const codeLen = 8

// alphabet omits the character pairs people transcribe wrongly (0/O, 1/I/L), so
// a code read off a phone screen can be typed into the panel without ambiguity.
const alphabet = "23456789ABCDEFGHJKMNPQRSTUVWXYZ"

// NewCode returns a fresh code in display form ("XXXX-XXXX"). The dash is
// cosmetic; Normalize strips it, so the operator may type it either way.
func NewCode() (string, error) {
	buf := make([]byte, codeLen)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	out := make([]byte, codeLen)
	for i, b := range buf {
		// Modulo bias over a 31-letter alphabet is negligible at this length and
		// costs nothing that matters for a code this short-lived.
		out[i] = alphabet[int(b)%len(alphabet)]
	}
	return string(out[:4]) + "-" + string(out[4:]), nil
}

// Normalize reduces a typed code to its canonical form: upper-cased, with every
// character outside the alphabet (dashes, spaces, stray punctuation) dropped. It
// feeds both issuing and redeeming, so formatting never decides a match.
func Normalize(code string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(strings.TrimSpace(code)) {
		if strings.ContainsRune(alphabet, r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Hash returns the SHA-256 of the normalized code. Only this reaches the
// database: a row read cannot replay a live code, and lookup stays a single
// indexed comparison. bcrypt would buy nothing here: anyone who can read the
// table can already rewrite the password hash next to it.
func Hash(code string) string {
	sum := sha256.Sum256([]byte(Normalize(code)))
	return hex.EncodeToString(sum[:])
}

// Valid reports whether a typed code has the right shape to be worth a lookup.
func Valid(code string) bool { return len(Normalize(code)) == codeLen }
