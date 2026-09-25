// Package panel is the operator-facing control plane: a localhost HTTP API for
// managing fleet state (in Postgres) and pushing rendered desired-state to node
// agents.
package panel

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"trustpanel/internal/core/agentapi"
	"trustpanel/internal/core/authz"
	"trustpanel/internal/core/clientcfg"
	"trustpanel/internal/core/controller"
	"trustpanel/internal/core/egress"
	"trustpanel/internal/core/jobs"
	"trustpanel/internal/core/journal"
	"trustpanel/internal/core/model"
	"trustpanel/internal/core/render"
	"trustpanel/internal/core/store"
	"trustpanel/internal/core/watchdog"
	"trustpanel/webui"
)

// recordAlert hands a built alert to a recording channel without flattening it
// first: the journal leg can render it in both languages, and a channel that
// only takes text still gets the English one. An alert rendered before it is
// handed over reaches the journal in one language and stays there.
func recordAlert(ctx context.Context, a watchdog.Alerter, sev watchdog.Severity, msg watchdog.MsgFunc) {
	if a == nil {
		return
	}
	if la, ok := a.(watchdog.LocalizedAlerter); ok {
		_ = la.AlertLocalized(ctx, sev, msg)
		return
	}
	_ = a.Alert(ctx, sev, watchdog.Render(msg, watchdog.DefaultLang))
}

// MultiAlerter fans an alert out to several channels (e.g. log + Telegram).
type MultiAlerter []watchdog.Alerter

func (m MultiAlerter) Alert(ctx context.Context, sev watchdog.Severity, msg string) error {
	for _, a := range m {
		if err := a.Alert(ctx, sev, msg); err != nil {
			log.Printf("alert: channel failed: %v", err)
		}
	}
	return nil
}

// AlertLocalized fans a per-recipient message out to each channel: a channel that
// can localize (the Telegram leg) renders per recipient; the log/event legs get
// the canonical DefaultLang rendering.
func (m MultiAlerter) AlertLocalized(ctx context.Context, sev watchdog.Severity, msg watchdog.MsgFunc) error {
	for _, a := range m {
		var err error
		if la, ok := a.(watchdog.LocalizedAlerter); ok {
			err = la.AlertLocalized(ctx, sev, msg)
		} else {
			err = a.Alert(ctx, sev, watchdog.Render(msg, watchdog.DefaultLang))
		}
		if err != nil {
			log.Printf("alert: channel failed: %v", err)
		}
	}
	return nil
}

// Panel serves the operator API.
type Panel struct {
	store    *store.Store
	fleet    *controller.Fleet
	sessions *SessionManager
	logins   *loginThrottle
	nextRev  int64
	now      func() time.Time

	reconcileCount    int64 // atomic
	lastReconcileUnix int64 // atomic

	jobs *jobs.Manager
	prov *ProvisionConfig // nil unless EnableProvisioning was called

	metricsMu sync.Mutex
	metrics   map[string]nodeMetric // node id -> latest live system metrics

	billingAlerter watchdog.Alerter // nil => billing alerts disabled
	billingDays    int              // alert when paid_until is within this many days

	expiryRecorder watchdog.Alerter // nil => config-expiry alerts disabled (log + event leg)
	expiryDays     int              // warn an owner when a client config expires within this many days

	configRecorder watchdog.Alerter // nil => config-health alerts disabled (log + event leg)
	configMu       sync.Mutex
	configBad      map[string]bool // owner_id -> last config-health alert was "cannot build" (in-memory dedup)

	geo render.GeoMatcher // route-tester geo evaluator (nil => geo conditions unevaluated)

	ruleSets RuleSetCatalog // geo rule-set cache + sources (nil => geo databases surface disabled)

	reconcileRunMu sync.Mutex // single-flights ReconcileOnce (manual + loop)
	reconcileMu    sync.Mutex
	lastReconcile  map[string]reconcileRecord // node id -> latest reconcile outcome

	dirtyCh chan struct{} // coalesced auto-sync trigger (buffered 1); see markDirty

	edgeMu   sync.Mutex
	lastEdge map[string]edgeRecord // entry node id -> latest external :443 probe

	botHealthMu sync.Mutex
	botHealth   map[string]botHealthRecord // "bot"|"alert" -> latest getMe result

	replMu   sync.Mutex
	lastRepl map[string]replRecord // standby node id -> latest replication-slot health

	// Egress auto-failover bookkeeping (in-memory; no schema). Tracks each exit's
	// current outage so a sustained one auto-reassigns its groups to a healthy exit
	// exactly once, and resets on recovery. See reviewEgressFailover.
	egressMu          sync.Mutex
	exitDegradedSince map[string]time.Time // exit id -> when its current degraded streak began
	exitFailedOver    map[string]bool      // exit id -> auto egress-failover already executed this outage
	exitBlackholed    map[string]bool      // exit id -> "no healthy exit" alert already sent this outage

	// Background-monitor push alerting (nil => record-for-UI only, no Telegram).
	geoAlerts MultiAlerter // geo source news (a category a rule uses withdrawn)

	botName botNames // the management bot's own name, for the links in alerts

	pinMu     sync.Mutex // guards pinnedKey
	pinnedKey string     // the set of geo categories in use at the last reconcile

	nodeMon *watchdog.Monitor // per-node agent liveness (any node down/recovered)
	edgeMon *watchdog.Monitor // entry :443 external reachability
	replMon *watchdog.Monitor // standby replication-slot health
	botMon  *watchdog.Monitor // Telegram bot/alert channel reachability
	certMon *watchdog.Monitor // management certificates running out

	certMu   sync.Mutex                           // guards certSeen
	certSeen map[string]*agentapi.FleetCertStatus // last reported per node id
	certCA   string                               // the panel's own trust file
	certLeaf string                               // the panel's own client certificate
	certDays int                                  // warn this many days ahead
}

// SetMonitorAlerts wires push alerting onto the background monitors so a node
// dying (or recovering), an entry going unreachable, replication breaking, or a
// bot channel failing reaches the operator, not just the UI. Without it the
// monitors still record state for the UI but send nothing. Call once at startup.
// The alerter is the α (primary-side) sender; it runs on the active control
// plane, so it cannot report the active node's own death; that is the standby
// watchdog's (β) job.
// base carries the audience-agnostic legs (console log, event log, plus any
// explicit flag/env Telegram/webhook channel). Each monitor adds its own
// audience-scoped Telegram leg: data-plane status (node liveness, entry edge) is
// broadcast to every account with an alert chat; control-plane internals
// (replication, bot-channel health) page the admin chat only.
func (p *Panel) SetMonitorAlerts(base watchdog.Alerter) {
	// The floors below are measured on the panel's clock, read at each sample so
	// a test that installs its own after this call still governs them.
	clock := func() time.Time {
		if p.now != nil {
			return p.now()
		}
		return time.Now()
	}
	mon := func(aud audience, threshold int) *watchdog.Monitor {
		return watchdog.NewMonitor(MultiAlerter{base, routedTelegram{p: p, aud: aud}}, threshold).WithClock(clock)
	}
	// Geo-database news is not a monitor: it happens on the source-check pass and
	// is reported once, when the check learns it. It pages the admin chat like
	// the other control-plane internals; each affected namespace is told about
	// its own rules separately (alertWithdrawn).
	p.geoAlerts = MultiAlerter{base, routedTelegram{p: p, aud: audAdmin}}
	// Each of these also has to stay broken for a while before it says so. The
	// sample counts below are cadence-dependent and were far too eager on their
	// own: two failed edge probes is thirty-five seconds, which is how a blip
	// nobody would have noticed became a critical page followed, twenty-five
	// seconds later, by an all-clear. What is worth waking someone for is an
	// outage that is still there a few minutes on.
	p.nodeMon = mon(audBroadcast, 2).RequireDownFor(3 * time.Minute)
	p.edgeMon = mon(audBroadcast, 2).RequireDownFor(3 * time.Minute)
	p.replMon = mon(audAdmin, 2).RequireDownFor(3 * time.Minute)
	p.botMon = mon(audAdmin, 1).RequireDownFor(5 * time.Minute) // a chat outage is not urgent
	p.certMon = mon(audAdmin, 1)                                // a date, not a flapping probe: warn on the first look
}

// replRecord is the most recent replication-slot health for an expected standby,
// read from the primary's pg_replication_slots. Missing=true means no physical
// slot exists for the standby at all (never provisioned / dropped). A down or
// far-behind replica is a control-plane (HA) concern only: the standby's VPN
// data plane keeps running. The UI surfaces it as a warning, not an error.
type replRecord struct {
	Active      bool
	BytesBehind *int64
	Missing     bool
	At          time.Time
}

// edgeRecord is the most recent external probe of an entry node's public :443,
// done from the panel (i.e. off-node) so a node that is up but unreachable from
// the outside still shows red.
type edgeRecord struct {
	Ok    bool
	Error string
	At    time.Time
}

// botHealthRecord is the most recent Telegram getMe result for a configured
// token. Status is one of: ok | unauthorized | unreachable | unconfigured.
type botHealthRecord struct {
	Status string
	Detail string
	At     time.Time
}

// reconcileRecord is the most recent reconcile outcome for one node, surfaced on
// the Overview so a silent rollback/error is visible without a manual reconcile.
type reconcileRecord struct {
	Outcome  string
	Changed  bool
	Error    string
	Warnings []render.Phrase
	At       time.Time
}

// SetRouteTester wires the geo evaluator used by POST /api/route-test.
func (p *Panel) SetRouteTester(geo render.GeoMatcher) { p.geo = geo }

// SetBillingAlerts enables payment-due alerts: nodes whose paid_until is within
// `days` (or overdue) trigger an alert on each RunBillingAlertLoop tick. The
// passed alerter records (log + event log) only; the Telegram leg is routed
// per node to the node owner + admin by CheckBillingDue (audience: audOwner).
func (p *Panel) SetBillingAlerts(a watchdog.Alerter, days int) {
	if days <= 0 {
		days = 7
	}
	p.billingAlerter = a
	p.billingDays = days
}

// nodeMetric is the latest live resource usage observed for a node.
type nodeMetric struct {
	System *agentapi.SystemMetrics
	At     time.Time
}

// New builds a panel. fleet may be nil if push/reconcile is not wired yet.
func New(st *store.Store, fleet *controller.Fleet, sessions *SessionManager) *Panel {
	return &Panel{
		store: st, fleet: fleet, sessions: sessions,
		logins:  newLoginThrottle(),
		nextRev: time.Now().UnixNano(), now: time.Now,
		jobs:          jobs.NewManager(),
		metrics:       map[string]nodeMetric{},
		lastReconcile: map[string]reconcileRecord{},
		lastEdge:      map[string]edgeRecord{},
		botHealth:     map[string]botHealthRecord{},
		lastRepl:      map[string]replRecord{},
		dirtyCh:       make(chan struct{}, 1),

		exitDegradedSince: map[string]time.Time{},
		exitFailedOver:    map[string]bool{},
		exitBlackholed:    map[string]bool{},
	}
}

