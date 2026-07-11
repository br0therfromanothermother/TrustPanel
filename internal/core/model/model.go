// Package model defines the TrustPanel domain model: the single source of
// truth from which entry/exit node configs are rendered.
//
// Key invariants:
//
//   - A node's public role is entry XOR exit. "single" = entry with local
//     egress; there is no separate role for it.
//   - User.Username is the immutable join-key: it appears identically in
//     TrustTunnel credentials.toml, sing-box inbound users[], and the
//     v2ray_api stats users[].
//   - RoutePolicy is both the exclusive route and the server-side split-routing
//     guard, distinguished by Tier.
package model

import (
	"fmt"
	"net/netip"
	"strings"
	"time"
)

// PublicRole is a node's single public :443 listener role (entry XOR exit).
type PublicRole string

const (
	RoleEntry PublicRole = "entry"
	RoleExit  PublicRole = "exit"
)

func (r PublicRole) Valid() bool { return r == RoleEntry || r == RoleExit }

// PGRole is the node's Postgres role under HA.
type PGRole string

const (
	PGNone    PGRole = "none"
	PGPrimary PGRole = "primary"
	PGReplica PGRole = "replica"
)

func (r PGRole) Valid() bool { return r == PGNone || r == PGPrimary || r == PGReplica }

// NodeHealth is the last reported health of a node (from agent /v1/status).
type NodeHealth string

const (
	HealthHealthy  NodeHealth = "healthy"
	HealthDegraded NodeHealth = "degraded"
	HealthUnknown  NodeHealth = "unknown"
)

// DialIn describes how entry nodes reach an exit node's data-plane inbound.
// Only exit nodes carry it. The protocol is VLESS+Reality on :443: Reality
// borrows a third-party TargetSNI so the inter-node link looks like ordinary
// CDN origin-pull.
//
// Secret split: PrivKey and UUID are rendered into node configs, so they live
// in Postgres as business data; only process secrets (controller mTLS key,
// bot token) are provisioned out-of-band.
type DialIn struct {
	Proto     string `json:"proto"`      // "vless-reality"
	Port      int    `json:"port"`       // 443
	UUID      string `json:"uuid"`       // vless user id entries authenticate with
	TargetSNI string `json:"target_sni"` // borrowed third-party domain (operator-set, validated)
	PublicKey string `json:"public_key"` // reality public key -> entry outbound
	PrivKey   string `json:"priv_key"`   // reality private key -> exit inbound only
	ShortID   string `json:"short_id"`   // reality short_id
}

const DialInProtoVLESSReality = "vless-reality"

