// Package store is the Postgres-backed source of truth for the panel. It
// loads the full model.State for the render pipeline and persists entity
// mutations. Postgres (not SQLite) is required because HA replicates this
// store to the standby exit.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"trustpanel/internal/core/model"
)

type Store struct {
	pool *pgxpool.Pool
}

// Open connects to Postgres using a pgx connection string or URL.
func Open(ctx context.Context, dsn string) (*Store, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	return &Store{pool: pool}, nil
}

func (s *Store) Close() { s.pool.Close() }

// NodeByID returns one node by id (false when absent). Used by bootstrap to reuse
// an existing exit's dial-in on a re-run instead of rotating its Reality identity.
func (s *Store) NodeByID(ctx context.Context, id string) (model.Node, bool, error) {
	st, err := s.LoadState(ctx)
	if err != nil {
		return model.Node{}, false, err
	}
	n, ok := st.NodeByID(id)
	return n, ok, nil
}

// Migrate runs a SQL migration script in one transaction.
func (s *Store) Migrate(ctx context.Context, sqlText string) error {
	_, err := s.pool.Exec(ctx, sqlText)
	return err
}

// MigrateOnce applies sqlText only if a migration with this name has not been
// applied before, recording it in schema_migrations. This makes startup
// migrations idempotent so a service restart does not re-run (and fail on)
// already-applied DDL. Returns whether the migration was applied now.
func (s *Store) MigrateOnce(ctx context.Context, name, sqlText string) (bool, error) {
	if _, err := s.pool.Exec(ctx,
		`CREATE TABLE IF NOT EXISTS schema_migrations (
		   name text PRIMARY KEY,
		   applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		return false, err
	}
	tag, err := s.pool.Exec(ctx,
		`INSERT INTO schema_migrations (name) VALUES ($1) ON CONFLICT (name) DO NOTHING`, name)
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() == 0 {
		return false, nil // already applied
	}
	if _, err := s.pool.Exec(ctx, sqlText); err != nil {
		// Roll back the bookkeeping row so a transient failure can be retried.
		_, _ = s.pool.Exec(ctx, `DELETE FROM schema_migrations WHERE name=$1`, name)
		return false, err
	}
	return true, nil
}

// LoadState reads the entire control-plane state for rendering.
func (s *Store) LoadState(ctx context.Context) (model.State, error) {
	var st model.State
	var err error
	if st.ControlPlane, err = s.controlPlane(ctx); err != nil {
		return st, err
	}
	if st.Nodes, err = s.nodes(ctx); err != nil {
		return st, err
	}
	if st.Groups, err = s.groups(ctx); err != nil {
		return st, err
	}
	if st.Users, err = s.users(ctx); err != nil {
		return st, err
	}
	if st.RoutePolicies, err = s.routePolicies(ctx); err != nil {
		return st, err
	}
	if st.Domains, err = s.domains(ctx); err != nil {
		return st, err
	}
	return st, nil
}

// ---- control plane ----

func (s *Store) controlPlane(ctx context.Context) (model.ControlPlane, error) {
	var cp model.ControlPlane
	var active *string
	err := s.pool.QueryRow(ctx,
		`SELECT active_node_id, epoch, standby_node_ids, updated_at FROM control_plane WHERE id = true`).
		Scan(&active, &cp.Epoch, &cp.StandbyNodeIDs, &cp.UpdatedAt)
	if err != nil {
		return cp, err
	}
	if active != nil {
		cp.ActiveNodeID = *active
	}
	return cp, nil
}

func (s *Store) ControlPlane(ctx context.Context) (model.ControlPlane, error) {
	return s.controlPlane(ctx)
}

// Promote sets a new active node and bumps the epoch (the fencing generation),
// then keeps nodes.pg_role consistent in the same transaction: the new active
// node becomes pg_role=primary, and the old primary is demoted to none — after
// a real failover it's on a diverged timeline, not a valid replica, until
// add-standby rebuilds it via pg_basebackup. Returns the new epoch.
func (s *Store) Promote(ctx context.Context, activeNodeID string) (int64, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	var epoch int64
	if err := tx.QueryRow(ctx,
		`UPDATE control_plane SET active_node_id = $1, epoch = epoch + 1, updated_at = now()
		 WHERE id = true RETURNING epoch`, activeNodeID).Scan(&epoch); err != nil {
		return 0, err
	}
	// Demote any stale primary other than the new one.
	if _, err := tx.Exec(ctx,
		`UPDATE nodes SET pg_role = 'none', updated_at = now()
		 WHERE pg_role = 'primary' AND id <> $1`, activeNodeID); err != nil {
		return 0, err
	}
	// Mark the new active node primary.
	if _, err := tx.Exec(ctx,
		`UPDATE nodes SET pg_role = 'primary', updated_at = now() WHERE id = $1`, activeNodeID); err != nil {
		return 0, err
	}
	// Drop the just-promoted node from the standby list. Before this, add-standby had
	// recorded it as *the* standby; leaving it there means the new primary monitors
	// replication to itself and fires recurring spurious "replication degraded / HA
	// at risk" alerts. We deliberately do NOT re-add the old primary as a
	// standby candidate: it is (presumably) dead and not replicating, so tracking it
	// would reintroduce the same false-degraded noise. The list is left without this
	// node until a human runs add-standby to rebuild a real replica.
	if _, err := tx.Exec(ctx,
		`UPDATE control_plane SET standby_node_ids = array_remove(standby_node_ids, $1), updated_at = now()
		 WHERE id = true`, activeNodeID); err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return epoch, err
}

// SetStandbys records the standby exit-candidate node ids.
func (s *Store) SetStandbys(ctx context.Context, ids []string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE control_plane SET standby_node_ids = $1, updated_at = now() WHERE id = true`, ids)
	return err
}

