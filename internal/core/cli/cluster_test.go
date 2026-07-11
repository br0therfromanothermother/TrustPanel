package cli

import (
	"strings"
	"testing"
)

func TestStartLocalServe(t *testing.T) {
	var name string
	var args []string
	if err := startLocalServe(func(n string, a ...string) ([]byte, error) {
		name, args = n, a
		return nil, nil
	}); err != nil {
		t.Fatalf("startLocalServe: %v", err)
	}
	if name != "systemctl" || strings.Join(args, " ") != "enable --now trustpanel-serve.service" {
		t.Errorf("startLocalServe ran %q %v", name, args)
	}
}

func TestDSNFromEnv(t *testing.T) {
	const want = "host=127.0.0.1 dbname=trustpanel"
	cases := map[string][]byte{
		"unquoted": []byte("# comment\nTRUSTPANEL_BRAND=x\nTRUSTPANEL_DSN=host=127.0.0.1 dbname=trustpanel\n"),
		// serve.env now quotes the value; the reader must strip the quotes
		// so the space-containing keyword DSN is returned intact.
		"double-quoted": []byte("TRUSTPANEL_DSN=\"host=127.0.0.1 dbname=trustpanel\"\n"),
		"single-quoted": []byte("TRUSTPANEL_DSN='host=127.0.0.1 dbname=trustpanel'\n"),
	}
	for name, env := range cases {
		if got := dsnFromEnv(env); got != want {
			t.Errorf("dsnFromEnv[%s] = %q, want %q", name, got, want)
		}
	}
}
