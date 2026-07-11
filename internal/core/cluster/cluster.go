// Package cluster holds the runner-agnostic core that turns a registered exit
// node into a control-plane standby: a Postgres streaming replica, an
// out-of-band CA-key holder, and a staged (disabled) serve unit plus an enabled
// watchdog. The steps are split so two callers can share them:
//
//   - the break-glass CLI `trustpanel cluster add-standby` (root on the primary,
//     SSH to the standby), and
//   - the panel's UI-triggered flow, where the privileged work runs inside the
//     per-node root agents (primary-side locally on the primary's agent;
//     standby-side locally on the standby's agent), so the CA private key flows
//     only primary-agent -> standby-agent over mTLS and never through the
//     non-root panel process.
//
// The functions take injected runners so the flow is unit-testable with fakes.
package cluster

import (
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"trustpanel/internal/core/agentapi"
	"trustpanel/internal/core/bootstrap"
	"trustpanel/internal/core/model"
	"trustpanel/internal/core/provision"
)

// Params is the fully-resolved input to the add-standby steps (no flag parsing,
// no live file/DSN lookups), so the core flow is unit-testable with fakes. The
// controller cert is pre-minted by the caller (the CLI / the primary agent, both
// of which hold the CA) — StageControlPlane only writes it.
type Params struct {
	PrimaryID, StandbyID string
	PrimaryIP, StandbyIP string
	StandbyIPs           []net.IP // controller cert SANs (informational here)
	ReplUser, ReplPass   string
	ReplSlot             string
	ServeEnv, DeployEnv  []byte
	CACertPEM            []byte // staged as ca.crt on the standby
	CAKeyPEM             []byte // crown jewel, staged 0600 out-of-band
	ControllerCert       []byte // standby's pre-minted controller cert
	ControllerKey        []byte // standby's pre-minted controller key
	Binary, TrustTunnel  []byte
	Layout               bootstrap.Layout
	Validity             time.Duration
}

// CmdRunner is the subset of a runner the primary side needs (local, root): just
// command execution.
type CmdRunner interface {
	Run(ctx context.Context, cmd string) (string, error)
}

// Store is the slice of the panel store add-standby touches (bookkeeping).
type Store interface {
	ControlPlane(ctx context.Context) (model.ControlPlane, error)
	LoadState(ctx context.Context) (model.State, error)
	UpsertNode(ctx context.Context, n model.Node) error
	SetStandbys(ctx context.Context, ids []string) error
}

// AddStandby is the all-in-one orchestrator used by the break-glass CLI: it runs
// every step in one process (primary-side via prim, standby-side via sb, then DB
// bookkeeping via st). The agent-routed path calls the individual steps on the
// machine that owns each side instead.
func AddStandby(ctx context.Context, prim CmdRunner, sb provision.Runner, st Store, p Params, log func(string)) error {
	step := stepper(log)
	if err := ConfigurePrimary(ctx, prim, p, step); err != nil {
		return err
	}
	if err := MakeReplica(ctx, sb, p, step); err != nil {
		return err
	}
	if err := StageControlPlane(ctx, sb, p, step); err != nil {
		return err
	}
	if err := RecordTopology(ctx, st, p, step); err != nil {
		return err
	}
	if out, err := Verify(ctx, prim); err == nil && out != "" {
		step("primary pg_stat_replication: " + out)
	}
	step("standby ready (promote with: trustpanel promote --node-id " + p.StandbyID + " --pg-promote --start-serve)")
	return nil
}

// ParamsFromPrimaryApply maps the wire payload to the subset of Params that
// ConfigurePrimary/Verify need, shared by the agent and the privileged helper.
func ParamsFromPrimaryApply(in agentapi.PrimaryApplyInput, layout bootstrap.Layout) Params {
	return Params{
		PrimaryID: in.PrimaryID, StandbyID: in.StandbyID,
		PrimaryIP: in.PrimaryIP, StandbyIP: in.StandbyIP,
		ReplUser: in.ReplUser, ReplPass: in.ReplPass, ReplSlot: in.ReplSlot,
		Layout: layout,
	}
}

