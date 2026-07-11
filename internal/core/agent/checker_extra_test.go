package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFirstTomlValue(t *testing.T) {
	d := t.TempDir()
	os.WriteFile(filepath.Join(d, "hosts.toml"), []byte("# c\nhostname = \"example.com\"\nhostname = \"other\"\n"), 0o644)
	os.WriteFile(filepath.Join(d, "credentials.toml"), []byte("[[client]]\nusername = \"alice\"\npassword = \"x\"\n"), 0o644)
	if v := firstTomlValue(filepath.Join(d, "hosts.toml"), "hostname"); v != "example.com" {
		t.Fatalf("host=%q", v)
	}
	if v := firstTomlValue(filepath.Join(d, "credentials.toml"), "username"); v != "alice" {
		t.Fatalf("client=%q", v)
	}
	if v := firstTomlValue(filepath.Join(d, "missing.toml"), "x"); v != "" {
		t.Fatalf("want empty, got %q", v)
	}
}
