# How to restore from this backup

You are reading this because you unpacked a TrustPanel snapshot. It is
self-contained on purpose: you should not need the source repo, the website, or
any surviving server to recover.

## What's in this archive

- `db.sql` — a `pg_dump` of the whole control-plane database (every node, group,
  user + secret, route policy, domain, admin hash, traffic, limits, billing).
- `pki/` — the fleet PKI. `pki/ca.key` is the only irreplaceable secret; every
  node/controller certificate can be re-issued from it.
- `MANIFEST.txt` — when this snapshot was taken and how big the dump was.

> This archive contains the CA private key and user secrets. Keep it offline and
> treat it like a root password.

## You need

- A Linux host with `trustpanel`, `postgres`, and `pg_dump`/`psql` installed
  (any fresh box; it becomes the new control plane).
- This archive, decrypted. If it came off-site it is `age`-encrypted and split
  into parts — reassemble and decrypt with the private key you generated and
  stored offline:
  ```
  cat trustpanel-*.tar.gz.age.part-* > snapshot.tar.gz.age
  age -d -i /path/to/your-age-key.txt -o snapshot.tar.gz snapshot.tar.gz.age
  ```

## Path A — rebuild the control plane from this snapshot

Use this when the control plane is gone and there is no warm standby.

1. Put the snapshot on the new box and set the DB connection string:
   ```
   export TRUSTPANEL_DSN="host=/var/run/postgresql user=trustpanel dbname=trustpanel sslmode=disable"
   ```
2. Restore the database and PKI (recreates the DB owned by the app role and
   writes `pki/*` back with the right owner/modes):
   ```
   sudo trustpanel restore --apply --from snapshot.tar.gz --dsn "$TRUSTPANEL_DSN"
   ```
   It refuses to clobber a DB that already holds fleet data (override with
   `--force`).
3. Claim leadership and start the panel. The restored DB is already a standalone
   primary, so do **not** pass `--pg-promote`:
   ```
   sudo trustpanel promote --node-id <this-node-id> --start-serve
   ```
4. Verify, then re-point your panel tunnel at the new host:
   ```
   systemctl is-active trustpanel-serve
   curl -fsS http://127.0.0.1:8787/api/health     # {"status":"ok"}
   ```
   Entry nodes keep working — their certificates chain to the restored CA.

## Path B — promote a warm standby (faster, no restore needed)

Use this when the primary died but a standby is alive. Run **on the standby**;
its replica already holds the data and the CA, so there is nothing to restore:
```
sudo trustpanel promote --pg-promote --start-serve
```
Run on a provisioned standby, `promote` resolves **its own** node id from
`/etc/trustpanel/agent.env`, so you do not need to paste it. If that file is gone
(bare recovery host), pass it explicitly:
`sudo trustpanel promote --node-id <this-node-id> --pg-promote --start-serve`.

`--pg-promote` turns its read-only replica into a primary. Verify as in Path A
step 4. When the old primary comes back, do not let it serve — rejoin it as a
fresh replica (`trustpanel cluster add-standby --node-id <old-primary-id> …`).

## After recovery

- Re-issue production TLS on the entry nodes if needed.
- Reconfigure off-site backup so you have a fresh chain going forward.