// ReplSlot is one physical replication slot's live health, read from the primary
// the panel runs on. Active=false means no standby is currently consuming the
// slot (replica down / disconnected / never connected). BytesBehind is how far
// the slot's restart_lsn trails the primary's current WAL (nil if unknown — no
// restart_lsn yet, or this node is itself in recovery so it isn't the primary).
type ReplSlot struct {
	Name        string
	Active      bool
	BytesBehind *int64
}

// ReplicationSlots reports physical replication slots and their lag, used by the
// panel's replication health probe. It needs no superuser/pg_monitor — a plain
// LOGIN role can read pg_replication_slots and the WAL-position functions. When
// this node is in recovery (i.e. not the primary), BytesBehind is left nil since
// pg_current_wal_lsn() is not meaningful there.
func (s *Store) ReplicationSlots(ctx context.Context) ([]ReplSlot, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT slot_name, active,
		        CASE WHEN pg_is_in_recovery() THEN NULL
		             ELSE pg_wal_lsn_diff(pg_current_wal_lsn(), restart_lsn) END
		 FROM pg_replication_slots WHERE slot_type = 'physical'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ReplSlot
	for rows.Next() {
		var sl ReplSlot
		if err := rows.Scan(&sl.Name, &sl.Active, &sl.BytesBehind); err != nil {
			return nil, err
		}
		out = append(out, sl)
	}
	return out, rows.Err()
}

// ---- settings (singleton) ----

// GetSettings reads the panel-managed settings singleton. Returns the zero
// value (all disabled) if the row is missing or empty. Tokens are returned in
// the clear — callers (bot/alerter) need them; the API layer masks before
// sending to the browser.
func (s *Store) GetSettings(ctx context.Context) (model.Settings, error) {
	var raw []byte
	err := s.pool.QueryRow(ctx, `SELECT data FROM settings WHERE id = true`).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Settings{}, nil
	}
	if err != nil {
		return model.Settings{}, err
	}
	var out model.Settings
	if len(raw) == 0 {
		return out, nil
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return model.Settings{}, fmt.Errorf("decode settings: %w", err)
	}
	return out, nil
}

// SaveSettings upserts the settings singleton.
func (s *Store) SaveSettings(ctx context.Context, st model.Settings) error {
	raw, err := json.Marshal(st)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx,
		`INSERT INTO settings (id, data, updated_at) VALUES (true, $1, now())
		 ON CONFLICT (id) DO UPDATE SET data = EXCLUDED.data, updated_at = now()`, raw)
	return err
}

// ---- alert dead-man heartbeat ----