// Handler returns the panel mux. The panel is intended to bind 127.0.0.1 only.
func (p *Panel) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", p.handleHealth)
	mux.HandleFunc("POST /api/auth/login", p.handleLogin)
	// Locked-out recovery: unauthenticated by necessity, guarded by the one-time
	// code the bot issues to the account's bound Telegram.
	mux.HandleFunc("POST /api/auth/recover", p.handleRecoverPassword)
	mux.HandleFunc("POST /api/auth/logout", p.handleLogout)
	mux.HandleFunc("GET /api/session", p.handleSession)

	// Reads are scoped per-account inside the handler (authz.ScopeState).
	mux.Handle("GET /api/state", p.protected(p.handleState))
	mux.Handle("GET /api/traffic", p.protected(p.handleTraffic))
	mux.Handle("GET /api/traffic/series", p.protected(p.handleTrafficSeries))
	mux.Handle("GET /api/nodes/series", p.protected(p.handleNodeTrafficSeries))
	mux.Handle("GET /api/nodes/resource-series", p.protected(p.handleNodeResourceSeries))
	mux.Handle("GET /api/overview", p.protected(p.handleOverview))
	mux.Handle("POST /api/route-test", p.protected(p.handleRouteTest))
	mux.Handle("POST /api/account/password", p.protected(p.handleChangePassword))
	mux.Handle("POST /api/account/telegram", p.protected(p.handleSetAccountTelegram))
	mux.Handle("POST /api/account/locale", p.protected(p.handleSetAccountLocale))
	// Bootstrap-only cross-namespace lens toggle (session-scoped, off by default).
	mux.Handle("POST /api/dev/cross-namespace-view", p.protected(p.handleDevView))

	// Infra-only: control plane, HA, backups, global settings, account
	// management. Operators get 403. (Node lifecycle and domains are ownable,
	// gated per-resource below, rather than infra-only.)
	mux.Handle("POST /api/admin", p.protected(p.infraOnly(p.handleSetAdmin)))
	mux.Handle("POST /api/reconcile", p.protected(p.infraOnly(p.handleReconcile)))
	mux.Handle("GET /api/settings", p.protected(p.infraOnly(p.handleGetSettings)))
	mux.Handle("POST /api/settings", p.protected(p.infraOnly(p.handleSaveSettings)))
	mux.Handle("POST /api/settings/test-channel", p.protected(p.infraOnly(p.handleTestChannel)))
	// Event log is scoped per-namespace inside the handler (each viewer sees only
	// its own namespace's events; the fleet owner sees the infra namespace), so an
	// operator gets a journal of its own actions, not infraOnly.
	mux.Handle("GET /api/events", p.protected(p.handleListEvents))
	mux.Handle("GET /api/events/operation", p.protected(p.handleEventOperation))
	// Account management is scoped inside each handler (fleet owner = all;
	// namespace admin = own namespace; operator = forbidden), not infraOnly, so a
	// namespace admin can manage its own members.
	mux.Handle("GET /api/admins", p.protected(p.handleListAdmins))
	mux.Handle("POST /api/admins", p.protected(p.handleCreateAdmin))
	mux.Handle("POST /api/admins/{username}/role", p.protected(p.handleSetRole))
	mux.Handle("DELETE /api/admins/{username}", p.protected(p.handleDeleteAdmin))
	mux.Handle("POST /api/nodes", p.protected(p.infraOnly(p.handleUpsertNode)))
	// Per-node lifecycle is ownable: an operator manages its own nodes (drain,
	// limits, billing, TLS-promote, decommission), read-only on others; admins
	// manage any. provision creates an owned node; jobs lets the operator follow it.
	mux.Handle("DELETE /api/nodes/{id}", p.protected(p.nodeOwner(p.handleDeleteNode)))
	mux.Handle("POST /api/nodes/{id}/limits", p.protected(p.nodeOwner(p.handleSetNodeLimits)))
	mux.Handle("POST /api/nodes/{id}/promote-tls", p.protected(p.nodeOwner(p.handlePromoteTLS)))
	mux.Handle("POST /api/nodes/{id}/billing", p.protected(p.nodeOwner(p.handleSetNodeBilling)))
	mux.Handle("POST /api/nodes/{id}/maintenance", p.protected(p.nodeOwner(p.handleSetNodeMaintenance)))
	mux.Handle("POST /api/nodes/{id}/reassign-egress", p.protected(p.infraOnly(p.handleReassignEgress)))
	mux.Handle("POST /api/nodes/provision", p.protected(p.infraOnly(p.handleProvision)))
	mux.Handle("POST /api/cluster/add-entry", p.protected(p.infraOnly(p.handleAddEntry)))
	mux.Handle("POST /api/cluster/add-standby", p.protected(p.infraOnly(p.handleAddStandby)))
	mux.Handle("GET /api/jobs/{id}", p.protected(p.handleJob))
	// Domains are ownable through their node: an operator manages domains/TLS on
	// its own nodes (gated inside the handlers), read-only on others; admins any.
	mux.Handle("POST /api/domains", p.protected(p.infraOnly(p.handleUpsertDomain)))
	mux.Handle("DELETE /api/domains/{id}", p.protected(p.infraOnly(p.handleDeleteDomain)))

	// Operator namespace: gated per-resource by owner_id inside each handler.
	mux.Handle("POST /api/groups", p.protected(p.handleUpsertGroup))
	mux.Handle("DELETE /api/groups/{id}", p.protected(p.handleDeleteGroup))
	mux.Handle("POST /api/users", p.protected(p.handleUpsertUser))
	mux.Handle("POST /api/users/bulk", p.protected(p.handleBulkUsers))
	mux.Handle("DELETE /api/users/{id}", p.protected(p.handleDeleteUser))
	mux.Handle("GET /api/users/{id}/client-config", p.protected(p.handleClientConfig))
	mux.Handle("POST /api/route-policies", p.protected(p.handleUpsertRoutePolicy))
	mux.Handle("DELETE /api/route-policies/{id}", p.protected(p.handleDeleteRoutePolicy))
	// The geo databases are infra, but the rule form offers their category
	// catalogue as suggestions. Reading is therefore open to anyone who can write a
	// rule, and only repointing or re-checking a source is infra-only.
	mux.Handle("GET /api/rulesets", p.protected(p.handleListRuleSets))
	mux.Handle("POST /api/rulesets/check", p.protected(p.infraOnly(p.handleCheckRuleSets)))
	mux.Handle("POST /api/rulesets/source", p.protected(p.infraOnly(p.handleSetRuleSetSource)))
	mux.Handle("POST /api/rulesets/schedule", p.protected(p.infraOnly(p.handleSetRuleSetSchedule)))
	mux.Handle("POST /api/rulesets/update", p.protected(p.infraOnly(p.handleUpdateAllRuleSets)))
	mux.Handle("POST /api/rulesets/{tag}/update", p.protected(p.infraOnly(p.handleUpdateRuleSet)))
	mux.Handle("POST /api/rulesets/{tag}/rollback", p.protected(p.infraOnly(p.handleRollbackRuleSet)))
	mux.Handle("POST /api/rulesets/{tag}/auto-update", p.protected(p.infraOnly(p.handleSetRuleSetAutoUpdate)))

	// Embedded operator web UI (served for any non-API GET path). API routes
	// above are more specific and take precedence.
	mux.Handle("GET /", http.FileServerFS(webui.Static()))
	return mux
}

// protected requires a valid session once an admin exists. While no admin
// exists the routes stay open so a fresh install can bootstrap.
func (p *Panel) protected(h http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n, err := p.store.AdminCount(r.Context())
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if n > 0 {
			c, err := r.Cookie(sessionCookie)
			if err != nil {
				writeErr(w, http.StatusUnauthorized, "authentication required")
				return
			}
			username, ok := p.sessions.Validate(c.Value)
			if !ok {
				writeErr(w, http.StatusUnauthorized, "invalid or expired session")
				return
			}
			// Re-validate that the session's account still exists. A deleted account
			// must lose access immediately regardless of session TTL; on a store
			// error we also reject (fail closed) rather than let account() fall
			// through, which makes the fail-closed account() unreachable for
			// protected routes (deleted/unresolved → 401 here).
			if _, err := p.store.AdminByUsername(r.Context(), username); err != nil {
				writeErr(w, http.StatusUnauthorized, "session no longer valid")
				return
			}
			// CSRF: every state-changing request must echo the per-session token in
			// the X-CSRF-Token header. The SameSite=Strict session cookie is the
			// first line of defense; this is defense in depth, since a cross-site POST
			// can carry neither the cookie nor a custom header. Safe methods (GET/HEAD/
			// OPTIONS) are exempt. The UI's api() wrapper attaches the token to every
			// non-GET request.
			if isUnsafeMethod(r.Method) && !p.sessions.ValidCSRF(c.Value, r.Header.Get("X-CSRF-Token")) {
				writeErr(w, http.StatusForbidden, "missing or invalid CSRF token")
				return
			}
		}
		h(w, r)
	})
}

// infraOnly rejects operators from infra-only surfaces (control plane, HA,
// backups, global settings, account management, node provisioning, domains).
// It runs inside protected(), where a session is already validated; the account
// resolver returns a synthetic admin during open setup (no accounts yet), so
// bootstrap is not locked out.
func (p *Panel) infraOnly(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !authz.CanManageInfra(p.account(r)) {
			writeErr(w, http.StatusForbidden, "this action is available to admins only")
			return
		}
		h(w, r)
	}
}

// nodeOwner gates a per-node ({id}) lifecycle endpoint (drain, limits, billing,
// TLS-promote, delete). Because nodes are shared infra, any admin may manage any
// node and an operator none (404 if the node is gone, 403 otherwise). Kept as a
// distinct wrapper from infraOnly for the 404-before-403 nicety. Runs inside
// protected().
func (p *Panel) nodeOwner(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		acct := p.account(r)
		if !acct.CanManageInfra() {
			st, err := p.store.LoadState(r.Context())
			if err != nil {
				writeErr(w, http.StatusInternalServerError, err.Error())
				return
			}
			n, ok := st.NodeByID(r.PathValue("id"))
			if !ok {
				writeErr(w, http.StatusNotFound, "node not found")
				return
			}
			if !authz.CanWriteNode(acct, n) {
				writeErr(w, http.StatusForbidden, "node management is available to admins only")
				return
			}
		}
		h(w, r)
	}
}

// isUnsafeMethod reports whether an HTTP method mutates state and therefore
// requires a CSRF token.
func isUnsafeMethod(m string) bool {
	switch m {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

// ---- auth ----

func (p *Panel) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req struct{ Username, Password string }
	if !decode(w, r, &req) {
		return
	}
	// Lockout: refuse while the username is in a throttle window (brute-force guard).
	if d := p.logins.retryAfter(req.Username); d > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(int(d.Seconds())+1))
		writeErr(w, http.StatusTooManyRequests, "too many failed login attempts; try again later")
		return
	}
	ok, err := verifyLogin(r.Context(), p.store, req.Username, req.Password)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		p.logins.fail(req.Username)
		writeErr(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	// The panel is an admin-only surface (SSH-tunnel-to-localhost = infra-trusted).
	// Operators have no panel; their sole surface is the role-aware bot (decision
	// 4). Credentials are valid here. This is not a brute-force signal; count it
	// as a success for lockout purposes, then refuse the session.
	if a, err := p.store.AdminByUsername(r.Context(), req.Username); err == nil && a.Role == model.RoleOperator {
		p.logins.success(req.Username)
		writeErr(w, http.StatusForbidden, "operators manage clients through the Telegram bot, not the panel")
		return
	}
	p.logins.success(req.Username)
	token, csrf, err := p.sessions.Create(req.Username)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: token, Path: "/",
		HttpOnly: true, SameSite: http.SameSiteStrictMode,
	})
	writeJSON(w, http.StatusOK, map[string]string{"username": req.Username, "csrf_token": csrf})
}

func (p *Panel) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		p.sessions.Revoke(c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true})
	writeJSON(w, http.StatusOK, map[string]string{"status": "logged-out"})
}