// ApplyReplica runs the standby side (MakeReplica + StageControlPlane) from the
// secret bundle. Like ApplyPrimary it is the privileged half the standby agent
// cannot run inside its sandbox: the agent pipes the bundle to a transient root
// unit (`trustpanel cluster _replica-apply`), and the same code is the in-process
// fallback. The trustpanel binary staged on the standby is this process's own
// executable (version-matched), read here rather than shipped over the wire.
func ApplyReplica(ctx context.Context, sb provision.Runner, in agentapi.PrepareReplicaRequest, layout bootstrap.Layout, step func(string)) error {
	dec := func(field, b64 string) ([]byte, error) {
		out, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			return nil, fmt.Errorf("decode %s: %w", field, err)
		}
		return out, nil
	}
	serveEnv, err := dec("serve_env", in.ServeEnv)
	if err != nil {
		return err
	}
	deployEnv, err := dec("deploy_env", in.DeployEnv)
	if err != nil {
		return err
	}
	caCert, err := dec("ca_cert_pem", in.CACertPEM)
	if err != nil {
		return err
	}
	caKey, err := dec("ca_key_pem", in.CAKeyPEM)
	if err != nil {
		return err
	}
	ctrlCert, err := dec("ctrl_cert", in.CtrlCert)
	if err != nil {
		return err
	}
	ctrlKey, err := dec("ctrl_key", in.CtrlKey)
	if err != nil {
		return err
	}
	var binary []byte
	if exe, e := os.Executable(); e == nil {
		binary, _ = os.ReadFile(exe)
	}
	p := Params{
		PrimaryID: in.PrimaryID, StandbyID: in.StandbyID,
		PrimaryIP: in.PrimaryIP, StandbyIP: in.StandbyIP,
		ReplUser: in.ReplUser, ReplPass: in.ReplPass, ReplSlot: in.ReplSlot,
		ServeEnv: serveEnv, DeployEnv: deployEnv,
		CACertPEM: caCert, CAKeyPEM: caKey,
		ControllerCert: ctrlCert, ControllerKey: ctrlKey,
		Binary: binary,
		Layout: layout,
	}
	if err := MakeReplica(ctx, sb, p, step); err != nil {
		return err
	}
	return StageControlPlane(ctx, sb, p, step)
}