// StampAlertHeartbeat records (at the DB clock) that the active control plane can
// currently reach Telegram. It replicates to the standby; a stale stamp there
// means the primary can no longer publish alerts. Writable on the primary only
// (the standby's replica is read-only, which is fine — only the primary stamps).
func (s *Store) StampAlertHeartbeat(ctx context.Context) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO alert_heartbeat (id, ok_at) VALUES (true, now())
		 ON CONFLICT (id) DO UPDATE SET ok_at = now()`)
	return err
}

// AlertHeartbeatAge returns how long ago the alert heartbeat was last stamped,
// measured against the local DB clock (so a replica compares the replicated
// ok_at to its own now()). ok is false when no stamp exists yet.
func (s *Store) AlertHeartbeatAge(ctx context.Context) (age time.Duration, ok bool, err error) {
	var secs *float64
	err = s.pool.QueryRow(ctx,
		`SELECT EXTRACT(EPOCH FROM (now() - ok_at)) FROM alert_heartbeat WHERE id = true`).Scan(&secs)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && secs == nil) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return time.Duration(*secs * float64(time.Second)), true, nil
}

// ---- nodes ----

func (s *Store) nodes(ctx context.Context) ([]model.Node, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, name, public_role, mgmt_capable, public_ips, agent_addr, health,
		        last_seen_at, pg_role, dial_in, limits, billing, managed_services,
		        COALESCE(owner_id,''), maintenance, created_at, updated_at
		 FROM nodes ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Node
	for rows.Next() {
		var n model.Node
		var lastSeen *time.Time
		var dialIn, limits, billing, managedServices []byte
		if err := rows.Scan(&n.ID, &n.Name, &n.PublicRole, &n.MgmtCapable, &n.PublicIPs,
			&n.AgentAddr, &n.Health, &lastSeen, &n.PGRole, &dialIn, &limits, &billing, &managedServices,
			&n.OwnerID, &n.Maintenance, &n.CreatedAt, &n.UpdatedAt); err != nil {
			return nil, err
		}
		n.LastSeenAt = lastSeen
		if len(managedServices) > 0 {
			if err := json.Unmarshal(managedServices, &n.ManagedServices); err != nil {
				return nil, fmt.Errorf("node %q managed_services: %w", n.ID, err)
			}
		}
		if len(dialIn) > 0 {
			var d model.DialIn
			if err := json.Unmarshal(dialIn, &d); err != nil {
				return nil, fmt.Errorf("node %q dial_in: %w", n.ID, err)
			}
			n.DialIn = &d
		}
		if len(limits) > 0 {
			var lim model.NodeLimits
			if err := json.Unmarshal(limits, &lim); err != nil {
				return nil, fmt.Errorf("node %q limits: %w", n.ID, err)
			}
			n.Limits = &lim
		}
		if len(billing) > 0 {
			var b model.NodeBilling
			if err := json.Unmarshal(billing, &b); err != nil {
				return nil, fmt.Errorf("node %q billing: %w", n.ID, err)
			}
			n.Billing = &b
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func (s *Store) UpsertNode(ctx context.Context, n model.Node) error {
	if err := n.Validate(); err != nil {
		return err
	}
	var dialIn any
	if n.DialIn != nil {
		b, err := json.Marshal(n.DialIn)
		if err != nil {
			return err
		}
		dialIn = b
	}
	// managed_services is set only at registration/role-flip; a nil slice means
	// "leave it as is" (COALESCE) so ordinary node upserts/edits never wipe it.
	var managed any
	if n.ManagedServices != nil {
		b, err := json.Marshal(n.ManagedServices)
		if err != nil {
			return err
		}
		managed = b
	}
	// health/last_seen_at are owned by the probe loop (SetNodeHealth), not by
	// ordinary upserts: a panel edit (e.g. rename) posts the node form without
	// these runtime fields, so an empty value must PRESERVE the observed health
	// rather than reset it to "unknown" and blank last_seen — otherwise a rename
	// briefly shows the node as dead until the next poll. Same COALESCE posture
	// as managed_services above. A blank health on INSERT falls back to "unknown".
	_, err := s.pool.Exec(ctx,
		`INSERT INTO nodes (id, name, public_role, mgmt_capable, public_ips, agent_addr,
		        health, last_seen_at, pg_role, dial_in, managed_services, owner_id, updated_at)
		 VALUES ($1,$2,$3,$4,$5,$6, COALESCE(NULLIF($7,''),'unknown'),$8,$9,$10,$11, NULLIF($12,''), now())
		 ON CONFLICT (id) DO UPDATE SET
		   name=$2, public_role=$3, mgmt_capable=$4, public_ips=$5, agent_addr=$6,
		   health=COALESCE(NULLIF($7,''), nodes.health),
		   last_seen_at=COALESCE($8, nodes.last_seen_at),
		   -- pg_role is owned by the HA promote flow (a direct UPDATE), not ordinary
		   -- upserts: a node-form edit posts no pg_role, so a 'none' value must
		   -- PRESERVE the stored role rather than clobber the live primary to 'none'.
		   -- Demotion still works because Promote writes pg_role directly.
		   pg_role=COALESCE(NULLIF($9,'none'), nodes.pg_role),
		   -- dial_in carries the exit's Reality identity. The handler merges the
		   -- stored keys into a key-light incoming dial_in and carries the whole
		   -- stored value when the form omits it, so a plain edit cannot wipe the
		   -- exit's identity; a deliberate exit->entry conversion still sets it
		   -- NULL. So this is a straight assignment (like mgmt_capable's carry).
		   dial_in=$10,
		   managed_services=COALESCE($11, nodes.managed_services),
		   owner_id=COALESCE(NULLIF($12,''), nodes.owner_id), updated_at=now()`,
		n.ID, n.Name, n.PublicRole, n.MgmtCapable, n.PublicIPs, n.AgentAddr,
		string(n.Health), n.LastSeenAt, pgRole(n.PGRole), dialIn, managed, n.OwnerID)
	return err
}

// SetNodeLimits sets (or clears, when lim is nil) a node's VPS plan limits
// without touching its other fields. Limits are managed only here so ordinary
// node upserts never wipe them.
func (s *Store) SetNodeLimits(ctx context.Context, id string, lim *model.NodeLimits) error {
	if lim != nil {
		if err := lim.Validate(); err != nil {
			return err
		}
	}
	var v any
	if lim != nil {
		b, err := json.Marshal(lim)
		if err != nil {
			return err
		}
		v = b
	}
	_, err := s.pool.Exec(ctx, `UPDATE nodes SET limits=$2, updated_at=now() WHERE id=$1`, id, v)
	return err
}

// SetNodeBilling sets (or clears, when b is nil) a node's payment tracking.
func (s *Store) SetNodeBilling(ctx context.Context, id string, b *model.NodeBilling) error {
	if b != nil {
		if err := b.Validate(); err != nil {
			return err
		}
	}
	var v any
	if b != nil {
		buf, err := json.Marshal(b)
		if err != nil {
			return err
		}
		v = buf
	}
	_, err := s.pool.Exec(ctx, `UPDATE nodes SET billing=$2, updated_at=now() WHERE id=$1`, id, v)
	return err
}

// SetNodeMaintenance drains (on=true) or restores a node without touching its
// other fields. Maintenance is managed only here so ordinary node upserts
// (provision, rename, role-flip, reconcile) never clobber the drain state.
func (s *Store) SetNodeMaintenance(ctx context.Context, id string, on bool) error {
	_, err := s.pool.Exec(ctx, `UPDATE nodes SET maintenance=$2, updated_at=now() WHERE id=$1`, id, on)
	return err
}

// SetNodeHealth records a node's observed health; lastSeen, when non-nil, also
// updates last_seen_at (a failed poll leaves the previous last_seen intact).
func (s *Store) SetNodeHealth(ctx context.Context, id string, h model.NodeHealth, lastSeen *time.Time) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE nodes SET health=$2, last_seen_at=COALESCE($3, last_seen_at), updated_at=now() WHERE id=$1`,
		id, health(h), lastSeen)
	return err
}