func (p *Panel) handleSession(w http.ResponseWriter, r *http.Request) {
	resp := map[string]any{"authenticated": false}
	if c, err := r.Cookie(sessionCookie); err == nil {
		if user, ok := p.sessions.Validate(c.Value); ok {
			// Role + namespace drive UI gating (operators don't render infra
			// surfaces; namespace admins render member management but not infra). It
			// is advisory only: the server enforces every gate regardless.
			acct, _ := p.store.AdminByUsername(r.Context(), user)
			acct.Username = user
			role := model.RoleAdmin
			if acct.Role.Valid() {
				role = acct.Role
			}
			resp = map[string]any{
				"authenticated": true,
				"username":      user,
				"role":          string(role),
				"namespace":     acct.Namespace(),
				// owns_infra now means "may manage shared infra" (any admin); the
				// bootstrap owner (see-all lens, account minting) is a distinct flag.
				"owns_infra":         acct.CanManageInfra(),
				"is_bootstrap":       acct.IsBootstrapOwner(),
				"can_manage_members": acct.IsBootstrapOwner(),
				"locale":             acct.Locale,
			}
			if acct.IsBootstrapOwner() {
				// Advertise the buried cross-namespace lens' current state so the UI can
				// render its topbar indicator without a second round-trip.
				resp["cross_namespace_view"] = p.sessions.ExpandView(c.Value)
			}
			if csrf, ok := p.sessions.CSRF(c.Value); ok {
				resp["csrf_token"] = csrf
			}
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleSetAdmin mints the very first account, during the window when the panel
// has none and protected() is still letting everything through. That is all it
// does: once an account exists it refuses, because its store call upserts by
// username with the table's defaults, which would let any admin mint a second
// bootstrap owner for themselves or overwrite somebody else's password. Later
// accounts are made through /api/admins, which asks who is asking; a password
// is changed through /api/account/password or the recovery code.
func (p *Panel) handleSetAdmin(w http.ResponseWriter, r *http.Request) {
	n, err := p.store.AdminCount(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if n > 0 {
		writeErr(w, http.StatusForbidden, "the panel already has an account; new ones are added in Accounts")
		return
	}
	var req struct{ Username, Password string }
	if !decode(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Username) == "" || len(req.Password) < 8 {
		writeErr(w, http.StatusBadRequest, "username required and password must be at least 8 chars")
		return
	}
	hash, err := HashPassword(req.Password)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := p.store.UpsertAdmin(r.Context(), req.Username, hash); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"username": req.Username})
}

// ---- state / reconcile ----

func (p *Panel) handleHealth(w http.ResponseWriter, r *http.Request) {
	resp := map[string]any{
		"status":          "ok",
		"time":            p.now().UTC(),
		"reconcile_count": atomic.LoadInt64(&p.reconcileCount),
	}
	if last := atomic.LoadInt64(&p.lastReconcileUnix); last > 0 {
		resp["last_reconcile_at"] = time.Unix(last, 0).UTC()
	}
	writeJSON(w, http.StatusOK, resp)
}

func (p *Panel) handleState(w http.ResponseWriter, r *http.Request) {
	st, err := p.store.LoadState(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	acct := p.account(r)
	seeAll := p.seesAllNamespaces(r, acct)
	view := authz.ScopeStateView(st, acct, seeAll)
	redactNodeSecrets(&view)
	writeJSON(w, http.StatusOK, stateResponse{
		State:     view,
		Aggregate: authz.OthersAggregateView(st, acct, seeAll),
	})
}

// redactNodeSecrets blanks the exit Reality private key from a state view before
// it leaves the panel API. PrivKey is needed only exit-side (rendered into
// the node's own sing-box config server-side); returning it in /api/state widened
// its blast radius to every admin's browser memory/logs. The node edit form does
// not need it to round-trip, since preserveDialInKeys merges the stored key back
// on save. The DialIn is cloned so the underlying (possibly shared) node is untouched.
func redactNodeSecrets(st *model.State) {
	for i := range st.Nodes {
		if st.Nodes[i].DialIn != nil && st.Nodes[i].DialIn.PrivKey != "" {
			clone := *st.Nodes[i].DialIn
			clone.PrivKey = ""
			st.Nodes[i].DialIn = &clone
		}
	}
}

// handleDevView flips the bootstrap owner's session-scoped cross-namespace lens.
// It is the panel's buried "see every namespace" switch: off by default, reset
// on re-login. Non-bootstrap accounts get 403 (they can never see all).
func (p *Panel) handleDevView(w http.ResponseWriter, r *http.Request) {
	acct := p.account(r)
	if !acct.IsBootstrapOwner() {
		writeErr(w, http.StatusForbidden, "cross-scope view is available to the bootstrap owner only")
		return
	}
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if !decode(w, r, &req) {
		return
	}
	_, token, ok := p.currentUser(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "no session")
		return
	}
	if !p.sessions.SetExpandView(token, req.Enabled) {
		writeErr(w, http.StatusUnauthorized, "session expired")
		return
	}
	if req.Enabled {
		actor, _, _ := p.currentUser(r)
		p.recordEvent(r.Context(), model.EventAdmin, model.SeverityWarn,
			journal.Line("cross-scope view enabled"), actor, acct.Namespace())
	}
	writeJSON(w, http.StatusOK, map[string]bool{"enabled": req.Enabled})
}

// stateResponse wraps the (scoped) state with the non-identifying aggregate of
// the clients the caller may not see. For an admin the aggregate is empty.
type stateResponse struct {
	model.State
	Aggregate authz.Aggregate `json:"aggregate"`
}

type reconcileNodeResult struct {
	Outcome  string          `json:"outcome"`
	Changed  bool            `json:"changed"`
	Error    string          `json:"error,omitempty"`
	Warnings []render.Phrase `json:"warnings,omitempty"`
}

func (p *Panel) handleReconcile(w http.ResponseWriter, r *http.Request) {
	rev, out, err := p.ReconcileOnce(r.Context())
	if err != nil {
		if err == errNoFleet {
			writeErr(w, http.StatusServiceUnavailable, err.Error())
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	resp := make(map[string]reconcileNodeResult, len(out))
	for id, o := range out {
		nr := reconcileNodeResult{Outcome: string(o.Result.Outcome), Changed: o.Result.Changed, Warnings: o.Warnings}
		if o.Err != nil {
			nr.Error = o.Err.Error()
		} else if o.Result.Error != "" {
			nr.Error = o.Result.Error
		}
		resp[id] = nr
	}
	writeJSON(w, http.StatusOK, map[string]any{"revision": rev, "nodes": resp})
}

var errNoFleet = fmt.Errorf("pushing to nodes is not configured")

// ReconcileOnce loads the current state and pushes desired-state to every node.
// Shared by POST /api/reconcile and the background reconcile loop.
func (p *Panel) ReconcileOnce(ctx context.Context) (int64, map[string]controller.NodeOutcome, error) {
	if p.fleet == nil {
		return 0, nil, errNoFleet
	}
	// Single-flight: a manual POST /api/reconcile and the background loop must not
	// push concurrently, since two overlapping pushes can land out of revision order
	// on an agent and record a spurious non-monotonic "rejected" outcome.
	p.reconcileRunMu.Lock()
	defer p.reconcileRunMu.Unlock()
	st, err := p.store.LoadState(ctx)
	if err != nil {
		return 0, nil, err
	}
	// Every category the rules name must be on disk before the push needs it: a
	// rule written since the last source check (here or from the bot) would
	// otherwise send the reconcile to the network at the one moment a node is
	// waiting for its configuration, and a source that is briefly unreachable
	// then holds up everything else in that push.
	p.ensureRuleSetPins(ctx, st)
	rev := atomic.AddInt64(&p.nextRev, 1)
	out := p.fleet.Reconcile(ctx, st, rev, p.now())
	atomic.AddInt64(&p.reconcileCount, 1)
	atomic.StoreInt64(&p.lastReconcileUnix, p.now().Unix())
	p.reconcileMu.Lock()
	for id, o := range out {
		rec := reconcileRecord{Outcome: string(o.Result.Outcome), Changed: o.Result.Changed, Warnings: o.Warnings, At: p.now()}
		if o.Err != nil {
			rec.Error = o.Err.Error()
		} else if o.Result.Error != "" {
			rec.Error = o.Result.Error
		}
		prev, had := p.lastReconcile[id]
		p.lastReconcile[id] = rec
		// Surface render compile-time warnings about the pushed desired state (e.g.
		// an exclusion skipped, a drained exit falling back to local egress). Without
		// this they are invisible unless an operator runs the route-tester on the
		// exact target. Log on transition (new warnings) only.
		if len(rec.Warnings) > 0 && (!had || !render.SamePhrases(prev.Warnings, rec.Warnings)) {
			for _, wmsg := range rec.Warnings {
				log.Printf("reconcile: node %s warning: %s", id, wmsg)
			}
		}
		// Surface per-node reconcile trouble (stale-leader/rejected/rolled-back, e.g.
		// the epoch fence rejecting a stale controller after a re-home) in the
		// log, not just lastReconcile (the UI), so it's visible in journalctl. Log
		// on transition (when it turns bad, changes which bad, or recovers) only,
		// without spamming the steady state.
		now, before := reconcileTrouble(rec), ""
		if had {
			before = reconcileTrouble(prev)
		}
		switch {
		case now != "" && (!had || prev.Outcome != rec.Outcome || prev.Error != rec.Error):
			log.Printf("reconcile: node %s %s", id, now)
		case now == "" && before != "":
			log.Printf("reconcile: node %s recovered (%s)", id, rec.Outcome)
		}
	}
	p.reconcileMu.Unlock()
	return rev, out, nil
}

// equalStrs reports whether two string slices are identical (order-sensitive).
// Used to log render warnings only when the set changes, not every reconcile.
func equalStrs(a, b []string) bool {
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

// reconcileTrouble returns a short description when a node's reconcile outcome is
// not a clean apply (stale-leader, rejected, rolled-back, or a transport/agent
// error), else "". Used to log per-node problems on transition.
func reconcileTrouble(r reconcileRecord) string {
	switch agentapi.Outcome(r.Outcome) {
	case agentapi.OutcomeStaleLeader:
		msg := "stale-leader (epoch fence): agent rejected the push; re-provision or reset agent state if this node was re-homed"
		if r.Error != "" {
			msg += ": " + r.Error
		}
		return msg
	case agentapi.OutcomeRejected:
		return "rejected: " + r.Error
	case agentapi.OutcomeRolledBack:
		return "rolled-back (config restored): " + r.Error
	}
	if r.Error != "" {
		return "error: " + r.Error
	}
	return ""
}

// liveInterval reads the live-configured cadence for a background loop from
// settings, falling back to the static value supplied at startup (the serve flag,
// or a test value) when the setting is unset (0) or settings are unavailable.
// This makes the loop cadences editable from the Settings tab without a
// restart: the cadence is re-read at the top of every cycle.
func (p *Panel) liveInterval(get func(model.PanelSettings) time.Duration, set func(model.PanelSettings) int, fallback time.Duration) time.Duration {
	if s, err := p.store.GetSettings(context.Background()); err == nil && set(s.Panel) > 0 {
		return get(s.Panel)
	}
	return fallback
}

// dynLoop runs tick at a cadence re-read each cycle from next(). It returns when
// ctx is cancelled. A zero next() value stops the loop (disabled).
func dynLoop(ctx context.Context, next func() time.Duration, tick func()) {
	for {
		d := next()
		if d <= 0 {
			return
		}
		timer := time.NewTimer(d)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			tick()
		}
	}
}

// markDirty requests a coalesced background reconcile after a write. An edit
// converges to the fleet within a couple of seconds without anyone pressing
// "Sync now", and without N edits triggering N pushes. Non-blocking: if a sync
// is already pending the extra signal is dropped (the pending run will cover it).
// A no-op when push/reconcile is not wired (fleet == nil) or auto-sync is off.
func (p *Panel) markDirty() {
	if p.fleet == nil || p.dirtyCh == nil {
		return
	}
	select {
	case p.dirtyCh <- struct{}{}:
	default:
	}
}

// RunAutoSyncLoop debounces markDirty signals into coalesced reconciles: on the
// first dirty signal it waits a fixed window (collapsing a burst of edits, and a
// bulk write's single mutation, into one push), draining further signals, then
// reconciles once. A failed auto-sync is logged and recorded as an alert event
// so a push that silently rolls back (node draining/unreachable) is visible in
// the Logs tab rather than disappearing. debounce <= 0 disables auto-sync (the
// periodic RunReconcileLoop still converges the fleet).
func (p *Panel) RunAutoSyncLoop(ctx context.Context, debounce time.Duration) {
	if p.fleet == nil || debounce <= 0 {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-p.dirtyCh:
		}
		// Coalesce a burst into one push: wait a fixed window from the first
		// dirty signal, draining any that arrive while we wait.
		timer := time.NewTimer(debounce)
	coalesce:
		for {
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-p.dirtyCh:
				// keep waiting; the fixed window bounds write latency
			case <-timer.C:
				break coalesce
			}
		}
		if _, _, err := p.ReconcileOnce(ctx); err != nil {
			log.Printf("auto-sync: %v", err)
			p.recordEvent(ctx, model.EventAlert, model.SeverityWarn,
				journal.Line("auto-sync after a change failed: %s", err.Error()), "system", "")
		}
	}
}

// RunReconcileLoop periodically reconciles the fleet until ctx is cancelled, so
// nodes converge (and a recovered node is re-pushed) without operator action.
// interval is the fallback cadence; the live value comes from settings.
func (p *Panel) RunReconcileLoop(ctx context.Context, interval time.Duration) {
	if interval <= 0 || p.fleet == nil {
		return
	}
	dynLoop(ctx, func() time.Duration {
		return p.liveInterval(model.PanelSettings.ReconcileInterval, func(ps model.PanelSettings) int { return ps.ReconcileSeconds }, interval)
	}, func() {
		if _, _, err := p.ReconcileOnce(ctx); err != nil {
			log.Printf("reconcile loop: %v", err)
		}
	})
}

// ---- traffic stats ----

// PollStatsOnce fetches per-user traffic from every entry node's agent and
// accumulates the deltas into the store. Shared by GET-driven refresh and the
// background stats loop. Per-node failures are logged and skipped so one
// unreachable node does not block the rest.
func (p *Panel) PollStatsOnce(ctx context.Context) error {
	if p.fleet == nil {
		return errNoFleet
	}
	st, err := p.store.LoadState(ctx)
	if err != nil {
		return err
	}
	userByName := make(map[string]string, len(st.Users))
	for _, u := range st.Users {
		userByName[u.Username] = u.ID
	}
	liveNodes := make(map[string]bool, len(st.Nodes))
	nodeHealth := make(map[string]model.NodeHealth, len(st.Nodes))
	for _, n := range st.Nodes {
		liveNodes[n.ID] = true
		status, err := p.fleet.Status(ctx, n)
		if err != nil {
			log.Printf("stats: node %s status: %v", n.ID, err)
			nodeHealth[n.ID] = model.HealthDegraded
			if e := p.store.SetNodeHealth(ctx, n.ID, model.HealthDegraded, nil); e != nil {
				log.Printf("stats: health %s: %v", n.ID, e)
			}
			p.observeNode(ctx, n, false, err, st)
			continue
		}
		// Agent answered, but that only proves the agent process is up; it says
		// nothing about the data-plane services under it. Cross-check the
		// per-unit state the agent already reports so a dead sing-box/trusttunnel
		// under a live agent shows degraded instead of a false-green dot.
		health := model.HealthHealthy
		if down := deadRequiredServices(status.Services, n.PublicRole); len(down) > 0 {
			health = model.HealthDegraded
			log.Printf("stats: node %s degraded: service(s) not active: %s", n.ID, strings.Join(down, ", "))
		}
		nodeHealth[n.ID] = health
		now := p.now()
		if e := p.store.SetNodeHealth(ctx, n.ID, health, &now); e != nil {
			log.Printf("stats: health %s: %v", n.ID, e)
		}
		p.observeNode(ctx, n, true, nil, st)
		// Keep the reported management-certificate dates for the slow expiry
		// check, which runs on its own cadence rather than on every poll.
		p.certMu.Lock()
		if p.certSeen == nil {
			p.certSeen = map[string]*agentapi.FleetCertStatus{}
		}
		p.certSeen[n.ID] = status.FleetCert
		p.certMu.Unlock()
		// Live resource metrics + monthly node traffic, for every node.
		if status.System != nil {
			p.metricsMu.Lock()
			p.metrics[n.ID] = nodeMetric{System: status.System, At: p.now()}
			p.metricsMu.Unlock()
			if status.System.NetRxBytes > 0 || status.System.NetTxBytes > 0 {
				if err := p.store.AccumulateNodeTraffic(ctx, n.ID, status.System.NetRxBytes, status.System.NetTxBytes, p.now()); err != nil {
					log.Printf("stats: node-traffic %s: %v", n.ID, err)
				}
			}
			// CPU/memory/disk history sample, for the Nodes tab's CPU/Memory views.
			sm := status.System
			if err := p.store.RecordNodeResourceSample(ctx, n.ID, sm.Load1, sm.CPUCores, sm.MemUsedMB, sm.MemTotalMB, sm.DiskUsedGB, sm.DiskTotalGB); err != nil {
				log.Printf("stats: node-resource-sample %s: %v", n.ID, err)
			}
		}
		if !n.IsEntry() {
			continue
		}
		// Per-user traffic + ACME cert status, entry nodes only.
		for _, ut := range status.UserTraffic {
			uid, ok := userByName[ut.Username]
			if !ok {
				continue // unknown/stale username; skip
			}
			// rx = uplink (client→server), tx = downlink (server→client).
			if err := p.store.AccumulateTraffic(ctx, uid, n.ID, ut.UplinkBytes, ut.DownlinkBytes); err != nil {
				log.Printf("stats: accumulate %s@%s: %v", ut.Username, n.ID, err)
			}
		}
		if status.TLSCert != nil {
			if err := p.store.SetDomainTLSStatusForNode(ctx, n.ID, certStatusLabel(status.TLSCert, p.now()), status.TLSCert.Issuer); err != nil {
				log.Printf("stats: tls_status %s: %v", n.ID, err)
			}
		}
	}
	p.forgetAbsentNodes(liveNodes)
	// A sustained exit outage auto-reassigns its groups to a healthy exit.
	p.reviewEgressFailover(ctx, st, nodeHealth)
	// Bound the time-series table.
	if err := p.store.PruneTrafficSamples(ctx, p.now().Add(-trafficSampleRetention)); err != nil {
		log.Printf("stats: prune traffic samples: %v", err)
	}
	return nil
}

// deadRequiredServices returns the names of any data-plane systemd units the
// agent reports as not "active" that this node's role actually needs to be
// running, so a dead sing-box/trusttunnel under a live agent process shows as
// degraded instead of a false-green health dot. ServiceSingBox runs on every
// role; ServiceTrustTunnel is only installed on entries (see provisionUnits)
// so it's ignored elsewhere.
func deadRequiredServices(services []agentapi.ServiceStatus, role model.PublicRole) []string {
	var down []string
	for _, s := range services {
		switch s.Name {
		case controller.ServiceSingBox:
		case controller.ServiceTrustTunnel:
			if role != model.RoleEntry {
				continue
			}
		default:
			continue
		}
		if s.State != "active" {
			down = append(down, s.Name)
		}
	}
	return down
}

const (
	// activityWindow: a user counts as "active now" if they moved enough bytes
	// (see PanelSettings.ActivityFloorBytes) within it. It spans a couple of stats
	// ticks so a single idle poll doesn't make an actively-downloading client flap
	// to inactive, but is kept short so the online view tracks reality closely.
	activityWindow = 2 * time.Minute
	// trafficSampleRetention bounds the traffic_samples table. Kept at ~a month so
	// the per-user/-node graph can offer week and month ranges.
	trafficSampleRetention = 31 * 24 * time.Hour
)

// forgetAbsentNodes drops liveness state for nodes no longer registered, called
// once per stats poll. The keys must carry the same "node:" prefix observeNode
// keys with: passing bare ids makes Forget treat every live node as absent and
// wipe its fail counter each poll. The threshold is then never reached and the
// node-down alert never fires. Kept as a seam so a regression here is caught
// by TestForgetPreservesDownCounter rather than only in production.
func (p *Panel) forgetAbsentNodes(liveNodes map[string]bool) {
	if p.nodeMon != nil {
		p.nodeMon.Forget(prefixKeys("node:", liveNodes))
	}
	if p.certMon != nil {
		p.certMon.Forget(prefixKeys("cert:", liveNodes))
	}
	p.certMu.Lock()
	for id := range p.certSeen {
		if !liveNodes[id] {
			delete(p.certSeen, id)
		}
	}
	p.certMu.Unlock()
}

// observeNode feeds a node's agent-reachability sample to the node-liveness
// monitor. Because every node carries user traffic (entry or exit), a node going down
// is Critical (service impact); recovery is always silent. The active
// control-plane node cannot observe its own death here (that is the standby
// watchdog's (β) job), but it observes every other node, so a dead
// standby/entry/exit is reported even while the data plane keeps serving.
func (p *Panel) observeNode(ctx context.Context, n model.Node, healthy bool, probeErr error, st model.State) {
	if p.nodeMon == nil {
		return
	}
	down := watchdog.MsgNodeDown(n.Name, string(n.PublicRole), fmt.Sprint(probeErr))
	// If the dead node is a live exit, its groups' traffic is blackholed until
	// egress failover (or an operator) reassigns them. Say so in the broadcast,
	// but never name the groups here: this leg fans out to every account's alert
	// chat, and group names are namespace-private: naming them would leak one
	// operator's group names to every other namespace. The per-namespace failover
	// alert (reviewEgressFailover) names each owner's own groups to them only.
	if n.IsExit() {
		if deps := egressDependents(st, n.ID); len(deps) > 0 {
			down = watchdog.MsgExitDown(n.Name, fmt.Sprint(probeErr))
		}
	}
	up := watchdog.MsgNodeUp(n.Name, string(n.PublicRole))
	if n.IsExit() && len(egressDependents(st, n.ID)) > 0 {
		// It was carrying groups when it died. Its recovery has to say what
		// did not come back with it: nothing moves them, and a plain "it is back"
		// reads as if the whole thing were over.
		up = watchdog.MsgExitUp(n.Name)
	}
	p.nodeMon.Observe(ctx, "node:"+n.ID, healthy, watchdog.SeverityCritical, down, up)
}

// groupName is a group's display name, for a message that has to place a client
// for whoever reads it.
func groupName(st model.State, id string) string {
	for _, g := range st.Groups {
		if g.ID == id {
			return g.Name
		}
	}
	return ""
}

// egressDependents returns display labels for the groups that depend on nodeID as
// their egress, either directly (group.DefaultExitID) or via an enabled exit-tier
// route policy (ExitNodeID). Empty means the node carries no live egress, so its
// death is HA-only. Used to escalate/annotate a dead exit's alerts.
func egressDependents(st model.State, nodeID string) []string {
	nameByGroup := make(map[string]string, len(st.Groups))
	for _, g := range st.Groups {
		nameByGroup[g.ID] = g.Name
	}
	seen := map[string]bool{}
	var out []string
	add := func(label string) {
		if label != "" && !seen[label] {
			seen[label] = true
			out = append(out, label)
		}
	}
	groupLabel := func(id string) string {
		if n := nameByGroup[id]; n != "" {
			return n
		}
		return id
	}
	for _, g := range st.Groups {
		if g.DefaultExitID == nodeID {
			add(groupLabel(g.ID))
		}
	}
	// A rule leans on an exit three ways: where its own matched traffic goes,
	// where the same traffic goes for everyone the rule is not reserved for, and
	// where it falls back when its first choice is unavailable. All three have to be counted: an exit carrying only reserves
	// or fallbacks would otherwise read as carrying nothing, its death downgraded
	// to an HA notice and the groups behind it left out of the list an operator
	// reads to decide what to do. Which rules those are is egress.RulesNaming; whose
	// groups each way affects is decided here.
	for _, pol := range egress.RulesNaming(st, nodeID) {
		if pol.Action == model.ActionExit && pol.ExitNodeID == nodeID {
			if pol.AppliesToGroupID != "" {
				add(groupLabel(pol.AppliesToGroupID))
			} else {
				add("all groups")
			}
		}
		if pol.OthersAction == model.ActionExit && pol.OthersExitID == nodeID {
			// The reservation answers for its own group; OthersExitID is where the
			// same destinations go for everybody else.
			add("all groups")
		}
		if pol.FallbackKind == model.FallbackExit && pol.FallbackExitID == nodeID {
			if pol.AppliesToGroupID != "" {
				add(groupLabel(pol.AppliesToGroupID))
			} else {
				add("all groups")
			}
		}
	}
	return out
}

// failoverOutcome records what one exit's auto-failover attempt did, letting the caller
// set the right in-memory dedup flag.
type failoverOutcome int

const (
	foMoved    failoverOutcome = iota // groups were reassigned to a healthy exit
	foNoGroups                        // the exit carried no default egress, nothing to move
	foNoTarget                        // groups depend on it but no healthy exit exists to move to
	foError                           // a store error aborted the attempt (retried next poll)
)

// reviewEgressFailover watches exits for a sustained outage and, once one has
// been degraded/unreachable longer than the configured debounce, auto-reassigns
// the groups that egress through it onto a healthy exit so their traffic stops
// being blackholed. It runs at the end of every stats poll off the health map
// that poll just computed. Recovery is deliberately not auto-reverted: a
// recovered exit stays drained and its former groups keep running on the target,
// so this only ever fires forward and the operator decides whether to move them
// back. The active control plane never sees its own node as degraded here; it
// cannot fail itself over.
func (p *Panel) reviewEgressFailover(ctx context.Context, st model.State, nodeHealth map[string]model.NodeHealth) {
	debounce := p.egressFailoverDebounce(ctx)
	now := p.now()

	// Phase 1: advance streaks under the lock and collect the exits now due.
	p.egressMu.Lock()
	var due []string
	for _, n := range st.Nodes {
		if !n.IsExit() {
			continue
		}
		if nodeHealth[n.ID] != model.HealthDegraded {
			// Healthy again (or not observed this poll): clear the outage so a future
			// one starts a fresh streak. Moved groups are left where they are.
			delete(p.exitDegradedSince, n.ID)
			delete(p.exitFailedOver, n.ID)
			delete(p.exitBlackholed, n.ID)
			continue
		}
		if p.exitDegradedSince[n.ID].IsZero() {
			p.exitDegradedSince[n.ID] = now // outage starts now
			continue
		}
		if p.exitFailedOver[n.ID] {
			continue // already moved this outage
		}
		if now.Sub(p.exitDegradedSince[n.ID]) < debounce {
			continue // still within the debounce window
		}
		due = append(due, n.ID)
	}
	p.egressMu.Unlock()

	// Phase 2: act outside the lock (DB writes + Telegram fan-out).
	for _, id := range due {
		dead, ok := st.NodeByID(id)
		if !ok {
			continue
		}
		p.egressMu.Lock()
		suppressBlackhole := p.exitBlackholed[id]
		p.egressMu.Unlock()

		outcome := p.doEgressFailover(ctx, dead, nodeHealth, suppressBlackhole)

		p.egressMu.Lock()
		switch outcome {
		case foMoved, foNoGroups:
			p.exitFailedOver[id] = true // done for this outage; stop re-checking
		case foNoTarget:
			p.exitBlackholed[id] = true // alerted once, but keep retrying so a later-recovering exit gets used
		}
		p.egressMu.Unlock()
	}
}

// egressFailoverDebounce reads the live failover debounce from settings, falling
// back to the documented default if settings can't be loaded.
func (p *Panel) egressFailoverDebounce(ctx context.Context) time.Duration {
	s, err := p.store.GetSettings(ctx)
	if err != nil {
		return model.PanelSettings{}.EgressFailover()
	}
	return s.Panel.EgressFailover()
}

// doEgressFailover reassigns the groups that egress via dead onto the best
// healthy exit and drains dead. It alerts each affected namespace about its own
// groups only (owner + admin), never broadcasting group names cross-namespace.
// suppressBlackholeAlert avoids re-sending the "no healthy exit" alert on repeat
// attempts within one outage.
func (p *Panel) doEgressFailover(ctx context.Context, dead model.Node, nodeHealth map[string]model.NodeHealth, suppressBlackholeAlert bool) failoverOutcome {
	st, err := p.store.LoadState(ctx)
	if err != nil {
		log.Printf("egress failover: load state: %v", err)
		return foError
	}
	opID := newOpID() // this attempt, and every line it writes anywhere
	// Groups that currently egress through the dead exit, bucketed by namespace.
	buckets := map[string][]string{} // owner id -> group display labels
	var groupIDs []string
	for _, g := range st.Groups {
		if g.DefaultExitID != dead.ID {
			continue
		}
		groupIDs = append(groupIDs, g.ID)
		label := g.Name
		if label == "" {
			label = g.ID
		}
		buckets[g.OwnerID] = append(buckets[g.OwnerID], label)
	}
	for _, names := range buckets {
		sort.Strings(names)
	}
	// Rules that name this exit fail over by their own declaration rather than
	// by being reassigned (see policyExit in the renderer), but only once the
	// exit is out of rotation. Count them so an exit carrying rules and no
	// group's default still gets drained instead of being left to blackhole.
	// A rule that names it as nothing but a fallback counts too: it carries no
	// traffic today, and leaving the node in rotation would hand it everything
	// the moment its own exit goes.
	naming := len(st.PoliciesNamingExit(dead.ID))
	if len(groupIDs) == 0 && naming == 0 {
		return foNoGroups
	}
	if len(groupIDs) == 0 {
		if err := p.store.SetNodeMaintenance(ctx, dead.ID, true); err != nil {
			log.Printf("egress failover: drain %s: %v", dead.ID, err)
			return foError
		}
		p.recordOpEvent(ctx, model.EventAdmin, model.SeverityWarn,
			journal.Line("egress failover: exit %s down, drained — %s name it and route on without it", dead.Name, journal.Rules(naming)), "system", "", opID)
		p.markDirty()
		return foMoved
	}

	target := p.pickFailoverTarget(st, dead.ID, nodeHealth)
	if target == nil {
		// There is nowhere to move them to, but leaving the exit in rotation is
		// the worse answer: the entry would keep handing traffic to a machine
		// that is gone. Out of rotation, a group's traffic leaves from the entry
		// node and a rule that answered "if that exit is down" gets its answer.
		if err := p.store.SetNodeMaintenance(ctx, dead.ID, true); err != nil {
			log.Printf("egress failover: drain %s: %v", dead.ID, err)
		} else {
			p.markDirty()
		}
		if !suppressBlackholeAlert {
			for owner, names := range buckets {
				p.sendOwnerAlert(ctx, owner, watchdog.SeverityCritical, watchdog.MsgEgressBlackholed(dead.Name, strings.Join(names, ", ")))
			}
			p.recordOpEvent(ctx, model.EventAdmin, model.SeverityCrit,
				journal.Line("egress failover: exit %s down, no healthy exit for %s — drained, their traffic leaves from the entry node", dead.Name, journal.Groups(len(groupIDs))), "system", "", opID)
		}
		return foNoTarget
	}

	moves, err := p.switchEgress(ctx, groupIDs, target.ID)
	p.logEgressMoves(ctx, moves, model.SeverityWarn, journal.Line("egress failover: %s stopped answering", exitLabel(st, dead.ID)), "system", opID)
	if err != nil {
		log.Printf("egress failover: switch %d group(s) %s->%s: %v", len(groupIDs), dead.ID, target.ID, err)
		return foError
	}
	moved := len(moves)
	// Drain the dead exit so it stays out of rotation. Best-effort: the move is the
	// point, and a drain failure must not lose it.
	if err := p.store.SetNodeMaintenance(ctx, dead.ID, true); err != nil {
		log.Printf("egress failover: drain %s: %v", dead.ID, err)
	}
	msg := journal.Line("egress failover: auto-moved %s from %s to %s", journal.Groups(moved), dead.Name, target.Name)
	if naming > 0 {
		msg = journal.Join(msg, journal.Line("; %s name it and route on without it", journal.Rules(naming)))
	}
	p.recordOpEvent(ctx, model.EventAdmin, model.SeverityWarn, msg, "system", "", opID)
	p.markDirty() // new egress → re-render entries
	for owner, names := range buckets {
		p.sendOwnerAlert(ctx, owner, watchdog.SeverityCritical, watchdog.MsgEgressFailover(dead.Name, target.Name, strings.Join(names, ", ")))
	}
	return foMoved
}

// pickFailoverTarget chooses the healthy exit to receive a dead exit's groups:
// an in-rotation exit that this poll saw healthy, preferring the one already
// carrying the most groups (consolidate rather than scatter), then lowest id for
// a stable tie-break. Returns nil when no healthy exit is available.
func (p *Panel) pickFailoverTarget(st model.State, deadID string, nodeHealth map[string]model.NodeHealth) *model.Node {
	groupCount := map[string]int{}
	for _, g := range st.Groups {
		if g.DefaultExitID != "" && g.DefaultExitID != model.ExitDirect {
			groupCount[g.DefaultExitID]++
		}
	}
	var best *model.Node
	for i := range st.Nodes {
		n := st.Nodes[i]
		if n.ID == deadID || !n.IsExit() || n.Maintenance {
			continue
		}
		if nodeHealth[n.ID] != model.HealthHealthy {
			continue
		}
		if best == nil {
			best = &st.Nodes[i]
			continue
		}
		bc, nc := groupCount[best.ID], groupCount[n.ID]
		if nc > bc || (nc == bc && n.ID < best.ID) {
			best = &st.Nodes[i]
		}
	}
	return best
}

// RunBillingAlertLoop periodically alerts on nodes whose VPS payment is due
// within billingDays (or overdue). interval <= 0 or no alerter disables it.
func (p *Panel) RunBillingAlertLoop(ctx context.Context, interval time.Duration) {
	if interval <= 0 || p.billingAlerter == nil {
		return
	}
	p.CheckBillingDue(ctx)
	dynLoop(ctx, func() time.Duration {
		return p.liveInterval(model.PanelSettings.BillingInterval, func(ps model.PanelSettings) int { return ps.BillingAlertHours }, interval)
	}, func() { p.CheckBillingDue(ctx) })
}

// CheckBillingDue alerts once for every node due within billingDays / overdue.
func (p *Panel) CheckBillingDue(ctx context.Context) {
	if p.billingAlerter == nil {
		return
	}
	st, err := p.store.LoadState(ctx)
	if err != nil {
		log.Printf("billing alert: load state: %v", err)
		return
	}
	now := p.now()
	warnDays := p.billingDays
	if s, err := p.store.GetSettings(ctx); err == nil && s.Panel.BillingAlertDays > 0 {
		warnDays = s.Panel.BillingDays()
	}
	for _, n := range st.Nodes {
		if n.Billing == nil || n.Billing.PaidUntil == nil {
			continue
		}
		days := int(n.Billing.PaidUntil.Sub(now).Hours() / 24)
		if days > warnDays {
			continue
		}
		until := n.Billing.PaidUntil.UTC().Format("2006-01-02")
		msg := watchdog.MsgBilling(n.Name, days, until)
		recordAlert(ctx, p.billingAlerter, watchdog.SeverityLow, msg) // log + event log
		p.sendOwnerAlert(ctx, n.OwnerID, watchdog.SeverityLow, msg)   // Telegram: node owner + admin (their language)
	}
}

// SetExpiryAlerts enables config-expiry alerts: a client whose ExpiresAt falls
// within `days` (or is already expired) triggers one alert to its owner (+admin)
// per expiry date. The alerter records (log + event log) only; the Telegram leg
// is routed to the client's owner by CheckExpiringConfigs (audience: audOwner).
func (p *Panel) SetExpiryAlerts(a watchdog.Alerter, days int) {
	if days <= 0 {
		days = 3
	}
	p.expiryRecorder = a
	p.expiryDays = days
}

// RunExpiryAlertLoop periodically warns owners about client configs expiring
// within the window. interval <= 0 or no recorder disables it. It shares the
// billing cadence (both are slow due-date housekeeping checks).
func (p *Panel) RunExpiryAlertLoop(ctx context.Context, interval time.Duration) {
	if interval <= 0 || p.expiryRecorder == nil {
		return
	}
	p.CheckExpiringConfigs(ctx)
	dynLoop(ctx, func() time.Duration {
		return p.liveInterval(model.PanelSettings.BillingInterval, func(ps model.PanelSettings) int { return ps.BillingAlertHours }, interval)
	}, func() { p.CheckExpiringConfigs(ctx) })
}

// CheckExpiringConfigs alerts an owner once for every client config expiring
// within the warning window (or already expired). De-dup is per expiry date:
// MarkExpiryAlerted stores the warned-for ExpiresAt. An unchanged date is not
// re-warned and extending a config re-arms the warning for the new date.
func (p *Panel) CheckExpiringConfigs(ctx context.Context) {
	if p.expiryRecorder == nil {
		return
	}
	st, err := p.store.LoadState(ctx)
	if err != nil {
		log.Printf("expiry alert: load state: %v", err)
		return
	}
	now := p.now()
	warnDays := p.expiryDays
	if s, err := p.store.GetSettings(ctx); err == nil && s.Panel.ExpiryAlertDays > 0 {
		warnDays = s.Panel.ExpiryDays()
	}
	for _, u := range st.Users {
		if u.ExpiresAt == nil {
			continue // no expiry, nothing to warn about
		}
		// A client switched off by hand is not news. One switched off because its
		// access ran out is exactly the news the owner is waiting for, and the
		// sweep must not swallow the warning by getting there first.
		if !u.Enabled && !u.Expired(now) {
			continue
		}
		// Already warned for this exact expiry date? skip (re-arms when it changes).
		if u.ExpiryAlertedFor != nil && u.ExpiryAlertedFor.Equal(*u.ExpiresAt) {
			continue
		}
		days := int(u.ExpiresAt.Sub(now).Hours() / 24)
		if days > warnDays {
			continue // not in the warning window yet
		}
		until := u.ExpiresAt.UTC().Format("2006-01-02")
		// The name the owner knows the client by, and the group, which is how they
		// place it without opening anything.
		who := u.DisplayName
		if who == "" {
			who = u.Username
		}
		msg := watchdog.MsgConfigExpiry(who, groupName(st, u.GroupID), !now.Before(*u.ExpiresAt), days, until,
			p.botLink(ctx, u.ID))
		canonical := watchdog.PlainText(watchdog.Render(msg, watchdog.DefaultLang))
		_ = p.expiryRecorder.Alert(ctx, watchdog.SeverityLow, canonical)                        // console log
		p.recordEvent(ctx, model.EventAlert, model.SeverityWarn, alertLine(msg), "", u.OwnerID) // namespaced event log
		p.sendNamespaceAlert(ctx, u.OwnerID, watchdog.SeverityLow, msg)                         // Telegram: client owner only (admin fallback), their language
		if err := p.store.MarkExpiryAlerted(ctx, u.ID, u.ExpiresAt); err != nil {
			log.Printf("expiry alert: mark %s: %v", u.ID, err)
		}
	}
}

// RunAccessExpiryLoop switches off clients whose access period has ended, at a
// fixed cadence of its own. It is not a push and does not need the fleet: a date
// passing is not something an operator tunes the frequency of, and it must keep
// working on a panel whose background reconcile is switched off.
func (p *Panel) RunAccessExpiryLoop(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		return
	}
	p.SweepExpiredAccess(ctx)
	dynLoop(ctx, func() time.Duration { return interval }, func() { p.SweepExpiredAccess(ctx) })
}

// SweepExpiredAccess switches off every client whose access date has passed and
// writes one line per client into its owner's journal. Expiry was enforced only
// where the config is rendered: the client stopped connecting on the date, while
// its switch stayed green, so nothing on the screen told a paid-up client from a
// lapsed one. The rendered config already leaves an expired client out; this
// changes no traffic; it makes the record say what is already true, and it is
// what gives a later extension something to ask about. Returns how many were
// switched off.
func (p *Panel) SweepExpiredAccess(ctx context.Context) int {
	st, err := p.store.LoadState(ctx)
	if err != nil {
		log.Printf("access expiry: load state: %v", err)
		return 0
	}
	now := p.now()
	off := 0
	for _, u := range st.Users {
		if !u.Enabled || !u.Expired(now) {
			continue
		}
		u.Enabled = false
		if err := p.store.UpsertUser(ctx, u); err != nil {
			log.Printf("access expiry: %s: %v", u.ID, err)
			continue
		}
		p.recordEvent(ctx, model.EventAdmin, model.SeverityWarn,
			journal.Line("client %q switched off: access ended %s", userLabel(u), u.ExpiresAt.UTC().Format("2006-01-02")),
			"system", u.OwnerID)
		off++
	}
	// The record has changed. Ask for the same coalesced push any other write
	// gets: on a panel whose periodic reconcile is switched off, the nodes would
	// otherwise keep the lapsed client until something else happened to be saved.
	if off > 0 {
		p.markDirty()
	}
	return off
}

// userLabel names a client the way its owner wrote it down, falling back to the
// login when the client was created without a display name.
func userLabel(u model.User) string {
	if s := strings.TrimSpace(u.DisplayName); s != "" {
		return s
	}
	return u.Username
}

// errAccessEnded is the refusal an operator gets for switching on a client whose
// access date has passed: the sweep above would put it straight back off, and
// the rendered config would leave it out meanwhile. The date is not in the text
// so the panel can translate the sentence; the form shows the date itself.
var errAccessEnded = errors.New("access has already ended; extend the access date to switch this client on")

// SetConfigHealthAlerts enables config-health alerts: when the fleet has no
// healthy in-rotation entry node, every namespace with enabled clients is warned
// once that its clients cannot get a working config (and once more on recovery).
// In the new model an operator lives only in the bot and sees infra only as a ✅/⚠️
// aggregate, so it must be paged when an infra fault breaks its clients. The
// alerter records (log + event log); the Telegram leg is routed to the
// namespace owner.
func (p *Panel) SetConfigHealthAlerts(a watchdog.Alerter) {
	p.configRecorder = a
	p.configMu.Lock()
	p.configBad = map[string]bool{}
	p.configMu.Unlock()
}

// RunConfigHealthLoop periodically checks whether clients can get a working
// config. interval <= 0 or no recorder disables it; it shares the billing cadence.
func (p *Panel) RunConfigHealthLoop(ctx context.Context, interval time.Duration) {
	if interval <= 0 || p.configRecorder == nil {
		return
	}
	p.CheckConfigHealth(ctx)
	dynLoop(ctx, func() time.Duration {
		return p.liveInterval(model.PanelSettings.BillingInterval, func(ps model.PanelSettings) int { return ps.BillingAlertHours }, interval)
	}, func() { p.CheckConfigHealth(ctx) })
}

// CheckConfigHealth alerts each namespace once when its enabled clients cannot get
// a working config because the fleet has no healthy in-rotation entry, and once
// more when entry health is restored. De-dup is in-memory (per owner): the alert
// fires only on the bad↔ok transition, leaving a sustained outage un-resent every
// tick. The count is non-identifying (no client names) and safe to page the
// owner directly. A single-process panel re-evaluates from scratch on restart,
// which at worst re-sends one transition alert, acceptable for a transient signal.
func (p *Panel) CheckConfigHealth(ctx context.Context) {
	if p.configRecorder == nil {
		return
	}
	st, err := p.store.LoadState(ctx)
	if err != nil {
		log.Printf("config health: load state: %v", err)
		return
	}
	usable := hasUsableEntry(st)
	perNS := map[string]int{}
	for _, u := range st.Users {
		if u.Enabled {
			perNS[u.OwnerID]++
		}
	}
	p.configMu.Lock()
	defer p.configMu.Unlock()
	if p.configBad == nil {
		p.configBad = map[string]bool{}
	}
	// Consider every namespace that has clients now or was previously warned.
	considered := map[string]bool{}
	for ns := range perNS {
		considered[ns] = true
	}
	for ns := range p.configBad {
		considered[ns] = true
	}
	for ns := range considered {
		switch {
		case !usable && perNS[ns] > 0:
			if !p.configBad[ns] {
				msg := watchdog.MsgConfigCannotBuild(perNS[ns])
				canonical := watchdog.PlainText(watchdog.Render(msg, watchdog.DefaultLang))
				_ = p.configRecorder.Alert(ctx, watchdog.SeverityCritical, canonical)
				p.recordEvent(ctx, model.EventAlert, model.SeverityWarn, alertLine(msg), "", ns)
				p.sendNamespaceAlert(ctx, ns, watchdog.SeverityCritical, msg)
				p.configBad[ns] = true
			}
		default:
			if p.configBad[ns] {
				msg := watchdog.MsgConfigRestored()
				canonical := watchdog.PlainText(watchdog.Render(msg, watchdog.DefaultLang))
				_ = p.configRecorder.Alert(ctx, watchdog.SeverityLow, canonical)
				p.recordEvent(ctx, model.EventAlert, model.SeverityInfo, alertLine(msg), "", ns)
				p.sendNamespaceAlert(ctx, ns, watchdog.SeverityLow, msg)
				delete(p.configBad, ns)
			}
		}
	}
}

// hasUsableEntry reports whether the fleet has at least one entry node that is
// healthy and in rotation: the precondition for handing out a config that will
// actually connect (mirrors the bot's pickEntry preference).
func hasUsableEntry(st model.State) bool {
	for _, n := range st.Nodes {
		if n.IsEntry() && !n.Maintenance && n.Health == model.HealthHealthy {
			return true
		}
	}
	return false
}

// certStatusLabel renders an agent cert report into a short human label stored
// in Domain.tls_status.
func certStatusLabel(c *agentapi.TLSCertStatus, now time.Time) string {
	switch {
	case c.NotAfter != nil:
		days := int(c.NotAfter.Sub(now).Hours() / 24)
		label := fmt.Sprintf("valid until %s (%dd)", c.NotAfter.UTC().Format("2006-01-02"), days)
		if c.LastError != "" {
			label += " · renew error"
		}
		return label
	case c.LastError != "":
		return "error: " + c.LastError
	default:
		return "pending"
	}
}

// RunStatsLoop periodically polls per-user traffic from entry nodes until ctx is
// cancelled (mirrors RunReconcileLoop). interval <= 0 disables it.
func (p *Panel) RunStatsLoop(ctx context.Context, interval time.Duration) {
	if interval <= 0 || p.fleet == nil {
		return
	}
	dynLoop(ctx, func() time.Duration {
		return p.liveInterval(model.PanelSettings.StatsInterval, func(ps model.PanelSettings) int { return ps.StatsSeconds }, interval)
	}, func() {
		if err := p.PollStatsOnce(ctx); err != nil {
			log.Printf("stats loop: %v", err)
		}
	})
}

func (p *Panel) handleTraffic(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	totals, err := p.store.UserTrafficTotals(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Per-user rows never leak another namespace's client names. A scoped account,
	// and the bootstrap owner unless it has turned on the cross-namespace lens,
	// sees detailed rows only for its own clients. Every other namespace collapses
	// into a non-identifying summary (volume + online count) the operator can
	// still gauge fleet load without seeing whose clients they are.
	acct := p.account(r)
	seeAll := p.seesAllNamespaces(r, acct)
	st, err := p.store.LoadState(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	ownerOf := make(map[string]string, len(st.Users))
	for _, u := range st.Users {
		ownerOf[u.ID] = u.OwnerID
	}
	me := acct.Namespace()
	// Active-now overlay: who moved past the online floor within the activity
	// window. The floor (KB/min) is a live Setting so a merely-connected client
	// idling on background chatter does not read as "online".
	settings, err := p.store.GetSettings(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	since := p.now().Add(-activityWindow)
	activity, err := p.store.RecentActivity(ctx, since, settings.Panel.ActivityFloorBytes(activityWindow))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	rows := make([]trafficRow, 0, len(totals))
	activeCount := 0
	othersByNS := map[string]*nsTrafficSummary{}
	for _, tot := range totals {
		_, active := activity[tot.UserID]
		if seeAll || ownerOf[tot.UserID] == me {
			row := trafficRow{UserTrafficTotal: tot}
			if a, ok := activity[tot.UserID]; ok {
				row.Active = true
				row.RecentRx, row.RecentTx = a.RxBytes, a.TxBytes
				activeCount++
			}
			rows = append(rows, row)
			continue
		}
		ns := ownerOf[tot.UserID]
		s := othersByNS[ns]
		if s == nil {
			s = &nsTrafficSummary{Namespace: ns}
			othersByNS[ns] = s
		}
		s.Users++
		s.RxBytes += tot.RxBytes
		s.TxBytes += tot.TxBytes
		s.TotalBytes += tot.TotalBytes
		if active {
			s.Online++
		}
	}
	others := make([]nsTrafficSummary, 0, len(othersByNS))
	for _, s := range othersByNS {
		others = append(others, *s)
	}
	sort.Slice(others, func(i, j int) bool { return others[i].Namespace < others[j].Namespace })
	writeJSON(w, http.StatusOK, map[string]any{
		"users":          rows,
		"active_count":   activeCount,
		"window_seconds": int(activityWindow.Seconds()),
		"others":         others,
	})
}

// nsTrafficSummary is the non-identifying roll-up of one namespace the viewer may
// not see in detail: how many clients, how many are online now, and the volume,
// never a username. It powers the Traffic tab's "other namespaces" summary.
type nsTrafficSummary struct {
	Namespace  string `json:"namespace"`
	Users      int    `json:"users"`
	Online     int    `json:"online"`
	RxBytes    int64  `json:"rx_bytes"`
	TxBytes    int64  `json:"tx_bytes"`
	TotalBytes int64  `json:"total_bytes"`
}

// trafficRow is a user's cumulative totals plus the active-now overlay (recent
// throughput within the activity window).
type trafficRow struct {
	store.UserTrafficTotal
	Active   bool  `json:"active"`
	RecentRx int64 `json:"recent_rx_bytes"`
	RecentTx int64 `json:"recent_tx_bytes"`
}

// handleTrafficSeries returns one user's bucketed traffic time-series for the
// per-user graph: GET /api/traffic/series?user=<id>&minutes=<n>. Operators may
// query only their own clients.
func (p *Panel) handleTrafficSeries(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userID := r.URL.Query().Get("user")
	if userID == "" {
		writeErr(w, http.StatusBadRequest, "user query parameter is required")
		return
	}
	minutes := 60
	if v := r.URL.Query().Get("minutes"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 31*24*60 {
			minutes = n
		}
	}
	// Owner-gate: a scoped account may only see its own client's series.
	if acct := p.account(r); !acct.IsBootstrapOwner() {
		st, err := p.store.LoadState(ctx)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		owned := false
		for _, u := range st.Users {
			if u.ID == userID {
				owned = u.OwnerID == acct.Namespace()
				break
			}
		}
		if !owned {
			writeErr(w, http.StatusForbidden, "that client belongs to another scope")
			return
		}
	}
	window := time.Duration(minutes) * time.Minute
	// ~60 points across the window, floor 60s (the finest the stats loop samples).
	bucket := window / 60
	if bucket < time.Minute {
		bucket = time.Minute
	}
	series, err := p.store.UserTrafficSeries(ctx, userID, p.now().Add(-window), bucket)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"series": series, "bucket_seconds": int(bucket.Seconds())})
}

// handleNodeTrafficSeries returns one node's bucketed throughput time-series:
// GET /api/nodes/series?node=<id>&minutes=<n>. Nodes are visible to every
// account (read-only across namespaces), so no owner gate, only a session.
func (p *Panel) handleNodeTrafficSeries(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	nodeID := r.URL.Query().Get("node")
	if nodeID == "" {
		writeErr(w, http.StatusBadRequest, "node query parameter is required")
		return
	}
	minutes := 60
	if v := r.URL.Query().Get("minutes"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 31*24*60 {
			minutes = n
		}
	}
	window := time.Duration(minutes) * time.Minute
	bucket := window / 60
	if bucket < time.Minute {
		bucket = time.Minute
	}
	series, err := p.store.NodeTrafficSeries(ctx, nodeID, p.now().Add(-window), bucket)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"series": series, "bucket_seconds": int(bucket.Seconds())})
}

