package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"trustpanel/internal/core/pki"
)

// VerifyOptions configures a verify-restore drill: it proves the latest snapshot
// is actually restorable by loading it into a throwaway Postgres and checking the
// PKI — without ever touching the live database. This is "Layer 4": a backup you
// have never restored is only a hope.
type VerifyOptions struct {
	SnapshotPath  string // explicit snapshot; "" => newest in Dir
	Dir           string // backup dir (default /var/backups/trustpanel)
	PgBinDir      string // dir holding initdb/pg_ctl/psql; "" => discover
	RunAsPostgres bool   // wrap PG commands with `runuser -u postgres` (PG refuses to run as root)
	KeepTemp      bool   // leave the temp dir for debugging
}

// VerifyReport is the outcome of a successful drill.
type VerifyReport struct {
	Snapshot         string
	DBBytes          int64
	MigrationVersion string
	TableRows        map[string]int64 // sampled key tables
	PKIChecked       bool
	CAExpiry         time.Time
	Duration         time.Duration
}

// probeTables are the tables we sanity-count after the restore; each should
// normally have rows in a live fleet (a 0 count is surfaced, not failed, since a
// brand-new fleet legitimately has none).
var probeTables = []string{"nodes", "users", "settings", "groups"}

// VerifyRestore performs the drill end to end and returns the report. Any step
// failing (snapshot missing db.sql, restore error, PKI broken) returns an error
// — the caller (the drill CLI) turns that into an alert.
func VerifyRestore(ctx context.Context, o VerifyOptions) (*VerifyReport, error) {
	start := time.Now()
	if o.Dir == "" {
		o.Dir = "/var/backups/trustpanel"
	}
	snap := o.SnapshotPath
	if snap == "" {
		s, err := LatestSnapshot(o.Dir)
		if err != nil {
			return nil, err
		}
		snap = s
	}

	work, err := os.MkdirTemp("", "trustpanel-verify-")
	if err != nil {
		return nil, err
	}
	if !o.KeepTemp {
		defer os.RemoveAll(work)
	}

	if err := extractTarGz(snap, work); err != nil {
		return nil, fmt.Errorf("extract snapshot: %w", err)
	}
	dbSQL := filepath.Join(work, "db.sql")
	st, err := os.Stat(dbSQL)
	if err != nil {
		return nil, fmt.Errorf("snapshot has no db.sql: %w", err)
	}

	rep := &VerifyReport{Snapshot: snap, DBBytes: st.Size(), TableRows: map[string]int64{}}
	if err := o.verifyDB(ctx, work, dbSQL, rep); err != nil {
		return nil, err
	}

	pkiDir := filepath.Join(work, "pki")
	if _, err := os.Stat(pkiDir); err == nil {
		exp, err := checkPKI(pkiDir)
		if err != nil {
			return nil, fmt.Errorf("PKI verify: %w", err)
		}
		rep.PKIChecked = true
		rep.CAExpiry = exp
	}

	rep.Duration = time.Since(start)
	return rep, nil
}

// verifyDB spins an ephemeral Postgres in work, loads db.sql, and runs the
// sanity queries into rep. The instance listens on a private unix socket only
// (no TCP), and is torn down (immediate stop) regardless of outcome.
func (o VerifyOptions) verifyDB(ctx context.Context, work, dbSQL string, rep *VerifyReport) error {
	bin, err := o.resolveBinDir()
	if err != nil {
		return err
	}
	datadir := filepath.Join(work, "pgdata")
	sockdir := filepath.Join(work, "sock")
	for _, d := range []string{datadir, sockdir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return err
		}
	}
	// When running as root, PG must run as the postgres user; hand it the temp dir.
	if o.RunAsPostgres {
		if out, err := exec.CommandContext(ctx, "chown", "-R", "postgres:postgres", work).CombinedOutput(); err != nil {
			return fmt.Errorf("chown temp to postgres: %v: %s", err, out)
		}
	}

	run := func(name string, args ...string) (string, error) {
		full := append([]string{filepath.Join(bin, name)}, args...)
		if o.RunAsPostgres {
			full = append([]string{"runuser", "-u", "postgres", "--"}, full...)
		}
		cmd := exec.CommandContext(ctx, full[0], full[1:]...)
		var buf bytes.Buffer
		cmd.Stdout, cmd.Stderr = &buf, &buf
		err := cmd.Run()
		return buf.String(), err
	}

	if out, err := run("initdb", "-D", datadir, "-U", "postgres", "--auth=trust", "-E", "UTF8"); err != nil {
		return fmt.Errorf("initdb: %v: %s", err, lastLines(out, 3))
	}
	port := freePort()
	logfile := filepath.Join(work, "pg.log")
	opts := fmt.Sprintf("-p %d -k %s -c listen_addresses=''", port, sockdir)
	// -l redirects the server's stdout/stderr to a logfile. Without it the daemon
	// inherits our captured pipe and pg_ctl's Run() never sees EOF (it would hang).
	if out, err := run("pg_ctl", "-D", datadir, "-l", logfile, "-o", opts, "-w", "-t", "60", "start"); err != nil {
		return fmt.Errorf("start ephemeral postgres: %v: %s", err, lastLines(readFileOr(logfile, out), 8))
	}
	defer run("pg_ctl", "-D", datadir, "-m", "immediate", "stop") //nolint:errcheck // best-effort teardown

	psql := func(db string, extra ...string) (string, error) {
		args := append([]string{"-h", sockdir, "-p", strconv.Itoa(port), "-U", "postgres", "-v", "ON_ERROR_STOP=1", "-d", db}, extra...)
		return run("psql", args...)
	}

	if out, err := psql("postgres", "-c", "CREATE DATABASE trustpanel"); err != nil {
		return fmt.Errorf("createdb: %v: %s", err, lastLines(out, 3))
	}
	if out, err := psql("trustpanel", "-q", "-f", dbSQL); err != nil {
		return fmt.Errorf("restore db.sql (the snapshot does NOT load cleanly): %v: %s", err, lastLines(out, 8))
	}

	// Latest applied migration (guarded so a schema without the table doesn't
	// error). The real schema_migrations key is the migration NAME (see
	// store.MigrateOnce), which sorts chronologically (0001_…, 0009_…), so max(name)
	// is the newest one — not a numeric `version` column (which never existed and
	// made this probe silently empty in production).
	if v, err := psql("trustpanel", "-tAc",
		"SELECT COALESCE(max(name),'') FROM schema_migrations WHERE to_regclass('schema_migrations') IS NOT NULL"); err == nil {
		rep.MigrationVersion = strings.TrimSpace(v)
	}
	for _, tbl := range probeTables {
		out, err := psql("trustpanel", "-tAc",
			fmt.Sprintf("SELECT CASE WHEN to_regclass('%s') IS NULL THEN -1 ELSE (SELECT count(*) FROM %s) END", tbl, tbl))
		if err != nil {
			continue
		}
		if n, err := strconv.ParseInt(strings.TrimSpace(out), 10, 64); err == nil && n >= 0 {
			rep.TableRows[tbl] = n
		}
	}
	return nil
}

