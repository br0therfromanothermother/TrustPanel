package store

import (
	"context"
	"errors"
	"time"
)

// ErrRecoveryCode is returned for every unusable code: unknown, expired or
// already spent. The three cases share one error on purpose: the redeeming
// endpoint must not become an oracle that tells an attacker which of them a
// guess hit.
var ErrRecoveryCode = errors.New("recovery code is invalid, expired or already used")

// IssueRecoveryCode stores the hash of a fresh recovery code for username and
// drops any earlier code for that account, so an operator who asks twice can
// only ever redeem the newest one. telegramID is recorded for the audit trail.
func (s *Store) IssueRecoveryCode(ctx context.Context, username, codeHash string, telegramID int64, expiresAt time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var exists bool
	if err := tx.QueryRow(ctx, `SELECT true FROM admins WHERE username = $1`, username).Scan(&exists); err != nil {
		return ErrNoAdmin
	}
	if _, err := tx.Exec(ctx, `DELETE FROM admin_recovery_codes WHERE username = $1`, username); err != nil {
		return err
	}
	var issuedTo *int64
	if telegramID != 0 {
		issuedTo = &telegramID
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO admin_recovery_codes (code_hash, username, issued_to, expires_at)
		 VALUES ($1, $2, $3, $4)`, codeHash, username, issuedTo, expiresAt); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// RedeemRecoveryCode spends a code and returns the account it belongs to. The
// check and the spend are one statement, so two racing redemptions cannot both
// win: the second matches no unused row and gets ErrRecoveryCode.
func (s *Store) RedeemRecoveryCode(ctx context.Context, codeHash string) (string, error) {
	var username string
	err := s.pool.QueryRow(ctx,
		`UPDATE admin_recovery_codes
		    SET used_at = now()
		  WHERE code_hash = $1 AND used_at IS NULL AND expires_at > now()
		 RETURNING username`, codeHash).Scan(&username)
	if err != nil {
		return "", ErrRecoveryCode
	}
	return username, nil
}

// LastRecoveryIssue reports when username last asked for a code, so the bot can
// hold a caller to a cooldown instead of letting a stuck finger mint codes (and
// fire alerts) in a loop. A zero time means the account has never asked.
func (s *Store) LastRecoveryIssue(ctx context.Context, username string) (time.Time, error) {
	var at *time.Time
	if err := s.pool.QueryRow(ctx,
		`SELECT max(created_at) FROM admin_recovery_codes WHERE username = $1`, username).Scan(&at); err != nil {
		return time.Time{}, err
	}
	if at == nil {
		return time.Time{}, nil
	}
	return *at, nil
}
