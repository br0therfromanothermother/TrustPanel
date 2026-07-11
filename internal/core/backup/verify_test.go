package backup

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"trustpanel/internal/core/pki"
)

func mustWrite(t *testing.T, path string, body []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
}

// a self-contained dump that mimics a real snapshot's db.sql: the migration
// tracking table plus a couple of the probe tables with rows.
const sampleDump = `
CREATE TABLE schema_migrations (name text primary key, applied_at timestamptz NOT NULL DEFAULT now());
INSERT INTO schema_migrations (name) VALUES ('0001_init'),('0002_users'),('0009_domain_tls_issuer');
CREATE TABLE nodes (id text primary key, name text);
INSERT INTO nodes VALUES ('n1','exit1'),('n2','entry1');
CREATE TABLE users (id text primary key);
INSERT INTO users VALUES ('u1'),('u2'),('u3');
CREATE TABLE settings (data jsonb);
INSERT INTO settings VALUES ('{}');
`

func writeSnapshot(t *testing.T, dir string, dump string, withPKI bool) string {
	t.Helper()
	files := map[string][]byte{
		"db.sql":       []byte(dump),
		"MANIFEST.txt": []byte("test snapshot\n"),
	}
	if withPKI {
		caCert, caKey, err := pki.GenerateCA("verify-test-ca", 24*time.Hour)
		if err != nil {
			t.Fatalf("generate CA: %v", err)
		}
		files["pki/ca.crt"] = caCert
		files["pki/ca.key"] = caKey
	}
	path := filepath.Join(dir, filePrefix+"20260622-120000.tar.gz")
	if err := writeArchive(path, files); err != nil {
		t.Fatalf("write archive: %v", err)
	}
	return path
}

func TestVerifyRestoreEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("initdb"); err != nil {
		t.Skip("initdb not available; skipping ephemeral-Postgres drill")
	}
	dir := t.TempDir()
	snap := writeSnapshot(t, dir, sampleDump, true)

	rep, err := VerifyRestore(context.Background(), VerifyOptions{SnapshotPath: snap})
	if err != nil {
		t.Fatalf("verify-restore: %v", err)
	}
	if rep.MigrationVersion != "0009_domain_tls_issuer" {
		t.Errorf("migration version = %q, want 0009_domain_tls_issuer", rep.MigrationVersion)
	}
	if rep.TableRows["nodes"] != 2 || rep.TableRows["users"] != 3 || rep.TableRows["settings"] != 1 {
		t.Errorf("row counts = %v, want nodes=2 users=3 settings=1", rep.TableRows)
	}
	if !rep.PKIChecked || rep.CAExpiry.IsZero() {
		t.Errorf("PKI not checked: %+v", rep)
	}
}

func TestVerifyRestoreRejectsBadDump(t *testing.T) {
	if _, err := exec.LookPath("initdb"); err != nil {
		t.Skip("initdb not available")
	}
	dir := t.TempDir()
	snap := writeSnapshot(t, dir, "CREATE TABLE bad (; -- syntax error\n", false)

	_, err := VerifyRestore(context.Background(), VerifyOptions{SnapshotPath: snap})
	if err == nil || !strings.Contains(err.Error(), "does NOT load cleanly") {
		t.Fatalf("expected a load failure, got %v", err)
	}
}

func TestCheckPKIDetectsMismatch(t *testing.T) {
	// A ca.crt from one CA with a ca.key from another must be rejected.
	dir := t.TempDir()
	cert1, _, _ := pki.GenerateCA("ca-one", time.Hour)
	_, key2, _ := pki.GenerateCA("ca-two", time.Hour)
	pkiDir := filepath.Join(dir, "pki")
	mustWrite(t, filepath.Join(pkiDir, "ca.crt"), cert1)
	mustWrite(t, filepath.Join(pkiDir, "ca.key"), key2)
	// The mismatch is caught as soon as the CA key is used to sign (x509 reports
	// "doesn't match parent's PublicKey"); either that or the chain check rejects it.
	if _, err := checkPKI(pkiDir); err == nil || !strings.Contains(err.Error(), "match") {
		t.Fatalf("expected key/cert mismatch rejection, got %v", err)
	}
}

func TestLatestSnapshot(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, filePrefix+"20260101-000000.tar.gz"), []byte("a"))
	mustWrite(t, filepath.Join(dir, filePrefix+"20260622-120000.tar.gz"), []byte("b"))
	mustWrite(t, filepath.Join(dir, "not-a-snapshot.txt"), []byte("c"))
	got, err := LatestSnapshot(dir)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(got) != filePrefix+"20260622-120000.tar.gz" {
		t.Fatalf("latest = %s", got)
	}
}
