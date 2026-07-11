package cli

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"

	"trustpanel/internal/core/store"
)

// RunPromote performs the assisted promote: optionally promote the local
// Postgres replica to primary, then bump the control-plane
// epoch and set this node active. The operator runs this on the standby after
// confirming the old primary is gone (the human is the fence).
func RunPromote(args []string) {
	fs := flag.NewFlagSet("promote", flag.ExitOnError)
	dsn := fs.String("dsn", "", "Postgres DSN of the local node (or TRUSTPANEL_DSN, or /etc/trustpanel/serve.env)")
	nodeID := fs.String("node-id", "", "this node's id; if omitted, resolved from TRUSTPANEL_NODE_ID or /etc/trustpanel/agent.env")
	agentEnv := fs.String("agent-env", "/etc/trustpanel/agent.env", "agent.env to read this node's id from when --node-id is omitted")
	pgPromote := fs.Bool("pg-promote", false, "run pg_promote() on the local replica first")
	startServe := fs.Bool("start-serve", false, "enable + start trustpanel-serve.service locally after promote (brings the panel up on this node)")
	waitDur := fs.Duration("wait", 30*time.Second, "max wait for the replica to become primary")
	_ = fs.Parse(args)

	// Resolve the DSN via the shared resolver (flag → $TRUSTPANEL_DSN → the local
	// /etc/trustpanel/serve.env, whole-line so a space-containing keyword DSN is
	// not truncated). Under `sudo` the env is scrubbed and serve.env isn't
	// sourced, so a bare flag-or-env-only resolution aborts on a fully-provisioned
	// standby; reusing connDSN resolves it the same way every other subcommand does.
	resolvedDSN := connDSN(*dsn)
	// Self-resolve "which node am I" so a panicked operator on the standby can run
	// a bare `trustpanel promote` with no id to paste: the id was written to
	// agent.env at provision time (TRUSTPANEL_NODE_ID), and may also be in the env.
	id := *nodeID
	if id == "" {
		id = os.Getenv("TRUSTPANEL_NODE_ID")
	}
	if id == "" {
		id = nodeIDFromEnvFile(*agentEnv)
	}
	if resolvedDSN == "" || id == "" {
		log.Fatal("promote: --dsn (or TRUSTPANEL_DSN, or /etc/trustpanel/serve.env) is required, and --node-id could not be resolved (pass --node-id, or run on a provisioned node with /etc/trustpanel/agent.env)")
	}
	if *nodeID == "" {
		log.Printf("promote: resolved this node's id = %s", id)
	}
	ctx := context.Background()

	// Open + ping the store BEFORE the irreversible pg_promote(), so a bad or
	// truncated DSN (e.g. `user=root`) fails here rather than leaving a
	// half-promoted split state — PG primary but control_plane never flipped and
	// serve down. Pinging a replica is read-only, so this is safe before
	// promotion; the same pool stays valid once the node becomes primary.
	st, err := store.Open(ctx, resolvedDSN)
	if err != nil {
		log.Fatalf("promote: open store (check --dsn / serve.env): %v", err)
	}
	defer st.Close()

	if *pgPromote {
		if err := promoteReplica(ctx, psqlSuper, *waitDur); err != nil {
			log.Fatalf("promote: pg_promote: %v", err)
		}
		log.Printf("postgres replica promoted to primary")
	}

	epoch, err := st.Promote(ctx, id)
	if err != nil {
		log.Fatalf("promote: bump epoch: %v", err)
	}
	log.Printf("promoted: active=%s epoch=%d (agents now reject controllers below this epoch)", id, epoch)

	// This node is now the active control plane (primary). It must NOT also run the
	// standby watchdog: a leftover watchdog keeps probing the former primary (now
	// the standby) and fires misdirected "peer DOWN — run promote" alerts whenever
	// that peer's Postgres is briefly down — e.g. during its own basebackup rebuild
	// into a fresh standby. The standby is the one that watches the primary, so the
	// watchdog's owner is whichever node is the standby. Disable it best-effort
	// (a node with no watchdog unit is fine).
	disableLocalWatchdog(systemctlRun)

	if *startServe {
		// The standby's serve + bot units were staged disabled by `cluster
		// add-standby` (a replica DB is read-only). Now that this node is primary +
		// active, bring the panel up so the control plane is actually serving.
		if err := startLocalServe(systemctlRun); err != nil {
			log.Fatalf("promote: start serve: %v", err)
		}
		log.Printf("panel started: trustpanel-serve.service enabled + running")

		// The management bot follows the active control plane: bring it up
		// alongside serve so the Telegram management bot isn't gone after a
		// failover. Best-effort — serve (the control plane) is the critical
		// part; a missing/older bot unit must not abort the promote.
		if err := startLocalBot(systemctlRun); err != nil {
			log.Printf("promote: note: management bot not started (ok if this install predates the staged bot unit; run `systemctl enable --now trustpanel-bot.service`): %v", err)
		} else {
			log.Printf("management bot started: trustpanel-bot.service enabled + running")
		}
	}
	// NOTE: promote runs locally on the new primary and cannot reach the (assumed
	// dead) old primary, so it does not remotely fence its serve/bot. The epoch
	// fence already protects agents from a revived stale controller; the old
	// primary's serve/bot are then disabled when it is rebuilt as a standby via
	// `cluster add-standby` (which stages serve+bot disabled).
}