// NodeTrafficTotal is one node's accumulated traffic for the current month.
type NodeTrafficTotal struct {
	NodeID  string `json:"node_id"`
	Period  string `json:"period"`
	RxBytes int64  `json:"rx_bytes"`
	TxBytes int64  `json:"tx_bytes"`
}

// AccumulateNodeTraffic folds a node's absolute (since-boot) interface counters
// into the current month's totals, the same delta trick as user traffic, and
// resets at the UTC month boundary so it tracks the VPS monthly cap.
func (s *Store) AccumulateNodeTraffic(ctx context.Context, nodeID string, absRx, absTx int64, now time.Time) error {
	period := now.UTC().Format("2006-01")
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var curPeriod string
	var lastRx, lastTx int64
	found := true
	err = tx.QueryRow(ctx,
		`SELECT period, last_abs_rx, last_abs_tx FROM node_traffic WHERE node_id=$1 FOR UPDATE`, nodeID).
		Scan(&curPeriod, &lastRx, &lastTx)
	switch {
	case err == nil:
	case errors.Is(err, pgx.ErrNoRows):
		found = false
	default:
		return err
	}

	switch {
	case !found:
		_, err = tx.Exec(ctx,
			`INSERT INTO node_traffic (node_id, period, rx_bytes, tx_bytes, last_abs_rx, last_abs_tx, updated_at)
			 VALUES ($1,$2,$3,$4,$3,$4, now())`,
			nodeID, period, absRx, absTx)
	case curPeriod != period:
		// Month rolled over: start a fresh window at the current absolute reading.
		_, err = tx.Exec(ctx,
			`UPDATE node_traffic SET period=$2, rx_bytes=0, tx_bytes=0, last_abs_rx=$3, last_abs_tx=$4, updated_at=now()
			 WHERE node_id=$1`, nodeID, period, absRx, absTx)
	default:
		dRx, dTx := delta(absRx, lastRx), delta(absTx, lastTx)
		_, err = tx.Exec(ctx,
			`UPDATE node_traffic SET rx_bytes=rx_bytes+$2, tx_bytes=tx_bytes+$3, last_abs_rx=$4, last_abs_tx=$5, updated_at=now()
			 WHERE node_id=$1`, nodeID, dRx, dTx, absRx, absTx)
		if err == nil && (dRx != 0 || dTx != 0) {
			// Time-series sample for the per-node throughput graph. Skipped on the
			// first reading / month rollover above (no real delta).
			_, err = tx.Exec(ctx,
				`INSERT INTO node_traffic_samples (node_id, rx_bytes, tx_bytes) VALUES ($1,$2,$3)`,
				nodeID, dRx, dTx)
		}
	}
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// NodeTrafficSeries returns one node's bucketed throughput time-series since the
// given time, summing the per-tick deltas into fixed-width buckets.
func (s *Store) NodeTrafficSeries(ctx context.Context, nodeID string, since time.Time, bucket time.Duration) ([]TrafficBucket, error) {
	secs := int64(bucket.Seconds())
	if secs <= 0 {
		secs = 60
	}
	rows, err := s.pool.Query(ctx,
		`SELECT to_timestamp(floor(extract(epoch FROM at)/$3)*$3) AS bucket,
		        COALESCE(SUM(rx_bytes),0), COALESCE(SUM(tx_bytes),0)
		 FROM node_traffic_samples
		 WHERE node_id = $1 AND at >= $2
		 GROUP BY bucket ORDER BY bucket`, nodeID, since, secs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TrafficBucket
	for rows.Next() {
		var b TrafficBucket
		if err := rows.Scan(&b.At, &b.RxBytes, &b.TxBytes); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// RecordNodeResourceSample stores one poll's live CPU/memory/disk reading for a
// node — a plain gauge sample (no delta/accumulation, unlike traffic), backing
// the Nodes tab's CPU/Memory history views. Called once per stats poll for every
// node that answered with system metrics.
func (s *Store) RecordNodeResourceSample(ctx context.Context, nodeID string, load1 float64, cores int, memUsedMB, memTotalMB, diskUsedGB, diskTotalGB int64) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO node_resource_samples (node_id, cpu_load1, cpu_cores, mem_used_mb, mem_total_mb, disk_used_gb, disk_total_gb)
		 VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		nodeID, load1, cores, memUsedMB, memTotalMB, diskUsedGB, diskTotalGB)
	return err
}

// ResourceBucket is one time-series point for a node's CPU/memory history: CPU
// as a 0-100 utilization percent (averaged load1/cores across the bucket, not a
// byte counter like TrafficBucket), memory as used/total MB.
type ResourceBucket struct {
	At         time.Time `json:"at"`
	CPUPercent float64   `json:"cpu_pct"`
	MemUsedMB  int64     `json:"mem_used_mb"`
	MemTotalMB int64     `json:"mem_total_mb"`
}

// NodeResourceSeries returns one node's bucketed CPU/memory history since the
// given time, averaging samples within each fixed-width bucket (a gauge, so
// unlike NodeTrafficSeries this averages rather than sums).
func (s *Store) NodeResourceSeries(ctx context.Context, nodeID string, since time.Time, bucket time.Duration) ([]ResourceBucket, error) {
	secs := int64(bucket.Seconds())
	if secs <= 0 {
		secs = 60
	}
	rows, err := s.pool.Query(ctx,
		`SELECT to_timestamp(floor(extract(epoch FROM at)/$3)*$3) AS bucket,
		        COALESCE(AVG(cpu_load1 / NULLIF(cpu_cores,0)) * 100, 0),
		        COALESCE(AVG(mem_used_mb), 0), COALESCE(AVG(mem_total_mb), 0)
		 FROM node_resource_samples
		 WHERE node_id = $1 AND at >= $2
		 GROUP BY bucket ORDER BY bucket`, nodeID, since, secs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ResourceBucket
	for rows.Next() {
		var b ResourceBucket
		if err := rows.Scan(&b.At, &b.CPUPercent, &b.MemUsedMB, &b.MemTotalMB); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// NodeTrafficTotals returns the current-month accumulated traffic per node.
func (s *Store) NodeTrafficTotals(ctx context.Context) ([]NodeTrafficTotal, error) {
	rows, err := s.pool.Query(ctx, `SELECT node_id, period, rx_bytes, tx_bytes FROM node_traffic`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NodeTrafficTotal
	for rows.Next() {
		var t NodeTrafficTotal
		if err := rows.Scan(&t.NodeID, &t.Period, &t.RxBytes, &t.TxBytes); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) DeleteNode(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM nodes WHERE id = $1`, id)
	return err
}

// ---- groups ----

func (s *Store) groups(ctx context.Context) ([]model.Group, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, name, COALESCE(default_exit_id, ''), COALESCE(owner_id,''), created_at, updated_at FROM groups ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Group
	for rows.Next() {
		var g model.Group
		if err := rows.Scan(&g.ID, &g.Name, &g.DefaultExitID, &g.OwnerID, &g.CreatedAt, &g.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func (s *Store) UpsertGroup(ctx context.Context, g model.Group) error {
	if err := g.Validate(); err != nil {
		return err
	}
	_, err := s.pool.Exec(ctx,
		`INSERT INTO groups (id, name, default_exit_id, owner_id, updated_at)
		 VALUES ($1,$2,$3,NULLIF($4,''), now())
		 ON CONFLICT (id) DO UPDATE SET name=$2, default_exit_id=$3,
		   owner_id=COALESCE(NULLIF($4,''), groups.owner_id), updated_at=now()`,
		g.ID, g.Name, nullIfEmpty(g.DefaultExitID), g.OwnerID)
	return err
}

func (s *Store) DeleteGroup(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM groups WHERE id = $1`, id)
	return err
}

// DefaultGroupID is the id of the always-present catch-all group. Policies and
// new users default to it, so there is never an "empty/none" group to pick.
const DefaultGroupID = "default"

// EnsureDefaultGroup makes sure the catch-all "default" group exists. It is
// idempotent and leaves an existing group (incl. its default_exit_id) untouched.
func (s *Store) EnsureDefaultGroup(ctx context.Context) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO groups (id, name, updated_at) VALUES ($1,$2, now())
		 ON CONFLICT (id) DO NOTHING`, DefaultGroupID, DefaultGroupID)
	return err
}

// BackfillDefaultExit points every group that has no default_exit_id yet at
// exitID. Called right after an exit node is registered (bootstrap or panel
// provisioning) so a fresh multi-node deployment doesn't silently egress
// entries direct until an operator manually sets one — see the "default exit"
// note in the README. Only touches groups still unset, so it's a one-time
// effect: it never overrides an operator's or a later switchEgress's choice.
func (s *Store) BackfillDefaultExit(ctx context.Context, exitID string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE groups SET default_exit_id=$1, updated_at=now() WHERE COALESCE(default_exit_id,'')=''`,
		exitID)
	return err
}

// defaultPolicies is the starter rule set a fresh panel ships with: a Russian
// split-routing guard (.ru/.su/.рф and RU geoip/geosite → direct; gov-ru →
// block) plus youtube → direct. The guard rules apply to all users (empty group)
// so the split-routing baseline covers everyone out of the box.
func defaultPolicies() []model.RoutePolicy {
	return []model.RoutePolicy{
		{ID: "ru-domains", Name: "ru-domains", Priority: 100, Tier: model.TierGuard,
			Action: model.ActionDirect, MatchDomains: []string{".ru", ".su", ".рф"}},
		{ID: "ru-geoip", Name: "ru-geoip", Priority: 100, Tier: model.TierGuard,
			Action: model.ActionDirect, MatchGeoIP: []string{"ru"}, MatchGeoSite: []string{"category-ru"}},
		{ID: "ru-gov-block", Name: "ru-gov-block", Priority: 100, Tier: model.TierGuard,
			Action: model.ActionBlock, MatchGeoSite: []string{"category-gov-ru"}},
		{ID: "youtube", Name: "youtube", Priority: 100, Tier: model.TierExit,
			Action: model.ActionDirect, MatchGeoSite: []string{"youtube"}},
	}
}

// SeedDefaultPolicies installs defaultPolicies() exactly once, and only on a
// fresh panel (no route policies yet) — so a brand-new deployment ships with the
// starter rule set, while an existing panel (or one whose operator deleted the
// defaults) is left untouched.
//
// The whole thing runs in ONE transaction: the once-only marker row and the
// policy inserts commit together or not at all. This fixes two failure modes of
// the previous "mark applied, then insert in a loop" form: a failure on the
// first insert left the seed marked applied with ZERO policies (a fresh panel
// shipped empty), and a failure mid-loop left a partial seed — both unrecoverable
// because the marker was already set.
func (s *Store) SeedDefaultPolicies(ctx context.Context) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx,
		`CREATE TABLE IF NOT EXISTS schema_migrations (
		   name text PRIMARY KEY,
		   applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx,
		`INSERT INTO schema_migrations (name) VALUES ('seed_default_policies_v1') ON CONFLICT (name) DO NOTHING`)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return tx.Commit(ctx) // already seeded once — never bring the defaults back
	}
	var n int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM route_policies`).Scan(&n); err != nil {
		return err
	}
	if n == 0 { // existing panel with policies — don't duplicate
		for _, p := range defaultPolicies() {
			if err := p.Validate(); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, routePolicyUpsertSQL, routePolicyArgs(p)...); err != nil {
				return err
			}
		}
	}
	return tx.Commit(ctx)
}

// ---- users ----

func (s *Store) users(ctx context.Context) ([]model.User, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, username, secret, display_name, enabled, group_id, expires_at,
		        data_limit, used_traffic, reset_period, used_reset_at, COALESCE(owner_id,''), expiry_alerted_for, created_at, updated_at
		 FROM users ORDER BY username`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.User
	for rows.Next() {
		var u model.User
		if err := rows.Scan(&u.ID, &u.Username, &u.Secret, &u.DisplayName, &u.Enabled, &u.GroupID,
			&u.ExpiresAt, &u.DataLimit, &u.UsedTraffic, &u.ResetPeriod, &u.UsedResetAt, &u.OwnerID,
			&u.ExpiryAlertedFor, &u.CreatedAt, &u.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// User returns one user by id (ok=false if absent). Used by the upsert handler
// to preserve the existing secret on edit when no new password is given.
func (s *Store) User(ctx context.Context, id string) (model.User, bool, error) {
	var u model.User
	err := s.pool.QueryRow(ctx,
		`SELECT id, username, secret, display_name, enabled, group_id, expires_at,
		        data_limit, used_traffic, reset_period, used_reset_at, COALESCE(owner_id,''), expiry_alerted_for, created_at, updated_at
		 FROM users WHERE id = $1`, id).Scan(
		&u.ID, &u.Username, &u.Secret, &u.DisplayName, &u.Enabled, &u.GroupID,
		&u.ExpiresAt, &u.DataLimit, &u.UsedTraffic, &u.ResetPeriod, &u.UsedResetAt, &u.OwnerID,
		&u.ExpiryAlertedFor, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return model.User{}, false, nil
		}
		return model.User{}, false, err
	}
	return u, true, nil
}

// UserCount returns the number of VPN users. A fresh install seeds one so the
// entry data plane comes up — TrustTunnel refuses to start with no clients.
func (s *Store) UserCount(ctx context.Context) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&n)
	return n, err
}

func (s *Store) UpsertUser(ctx context.Context, u model.User) error {
	if err := u.Validate(); err != nil {
		return err
	}
	_, err := s.pool.Exec(ctx,
		`INSERT INTO users (id, username, secret, display_name, enabled, group_id, expires_at,
		        data_limit, used_traffic, reset_period, used_reset_at, owner_id, updated_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,NULLIF($12,''), now())
		 ON CONFLICT (id) DO UPDATE SET
		   username=$2, secret=$3, display_name=$4, enabled=$5, group_id=$6, expires_at=$7,
		   data_limit=$8, used_traffic=$9, reset_period=$10, used_reset_at=$11,
		   owner_id=COALESCE(NULLIF($12,''), users.owner_id), updated_at=now()`,
		u.ID, u.Username, u.Secret, u.DisplayName, u.Enabled, u.GroupID, u.ExpiresAt,
		u.DataLimit, u.UsedTraffic, u.ResetPeriod, u.UsedResetAt, u.OwnerID)
	return err
}

func (s *Store) DeleteUser(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, id)
	return err
}

// MarkExpiryAlerted records the expires_at value a user was last warned about, so
// the expiry-alert loop fires once per expiry date. It is the sole writer of
// expiry_alerted_for (UpsertUser never touches it), so a config edit re-arms the
// warning automatically when expires_at changes to a value that no longer matches.
func (s *Store) MarkExpiryAlerted(ctx context.Context, id string, expiresAt *time.Time) error {
	_, err := s.pool.Exec(ctx, `UPDATE users SET expiry_alerted_for=$2 WHERE id=$1`, id, expiresAt)
	return err
}

// ---- route policies ----

func (s *Store) routePolicies(ctx context.Context) ([]model.RoutePolicy, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, name, priority, tier, COALESCE(applies_to_group_id, ''),
		        match_domains, match_cidrs, match_geoip, match_geosite, action,
		        COALESCE(exit_node_id, ''), COALESCE(fallback_kind, ''), COALESCE(fallback_exit_id, ''),
		        exclude_domains, disabled, COALESCE(owner_id,''), created_at, updated_at
		 FROM route_policies ORDER BY priority DESC, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.RoutePolicy
	for rows.Next() {
		var p model.RoutePolicy
		if err := rows.Scan(&p.ID, &p.Name, &p.Priority, &p.Tier, &p.AppliesToGroupID,
			&p.MatchDomains, &p.MatchCIDRs, &p.MatchGeoIP, &p.MatchGeoSite, &p.Action,
			&p.ExitNodeID, &p.FallbackKind, &p.FallbackExitID, &p.ExcludeDomains, &p.Disabled, &p.OwnerID, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// routePolicyUpsertSQL is shared by UpsertRoutePolicy and the seed transaction so
// both write rows identically (and the seed can run inside its own tx).
const routePolicyUpsertSQL = `INSERT INTO route_policies (id, name, priority, tier, applies_to_group_id,
        match_domains, match_cidrs, match_geoip, match_geosite, action,
        exit_node_id, fallback_kind, fallback_exit_id, exclude_domains, disabled, owner_id, updated_at)
 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,NULLIF($16,''), now())
 ON CONFLICT (id) DO UPDATE SET
   name=$2, priority=$3, tier=$4, applies_to_group_id=$5,
   match_domains=$6, match_cidrs=$7, match_geoip=$8, match_geosite=$9, action=$10,
   exit_node_id=$11, fallback_kind=$12, fallback_exit_id=$13, exclude_domains=$14, disabled=$15,
   owner_id=COALESCE(NULLIF($16,''), route_policies.owner_id), updated_at=now()`

// routePolicyArgs builds the positional args for routePolicyUpsertSQL.
func routePolicyArgs(p model.RoutePolicy) []any {
	return []any{
		p.ID, p.Name, p.Priority, p.Tier, nullIfEmpty(p.AppliesToGroupID),
		emptySlice(p.MatchDomains), emptySlice(p.MatchCIDRs), emptySlice(p.MatchGeoIP), emptySlice(p.MatchGeoSite),
		p.Action, nullIfEmpty(p.ExitNodeID), nullIfEmpty(string(p.FallbackKind)), nullIfEmpty(p.FallbackExitID),
		emptySlice(p.ExcludeDomains), p.Disabled, p.OwnerID,
	}
}

func (s *Store) UpsertRoutePolicy(ctx context.Context, p model.RoutePolicy) error {
	if err := p.Validate(); err != nil {
		return err
	}
	_, err := s.pool.Exec(ctx, routePolicyUpsertSQL, routePolicyArgs(p)...)
	return err
}

func (s *Store) DeleteRoutePolicy(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM route_policies WHERE id = $1`, id)
	return err
}

