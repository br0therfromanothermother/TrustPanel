package panel

import (
	"net/http"
	"strconv"
	"strings"

	"trustpanel/internal/core/journal"
	"trustpanel/internal/core/model"
	"trustpanel/internal/core/recovery"
	"trustpanel/internal/core/store"
)

// handleRecoverPassword sets a new password for an account that proved control
// of its bound Telegram by quoting a one-time code the management bot issued.
// It is deliberately unauthenticated, the caller being locked out, so every
// guard the login path has applies here too, and the panel
// still only listens on localhost behind an SSH tunnel.
func (p *Panel) handleRecoverPassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username    string `json:"username"`
		Code        string `json:"code"`
		NewPassword string `json:"new_password"`
	}
	if !decode(w, r, &req) {
		return
	}
	username := strings.TrimSpace(req.Username)

	// Throttle under a key of its own rather than the login key: a flood of bad
	// codes must not lock the real operator out of a login they still know.
	throttleKey := "recover:" + username
	if d := p.logins.retryAfter(throttleKey); d > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(int(d.Seconds())+1))
		writeErr(w, http.StatusTooManyRequests, "too many attempts; try again later")
		return
	}
	if username == "" || !recovery.Valid(req.Code) {
		p.logins.fail(throttleKey)
		writeErr(w, http.StatusBadRequest, "username and a valid recovery code are required")
		return
	}
	if len(req.NewPassword) < 8 {
		// Not a credential guess, so it must not count toward the lockout.
		writeErr(w, http.StatusBadRequest, "new password must be at least 8 characters")
		return
	}

	owner, err := p.store.RedeemRecoveryCode(r.Context(), recovery.Hash(req.Code))
	if err != nil {
		p.logins.fail(throttleKey)
		writeErr(w, http.StatusUnauthorized, store.ErrRecoveryCode.Error())
		return
	}
	// The code is spent either way now. It must also belong to the account the
	// caller named, so a code cannot be redeemed against someone else's login.
	if owner != username {
		p.logins.fail(throttleKey)
		writeErr(w, http.StatusUnauthorized, store.ErrRecoveryCode.Error())
		return
	}

	hash, err := HashPassword(req.NewPassword)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := p.store.UpsertAdmin(r.Context(), username, hash); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	p.logins.success(throttleKey)
	// Nobody holds a legitimate session at this point, the operator having been
	// locked out, so drop every one rather than leave a stale login alive.
	p.sessions.RevokeAllExcept("")

	var ns string
	if a, err := p.store.AdminByUsername(r.Context(), username); err == nil {
		ns = a.Namespace()
	}
	p.recordEvent(r.Context(), model.EventAdmin, model.SeverityWarn,
		journal.Line("password reset for %q with a Telegram recovery code (all sessions revoked)", username), username, ns)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
