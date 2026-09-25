# TrustPanel

## Build

```sh
go build ./cmd/trustpanel
```

Dependencies:

- sing-box built with `-tags "with_v2ray_api with_utls"`
- `trusttunnel_endpoint` (`scripts/build-trusttunnel.sh`)

## Quickstart

Both variants use `scripts/install.sh` on a fresh server (as root). It fetches
the release binaries, verifies checksums, and runs `trustpanel bootstrap`.
Newest release unless `--tag vX.Y.Z` pins one; `trustpanel version` says which
one is installed.

Before starting: point DNS at the server; pick your own domain and brand;
don't ship the `ExampleCDN` placeholder below.

### Single node

One box: control plane + client-facing entry + local egress. No separate
exit, no Reality.

```sh
curl -fsSL https://raw.githubusercontent.com/br0therfromanothermother/TrustPanel/main/scripts/install.sh \
  | sudo bash -s -- \
      --domain "example.com" \
      --brand "ExampleCDN" \
      --single \
      [--public-ip 1.1.1.1] \
      [--node-name "Node1"] \
      [--connect-subdomain "my"] \
      [--admin-user "admin"] [--admin-password "change-me"] \
      [--vpn-user "admin"] [--vpn-password "change-me-too"] \
      [--acme-email "admin@example.com"] [--acme-staging] \
      [--harden \
        [--harden-sudo-user "user"] \
        [--harden-ssh-pubkey-file "/root/.ssh/id_ed25519.pub"] \
        [--harden-ssh-port 2222] \
        [--harden-disable-root] \
        [--harden-disable-password] \
        [--harden-firewall] \
        [--harden-fail2ban]]
```

DNS: two A/AAAA records to this box's IP, the apex (`example.com`) and the
connect subdomain (`my.example.com`). The client endpoint comes up
once DNS resolves and 80/443 are reachable.

Pick a connect subdomain that reads as a client login for the brand.

### Multi-node

The **panel goes on the exit node**, not the entry. Run `install.sh` there
first:

```sh
curl -fsSL https://raw.githubusercontent.com/br0therfromanothermother/TrustPanel/main/scripts/install.sh \
  | sudo bash -s -- \
      --brand "ExampleCDN" \
      --reality-sni "www.example-target.com" \
      [--domain "example.com"] \
      [--public-ip 1.1.1.1] \
      [--node-name "Node1"] \
      [--admin-user "admin"] [--admin-password "change-me"] \
      [--vpn-user "admin"] [--vpn-password "change-me-too"] \
      [--harden \
        [--harden-sudo-user "user"] \
        [--harden-ssh-pubkey-file "/root/.ssh/id_ed25519.pub"] \
        [--harden-ssh-port 2222] \
        [--harden-disable-root] \
        [--harden-disable-password] \
        [--harden-firewall] \
        [--harden-fail2ban]]
```

`--reality-sni` is a real TLS 1.3 site to borrow for the handshake; pick your
own. `--domain` is optional here: set it now, or set the apex when you add the
first entry.

Then provision entry nodes from the panel: **Servers → Add server**. Enter each
entry's endpoint hostname there (point its DNS at its IP first).

## Reaching the panel

The panel listens on `127.0.0.1:8787` on the node it runs on. Reach it over
SSH:

```sh
ssh -L 8787:127.0.0.1:8787 <user>@<panel-node-ip>
```

Then open `http://127.0.0.1:8787`. Login and DB credentials are printed once
by `bootstrap`/`install.sh` at the end of the run. Save them.

Users, nodes, routing and billing are run from the panel and the Telegram bot.

## Backups

Every control-plane node snapshots the database + PKI locally on the
configured interval (default 24h, kept under `/var/backups/trustpanel`).
Off-site delivery to Telegram is optional.

To enable it, generate a keypair on your own machine:

```sh
age-keygen -o "trustpanel-backup-key.txt"
```

Paste the printed public key into **Settings → Backup** and turn on off-site
delivery. Keep `trustpanel-backup-key.txt` offline.

To restore, unpack a snapshot and follow the `RECOVERY.md` inside it
(decrypt → `restore --apply` → `promote`).

## Failover

If the primary is down and confirmed dead, promote the standby. Run on the
standby, as root:

```sh
sudo trustpanel promote --pg-promote --start-serve
```

Reach the panel over SSH as above, using this node's IP.

Rebuild the recovered old primary as the new standby with `trustpanel cluster
add-standby`.