// ---- domains ----

func (s *Store) domains(ctx context.Context) ([]model.Domain, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, hostname, purpose, node_id, tls_status, tls_issuer, created_at, updated_at FROM domains ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Domain
	for rows.Next() {
		var d model.Domain
		if err := rows.Scan(&d.ID, &d.Hostname, &d.Purpose, &d.NodeID, &d.TLSStatus, &d.TLSIssuer, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) UpsertDomain(ctx context.Context, d model.Domain) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO domains (id, hostname, purpose, node_id, tls_status, updated_at)
		 VALUES ($1,$2,$3,$4,$5, now())
		 ON CONFLICT (id) DO UPDATE SET hostname=$2, purpose=$3, node_id=$4, tls_status=$5, updated_at=now()`,
		d.ID, d.Hostname, d.Purpose, d.NodeID, defStr(d.TLSStatus, "pending"))
	return err
}

func (s *Store) DeleteDomain(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM domains WHERE id = $1`, id)
	return err
}

// SetDomainTLSStatusForNode updates tls_status + tls_issuer for every domain
// bound to a node. Used by the stats loop to surface the entry agent's ACME cert
// expiry and whether it's a staging or production leaf.
func (s *Store) SetDomainTLSStatusForNode(ctx context.Context, nodeID, status, issuer string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE domains SET tls_status=$2, tls_issuer=$3, updated_at=now() WHERE node_id=$1`, nodeID, status, issuer)
	return err
}

// ---- traffic accounting ----

// AccumulateTraffic folds an absolute cumulative reading from a node's sing-box
// v2ray stats API into the running per-(user, entry) totals. absRx is the
// uplink (client→server) reading; absTx is the downlink (server→client).
//
// Delta math (one transaction so concurrent polls don't race the read-modify-
// write): delta = abs - last_abs, but a reading smaller than the last one means
// sing-box restarted and zeroed its counter, so the new absolute is the delta.
// The accumulated totals and users.used_traffic grow by the deltas.
func (s *Store) AccumulateTraffic(ctx context.Context, userID, entryNodeID string, absRx, absTx int64) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var lastRx, lastTx int64
	found := true
	err = tx.QueryRow(ctx,
		`SELECT last_abs_rx, last_abs_tx FROM user_traffic
		 WHERE user_id = $1 AND entry_node_id = $2 FOR UPDATE`, userID, entryNodeID).
		Scan(&lastRx, &lastTx)
	switch {
	case err == nil:
	case errors.Is(err, pgx.ErrNoRows):
		found = false
	default:
		return err
	}

	deltaRx := delta(absRx, lastRx)
	deltaTx := delta(absTx, lastTx)

	if found {
		_, err = tx.Exec(ctx,
			`UPDATE user_traffic
			 SET rx_bytes = rx_bytes + $3, tx_bytes = tx_bytes + $4,
			     last_abs_rx = $5, last_abs_tx = $6, updated_at = now()
			 WHERE user_id = $1 AND entry_node_id = $2`,
			userID, entryNodeID, deltaRx, deltaTx, absRx, absTx)
	} else {
		_, err = tx.Exec(ctx,
			`INSERT INTO user_traffic
			   (user_id, entry_node_id, rx_bytes, tx_bytes, last_abs_rx, last_abs_tx, updated_at)
			 VALUES ($1,$2,$3,$4,$5,$6, now())`,
			userID, entryNodeID, deltaRx, deltaTx, absRx, absTx)
	}
	if err != nil {
		return err
	}

	if deltaRx != 0 || deltaTx != 0 {
		if _, err = tx.Exec(ctx,
			`UPDATE users SET used_traffic = used_traffic + $2 WHERE id = $1`,
			userID, deltaRx+deltaTx); err != nil {
			return err
		}
		// Time-series sample for the activity view: one row per tick that moved
		// bytes. Idle users write nothing, so "active now" is simply a
		// recent sample existing. The delta above is already counter-reset-clamped.
		if _, err = tx.Exec(ctx,
			`INSERT INTO traffic_samples (user_id, entry_node_id, rx_bytes, tx_bytes)
			 VALUES ($1,$2,$3,$4)`, userID, entryNodeID, deltaRx, deltaTx); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// ActivityStat is a user's throughput over a recent window (active-now view).
type ActivityStat struct {
	UserID  string `json:"user_id"`
	RxBytes int64  `json:"rx_bytes"`
	TxBytes int64  `json:"tx_bytes"`
}

// RecentActivity sums each user's traffic-sample deltas since `since`, i.e. who
// actually moved bytes in the window, keeping only users whose rx+tx reached
// minBytes (a floor that filters out idle background chatter so a merely-
// connected client does not read as "online"). minBytes<=0 keeps everyone with
// any movement. Users below the floor are absent from the result.
func (s *Store) RecentActivity(ctx context.Context, since time.Time, minBytes int64) (map[string]ActivityStat, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT user_id, COALESCE(SUM(rx_bytes),0), COALESCE(SUM(tx_bytes),0)
		 FROM traffic_samples WHERE at >= $1 GROUP BY user_id
		 HAVING COALESCE(SUM(rx_bytes),0)+COALESCE(SUM(tx_bytes),0) >= $2`, since, minBytes)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]ActivityStat{}
	for rows.Next() {
		var a ActivityStat
		if err := rows.Scan(&a.UserID, &a.RxBytes, &a.TxBytes); err != nil {
			return nil, err
		}
		out[a.UserID] = a
	}
	return out, rows.Err()
}

