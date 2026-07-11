package panel

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"trustpanel/internal/core/store"
)

const sessionCookie = "trustpanel_session"

// SessionManager holds browser sessions in memory. Sessions are deliberately
// not persisted/replicated: after a promote the operator re-logs in.
type SessionManager struct {
	mu       sync.Mutex
	sessions map[string]session
	ttl      time.Duration
	now      func() time.Time
}

type session struct {
	username  string
	csrf      string
	expiresAt time.Time
	// expandView is the bootstrap owner's opt-in cross-namespace lens. It lives on
	// the session (not persisted) so it auto-reverts on re-login — the see-all view
	// is a deliberate, temporary act, never a sticky default.
	expandView bool
}

func NewSessionManager(ttl time.Duration) *SessionManager {
	return &SessionManager{sessions: map[string]session{}, ttl: ttl, now: time.Now}
}

// Create issues a new session token for username, plus a per-session CSRF token
// (returned so the UI can echo it back in an X-CSRF-Token header on
// state-changing requests).
func (m *SessionManager) Create(username string) (token, csrf string, err error) {
	t, err := randToken()
	if err != nil {
		return "", "", err
	}
	c, err := randToken()
	if err != nil {
		return "", "", err
	}
	m.mu.Lock()
	m.sessions[t] = session{username: username, csrf: c, expiresAt: m.now().Add(m.ttl)}
	m.mu.Unlock()
	return t, c, nil
}

func randToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// CSRF returns the CSRF token bound to a live session.
func (m *SessionManager) CSRF(token string) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[token]
	if !ok || m.now().After(s.expiresAt) {
		return "", false
	}
	return s.csrf, true
}

// ValidCSRF reports whether csrf matches the token bound to a live session
// (constant-time compare).
func (m *SessionManager) ValidCSRF(token, csrf string) bool {
	want, ok := m.CSRF(token)
	if !ok || want == "" || csrf == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(want), []byte(csrf)) == 1
}

// Validate returns the username for a live session token.
func (m *SessionManager) Validate(token string) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[token]
	if !ok {
		return "", false
	}
	if m.now().After(s.expiresAt) {
		delete(m.sessions, token)
		return "", false
	}
	return s.username, true
}

// SetExpandView flips the bootstrap owner's cross-namespace lens for a live
// session. Returns false if the token is not live.
func (m *SessionManager) SetExpandView(token string, on bool) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[token]
	if !ok || m.now().After(s.expiresAt) {
		return false
	}
	s.expandView = on
	m.sessions[token] = s
	return true
}

// ExpandView reports whether the session has the cross-namespace lens enabled.
func (m *SessionManager) ExpandView(token string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[token]
	if !ok || m.now().After(s.expiresAt) {
		return false
	}
	return s.expandView
}

func (m *SessionManager) Revoke(token string) {
	m.mu.Lock()
	delete(m.sessions, token)
	m.mu.Unlock()
}

// RevokeUser drops every session belonging to username, returning how many were
// removed. Called when an account is deleted or demoted so a still-open browser
// session cannot keep acting with stale (or, before the fail-closed fix, escalated)
// privileges until its TTL lapses.
func (m *SessionManager) RevokeUser(username string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for t, s := range m.sessions {
		if s.username == username {
			delete(m.sessions, t)
			n++
		}
	}
	return n
}

// SetTTL updates the lifetime applied to sessions created from now on. Existing
// sessions keep their already-computed expiry. Used by the Settings tab so the
// session lifetime is tunable live (no serve restart).
func (m *SessionManager) SetTTL(ttl time.Duration) {
	if ttl <= 0 {
		return
	}
	m.mu.Lock()
	m.ttl = ttl
	m.mu.Unlock()
}

// RevokeAllExcept drops every session except the given token (used by "log out
// all other sessions"). Pass "" to revoke everything including the caller.
// Returns the number of sessions removed.
func (m *SessionManager) RevokeAllExcept(keep string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for t := range m.sessions {
		if t == keep {
			continue
		}
		delete(m.sessions, t)
		n++
	}
	return n
}

// loginThrottle blunts brute force on the cookie-auth login endpoint (the panel
// has no external WAF/IDS). It is keyed by username and applies an exponential
// lockout after a few free attempts. In-memory and best-effort: it resets on
// restart, which is fine for a localhost, few-account panel. (Keying on username
// means an attacker can lock out a known account by spamming it — acceptable here
// given the localhost+SSH-tunnel threat model; the alternative, keying on a
// behind-tunnel source IP, is not meaningful.)
type loginThrottle struct {
	mu      sync.Mutex
	entries map[string]*throttleEntry
	now     func() time.Time
}

type throttleEntry struct {
	fails       int
	lockedUntil time.Time
}

const (
	loginFreeAttempts = 5                // failures allowed before lockout kicks in
	loginBaseLock     = 2 * time.Second  // first lockout, doubles each further fail
	loginMaxLock      = 15 * time.Minute // cap
)

func newLoginThrottle() *loginThrottle {
	return &loginThrottle{entries: map[string]*throttleEntry{}, now: time.Now}
}

// retryAfter returns how long the username must wait before another attempt, or 0.
func (l *loginThrottle) retryAfter(username string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	e := l.entries[username]
	if e == nil {
		return 0
	}
	if d := e.lockedUntil.Sub(l.now()); d > 0 {
		return d
	}
	return 0
}

// fail records a failed attempt and (re)arms the lockout once past the free
// allowance, with exponentially growing duration capped at loginMaxLock.
func (l *loginThrottle) fail(username string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e := l.entries[username]
	if e == nil {
		e = &throttleEntry{}
		l.entries[username] = e
	}
	e.fails++
	if e.fails > loginFreeAttempts {
		shift := e.fails - loginFreeAttempts - 1
		lock := loginBaseLock << shift
		if lock > loginMaxLock || lock <= 0 { // <=0 guards shift overflow
			lock = loginMaxLock
		}
		e.lockedUntil = l.now().Add(lock)
	}
}

// success clears any throttle state for a username after a valid login.
func (l *loginThrottle) success(username string) {
	l.mu.Lock()
	delete(l.entries, username)
	l.mu.Unlock()
}

// HashPassword returns a bcrypt hash for storage.
func HashPassword(password string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(h), err
}

// verifyLogin checks a username/password against the stored admin hash.
func verifyLogin(ctx context.Context, s *store.Store, username, password string) (bool, error) {
	admin, err := s.AdminByUsername(ctx, username)
	if errors.Is(err, store.ErrNoAdmin) {
		// Run a dummy compare to keep timing similar for unknown users.
		_ = bcrypt.CompareHashAndPassword([]byte("$2a$10$invalidinvalidinvalidinvalidinvalidinvalidinvalidinv"), []byte(password))
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := bcrypt.CompareHashAndPassword([]byte(admin.PasswordHash), []byte(password)); err != nil {
		return false, nil
	}
	return true, nil
}
