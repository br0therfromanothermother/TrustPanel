package panel

import "testing"

// TestRedactTokenStripsTokenFromTransportError guards against a real leak: a
// failed getMe request's Go error embeds the full request URL verbatim
// (https://api.telegram.org/bot<TOKEN>/getMe), so the raw token must never
// reach detail text that later gets sent to the Telegram alert channel.
func TestRedactTokenStripsTokenFromTransportError(t *testing.T) {
	const token = "111111111:TEST-fake-token-not-a-real-credential-000"
	raw := `Get "https://api.telegram.org/bot` + token + `/getMe": read tcp [::1]:1->[::1]:443: read: connection reset by peer`

	got := redactToken(raw, token)
	if got == raw {
		t.Fatal("redactToken did not modify the string")
	}
	for i := 0; i+len(token) <= len(got); i++ {
		if got[i:i+len(token)] == token {
			t.Fatalf("token still present in redacted text: %q", got)
		}
	}
}

func TestRedactTokenNoOpOnEmptyToken(t *testing.T) {
	if got := redactToken("some error", ""); got != "some error" {
		t.Errorf("redactToken with empty token should be a no-op, got %q", got)
	}
}
