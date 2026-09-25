# systemd units

Units for the single `trustpanel` binary (subcommands) plus the data-plane
processes the agent manages.

## Which units run where

| Unit | entry node | exit node | mgmt/panel host (an exit) |
|------|:---------:|:--------:|:-------------------------:|
| `trustpanel-agent.service` | ✓ | ✓ | ✓ |
| `trustpanel-singbox.service` | ✓ (policy router) | ✓ (Reality inbound) | ✓ |
| `trusttunnel.service` | ✓ | - | - |
| `trustpanel-fallback.service` | ✓ | - | - |
| `trustpanel-serve.service` | - | - | ✓ (active only) |
| `trustpanel-watchdog.service` | - | ✓ (candidates) | ✓ |
| `trustpanel-backup.timer` | - | - | ✓ primary + standby |
| `trustpanel-verify-restore.timer` | - | - | ✓ primary + standby |
| `postgresql.service` (distro) | - | - | ✓ primary + replica on standby |

- **Agent** runs on every node and is the only thing the panel talks to (mTLS).
  It writes `/etc/trusttunnel` + `/etc/trustpanel/singbox` and restarts
  `trusttunnel.service` / `trustpanel-singbox.service` on reconcile.
- **serve** (panel) runs only on the active exit-candidate; on promote it is
  started on the new active node. Binds localhost.
- **watchdog** runs on both exit-candidates and watches the peer.
- **backup / verify-restore timers** run on the primary and the standby (both
  hold the DB + CA). The timers fire on a fixed cadence (backup hourly, verify
  daily) but the binary self-gates with `--scheduled`: the real interval,
  retention, and on/off come from the panel-configured, replicated settings, so
  one panel edit governs both nodes without editing systemd. bootstrap installs
  these from the binary's embedded units (`internal/core/bootstrap/units.go`);
  the files here are the canonical reference.

## Per-host configuration (EnvironmentFile)

- `/etc/trustpanel/agent.env`: `TRUSTPANEL_NODE_ID=<node id>`
- `/etc/trustpanel/serve.env`: `TRUSTPANEL_DSN=postgres://...`
- `/etc/trustpanel/watchdog.env`: `TRUSTPANEL_TARGET=<peer id>`,
  `TRUSTPANEL_WATCH_TCP=<peer-ip>:5432`, `TRUSTPANEL_ALERT_TG_TOKEN=...`,
  `TRUSTPANEL_ALERT_CHAT_ID=...`

## PKI layout (provisioned, not in the DB)

- `/etc/trustpanel/pki/ca.crt`: fleet CA bundle (all nodes)
- `/etc/trustpanel/pki/node.crt` + `node.key`: per-node cert (agents)
- `/etc/trustpanel/pki/controller.crt` + `controller.key`: controller cert
  (exit-candidates that may host the panel)

Generate with `trustpanel ca init|controller|node|sign`.

## Binaries

- `/usr/local/bin/trustpanel`: the panel/agent/ca/watchdog/fallback binary
- `/usr/local/bin/sing-box`: built with `-tags "with_v2ray_api with_utls"`
- `/opt/trusttunnel/trusttunnel_endpoint`: pinned TrustTunnel build (+ the
  private-origin patch)