// Node is a server in the fleet. mgmt_capable marks an exit that may host the
// control plane (panel + Postgres-primary + bot).
type Node struct {
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	PublicRole  PublicRole   `json:"public_role"`
	MgmtCapable bool         `json:"mgmt_capable"`
	PublicIPs   []string     `json:"public_ips"`
	AgentAddr   string       `json:"agent_addr"` // host:port for the mTLS control channel
	Health      NodeHealth   `json:"health"`
	LastSeenAt  *time.Time   `json:"last_seen_at,omitempty"`
	PGRole      PGRole       `json:"pg_role"`
	DialIn      *DialIn      `json:"dial_in,omitempty"` // exit nodes only
	Limits      *NodeLimits  `json:"limits,omitempty"`  // optional VPS plan caps, for monitoring
	Billing     *NodeBilling `json:"billing,omitempty"` // optional payment tracking
	// Maintenance drains the node: it stays provisioned (cert/agent/reconcile keep
	// running) but is taken out of rotation. A draining exit no longer receives
	// routed traffic (dependent groups/policies fall back to local egress); a
	// draining entry is flagged out of client issuance. Owned by SetNodeMaintenance.
	Maintenance bool `json:"maintenance,omitempty"`
	// ManagedServices is the set of systemd units this node's agent allowlists
	// (matches its --services flag). The controller derives each unit's desired
	// Want from the node's role: a unit in this set that the current role does not
	// need is pushed WantStopped. This is what lets a single->exit flip stop the
	// (no-longer-needed) trusttunnel unit, while a born exit (empty here) is left
	// untouched. Empty/nil means "default to exactly the role's running set".
	ManagedServices []string `json:"managed_services,omitempty"`
	// OwnerID is the account that owns this node's lifecycle (provision/drain/
	// decommission). Empty = admin/infra namespace. HA membership stays admin-only
	// regardless of owner (operator nodes can't be standbys).
	OwnerID   string    `json:"owner_id,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// NodeBilling tracks a node's VPS payment: paid through PaidUntil, on a recurring
// TermMonths plan (1/3/6/12). Informational — surfaced on the dashboard.
type NodeBilling struct {
	PaidUntil  *time.Time `json:"paid_until,omitempty"`
	TermMonths int        `json:"term_months,omitempty"` // 1, 3, 6, or 12
}

var validTerms = map[int]bool{0: true, 1: true, 3: true, 6: true, 12: true}

func (b NodeBilling) Validate() error {
	if !validTerms[b.TermMonths] {
		return fmt.Errorf("billing term_months must be one of 1, 3, 6, 12")
	}
	return nil
}

// NodeLimits are the operator-entered VPS plan capacities for a node. They are
// informational (monitoring/dashboard reference), not enforced. Zero means
// "unset" for that dimension.
type NodeLimits struct {
	CPUCores  float64 `json:"cpu_cores,omitempty"`  // vCPU count
	MemoryMB  int64   `json:"memory_mb,omitempty"`  // RAM, MiB
	DiskGB    int64   `json:"disk_gb,omitempty"`    // disk, GiB
	TrafficGB int64   `json:"traffic_gb,omitempty"` // monthly traffic, GiB
}

func (l NodeLimits) Validate() error {
	if l.CPUCores < 0 || l.MemoryMB < 0 || l.DiskGB < 0 || l.TrafficGB < 0 {
		return fmt.Errorf("node limits must be non-negative")
	}
	return nil
}

func (n Node) IsEntry() bool { return n.PublicRole == RoleEntry }
func (n Node) IsExit() bool  { return n.PublicRole == RoleExit }

func (n Node) Validate() error {
	if strings.TrimSpace(n.ID) == "" {
		return fmt.Errorf("node id is required")
	}
	if !n.PublicRole.Valid() {
		return fmt.Errorf("node %q: invalid public_role %q", n.ID, n.PublicRole)
	}
	if n.PGRole != "" && !n.PGRole.Valid() {
		return fmt.Errorf("node %q: invalid pg_role %q", n.ID, n.PGRole)
	}
	switch n.PublicRole {
	case RoleExit:
		if n.DialIn == nil {
			return fmt.Errorf("exit node %q: dial_in is required", n.ID)
		}
		if err := n.DialIn.validate(n.ID); err != nil {
			return err
		}
	case RoleEntry:
		if n.DialIn != nil {
			return fmt.Errorf("entry node %q: dial_in must be empty (entries do not accept node-to-node inbound)", n.ID)
		}
	}
	if n.Limits != nil {
		if err := n.Limits.Validate(); err != nil {
			return fmt.Errorf("node %q: %w", n.ID, err)
		}
	}
	if n.Billing != nil {
		if err := n.Billing.Validate(); err != nil {
			return fmt.Errorf("node %q: %w", n.ID, err)
		}
	}
	return nil
}

func (d DialIn) validate(nodeID string) error {
	if d.Proto != DialInProtoVLESSReality {
		return fmt.Errorf("exit node %q: unsupported dial_in proto %q", nodeID, d.Proto)
	}
	if d.Port <= 0 || d.Port > 65535 {
		return fmt.Errorf("exit node %q: invalid dial_in port %d", nodeID, d.Port)
	}
	if strings.TrimSpace(d.UUID) == "" {
		return fmt.Errorf("exit node %q: dial_in uuid is required", nodeID)
	}
	if strings.TrimSpace(d.TargetSNI) == "" {
		return fmt.Errorf("exit node %q: reality target_sni is required", nodeID)
	}
	return nil
}

// Group is the unit of routing policy (per-group enforcement). A user belongs
// to exactly one group in v1 (User -> Group many-to-one). DefaultExitID empty
// means the group egresses locally from the entry (direct).
type Group struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	DefaultExitID string    `json:"default_exit_id,omitempty"`
	OwnerID       string    `json:"owner_id,omitempty"` // empty = admin namespace
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func (g Group) Validate() error {
	if strings.TrimSpace(g.ID) == "" {
		return fmt.Errorf("group id is required")
	}
	if strings.TrimSpace(g.Name) == "" {
		return fmt.Errorf("group %q: name is required", g.ID)
	}
	return nil
}

// User is a VPN client identity. Username is the immutable join-key (see package
// doc). The quota fields are reserved for v2 and are not enforced in v1
// (scope = personal; expiry is enforced by re-render).
type User struct {
	ID          string     `json:"id"`
	Username    string     `json:"username"`
	Secret      string     `json:"-"` // password; never serialized
	DisplayName string     `json:"display_name"`
	Enabled     bool       `json:"enabled"`
	GroupID     string     `json:"group_id"`
	OwnerID     string     `json:"owner_id,omitempty"` // empty = admin namespace
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	// ExpiryAlertedFor is the ExpiresAt value the owner was last warned about, so
	// the expiry-alert loop fires once per expiry date and re-arms when a config is
	// extended. Written only by the alert loop; never serialized to clients.
	ExpiryAlertedFor *time.Time `json:"-"`

	// v2-reserved: schema only, no enforcement in v1.
	DataLimit   int64      `json:"data_limit"`   // bytes; 0 = unlimited
	UsedTraffic int64      `json:"used_traffic"` // bytes; accumulated running total
	ResetPeriod string     `json:"reset_period,omitempty"`
	UsedResetAt *time.Time `json:"used_reset_at,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (u User) Validate() error {
	if strings.TrimSpace(u.ID) == "" {
		return fmt.Errorf("user id is required")
	}
	// '/' (and '\\') in an id break the plain REST path — DELETE /api/users/a/b is a
	// 405 (router split) and only a URL-encoded id works. Reject them at create so an
	// id is always addressable via its plain path.
	if strings.ContainsAny(u.ID, `/\`) {
		return fmt.Errorf("user id %q must not contain '/' or '\\'", u.ID)
	}
	if err := ValidateUsername(u.Username); err != nil {
		return fmt.Errorf("user %q: %w", u.ID, err)
	}
	if strings.TrimSpace(u.GroupID) == "" {
		return fmt.Errorf("user %q: group_id is required", u.ID)
	}
	return nil
}

// ValidateUsername enforces the join-key constraints. The username is used
// verbatim as a SOCKS5/credentials username and as a sing-box route auth_user
// and stats key, so it must be non-empty and free of control/space characters.
func ValidateUsername(username string) error {
	if strings.TrimSpace(username) == "" {
		return fmt.Errorf("username is required")
	}
	if username != strings.TrimSpace(username) {
		return fmt.Errorf("username %q must not have leading/trailing spaces", username)
	}
	for _, r := range username {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("username %q contains a control character", username)
		}
	}
	return nil
}

// Expired reports whether the user is past expiry at the given time. The panel
// excludes expired users from rendered configs (expiry enforcement, B-scope).
func (u User) Expired(at time.Time) bool {
	return u.ExpiresAt != nil && !at.Before(*u.ExpiresAt)
}

// Active reports whether the user should appear in rendered node configs.
func (u User) Active(at time.Time) bool {
	return u.Enabled && !u.Expired(at)
}

// RuleTier orders policies (sing-box first-match top-down). Fleet mandates
// compile above everything: they are the fleet owner's exclusive routes that no
// namespace can override. Guard rules are server-side split-routing safety nets
// and compile above exit rules. Exit rules are the per-namespace exclusive routes.
type RuleTier string

const (
	TierFleet RuleTier = "fleet"
	TierGuard RuleTier = "guard"
	TierExit  RuleTier = "exit"
)

func (t RuleTier) Valid() bool { return t == TierFleet || t == TierGuard || t == TierExit }

// RuleAction is the outbound a matched policy selects.
type RuleAction string

const (
	ActionExit   RuleAction = "exit"
	ActionDirect RuleAction = "direct"
	ActionBlock  RuleAction = "block"
)

func (a RuleAction) Valid() bool {
	return a == ActionExit || a == ActionDirect || a == ActionBlock
}

// FallbackKind is what a policy resolves to when its exit is unavailable. It is
// resolved at compile time against fleet state (block, local direct, or another
// exit).
type FallbackKind string

const (
	FallbackBlock  FallbackKind = "block"
	FallbackDirect FallbackKind = "direct"
	FallbackExit   FallbackKind = "exit"
)

func (f FallbackKind) Valid() bool {
	return f == FallbackBlock || f == FallbackDirect || f == FallbackExit
}

// RoutePolicy is one server-side routing rule. Fleet-tier rules are the fleet
// owner's mandates that override every namespace (highest precedence, may route
// to an exit). Guard-tier rules force matched traffic out of the entry directly
// (the split-routing safety net); exit-tier rules implement per-namespace
// exclusive routes. AppliesToGroupID empty means all users.
type RoutePolicy struct {
	ID               string       `json:"id"`
	Name             string       `json:"name"`
	Priority         int          `json:"priority"`
	Tier             RuleTier     `json:"tier"`
	AppliesToGroupID string       `json:"applies_to_group_id,omitempty"`
	MatchDomains     []string     `json:"match_domains,omitempty"`
	MatchCIDRs       []string     `json:"match_cidrs,omitempty"`
	MatchGeoIP       []string     `json:"match_geoip,omitempty"`
	MatchGeoSite     []string     `json:"match_geosite,omitempty"`
	ExcludeDomains   []string     `json:"exclude_domains,omitempty"` // domains that bypass this policy (take the group's normal path)
	Action           RuleAction   `json:"action"`
	ExitNodeID       string       `json:"exit_node_id,omitempty"`     // when Action == exit
	FallbackKind     FallbackKind `json:"fallback_kind,omitempty"`    // when Action == exit
	FallbackExitID   string       `json:"fallback_exit_id,omitempty"` // when FallbackKind == exit
	Disabled         bool         `json:"disabled,omitempty"`         // true = rule is kept but not applied/evaluated
	OwnerID          string       `json:"owner_id,omitempty"`         // empty = admin namespace; fleet- and guard-tier are always infra-owned
	CreatedAt        time.Time    `json:"created_at"`
	UpdatedAt        time.Time    `json:"updated_at"`
}

func (p RoutePolicy) HasMatch() bool {
	return len(p.MatchDomains)+len(p.MatchCIDRs)+len(p.MatchGeoIP)+len(p.MatchGeoSite) > 0
}

func (p RoutePolicy) Validate() error {
	if strings.TrimSpace(p.ID) == "" {
		return fmt.Errorf("route policy id is required")
	}
	if !p.Tier.Valid() {
		return fmt.Errorf("route policy %q: invalid tier %q", p.ID, p.Tier)
	}
	if !p.Action.Valid() {
		return fmt.Errorf("route policy %q: invalid action %q", p.ID, p.Action)
	}
	if !p.HasMatch() {
		return fmt.Errorf("route policy %q: at least one match target is required", p.ID)
	}
	for _, cidr := range p.MatchCIDRs {
		if _, err := netip.ParsePrefix(strings.TrimSpace(cidr)); err != nil {
			if _, aerr := netip.ParseAddr(strings.TrimSpace(cidr)); aerr != nil {
				return fmt.Errorf("route policy %q: invalid cidr/ip %q", p.ID, cidr)
			}
		}
	}
	if p.Action == ActionExit {
		if strings.TrimSpace(p.ExitNodeID) == "" {
			return fmt.Errorf("route policy %q: exit action requires exit_node_id", p.ID)
		}
		if p.FallbackKind != "" && !p.FallbackKind.Valid() {
			return fmt.Errorf("route policy %q: invalid fallback_kind %q", p.ID, p.FallbackKind)
		}
		if p.FallbackKind == FallbackExit && strings.TrimSpace(p.FallbackExitID) == "" {
			return fmt.Errorf("route policy %q: fallback_kind=exit requires fallback_exit_id", p.ID)
		}
	}
	if p.Tier == TierGuard && p.Action == ActionExit {
		return fmt.Errorf("route policy %q: guard-tier rules cannot route to an exit (they keep traffic on the entry)", p.ID)
	}
	return nil
}

// ControlPlane is the fleet singleton tracking control-plane leadership. Epoch is
// the fencing generation: agents reject a controller presenting epoch lower
// than the highest they have accepted.
type ControlPlane struct {
	ActiveNodeID   string    `json:"active_node_id"`
	Epoch          int64     `json:"epoch"`
	StandbyNodeIDs []string  `json:"standby_node_ids"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// DomainPurpose distinguishes TLS host bindings (these are NOT routing match
// domains; see RoutePolicy.MatchDomains for routing).
type DomainPurpose string

const (
	PurposeMainFallback DomainPurpose = "main-fallback" // entry TrustTunnel host + same-domain fallback
	PurposeFallbackSite DomainPurpose = "fallback-site"
)

// Domain is a TLS host binding for an entry node's TrustTunnel listener.
type Domain struct {
	ID        string        `json:"id"`
	Hostname  string        `json:"hostname"`
	Purpose   DomainPurpose `json:"purpose"`
	NodeID    string        `json:"node_id"`
	TLSStatus string        `json:"tls_status"`
	TLSIssuer string        `json:"tls_issuer"` // leaf cert issuer label (staging vs production)
	CreatedAt time.Time     `json:"created_at"`
	UpdatedAt time.Time     `json:"updated_at"`
}

// Settings is panel-managed configuration stored as a singleton jsonb row
// (settings table). It is NOT part of State: tokens never ship to the browser
// via /api/state — the Bots API masks them. The bot/alert processes read the
// real values directly from the store.
type Settings struct {
	Bot    BotSettings    `json:"bot"`    // management bot (two-way command bot)
	Alert  AlertSettings  `json:"alert"`  // one-way alert bot (billing/watchdog)
	Backup BackupSettings `json:"backup"` // off-site delivery of DR snapshots to Telegram
	Fleet  FleetSettings  `json:"fleet"`  // fleet-wide defaults inherited by new-node provisioning
	Panel  PanelSettings  `json:"panel"`  // panel runtime tunables (loop intervals, session lifetime)
}

// FleetSettings holds fleet-wide defaults the panel offers when provisioning a
// new node, so the operator does not re-type them per install. They are purely
// UI conveniences/defaults — the authoritative per-node values live on the node.
type FleetSettings struct {
	ACMEEmail  string `json:"acme_email"`  // default ACME contact for new entries (else admin@<apex>)
	RealitySNI string `json:"reality_sni"` // default borrowed-CDN SNI for new exits
	Brand      string `json:"brand"`       // display brand (camouflage site / portal)
	Apex       string `json:"apex"`        // apex domain
}

// PanelSettings are runtime tunables the panel reads live (no restart): the
// background-loop cadences and the browser session lifetime. Zero means "use the
// documented default"; the effective accessors below also clamp to safe floors so
// a typo cannot hammer the fleet.
type PanelSettings struct {
	ReconcileSeconds  int `json:"reconcile_seconds"`   // fleet reconcile cadence; <=0 = 60
	StatsSeconds      int `json:"stats_seconds"`       // per-user traffic poll cadence; <=0 = 60
	BillingAlertHours int `json:"billing_alert_hours"` // billing due-date check cadence; <=0 = 24
	BillingAlertDays  int `json:"billing_alert_days"`  // warn when payment is due within N days; <=0 = 7
	ExpiryAlertDays   int `json:"expiry_alert_days"`   // warn an owner when a client config expires within N days; <=0 = 3
	SessionHours      int `json:"session_hours"`       // browser session lifetime; <=0 = 12
	// EgressFailoverSeconds: how long an exit must stay degraded/unreachable before
	// its groups are auto-reassigned to a healthy exit; <=0 = 180, floor 60.
	EgressFailoverSeconds int `json:"egress_failover_seconds"`
	// ActivityKBPerMin: the per-minute traffic floor (KB) a client must exceed
	// within the activity window to read as "online now"; <=0 = 100. Expressed
	// per-minute so it stays meaningful independent of the window length.
	ActivityKBPerMin int `json:"activity_kb_per_min"`
}

func clampSeconds(v, def, min int) time.Duration {
	if v <= 0 {
		v = def
	}
	if v < min {
		v = min
	}
	return time.Duration(v) * time.Second
}

// ReconcileInterval is the fleet reconcile cadence (default 60s, floor 15s).
func (p PanelSettings) ReconcileInterval() time.Duration {
	return clampSeconds(p.ReconcileSeconds, 60, 15)
}

// StatsInterval is the per-user traffic poll cadence (default 60s, floor 15s).
func (p PanelSettings) StatsInterval() time.Duration { return clampSeconds(p.StatsSeconds, 60, 15) }

// BillingInterval is the billing due-date check cadence (default 24h, floor 1h).
func (p PanelSettings) BillingInterval() time.Duration {
	h := p.BillingAlertHours
	if h <= 0 {
		h = 24
	}
	if h < 1 {
		h = 1
	}
	return time.Duration(h) * time.Hour
}

// BillingDays is the "due within N days" warning window (default 7).
func (p PanelSettings) BillingDays() int {
	if p.BillingAlertDays > 0 {
		return p.BillingAlertDays
	}
	return 7
}

// ExpiryDays is the "config expires within N days" warning window (default 3).
func (p PanelSettings) ExpiryDays() int {
	if p.ExpiryAlertDays > 0 {
		return p.ExpiryAlertDays
	}
	return 3
}

// SessionTTL is the browser session lifetime (default 12h, floor 1h).
func (p PanelSettings) SessionTTL() time.Duration {
	h := p.SessionHours
	if h <= 0 {
		h = 12
	}
	if h < 1 {
		h = 1
	}
	return time.Duration(h) * time.Hour
}

// EgressFailover is how long an exit must stay degraded before its groups are
// auto-reassigned to a healthy exit (default 180s, floor 60s).
func (p PanelSettings) EgressFailover() time.Duration {
	return clampSeconds(p.EgressFailoverSeconds, 180, 60)
}

// ActivityKBMin is the per-minute traffic floor (KB) a client must exceed within
// the activity window to count as "online now" (default 100).
func (p PanelSettings) ActivityKBMin() int {
	if p.ActivityKBPerMin <= 0 {
		return 100
	}
	return p.ActivityKBPerMin
}

// ActivityFloorBytes is the total rx+tx a client must move within window to count
// as online, derived from the KB/min floor so it scales with the window length.
func (p PanelSettings) ActivityFloorBytes(window time.Duration) int64 {
	return int64(float64(p.ActivityKBMin()) * 1024 * window.Minutes())
}

// BotSettings configures the operator management bot. Who may use it is decided
// per-account by the Telegram binding (admins.telegram_id), not a flat allowlist.
type BotSettings struct {
	Enabled bool   `json:"enabled"`
	Token   string `json:"token"`
}

// AlertSettings configures the one-way alert bot (billing-due, watchdog).
type AlertSettings struct {
	Enabled bool `json:"enabled"`
	// Token is the BACKUP (β) alert bot, used by the standby's watchdog to page the
	// alert chat when the primary's dead-man heartbeat goes stale (management bot /
	// primary host down). Normal alerts always go through the management bot (α);
	// this is a redundant failover sender, not an alternative.
	Token  string `json:"token"`
	ChatID string `json:"chat_id"`
}

// BackupSettings configures off-site delivery of disaster-recovery snapshots to a
// private Telegram channel. The snapshot contains the CA key + user secrets and
// Telegram is NOT end-to-end encrypted, so the snapshot is age-encrypted before it
// leaves the box: AgeRecipient (an age `age1...` public key) is mandatory when
// Enabled, and the matching private key is held by the operator OFF the fleet.
// The bot token is reused from AlertSettings; only a dedicated ChatID is set here
// so a leaked alert token does not expose the backup channel's history any more
// than it already would. Large ciphertext is split into <=ChunkBytes parts to
// stay under the Telegram Bot API document limit.
type BackupSettings struct {
	Enabled      bool   `json:"enabled"`       // off-site Telegram delivery (local snapshot is governed by LocalEnabled)
	ChatID       string `json:"chat_id"`       // dedicated private backup channel
	AgeRecipient string `json:"age_recipient"` // age public key (age1...)
	ChunkBytes   int    `json:"chunk_bytes"`   // max bytes per part; <=0 uses the default

	// Schedule & retention for the LOCAL snapshot. These live in the (replicated)
	// settings rather than each node's systemd unit, so the panel governs both the
	// active node and the standby with one edit; the backup binary reads them on
	// each scheduled run and self-gates. Pointers default to "on" when absent so an
	// upgrade of an existing fleet (whose row predates these fields) keeps backing
	// up rather than silently going dark.
	LocalEnabled  *bool `json:"local_enabled,omitempty"` // nil = on; false disables the scheduled local backup
	IntervalHours int   `json:"interval_hours"`          // run at most every N hours; <=0 = 24
	Keep          int   `json:"keep"`                    // local snapshots retained; <=0 = 14

	// Verify-restore drill schedule (same self-gating model, keyed off its marker).
	VerifyEnabled      *bool `json:"verify_enabled,omitempty"` // nil = on; false disables the scheduled drill
	VerifyIntervalDays int   `json:"verify_interval_days"`     // run at most every N days; <=0 = 7
}

// Effective backup-policy accessors apply the documented defaults so callers
// never special-case a zero/absent value. They are used by both the backup CLI
// (to self-gate a scheduled run) and the panel view (to show real numbers).

// LocalOn reports whether the scheduled local backup should run (default on).
func (b BackupSettings) LocalOn() bool { return b.LocalEnabled == nil || *b.LocalEnabled }

// KeepOrDefault is the retention count (default 14).
func (b BackupSettings) KeepOrDefault() int {
	if b.Keep > 0 {
		return b.Keep
	}
	return 14
}

// BackupInterval is how often the local snapshot should run (default 24h).
func (b BackupSettings) BackupInterval() time.Duration {
	if b.IntervalHours > 0 {
		return time.Duration(b.IntervalHours) * time.Hour
	}
	return 24 * time.Hour
}

// VerifyOn reports whether the scheduled verify-restore drill should run (default on).
func (b BackupSettings) VerifyOn() bool { return b.VerifyEnabled == nil || *b.VerifyEnabled }

// VerifyInterval is how often the drill should run (default 7 days).
func (b BackupSettings) VerifyInterval() time.Duration {
	if b.VerifyIntervalDays > 0 {
		return time.Duration(b.VerifyIntervalDays) * 24 * time.Hour
	}
	return 7 * 24 * time.Hour
}

// State is the full control-plane state used by the render pipeline.
type State struct {
	ControlPlane  ControlPlane  `json:"control_plane"`
	Nodes         []Node        `json:"nodes"`
	Groups        []Group       `json:"groups"`
	Users         []User        `json:"users"`
	RoutePolicies []RoutePolicy `json:"route_policies"`
	Domains       []Domain      `json:"domains"`
}

// Validate runs entity-level and cross-reference validation over the state.
func (s State) Validate() error {
	nodeIDs := map[string]Node{}
	for _, n := range s.Nodes {
		if err := n.Validate(); err != nil {
			return err
		}
		if _, dup := nodeIDs[n.ID]; dup {
			return fmt.Errorf("duplicate node id %q", n.ID)
		}
		nodeIDs[n.ID] = n
	}
	groupIDs := map[string]bool{}
	for _, g := range s.Groups {
		if err := g.Validate(); err != nil {
			return err
		}
		if groupIDs[g.ID] {
			return fmt.Errorf("duplicate group id %q", g.ID)
		}
		groupIDs[g.ID] = true
		if g.DefaultExitID != "" {
			if n, ok := nodeIDs[g.DefaultExitID]; !ok || !n.IsExit() {
				return fmt.Errorf("group %q: default_exit_id %q is not an exit node", g.ID, g.DefaultExitID)
			}
		}
	}
	usernames := map[string]bool{}
	for _, u := range s.Users {
		if err := u.Validate(); err != nil {
			return err
		}
		if usernames[u.Username] {
			return fmt.Errorf("duplicate username %q", u.Username)
		}
		usernames[u.Username] = true
		if !groupIDs[u.GroupID] {
			return fmt.Errorf("user %q: group_id %q does not exist", u.ID, u.GroupID)
		}
	}
	for _, p := range s.RoutePolicies {
		if err := p.Validate(); err != nil {
			return err
		}
		if p.AppliesToGroupID != "" && !groupIDs[p.AppliesToGroupID] {
			return fmt.Errorf("route policy %q: applies_to_group_id %q does not exist", p.ID, p.AppliesToGroupID)
		}
		if p.Action == ActionExit {
			if n, ok := nodeIDs[p.ExitNodeID]; !ok || !n.IsExit() {
				return fmt.Errorf("route policy %q: exit_node_id %q is not an exit node", p.ID, p.ExitNodeID)
			}
			if p.FallbackKind == FallbackExit {
				if n, ok := nodeIDs[p.FallbackExitID]; !ok || !n.IsExit() {
					return fmt.Errorf("route policy %q: fallback_exit_id %q is not an exit node", p.ID, p.FallbackExitID)
				}
			}
		}
	}
	return nil
}

// NodeByID returns the node with the given id.
func (s State) NodeByID(id string) (Node, bool) {
	for _, n := range s.Nodes {
		if n.ID == id {
			return n, true
		}
	}
	return Node{}, false
}

// DomainByID returns the domain with the given id, if present.
func (s State) DomainByID(id string) (Domain, bool) {
	for _, d := range s.Domains {
		if d.ID == id {
			return d, true
		}
	}
	return Domain{}, false
}

// ActiveUsers returns enabled, non-expired users as of at, sorted by username
// for deterministic rendering.
func (s State) ActiveUsers(at time.Time) []User {
	var out []User
	for _, u := range s.Users {
		if u.Active(at) {
			out = append(out, u)
		}
	}
	sortUsersByUsername(out)
	return out
}

func sortUsersByUsername(users []User) {
	for i := 1; i < len(users); i++ {
		for j := i; j > 0 && users[j-1].Username > users[j].Username; j-- {
			users[j-1], users[j] = users[j], users[j-1]
		}
	}
}
