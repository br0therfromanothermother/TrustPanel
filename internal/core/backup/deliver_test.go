package backup

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"filippo.io/age"
)

// fakeTelegram captures sendDocument parts and the sendMessage manifest so a test
// can reassemble + decrypt what would have been pushed off-site.
type fakeTelegram struct {
	mu       sync.Mutex
	parts    map[string][]byte // filename -> bytes
	manifest string
	srv      *httptest.Server
}

func newFakeTelegram(t *testing.T) *fakeTelegram {
	t.Helper()
	f := &fakeTelegram{parts: map[string][]byte{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/sendDocument"):
			if err := r.ParseMultipartForm(64 << 20); err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			file, hdr, err := r.FormFile("document")
			if err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			defer file.Close()
			buf, _ := io.ReadAll(file)
			f.mu.Lock()
			f.parts[hdr.Filename] = buf
			f.mu.Unlock()
			fmt.Fprint(w, `{"ok":true}`)
		case strings.HasSuffix(r.URL.Path, "/sendMessage"):
			_ = r.ParseForm()
			f.mu.Lock()
			f.manifest = r.FormValue("text")
			f.mu.Unlock()
			fmt.Fprint(w, `{"ok":true}`)
		default:
			http.Error(w, "unexpected "+r.URL.Path, 404)
		}
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

// reassemble concatenates the captured parts in filename (== index) order.
func (f *fakeTelegram) reassemble() []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	names := make([]string, 0, len(f.parts))
	for n := range f.parts {
		names = append(names, n)
	}
	sort.Strings(names)
	var out []byte
	for _, n := range names {
		out = append(out, f.parts[n]...)
	}
	return out
}

func newAgeKeypair(t *testing.T) (*age.X25519Identity, string) {
	t.Helper()
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatalf("gen age identity: %v", err)
	}
	return id, id.Recipient().String()
}

func TestDeliverTelegramRoundTrip(t *testing.T) {
	id, recipient := newAgeKeypair(t)
	dir := t.TempDir()
	snap := filepath.Join(dir, "trustpanel-20260622-033000.tar.gz")
	payload := []byte(strings.Repeat("snapshot-bytes-", 5000)) // ~75KB
	if err := os.WriteFile(snap, payload, 0o600); err != nil {
		t.Fatal(err)
	}

	fk := newFakeTelegram(t)
	// Small chunk size forces multiple parts so splitting is exercised.
	err := DeliverTelegram(context.Background(), snap,
		TelegramTarget{Token: "T", ChatID: "-100", BaseURL: fk.srv.URL},
		DeliverOptions{AgeRecipient: recipient, ChunkBytes: 20 << 10})
	if err != nil {
		t.Fatalf("deliver: %v", err)
	}
	if len(fk.parts) < 2 {
		t.Fatalf("expected multiple parts, got %d", len(fk.parts))
	}
	if !strings.Contains(fk.manifest, "parts: ") || !strings.Contains(fk.manifest, "cipher_sha256") {
		t.Fatalf("manifest missing fields: %q", fk.manifest)
	}

	// Reassemble the ciphertext and decrypt with the matching identity.
	cipher := fk.reassemble()
	r, err := age.Decrypt(strings.NewReader(string(cipher)), id)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read decrypted: %v", err)
	}
	if string(got) != string(payload) {
		t.Fatalf("roundtrip mismatch: got %d bytes want %d", len(got), len(payload))
	}
}

func TestDeliverTelegramRequiresRecipient(t *testing.T) {
	dir := t.TempDir()
	snap := filepath.Join(dir, "trustpanel-x.tar.gz")
	_ = os.WriteFile(snap, []byte("x"), 0o600)
	err := DeliverTelegram(context.Background(), snap,
		TelegramTarget{Token: "T", ChatID: "-100"},
		DeliverOptions{AgeRecipient: ""})
	if err == nil || !strings.Contains(err.Error(), "age recipient is required") {
		t.Fatalf("expected recipient-required error, got %v", err)
	}
}

func TestRestoreFileReassemblesAndDecrypts(t *testing.T) {
	id, recipient := newAgeKeypair(t)

	// Produce parts via the real delivery path into a fake channel.
	srcDir := t.TempDir()
	snap := filepath.Join(srcDir, "trustpanel-20260622-040000.tar.gz")
	payload := []byte(strings.Repeat("DR-", 10000))
	if err := os.WriteFile(snap, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	fk := newFakeTelegram(t)
	if err := DeliverTelegram(context.Background(), snap,
		TelegramTarget{Token: "T", ChatID: "-100", BaseURL: fk.srv.URL},
		DeliverOptions{AgeRecipient: recipient, ChunkBytes: 8 << 10}); err != nil {
		t.Fatalf("deliver: %v", err)
	}

	// Simulate the operator downloading the parts into a directory.
	dlDir := t.TempDir()
	for name, body := range fk.parts {
		if err := os.WriteFile(filepath.Join(dlDir, name), body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// Write the age identity file (age-keygen format).
	idFile := filepath.Join(t.TempDir(), "key.txt")
	if err := os.WriteFile(idFile, []byte(id.String()+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(t.TempDir(), "restored.tar.gz")
	got, err := RestoreFile(dlDir, idFile, out)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	b, err := os.ReadFile(got)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != string(payload) {
		t.Fatalf("restored mismatch: %d vs %d bytes", len(b), len(payload))
	}
}

func TestCollectPartsRejectsMixedSnapshots(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "trustpanel-A.tar.gz"+partInfix+"001"), []byte("a"), 0o600)
	_ = os.WriteFile(filepath.Join(dir, "trustpanel-B.tar.gz"+partInfix+"001"), []byte("b"), 0o600)
	if _, err := collectParts(dir); err == nil || !strings.Contains(err.Error(), "multiple snapshots") {
		t.Fatalf("expected mixed-snapshot rejection, got %v", err)
	}
}

func TestSplitBytes(t *testing.T) {
	got := splitBytes([]byte("abcdefg"), 3)
	if len(got) != 3 || string(got[0]) != "abc" || string(got[2]) != "g" {
		t.Fatalf("unexpected split: %q", got)
	}
	if one := splitBytes(nil, 10); len(one) != 1 || len(one[0]) != 0 {
		t.Fatalf("empty input should yield one empty chunk, got %v", one)
	}
}
