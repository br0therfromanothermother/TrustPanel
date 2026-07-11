package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakePsql records the SQL it received and replies from a scripted queue of
// pg_is_in_recovery() answers, so promoteReplica's flow is testable without a DB.
type fakePsql struct {
	sql      []string
	recovery []string // successive answers for pg_is_in_recovery()
	promoted bool
}

func (f *fakePsql) run(sql string) (string, error) {
	f.sql = append(f.sql, sql)
	switch {
	case strings.Contains(sql, "pg_promote"):
		f.promoted = true
		return "t\n", nil
	case strings.Contains(sql, "pg_is_in_recovery"):
		if len(f.recovery) == 0 {
			return "f\n", nil
		}
		ans := f.recovery[0]
		f.recovery = f.recovery[1:]
		return ans + "\n", nil
	}
	return "", fmt.Errorf("unexpected sql: %s", sql)
}

func TestPromoteReplicaPromotesViaSuperuser(t *testing.T) {
	f := &fakePsql{recovery: []string{"t", "f"}} // in recovery, then primary after promote
	if err := promoteReplica(context.Background(), f.run, 5*time.Second); err != nil {
		t.Fatalf("promoteReplica: %v", err)
	}
	if !f.promoted {
		t.Fatal("pg_promote was never called")
	}
	// Must call pg_promote with an explicit wait (so it blocks until promoted),
	// never connect via the app DSN (this path only uses the injected runner).
	var sawPromote bool
	for _, s := range f.sql {
		if strings.Contains(s, "pg_promote(true,") {
			sawPromote = true
		}
	}
	if !sawPromote {
		t.Errorf("expected pg_promote(true, N), got %v", f.sql)
	}
}

func TestPromoteReplicaNoopWhenAlreadyPrimary(t *testing.T) {
	f := &fakePsql{recovery: []string{"f"}} // already out of recovery
	if err := promoteReplica(context.Background(), f.run, time.Second); err != nil {
		t.Fatalf("promoteReplica: %v", err)
	}
	if f.promoted {
		t.Error("pg_promote should not run on a node that is already primary")
	}
}

// fakeSystemctl records the systemctl invocations and can be scripted to fail,
// so the watchdog-disable step is testable without touching the host.
type fakeSystemctl struct {
	calls [][]string
	err   error
}

func (f *fakeSystemctl) run(name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, append([]string{name}, args...))
	return []byte("Unit trustpanel-watchdog.service not loaded."), f.err
}

func TestDisableLocalWatchdogIssuesDisableNow(t *testing.T) {
	f := &fakeSystemctl{}
	disableLocalWatchdog(f.run)
	if len(f.calls) != 1 {
		t.Fatalf("expected exactly one systemctl call, got %v", f.calls)
	}
	got := strings.Join(f.calls[0], " ")
	if got != "systemctl disable --now trustpanel-watchdog.service" {
		t.Errorf("wrong command: %q", got)
	}
}

func TestDisableLocalWatchdogIsBestEffort(t *testing.T) {
	// A node with no watchdog unit makes systemctl fail; promote must not abort.
	f := &fakeSystemctl{err: fmt.Errorf("exit status 1")}
	disableLocalWatchdog(f.run) // must not panic or fatal
	if len(f.calls) != 1 {
		t.Fatalf("expected the disable to still be attempted, got %v", f.calls)
	}
}

func TestStartLocalServeAndBotIssueEnableNow(t *testing.T) {
	// The management bot follows the active control plane, so promote must bring up
	// both serve and the bot with `enable --now`.
	for _, tc := range []struct {
		name string
		fn   func(func(string, ...string) ([]byte, error)) error
		want string
	}{
		{"serve", startLocalServe, "systemctl enable --now trustpanel-serve.service"},
		{"bot", startLocalBot, "systemctl enable --now trustpanel-bot.service"},
	} {
		f := &fakeSystemctl{}
		if err := tc.fn(f.run); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if len(f.calls) != 1 || strings.Join(f.calls[0], " ") != tc.want {
			t.Errorf("%s: got %v, want %q", tc.name, f.calls, tc.want)
		}
	}
}

func TestNodeIDFromEnvFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.env")
	if err := os.WriteFile(path, []byte("TRUSTPANEL_ACME_STAGING=1\nTRUSTPANEL_NODE_ID=\"exit-nl-a3f2\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := nodeIDFromEnvFile(path); got != "exit-nl-a3f2" {
		t.Errorf("node id = %q, want exit-nl-a3f2", got)
	}
	// Missing file or absent key resolves to "" (caller then requires --node-id).
	if got := nodeIDFromEnvFile(filepath.Join(dir, "nope.env")); got != "" {
		t.Errorf("missing file should yield empty, got %q", got)
	}
	bare := filepath.Join(dir, "bare.env")
	os.WriteFile(bare, []byte("TRUSTPANEL_ACME_EMAIL=a@b.c\n"), 0o644)
	if got := nodeIDFromEnvFile(bare); got != "" {
		t.Errorf("absent key should yield empty, got %q", got)
	}
}
