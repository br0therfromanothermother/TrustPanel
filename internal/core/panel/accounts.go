package panel

import (
	"net/http"
	"strings"

	"trustpanel/internal/core/authz"
	"trustpanel/internal/core/model"
)

// nsLabel renders a namespace id for event messages — the empty infra namespace
// reads as "infra" rather than an empty string.
func nsLabel(ns string) string {
	if ns == "" {
		return "infra"
	}
	return ns
}

// currentUser resolves the logged-in operator (and their session token) from the
// request cookie. Returns ok=false when there is no live session — callers use
// this for self-targeting actions (change own password, revoke other sessions).
func (p *Panel) currentUser(r *http.Request) (username, token string, ok bool) {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return "", "", false
	}
	u, ok := p.sessions.Validate(c.Value)
	if !ok {
		return "", "", false
	}
	return u, c.Value, true
}

// seesAllNamespaces reports whether this request should get the bootstrap owner's
// cross-namespace see-all view. It requires BOTH bootstrap privilege AND the
// session's opt-in expand-view lens: the default, even for the bootstrap owner, is
// its own namespace only. Scoped accounts never see all regardless of the flag.
func (p *Panel) seesAllNamespaces(r *http.Request, acct model.AdminAccount) bool {
	if !acct.IsBootstrapOwner() {
		return false
	}
	_, token, ok := p.currentUser(r)
	if !ok {
		return false
	}
	return p.sessions.ExpandView(token)
}

// unresolvedNamespace is the sentinel namespace stamped on the powerless account
// returned when a live session names an account that no longer resolves. It
// contains a NUL byte so it can never equal a real username/namespace, making
// every authz Can*/ScopeState check deny.
const unresolvedNamespace = "\x00unresolved"

// account resolves the logged-in account (with its role) for authorization. When
// there is no live session AND no accounts exist yet (fresh install, open setup),
// it returns a synthetic admin so bootstrap is never locked out — protected()
// keeps that path reachable only while no account exists.
//
// FAIL CLOSED: a live session whose account does not resolve (deleted account) or
// a store error must NOT become a fleet owner. Returning an admin in the infra
// namespace here was a privilege-escalation hole (a fired co-owner kept a valid
// session cookie → every request resolved to a synthetic fleet owner). Instead we
// return a powerless operator in a sentinel namespace that matches no resource.
// protected() also rejects this case up front; this is defense in depth for any
// handler that calls account() directly.
func (p *Panel) account(r *http.Request) model.AdminAccount {
	username, _, ok := p.currentUser(r)
	if !ok {
		return model.AdminAccount{Role: model.RoleAdmin}
	}
	a, err := p.store.AdminByUsername(r.Context(), username)
	if err != nil {
		return model.AdminAccount{Username: username, Role: model.RoleOperator, NamespaceID: unresolvedNamespace}
	}
	return a
}

