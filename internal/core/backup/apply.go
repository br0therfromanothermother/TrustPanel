package backup

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// ApplyOptions configures applying a plaintext snapshot ONTO a freshly
// bootstrapped control plane: it reloads the database and restores the PKI. It
// does the deterministic, destructive mechanics only — it does NOT touch
// control-plane leadership (active_node / epoch). After it, claim leadership with
// `trustpanel promote --node-id <this> --start-serve` (no --pg-promote: the DB is
// already primary, not a replica).
type ApplyOptions struct {
	SnapshotPath string // plaintext snapshot tar.gz (db.sql + pki/*), e.g. from `restore --from-file`
	DSN          string // app DSN (the role serve uses); its dbname+user are recreated
	PKIDir       string // where pki/* is restored (default /etc/trustpanel/pki)
	Owner        string // chown owner for restored PKI ("user:group"); "" => skip chown
	Force        bool   // proceed even if the target DB already holds fleet data
	StopServe    bool   // stop trustpanel-serve before the reload (default true via CLI)
	Runner       ApplyRunner
}

// ApplyReport summarises what was applied.
type ApplyReport struct {
	DBName     string
	Owner      string
	PriorNodes int // node rows found before the reload (0 unless --force over real data)
	PKIFiles   int
	Duration   time.Duration
}

// ApplyRunner is the privileged surface ApplyRestore needs; injected so the
// orchestration (guard, drop/recreate order, load) is unit-testable with a fake.
type ApplyRunner interface {
	// Super runs SQL as the postgres superuser (local peer auth) against db.
	Super(ctx context.Context, db, sql string) (string, error)
	// LoadDump loads a plain SQL dump file as the app role via dsn.
	LoadDump(ctx context.Context, dsn, file string) (string, error)
	// Systemctl runs a systemctl subcommand (best-effort callers ignore the error).
	Systemctl(ctx context.Context, args ...string) (string, error)
}

// ApplyRestore reloads the database and restores the PKI from a plaintext
// snapshot. It refuses to overwrite a populated DB without Force.
func ApplyRestore(ctx context.Context, o ApplyOptions) (*ApplyReport, error) {
	start := time.Now()
	if o.PKIDir == "" {
		o.PKIDir = "/etc/trustpanel/pki"
	}
	if o.Runner == nil {
		o.Runner = ExecApplyRunner{}
	}
	ci, err := parseConnInfo(o.DSN)
	if err != nil {
		return nil, err
	}
	if ci.dbname == "" || ci.user == "" {
		return nil, fmt.Errorf("DSN must specify dbname and user")
	}

	work, err := os.MkdirTemp("", "trustpanel-apply-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(work)
	if err := extractTarGz(o.SnapshotPath, work); err != nil {
		return nil, fmt.Errorf("extract snapshot: %w", err)
	}
	dbSQL := filepath.Join(work, "db.sql")
	if _, err := os.Stat(dbSQL); err != nil {
		return nil, fmt.Errorf("snapshot has no db.sql: %w", err)
	}

	rep := &ApplyReport{DBName: ci.dbname, Owner: ci.user}

	// Guard: refuse to clobber a DB that already holds fleet data.
	prior, err := o.priorNodeCount(ctx, ci.dbname)
	if err != nil {
		return nil, err
	}
	rep.PriorNodes = prior
	if prior > 0 && !o.Force {
		return nil, fmt.Errorf("target database %q already has %d nodes — refusing to overwrite without --force", ci.dbname, prior)
	}

	// Stop the panel so it is not writing while we recreate the DB underneath it.
	if o.StopServe {
		_, _ = o.Runner.Systemctl(ctx, "stop", "trustpanel-serve.service") // best effort
	}

	// Recreate the database owned by the app role (mirrors bootstrap's
	// `createdb -O <user>`), so the loaded objects end up owned by the app role.
	if _, err := o.Runner.Super(ctx, "postgres", terminateBackendsSQL(ci.dbname)); err != nil {
		return nil, fmt.Errorf("terminate connections to %s: %w", ci.dbname, err)
	}
	if _, err := o.Runner.Super(ctx, "postgres", fmt.Sprintf(`DROP DATABASE IF EXISTS %s`, quoteIdent(ci.dbname))); err != nil {
		return nil, fmt.Errorf("drop database: %w", err)
	}
	if _, err := o.Runner.Super(ctx, "postgres", fmt.Sprintf(`CREATE DATABASE %s OWNER %s`, quoteIdent(ci.dbname), quoteIdent(ci.user))); err != nil {
		return nil, fmt.Errorf("create database: %w", err)
	}

	// Load the dump as the app role (objects owned by it, as after a bootstrap).
	if out, err := o.Runner.LoadDump(ctx, o.DSN, dbSQL); err != nil {
		return nil, fmt.Errorf("load db.sql (the snapshot did not restore cleanly): %w: %s", err, lastLines(out, 8))
	}

	// Restore the PKI: this brings back the ORIGINAL CA so existing node certs
	// stay valid.
	n, err := restorePKIFiles(filepath.Join(work, "pki"), o.PKIDir, o.Owner)
	if err != nil {
		return nil, fmt.Errorf("restore PKI: %w", err)
	}
	rep.PKIFiles = n

	rep.Duration = time.Since(start)
	return rep, nil
}

// priorNodeCount returns the number of node rows in db, or 0 if the database or
// the nodes table does not exist yet (a fresh bootstrap).
func (o ApplyOptions) priorNodeCount(ctx context.Context, db string) (int, error) {
	exists, err := o.Runner.Super(ctx, "postgres",
		fmt.Sprintf("SELECT 1 FROM pg_database WHERE datname=%s", quoteLiteral(db)))
	if err != nil {
		return 0, fmt.Errorf("check database exists: %w", err)
	}
	if strings.TrimSpace(lastLine(exists)) != "1" {
		return 0, nil // DB not created yet
	}
	out, err := o.Runner.Super(ctx, db,
		"SELECT CASE WHEN to_regclass('nodes') IS NULL THEN 0 ELSE (SELECT count(*) FROM nodes) END")
	if err != nil {
		return 0, fmt.Errorf("count existing nodes: %w", err)
	}
	n, _ := strconv.Atoi(strings.TrimSpace(lastLine(out)))
	return n, nil
}

// restorePKIFiles copies every regular file from src into dst, giving *.key mode
// 0600 and everything else 0644, and (when owner != "") chowning them. Returns
// the count restored. A missing src (snapshot without PKI) is not an error.
func restorePKIFiles(src, dst, owner string) (int, error) {
	entries, err := os.ReadDir(src)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return 0, err
	}
	n := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		body, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil {
			return n, err
		}
		mode := os.FileMode(0o644)
		if strings.HasSuffix(e.Name(), ".key") {
			mode = 0o600
		}
		out := filepath.Join(dst, e.Name())
		if err := os.WriteFile(out, body, mode); err != nil {
			return n, err
		}
		if owner != "" {
			if o, e2 := exec.Command("chown", owner, out).CombinedOutput(); e2 != nil {
				return n, fmt.Errorf("chown %s: %v: %s", out, e2, strings.TrimSpace(string(o)))
			}
		}
		n++
	}
	return n, nil
}