// nodeIDFromEnvFile reads TRUSTPANEL_NODE_ID from an agent.env-style file (one
// KEY=value per line). Returns "" if the file is missing or the key is absent.
func nodeIDFromEnvFile(path string) string {
	return envValueFromFile(path, "TRUSTPANEL_NODE_ID")
}

// envValueFromFile reads a single KEY's value from a systemd EnvironmentFile-style
// file (one KEY=value per line). It takes the WHOLE remainder of the line as the
// value — matching systemd's semantics, NOT bash `source` — so a space-containing
// keyword DSN like `TRUSTPANEL_DSN=host=127.0.0.1 port=5432 user=trustpanel ...`
// is read intact rather than truncated at the first space. Surrounding
// matched quotes are stripped. Returns "" if missing.
func envValueFromFile(path, key string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	prefix := key + "="
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, prefix) {
			v := strings.TrimSpace(strings.TrimPrefix(line, prefix))
			if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
				v = v[1 : len(v)-1]
			}
			return v
		}
	}
	return ""
}

// systemctlRun runs a systemctl command, returning combined output.
func systemctlRun(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).CombinedOutput()
}

// startLocalServe enables + starts the local panel unit. The command runner is
// injected so the command shape is unit-testable without touching the host.
func startLocalServe(run func(name string, args ...string) ([]byte, error)) error {
	out, err := run("systemctl", "enable", "--now", "trustpanel-serve.service")
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// startLocalBot enables + starts the local management-bot unit, co-locating the
// bot with serve on the active primary. Runner injected for testability.
func startLocalBot(run func(name string, args ...string) ([]byte, error)) error {
	out, err := run("systemctl", "enable", "--now", "trustpanel-bot.service")
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// disableLocalWatchdog stops + disables this node's standby watchdog after it has
// become the active primary. It is best-effort: a node that never had a watchdog
// unit (e.g. a non-HA control plane) returns an error from systemctl, which is
// expected and only noted. The command runner is injected for testability.
func disableLocalWatchdog(run func(name string, args ...string) ([]byte, error)) {
	if out, err := run("systemctl", "disable", "--now", "trustpanel-watchdog.service"); err != nil {
		log.Printf("promote: note: local watchdog not disabled (ok if none installed): %s", strings.TrimSpace(string(out)))
		return
	}
	log.Printf("local watchdog disabled (this node is now primary; the standby watches it)")
}

// promoteReplica runs pg_promote() and waits until the server leaves recovery.
//
// pg_promote() is superuser-only, so this MUST run as the postgres superuser via
// local peer auth (the injected psql runner) — NOT via the app DSN. The app role
// the panel/store use is not a superuser, so connecting with TRUSTPANEL_DSN here
// fails with "permission denied for function pg_promote" (SQLSTATE 42501).
func promoteReplica(ctx context.Context, psql func(sql string) (string, error), wait time.Duration) error {
	rec, err := psql("SELECT pg_is_in_recovery()")
	if err != nil {
		return fmt.Errorf("pg_is_in_recovery: %w (%s)", err, rec)
	}
	if lastLine(rec) != "t" {
		return nil // already primary (not in recovery)
	}
	secs := int(wait.Seconds())
	if secs < 1 {
		secs = 1
	}
	// pg_promote(wait => true, wait_seconds => secs) blocks server-side until the
	// node leaves recovery (or the timeout elapses); then re-verify locally.
	if out, err := psql(fmt.Sprintf("SELECT pg_promote(true, %d)", secs)); err != nil {
		return fmt.Errorf("pg_promote: %w (%s)", err, out)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		rec, err := psql("SELECT pg_is_in_recovery()")
		if err == nil && lastLine(rec) == "f" {
			return nil
		}
		if time.Now().After(deadline) {
			return context.DeadlineExceeded
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// psqlSuper runs a single SQL statement as the postgres superuser over local peer
// auth, returning combined output. cwd is /tmp because a root caller's home is
// typically unreadable by the postgres user, and that warning would otherwise
// pollute the captured output (use lastLine to read the result).
func psqlSuper(sql string) (string, error) {
	cmd := exec.Command("sudo", "-u", "postgres", "psql", "-tAXc", sql)
	cmd.Dir = "/tmp"
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// lastLine returns the last non-empty, trimmed line of s.
func lastLine(s string) string {
	lines := strings.Split(s, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if t := strings.TrimSpace(lines[i]); t != "" {
			return t
		}
	}
	return ""
}