// ConfigurePrimary opens Postgres replication on the primary for the standby IP:
// replication role + slot, wal_level/listen_addresses, an IP-scoped pg_hba rule,
// a ufw allow, then reload/restart. Idempotent.
func ConfigurePrimary(ctx context.Context, prim CmdRunner, p Params, step func(string)) error {
	step = stepper2(step)
	// Validate every value interpolated into the SQL/shell below so a malformed
	// slot name or IP can never reach `psql` or the shell. These are fleet-internal,
	// admin-only values, but pinning their shape removes the injection surface
	// outright (ReplUser/ReplPass are additionally handled at their use site).
	if !safeIdent(p.ReplSlot) {
		return fmt.Errorf("unsafe replication slot name %q (allowed: letters, digits, underscore; must not start with a digit)", p.ReplSlot)
	}
	if net.ParseIP(p.PrimaryIP) == nil {
		return fmt.Errorf("invalid primary IP %q", p.PrimaryIP)
	}
	if net.ParseIP(p.StandbyIP) == nil {
		return fmt.Errorf("invalid standby IP %q", p.StandbyIP)
	}
	needRestart := false

	la, err := psqlQuery(ctx, prim, "SHOW listen_addresses")
	if err != nil {
		return fmt.Errorf("read listen_addresses: %w", err)
	}
	if la != "*" && !strings.Contains(la, p.PrimaryIP) {
		if err := psqlExec(ctx, prim, "ALTER SYSTEM SET listen_addresses = 'localhost,"+p.PrimaryIP+"'"); err != nil {
			return fmt.Errorf("set listen_addresses: %w", err)
		}
		needRestart = true
		step("primary listen_addresses -> localhost," + p.PrimaryIP + " (restart required)")
	}

	wl, err := psqlQuery(ctx, prim, "SHOW wal_level")
	if err != nil {
		return fmt.Errorf("read wal_level: %w", err)
	}
	if wl != "replica" && wl != "logical" {
		if err := psqlExec(ctx, prim, "ALTER SYSTEM SET wal_level = 'replica'"); err != nil {
			return fmt.Errorf("set wal_level: %w", err)
		}
		needRestart = true
		step("primary wal_level -> replica (restart required)")
	}

	// Replication role (idempotent; always (re)set the password so a re-run stays
	// in sync with the conninfo written to the standby). ReplUser is interpolated
	// as a SQL identifier (DDL can't parametrize it), so validate its charset; the
	// password is a SQL string literal, so escape any single quotes. Both are
	// constrained in practice ("replicator" + a hex password), but this keeps the
	// step safe if a caller ever passes something exotic.
	if !safeIdent(p.ReplUser) {
		return fmt.Errorf("unsafe replication role name %q (allowed: letters, digits, underscore; must not start with a digit)", p.ReplUser)
	}
	pw := strings.ReplaceAll(p.ReplPass, "'", "''")
	roleSQL := fmt.Sprintf(
		"DO $$ BEGIN IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname='%s') THEN CREATE ROLE %s WITH REPLICATION LOGIN PASSWORD '%s'; ELSE ALTER ROLE %s WITH REPLICATION LOGIN PASSWORD '%s'; END IF; END $$;",
		p.ReplUser, p.ReplUser, pw, p.ReplUser, pw)
	if err := psqlExec(ctx, prim, roleSQL); err != nil {
		return fmt.Errorf("create replication role: %w", err)
	}
	step("replication role ready: " + p.ReplUser)

	// Physical replication slot (idempotent).
	slotSQL := fmt.Sprintf(
		"DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM pg_replication_slots WHERE slot_name='%s') THEN PERFORM pg_create_physical_replication_slot('%s'); END IF; END $$;",
		p.ReplSlot, p.ReplSlot)
	if err := psqlExec(ctx, prim, slotSQL); err != nil {
		return fmt.Errorf("create replication slot: %w", err)
	}
	step("replication slot ready: " + p.ReplSlot)

	// pg_hba: allow the standby IP to replicate (prefer hostssl + scram).
	hbaFile, err := psqlQuery(ctx, prim, "SHOW hba_file")
	if err != nil {
		return fmt.Errorf("locate pg_hba.conf: %w", err)
	}
	conn := "hostssl"
	if ssl, _ := psqlQuery(ctx, prim, "SHOW ssl"); ssl != "on" {
		conn = "host"
		step("WARNING: primary SSL is off — replication will be unencrypted (host, not hostssl)")
	}
	rule := fmt.Sprintf("%s replication %s %s/32 scram-sha-256", conn, p.ReplUser, p.StandbyIP)
	hbaCmd := fmt.Sprintf("grep -qxF %s %s || printf '%%s\\n' %s >> %s",
		shq(rule), shq(hbaFile), shq(rule), shq(hbaFile))
	if _, err := prim.Run(ctx, hbaCmd); err != nil {
		return fmt.Errorf("append pg_hba rule: %w", err)
	}
	step("pg_hba rule ensured for " + p.StandbyIP + "/32")

	// ufw (only if active).
	if _, err := prim.Run(ctx, fmt.Sprintf(
		"if command -v ufw >/dev/null && ufw status 2>/dev/null | grep -q 'Status: active'; then ufw allow from %s to any port 5432 proto tcp; fi", p.StandbyIP)); err != nil {
		return fmt.Errorf("ufw allow: %w", err)
	}

	if needRestart {
		if _, err := prim.Run(ctx, "systemctl restart postgresql"); err != nil {
			return fmt.Errorf("restart postgresql: %w", err)
		}
		step("primary postgresql restarted (config applied)")
	} else {
		if err := psqlExec(ctx, prim, "SELECT pg_reload_conf()"); err != nil {
			return fmt.Errorf("reload postgres config: %w", err)
		}
		step("primary postgres config reloaded")
	}
	return nil
}

