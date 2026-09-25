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

	"trustpanel/internal/core/watchdog"
)

// DefaultChunkBytes is the per-part ciphertext size used when none is configured.
// The Telegram Bot API caps a bot's document upload at 50 MB; 45 MiB leaves room
// for multipart overhead.
const DefaultChunkBytes = 45 << 20

// partInfix is inserted before the zero-padded part index when, and only when,
// a snapshot had to be split: "trustpanel-20260622-033000.tar.gz.age.part001".
const partInfix = ".part"

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
	AgeRecipient string // age public key (age1...); required when the snapshot on disk is not already encrypted
	ChunkBytes   int    // max bytes per part; <=0 uses DefaultChunkBytes
	Lang         string // language for the chat messages; "" => watchdog.DefaultLang
	Now          func() time.Time
}

// DeliverTelegram uploads the snapshot at path to the backup chat and follows it
// with a short manifest, so the copy can be found, checked and restored from the
// chat alone months later.
//
// The snapshot is sent as it lies on disk when it is already age-encrypted,
// which it is by default. Encrypting it a second time on the way out buys
// nothing, doubles the name to ".age.age" and leaves a file that needs two
// passes to open, none of it mentioned in the manifest. A
// plaintext snapshot (the --no-encrypt path) is still encrypted here before it
// leaves: Telegram is not end-to-end, and the archive holds the CA key.
//
// It is split only when it genuinely exceeds the part limit. Labelling a two
// megabyte file "part 1 of 1" describes an operation that did not happen.
func DeliverTelegram(ctx context.Context, path string, tgt TelegramTarget, o DeliverOptions) error {
	if strings.TrimSpace(tgt.Token) == "" || strings.TrimSpace(tgt.ChatID) == "" {
		return fmt.Errorf("backup deliver: telegram token and chat id are required")
	}
	chunk := o.ChunkBytes
	if chunk <= 0 {
		chunk = DefaultChunkBytes
	}
	lang := o.Lang
	if lang == "" {
		lang = watchdog.DefaultLang
	}

	blob, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	base := filepath.Base(path)
	if !strings.HasSuffix(base, ageSuffix) {
		if strings.TrimSpace(o.AgeRecipient) == "" {
			// Refuse to send the CA key + secrets in the clear. Telegram is not E2E.
			return fmt.Errorf("backup deliver: age recipient is required (snapshot must be encrypted)")
		}
		if blob, err = encryptAge(blob, o.AgeRecipient); err != nil {
			return err
		}
		base += ageSuffix
	}

	whole := sha256.Sum256(blob)
	wholeSum := hex.EncodeToString(whole[:])
	parts := splitBytes(blob, chunk)
	tg := newTelegram(tgt.Token, tgt.ChatID, tgt.BaseURL, tgt.HTTPClient)

	for i, p := range parts {
		name := base
		if len(parts) > 1 {
			name = fmt.Sprintf("%s%s%03d", base, partInfix, i+1)
		}
		sum := sha256.Sum256(p)
		caption := watchdog.Render(
			watchdog.MsgBackupPart(name, i+1, len(parts), int64(len(p)), hex.EncodeToString(sum[:])), lang)
		if err := tg.sendDocument(ctx, name, p, caption); err != nil {
			return fmt.Errorf("send part %d/%d: %w", i+1, len(parts), err)
		}
	}

	manifest := watchdog.Render(
		watchdog.MsgBackupDelivered(base, int64(len(blob)), wholeSum, len(parts)), lang)
	if err := tg.sendMessage(ctx, manifest); err != nil {
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
