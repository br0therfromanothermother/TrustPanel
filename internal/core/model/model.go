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
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"time"
)

// The refusals a person reads in a form, as sentinels and not as text.
//
// Validation messages are written for whoever called the API: they name the
// record by id and the field by its JSON name, which a script needs and
// exactly what an operator staring at a form does not. The panel recognises
// these with errors.Is and shows the sentence below instead, in the language the
// panel is being read in; see friendlyErr. The wrapping error keeps the id, so
// nothing is lost for an API caller or the log.
//
// Only refusals a form can actually produce belong here. The rest of validation
// is the last line of defence behind a form that already checked, and an
// operator will never see it.
var (
	ErrUsernameRequired = errors.New("the username cannot be empty")
	ErrUsernameSpaces   = errors.New("the username cannot start or end with a space")
	ErrUsernameControl  = errors.New("the username has a character in it that is not allowed")
	ErrUsernameTooLong  = errors.New("the username is too long")
	ErrClientNeedsGroup = errors.New("a client has to belong to a group")
	ErrGroupNeedsName   = errors.New("a group has to have a name")
	ErrRuleNeedsMatch   = errors.New("a rule has to say what traffic it catches")
	ErrLimitsNegative   = errors.New("the figures of a plan cannot be negative")
	ErrBillingTerm      = errors.New("the payment term has to be 1, 3, 6 or 12 months")
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
// Secret split: PrivKey and UUID are rendered into node configs. They live
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
	// need is pushed WantStopped, which lets a single->exit flip stop the
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
// TermMonths plan (1/3/6/12). Informational, surfaced on the dashboard.
type NodeBilling struct {
	PaidUntil  *time.Time `json:"paid_until,omitempty"`
	TermMonths int        `json:"term_months,omitempty"` // 1, 3, 6, or 12
}

var validTerms = map[int]bool{0: true, 1: true, 3: true, 6: true, 12: true}

func (b NodeBilling) Validate() error {
	if !validTerms[b.TermMonths] {
		return fmt.Errorf("billing term_months must be one of 1, 3, 6, 12: %w", ErrBillingTerm)
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
		return fmt.Errorf("node limits must be non-negative: %w", ErrLimitsNegative)
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

// ExitDirect is the sentinel DefaultExitID that pins a group to local egress out
// of the entry itself, even when exit nodes exist. It is distinct from an empty
// DefaultExitID, which means "unset": a group created while exits exist inherits
// the deployment's default exit, and only an exit-less deployment leaves it on
// local egress.
const ExitDirect = "direct"

// Group is the unit of routing policy (per-group enforcement). A user belongs
// to exactly one group in v1 (User -> Group many-to-one). DefaultExitID is an
// exit node id, the ExitDirect sentinel for deliberate local egress, or empty
// (unset, resolved to the deployment default at group creation).
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
		return fmt.Errorf("group %q: name is required: %w", g.ID, ErrGroupNeedsName)
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
	// '/' (and '\\') in an id break the plain REST path: DELETE /api/users/a/b is a
	// 405 (router split) and only a URL-encoded id works. Reject them at create so an
	// id is always addressable via its plain path.
	if strings.ContainsAny(u.ID, `/\`) {
		return fmt.Errorf("user id %q must not contain '/' or '\\'", u.ID)
	}
	if err := ValidateUsername(u.Username); err != nil {
		return fmt.Errorf("user %q: %w", u.ID, err)
	}
	if strings.TrimSpace(u.GroupID) == "" {
		return fmt.Errorf("user %q: group_id is required: %w", u.ID, ErrClientNeedsGroup)
	}
	return nil
}

// ValidateUsername enforces the join-key constraints. The username is used
// verbatim as the client's SOCKS5 credential on the entry node's internal hop,
// as a sing-box auth_user value and as the traffic-stats key. It must be
// non-empty, bounded, and free of control characters. A space inside it is
// allowed, since RFC1929 carries one, but not at either end, where it is
// invisible and would not match anything.
func ValidateUsername(username string) error {
	if strings.TrimSpace(username) == "" {
		return fmt.Errorf("username is required: %w", ErrUsernameRequired)
	}
	if username != strings.TrimSpace(username) {
		return fmt.Errorf("username %q must not have leading/trailing spaces: %w", username, ErrUsernameSpaces)
	}
	// A username is a credential: it is written into the client's SOCKS5
	// credentials, into the node's auth_user list and into the traffic-stats key,
	// and it is carried around by every surface that shows a client. Nothing
	// downstream benefits from one longer than a name, and an unbounded field
	// would let a client be created that no interface could then render: the bot's
	// buttons build a Telegram callback out of it, and Telegram rejects the whole
	// message past 64 bytes and not the one button.
	if len(username) > MaxUsernameLen {
		return fmt.Errorf("username is %d characters, the limit is %d: %w", len(username), MaxUsernameLen, ErrUsernameTooLong)
	}
	for _, r := range username {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("username %q contains a control character: %w", username, ErrUsernameControl)
		}
	}
	return nil
}

// MaxUsernameLen is the cap, in bytes, which is the limit the database
// column and the wire formats see.
const MaxUsernameLen = 64

// Expired reports whether the user is past expiry at the given time. The panel
// excludes expired users from rendered configs (expiry enforcement, B-scope).
func (u User) Expired(at time.Time) bool {
	return u.ExpiresAt != nil && !at.Before(*u.ExpiresAt)
}

// Active reports whether the user should appear in rendered node configs.
func (u User) Active(at time.Time) bool {
	return u.Enabled && !u.Expired(at)
}

// RuleTier is the level a policy sits at (sing-box first-match top-down). Fleet
// rules are the whole network's, written by the owner of it, and compile above
// everything: no namespace can override one. Exit rules are a namespace's own.
//
// A network-wide rule that keeps traffic on the entry instead of sending it to
// an exit is not a third level: that is the action, direct or block. Storing it
// as a level too would mean a question nobody can answer without first knowing
// the action, and two values free to contradict each other.
type RuleTier string

const (
	TierFleet RuleTier = "fleet"
	TierExit  RuleTier = "exit"
)

func (t RuleTier) Valid() bool { return t == TierFleet || t == TierExit }

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

// FallbackKind is where a policy resolves to when its exit is unavailable. It is
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
	// Conditions is the rule's match, read left to right. The four Match* fields
	// above are what it replaced; they are still written so a rolled-back binary
	// can read the rule, and are only read when Conditions is empty.
	Conditions []RouteCondition `json:"conditions,omitempty"`
	// OthersAction reserves the match for AppliesToGroupID and says what the same
	// traffic does for everyone else: another exit, out without the tunnel, or
	// refused. Empty leaves them to the rules below, where a broader rule (a
	// category that happens to list the same domain) decides for them.
	//
	// A rule that answers for everyone is checked before the ordinary rules of
	// its tier; see policiesByTier.
	OthersAction RuleAction `json:"others_action,omitempty"`
	OthersExitID string     `json:"others_exit_id,omitempty"` // when OthersAction == exit
	Disabled     bool       `json:"disabled,omitempty"`       // true = rule is kept but not applied/evaluated
	OwnerID      string     `json:"owner_id,omitempty"`       // empty = admin namespace; fleet- and guard-tier are always infra-owned
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

// RuleSetKind names one of the two geo databases routing draws on: country
// membership by IP, and domain category. They are configured separately because
// they are separate upstream projects.
const (
	RuleSetGeoIP   = "geoip"
	RuleSetGeoSite = "geosite"
)

// RuleSetSource is where one kind's .srs files are fetched from. A tag resolves
// to <URL><tag>.srs, which is the whole contract a source has to satisfy.
//
// Catalog is the list of category names the source offered at CatalogAt, and
// CatalogPrev the list before it. Keeping the previous generation makes
// "12 categories appeared, 3 went away" answerable at all: nothing upstream
// publishes a changelog: the only record of a rename is our own last look.
type RuleSetSource struct {
	Kind        string     `json:"kind"`
	Preset      string     `json:"preset"`
	URL         string     `json:"url"`
	CheckedAt   *time.Time `json:"checked_at,omitempty"`
	CheckError  string     `json:"check_error,omitempty"`
	Catalog     []string   `json:"catalog,omitempty"`
	CatalogPrev []string   `json:"catalog_prev,omitempty"`
	CatalogAt   *time.Time `json:"catalog_at,omitempty"`
}

// RuleSet is one cached .srs and what the last check found at its source.
// ContentID and UpstreamID are opaque identities of the bytes: equal means the
// local copy is already what the source has, and no comparison of sizes or dates
// is needed, or trustworthy: an upstream rebuild changes mtime without changing
// a single domain.
type RuleSet struct {
	Tag          string     `json:"tag"`
	Kind         string     `json:"kind"`
	Size         int64      `json:"size"`
	ContentID    string     `json:"content_id,omitempty"`
	FetchedAt    *time.Time `json:"fetched_at,omitempty"`
	UpstreamID   string     `json:"upstream_id,omitempty"`
	UpstreamSize int64      `json:"upstream_size,omitempty"`
	CheckedAt    *time.Time `json:"checked_at,omitempty"`
	UpstreamGone bool       `json:"upstream_gone,omitempty"`
	AutoUpdate   bool       `json:"auto_update,omitempty"`
	// The copy an update replaced, kept so it can be put back. Empty means there
	// is nothing to go back to: this set has never been updated here.
	PrevSize      int64      `json:"prev_size,omitempty"`
	PrevContentID string     `json:"prev_content_id,omitempty"`
	PrevFetchedAt *time.Time `json:"prev_fetched_at,omitempty"`
}

// CanRollback reports whether a previous copy is on hand.
func (r RuleSet) CanRollback() bool { return r.PrevContentID != "" }

// Stale reports whether the source holds different bytes than the local copy.
// An unchecked set (no upstream id yet) is not stale: "we have not looked" and
// "we looked and it differs" must not render as the same thing.
func (r RuleSet) Stale() bool {
	return r.UpstreamID != "" && r.ContentID != "" && r.UpstreamID != r.ContentID
}

// Condition kinds and joins. A rule is a list of conditions read strictly left
// to right, with no precedence: "A and B or C" is "(A and B) or C". Precedence
// is the part of a boolean expression that a form cannot show, and hiding it
// would mislead exactly the people a builder like this is for.
const (
	CondDomain  = "domain"
	CondGeoIP   = "geoip"
	CondGeoSite = "geosite"
	CondCIDR    = "cidr"

	JoinAnd    = "and"
	JoinOr     = "or"
	JoinExcept = "except"
)

// RouteCondition is one line of a rule's match: what to look at, which values,
// and how it joins to everything above it. The first condition's Join is
// ignored, as there is nothing above it to join to.
type RouteCondition struct {
	Join   string   `json:"join,omitempty"`
	Kind   string   `json:"kind"`
	Values []string `json:"values"`
}

func (c RouteCondition) Validate(first bool) error {
	switch c.Kind {
	case CondDomain, CondGeoIP, CondGeoSite, CondCIDR:
	default:
		return fmt.Errorf("unknown condition kind %q", c.Kind)
	}
	if len(cleanValues(c.Values)) == 0 {
		return fmt.Errorf("condition %q has no values", c.Kind)
	}
	if first {
		return nil
	}
	switch c.Join {
	case JoinAnd, JoinOr, JoinExcept:
		return nil
	}
	return fmt.Errorf("unknown join %q", c.Join)
}

// cleanValues drops blanks and trims, leaving a stray comma in the form unable to
// become a condition value that matches nothing and hides why.
func cleanValues(in []string) []string {
	out := make([]string, 0, len(in))
	for _, v := range in {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// Match returns the rule's conditions, deriving them from the older per-kind
// fields when a rule predates them. Every reader goes through here; one rule
// cannot be evaluated one way by the renderer and another by the tester.
func (p RoutePolicy) Match() []RouteCondition {
	if len(p.Conditions) > 0 {
		return p.Conditions
	}
	return LegacyConditions(p)
}

// LegacyConditions expresses the older four-field shape as conditions:
//
//	(geoip or geosite) and domains and cidrs, but not excluded domains
//
// which is how that form read: four fields that narrow each other,
// with the two geo fields as alternatives because both compile into rule_set and
// different rule-sets are alternatives to each other.
//
// A flat sing-box rule lists its address matchers as alternatives. Domains and
// cidrs set together would come out as either/or. The renderer emits the
// intersection the form promises instead; reading a stored rule as the sentence
// it was described by keeps it honest,
// and an operator who wanted the alternatives can now write them as such.
func LegacyConditions(p RoutePolicy) []RouteCondition {
	var out []RouteCondition
	add := func(join, kind string, vals []string) {
		v := cleanValues(vals)
		if len(v) == 0 {
			return
		}
		if len(out) == 0 {
			join = JoinAnd // the first condition's join is ignored
		}
		out = append(out, RouteCondition{Join: join, Kind: kind, Values: v})
	}
	add(JoinAnd, CondGeoIP, p.MatchGeoIP)
	add(JoinOr, CondGeoSite, p.MatchGeoSite)
	add(JoinAnd, CondDomain, p.MatchDomains)
	add(JoinAnd, CondCIDR, p.MatchCIDRs)
	if v := cleanValues(p.ExcludeDomains); len(v) > 0 && len(out) > 0 {
		out = append(out, RouteCondition{Join: JoinExcept, Kind: CondDomain, Values: v})
	}
	return out
}

func (p RoutePolicy) HasMatch() bool { return len(p.Match()) > 0 }

// ExitRefs is the ways one rule can point traffic at one exit node.
type ExitRefs struct {
	Action   bool // the rule's own action sends matched traffic there
	Others   bool // the half that answers for everyone else sends it there
	Fallback bool // the rule goes there when its own exit is out of rotation
}

// Any reports whether the rule names the exit at all.
func (r ExitRefs) Any() bool { return r.Action || r.Others || r.Fallback }

// NamesExit reports how this rule points at the given exit. The three ways are
// asked together because any one of them can be the only thing pointing at a
// node: a rule that lists an exit as nothing but its fallback keeps no traffic
// on it today and hands it everything the moment its own exit goes. Everything
// that asks "who depends on this exit" (failover, drain, the node card) asks
// here, where the answers cannot drift apart. A disabled rule names nothing: it is
// kept, not applied.
func (p RoutePolicy) NamesExit(nodeID string) ExitRefs {
	if nodeID == "" || p.Disabled {
		return ExitRefs{}
	}
	return ExitRefs{
		Action:   p.Action == ActionExit && p.ExitNodeID == nodeID,
		Others:   p.OthersAction == ActionExit && p.OthersExitID == nodeID,
		Fallback: p.FallbackKind == FallbackExit && p.FallbackExitID == nodeID,
	}
}

// PoliciesNamingExit returns the rules that point at this exit, in state order.
func (s State) PoliciesNamingExit(nodeID string) []RoutePolicy {
	var out []RoutePolicy
	for _, p := range s.RoutePolicies {
		if p.NamesExit(nodeID).Any() {
			out = append(out, p)
		}
	}
	return out
}

// MatchNode is a rule's match as a tree: either one condition, or two nodes
// with the word that joins them. It exists because a list of lines has to be
// read in some order, and the only order a person can follow while writing one
// is the order they wrote it in.
type MatchNode struct {
	Op   string // "" for a condition, otherwise JoinAnd / JoinOr / JoinExcept
	Cond RouteCondition
	L, R *MatchNode
}

// IsCond reports a leaf: one condition and not two nodes joined.
func (n *MatchNode) IsCond() bool { return n != nil && n.Op == "" }

// MatchTree folds a condition list the way the form reads it: top to bottom,
// each line joining to everything above it. So
//
//	catch a.com and country RU or geosite:ads
//
// is (a.com and RU) or ads, not a.com and (RU or ads). Neither reading is
// wrong in general; this one is the one the operator can follow while typing,
// because the answer after every line is the whole of what is above it. The
// form brackets the runs as they are built. The grouping is never left to be
// guessed, and the renderer nests whatever does not fit one ordinary sing-box
// rule.
//
// "but not" excludes what it names from everything above it, for the same
// reason: it is written after what it narrows.
func MatchTree(conds []RouteCondition) *MatchNode {
	var acc *MatchNode
	for i, c := range conds {
		leaf := &MatchNode{Cond: c}
		if acc == nil {
			// A leading exclusion would be a rule that catches everything but a
			// handful of names; validation refuses it, and one that arrives
			// anyway is dropped instead of obeyed.
			if i > 0 && c.Join == JoinExcept {
				continue
			}
			acc = leaf
			continue
		}
		op := c.Join
		if op != JoinOr && op != JoinExcept {
			op = JoinAnd
		}
		acc = &MatchNode{Op: op, L: acc, R: leaf}
	}
	return acc
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
		return fmt.Errorf("route policy %q: at least one match target is required: %w", p.ID, ErrRuleNeedsMatch)
	}
	for i, c := range p.Conditions {
		if err := c.Validate(i == 0); err != nil {
			return fmt.Errorf("route policy %q: %w", p.ID, err)
		}
	}
	// Every rule starts by saying what it catches. A leading "except" would be a
	// rule that matches everything but a handful of names, which is a footgun
	// dressed as an exclusion.
	if len(p.Conditions) > 0 && p.Conditions[0].Join == JoinExcept {
		return fmt.Errorf("route policy %q: the first condition cannot be an exclusion", p.ID)
	}
	for _, c := range p.Match() {
		if c.Kind != CondCIDR {
			continue
		}
		for _, cidr := range c.Values {
			if _, err := netip.ParsePrefix(strings.TrimSpace(cidr)); err != nil {
				if _, aerr := netip.ParseAddr(strings.TrimSpace(cidr)); aerr != nil {
					return fmt.Errorf("route policy %q: invalid cidr/ip %q", p.ID, cidr)
				}
			}
		}
	}
	if p.Action == ActionExit && strings.TrimSpace(p.ExitNodeID) == "" {
		return fmt.Errorf("route policy %q: exit action requires exit_node_id", p.ID)
	}
	// The fallback answers for whichever half of the rule names an exit, and a
	// reserved rule's half for everyone else names one too.
	if p.Action == ActionExit || p.OthersAction == ActionExit {
		if p.FallbackKind != "" && !p.FallbackKind.Valid() {
			return fmt.Errorf("route policy %q: invalid fallback_kind %q", p.ID, p.FallbackKind)
		}
		if p.FallbackKind == FallbackExit && strings.TrimSpace(p.FallbackExitID) == "" {
			return fmt.Errorf("route policy %q: fallback_kind=exit requires fallback_exit_id", p.ID)
		}
		// A fallback onto the same node is not a fallback: when that node is out
		// of rotation both halves of the answer are out with it.
		if p.FallbackKind == FallbackExit && p.Action == ActionExit && p.FallbackExitID == p.ExitNodeID {
			return fmt.Errorf("route policy %q: the fallback exit is the exit it falls back from", p.ID)
		}
	} else if p.FallbackKind != "" {
		return fmt.Errorf("route policy %q: a fallback was set but the rule sends nothing to an exit", p.ID)
	}
	if p.OthersAction != "" {
		if !p.OthersAction.Valid() {
			return fmt.Errorf("route policy %q: invalid action for everyone else %q", p.ID, p.OthersAction)
		}
		if strings.TrimSpace(p.AppliesToGroupID) == "" {
			return fmt.Errorf("route policy %q: a rule that answers for everyone else needs the group it applies to", p.ID)
		}
		if p.OthersAction == ActionExit && strings.TrimSpace(p.OthersExitID) == "" {
			return fmt.Errorf("route policy %q: sending everyone else to an exit requires that exit", p.ID)
		}
		// Reserving a destination for a group means everyone else gets something
		// ELSE. Both halves landing on the same treatment renders two rules with
		// one match and one outcome: the second is dead, and the operator who
		// wrote it believes a group has an exit of its own.
		if p.OthersAction == p.Action && (p.OthersAction != ActionExit || p.OthersExitID == p.ExitNodeID) {
			return fmt.Errorf("route policy %q: everyone else is given the same treatment as %q, so nothing is reserved", p.ID, p.AppliesToGroupID)
		}
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
	// PurposeEndpoint is the VPN connection endpoint an entry node's TrustTunnel
	// listener terminates; non-VPN visitors see a camouflage site on the same host.
	PurposeEndpoint DomainPurpose = "endpoint"
	// PurposeDecoy is a camouflage-only landing host (no VPN traffic), e.g. the
	// deployment's public apex served in front of the endpoint.
	PurposeDecoy DomainPurpose = "decoy"
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
// (settings table). It is not part of State: tokens never ship to the browser
// via /api/state, and the Bots API masks them. The bot/alert processes read the
// real values directly from the store.
type Settings struct {
	Bot    BotSettings    `json:"bot"`    // management bot (two-way command bot)
	Alert  AlertSettings  `json:"alert"`  // one-way alert bot (billing/watchdog)
	Backup BackupSettings `json:"backup"` // off-site delivery of DR snapshots to Telegram
	Fleet  FleetSettings  `json:"fleet"`  // fleet-wide defaults inherited by new-node provisioning
	Panel  PanelSettings  `json:"panel"`  // panel runtime tunables (loop intervals, session lifetime)
}

// FleetSettings holds fleet-wide defaults the panel offers when provisioning a
// new node, saving the operator from re-typing them per install. They are purely
// UI conveniences and defaults; the authoritative values live on the node.
type FleetSettings struct {
	ACMEEmail        string `json:"acme_email"`        // default ACME contact for new entries (else admin@<apex>)
	RealitySNI       string `json:"reality_sni"`       // default borrowed-CDN SNI for new exits
	Brand            string `json:"brand"`             // display brand (camouflage site / portal)
	Apex             string `json:"apex"`              // apex domain
	ConnectSubdomain string `json:"connect_subdomain"` // login subdomain the endpoint + camouflage portal advertise (<sub>.<apex>)
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
	// GeoCheckHours: how often to ask the geo rule-set sources what they now
	// offer; <=0 = 168 (weekly), floor 6. The floor is the shortest interval that
	// can find anything. The fastest source rebuilds every six hours; checking
	// more often spends requests to learn nothing.
	GeoCheckHours int `json:"geo_check_hours"`
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

// GeoCheckInterval is how often the geo rule-set sources are asked what they
// now offer (default 168h, floor 6h).
func (p PanelSettings) GeoCheckInterval() time.Duration {
	h := p.GeoCheckHours
	if h <= 0 {
		h = 24 * 7
	}
	if h < 6 {
		h = 6
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
// Telegram is not end-to-end encrypted. The snapshot is age-encrypted before it
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

	// Sender names which bot carries the snapshot off the box: BackupSenderAlert,
	// BackupSenderBot, or empty to let the panel pick. It is a setting and not
	// a constant because an installation may run one bot or two, and which one
	// ends up holding a channel full of encrypted snapshots is the operator's
	// decision, not something to be inferred from which token happens to be set.
	Sender string `json:"sender,omitempty"`

	// Schedule & retention for the LOCAL snapshot. These live in the (replicated)
	// settings and not each node's systemd unit, letting the panel govern both the
	// active node and the standby with one edit; the backup binary reads them on
	// each scheduled run and self-gates. Pointers default to "on" when absent so an
	// upgrade of an existing fleet (whose row predates these fields) keeps backing
	// up instead of silently going dark.
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

// Which bot carries snapshots off the box. Stored as a string so an unknown
// value from a newer panel degrades to "let the panel pick" rather than to a
// silently wrong bot.
const (
	BackupSenderAlert = "alert" // the dedicated alert bot
	BackupSenderBot   = "bot"   // the management bot
)

// BackupSenderToken resolves the bot token that delivers snapshots off-site.
// It returns empty only when there is no bot at all to send with.
//
// With no explicit choice the dedicated alert bot is preferred, which keeps a
// channel full of encrypted database dumps out of the management bot's history;
// but an installation that runs a single bot still has to get its snapshots off
// the box, so it falls back to the management bot rather than quietly declining
// to deliver.
func (s Settings) BackupSenderToken() string {
	switch s.Backup.Sender {
	case BackupSenderBot:
		return s.Bot.Token
	case BackupSenderAlert:
		return s.Alert.Token
	}
	if s.Alert.Token != "" {
		return s.Alert.Token
	}
	return s.Bot.Token
}

// BackupSenderName reports which bot BackupSenderToken settled on, letting the panel
// can say it out loud instead of leaving the operator to guess.
func (s Settings) BackupSenderName() string {
	if s.BackupSenderToken() == "" {
		return ""
	}
	if s.Backup.Sender == BackupSenderBot || (s.Backup.Sender == "" && s.Alert.Token == "") {
		return BackupSenderBot
	}
	return BackupSenderAlert
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
		if g.DefaultExitID != "" && g.DefaultExitID != ExitDirect {
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