// handleNodeResourceSeries returns one node's bucketed CPU/memory history:
// GET /api/nodes/resource-series?node=<id>&minutes=<n>. Same visibility as
// handleNodeTrafficSeries (nodes are visible to every account, read-only).
func (p *Panel) handleNodeResourceSeries(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	nodeID := r.URL.Query().Get("node")
	if nodeID == "" {
		writeErr(w, http.StatusBadRequest, "node query parameter is required")
		return
	}
	minutes := 60
	if v := r.URL.Query().Get("minutes"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 31*24*60 {
			minutes = n
		}
	}
	window := time.Duration(minutes) * time.Minute
	bucket := window / 60
	if bucket < time.Minute {
		bucket = time.Minute
	}
	series, err := p.store.NodeResourceSeries(ctx, nodeID, p.now().Add(-window), bucket)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"series": series, "bucket_seconds": int(bucket.Seconds())})
}

// nodeOverview is one node's monitoring row: configured limits vs live usage.
type nodeOverview struct {
	ID              string                  `json:"id"`
	Name            string                  `json:"name"`
	Role            model.PublicRole        `json:"role"`
	Health          model.NodeHealth        `json:"health"`
	Limits          *model.NodeLimits       `json:"limits,omitempty"`
	System          *agentapi.SystemMetrics `json:"system,omitempty"`
	MetricsAgeSec   int64                   `json:"metrics_age_sec"` // -1 if never seen
	TrafficRx       int64                   `json:"traffic_rx_bytes"`
	TrafficTx       int64                   `json:"traffic_tx_bytes"`
	TrafficPeriod   string                  `json:"traffic_period,omitempty"`
	Billing         *model.NodeBilling      `json:"billing,omitempty"`
	BillingDaysLeft *int                    `json:"billing_days_left,omitempty"` // nil if no paid_until
	Reconcile       *nodeReconcileView      `json:"reconcile,omitempty"`
	ActiveClients   int                     `json:"active_clients"`        // clients moving bytes through this entry now
	Edge            *nodeEdgeView           `json:"edge,omitempty"`        // external :443 probe (entry nodes)
	Replication     *nodeReplView           `json:"replication,omitempty"` // standby replication-slot health
}

