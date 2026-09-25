package backup

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"filippo.io/age"
)

// encryptAge encrypts plain for the given age X25519 recipient ("age1...").
// The matching identity (private key) is held by the operator off the fleet, so
// even a leaked Telegram token / a Telegram-side read yields only ciphertext.
func encryptAge(plain []byte, recipient string) ([]byte, error) {
	r, err := age.ParseX25519Recipient(strings.TrimSpace(recipient))
	if err != nil {
		return nil, fmt.Errorf("parse age recipient: %w", err)
	}
	var buf bytes.Buffer
	w, err := age.Encrypt(&buf, r)
	if err != nil {
		return nil, fmt.Errorf("age encrypt: %w", err)
	}
	if _, err := w.Write(plain); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// encryptAgeTo encrypts for several recipients at once. A snapshot on the box
// is written for two: the operator's key, held off the fleet, and the node's
// own key, so the box can still verify and restore its own backups while an
// archive that travels without the node's key is only ciphertext.
func encryptAgeTo(plain []byte, recipients []age.Recipient) ([]byte, error) {
	if len(recipients) == 0 {
		return nil, fmt.Errorf("age encrypt: no recipients")
	}
	var buf bytes.Buffer
	w, err := age.Encrypt(&buf, recipients...)
	if err != nil {
		return nil, fmt.Errorf("age encrypt: %w", err)
	}
	if _, err := w.Write(plain); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// DefaultIdentityPath is where the node keeps its own backup key. It sits
// outside the PKI dir deliberately: a key inside the archive it unlocks would
// be no encryption at all.
const DefaultIdentityPath = "/etc/trustpanel/backup.identity"

// decryptAgeWith decrypts with one identity already in hand.
func decryptAgeWith(ciphertext []byte, id age.Identity) ([]byte, error) {
	r, err := age.Decrypt(bytes.NewReader(ciphertext), id)
	if err != nil {
		return nil, fmt.Errorf("age decrypt (wrong key or corrupt data): %w", err)
	}
	return io.ReadAll(r)
}

// NodeIdentity loads the node's own age key, minting it on first use. It is the
// key that lets this machine read back what it wrote, which the weekly restore
// drill needs, and nothing else: a snapshot is really for the operator's key
// for. Mode 0600, and the file is never included in a snapshot (it lives
// outside the PKI dir on purpose; a key inside the archive it unlocks would be
// no encryption at all).
func NodeIdentity(path string) (*age.X25519Identity, error) {
	b, err := os.ReadFile(path)
	if err == nil {
		ids, perr := age.ParseIdentities(bytes.NewReader(b))
		if perr != nil {
			return nil, fmt.Errorf("parse node backup key %s: %w", path, perr)
		}
		for _, id := range ids {
			if x, ok := id.(*age.X25519Identity); ok {
				return x, nil
			}
		}
		return nil, fmt.Errorf("node backup key %s holds no X25519 identity", path)
	}
	if !os.IsNotExist(err) {
		return nil, err
	}
	id, err := age.GenerateX25519Identity()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	body := fmt.Sprintf("# TrustPanel node backup key. Keep it on this machine; it only\n"+
		"# lets the node read back its own snapshots.\n# public key: %s\n%s\n",
		id.Recipient(), id)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		return nil, err
	}
	return id, nil
}

// decryptAge decrypts ciphertext with the identities parsed from an age key file
// (the output of `age-keygen`; comment lines are ignored). Used by file-level
// restore on the operator's machine, never on the fleet.
func decryptAge(ciphertext []byte, identitiesFile io.Reader) ([]byte, error) {
	ids, err := age.ParseIdentities(identitiesFile)
	if err != nil {
		return nil, fmt.Errorf("parse age identity: %w", err)
	}
	r, err := age.Decrypt(bytes.NewReader(ciphertext), ids...)
	if err != nil {
		return nil, fmt.Errorf("age decrypt (wrong key or corrupt data): %w", err)
	}
	return io.ReadAll(r)
}