// adminView is the browser-facing account row (never a hash).
type adminView struct {
	Username    string `json:"username"`
	Role        string `json:"role"`
	Namespace   string `json:"namespace"`
	HasTelegram bool   `json:"has_telegram"`
	HasPassword bool   `json:"has_password"`
	AlertChatID string `json:"alert_chat_id,omitempty"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
	IsCurrent   bool   `json:"is_current"`
}

// handleListAdmins lists every account across all namespaces. Account management
// is the bootstrap owner's exclusive surface (decisions 7/11) — a co-owner admin
// or operator gets 403.
func (p *Panel) handleListAdmins(w http.ResponseWriter, r *http.Request) {
	acct := p.account(r)
	if !acct.IsBootstrapOwner() {
		writeErr(w, http.StatusForbidden, "account management is available to the owner only")
		return
	}
	admins, err := p.store.ListAdmins(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	me, _, _ := p.currentUser(r)
	out := make([]adminView, 0, len(admins))
	for _, a := range admins {
		role := a.Role
		if !role.Valid() {
			role = model.RoleAdmin
		}
		out = append(out, adminView{
			Username:    a.Username,
			Role:        string(role),
			Namespace:   a.Namespace(),
			HasTelegram: a.TelegramID != nil,
			HasPassword: a.PasswordHash != "",
			AlertChatID: a.AlertChatID,
			CreatedAt:   a.CreatedAt.UTC().Format("2006-01-02"),
			UpdatedAt:   a.UpdatedAt.UTC().Format("2006-01-02"),
			IsCurrent:   a.Username == me,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"admins": out})
}

// handleCreateAdmin adds an account (or resets an existing infra admin's
// password). It is the bootstrap owner's exclusive surface: mint a
// brand-new operator (its own single-member namespace), seed an explicit
// namespace with an admin/operator (a co-owner), or create/reset an infra admin
// (role=admin, no namespace). Co-owner admins and operators are forbidden.
func (p *Panel) handleCreateAdmin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username, Password, Role, Namespace string
		TelegramID                          int64 `json:"telegram_id"`
	}
	if !decode(w, r, &req) {
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	if req.Username == "" {
		writeErr(w, http.StatusBadRequest, "username is required")
		return
	}
	acct := p.account(r)
	if !acct.IsBootstrapOwner() {
		writeErr(w, http.StatusForbidden, "account creation is available to the owner only")
		return
	}
	role := model.Role(req.Role)
	if req.Namespace != "" && role != model.RoleAdmin {
		role = model.RoleOperator
	} else if role == "" {
		role = model.RoleAdmin
	}
	// Role gates which field is mandatory (role == access surface, the roles
	// design): an operator's only surface is the bot, so its Telegram id must be
	// bound now or it could never reach anything — a panel password is optional.
	// An admin's surface is the panel, so the reverse holds: password mandatory,
	// Telegram optional.
	if role == model.RoleOperator {
		if req.TelegramID == 0 {
			writeErr(w, http.StatusBadRequest, "telegram id is required for the operator role")
			return
		}
		if req.Password != "" && len(req.Password) < 8 {
			writeErr(w, http.StatusBadRequest, "password must be at least 8 characters")
			return
		}
	} else if len(req.Password) < 8 {
		writeErr(w, http.StatusBadRequest, "password must be at least 8 characters")
		return
	}
	var hash string
	if req.Password != "" {
		h, err := HashPassword(req.Password)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		hash = h
	}
	actor, _, _ := p.currentUser(r)

	// Bootstrap owner mints accounts/namespaces.
	switch {
	case req.Namespace != "":
		// Seed/extend an explicit namespace (mint if missing).
		if err := p.store.CreateNamespace(r.Context(), req.Namespace, req.Namespace); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := p.store.CreateAccountIn(r.Context(), req.Username, hash, role, req.Namespace); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
	case role == model.RoleOperator:
		// Mint a fresh single-member namespace named after the operator.
		if err := p.store.CreateAccount(r.Context(), req.Username, hash, model.RoleOperator); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
	default:
		// Infra admin: create, or reset an existing admin's password.
		if err := p.store.UpsertAdmin(r.Context(), req.Username, hash); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	// Operators have no panel, so the owner binds their Telegram id here at creation
	// — otherwise a fresh operator could never reach the bot (its sole surface).
	if req.TelegramID != 0 {
		tg := req.TelegramID
		if err := p.store.SetAccountTelegram(r.Context(), req.Username, &tg, ""); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	p.recordEvent(r.Context(), model.EventAdmin, model.SeverityInfo,
		"account \""+req.Username+"\" created/updated", actor, "") // infra action
	writeJSON(w, http.StatusOK, map[string]string{"username": req.Username})
}

// handleSetRole changes an account's role (admin<->operator) in place. The fleet
// owner may change anyone; a namespace admin may change only members of its own
// namespace and may not grant infra. Guards keep a namespace (and the fleet)
// from losing its last admin.
func (p *Panel) handleSetRole(w http.ResponseWriter, r *http.Request) {
	username := r.PathValue("username")
	var req struct {
		Role       string
		Password   string
		TelegramID int64 `json:"telegram_id"`
	}
	if !decode(w, r, &req) {
		return
	}
	role := model.Role(req.Role)
	if role != model.RoleAdmin && role != model.RoleOperator {
		writeErr(w, http.StatusBadRequest, "role must be admin or operator")
		return
	}
	actor := p.account(r)
	target, err := p.store.AdminByUsername(r.Context(), username)
	if err != nil {
		writeErr(w, http.StatusNotFound, "account not found")
		return
	}
	if !authz.CanManageMembers(actor, target.Namespace()) {
		writeErr(w, http.StatusForbidden, "you cannot manage members of that namespace")
		return
	}
	if target.Role == role {
		writeJSON(w, http.StatusOK, map[string]string{"username": username, "role": string(role)})
		return
	}
	// Demoting an admin -> operator must not leave the infra namespace without an
	// owner, nor strip a namespace of its last admin WHILE OTHER MEMBERS REMAIN
	// (they would have no one to manage them). Demoting the sole member of a
	// single-account namespace is fine — there is no one left to strand. (Mirrors
	// the DeleteAdmin guards.)
	if target.Role.IsAdmin() && role == model.RoleOperator {
		n, err := p.store.CountNamespaceAdmins(r.Context(), target.Namespace())
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if n <= 1 {
			if target.Namespace() == "" {
				writeErr(w, http.StatusBadRequest, "cannot demote the last owner")
				return
			}
			total, err := p.store.CountNamespaceAccounts(r.Context(), target.Namespace())
			if err != nil {
				writeErr(w, http.StatusInternalServerError, err.Error())
				return
			}
			if total > 1 {
				writeErr(w, http.StatusBadRequest, "cannot demote the last admin of a namespace while members remain")
				return
			}
		}
	}
	// Role gates the field the target needs for its new surface (role == access
	// surface, mirrors handleCreateAdmin): promoting to admin needs a panel
	// password, demoting to operator needs a Telegram id. Skip the ask if the
	// account already carries one from an earlier stint in that role — an admin
	// demoted to operator may already have Telegram bound, and an operator
	// promoted to admin may already have a password from before.
	if role == model.RoleAdmin && target.PasswordHash == "" {
		if len(req.Password) < 8 {
			writeErr(w, http.StatusBadRequest, "password is required to make this account an admin")
			return
		}
		hash, err := HashPassword(req.Password)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if err := p.store.UpsertAdmin(r.Context(), username, hash); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	if role == model.RoleOperator && target.TelegramID == nil {
		if req.TelegramID == 0 {
			writeErr(w, http.StatusBadRequest, "telegram id is required to make this account an operator")
			return
		}
		tg := req.TelegramID
		if err := p.store.SetAccountTelegram(r.Context(), username, &tg, target.AlertChatID); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if err := p.store.SetRole(r.Context(), username, role); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	// A role change alters privileges; force the account to re-authenticate so a
	// live session cannot keep acting under its previous role. (account() re-reads
	// the role each request, but revoking is the clean, explicit boundary.)
	p.sessions.RevokeUser(username)
	me, _, _ := p.currentUser(r)
	// Scope member-management events to the ACTOR's namespace so they land in the
	// journal of whoever performed them (the fleet owner sees changes it makes to
	// any namespace; a namespace admin sees its own).
	p.recordEvent(r.Context(), model.EventAdmin, model.SeverityWarn,
		"role of \""+username+"\" set to "+string(role)+" (namespace \""+nsLabel(target.Namespace())+"\")", me, actor.Namespace())
	writeJSON(w, http.StatusOK, map[string]string{"username": username, "role": string(role)})
}

// handleSetAccountTelegram binds the signed-in account's own Telegram id (for the
// role-aware management bot) and alert chat. Self-service for every account —
// not infra-only. An empty/zero id clears the binding.
func (p *Panel) handleSetAccountTelegram(w http.ResponseWriter, r *http.Request) {
	me, _, ok := p.currentUser(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "authentication required")
		return
	}
	var req struct {
		TelegramID  int64  `json:"telegram_id"`
		AlertChatID string `json:"alert_chat_id"`
	}
	if !decode(w, r, &req) {
		return
	}
	var tg *int64
	if req.TelegramID != 0 {
		tg = &req.TelegramID
	}
	if err := p.store.SetAccountTelegram(r.Context(), me, tg, strings.TrimSpace(req.AlertChatID)); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	p.recordEvent(r.Context(), model.EventAdmin, model.SeverityInfo,
		"Telegram binding updated for \""+me+"\"", me, p.account(r).Namespace()) // self-service: own namespace
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleSetAccountLocale saves the signed-in account's UI language so it follows
// the account across devices and reaches the bot. Self-service; "" clears it.
func (p *Panel) handleSetAccountLocale(w http.ResponseWriter, r *http.Request) {
	me, _, ok := p.currentUser(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "authentication required")
		return
	}
	var req struct {
		Locale string `json:"locale"`
	}
	if !decode(w, r, &req) {
		return
	}
	loc := strings.TrimSpace(req.Locale)
	if loc != "" && loc != "en" && loc != "ru" {
		writeErr(w, http.StatusBadRequest, "locale must be en or ru")
		return
	}
	if err := p.store.SetAccountLocale(r.Context(), me, loc); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (p *Panel) handleDeleteAdmin(w http.ResponseWriter, r *http.Request) {
	username := r.PathValue("username")
	me, _, _ := p.currentUser(r)
	if username == me {
		writeErr(w, http.StatusBadRequest, "you cannot delete the account you are signed in as")
		return
	}
	actor := p.account(r)
	target, err := p.store.AdminByUsername(r.Context(), username)
	if err != nil {
		writeErr(w, http.StatusNotFound, "account not found")
		return
	}
	if !authz.CanManageMembers(actor, target.Namespace()) {
		writeErr(w, http.StatusForbidden, "you cannot manage members of that namespace")
		return
	}
	if err := p.store.DeleteAdmin(r.Context(), username); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	// Revoke the deleted account's live sessions at once — otherwise its session
	// cookie keeps working until TTL (and, combined with the former fail-open, was
	// escalating to fleet owner). protected() now also rejects deleted accounts.
	p.sessions.RevokeUser(username)
	// Scope to the actor's namespace so the deletion shows up in the journal of
	// whoever performed it (see handleSetRole).
	p.recordEvent(r.Context(), model.EventAdmin, model.SeverityWarn,
		"account \""+username+"\" ("+string(target.Role)+", namespace \""+nsLabel(target.Namespace())+"\") deleted", me, actor.Namespace())
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleChangePassword rotates the signed-in operator's own password. The current
// password must be supplied and verified, so a hijacked session alone cannot lock
// the real operator out by silently changing the password.
func (p *Panel) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	me, token, ok := p.currentUser(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "authentication required")
		return
	}
	var req struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if !decode(w, r, &req) {
		return
	}
	if len(req.New) < 8 {
		writeErr(w, http.StatusBadRequest, "new password must be at least 8 characters")
		return
	}
	good, err := verifyLogin(r.Context(), p.store, me, req.Current)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !good {
		writeErr(w, http.StatusForbidden, "current password is incorrect")
		return
	}
	hash, err := HashPassword(req.New)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := p.store.UpsertAdmin(r.Context(), me, hash); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Drop every other session: a changed password should invalidate logins
	// elsewhere. The caller's current session is kept so the UI stays usable.
	p.sessions.RevokeAllExcept(token)
	p.recordEvent(r.Context(), model.EventAdmin, model.SeverityWarn,
		"password changed for \""+me+"\" (other sessions revoked)", me, p.account(r).Namespace()) // self-service: own namespace
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