// nodeEdgeView is the external edge-probe result surfaced to the UI dot.
type nodeEdgeView struct {
	Ok     bool   `json:"ok"`
	Error  string `json:"error,omitempty"`
	AgeSec int64  `json:"age_sec"`
}

// nodeReplView is a standby's replication-slot health surfaced to the UI dot.
// Active=false or Missing=true drives a warning (HA degraded, data plane fine).
type nodeReplView struct {
	Active      bool   `json:"active"`
	Missing     bool   `json:"missing"`
	BytesBehind *int64 `json:"bytes_behind,omitempty"`
	AgeSec      int64  `json:"age_sec"`
}

type nodeReconcileView struct {
	Outcome  string          `json:"outcome"`
	Changed  bool            `json:"changed"`
	Error    string          `json:"error,omitempty"`
	Warnings []render.Phrase `json:"warnings,omitempty"`
	AgeSec   int64           `json:"age_sec"`
	Ok       bool            `json:"ok"`
}

func (p *Panel) handleOverview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	st, err := p.store.LoadState(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	traffic, err := p.store.NodeTrafficTotals(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	tByNode := make(map[string]store.NodeTrafficTotal, len(traffic))
	for _, t := range traffic {
		tByNode[t.NodeID] = t
	}
	// Who is on each entry right now, letting a drain say whether it cuts
	// anybody off. Best-effort: a failure here must not take the page down.
	activeByEntry := map[string]int{}
	if set, err := p.store.GetSettings(ctx); err == nil {
		if m, err := p.store.ActiveClientsByEntry(ctx, p.now().Add(-activityWindow), set.Panel.ActivityFloorBytes(activityWindow)); err == nil {
			activeByEntry = m
		}
	}

	now := p.now()
	rows := make([]nodeOverview, 0, len(st.Nodes))
	healthy := 0
	for _, n := range st.Nodes {
		if n.Health == model.HealthHealthy {
			healthy++
		}
		row := nodeOverview{ID: n.ID, Name: n.Name, Role: n.PublicRole, Health: n.Health, Limits: n.Limits, Billing: n.Billing, MetricsAgeSec: -1}
		if n.Billing != nil && n.Billing.PaidUntil != nil {
			d := int(n.Billing.PaidUntil.Sub(now).Hours() / 24)
			row.BillingDaysLeft = &d
		}
		p.metricsMu.Lock()
		if m, ok := p.metrics[n.ID]; ok {
			row.System = m.System
			row.MetricsAgeSec = int64(now.Sub(m.At).Seconds())
		}
		p.metricsMu.Unlock()
		if t, ok := tByNode[n.ID]; ok {
			row.TrafficRx, row.TrafficTx, row.TrafficPeriod = t.RxBytes, t.TxBytes, t.Period
		}
		row.ActiveClients = activeByEntry[n.ID]
		p.reconcileMu.Lock()
		if rec, ok := p.lastReconcile[n.ID]; ok {
			row.Reconcile = &nodeReconcileView{
				Outcome: rec.Outcome, Changed: rec.Changed, Error: rec.Error, Warnings: rec.Warnings,
				AgeSec: int64(now.Sub(rec.At).Seconds()),
				Ok:     rec.Error == "" && (rec.Outcome == "applied" || rec.Outcome == "no-change"),
			}
		}
		p.reconcileMu.Unlock()
		// Only entry nodes have a public :443 web endpoint to probe. An exit's
		// :443 is Reality (not externally HTTP/TLS-probeable), and a node that
		// flipped entry->exit keeps a stale lastEdge record the probe loop no
		// longer refreshes, so gate the dot on the current role, not on the
		// mere presence of a (possibly frozen) record.
		if n.IsEntry() {
			p.edgeMu.Lock()
			if e, ok := p.lastEdge[n.ID]; ok {
				row.Edge = &nodeEdgeView{Ok: e.Ok, Error: e.Error, AgeSec: int64(now.Sub(e.At).Seconds())}
			}
			p.edgeMu.Unlock()
		}
		// Replication health is only recorded for nodes the control plane expects
		// to be streaming standbys; surface it so the UI dot can warn on a down or
		// far-behind replica (HA degraded, data plane unaffected).
		p.replMu.Lock()
		if rr, ok := p.lastRepl[n.ID]; ok {
			row.Replication = &nodeReplView{Active: rr.Active, Missing: rr.Missing, BytesBehind: rr.BytesBehind, AgeSec: int64(now.Sub(rr.At).Seconds())}
		}
		p.replMu.Unlock()
		rows = append(rows, row)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"nodes": rows,
		"summary": map[string]any{
			"nodes":       len(st.Nodes),
			"healthy":     healthy,
			"users":       len(st.Users),
			"groups":      len(st.Groups),
			"epoch":       st.ControlPlane.Epoch,
			"active_node": st.ControlPlane.ActiveNodeID,
		},
	})
}

