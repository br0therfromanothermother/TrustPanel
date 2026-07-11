package store

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"trustpanel/internal/core/model"
)

// ErrNoAdmin is returned when the requested admin account does not exist.
var ErrNoAdmin = errors.New("admin not found")

// AdminCount returns the number of admin accounts. When zero, the panel keeps
// protected routes open so a fresh install cannot lock out the operator.
func (s *Store) AdminCount(ctx context.Context) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM admins`).Scan(&n)
	return n, err
}

// AdminByUsername loads an admin account, returning ErrNoAdmin if absent.
func (s *Store) AdminByUsername(ctx context.Context, username string) (model.AdminAccount, error) {
	var a model.AdminAccount
	err := s.pool.QueryRow(ctx,
		`SELECT username, password_hash, role, namespace, telegram_id, alert_chat_id, locale, created_at, updated_at
		 FROM admins WHERE username = $1`, username).
		Scan(&a.Username, &a.PasswordHash, &a.Role, &a.NamespaceID, &a.TelegramID, &a.AlertChatID, &a.Locale, &a.CreatedAt, &a.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return a, ErrNoAdmin
	}
	return a, err
}

// AccountByTelegramID resolves a Telegram user id to its bound account (the
// role-aware bot's authorization: telegram_id -> account -> role/namespace,
// replacing the flat allowlist). Returns ErrNoAdmin if no account is bound.
func (s *Store) AccountByTelegramID(ctx context.Context, telegramID int64) (model.AdminAccount, error) {
	var a model.AdminAccount
	err := s.pool.QueryRow(ctx,
		`SELECT username, password_hash, role, namespace, telegram_id, alert_chat_id, locale, created_at, updated_at
		 FROM admins WHERE telegram_id = $1`, telegramID).
		Scan(&a.Username, &a.PasswordHash, &a.Role, &a.NamespaceID, &a.TelegramID, &a.AlertChatID, &a.Locale, &a.CreatedAt, &a.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return a, ErrNoAdmin
	}
	return a, err
}

// UpsertAdmin creates or updates an account's password hash. On insert the role
// defaults to admin (the bootstrap account); an existing role is left untouched.
func (s *Store) UpsertAdmin(ctx context.Context, username, passwordHash string) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO admins (username, password_hash, updated_at) VALUES ($1, $2, now())
		 ON CONFLICT (username) DO UPDATE SET password_hash = $2, updated_at = now()`,
		username, passwordHash)
	return err
}

// CreateAccount mints a fresh single-member namespace and seeds it with one
// account (the fleet owner creating a brand-new operator). An operator's
// namespace id equals its username — the legacy 1:1 mapping — so an admin role
// lands in the infra namespace (""). It fails if the username already exists.
// Adding a further member to an EXISTING namespace uses CreateAccountIn.
func (s *Store) CreateAccount(ctx context.Context, username, passwordHash string, role model.Role) error {
	if !role.Valid() {
		return errors.New("invalid role")
	}
	ns := ""
	if role == model.RoleOperator {
		ns = username
		if err := s.CreateNamespace(ctx, ns, ns); err != nil {
			return err
		}
	}
	return s.CreateAccountIn(ctx, username, passwordHash, role, ns)
}

// SetAccountTelegram binds (telegramID nil clears) this account's Telegram id and
// alert chat. The telegram_id unique index rejects binding one id to two accounts.
func (s *Store) SetAccountTelegram(ctx context.Context, username string, telegramID *int64, alertChatID string) error {
	ct, err := s.pool.Exec(ctx,
		`UPDATE admins SET telegram_id=$2, alert_chat_id=$3, updated_at=now() WHERE username=$1`,
		username, telegramID, alertChatID)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNoAdmin
	}
	return nil
}

// SetAccountLocale saves this account's chosen UI language ("" clears it).
func (s *Store) SetAccountLocale(ctx context.Context, username, locale string) error {
	ct, err := s.pool.Exec(ctx,
		`UPDATE admins SET locale=$2, updated_at=now() WHERE username=$1`, username, locale)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNoAdmin
	}
	return nil
}

