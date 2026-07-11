package backup

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// RestoreFile is the file-level half of disaster recovery, run on the operator's
// machine (NOT the fleet): it collects the ".age.partNNN" files downloaded from
// the Telegram backup channel into dir, concatenates them in index order,
// age-decrypts with the identity file, and writes the plaintext snapshot tar.gz
// to outPath. The database/PKI restore from that tar.gz then follows
// the disaster-recovery guide. Returns the written path.
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
	plain, err := decryptAge(cipher, idf)
	if err != nil {
		return "", err
	}

	if err := os.WriteFile(outPath, plain, 0o600); err != nil {
		return "", err
	}
	return outPath, nil
}

// collectParts returns the part files in dir sorted by their zero-padded index.
// All parts must belong to a single snapshot base name (we refuse a mixed dir so
// a half-overlapping set can't silently reassemble into garbage).
func collectParts(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var names []string
	bases := map[string]struct{}{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		n := e.Name()
		if i := strings.Index(n, partInfix); i >= 0 {
			names = append(names, n)
			bases[n[:i]] = struct{}{}
		}
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("no backup parts (*%sNNN) found in %s", partInfix, dir)
	}
	if len(bases) > 1 {
		return nil, fmt.Errorf("dir %s mixes parts from multiple snapshots %v — separate them first", dir, keys(bases))
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