// ---- entity CRUD ----

func (p *Panel) handleUpsertNode(w http.ResponseWriter, r *http.Request) {
	var n model.Node
	if !decode(w, r, &n) {
		return
	}
	// Creating a node is infra (admin); operators get nodes via "Add server"
	// (provision). An operator may edit an existing node it owns, but cannot
	// create one or reassign ownership.
	acct := p.account(r)
	st, err := p.store.LoadState(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	existing, exists := st.NodeByID(n.ID)
	// mgmt_capable is owned by the control-plane flows (bootstrap, make-standby,
	// convert), not the node form. Carry the stored value across an edit so the
	// form, which does not expose the flag, cannot silently clear it and break
	// HA primary detection.
	if exists {
		n.MgmtCapable = existing.MgmtCapable
		// pg_role is owned by the HA promote flow, not the node form; carry the
		// stored value so an edit cannot reset the live PG primary to "none" and
		// make the overview mis-report HA state. (The store also COALESCEs a
		// 'none' pg_role, so non-panel callers are covered too.)
		n.PGRole = existing.PGRole
		// The exit's Reality keys live only in dial_in and are not always re-posted
		// by the form (and priv_key is redacted from reads). Preserve the stored
		// keys when the incoming dial_in omits them. An edit then cannot silently
		// wipe the exit's identity and break every client.
		preserveDialInKeys(&n, existing)
	}
	if !acct.CanManageInfra() {
		// Operators have no infra surface; node create/edit is admin-only (and the
		// route is infraOnly-wrapped, so this is defense in depth).
		writeErr(w, http.StatusForbidden, "node management is available to admins only")
		return
	}
	p.upsert(w, r, func(ctx context.Context) error { return p.store.UpsertNode(ctx, n) }, n.ID)
}

// preserveDialInKeys carries the stored exit's Reality identity across a node
// edit when the incoming form omits it. Two cases:
//   - the form posts no dial_in at all (nil): carry the whole stored one, unless
//     the node is genuinely losing its dial_in (a deliberate exit->entry change
//     posts public_role=entry, in which case we leave it nil for validation).
//   - the form posts a dial_in but with blank Reality keys (public_key/priv_key/
//     short_id, e.g. priv_key was redacted from the read it pre-filled from):
//     fill each blank key from the stored value so it round-trips.
func preserveDialInKeys(n *model.Node, existing model.Node) {
	if existing.DialIn == nil {
		return
	}
	if n.DialIn == nil {
		if n.PublicRole == model.RoleEntry {
			return // deliberate exit->entry conversion clears dial_in
		}
		clone := *existing.DialIn
		n.DialIn = &clone
		return
	}
	if n.DialIn.PublicKey == "" {
		n.DialIn.PublicKey = existing.DialIn.PublicKey
	}
	if n.DialIn.PrivKey == "" {
		n.DialIn.PrivKey = existing.DialIn.PrivKey
	}
	if n.DialIn.ShortID == "" {
		n.DialIn.ShortID = existing.DialIn.ShortID
	}
}

func (p *Panel) handleDeleteNode(w http.ResponseWriter, r *http.Request) {
	p.del(w, r, func(ctx context.Context, id string) error { return p.store.DeleteNode(ctx, id) })
}

// handleReassignEgress moves every group that egresses via the given exit onto a
// chosen healthy exit, and drains the source node. It is the standalone "move
// egress off this exit" action a dead-exit recovery needs: the same switchEgress
// the convert wizard uses, but reachable directly. Reconcile then
// pushes the new entry configs so traffic re-homes to the target.
func (p *Panel) handleReassignEgress(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		TargetExitID string `json:"target_exit_id"`
	}
	if !decode(w, r, &req) {
		return
	}
	target := strings.TrimSpace(req.TargetExitID)
	if target == "" || target == id {
		writeErr(w, http.StatusBadRequest, "target_exit_id must be a different exit node")
		return
	}
	st, err := p.store.LoadState(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	tn, ok := st.NodeByID(target)
	if !ok || !tn.IsExit() {
		writeErr(w, http.StatusBadRequest, "target_exit_id is not an exit node")
		return
	}
	if tn.Maintenance {
		writeErr(w, http.StatusBadRequest, "target exit is drained — pick a node in rotation")
		return
	}
	// Only the groups that currently egress via the dead node move; others stay.
	var groupIDs []string
	for _, g := range st.Groups {
		if g.DefaultExitID == id {
			groupIDs = append(groupIDs, g.ID)
		}
	}
	if len(groupIDs) == 0 {
		writeErr(w, http.StatusBadRequest, "no groups egress via that node")
		return
	}
	opID := newOpID() // the reassignment and every group it moves
	moves, err := p.switchEgress(r.Context(), groupIDs, target)
	p.logEgressMoves(r.Context(), moves, model.SeverityInfo, journal.Line("egress reassigned off %s", exitLabel(st, id)), actorOf(p, r), opID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	moved := len(moves)
	// Drain the source so it stops taking rotation. Best-effort: the reassignment
	// is the point, and a drain failure must not lose the egress move.
	if _, srcExists := st.NodeByID(id); srcExists {
		if err := p.store.SetNodeMaintenance(r.Context(), id, true); err != nil {
			log.Printf("reassign-egress: drain %s: %v", id, err)
		}
	}
	actor, _, _ := p.currentUser(r)
	p.recordOpEvent(r.Context(), model.EventAdmin, model.SeverityInfo,
		journal.Line("reassigned egress of %s from %s to %s", journal.Groups(moved), exitLabel(st, id), exitLabel(st, target)), actor, "", opID)
	p.markDirty() // new egress → re-render entries
	writeJSON(w, http.StatusOK, map[string]any{"from": id, "to": target, "groups_moved": moved})
}

// handleSetNodeLimits sets a node's optional VPS plan limits (the "Limits"
// button). An all-zero body clears them.
// handlePromoteTLS switches one entry node's ACME issuance from staging to Let's
// Encrypt production and reissues, over the mTLS control channel (no SSH).
func (p *Panel) handlePromoteTLS(w http.ResponseWriter, r *http.Request) {
	if p.fleet == nil {
		writeErr(w, http.StatusServiceUnavailable, errNoFleet.Error())
		return
	}
	id := r.PathValue("id")
	st, err := p.store.LoadState(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	var node *model.Node
	for i := range st.Nodes {
		if st.Nodes[i].ID == id {
			node = &st.Nodes[i]
			break
		}
	}
	if node == nil {
		writeErr(w, http.StatusNotFound, "node not found")
		return
	}
	if err := p.fleet.PromoteACME(r.Context(), *node); err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": id, "status": "promoted"})
}

func (p *Panel) handleSetNodeLimits(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var lim model.NodeLimits
	if !decode(w, r, &lim) {
		return
	}
	ptr := &lim
	if lim == (model.NodeLimits{}) {
		ptr = nil // all zero -> clear
	}
	if err := p.store.SetNodeLimits(r.Context(), id, ptr); err != nil {
		// A figure the form let through is the operator's mistake, not the
		// server's: it comes back at the field, in words, not as a 500.
		writeErr(w, http.StatusBadRequest, friendlyErr(err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": id, "status": "saved"})
}

// handleSetNodeBilling sets a node's payment tracking (paid-until + term). An
// empty body (no date, term 0) clears it.
func (p *Panel) handleSetNodeBilling(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var b model.NodeBilling
	if !decode(w, r, &b) {
		return
	}
	ptr := &b
	if b.PaidUntil == nil && b.TermMonths == 0 {
		ptr = nil // clear
	}
	if err := p.store.SetNodeBilling(r.Context(), id, ptr); err != nil {
		writeErr(w, http.StatusBadRequest, friendlyErr(err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": id, "status": "saved"})
}

// handleSetNodeMaintenance drains (on=true) or restores a node. Draining an exit
// that groups egress through requires an explicit EgressTarget, either another
// exit or the ExitDirect sentinel, so those groups move to a chosen destination
// instead of silently dropping to local egress. The background reconcile then
// re-renders
// the affected entries.
func (p *Panel) handleSetNodeMaintenance(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		Maintenance  bool   `json:"maintenance"`
		EgressTarget string `json:"egress_target"`
	}
	if !decode(w, r, &req) {
		return
	}

	// The log names the node, not just its id: "drained node n-exit-01" is not
	// something anyone recognises a week later.
	st, err := p.store.LoadState(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	label := exitLabel(st, id)

	opID := newOpID() // the drain (or resume) and every group it moves
	var moves []egressMove
	if req.Maintenance {
		node, ok := st.NodeByID(id)
		if ok && node.IsExit() {
			var dependent []string
			for _, g := range st.Groups {
				if g.DefaultExitID == id {
					dependent = append(dependent, g.ID)
				}
			}
			if len(dependent) > 0 {
				target := strings.TrimSpace(req.EgressTarget)
				if target == "" {
					writeErr(w, http.StatusBadRequest, "egress_target is required: groups egress through this exit — choose another exit or direct")
					return
				}
				if target != model.ExitDirect {
					tn, ok := st.NodeByID(target)
					if target == id || !ok || !tn.IsExit() {
						writeErr(w, http.StatusBadRequest, "egress_target must be a different exit node or direct")
						return
					}
					if tn.Maintenance {
						writeErr(w, http.StatusBadRequest, "egress_target is drained — pick a node in rotation")
						return
					}
				}
				var err error
				moves, err = p.switchEgress(r.Context(), dependent, target)
				// Whatever moved before the error is still logged: a half-done
				// move an operator cannot see is a move they cannot undo.
				p.logEgressMoves(r.Context(), moves, model.SeverityInfo, journal.Line("maintenance of %s", exitLabel(st, id)), actorOf(p, r), opID)
				if err != nil {
					writeErr(w, http.StatusInternalServerError, err.Error())
					return
				}
			}
		}
	}

	if err := p.store.SetNodeMaintenance(r.Context(), id, req.Maintenance); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	actor, _, _ := p.currentUser(r)
	msg := journal.Line("resumed node %s", label)
	if req.Maintenance {
		msg = journal.Line("drained node %s", label)
	}
	if len(moves) > 0 {
		msg = journal.Join(msg, journal.Line(" — %s moved off it", journal.Groups(len(moves))))
	}
	p.recordOpEvent(r.Context(), model.EventAdmin, model.SeverityInfo, msg, actor, "", opID) // node ops are infra (admin namespace)
	p.markDirty()                                                                            // drain/resume changes desired routing
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "maintenance": req.Maintenance, "groups_moved": len(moves), "status": "saved"})
}

func (p *Panel) handleUpsertGroup(w http.ResponseWriter, r *http.Request) {
	var g model.Group
	if !decode(w, r, &g) {
		return
	}
	curOwner, existed := p.ownerOfGroup(r.Context(), g.ID)
	_, owner, ok := p.resolveOwner(r, curOwner, existed)
	if !ok {
		writeErr(w, http.StatusForbidden, "that group belongs to another scope")
		return
	}
	g.OwnerID = owner
	// A new group with no egress chosen inherits the deployment default exit, so
	// it never silently falls through to local egress once a exit exists. An
	// explicit local-egress choice arrives as the ExitDirect sentinel and is left
	// as-is; edits keep whatever the group already carries.
	st, stErr := p.store.LoadState(r.Context())
	if !existed && g.DefaultExitID == "" && stErr == nil {
		g.DefaultExitID = defaultEgressExit(st)
	}
	// An edit that moves a group to another exit is the same event as a drain
	// moving it, and gets the same line: which group, from where, to where.
	// "saved group g-1" on its own left nothing to put back from.
	var move *egressMove
	if existed && stErr == nil {
		for _, cur := range st.Groups {
			if cur.ID == g.ID && !sameEgress(cur.DefaultExitID, g.DefaultExitID) {
				move = &egressMove{
					GroupID: g.ID, GroupName: nameOr(g.Name, g.ID), OwnerID: g.OwnerID,
					From: exitLabel(st, cur.DefaultExitID), To: exitLabel(st, g.DefaultExitID),
				}
			}
		}
	}
	p.upsert(w, r, func(ctx context.Context) error {
		if err := p.store.UpsertGroup(ctx, g); err != nil {
			return err
		}
		if move != nil { // only once the write went through
			p.logEgressMoves(ctx, []egressMove{*move}, model.SeverityInfo, journal.Line("changed by hand"), actorOf(p, r), "")
		}
		return nil
	}, g.ID)
}

// sameEgress reports whether two stored default_exit_id values send the group's
// traffic to the same place. An unset exit and the direct sentinel both leave
// from the entry node, so the form that offers that answer once must not write
// a move into the journal when nothing about the traffic changed.
func sameEgress(a, b string) bool {
	norm := func(s string) string {
		if s == "" {
			return model.ExitDirect
		}
		return s
	}
	return norm(a) == norm(b)
}

// defaultEgressExit returns the exit a newly created group should inherit: the
// one the catch-all "default" group already uses when that is a live exit,
// otherwise the lowest-id exit (preferring one still in rotation). Returns ""
// when the deployment has no exit at all, leaving the group on local egress.
func defaultEgressExit(st model.State) string {
	liveExit := func(id string) bool {
		if id == "" || id == model.ExitDirect {
			return false
		}
		n, ok := st.NodeByID(id)
		return ok && n.IsExit()
	}
	for _, g := range st.Groups {
		if g.ID == store.DefaultGroupID && liveExit(g.DefaultExitID) {
			return g.DefaultExitID
		}
	}
	var inRotation, anyExit string
	for _, n := range st.Nodes {
		if !n.IsExit() {
			continue
		}
		if anyExit == "" || n.ID < anyExit {
			anyExit = n.ID
		}
		if !n.Maintenance && (inRotation == "" || n.ID < inRotation) {
			inRotation = n.ID
		}
	}
	if inRotation != "" {
		return inRotation
	}
	return anyExit
}

func (p *Panel) handleDeleteGroup(w http.ResponseWriter, r *http.Request) {
	curOwner, existed := p.ownerOfGroup(r.Context(), r.PathValue("id"))
	if _, _, ok := p.resolveOwner(r, curOwner, existed); !ok {
		writeErr(w, http.StatusForbidden, "that group belongs to another scope")
		return
	}
	p.del(w, r, func(ctx context.Context, id string) error { return p.store.DeleteGroup(ctx, id) })
}

// ownerOfGroup looks up a group's current owner_id (existed=false if absent).
func (p *Panel) ownerOfGroup(ctx context.Context, id string) (owner string, existed bool) {
	st, err := p.store.LoadState(ctx)
	if err != nil {
		return "", false
	}
	for _, g := range st.Groups {
		if g.ID == id {
			return g.OwnerID, true
		}
	}
	return "", false
}

// userUpsertRequest carries the password (model.User.Secret is json:"-").
type userUpsertRequest struct {
	ID          string     `json:"id"`
	Username    string     `json:"username"`
	Password    string     `json:"password"`
	Regenerate  bool       `json:"regenerate"` // force a fresh secret on edit
	DisplayName string     `json:"display_name"`
	Enabled     bool       `json:"enabled"`
	GroupID     string     `json:"group_id"`
	ExpiresAt   *time.Time `json:"expires_at"`
}

// minClientSecretLen is the floor for an operator-supplied VPN client credential.
// Auto-generated secrets are 48 hex chars; this only stops a human from setting a
// trivial password (e.g. "1234") that weakens the client's auth.
const minClientSecretLen = 12

func (p *Panel) handleUpsertUser(w http.ResponseWriter, r *http.Request) {
	var req userUpsertRequest
	if !decode(w, r, &req) {
		return
	}
	if req.Password != "" && len(req.Password) < minClientSecretLen {
		writeErr(w, http.StatusBadRequest, fmt.Sprintf("password must be at least %d characters", minClientSecretLen))
		return
	}
	// Secret resolution: an explicit password always wins; otherwise on an edit keep
	// the stored secret (a blank field must not silently rotate credentials) and
	// only generate when creating or when the operator ticks "regenerate".
	var existing model.User
	var existed bool
	if strings.TrimSpace(req.ID) != "" {
		var err error
		existing, existed, err = p.store.User(r.Context(), req.ID)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	secret := req.Password
	switch {
	case secret != "":
		// explicit password: use as given
	case existed && !req.Regenerate:
		secret = existing.Secret // edit without a new password: keep current
	default:
		// create, or an explicit regenerate request: mint a new credential secret.
		buf := make([]byte, 24)
		if _, err := rand.Read(buf); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		secret = hex.EncodeToString(buf)
	}
	acct, owner, ok := p.resolveOwner(r, existing.OwnerID, existed)
	if !ok {
		writeErr(w, http.StatusForbidden, "that client belongs to another scope")
		return
	}
	if !acct.IsBootstrapOwner() {
		st, err := p.store.LoadState(r.Context())
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if !operatorOwnsGroup(st, acct, req.GroupID) {
			// Covers both a typo'd id and a real other-namespace group without
			// confirming which (no cross-namespace existence oracle).
			writeErr(w, http.StatusForbidden, "no such group in your scope")
			return
		}
	}
	u := model.User{
		ID: req.ID, Username: req.Username, Secret: secret, DisplayName: req.DisplayName,
		Enabled: req.Enabled, GroupID: req.GroupID, ExpiresAt: req.ExpiresAt, OwnerID: owner,
	}
	// A switch that does not hold is worse than a refusal: the sweep would put an
	// expired client straight back off, and the rendered config leaves it out
	// meanwhile. Extending the date and switching on in one save is fine; this
	// only catches a date that has already passed.
	if u.Enabled && u.Expired(p.now()) {
		writeErr(w, http.StatusBadRequest, errAccessEnded.Error())
		return
	}
	p.upsert(w, r, func(ctx context.Context) error { return p.store.UpsertUser(ctx, u) }, u.ID)
}

func (p *Panel) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	u, existed, err := p.store.User(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if _, _, ok := p.resolveOwner(r, u.OwnerID, existed); !ok {
		writeErr(w, http.StatusForbidden, "that client belongs to another scope")
		return
	}
	p.del(w, r, func(ctx context.Context, id string) error { return p.store.DeleteUser(ctx, id) })
}

// bulkUsersRequest applies one action to many users at once.
type bulkUsersRequest struct {
	IDs       []string   `json:"ids"`
	Action    string     `json:"action"` // enable|disable|delete|set_group|set_expiry|clear_expiry
	GroupID   string     `json:"group_id,omitempty"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// handleBulkUsers applies one action to a set of users in a single call, strictly
// within the caller's namespace (an operator may only touch its own clients and
// only move them into its own groups). Each user is applied independently; the
// response reports how many succeeded and the first errors. Mirrors the
// per-resource gates of handleUpsertUser/handleDeleteUser.
func (p *Panel) handleBulkUsers(w http.ResponseWriter, r *http.Request) {
	var req bulkUsersRequest
	if !decode(w, r, &req) {
		return
	}
	switch req.Action {
	case "enable", "disable", "delete", "set_group", "set_expiry", "clear_expiry":
	default:
		writeErr(w, http.StatusBadRequest, "unknown bulk action "+req.Action)
		return
	}
	if len(req.IDs) == 0 {
		writeErr(w, http.StatusBadRequest, "no users selected")
		return
	}
	ctx := r.Context()
	st, err := p.store.LoadState(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	acct := p.account(r)
	if req.Action == "set_group" && !operatorOwnsGroup(st, acct, req.GroupID) {
		writeErr(w, http.StatusForbidden, "no such group in your scope")
		return
	}
	byID := make(map[string]model.User, len(st.Users))
	for _, u := range st.Users {
		byID[u.ID] = u
	}

	applied := 0
	var errs []string
	for _, id := range req.IDs {
		u, ok := byID[id]
		if !ok {
			errs = append(errs, id+": not found")
			continue
		}
		// Namespace gate: a scoped account may only touch its own clients.
		if !acct.IsBootstrapOwner() && u.OwnerID != acct.Namespace() {
			errs = append(errs, id+": another scope")
			continue
		}
		var opErr error
		switch req.Action {
		case "delete":
			opErr = p.store.DeleteUser(ctx, id)
		case "enable", "disable":
			if req.Action == "enable" && u.Expired(p.now()) {
				errs = append(errs, id+": "+errAccessEnded.Error())
				continue
			}
			u.Enabled = req.Action == "enable"
			opErr = p.store.UpsertUser(ctx, u)
		case "set_group":
			u.GroupID = req.GroupID
			opErr = p.store.UpsertUser(ctx, u)
		case "set_expiry":
			u.ExpiresAt = req.ExpiresAt
			opErr = p.store.UpsertUser(ctx, u)
		case "clear_expiry":
			u.ExpiresAt = nil
			opErr = p.store.UpsertUser(ctx, u)
		}
		if opErr != nil {
			errs = append(errs, id+": "+opErr.Error())
			continue
		}
		applied++
	}
	actor, _, _ := p.currentUser(r)
	p.recordEvent(ctx, model.EventAdmin, model.SeverityInfo,
		journal.Line("bulk %s on %s", journal.Term(req.Action), journal.Clients(applied)), actor, p.account(r).Namespace())
	if applied > 0 {
		p.markDirty()
	}
	writeJSON(w, http.StatusOK, map[string]any{"applied": applied, "failed": len(errs), "errors": errs})
}

// handleClientConfig exports a per-entry TrustTunnel client config for a user:
// GET /api/users/{id}/client-config?entry=<nodeID>.
func (p *Panel) handleClientConfig(w http.ResponseWriter, r *http.Request) {
	userID := r.PathValue("id")
	entry := r.URL.Query().Get("entry")
	if entry == "" {
		writeErr(w, http.StatusBadRequest, "entry query parameter is required")
		return
	}
	st, err := p.store.LoadState(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	// A scoped account may export configs only for its own clients (the deep link
	// carries the secret).
	if acct := p.account(r); !acct.IsBootstrapOwner() {
		owned := false
		for _, u := range st.Users {
			if u.ID == userID {
				owned = u.OwnerID == acct.Namespace()
				break
			}
		}
		if !owned {
			writeErr(w, http.StatusForbidden, "that client belongs to another scope")
			return
		}
	}
	cfg, err := clientcfg.Build(st, userID, entry, clientcfg.Options{}, p.now())
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, cfg)
}

func (p *Panel) handleUpsertRoutePolicy(w http.ResponseWriter, r *http.Request) {
	var pol model.RoutePolicy
	if !decode(w, r, &pol) {
		return
	}
	st, err := p.store.LoadState(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	cur, existed := findPolicy(st, pol.ID)
	acct := p.account(r)
	// A category the source does not publish cannot be given to a node, and the
	// node then receives no configuration at all. It is refused here, where
	// rules are actually written, and not only in the form that usually writes
	// them.
	// The catalogue cannot be read? That is not this rule's fault: the check says
	// nothing rather than refusing a write on a reading failure of its own.
	if srcs, err := p.store.RuleSetSources(r.Context()); err == nil {
		if bad := unknownGeoValues(pol, cur, existed, srcs); len(bad) > 0 {
			writeErr(w, http.StatusBadRequest, "not published by the geo source: "+strings.Join(bad, ", "))
			return
		}
	}
	// The network level is the shared baseline: any admin may write it, operators
	// may not. Touching a network rule at all, whether the incoming or the existing
	// tier is that level, requires the right.
	if isInfraTier(pol.Tier) || (existed && isInfraTier(cur.Tier)) {
		if !acct.CanManageInfra() {
			writeErr(w, http.StatusForbidden, "network-wide rules are managed by admins")
			return
		}
	}
	// A rule that STAYS infra keeps its infra ownership.
	if isInfraTier(pol.Tier) {
		if existed {
			pol.OwnerID = cur.OwnerID // keep its (infra) owner
		} else {
			pol.OwnerID = "" // infra namespace
		}
		p.upsert(w, r, func(ctx context.Context) error { return p.store.UpsertRoutePolicy(ctx, pol) }, pol.ID)
		return
	}
	// The new tier is exit, so it must follow namespace ownership, including when
	// converting from an infra rule: otherwise it'd stay in the infra branch with
	// owner_id="" and skip operatorOwnsGroup, letting a co-owner launder an exit
	// rule into the bootstrap namespace targeting a group it doesn't own. Routing
	// it here stamps the editor's namespace and re-runs the ownership check. A
	// co-owner is rejected (resolveOwner sees the infra-owned original as another
	// namespace) instead of stranding the rule.
	_, owner, ok := p.resolveOwner(r, cur.OwnerID, existed)
	if !ok {
		writeErr(w, http.StatusForbidden, "that policy belongs to another scope")
		return
	}
	if !operatorOwnsGroup(st, acct, pol.AppliesToGroupID) {
		writeErr(w, http.StatusForbidden, "no such group in your scope")
		return
	}
	pol.OwnerID = owner
	p.upsert(w, r, func(ctx context.Context) error { return p.store.UpsertRoutePolicy(ctx, pol) }, pol.ID)
}

func (p *Panel) handleDeleteRoutePolicy(w http.ResponseWriter, r *http.Request) {
	st, err := p.store.LoadState(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	cur, existed := findPolicy(st, r.PathValue("id"))
	acct := p.account(r)
	if isInfraTier(cur.Tier) {
		// Infra baseline: any admin may delete, operators may not.
		if !acct.CanManageInfra() {
			writeErr(w, http.StatusForbidden, "network-wide rules are managed by admins")
			return
		}
	} else if _, _, ok := p.resolveOwner(r, cur.OwnerID, existed); !ok {
		writeErr(w, http.StatusForbidden, "that policy belongs to another scope")
		return
	}
	p.del(w, r, func(ctx context.Context, id string) error { return p.store.DeleteRoutePolicy(ctx, id) })
}

// isInfraTier reports whether a policy tier is an infra-owned baseline (fleet
// mandate or guard safety net) that only the fleet owner may write.
func isInfraTier(t model.RuleTier) bool { return t == model.TierFleet }

func findPolicy(st model.State, id string) (model.RoutePolicy, bool) {
	for _, pl := range st.RoutePolicies {
		if pl.ID == id {
			return pl, true
		}
	}
	return model.RoutePolicy{}, false
}

func (p *Panel) handleUpsertDomain(w http.ResponseWriter, r *http.Request) {
	var d model.Domain
	if !decode(w, r, &d) {
		return
	}
	// Domains attach to nodes, which are shared infra: any admin may manage any
	// domain, operators none. The route is infraOnly-wrapped; this is defense in
	// depth.
	if acct := p.account(r); !acct.CanManageInfra() {
		writeErr(w, http.StatusForbidden, "domain management is available to admins only")
		return
	}
	p.upsert(w, r, func(ctx context.Context) error { return p.store.UpsertDomain(ctx, d) }, d.ID)
}

func (p *Panel) handleDeleteDomain(w http.ResponseWriter, r *http.Request) {
	if acct := p.account(r); !acct.CanManageInfra() {
		writeErr(w, http.StatusForbidden, "domain management is available to admins only")
		return
	}
	p.del(w, r, func(ctx context.Context, id string) error { return p.store.DeleteDomain(ctx, id) })
}

// ---- ownership helpers (operator namespace) ----

// resolveOwner authorizes a write to a resource currently owned by curOwner
// (existed=false for creates) and returns the owner_id to stamp on the row.
// Scoped accounts (co-owner admins and operators) may only touch their own
// namespace and always stamp their namespace; the bootstrap owner may touch
// anything and keeps the existing owner (empty = infra namespace on create).
// ok=false means the caller must reject 403.
func (p *Panel) resolveOwner(r *http.Request, curOwner string, existed bool) (acct model.AdminAccount, owner string, ok bool) {
	acct = p.account(r)
	if acct.IsBootstrapOwner() {
		if existed {
			return acct, curOwner, true
		}
		return acct, "", true
	}
	if existed && curOwner != acct.Namespace() {
		return acct, "", false
	}
	return acct, acct.Namespace(), true
}

// operatorOwnsGroup verifies a scoped account only attaches users/policies to a
// group in its own namespace (the bootstrap owner may use any group). Returns
// false to 403.
func operatorOwnsGroup(st model.State, acct model.AdminAccount, groupID string) bool {
	if acct.IsBootstrapOwner() || groupID == "" {
		return true
	}
	for _, g := range st.Groups {
		if g.ID == groupID {
			return g.OwnerID == acct.Namespace()
		}
	}
	return false // unknown group: deny for a non-infra account
}

// ---- shared helpers ----

// friendlyErr maps a raw Postgres driver error to a safe, human-readable message,
// so constraint names, table shapes, and SQLSTATE codes never leak to API clients
// A unique-username collision is deliberately generic ("username already
// taken") so a scoped caller can't use the raw 23505 as an oracle to enumerate
// another namespace's client usernames. Non-driver errors (model validation)
// pass through unchanged; those messages are meant for the operator.
// formRefusals are the validation failures an operator can actually produce
// from a form. Their text is written to be read, and to be a key the panel's
// dictionary can reach, unlike the wrapping error, which names the record by
// id for whoever called the API.
var formRefusals = []error{
	model.ErrUsernameRequired, model.ErrUsernameSpaces, model.ErrUsernameControl,
	model.ErrClientNeedsGroup, model.ErrGroupNeedsName, model.ErrRuleNeedsMatch,
	model.ErrLimitsNegative, model.ErrBillingTerm,
}

func friendlyErr(err error) string {
	for _, r := range formRefusals {
		if errors.Is(err, r) {
			return r.Error()
		}
	}
	var pg *pgconn.PgError
	if !errors.As(err, &pg) {
		return err.Error()
	}
	switch pg.Code {
	case "23505": // unique_violation
		if strings.Contains(pg.ConstraintName, "username") {
			return "that username is already taken"
		}
		return "that id is already in use"
	case "23503", "23001": // foreign_key_violation / restrict_violation (ON DELETE RESTRICT)
		if strings.Contains(pg.ConstraintName, "group") || strings.Contains(pg.ConstraintName, "users") {
			return "this group still has clients — reassign or remove them first"
		}
		return "this item is still referenced by others — remove or reassign them first"
	case "23502": // not_null_violation
		return "a required field is missing"
	case "23514": // check_violation
		return "a value is invalid"
	default:
		return "the change could not be saved"
	}
}

func (p *Panel) upsert(w http.ResponseWriter, r *http.Request, fn func(context.Context) error, id string) {
	if err := fn(r.Context()); err != nil {
		writeErr(w, http.StatusBadRequest, friendlyErr(err))
		return
	}
	actor, _, _ := p.currentUser(r)
	p.recordEvent(r.Context(), model.EventAdmin, model.SeverityInfo,
		journal.Line("saved %s %s", journal.Term(entityLabel(r)), p.entityRef(r, id)), actor, p.account(r).Namespace())
	p.markDirty()
	writeJSON(w, http.StatusOK, map[string]string{"id": id, "status": "saved"})
}

func (p *Panel) del(w http.ResponseWriter, r *http.Request, fn func(context.Context, string) error) {
	id := r.PathValue("id")
	// Read the name before the row is gone: "deleted user u-9f2" is a line
	// nobody can match to anyone afterwards.
	ref := p.entityRef(r, id)
	if err := fn(r.Context(), id); err != nil {
		writeErr(w, http.StatusBadRequest, friendlyErr(err))
		return
	}
	actor, _, _ := p.currentUser(r)
	p.recordEvent(r.Context(), model.EventAdmin, model.SeverityWarn,
		journal.Line("deleted %s %s", journal.Term(entityLabel(r)), ref), actor, p.account(r).Namespace())
	p.markDirty()
	writeJSON(w, http.StatusOK, map[string]string{"id": id, "status": "deleted"})
}

// entityRef names the record a request is about the way the journal needs it:
// the name someone gave it and the id it keeps. Both, because a person
// recognises the name and only the id survives a rename.
func (p *Panel) entityRef(r *http.Request, id string) journal.Msg {
	st, err := p.store.LoadState(r.Context())
	if err != nil {
		return journal.Text(id)
	}
	name := ""
	switch entityLabel(r) {
	case "node":
		if n, ok := st.NodeByID(id); ok {
			name = n.Name
		}
	case "group":
		for _, g := range st.Groups {
			if g.ID == id {
				name = g.Name
			}
		}
	case "user":
		for _, u := range st.Users {
			if u.ID == id {
				name = userLabel(u)
			}
		}
	case "route policy":
		for _, pol := range st.RoutePolicies {
			if pol.ID == id {
				name = pol.Name
			}
		}
	case "domain":
		for _, d := range st.Domains {
			if d.ID == id {
				name = d.Hostname
			}
		}
	}
	if strings.TrimSpace(name) == "" || name == id {
		return journal.Text(id)
	}
	return journal.Text(name + " (" + id + ")")
}

// entityLabel derives a singular entity name from the API path (/api/nodes ->
// "node") for the event-log message.
func entityLabel(r *http.Request) string {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 2 {
		return "item"
	}
	switch parts[1] {
	case "nodes":
		return "node"
	case "users":
		return "user"
	case "groups":
		return "group"
	case "route-policies":
		return "route policy"
	case "domains":
		return "domain"
	default:
		return strings.TrimSuffix(parts[1], "s")
	}
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeErr(w, http.StatusBadRequest, "decode body: "+err.Error())
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