// checkPKI validates the snapshot's CA: the key parses and matches the cert (by
// issuing a probe leaf and verifying it chains back), and the cert is not
// expired. Returns the CA's NotAfter.
func checkPKI(pkiDir string) (time.Time, error) {
	caCertPEM, err := os.ReadFile(filepath.Join(pkiDir, "ca.crt"))
	if err != nil {
		return time.Time{}, fmt.Errorf("read ca.crt: %w", err)
	}
	caKeyPEM, err := os.ReadFile(filepath.Join(pkiDir, "ca.key"))
	if err != nil {
		return time.Time{}, fmt.Errorf("read ca.key: %w", err)
	}
	ca, err := pki.LoadCA(caCertPEM, caKeyPEM)
	if err != nil {
		return time.Time{}, err
	}
	// Prove the key actually signs and matches the cert: issue a probe leaf and
	// verify it chains to the CA cert (a mismatched key would fail verification).
	leafPEM, _, err := ca.IssueLeaf(pki.RoleNode, "verify-restore-probe", nil, time.Hour)
	if err != nil {
		return time.Time{}, fmt.Errorf("CA key cannot sign: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caCertPEM) {
		return time.Time{}, fmt.Errorf("ca.crt is not a valid certificate")
	}
	leafBlock, _ := pem.Decode(leafPEM)
	if leafBlock == nil {
		return time.Time{}, fmt.Errorf("issued probe leaf is not PEM")
	}
	leaf, err := x509.ParseCertificate(leafBlock.Bytes)
	if err != nil {
		return time.Time{}, err
	}
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: pool,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil {
		return time.Time{}, fmt.Errorf("CA key does not match ca.crt: %w", err)
	}
	caBlock, _ := pem.Decode(caCertPEM)
	caCert, err := x509.ParseCertificate(caBlock.Bytes)
	if err != nil {
		return time.Time{}, err
	}
	if time.Now().After(caCert.NotAfter) {
		return caCert.NotAfter, fmt.Errorf("CA cert expired at %s", caCert.NotAfter.Format(time.RFC3339))
	}
	return caCert.NotAfter, nil
}

// resolveBinDir finds the dir holding initdb/pg_ctl/psql. Order: explicit
// PgBinDir, then `initdb` on PATH, then the highest /usr/lib/postgresql/*/bin
// (Debian, where they are not on PATH).
func (o VerifyOptions) resolveBinDir() (string, error) {
	if o.PgBinDir != "" {
		return o.PgBinDir, nil
	}
	if p, err := exec.LookPath("initdb"); err == nil {
		return filepath.Dir(p), nil
	}
	matches, _ := filepath.Glob("/usr/lib/postgresql/*/bin/initdb")
	if len(matches) > 0 {
		// Highest version last after a version-aware sort would be ideal; lexical is
		// close enough and we only need a working initdb.
		return filepath.Dir(matches[len(matches)-1]), nil
	}
	return "", fmt.Errorf("initdb not found (set --pg-bin-dir)")
}

// extractTarGz unpacks a gzip-compressed tar into dir, refusing path traversal.
// Only regular files are written (the snapshot holds db.sql, pki/*, MANIFEST).
func extractTarGz(path, dir string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		clean := filepath.Clean(hdr.Name)
		if strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) {
			return fmt.Errorf("unsafe path in archive: %q", hdr.Name)
		}
		dst := filepath.Join(dir, clean)
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return err
		}
		out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, tr); err != nil { //nolint:gosec // our own archive
			out.Close()
			return err
		}
		if err := out.Close(); err != nil {
			return err
		}
	}
}

// freePort asks the OS for an unused TCP port. The ephemeral PG only listens on a
// unix socket, but the port number uniquifies the socket file name.
func freePort() int {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 54329
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// readFileOr returns the file's contents, or fallback if it can't be read (used
// to surface the ephemeral server log on a startup failure).
func readFileOr(path, fallback string) string {
	if b, err := os.ReadFile(path); err == nil && len(b) > 0 {
		return string(b)
	}
	return fallback
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
