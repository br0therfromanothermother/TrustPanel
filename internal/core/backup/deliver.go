package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// DefaultChunkBytes is the per-part ciphertext size used when none is configured.
// The Telegram Bot API caps a bot's document upload at 50 MB; 45 MiB leaves room
// for multipart overhead.
const DefaultChunkBytes = 45 << 20

// partInfix is inserted before the zero-padded part index in each part filename,
// e.g. "trustpanel-20260622-033000.tar.gz.age.part001".
const partInfix = ".age.part"

// TelegramTarget identifies the destination chat. Token is reused from the alert
// bot; ChatID is the dedicated private backup channel.
type TelegramTarget struct {
	Token      string
	ChatID     string
	BaseURL    string // test hook; "" => api.telegram.org
	HTTPClient *http.Client
}

// DeliverOptions configures off-site delivery.
type DeliverOptions struct {
	AgeRecipient string // age public key (age1...); required
	ChunkBytes   int    // max bytes per part; <=0 uses DefaultChunkBytes
	Now          func() time.Time
}

// DeliverTelegram age-encrypts the snapshot at path, splits the ciphertext into
// <=ChunkBytes parts, uploads each as a Telegram document, then posts a manifest
// message so the part set is verifiable and reassemblable from the chat alone.
func DeliverTelegram(ctx context.Context, path string, tgt TelegramTarget, o DeliverOptions) error {
	if strings.TrimSpace(o.AgeRecipient) == "" {
		// Refuse to send the CA key + secrets in the clear. Telegram is not E2E.
		return fmt.Errorf("backup deliver: age recipient is required (snapshot must be encrypted)")
	}
	if strings.TrimSpace(tgt.Token) == "" || strings.TrimSpace(tgt.ChatID) == "" {
		return fmt.Errorf("backup deliver: telegram token and chat id are required")
	}
	chunk := o.ChunkBytes
	if chunk <= 0 {
		chunk = DefaultChunkBytes
	}

	plain, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	cipher, err := encryptAge(plain, o.AgeRecipient)
	if err != nil {
		return err
	}
	parts := splitBytes(cipher, chunk)

	base := filepath.Base(path)
	tg := newTelegram(tgt.Token, tgt.ChatID, tgt.BaseURL, tgt.HTTPClient)

	sums := make([]string, len(parts))
	for i, p := range parts {
		name := fmt.Sprintf("%s%s%03d", base, partInfix, i+1)
		sum := sha256.Sum256(p)
		sums[i] = hex.EncodeToString(sum[:])
		caption := fmt.Sprintf("%s — part %d/%d", base, i+1, len(parts))
		if err := tg.sendDocument(ctx, name, p, caption); err != nil {
			return fmt.Errorf("send part %d/%d: %w", i+1, len(parts), err)
		}
	}

	if err := tg.sendMessage(ctx, manifest(base, cipher, parts, sums)); err != nil {
		return fmt.Errorf("send manifest: %w", err)
	}
	return nil
}

// splitBytes slices b into consecutive chunks of at most size bytes. A zero-length
// input yields a single empty chunk so an (improbably) empty ciphertext is still
// representable as one part.
func splitBytes(b []byte, size int) [][]byte {
	if size <= 0 {
		size = DefaultChunkBytes
	}
	if len(b) == 0 {
		return [][]byte{{}}
	}
	var out [][]byte
	for off := 0; off < len(b); off += size {
		end := off + size
		if end > len(b) {
			end = len(b)
		}
		out = append(out, b[off:end])
	}
	return out
}

func manifest(base string, cipher []byte, parts [][]byte, sums []string) string {
	whole := sha256.Sum256(cipher)
	var b strings.Builder
	fmt.Fprintf(&b, "TrustPanel off-site backup\n")
	fmt.Fprintf(&b, "snapshot: %s\n", base)
	fmt.Fprintf(&b, "encrypted: age (x25519)\n")
	fmt.Fprintf(&b, "parts: %d\n", len(parts))
	fmt.Fprintf(&b, "cipher_bytes: %d\n", len(cipher))
	fmt.Fprintf(&b, "cipher_sha256: %s\n", hex.EncodeToString(whole[:]))
	for i := range parts {
		fmt.Fprintf(&b, "part %d/%d: %s%s%03d  bytes=%d  sha256=%s\n",
			i+1, len(parts), base, partInfix, i+1, len(parts[i]), sums[i])
	}
	fmt.Fprintf(&b, "restore: download all parts, then\n")
	fmt.Fprintf(&b, "  trustpanel restore --from-file <dir> --identity <age-key> --out %s\n", base)
	return b.String()
}
