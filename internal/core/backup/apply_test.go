package backup

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeApplyRunner records calls and answers the guard queries from canned state.
type fakeApplyRunner struct {
	dbExists  bool
	nodeCount string // returned for the to_regclass('nodes') guard
	calls     []string
}

func (f *fakeApplyRunner) Super(_ context.Context, db, sql string) (string, error) {
	f.calls = append(f.calls, "super:"+db+":"+firstWords(sql, 3))
	switch {
	case strings.Contains(sql, "pg_database"):
		if f.dbExists {
			return "1\n", nil
		}
		return "\n", nil
	case strings.Contains(sql, "to_regclass('nodes')"):
		return f.nodeCount + "\n", nil
	}
	return "", nil
}

func (f *fakeApplyRunner) LoadDump(_ context.Context, _, file string) (string, error) {
	f.calls = append(f.calls, "load:"+filepath.Base(file))
	return "", nil
}

func (f *fakeApplyRunner) Systemctl(_ context.Context, args ...string) (string, error) {
	f.calls = append(f.calls, "systemctl:"+strings.Join(args, " "))
	return "", nil
}

func firstWords(s string, n int) string {
	fields := strings.Fields(s)
	if len(fields) > n {
		fields = fields[:n]
	}
	return strings.Join(fields, " ")
}

const applyDSN = "host=127.0.0.1 port=5432 user=tp_app password=s3cret dbname=trustpanel sslmode=disable"

func TestApplyRestoreFreshDB(t *testing.T) {
	dir := t.TempDir()
	snap := writeSnapshot(t, dir, sampleDump, true) // db.sql + pki/ca.{crt,key}
	fr := &fakeApplyRunner{dbExists: false}
	pkiDst := filepath.Join(dir, "pki-out")

	rep, err := ApplyRestore(context.Background(), ApplyOptions{
		SnapshotPath: snap, DSN: applyDSN, PKIDir: pkiDst, Owner: "", StopServe: true, Runner: fr,
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if rep.DBName != "trustpanel" || rep.Owner != "tp_app" {
		t.Errorf("report db/owner = %s/%s", rep.DBName, rep.Owner)
	}
	if rep.PKIFiles != 2 {
		t.Errorf("pki files = %d, want 2", rep.PKIFiles)
	}
	// PKI restored with correct modes.
	if st, _ := os.Stat(filepath.Join(pkiDst, "ca.key")); st != nil && st.Mode().Perm() != 0o600 {
		t.Errorf("ca.key mode = %v, want 0600", st.Mode().Perm())
	}
	if st, _ := os.Stat(filepath.Join(pkiDst, "ca.crt")); st != nil && st.Mode().Perm() != 0o644 {
		t.Errorf("ca.crt mode = %v, want 0644", st.Mode().Perm())
	}
	// Order: serve stopped, then terminate -> drop -> create -> load.
	joined := strings.Join(fr.calls, "|")
	for _, want := range []string{"systemctl:stop", "super:postgres:SELECT pg_terminate_backend(pid)",
		"super:postgres:DROP DATABASE IF", "super:postgres:CREATE DATABASE \"trustpanel\"", "load:db.sql"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing step %q in %v", want, fr.calls)
		}
	}
	if idxOf(fr.calls, "DROP DATABASE") > idxOf(fr.calls, "load:db.sql") {
		t.Errorf("drop must precede load: %v", fr.calls)
	}
}

func TestApplyRestoreRefusesPopulatedDB(t *testing.T) {
	dir := t.TempDir()
	snap := writeSnapshot(t, dir, sampleDump, true)
	fr := &fakeApplyRunner{dbExists: true, nodeCount: "5"}

	_, err := ApplyRestore(context.Background(), ApplyOptions{
		SnapshotPath: snap, DSN: applyDSN, PKIDir: filepath.Join(dir, "o"), Runner: fr,
	})
	if err == nil || !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Fatalf("expected refusal on populated DB, got %v", err)
	}
	// Must NOT have dropped anything.
	if strings.Contains(strings.Join(fr.calls, "|"), "DROP DATABASE") {
		t.Errorf("guard must run before any destructive step: %v", fr.calls)
	}
}

func TestApplyRestoreForceOverwrites(t *testing.T) {
	dir := t.TempDir()
	snap := writeSnapshot(t, dir, sampleDump, true)
	fr := &fakeApplyRunner{dbExists: true, nodeCount: "5"}

	rep, err := ApplyRestore(context.Background(), ApplyOptions{
		SnapshotPath: snap, DSN: applyDSN, PKIDir: filepath.Join(dir, "o"), Force: true, Runner: fr,
	})
	if err != nil {
		t.Fatalf("force apply: %v", err)
	}
	if rep.PriorNodes != 5 {
		t.Errorf("prior nodes = %d, want 5", rep.PriorNodes)
	}
	if !strings.Contains(strings.Join(fr.calls, "|"), "DROP DATABASE") {
		t.Errorf("force should drop+recreate: %v", fr.calls)
	}
}

func TestParseConnInfo(t *testing.T) {
	ci, err := parseConnInfo(applyDSN)
	if err != nil {
		t.Fatal(err)
	}
	if ci.dbname != "trustpanel" || ci.user != "tp_app" || ci.password != "s3cret" {
		t.Errorf("parsed = %+v", ci)
	}
	if strings.Contains(ci.stripped, "password") {
		t.Errorf("stripped DSN must not contain the password: %q", ci.stripped)
	}
	if !strings.Contains(ci.stripped, "dbname=trustpanel") {
		t.Errorf("stripped DSN lost dbname: %q", ci.stripped)
	}
}

func idxOf(calls []string, sub string) int {
	for i, c := range calls {
		if strings.Contains(c, sub) {
			return i
		}
	}
	return -1
}
