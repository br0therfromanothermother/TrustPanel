package bootstrap

import "fmt"

// Layout is the on-disk layout the bootstrap installs into. Matches the paths the
// agent/serve/render expect.
type Layout struct {
	BinDir     string // /usr/local/bin
	EtcDir     string // /etc/trustpanel
	PKIDir     string // /etc/trustpanel/pki
	SingBoxDir string // /etc/trustpanel/singbox
	ShareDir   string // /usr/local/share/trustpanel
	VarLib     string // /var/lib/trustpanel
	AgentState string // /var/lib/trustpanel-agent
	LogDir     string // /var/log/trustpanel
	SystemdDir string // /etc/systemd/system
}

// DefaultLayout returns the standard layout used across the project.
func DefaultLayout() Layout {
	return Layout{
		BinDir:     "/usr/local/bin",
		EtcDir:     "/etc/trustpanel",
		PKIDir:     "/etc/trustpanel/pki",
		SingBoxDir: "/etc/trustpanel/singbox",
		ShareDir:   "/usr/local/share/trustpanel",
		VarLib:     "/var/lib/trustpanel",
		AgentState: "/var/lib/trustpanel-agent",
		LogDir:     "/var/log/trustpanel",
		SystemdDir: "/etc/systemd/system",
	}
}

func (l Layout) MigrationsDir() string { return l.ShareDir + "/migrations" }
func (l Layout) UnitsStageDir() string { return l.ShareDir + "/systemd" } // entry templates for provisioning

// serveEnv is the EnvironmentFile for the panel: the Postgres DSN. The value is
// double-quoted so the space-containing keyword DSN survives a manual `source
// serve.env` intact (systemd strips the quotes; a bare `source` without them
// word-splits the value to `host=127.0.0.1` and half-promotes a failover).
func serveEnv(dsn string) string {
	return "TRUSTPANEL_DSN=\"" + dsn + "\"\n"
}

// deploymentEnv records the deployment branding + login subdomain so the panel
// (serve) and the camouflage origin (fallback) agree on the connection host.
// Entry provisioning reuses these values to stamp each entry's fallback.env.
func deploymentEnv(brand, domain, connectSubdomain string) string {
	if connectSubdomain == "" {
		connectSubdomain = DefaultConnectSubdomain
	}
	return fmt.Sprintf("TRUSTPANEL_BRAND=%s\nTRUSTPANEL_DOMAIN=%s\nTRUSTPANEL_CONNECT_SUBDOMAIN=%s\n",
		brand, domain, connectSubdomain)
}

// serveUnit is the control-plane panel unit (with remote provisioning enabled).
func (l Layout) serveUnit() string {
	return `[Unit]
Description=TrustPanel control plane (serve)
After=network-online.target postgresql.service
Wants=network-online.target postgresql.service

[Service]
Type=simple
User=trustpanel
Group=trustpanel
EnvironmentFile=` + l.EtcDir + `/serve.env
EnvironmentFile=-` + l.EtcDir + `/deployment.env
ExecStart=` + l.BinDir + `/trustpanel serve --listen 127.0.0.1:8787 \
  --migrations-dir ` + l.MigrationsDir() + ` \
  --ca-file ` + l.PKIDir + `/ca.crt --cert-file ` + l.PKIDir + `/controller.crt --key-file ` + l.PKIDir + `/controller.key \
  --ca-key-file ` + l.PKIDir + `/ca.key \
  --provision-singbox-bin ` + l.BinDir + `/sing-box \
  --provision-trusttunnel-bin ` + l.ShareDir + `/trusttunnel_endpoint \
  --provision-units-dir ` + l.UnitsStageDir() + ` \
  --provision-known-hosts ` + l.VarLib + `/known_hosts \
  --reconcile-interval 30s
Restart=on-failure
RestartSec=3
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=` + l.LogDir + ` ` + l.VarLib + `
LogsDirectory=trustpanel

[Install]
WantedBy=multi-user.target
`
}

// ServeUnit exposes the control-plane panel unit for reuse outside bootstrap
// (e.g. `cluster add-standby`, which installs it disabled on the standby so the
// panel only comes up on promote).
func (l Layout) ServeUnit() string { return l.serveUnit() }

// BotUnit exposes the management-bot unit for reuse outside bootstrap. Like
// ServeUnit it is installed disabled on the standby so the management bot only
// comes up on promote — the bot follows the active control plane.
func (l Layout) BotUnit() string { return l.botUnit() }

