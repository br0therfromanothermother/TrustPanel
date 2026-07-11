package backup

import (
	"bytes"
	"fmt"
	"io"
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
