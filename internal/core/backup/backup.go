// Package backup creates disaster-recovery snapshots of the control plane: the
// Postgres database (all fleet state) plus the fleet PKI (the CA is the only
// truly irreplaceable secret, since controller and node certs re-issue from it).
// One snapshot is a single 0600 tar.gz; old ones are pruned to a retention count.
package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	_ "embed"
	"filippo.io/age"
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
// the backup itself, recoverable even if the whole fleet and the repo are gone.
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

	// IdentityPath is the node's own age key, minted on first use. When it is
	// set the snapshot is written encrypted (".tar.gz.age"): the archive holds
	// the database and the fleet's private keys, and file mode alone stops
	// nobody once it has been copied somewhere else. Empty means plaintext,
	// as the tests and an explicit --no-encrypt run do.
	IdentityPath string
	// AgeRecipient is the operator's public key, held off the fleet. Optional
	// but wanted: without it a snapshot can only ever be opened by this machine,
	// so losing the machine loses the backups with it.
	AgeRecipient string
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
	blob, err := archiveBytes(files)
	if err != nil {
		return "", err
	}
	if o.IdentityPath != "" {
		id, err := NodeIdentity(o.IdentityPath)
		if err != nil {
			return "", err
		}
		to := []age.Recipient{id.Recipient()}
		if r := strings.TrimSpace(o.AgeRecipient); r != "" {
			op, err := age.ParseX25519Recipient(r)
			if err != nil {
				return "", fmt.Errorf("backup: parse age recipient: %w", err)
			}
			to = append(to, op)
		}
		if blob, err = encryptAgeTo(blob, to); err != nil {
			return "", err
		}
		path += ageSuffix
	}
	if err := writeFileAtomic(path, blob); err != nil {
		return "", err
	}
	if err := Prune(o.OutDir, o.Keep); err != nil {
		return path, fmt.Errorf("snapshot written but prune failed: %w", err)
	}
	return path, nil
}

// ageSuffix marks an encrypted snapshot. Both spellings are snapshots for the
// purposes of retention and "which is the newest", so a fleet that switches
// encryption on does not orphan what it wrote before.
const ageSuffix = ".age"

func isSnapshotName(n string) bool {
	return strings.HasSuffix(n, ".tar.gz") || strings.HasSuffix(n, ".tar.gz"+ageSuffix)
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

// archiveBytes renders files (name->content) as a gzip-compressed tar in
// memory. A snapshot is encrypted whole before it touches the filesystem, so
// the archive is built as bytes and never streamed to disk: writing the
// plaintext out first and encrypting in place would leave it recoverable.
func archiveBytes(files map[string][]byte) ([]byte, error) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
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
			return nil, err
		}
		if _, err := tw.Write(body); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// writeArchive writes a plaintext snapshot straight to disk. Used where no
// encryption is asked for.
func writeArchive(path string, files map[string][]byte) error {
	blob, err := archiveBytes(files)
	if err != nil {
		return err
	}
	return writeFileAtomic(path, blob)
}

// writeFileAtomic writes blob to path through a temp file in the same dir. The
// temp name carries a random suffix so two runs into one dir cannot collide on
// a fixed one.
func writeFileAtomic(path string, blob []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp) // no-op once the rename below succeeds
	if _, err := f.Write(blob); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
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
		if !e.IsDir() && strings.HasPrefix(e.Name(), filePrefix) && isSnapshotName(e.Name()) {
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
		if !e.IsDir() && strings.HasPrefix(n, filePrefix) && isSnapshotName(n) && n > newest {
			newest = n
		}
	}
	if newest == "" {
		return "", fmt.Errorf("no snapshots (%s*.tar.gz) found in %s", filePrefix, dir)
	}
	return filepath.Join(dir, newest), nil
}