// WatchdogUnit is the control-plane watchdog: it probes the active
// primary's Postgres and alerts an operator (one-way) so they can run `promote`.
// It is installed on a standby (enabled) and never auto-promotes. All settings
// — which host to watch (TRUSTPANEL_WATCH_TCP), the target label
// (TRUSTPANEL_TARGET) and the optional alert channel — come from watchdog.env,
// which `cluster add-standby` writes. This is the canonical copy of the
// standalone deploy/systemd/trustpanel-watchdog.service.
func (l Layout) WatchdogUnit() string {
	return `[Unit]
Description=TrustPanel watchdog
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=trustpanel
Group=trustpanel
EnvironmentFile=` + l.EtcDir + `/watchdog.env
ExecStart=` + l.BinDir + `/trustpanel watchdog \
  --target ${TRUSTPANEL_TARGET} \
  --watch-tcp ${TRUSTPANEL_WATCH_TCP} \
  --interval 30s \
  --threshold 3 \
  --deadman-dsn ${TRUSTPANEL_DEADMAN_DSN} \
  --alert-telegram-token ${TRUSTPANEL_ALERT_TG_TOKEN} \
  --alert-chat-id ${TRUSTPANEL_ALERT_CHAT_ID}
Restart=on-failure
RestartSec=5
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true

[Install]
WantedBy=multi-user.target
`
}

// WatchdogEnv builds watchdog.env: the primary to probe, the local replica DSN
// for the alert dead-man, and (blank) alert-channel placeholders. The standby
// alert bot (β) is normally resolved live off the replica's settings (the same
// alert bot the panel manages), so these placeholders are an OPTIONAL fallback,
// used only when the replica is unreachable or no alert token is set in the
// panel. target is the primary's node id; primaryIP is the active Postgres host;
// deadmanDSN is the standby's own (127.0.0.1) replica DSN, off which both the
// dead-man heartbeat and β's live alert creds are read.
func WatchdogEnv(primaryIP, target, deadmanDSN string) string {
	return "TRUSTPANEL_TARGET=" + target + "\n" +
		"TRUSTPANEL_WATCH_TCP=" + primaryIP + ":5432\n" +
		"TRUSTPANEL_DEADMAN_DSN=" + deadmanDSN + "\n" +
		"TRUSTPANEL_ALERT_TG_TOKEN=\n" +
		"TRUSTPANEL_ALERT_CHAT_ID=\n"
}

// AgentEnv builds a minimal /etc/trustpanel/agent.env carrying just this node's
// id. An exit's agent unit passes --node-id as a flag and provisions no agent.env,
// so a former-exit standby has no file for `trustpanel promote` to self-resolve
// its node-id from — staging this lets the bare, alert-suggested promote command
// (`promote --pg-promote --start-serve`) resolve the id on its own.
func AgentEnv(nodeID string) string {
	return "TRUSTPANEL_NODE_ID=" + nodeID + "\n"
}

// botUnit is the operator Telegram management bot. It reads its DSN from
// serve.env and its token/admins from the panel DB (Bots tab) at runtime, so it
// needs no token file or admin flags: it idles harmlessly until configured in
// the panel. bot.env is optional (legacy flag overrides only).
func (l Layout) botUnit() string {
	return `[Unit]
Description=TrustPanel Telegram bot (config from panel DB)
After=network-online.target postgresql.service trustpanel-serve.service
Wants=network-online.target postgresql.service

[Service]
Type=simple
User=trustpanel
Group=trustpanel
EnvironmentFile=` + l.EtcDir + `/serve.env
EnvironmentFile=-` + l.EtcDir + `/bot.env
ExecStart=` + l.BinDir + `/trustpanel bot
Restart=on-failure
RestartSec=5
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true

[Install]
WantedBy=multi-user.target
`
}

// exitAgentUnit is the node agent for the exit (manages only sing-box; no
// trusttunnel, no ACME — the exit serves Reality and needs no public cert).
//
// This box is the bootstrapped control plane and an HA primary candidate, so its
// agent binds 0.0.0.0:8443 (mTLS) rather than localhost: after a failover the
// controller runs on the promoted standby and must be able to reach THIS node's
// agent at its public agent_addr (a localhost-only bind made the old primary
// unmanageable post-failover). The listener is mTLS-gated like every other exit
// agent; firewall :8443 to the fleet if exposure is a concern.
func (l Layout) exitAgentUnit(nodeID string) string {
	return `[Unit]
Description=TrustPanel node agent
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=` + l.BinDir + `/trustpanel agent --node-id ` + nodeID + ` --listen 0.0.0.0:8443 \
  --ca-file ` + l.PKIDir + `/ca.crt --cert-file ` + l.PKIDir + `/node.crt --key-file ` + l.PKIDir + `/node.key \
  --roots ` + l.SingBoxDir + ` --services trustpanel-singbox.service \
  --singbox-bin ` + l.BinDir + `/sing-box --state-file ` + l.AgentState + `/state.json --v2ray-api ''
Restart=on-failure
RestartSec=3

[Install]
WantedBy=multi-user.target
`
}