// AppendEvent records one operator-log event and opportunistically prunes rows
// older than 90 days so the table stays small without a separate job.
func (s *Store) AppendEvent(ctx context.Context, e model.Event) error {
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO events (kind, severity, message, actor, owner_id) VALUES ($1,$2,$3,$4,$5)`,
		string(e.Kind), string(e.Severity), e.Message, e.Actor, e.OwnerID); err != nil {
		return err
	}
	_, _ = s.pool.Exec(ctx, `DELETE FROM events WHERE at < now() - interval '90 days'`)
	return nil
}

// ListEvents returns the most recent events (newest first) for one namespace,
// optionally filtered by kind (empty = all), capped at limit (<=0 or >500 -> 200).
// owner is the viewer's namespace: "" = admin (infra + admin's own clients), an
// operator username for an operator. Events never cross namespaces.
func (s *Store) ListEvents(ctx context.Context, kind, owner string, limit int) ([]model.Event, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	conds := []string{"owner_id = $1"}
	args := []any{owner}
	if kind != "" {
		conds = append(conds, "kind = $2")
		args = append(args, kind)
	}
	q := `SELECT id, at, kind, severity, message, actor, owner_id FROM events WHERE ` +
		strings.Join(conds, " AND ") +
		fmt.Sprintf(" ORDER BY at DESC, id DESC LIMIT $%d", len(args)+1)
	args = append(args, limit)
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Event
	for rows.Next() {
		var e model.Event
		var kind, sev string
		if err := rows.Scan(&e.ID, &e.At, &kind, &sev, &e.Message, &e.Actor, &e.OwnerID); err != nil {
			return nil, err
		}
		e.Kind = model.EventKind(kind)
		e.Severity = model.EventSeverity(sev)
		out = append(out, e)
	}
	return out, rows.Err()
}

// ListAdmins returns all accounts (no hashes) ordered by username, across every
// namespace (the fleet-owner view).
func (s *Store) ListAdmins(ctx context.Context) ([]model.AdminAccount, error) {
	return s.listAdmins(ctx, allNamespaces)
}

// ListAdminsIn returns the accounts in one namespace (a namespace admin's member
// view). The infra namespace is namespace = "".
func (s *Store) ListAdminsIn(ctx context.Context, namespace string) ([]model.AdminAccount, error) {
	return s.listAdmins(ctx, namespace)
}

// listAdmins lists accounts, optionally restricted to one namespace. A negative
// filter (the sentinel "*") would be ambiguous, so callers pass "" for the infra
// namespace and use ListAdmins for the unscoped fleet-owner view.
func (s *Store) listAdmins(ctx context.Context, namespace string) ([]model.AdminAccount, error) {
	q := `SELECT username, password_hash, role, namespace, telegram_id, alert_chat_id, locale, created_at, updated_at FROM admins`
	var args []any
	if namespace != allNamespaces {
		q += ` WHERE namespace = $1`
		args = append(args, namespace)
	}
	q += ` ORDER BY username`
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.AdminAccount
	for rows.Next() {
		var a model.AdminAccount
		if err := rows.Scan(&a.Username, &a.PasswordHash, &a.Role, &a.NamespaceID, &a.TelegramID, &a.AlertChatID, &a.Locale, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// allNamespaces is the sentinel for listAdmins meaning "no namespace filter"
// (the fleet-owner view). It cannot collide with a real id: namespace ids are
// derived from usernames, which never contain a NUL.
const allNamespaces = "\x00all"

// CreateNamespace inserts a namespace row (idempotent). The infra namespace ("")
// is implicit and must never be created here.
func (s *Store) CreateNamespace(ctx context.Context, id, label string) error {
	if strings.TrimSpace(id) == "" {
		return errors.New("namespace id required")
	}
	_, err := s.pool.Exec(ctx,
		`INSERT INTO namespaces (id, label) VALUES ($1, $2) ON CONFLICT (id) DO NOTHING`,
		id, label)
	return err
}

// CreateAccountIn creates an account in an existing namespace with an explicit
// role. It fails if the username already exists (so it cannot silently change a
// role or namespace) — the caller mints the namespace first when needed.
func (s *Store) CreateAccountIn(ctx context.Context, username, passwordHash string, role model.Role, namespace string) error {
	if !role.Valid() {
		return errors.New("invalid role")
	}
	ct, err := s.pool.Exec(ctx,
		`INSERT INTO admins (username, password_hash, role, namespace, updated_at)
		 VALUES ($1,$2,$3,$4, now()) ON CONFLICT (username) DO NOTHING`,
		username, passwordHash, string(role), namespace)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return errors.New("account already exists")
	}
	return nil
}

// SetRole changes an account's role in place (admin<->operator). It does NOT move
// the account between namespaces and does NOT touch any owned resource — that is
// the point of decoupling the namespace from the account. Authorization (who may
// change whose role, and the last-admin guards) is enforced by the caller.
func (s *Store) SetRole(ctx context.Context, username string, role model.Role) error {
	if !role.Valid() {
		return errors.New("invalid role")
	}
	ct, err := s.pool.Exec(ctx,
		`UPDATE admins SET role=$2, updated_at=now() WHERE username=$1`, username, string(role))
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNoAdmin
	}
	return nil
}

// CountNamespaceAdmins returns how many admins a namespace has (the last-admin
// guard for delete/demote). namespace = "" counts the fleet owners.
func (s *Store) CountNamespaceAdmins(ctx context.Context, namespace string) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM admins WHERE namespace=$1 AND role='admin'`, namespace).Scan(&n)
	return n, err
}

