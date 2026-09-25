package backup

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// RestoreFile is the file-level half of disaster recovery, run on the operator's
// machine (not the fleet): it takes whatever was downloaded from the Telegram
// backup channel into dir (one whole snapshot, or the ".partNNN" pieces of one
// that had to be split), concatenates it in index order, age-decrypts it with
// the identity file, and writes the plaintext snapshot tar.gz to outPath. The
// database/PKI restore from that tar.gz then follows the disaster-recovery
// guide. Returns the written path.
func RestoreFile(dir, identityPath, outPath string) (string, error) {
	parts, err := collectParts(dir)
	if err != nil {
		return "", err
	}
	var cipher []byte
	for _, p := range parts {
		b, err := os.ReadFile(p)
		if err != nil {
			return "", err
		}
		cipher = append(cipher, b...)
	}

	idf, err := os.Open(identityPath)
	if err != nil {
		return "", fmt.Errorf("open age identity: %w", err)
	}
	defer idf.Close()
	idBytes, err := os.ReadFile(identityPath)
	if err != nil {
		return "", fmt.Errorf("read age identity: %w", err)
	}
	plain, err := decryptAge(cipher, idf)
	if err != nil {
		return "", err
	}
	// Deliveries made before the double-encryption fix wrapped an
	// already-encrypted snapshot a second time, so one pass leaves another age
	// file rather than an archive. Those copies are still sitting in the chat;
	// peel the extra layer instead of handing back something that is not a tar.
	if isAgeFile(plain) {
		inner, err := decryptAge(plain, bytes.NewReader(idBytes))
		if err != nil {
			return "", fmt.Errorf("snapshot is encrypted twice and the inner layer did not open: %w", err)
		}
		plain = inner
	}

	if err := os.WriteFile(outPath, plain, 0o600); err != nil {
		return "", err
	}
	return outPath, nil
}

// ageHeader begins every age file, whatever the recipients.
const ageHeader = "age-encryption.org/v1\n"

func isAgeFile(b []byte) bool { return bytes.HasPrefix(b, []byte(ageHeader)) }

// collectParts returns what has to be concatenated to rebuild the snapshot in
// dir: the ".partNNN" files in index order, or, for a snapshot small enough to
// have been sent whole (the ordinary case), the single file itself.
// Either way everything must belong to one snapshot; a mixed directory is
// refused instead of silently reassembled into garbage.
func collectParts(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var names, whole []string
	bases := map[string]struct{}{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		n := e.Name()
		if i := strings.Index(n, partInfix); i >= 0 {
			names = append(names, n)
			bases[n[:i]] = struct{}{}
			continue
		}
		if isSnapshotName(n) {
			whole = append(whole, n)
		}
	}
	if len(names) == 0 {
		switch len(whole) {
		case 0:
			return nil, fmt.Errorf("no snapshot and no parts (*%sNNN) found in %s", partInfix, dir)
		case 1:
			return []string{filepath.Join(dir, whole[0])}, nil
		default:
			sort.Strings(whole)
			return nil, fmt.Errorf("dir %s holds several snapshots %v; keep one and try again", dir, whole)
		}
	}
	if len(bases) > 1 {
		return nil, fmt.Errorf("dir %s mixes parts from multiple snapshots %v; separate them first", dir, keys(bases))
	}
	sort.Strings(names) // zero-padded indices => lexical == numeric order
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = filepath.Join(dir, n)
	}
	return out, nil
}

func keys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
