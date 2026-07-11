# TrustPanel

## Build

```sh
go build ./cmd/trustpanel
```

Dependencies:

- Postgres 16+
- sing-box built with `-tags "with_v2ray_api with_utls"`
- `trusttunnel_endpoint` (`scripts/build-trusttunnel.sh`, patch in `patches/`)

## Quickstart

Both variants use `scripts/install.sh` on a fresh server (as root). It pulls
`trustpanel`, `sing-box`, and `trusttunnel_endpoint` from a GitHub Release,
verifies checksums, and runs `trustpanel bootstrap`.

Before starting, point DNS at the server and decide your own domain and
brand — don't ship the `ExampleCDN` placeholder below.

### Single node

One box: control plane + client-facing entry + local egress. No separate
exit, no Reality.

```sh
curl -fsSL https://raw.githubusercontent.com/br0therfromanothermother/TrustPanel/main/scripts/install.sh \
  | sudo bash -s -- \
      --domain example.com \
      --brand ExampleCDN \
      --single \
      [--public-ip 1.1.1.1] \
      [--node-name "Node"] \
      [--connect-subdomain vpn] \
      [--admin-user admin] [--admin-password 'change-me'] \
      [--vpn-user admin] [--vpn-password 'change-me-too'] \
      [--acme-email admin@example.com] [--acme-staging] \
      [--harden \
        [--harden-sudo-user user] \
        [--harden-ssh-pubkey-file /root/.ssh/id_ed25519.pub] \
        [--harden-ssh-port 2222] \
        [--harden-disable-root] \
        [--harden-disable-password] \
        [--harden-firewall] \
        [--harden-fail2ban]]
```

DNS: two A/AAAA records to this box's IP — the apex (`example.com`) and the
connect subdomain (`vpn.example.com` by default). One TLS cert covers both.
The client endpoint comes up once DNS resolves and 80/443 are reachable.

### Multi-node

The **panel goes on the exit node**, not the entry. Run `install.sh` on the
box that will be the exit first:

```sh
curl -fsSL https://raw.githubusercontent.com/br0therfromanothermother/TrustPanel/main/scripts/install.sh \
  | sudo bash -s -- \
      --domain example.com \
      --brand ExampleCDN \
      --reality-sni www.example-target.com \
      [--public-ip 1.1.1.1] \
      [--node-name "Node 1"] \
      [--admin-user admin] [--admin-password 'change-me'] \
      [--vpn-user admin] [--vpn-password 'change-me-too'] \
      [--harden \
        [--harden-sudo-user user] \
        [--harden-ssh-pubkey-file /root/.ssh/id_ed25519.pub] \
        [--harden-ssh-port 2222] \
        [--harden-disable-root] \
        [--harden-disable-password] \
        [--harden-firewall] \
        [--harden-fail2ban]]
```

`--reality-sni` is a real TLS 1.3 site to borrow for the handshake — pick
your own. `--domain` here is just the default connect domain; entries can
override it with their own.

Then provision entry nodes from the panel: **Nodes → Install on server**
(SSH to the new box, its domain's DNS pointed at its IP first).

## Reaching the panel

The panel listens on `127.0.0.1:8787` on the node it runs on. Reach it over
SSH:

```sh
ssh -L 8787:127.0.0.1:8787 <user>@<panel-node-ip>
```

Then open `http://127.0.0.1:8787`. Login and DB credentials are printed once
by `bootstrap`/`install.sh` at the end of the run — save them, they aren't
stored anywhere else.

From here everything — users, nodes, routing, billing — is run from the panel
and the Telegram bot. The command-line steps below are only for backups and
failover.

## Backups

Every control-plane node snapshots the database + PKI locally on the
configured interval (default 24h, kept under `/var/backups/trustpanel`).
Off-site delivery to Telegram is optional and age-encrypted before it leaves
the box.

To enable it, generate a keypair on your own machine:

```sh
age-keygen -o trustpanel-backup-key.txt
```

Paste the printed public key into **Settings → Backup** and turn on off-site
delivery. Keep `trustpanel-backup-key.txt` offline — it is the only thing
that can decrypt an off-site snapshot.

To restore, unpack a snapshot and follow the `RECOVERY.md` inside it — a
self-contained runbook with the exact commands to rebuild the control plane
on a fresh box (decrypt → `restore --apply` → `promote`).

## Failover

If the primary is down and confirmed dead, promote the standby. Run on the
standby, as root:

```sh
sudo trustpanel promote --pg-promote --start-serve
```

This turns the standby's Postgres replica into a primary, bumps the
control-plane epoch (so the old primary is fenced off if it comes back), and
starts the panel + management bot on this node. Reach it over SSH as above,
using its own IP. `promote` finds this node's own id from
`/etc/trustpanel/agent.env`, so there is nothing to paste.

Rebuild the recovered old primary as the new standby with `trustpanel cluster
add-standby`.