// CountNamespaceAccounts returns the total number of accounts in a namespace
// (used to decide whether a delete empties the namespace).
func (s *Store) CountNamespaceAccounts(ctx context.Context, namespace string) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM admins WHERE namespace=$1`, namespace).Scan(&n)
	return n, err
}

// DeleteAdmin removes an account, honouring the multi-member namespace rules:
//   - refuse to remove the last fleet owner (admin in the infra namespace "") so
//     the panel cannot be locked out;
//   - refuse to remove the last admin of a non-empty namespace while other
//     members remain, so a namespace is never stranded without member management;
//   - only when the account is the LAST one in its (non-empty) namespace do we
//     reassign that namespace's resources to infra (owner_id -> NULL) and drop the
//     namespaces row; otherwise resources stay with the surviving members.
func (s *Store) DeleteAdmin(ctx context.Context, username string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var role, ns string
	if err := tx.QueryRow(ctx,
		`SELECT role, namespace FROM admins WHERE username = $1`, username).Scan(&role, &ns); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNoAdmin
		}
		return err
	}

	var nsAdmins, nsAccounts int
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FILTER (WHERE role='admin'), count(*) FROM admins WHERE namespace = $1`,
		ns).Scan(&nsAdmins, &nsAccounts); err != nil {
		return err
	}
	isAdmin := role == "admin"
	if isAdmin && ns == "" && nsAdmins <= 1 {
		return errors.New("cannot delete the last admin account")
	}
	if isAdmin && ns != "" && nsAdmins <= 1 && nsAccounts > 1 {
		return errors.New("cannot delete the last admin of a namespace while members remain")
	}

	// Empties a non-empty namespace? Reassign its resources to infra and drop it.
	if ns != "" && nsAccounts <= 1 {
		for _, t := range []string{"users", "groups", "route_policies", "nodes"} {
			if _, err := tx.Exec(ctx,
				`UPDATE `+t+` SET owner_id = NULL WHERE owner_id = $1`, ns); err != nil {
				return err
			}
		}
	}

	if _, err := tx.Exec(ctx, `DELETE FROM admins WHERE username = $1`, username); err != nil {
		return err
	}
	if ns != "" && nsAccounts <= 1 {
		if _, err := tx.Exec(ctx, `DELETE FROM namespaces WHERE id = $1`, ns); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
