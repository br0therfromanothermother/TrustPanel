// Package backup creates disaster-recovery snapshots of the control plane: the
// Postgres database (all fleet state) plus the fleet PKI (the CA is the only
// truly irreplaceable secret — controller/node certs can be re-issued from it).
// One snapshot is a single 0600 tar.gz; old ones are pruned to a retention count.
package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const filePrefix = "trustpanel-"

// recoveryDoc is bundled into every snapshot so the restore steps travel with
// the backup itself — recoverable even if the whole fleet (and the repo) is gone.
//
//go:embed RECOVERY.md
var recoveryDoc []byte

// Options configures a backup run.
type Options struct {
	DSN       string // Postgres conninfo (libpq/pgx keyword form)
	OutDir    string // where snapshots are written
	PKIDir    string // fleet PKI dir to include (ca.key etc.); "" to skip
	Keep      int    // retention count (older snapshots pruned); <=0 keeps all
	PgDumpBin string // pg_dump binary (default "pg_dump")
	Now       func() time.Time
}

// Create writes one snapshot and prunes old ones. Returns the snapshot path.
func Create(ctx context.Context, o Options) (string, error) {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.PgDumpBin == "" {
		o.PgDumpBin = "pg_dump"
	}
	if strings.TrimSpace(o.DSN) == "" {
		return "", fmt.Errorf("backup: DSN is required")
	}
	if err := os.MkdirAll(o.OutDir, 0o700); err != nil {
		return "", err
	}

	// pg_dump the whole database (conninfo passed as the dbname argument).
	var dumpErr strings.Builder
	cmd := exec.CommandContext(ctx, o.PgDumpBin, "--no-owner", "--no-privileges", o.DSN)
	cmd.Stderr = &dumpErr
	dump, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("pg_dump: %w: %s", err, strings.TrimSpace(dumpErr.String()))
	}

	files := map[string][]byte{"db.sql": dump}
	if o.PKIDir != "" {
		if err := collectPKI(o.PKIDir, files); err != nil {
			return "", err
		}
	}
	ts := o.Now().UTC().Format("20060102-150405")
	files["RECOVERY.md"] = recoveryDoc
	files["MANIFEST.txt"] = []byte(fmt.Sprintf(
		"TrustPanel backup\ncreated: %s UTC\ndb_bytes: %d\npki_dir: %s\nrestore: see RECOVERY.md in this archive\n",
		o.Now().UTC().Format(time.RFC3339), len(dump), o.PKIDir))

	path := filepath.Join(o.OutDir, filePrefix+ts+".tar.gz")
	if err := writeArchive(path, files); err != nil {
		return "", err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return "", err
	}
	if err := Prune(o.OutDir, o.Keep); err != nil {
		return path, fmt.Errorf("snapshot written but prune failed: %w", err)
	}
	return path, nil
}

func collectPKI(dir string, into map[string][]byte) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return err
		}
		into["pki/"+e.Name()] = b
	}
	return nil
}

// writeArchive writes files (name->content) as a gzip-compressed tar, atomically.
// The temp file carries a random suffix (os.CreateTemp) so two runs writing into
// the same dir cannot collide on a fixed ".tmp" name.
func writeArchive(path string, files map[string][]byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp) // no-op once the rename below succeeds
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	// Stable order for deterministic archives.
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		body := files[n]
		hdr := &tar.Header{Name: n, Mode: 0o600, Size: int64(len(body)), ModTime: time.Now()}
		if err := tw.WriteHeader(hdr); err != nil {
			_ = f.Close()
			return err
		}
		if _, err := tw.Write(body); err != nil {
			_ = f.Close()
			return err
		}
	}
	if err := tw.Close(); err != nil {
		_ = f.Close()
		return err
	}
	if err := gz.Close(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Prune keeps the newest `keep` snapshots in dir and removes older ones. Names
// embed a sortable UTC timestamp, so lexical sort == chronological.
func Prune(dir string, keep int) error {
	if keep <= 0 {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var snaps []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), filePrefix) && strings.HasSuffix(e.Name(), ".tar.gz") {
			snaps = append(snaps, e.Name())
		}
	}
	if len(snaps) <= keep {
		return nil
	}
	sort.Strings(snaps) // oldest first
	for _, old := range snaps[:len(snaps)-keep] {
		if err := os.Remove(filepath.Join(dir, old)); err != nil {
			return err
		}
	}
	return nil
}

// LatestSnapshot returns the path of the newest snapshot in dir. Names embed a
// sortable UTC timestamp, so lexical max == newest.
func LatestSnapshot(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	var newest string
	for _, e := range entries {
		n := e.Name()
		if !e.IsDir() && strings.HasPrefix(n, filePrefix) && strings.HasSuffix(n, ".tar.gz") && n > newest {
			newest = n
		}
	}
	if newest == "" {
		return "", fmt.Errorf("no snapshots (%s*.tar.gz) found in %s", filePrefix, dir)
	}
	return filepath.Join(dir, newest), nil
}