// MakeReplica wipes the standby's Postgres data dir and re-seeds it from the
// primary via pg_basebackup, leaving it a streaming replica (standby.signal +
// primary_conninfo + slot).
func MakeReplica(ctx context.Context, sb provision.Runner, p Params, step func(string)) error {
	step = stepper2(step)
	if _, err := sb.Run(ctx, "systemctl --version"); err != nil {
		return fmt.Errorf("standby preflight: systemd not available: %w", err)
	}
	if _, err := sb.Run(ctx, "command -v pg_basebackup >/dev/null || (DEBIAN_FRONTEND=noninteractive apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq postgresql)"); err != nil {
		return fmt.Errorf("install postgresql on standby: %w", err)
	}
	step("standby postgresql present")

	conninfo := fmt.Sprintf("host=%s port=5432 user=%s password=%s sslmode=require",
		p.PrimaryIP, p.ReplUser, p.ReplPass)
	// Works whether or not a Debian cluster already exists: pick the highest
	// installed version, create+register a 'main' cluster if missing, then wipe
	// its data dir and re-seed it from the primary via pg_basebackup. -R writes
	// standby.signal + primary_conninfo + primary_slot_name into the data dir.
	// We strip any inherited listen_addresses (the primary's basebackup carries
	// its own ALTER SYSTEM value, which the standby cannot bind) so the replica
	// listens on localhost only — serve reaches it at 127.0.0.1 after promote.
	script := `set -e
systemctl enable postgresql >/dev/null 2>&1 || true
VER=$(ls /usr/lib/postgresql 2>/dev/null | sort -n | tail -1)
[ -z "$VER" ] && { echo "no postgresql installed" >&2; exit 1; }
if ! pg_lsclusters -h 2>/dev/null | awk '{print $1,$2}' | grep -qx "$VER main"; then
  pg_createcluster "$VER" main
fi
# postgresql.service is a meta-unit that doesn't itself persist across
# reboots — enable the actual versioned per-cluster unit too, or the replica
# silently stays down after the standby box restarts.
systemctl enable "postgresql@${VER}-main" >/dev/null 2>&1 || true
PGDATA=$(pg_lsclusters -h | awk -v v="$VER" '$1==v && $2=="main"{print $6}')
[ -z "$PGDATA" ] && PGDATA="/var/lib/postgresql/$VER/main"
pg_ctlcluster "$VER" main stop 2>/dev/null || systemctl stop postgresql 2>/dev/null || true
rm -rf "$PGDATA"
install -d -o postgres -g postgres -m 0700 "$PGDATA"
runuser -u postgres -- pg_basebackup -D "$PGDATA" -Fp -Xs -P -R -S ` + shq(p.ReplSlot) + ` -d ` + shq(conninfo) + `
sed -i '/listen_addresses/d' "$PGDATA/postgresql.auto.conf" 2>/dev/null || true
pg_ctlcluster "$VER" main start || systemctl start postgresql
`
	if _, err := sb.Run(ctx, script); err != nil {
		return fmt.Errorf("pg_basebackup / start replica: %w", err)
	}
	rec, err := sb.Run(ctx, "for i in 1 2 3 4 5 6 7 8 9 10; do r=$(runuser -u postgres -- psql -tAc 'SELECT pg_is_in_recovery()' 2>/dev/null); [ \"$r\" = t ] && break; sleep 1; done; echo $r")
	if err != nil || strings.TrimSpace(rec) != "t" {
		return fmt.Errorf("standby did not enter recovery (pg_is_in_recovery=%q, err=%v)", strings.TrimSpace(rec), err)
	}
	step("standby is a streaming replica (pg_is_in_recovery=t)")
	return nil
}