// singleAgentUnit is the node agent for a single-box deployment: it manages the
// entry data plane (TrustTunnel + sing-box) AND issues the public TLS cert via
// node-local ACME, but binds the control channel to localhost because the panel
// runs on the same host. ACME/node-id come from agent.env so a later role flip
// (single -> exit) only needs to rewrite that file + this unit.
func (l Layout) singleAgentUnit(nodeID string) string {
	return `[Unit]
Description=TrustPanel node agent (single)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=root
Group=root
EnvironmentFile=` + l.EtcDir + `/agent.env
ExecStart=` + l.BinDir + `/trustpanel agent --node-id ` + nodeID + ` --listen 127.0.0.1:8443 \
  --ca-file ` + l.PKIDir + `/ca.crt --cert-file ` + l.PKIDir + `/node.crt --key-file ` + l.PKIDir + `/node.key \
  --roots /etc/trusttunnel,` + l.SingBoxDir + ` --services trusttunnel.service,trustpanel-singbox.service \
  --singbox-bin ` + l.BinDir + `/sing-box --trusttunnel-bin /opt/trusttunnel/trusttunnel_endpoint \
  --state-file ` + l.AgentState + `/state.json
Restart=on-failure
RestartSec=3
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/etc/trusttunnel ` + l.SingBoxDir + ` ` + l.AgentState + ` ` + l.LogDir + `
StateDirectory=trustpanel-agent
LogsDirectory=trustpanel

[Install]
WantedBy=multi-user.target
`
}

// backupUnit dumps Postgres + the fleet PKI to /var/backups/trustpanel daily.
// Runs as root so it can read the CA key and write the root-only backup dir.
func (l Layout) backupUnit() string {
	return `[Unit]
Description=TrustPanel backup (Postgres + PKI)
After=postgresql.service

[Service]
Type=oneshot
EnvironmentFile=` + l.EtcDir + `/serve.env
ExecStart=` + l.BinDir + `/trustpanel backup --scheduled --out-dir /var/backups/trustpanel --pki-dir ` + l.PKIDir + `
`
}

// backupTimer fires hourly; the actual cadence (and retention, and the on/off
// toggle) is set in the panel and read off the replicated settings by the backup
// binary, which self-gates with --scheduled. Firing hourly lets a panel change to
// the interval take effect on the next hour on both the active node and the
// standby without touching either box's systemd. RandomizedDelaySec staggers the
// two nodes so their off-site pushes do not collide.
func (l Layout) backupTimer() string {
	return `[Unit]
Description=TrustPanel backup trigger (cadence set in the panel)

[Timer]
OnCalendar=*-*-* *:30:00
RandomizedDelaySec=20m
Persistent=true

[Install]
WantedBy=timers.target
`
}

// BackupUnit / BackupTimer expose the backup units to the cluster package so a
// control-plane standby stages and enables the same daily backup as the primary
// (pg_dump runs against the hot replica, so off-site copies keep being produced
// even if the primary is gone). RandomizedDelaySec staggers the two nodes so
// their off-site pushes do not collide on the same second.
func (l Layout) BackupUnit() string  { return l.backupUnit() }
func (l Layout) BackupTimer() string { return l.backupTimer() }

// verifyUnit runs the verify-restore drill: it loads the newest snapshot into a
// throwaway Postgres and checks the PKI, proving the backup is restorable without
// touching the live DB. Runs as root (reads the root-only backup dir; drops to
// the postgres user for the ephemeral instance) and alerts on failure.
func (l Layout) verifyUnit() string {
	return `[Unit]
Description=TrustPanel verify-restore drill (restore newest backup into a throwaway Postgres)
After=postgresql.service trustpanel-backup.service

[Service]
Type=oneshot
EnvironmentFile=` + l.EtcDir + `/serve.env
ExecStart=` + l.BinDir + `/trustpanel verify-restore --scheduled --dir /var/backups/trustpanel
`
}

// verifyTimer fires daily; the drill cadence (and on/off toggle) is set in the
// panel and read off the replicated settings, the binary self-gating on its
// marker with --scheduled — so a panel change applies on both nodes without
// editing systemd.
func (l Layout) verifyTimer() string {
	return `[Unit]
Description=TrustPanel verify-restore trigger (cadence set in the panel)

[Timer]
OnCalendar=*-*-* 04:30:00
RandomizedDelaySec=20m
Persistent=true

[Install]
WantedBy=timers.target
`
}

// VerifyUnit / VerifyTimer expose the verify-restore drill units to the cluster
// package so a standby (which also produces snapshots) verifies them too.
func (l Layout) VerifyUnit() string  { return l.verifyUnit() }
func (l Layout) VerifyTimer() string { return l.verifyTimer() }

// singboxUnit runs sing-box from the agent-managed config (Reality :443 on the
// exit). Runs as root so it can bind :443.
func (l Layout) singboxUnit() string {
	return `[Unit]
Description=TrustPanel sing-box (routing/Reality)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
WorkingDirectory=` + l.SingBoxDir + `
ExecStart=` + l.BinDir + `/sing-box run -c ` + l.SingBoxDir + `/sing-box.json -D ` + l.SingBoxDir + `
Restart=on-failure
RestartSec=3
LimitNOFILE=1048576
AmbientCapabilities=CAP_NET_BIND_SERVICE

[Install]
WantedBy=multi-user.target
`
}
