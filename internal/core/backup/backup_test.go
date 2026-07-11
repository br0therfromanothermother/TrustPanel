package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// readArchive returns name->content for a tar.gz at path.
func readArchive(t *testing.T, path string) map[string]string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	got := map[string]string{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(tr)
		got[h.Name] = string(b)
	}
	return got
}

// TestCreateBundlesRecoveryDoc proves every snapshot carries the self-contained
// RECOVERY.md (so the runbook survives losing the whole fleet) and that the
// MANIFEST points at it. Uses a fake pg_dump so no real Postgres is needed.
func TestCreateBundlesRecoveryDoc(t *testing.T) {
	dir := t.TempDir()
	fakeDump := filepath.Join(dir, "fakedump.sh")
	if err := os.WriteFile(fakeDump, []byte("#!/bin/sh\nprintf 'SELECT 1;\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out")
	path, err := Create(context.Background(), Options{
		DSN: "x", OutDir: out, PgDumpBin: fakeDump,
	})
	if err != nil {
		t.Fatal(err)
	}
	got := readArchive(t, path)
	if !strings.Contains(got["RECOVERY.md"], "How to restore from this backup") {
		t.Errorf("snapshot missing/empty RECOVERY.md: %q", got["RECOVERY.md"])
	}
	if !strings.Contains(got["MANIFEST.txt"], "RECOVERY.md") {
		t.Errorf("MANIFEST should point at RECOVERY.md, got: %q", got["MANIFEST.txt"])
	}
	if got["db.sql"] != "SELECT 1;\n" {
		t.Errorf("db.sql wrong: %q", got["db.sql"])
	}
}

func TestWriteAndReadArchive(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "trustpanel-20260613-000000.tar.gz")
	files := map[string][]byte{"db.sql": []byte("SELECT 1;"), "pki/ca.key": []byte("KEY"), "MANIFEST.txt": []byte("m")}
	if err := writeArchive(path, files); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("archive must be 0600, got %o", info.Mode().Perm())
	}
	// Read it back.
	f, _ := os.Open(path)
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	got := map[string]string{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(tr)
		got[h.Name] = string(b)
	}
	if got["db.sql"] != "SELECT 1;" || got["pki/ca.key"] != "KEY" {
		t.Fatalf("archive contents wrong: %v", got)
	}
}

func TestPruneKeepsNewest(t *testing.T) {
	dir := t.TempDir()
	names := []string{
		"trustpanel-20260101-000000.tar.gz",
		"trustpanel-20260201-000000.tar.gz",
		"trustpanel-20260301-000000.tar.gz",
		"trustpanel-20260401-000000.tar.gz",
		"unrelated.txt",
	}
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := Prune(dir, 2); err != nil {
		t.Fatal(err)
	}
	left := map[string]bool{}
	es, _ := os.ReadDir(dir)
	for _, e := range es {
		left[e.Name()] = true
	}
	// Newest 2 snapshots + the unrelated file remain.
	if !left["trustpanel-20260301-000000.tar.gz"] || !left["trustpanel-20260401-000000.tar.gz"] {
		t.Errorf("newest snapshots removed: %v", left)
	}
	if left["trustpanel-20260101-000000.tar.gz"] || left["trustpanel-20260201-000000.tar.gz"] {
		t.Errorf("old snapshots not pruned: %v", left)
	}
	if !left["unrelated.txt"] {
		t.Errorf("prune touched unrelated files: %v", left)
	}
	// keep<=0 keeps all.
	for _, n := range []string{"trustpanel-20260501-000000.tar.gz", "trustpanel-20260601-000000.tar.gz"} {
		os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o600)
	}
	if err := Prune(dir, 0); err != nil {
		t.Fatal(err)
	}
	es, _ = os.ReadDir(dir)
	if len(es) < 5 {
		t.Errorf("keep<=0 should keep all, got %d", len(es))
	}
}