// TrafficBucket is one time-series point: total rx/tx in the bucket window.
type TrafficBucket struct {
	At      time.Time `json:"at"`
	RxBytes int64     `json:"rx_bytes"`
	TxBytes int64     `json:"tx_bytes"`
}

// UserTrafficSeries returns one user's traffic deltas since `since`, bucketed to
// `bucket` width (summed across entries), for the per-user graph. Empty buckets
// are omitted (the UI plots against the time axis).
func (s *Store) UserTrafficSeries(ctx context.Context, userID string, since time.Time, bucket time.Duration) ([]TrafficBucket, error) {
	secs := int64(bucket.Seconds())
	if secs <= 0 {
		secs = 60
	}
	rows, err := s.pool.Query(ctx,
		`SELECT to_timestamp(floor(extract(epoch FROM at)/$3)*$3) AS bucket,
		        COALESCE(SUM(rx_bytes),0), COALESCE(SUM(tx_bytes),0)
		 FROM traffic_samples
		 WHERE user_id = $1 AND at >= $2
		 GROUP BY bucket ORDER BY bucket`, userID, since, secs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TrafficBucket
	for rows.Next() {
		var b TrafficBucket
		if err := rows.Scan(&b.At, &b.RxBytes, &b.TxBytes); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// PruneTrafficSamples deletes samples older than `before` to bound the traffic
// and node-resource sample tables. Called from the stats loop.
func (s *Store) PruneTrafficSamples(ctx context.Context, before time.Time) error {
	if _, err := s.pool.Exec(ctx, `DELETE FROM traffic_samples WHERE at < $1`, before); err != nil {
		return err
	}
	if _, err := s.pool.Exec(ctx, `DELETE FROM node_traffic_samples WHERE at < $1`, before); err != nil {
		return err
	}
	_, err := s.pool.Exec(ctx, `DELETE FROM node_resource_samples WHERE at < $1`, before)
	return err
}

// delta computes the increment for one counter, treating a reading below the
// last seen value as a counter reset (sing-box restart).
func delta(abs, last int64) int64 {
	if abs < last {
		return abs
	}
	return abs - last
}

// UserTrafficTotal is one user's accumulated traffic summed across all entry
// nodes (the underlying table keeps per-entry rows; this sums them for display).
type UserTrafficTotal struct {
	UserID     string `json:"user_id"`
	Username   string `json:"username"`
	RxBytes    int64  `json:"rx_bytes"`    // uplink, client -> server
	TxBytes    int64  `json:"tx_bytes"`    // downlink, server -> client
	TotalBytes int64  `json:"total_bytes"` // rx + tx
	DataLimit  int64  `json:"data_limit"`  // v2-reserved; 0 = unlimited
}

// UserTrafficTotals returns every user with their summed traffic (zero for users
// with no recorded traffic yet).
func (s *Store) UserTrafficTotals(ctx context.Context) ([]UserTrafficTotal, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT u.id, u.username,
		        COALESCE(SUM(t.rx_bytes), 0), COALESCE(SUM(t.tx_bytes), 0), u.data_limit
		 FROM users u
		 LEFT JOIN user_traffic t ON t.user_id = u.id
		 GROUP BY u.id, u.username, u.data_limit
		 ORDER BY u.username`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UserTrafficTotal
	for rows.Next() {
		var t UserTrafficTotal
		if err := rows.Scan(&t.UserID, &t.Username, &t.RxBytes, &t.TxBytes, &t.DataLimit); err != nil {
			return nil, err
		}
		t.TotalBytes = t.RxBytes + t.TxBytes
		out = append(out, t)
	}
	return out, rows.Err()
}

// ---- helpers ----

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func emptySlice(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func defStr(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func health(h model.NodeHealth) model.NodeHealth {
	if h == "" {
		return model.HealthUnknown
	}
	return h
}

func pgRole(r model.PGRole) model.PGRole {
	if r == "" {
		return model.PGNone
	}
	return r
}