// StageControlPlane drops the out-of-band control-plane material on the standby
// (binary, CA cert+key, the pre-minted controller cert/key, serve/watchdog
// units, migrations) and leaves serve DISABLED + the watchdog ENABLED. The
// caller must have set p.ControllerCert/Key and p.CACertPEM.
func StageControlPlane(ctx context.Context, sb provision.Runner, p Params, step func(string)) error {
	step = stepper2(step)
	l := p.Layout

	// System user + directories (idempotent).
	if _, err := sb.Run(ctx, "id trustpanel >/dev/null 2>&1 || useradd --system --home "+l.VarLib+" --shell /usr/sbin/nologin trustpanel"); err != nil {
		return fmt.Errorf("create trustpanel user: %w", err)
	}
	dirs := strings.Join([]string{l.PKIDir, l.MigrationsDir(), l.UnitsStageDir(), l.BinDir, l.VarLib, l.LogDir, l.EtcDir}, " ")
	if _, err := sb.Run(ctx, "mkdir -p "+dirs); err != nil {
		return fmt.Errorf("mkdir on standby: %w", err)
	}
	if _, err := sb.Run(ctx, "chown trustpanel:trustpanel "+l.VarLib+" "+l.LogDir); err != nil {
		return fmt.Errorf("chown standby dirs: %w", err)
	}

	type putFile struct {
		path string
		body []byte
		mode os.FileMode
	}
	files := []putFile{
		{l.PKIDir + "/ca.crt", p.CACertPEM, 0o644},
		{l.PKIDir + "/ca.key", p.CAKeyPEM, 0o600}, // crown jewel, out-of-band
		{l.PKIDir + "/controller.crt", p.ControllerCert, 0o644},
		{l.PKIDir + "/controller.key", p.ControllerKey, 0o600},
		{l.EtcDir + "/serve.env", p.ServeEnv, 0o640},
		// agent.env carries this node's id so `trustpanel promote` (the bare command
		// the failover alert suggests) can self-resolve --node-id. An exit passes
		// --node-id as a unit flag and ships no agent.env, so without this the bare
		// promote aborts "node-id could not be resolved" on a real standby.
		{l.EtcDir + "/agent.env", []byte(bootstrap.AgentEnv(p.StandbyID)), 0o644},
		// 0640 root:trustpanel (chowned below): it carries TRUSTPANEL_DEADMAN_DSN,
		// the local replica DSN, which includes the DB password — must not be
		// world-readable (serve.env, with the same DSN, is already 0640).
		{l.EtcDir + "/watchdog.env", []byte(bootstrap.WatchdogEnv(p.PrimaryIP, p.PrimaryID, dsnFromEnv(p.ServeEnv))), 0o640},
		{l.VarLib + "/known_hosts", []byte{}, 0o600},
		{l.SystemdDir + "/trustpanel-serve.service", []byte(l.ServeUnit()), 0o644},
		// The management bot follows the active control plane: stage it (disabled,
		// like serve) so a later `promote --start-serve` can bring it up on the new
		// primary. Without this the standby had no bot unit at all, so the Telegram
		// management bot vanished after failover.
		{l.SystemdDir + "/trustpanel-bot.service", []byte(l.BotUnit()), 0o644},
		{l.SystemdDir + "/trustpanel-watchdog.service", []byte(l.WatchdogUnit()), 0o644},
		{l.SystemdDir + "/trustpanel-backup.service", []byte(l.BackupUnit()), 0o644},
		{l.SystemdDir + "/trustpanel-backup.timer", []byte(l.BackupTimer()), 0o644},
		{l.SystemdDir + "/trustpanel-verify-restore.service", []byte(l.VerifyUnit()), 0o644},
		{l.SystemdDir + "/trustpanel-verify-restore.timer", []byte(l.VerifyTimer()), 0o644},
	}
	if len(p.Binary) > 0 {
		files = append(files, putFile{l.BinDir + "/trustpanel", p.Binary, 0o755})
	}
	if len(p.DeployEnv) > 0 {
		files = append(files, putFile{l.EtcDir + "/deployment.env", p.DeployEnv, 0o644})
	}
	if len(p.TrustTunnel) > 0 {
		files = append(files, putFile{l.ShareDir + "/trusttunnel_endpoint", p.TrustTunnel, 0o755})
	}
	for _, m := range bootstrap.Migrations() {
		files = append(files, putFile{l.MigrationsDir() + "/" + m.Name, []byte(m.SQL), 0o644})
	}
	for name, content := range bootstrap.EntryUnitTemplates() {
		files = append(files, putFile{l.UnitsStageDir() + "/" + name, content, 0o644})
	}
	for _, f := range files {
		if err := sb.Put(ctx, f.body, f.path, f.mode); err != nil {
			return fmt.Errorf("push %s: %w", f.path, err)
		}
	}
	step("staged CA material, serve/watchdog units, migrations on standby")

	// Ownership: serve runs as trustpanel and must read ca.key/controller.key.
	if _, err := sb.Run(ctx, "chown -R trustpanel:trustpanel "+l.PKIDir); err != nil {
		return fmt.Errorf("chown pki: %w", err)
	}
	if _, err := sb.Run(ctx, "chown root:trustpanel "+l.EtcDir+"/serve.env && chmod 0640 "+l.EtcDir+"/serve.env"); err != nil {
		return fmt.Errorf("chown serve.env: %w", err)
	}
	// watchdog.env carries the deadman DSN (DB password) and the watchdog runs as
	// the trustpanel user — root:trustpanel 0640, same as serve.env (not world-read).
	if _, err := sb.Run(ctx, "chown root:trustpanel "+l.EtcDir+"/watchdog.env && chmod 0640 "+l.EtcDir+"/watchdog.env"); err != nil {
		return fmt.Errorf("chown watchdog.env: %w", err)
	}
	if _, err := sb.Run(ctx, "chown trustpanel:trustpanel "+l.VarLib+"/known_hosts"); err != nil {
		return fmt.Errorf("chown known_hosts: %w", err)
	}

	// serve AND the management bot stay DISABLED (a replica DB is read-only; both
	// come up only on promote — the bot follows the active control plane);
	// the watchdog runs now to alert if the primary disappears.
	if _, err := sb.Run(ctx, "systemctl daemon-reload"); err != nil {
		return fmt.Errorf("daemon-reload: %w", err)
	}
	if _, err := sb.Run(ctx, "systemctl disable trustpanel-serve.service >/dev/null 2>&1 || true"); err != nil {
		return fmt.Errorf("disable serve: %w", err)
	}
	if _, err := sb.Run(ctx, "systemctl disable --now trustpanel-bot.service >/dev/null 2>&1 || true"); err != nil {
		return fmt.Errorf("disable bot: %w", err)
	}
	if _, err := sb.Run(ctx, "systemctl enable --now trustpanel-watchdog.service"); err != nil {
		return fmt.Errorf("enable watchdog: %w", err)
	}

	// Daily backup also runs on the standby: pg_dump works against the read-only
	// replica, so a local snapshot (and, if configured, the off-site Telegram copy)
	// keeps being produced even after the primary is gone — the whole point of
	// Layer 1. The backup dir is root-only (it holds the CA key + user secrets).
	if _, err := sb.Run(ctx, "install -d -m 0700 /var/backups/trustpanel"); err != nil {
		return fmt.Errorf("create backup dir: %w", err)
	}
	if _, err := sb.Run(ctx, "systemctl enable --now trustpanel-backup.timer"); err != nil {
		return fmt.Errorf("enable backup timer: %w", err)
	}
	// The standby also verifies its own snapshots (Layer 4) — restore-into-throwaway
	// drill, alerting on failure. Harmless extra coverage and it survives a role swap.
	if _, err := sb.Run(ctx, "systemctl enable --now trustpanel-verify-restore.timer"); err != nil {
		return fmt.Errorf("enable verify-restore timer: %w", err)
	}
	step("serve staged (disabled); watchdog + backup + verify-restore timers enabled (cadence set in the panel)")
	return nil
}