// connInfo is the subset of a libpq keyword DSN ApplyRestore needs.
type connInfo struct {
	dbname, user, password, stripped string // stripped = DSN with password removed
}

// parseConnInfo parses a space-separated libpq keyword/value DSN.
func parseConnInfo(dsn string) (connInfo, error) {
	var ci connInfo
	var kept []string
	for _, f := range strings.Fields(dsn) {
		k, v, ok := strings.Cut(f, "=")
		if !ok {
			return ci, fmt.Errorf("malformed DSN field %q (want key=value)", f)
		}
		switch k {
		case "dbname":
			ci.dbname = v
		case "user":
			ci.user = v
		case "password":
			ci.password = v
			continue // keep out of the stripped form
		}
		kept = append(kept, f)
	}
	ci.stripped = strings.Join(kept, " ")
	return ci, nil
}

func terminateBackendsSQL(db string) string {
	return fmt.Sprintf(
		"SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname=%s AND pid<>pg_backend_pid()",
		quoteLiteral(db))
}

func quoteIdent(s string) string   { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }
func quoteLiteral(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// lastLine returns the last non-empty, trimmed line of s (psql -tA output).
func lastLine(s string) string {
	lines := strings.Split(s, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if t := strings.TrimSpace(lines[i]); t != "" {
			return t
		}
	}
	return ""
}

// ExecApplyRunner is the real privileged runner: postgres superuser over local
// peer auth, psql for the dump load, and systemctl.
type ExecApplyRunner struct{}

func (ExecApplyRunner) Super(ctx context.Context, db, sql string) (string, error) {
	cmd := exec.CommandContext(ctx, "sudo", "-u", "postgres", "psql", "-v", "ON_ERROR_STOP=1", "-tAXc", sql, "-d", db)
	cmd.Dir = "/tmp" // a root caller's home is unreadable by postgres
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (ExecApplyRunner) LoadDump(ctx context.Context, dsn, file string) (string, error) {
	ci, err := parseConnInfo(dsn)
	if err != nil {
		return "", err
	}
	// Pass the password via the environment, never on argv (which is world-visible).
	cmd := exec.CommandContext(ctx, "psql", "-v", "ON_ERROR_STOP=1", "-d", ci.stripped, "-f", file)
	cmd.Env = append(os.Environ(), "PGPASSWORD="+ci.password)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (ExecApplyRunner) Systemctl(ctx context.Context, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, "systemctl", args...).CombinedOutput()
	return string(out), err
}
