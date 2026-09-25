package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// atomicWrite writes data to path via a temp file + rename in the same dir.
func atomicWrite(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-"+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// sha256Hex returns the hex sha256 of data.
func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// fileSHA256 returns the hex sha256 of a file, or "" if it does not exist.
func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// resolveWithinRoots cleans path and verifies it sits inside one of roots.
// It rejects traversal and paths outside the allowlisted roots.
func resolveWithinRoots(roots []string, path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("empty path")
	}
	clean := filepath.Clean(path)
	if !filepath.IsAbs(clean) {
		return "", fmt.Errorf("path %q must be absolute", path)
	}
	for _, root := range roots {
		root = filepath.Clean(root)
		if clean == root || strings.HasPrefix(clean, root+string(os.PathSeparator)) {
			return clean, nil
		}
	}
	return "", fmt.Errorf("path %q is outside allowlisted roots", path)
}

// backupSet captures the prior contents of files so a failed apply can be
// rolled back. A nil entry means the file did not exist before.
type backupSet struct {
	entries map[string]*[]byte // abs path -> prior bytes (nil = absent)
}

func newBackupSet() *backupSet { return &backupSet{entries: map[string]*[]byte{}} }

func (b *backupSet) capture(path string) error {
	if _, ok := b.entries[path]; ok {
		return nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		b.entries[path] = nil
		return nil
	}
	if err != nil {
		return err
	}
	cp := append([]byte(nil), data...)
	b.entries[path] = &cp
	return nil
}

// restore reverts every captured file to its prior state.
func (b *backupSet) restore() error {
	var firstErr error
	for path, prior := range b.entries {
		var err error
		if prior == nil {
			err = os.Remove(path)
			if errors.Is(err, os.ErrNotExist) {
				err = nil
			}
		} else {
			err = atomicWrite(path, *prior, 0o600)
		}
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