// dsnFromEnv extracts TRUSTPANEL_DSN from a serve.env blob. On the standby this
// DSN points at 127.0.0.1 — its own local replica — which is exactly where the
// dead-man reads the replicated alert heartbeat.
func dsnFromEnv(env []byte) string {
	for _, line := range strings.Split(string(env), "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "TRUSTPANEL_DSN="); ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// RecordTopology marks the primary/replica pg_role, flags the standby
// mgmt_capable, and records it in control_plane.standby_node_ids.
func RecordTopology(ctx context.Context, st Store, p Params, step func(string)) error {
	step = stepper2(step)
	state, err := st.LoadState(ctx)
	if err != nil {
		return fmt.Errorf("reload state: %w", err)
	}
	prim, ok := state.NodeByID(p.PrimaryID)
	if !ok {
		return fmt.Errorf("primary %q vanished from state", p.PrimaryID)
	}
	prim.PGRole = model.PGPrimary
	if err := st.UpsertNode(ctx, prim); err != nil {
		return fmt.Errorf("mark primary pg_role: %w", err)
	}
	sbNode, ok := state.NodeByID(p.StandbyID)
	if !ok {
		return fmt.Errorf("standby %q vanished from state", p.StandbyID)
	}
	sbNode.PGRole = model.PGReplica
	sbNode.MgmtCapable = true
	if err := st.UpsertNode(ctx, sbNode); err != nil {
		return fmt.Errorf("mark standby pg_role: %w", err)
	}
	cp, err := st.ControlPlane(ctx)
	if err != nil {
		return fmt.Errorf("read control_plane: %w", err)
	}
	// Add the standby, but also PRUNE the current primary: after a role swap the
	// old standby becomes primary while lingering in standby_node_ids, which would
	// make findPrimary ambiguous / let the primary be treated as its own standby.
	standbys := remove(AppendUnique(cp.StandbyNodeIDs, p.StandbyID), p.PrimaryID)
	if err := st.SetStandbys(ctx, standbys); err != nil {
		return fmt.Errorf("record standby: %w", err)
	}
	step("topology recorded: primary=" + p.PrimaryID + " replica=" + p.StandbyID)
	return nil
}

// Verify returns the primary's pg_stat_replication as a one-liner (best effort).
func Verify(ctx context.Context, prim CmdRunner) (string, error) {
	out, err := psqlQuery(ctx, prim, "SELECT client_addr, state, sync_state FROM pg_stat_replication")
	if err != nil {
		return "", err
	}
	return oneLine(out), nil
}

// ---- helpers ----

func stepper(log func(string)) func(string) {
	return func(s string) {
		if log != nil {
			log(s)
		}
	}
}

// stepper2 normalises a possibly-nil step into a non-nil one.
func stepper2(step func(string)) func(string) {
	if step != nil {
		return step
	}
	return func(string) {}
}

// psqlQuery runs a single-value SHOW/SELECT as the postgres superuser. It cds to
// /tmp first: a root caller's cwd (e.g. /home/<user>) is unreadable by the
// postgres user, and that warning would otherwise pollute the captured output
// (the local runner merges stderr). lastLine guards against any remaining noise.
func psqlQuery(ctx context.Context, r CmdRunner, sql string) (string, error) {
	out, err := r.Run(ctx, "cd /tmp && sudo -u postgres psql -tAc "+shq(sql))
	return lastLine(out), err
}

func psqlExec(ctx context.Context, r CmdRunner, sql string) error {
	_, err := r.Run(ctx, "cd /tmp && sudo -u postgres psql -v ON_ERROR_STOP=1 -c "+shq(sql))
	return err
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

func oneLine(s string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(s, "\n", " ")), " ")
}

// SlotName derives a Postgres-safe physical replication slot name from a node id.
func SlotName(nodeID string) string {
	var b strings.Builder
	b.WriteString("standby_")
	for _, r := range strings.ToLower(nodeID) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	return b.String()
}

// AppendUnique appends id to ids if not already present.
func AppendUnique(ids []string, id string) []string {
	for _, x := range ids {
		if x == id {
			return ids
		}
	}
	return append(append([]string{}, ids...), id)
}

// remove returns ids without any occurrence of id (nil-safe, preserves order).
func remove(ids []string, id string) []string {
	out := make([]string, 0, len(ids))
	for _, x := range ids {
		if x != id {
			out = append(out, x)
		}
	}
	return out
}

// shq single-quotes a string for safe use as one shell argument.
func shq(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// safeIdent reports whether s is a safe bare SQL identifier (letters, digits,
// underscore; not starting with a digit). Used to gate values interpolated into
// DDL, which cannot be parametrized.
func safeIdent(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '_':
		case r >= '0' && r <= '9':
			if i == 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// Summary renders the operator-facing summary printed after add-standby.
func Summary(p Params) string {
	var b strings.Builder
	fmt.Fprintln(&b, "\n========================================================================")
	fmt.Fprintln(&b, " Standby prepared.")
	fmt.Fprintln(&b, "========================================================================")
	fmt.Fprintf(&b, " Primary:   %s (%s)\n", p.PrimaryID, p.PrimaryIP)
	fmt.Fprintf(&b, " Standby:   %s (%s) — streaming replica, slot %s\n", p.StandbyID, p.StandbyIP, p.ReplSlot)
	fmt.Fprintln(&b, " On the standby: serve is staged but DISABLED; watchdog and the daily")
	fmt.Fprintln(&b, "   backup timer are ENABLED (pg_dump runs against the replica).")
	fmt.Fprintln(&b, "------------------------------------------------------------------------")
	fmt.Fprintln(&b, " Alert bot (β): the standby watchdog resolves its alert-bot token LIVE")
	fmt.Fprintln(&b, "   from the replica (the same alert bot configured in the panel) — no")
	fmt.Fprintln(&b, "   manual step. Just set a DEDICATED alert bot (distinct from the mgmt")
	fmt.Fprintln(&b, "   bot) under Settings → Bots in the panel; it replicates to the standby.")
	fmt.Fprintln(&b, "   (watchdog.env TRUSTPANEL_ALERT_TG_TOKEN/_CHAT_ID stay an optional")
	fmt.Fprintln(&b, "   fallback for when the replica is unreachable.) The dead-man DSN is")
	fmt.Fprintln(&b, "   filled automatically; β watches the primary's Postgres AND its heartbeat.")
	fmt.Fprintln(&b, " FAILOVER IS MANUAL. When the primary is confirmed gone, on the standby:")
	fmt.Fprintf(&b, "   trustpanel promote --node-id %s --pg-promote --start-serve\n", p.StandbyID)
	fmt.Fprintln(&b, " NOTE: the CA private key now also lives on the standby (required for it")
	fmt.Fprintln(&b, "   to act as a control plane). Keep the box as protected as the primary.")
	fmt.Fprintln(&b, "========================================================================")
	return b.String()
}
