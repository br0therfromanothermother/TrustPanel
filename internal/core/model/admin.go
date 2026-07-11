package model

import "time"

// AdminAccount is a panel operator login. The password hash (bcrypt) is stored
// in Postgres so a promoted standby can authenticate operators; browser
// sessions are local and not persisted.
type AdminAccount struct {
	Username     string `json:"username"`
	PasswordHash string `json:"-"`
	// Role gates infra: admin manages the control plane (the split-brain fence),
	// operator gets a private client namespace. Empty defaults to admin.
	Role Role `json:"role"`
	// NamespaceID is the tenant this account belongs to. "" = the infra
	// namespace (the fleet owner). Decoupled from Username so several accounts
	// can share one namespace and a role can change without moving resources.
	NamespaceID string `json:"namespace"`
	// TelegramID binds this account to the role-aware management bot (replaces the
	// old flat allowlist); AlertChatID is where this account's alerts are sent.
	TelegramID  *int64 `json:"telegram_id,omitempty"`
	AlertChatID string `json:"alert_chat_id,omitempty"`
	// Locale is the account's chosen UI language ("ru"/"en"; "" = unset → browser
	// default in the panel, Telegram language_code in the bot). Saved server-side so
	// it follows the account across devices and reaches the bot.
	Locale    string    `json:"locale,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Role is an account's privilege level. There are exactly two, hardcoded — no
// RBAC builder.
type Role string

const (
	RoleAdmin    Role = "admin"
	RoleOperator Role = "operator"
)

func (r Role) Valid() bool   { return r == RoleAdmin || r == RoleOperator }
func (r Role) IsAdmin() bool { return r == RoleAdmin || r == "" } // empty defaults to admin

// CanManageInfra reports whether this account may manage SHARED infrastructure:
// the control plane, HA, backups, global settings, bot tokens, node provisioning,
// domains, and infra-tier (fleet/guard) routing. Infra is shared among all
// admins ("by agreement"); operators get none of it. Equivalent to "is an
// admin".
func (a AdminAccount) CanManageInfra() bool { return a.Role.IsAdmin() }

// IsBootstrapOwner reports whether this account is the admin of the infra
// namespace ("") — the bootstrap owner. It alone may see across all namespaces
// (the panel lens), mint/delete accounts and namespaces, change roles, and acts
// as the last-owner fence. An admin of a non-empty namespace (a co-owner) shares
// infra but is otherwise scoped to its own namespace.
func (a AdminAccount) IsBootstrapOwner() bool { return a.Role.IsAdmin() && a.NamespaceID == "" }

// Namespace is the owner_id this account writes to and reads from: "" for the
// fleet owner (the infra namespace), the namespace id otherwise. It is the scope
// key for owned resources (users/groups/policies/nodes) and the event log.
func (a AdminAccount) Namespace() string { return a.NamespaceID }
