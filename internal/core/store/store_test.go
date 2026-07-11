package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"trustpanel/internal/core/model"
	"trustpanel/internal/core/render"
)

var testDSN string

func TestMain(m *testing.M) {
	if base := os.Getenv("TRUSTPANEL_TEST_DSN"); base != "" {
		// Glob (sorted) so newly-added migrations are picked up automatically —
		// a hardcoded list silently drifts (e.g. 0009 was missed, breaking the
		// schema with a "column does not exist" error).
		migrations, err := filepath.Glob("../../../migrations/pg/*.sql")
		if err != nil {
			fmt.Println("store test migration glob:", err)
		}
		sort.Strings(migrations)
		dsn, err := setupTestDB(base, "trustpanel_store_test", migrations)
		if err != nil {
			fmt.Println("store test db setup:", err)
		} else {
			testDSN = dsn
		}
	}
	os.Exit(m.Run())
}

// setupTestDB creates a fresh per-package database off the base DSN and applies
// the given migrations, so parallel `go test ./...` packages do not share a
// schema. Returns the DSN for the new database.
func setupTestDB(base, dbName string, migrations []string) (string, error) {
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, base)
	if err != nil {
		return "", err
	}
	defer admin.Close(ctx)
	_, _ = admin.Exec(ctx, "DROP DATABASE IF EXISTS "+dbName+" WITH (FORCE)")
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+dbName); err != nil {
		return "", err
	}
	dsn := replaceDBName(base, dbName)
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return "", err
	}
	defer conn.Close(ctx)
	for _, mfile := range migrations {
		b, err := os.ReadFile(mfile)
		if err != nil {
			return "", err
		}
		if _, err := conn.Exec(ctx, string(b)); err != nil {
			return "", err
		}
	}
	return dsn, nil
}

func replaceDBName(dsn, name string) string {
	var out []string
	for _, f := range strings.Fields(dsn) {
		if !strings.HasPrefix(f, "dbname=") {
			out = append(out, f)
		}
	}
	out = append(out, "dbname="+name)
	return strings.Join(out, " ")
}

func openOrSkip(t *testing.T) *Store {
	t.Helper()
	if testDSN == "" {
		t.Skip("set TRUSTPANEL_TEST_DSN to run store integration tests")
	}
	s, err := Open(context.Background(), testDSN)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(s.Close)
	reset(t, s)
	return s
}

func reset(t *testing.T, s *Store) {
	t.Helper()
	ctx := context.Background()
	// TRUNCATE nodes CASCADE also clears control_plane (it FKs nodes), so the
	// singleton row is re-created afterwards.
	_, err := s.pool.Exec(ctx,
		`TRUNCATE nodes, groups, users, route_policies, domains, user_traffic CASCADE;
		 INSERT INTO control_plane (id) VALUES (true)
		   ON CONFLICT (id) DO UPDATE SET active_node_id=NULL, epoch=0, standby_node_ids='{}';`)
	if err != nil {
		t.Fatalf("reset: %v", err)
	}
}

// seed upserts the worked example in FK-safe order.
func seed(t *testing.T, s *Store, st model.State) {
	t.Helper()
	ctx := context.Background()
	for _, n := range st.Nodes {
		if err := s.UpsertNode(ctx, n); err != nil {
			t.Fatalf("upsert node %s: %v", n.ID, err)
		}
	}
	for _, g := range st.Groups {
		if err := s.UpsertGroup(ctx, g); err != nil {
			t.Fatalf("upsert group %s: %v", g.ID, err)
		}
	}
	for _, u := range st.Users {
		if err := s.UpsertUser(ctx, u); err != nil {
			t.Fatalf("upsert user %s: %v", u.ID, err)
		}
	}
	for _, p := range st.RoutePolicies {
		if err := s.UpsertRoutePolicy(ctx, p); err != nil {
			t.Fatalf("upsert policy %s: %v", p.ID, err)
		}
	}
	for _, d := range st.Domains {
		if err := s.UpsertDomain(ctx, d); err != nil {
			t.Fatalf("upsert domain %s: %v", d.ID, err)
		}
	}
}

func TestRoundTripWorkedExample(t *testing.T) {
	s := openOrSkip(t)
	ctx := context.Background()
	want := model.WorkedExample()
	seed(t, s, want)
	if _, err := s.Promote(ctx, "node2"); err != nil {
		t.Fatalf("promote: %v", err)
	}

	got, err := s.LoadState(ctx)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("loaded state should validate: %v", err)
	}
	if len(got.Nodes) != 3 || len(got.Users) != 3 || len(got.Groups) != 2 || len(got.RoutePolicies) != 3 {
		t.Fatalf("unexpected counts: nodes=%d users=%d groups=%d policies=%d",
			len(got.Nodes), len(got.Users), len(got.Groups), len(got.RoutePolicies))
	}

	// dial_in round-trips for exits.
	n1, _ := got.NodeByID("node1")
	if n1.DialIn == nil || n1.DialIn.TargetSNI != "www.example-cdn.com" || n1.DialIn.UUID != "uuid-node1" {
		t.Fatalf("dial_in not round-tripped: %+v", n1.DialIn)
	}
	entry, _ := got.NodeByID("entryA")
	if entry.DialIn != nil {
		t.Errorf("entry node should have nil dial_in")
	}

	// The loaded state renders (store -> render closes the loop).
	if _, err := render.RenderNode(got, "entryA", time.Now(), render.Options{}); err != nil {
		t.Fatalf("render from loaded state: %v", err)
	}
}

func TestPromoteBumpsEpoch(t *testing.T) {
	s := openOrSkip(t)
	ctx := context.Background()
	seed(t, s, model.WorkedExample())

	cp, _ := s.ControlPlane(ctx)
	if cp.Epoch != 0 {
		t.Fatalf("fresh epoch should be 0, got %d", cp.Epoch)
	}
	// add-standby records node1 as the standby; promoting node1 must drop it from
	// the standby list so the new primary does not monitor replication to itself.
	if err := s.SetStandbys(ctx, []string{"node1"}); err != nil {
		t.Fatal(err)
	}
	e1, err := s.Promote(ctx, "node2")
	if err != nil {
		t.Fatal(err)
	}
	e2, _ := s.Promote(ctx, "node1")
	if e1 != 1 || e2 != 2 {
		t.Fatalf("epoch should increment 1,2; got %d,%d", e1, e2)
	}
	cp, _ = s.ControlPlane(ctx)
	if cp.ActiveNodeID != "node1" || cp.Epoch != 2 {
		t.Fatalf("control plane not updated: %+v", cp)
	}
	// node1 was the standby; after promoting it, it must be gone from the list.
	for _, id := range cp.StandbyNodeIDs {
		if id == "node1" {
			t.Errorf("promoted node1 should be dropped from standby_node_ids, got %v", cp.StandbyNodeIDs)
		}
	}

	// Promote also keeps pg_role consistent: the new active node is primary and
	// the old primary (node2, primary in the WorkedExample) is demoted to none.
	st, err := s.LoadState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	n1, _ := st.NodeByID("node1")
	n2, _ := st.NodeByID("node2")
	if n1.PGRole != model.PGPrimary {
		t.Errorf("node1 should be pg primary after promote, got %q", n1.PGRole)
	}
	if n2.PGRole != model.PGNone {
		t.Errorf("old primary node2 should be demoted to none, got %q", n2.PGRole)
	}
}

// TestFreshDatabasePromotes runs Promote against a database that only just had
// the migrations applied — no test reset()/seed helper involved. bootstrap.Run
// calls Promote as its last step on a brand-new install, and Promote does a
// plain UPDATE ... RETURNING with no upsert fallback, so the control_plane
// singleton row must already exist once the migrations finish, not rely on a
// test fixture to have inserted it.
func TestFreshDatabasePromotes(t *testing.T) {
	base := os.Getenv("TRUSTPANEL_TEST_DSN")
	if base == "" {
		t.Skip("set TRUSTPANEL_TEST_DSN to run store integration tests")
	}
	migrations, err := filepath.Glob("../../../migrations/pg/*.sql")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(migrations)
	dsn, err := setupTestDB(base, "trustpanel_store_freshseed_test", migrations)
	if err != nil {
		t.Fatalf("setup fresh db: %v", err)
	}
	s, err := Open(context.Background(), dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()
	ctx := context.Background()

	if err := s.UpsertNode(ctx, model.Node{
		ID: "node1", Name: "Node 1", PublicRole: model.RoleEntry,
		PublicIPs: []string{"203.0.113.10"}, AgentAddr: "127.0.0.1:8443",
	}); err != nil {
		t.Fatalf("upsert node: %v", err)
	}
	if _, err := s.Promote(ctx, "node1"); err != nil {
		t.Fatalf("Promote on a freshly migrated db: %v", err)
	}
}

func TestReplicationSlots(t *testing.T) {
	s := openOrSkip(t)
	ctx := context.Background()

	conn, err := pgx.Connect(ctx, testDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	const slot = "standby_unit_test_slot"
	_, _ = conn.Exec(ctx, "SELECT pg_drop_replication_slot($1)", slot) // best-effort pre-clean
	if _, err := conn.Exec(ctx, "SELECT pg_create_physical_replication_slot($1)", slot); err != nil {
		t.Fatalf("create slot: %v", err)
	}
	defer conn.Exec(ctx, "SELECT pg_drop_replication_slot($1)", slot)

	slots, err := s.ReplicationSlots(ctx)
	if err != nil {
		t.Fatalf("ReplicationSlots: %v", err)
	}
	var found *ReplSlot
	for i := range slots {
		if slots[i].Name == slot {
			found = &slots[i]
		}
	}
	if found == nil {
		t.Fatalf("created slot %q not returned; got %+v", slot, slots)
	}
	// A freshly-created slot has no consumer connected.
	if found.Active {
		t.Errorf("brand-new slot should be inactive, got active")
	}
}

func TestUpsertUpdatesAndDeletes(t *testing.T) {
	s := openOrSkip(t)
	ctx := context.Background()
	seed(t, s, model.WorkedExample())

	// Update a user's display name via upsert.
	u := model.User{ID: "u-alice", Username: "alice", Secret: "s", DisplayName: "Alice Renamed", Enabled: true, GroupID: "everyone"}
	if err := s.UpsertUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	got, _ := s.users(ctx)
	var found bool
	for _, x := range got {
		if x.ID == "u-alice" {
			found = true
			if x.DisplayName != "Alice Renamed" {
				t.Errorf("display name not updated: %q", x.DisplayName)
			}
		}
	}
	if !found {
		t.Fatal("alice missing")
	}

	if err := s.DeleteUser(ctx, "u-bob"); err != nil {
		t.Fatal(err)
	}
	got, _ = s.users(ctx)
	if len(got) != 2 {
		t.Fatalf("after delete want 2 users, got %d", len(got))
	}
}

func TestMigrateOnceIsIdempotent(t *testing.T) {
	s := openOrSkip(t)
	ctx := context.Background()
	const ddl = `CREATE TABLE migrate_once_probe (id int PRIMARY KEY)`
	// First application runs the DDL.
	applied, err := s.MigrateOnce(ctx, "0099_probe.sql", ddl)
	if err != nil || !applied {
		t.Fatalf("first apply: applied=%v err=%v", applied, err)
	}
	// Second application is skipped — re-running the DDL would fail with "already
	// exists", which is exactly the restart crash-loop this guards against.
	applied, err = s.MigrateOnce(ctx, "0099_probe.sql", ddl)
	if err != nil {
		t.Fatalf("second apply errored (not idempotent): %v", err)
	}
	if applied {
		t.Fatal("second apply should be skipped")
	}
	_, _ = s.pool.Exec(ctx, `DROP TABLE IF EXISTS migrate_once_probe`)
}

func TestAlertHeartbeat(t *testing.T) {
	s := openOrSkip(t)
	ctx := context.Background()

	// No stamp yet -> ok=false.
	if _, ok, err := s.AlertHeartbeatAge(ctx); err != nil || ok {
		t.Fatalf("fresh table: age ok=%v err=%v, want ok=false", ok, err)
	}
	if err := s.StampAlertHeartbeat(ctx); err != nil {
		t.Fatalf("stamp: %v", err)
	}
	age, ok, err := s.AlertHeartbeatAge(ctx)
	if err != nil || !ok {
		t.Fatalf("after stamp: ok=%v err=%v, want ok=true", ok, err)
	}
	if age < 0 || age > time.Minute {
		t.Fatalf("age %s should be ~0 right after stamping", age)
	}
}

// TestUpsertNodePreservesPGRole checks that an ordinary node upsert (e.g. a
// panel rename) posts no pg_role, so the 'none' zero value must NOT clobber the
// live PG primary — the store COALESCEs it back to the stored role.
func TestUpsertNodePreservesPGRole(t *testing.T) {
	s := openOrSkip(t)
	ctx := context.Background()
	seed(t, s, model.WorkedExample())

	// node2 is the primary in the WorkedExample; confirm, then re-upsert it with a
	// zero pg_role (as the node form would) and check the role survives.
	st, _ := s.LoadState(ctx)
	n2, ok := st.NodeByID("node2")
	if !ok || n2.PGRole != model.PGPrimary {
		t.Fatalf("precondition: node2 should be primary, got %q (ok=%v)", n2.PGRole, ok)
	}
	n2.PGRole = "" // the node form does not carry pg_role
	n2.Name = "node2-renamed"
	if err := s.UpsertNode(ctx, n2); err != nil {
		t.Fatal(err)
	}
	st, _ = s.LoadState(ctx)
	got, _ := st.NodeByID("node2")
	if got.PGRole != model.PGPrimary {
		t.Errorf("pg_role should survive a plain edit, got %q", got.PGRole)
	}
	if got.Name != "node2-renamed" {
		t.Errorf("the rename should still apply, got %q", got.Name)
	}
}

func TestStandbys(t *testing.T) {
	s := openOrSkip(t)
	ctx := context.Background()
	seed(t, s, model.WorkedExample())
	if err := s.SetStandbys(ctx, []string{"node1"}); err != nil {
		t.Fatal(err)
	}
	cp, _ := s.ControlPlane(ctx)
	if len(cp.StandbyNodeIDs) != 1 || cp.StandbyNodeIDs[0] != "node1" {
		t.Fatalf("standbys not set: %+v", cp.StandbyNodeIDs)
	}
}

// readTraffic returns the accumulated (rx, tx, last_abs_rx, last_abs_tx) for one
// (user, entry) row, or all-zero if absent.
func readTraffic(t *testing.T, s *Store, userID, entryID string) (rx, tx, lastRx, lastTx int64) {
	t.Helper()
	err := s.pool.QueryRow(context.Background(),
		`SELECT rx_bytes, tx_bytes, last_abs_rx, last_abs_tx FROM user_traffic
		 WHERE user_id=$1 AND entry_node_id=$2`, userID, entryID).Scan(&rx, &tx, &lastRx, &lastTx)
	if err != nil && !strings.Contains(err.Error(), "no rows") {
		t.Fatal(err)
	}
	return
}

func usedTraffic(t *testing.T, s *Store, userID string) int64 {
	t.Helper()
	var used int64
	if err := s.pool.QueryRow(context.Background(),
		`SELECT used_traffic FROM users WHERE id=$1`, userID).Scan(&used); err != nil {
		t.Fatal(err)
	}
	return used
}

func TestAccumulateTraffic(t *testing.T) {
	s := openOrSkip(t)
	ctx := context.Background()
	seed(t, s, model.WorkedExample())

	// First sample sets the totals (delta == absolute).
	if err := s.AccumulateTraffic(ctx, "u-alice", "entryA", 100, 300); err != nil {
		t.Fatal(err)
	}
	rx, tx, lrx, ltx := readTraffic(t, s, "u-alice", "entryA")
	if rx != 100 || tx != 300 || lrx != 100 || ltx != 300 {
		t.Fatalf("first sample: rx=%d tx=%d last=(%d,%d)", rx, tx, lrx, ltx)
	}
	if u := usedTraffic(t, s, "u-alice"); u != 400 {
		t.Fatalf("used_traffic after first sample = %d, want 400", u)
	}

	// Second, increasing sample adds only the delta.
	if err := s.AccumulateTraffic(ctx, "u-alice", "entryA", 150, 500); err != nil {
		t.Fatal(err)
	}
	rx, tx, lrx, ltx = readTraffic(t, s, "u-alice", "entryA")
	if rx != 150 || tx != 500 || lrx != 150 || ltx != 500 {
		t.Fatalf("second sample: rx=%d tx=%d last=(%d,%d)", rx, tx, lrx, ltx)
	}
	if u := usedTraffic(t, s, "u-alice"); u != 650 {
		t.Fatalf("used_traffic after second sample = %d, want 650", u)
	}

	// A smaller sample means sing-box restarted (counter reset): add the new
	// absolute as the delta.
	if err := s.AccumulateTraffic(ctx, "u-alice", "entryA", 20, 5); err != nil {
		t.Fatal(err)
	}
	rx, tx, lrx, ltx = readTraffic(t, s, "u-alice", "entryA")
	if rx != 170 || tx != 505 || lrx != 20 || ltx != 5 {
		t.Fatalf("reset sample: rx=%d tx=%d last=(%d,%d)", rx, tx, lrx, ltx)
	}
	if u := usedTraffic(t, s, "u-alice"); u != 675 {
		t.Fatalf("used_traffic after reset = %d, want 675", u)
	}

	// A second entry contributes its own row; totals sum across entries.
	if err := s.AccumulateTraffic(ctx, "u-alice", "node1", 1000, 2000); err != nil {
		t.Fatal(err)
	}
	totals, err := s.UserTrafficTotals(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var alice *UserTrafficTotal
	for i := range totals {
		if totals[i].UserID == "u-alice" {
			alice = &totals[i]
		}
	}
	if alice == nil {
		t.Fatal("alice missing from totals")
	}
	if alice.RxBytes != 1170 || alice.TxBytes != 2505 || alice.TotalBytes != 3675 {
		t.Fatalf("summed totals: rx=%d tx=%d total=%d", alice.RxBytes, alice.TxBytes, alice.TotalBytes)
	}
	if len(totals) != 3 { // every user appears, even with zero traffic
		t.Fatalf("want 3 users in totals, got %d", len(totals))
	}
}

func TestTrafficSamplesAndActivity(t *testing.T) {
	s := openOrSkip(t)
	ctx := context.Background()
	seed(t, s, model.WorkedExample())

	// Two ticks that move bytes write samples; a zero-delta tick writes none.
	if err := s.AccumulateTraffic(ctx, "u-alice", "entryA", 100, 300); err != nil {
		t.Fatal(err)
	}
	if err := s.AccumulateTraffic(ctx, "u-alice", "entryA", 150, 500); err != nil { // +50,+200
		t.Fatal(err)
	}
	if err := s.AccumulateTraffic(ctx, "u-alice", "entryA", 150, 500); err != nil { // zero delta -> no sample
		t.Fatal(err)
	}

	// RecentActivity sums the deltas in the window: alice moved (100+50)=150 rx,
	// (300+200)=500 tx; bob moved nothing and is absent.
	act, err := s.RecentActivity(ctx, time.Now().Add(-time.Hour), 0)
	if err != nil {
		t.Fatal(err)
	}
	a, ok := act["u-alice"]
	if !ok {
		t.Fatal("alice should be active")
	}
	if a.RxBytes != 150 || a.TxBytes != 500 {
		t.Fatalf("alice recent rx/tx = %d/%d, want 150/500", a.RxBytes, a.TxBytes)
	}
	if _, ok := act["u-bob"]; ok {
		t.Fatal("bob moved no bytes and must be absent from activity")
	}

	// The minBytes floor filters out clients under the threshold: alice moved
	// 150+500=650 bytes, so a floor of 650 keeps her but 651 drops her.
	atFloor, err := s.RecentActivity(ctx, time.Now().Add(-time.Hour), 650)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := atFloor["u-alice"]; !ok {
		t.Fatal("alice moved exactly the floor and must count as active")
	}
	overFloor, err := s.RecentActivity(ctx, time.Now().Add(-time.Hour), 651)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := overFloor["u-alice"]; ok {
		t.Fatal("alice is below a higher floor and must be absent")
	}

	// A future-only window sees nothing (active-now stops when traffic stops).
	act2, err := s.RecentActivity(ctx, time.Now().Add(time.Hour), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(act2) != 0 {
		t.Fatalf("future window should be empty, got %v", act2)
	}

	// Series returns bucketed points.
	series, err := s.UserTrafficSeries(ctx, "u-alice", time.Now().Add(-time.Hour), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	var sumRx, sumTx int64
	for _, b := range series {
		sumRx += b.RxBytes
		sumTx += b.TxBytes
	}
	if sumRx != 150 || sumTx != 500 {
		t.Fatalf("series totals rx/tx = %d/%d, want 150/500", sumRx, sumTx)
	}

	// Prune drops everything older than now; activity then empties.
	if err := s.PruneTrafficSamples(ctx, time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	act3, err := s.RecentActivity(ctx, time.Now().Add(-time.Hour), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(act3) != 0 {
		t.Fatalf("after prune, activity should be empty, got %v", act3)
	}
}

func TestNodeLimitsAndTraffic(t *testing.T) {
	s := openOrSkip(t)
	ctx := context.Background()
	seed(t, s, model.WorkedExample())

	// Limits round-trip through LoadState and survive an ordinary node upsert.
	lim := &model.NodeLimits{CPUCores: 2, MemoryMB: 4096, DiskGB: 80, TrafficGB: 4000}
	if err := s.SetNodeLimits(ctx, "node1", lim); err != nil {
		t.Fatal(err)
	}
	st, _ := s.LoadState(ctx)
	n1, _ := st.NodeByID("node1")
	if n1.Limits == nil || n1.Limits.MemoryMB != 4096 || n1.Limits.TrafficGB != 4000 {
		t.Fatalf("limits not stored: %+v", n1.Limits)
	}
	// Re-upsert the node (no limits set on the struct) must NOT wipe limits.
	if err := s.UpsertNode(ctx, n1WithoutLimits(st)); err != nil {
		t.Fatal(err)
	}
	st, _ = s.LoadState(ctx)
	n1, _ = st.NodeByID("node1")
	if n1.Limits == nil || n1.Limits.MemoryMB != 4096 {
		t.Fatalf("limits wiped by upsert: %+v", n1.Limits)
	}

	// Node traffic: first sample, increasing delta, then month rollover resets.
	jan := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
	if err := s.AccumulateNodeTraffic(ctx, "node1", 1000, 2000, jan); err != nil {
		t.Fatal(err)
	}
	if err := s.AccumulateNodeTraffic(ctx, "node1", 1500, 2500, jan); err != nil {
		t.Fatal(err)
	}
	totals, _ := s.NodeTrafficTotals(ctx)
	got := nodeTotal(totals, "node1")
	if got.RxBytes != 1500 || got.TxBytes != 2500 || got.Period != "2026-01" {
		t.Fatalf("january totals wrong: %+v", got)
	}
	// February: counters keep climbing but the window resets.
	feb := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	if err := s.AccumulateNodeTraffic(ctx, "node1", 1600, 2600, feb); err != nil {
		t.Fatal(err)
	}
	if err := s.AccumulateNodeTraffic(ctx, "node1", 1700, 2800, feb); err != nil {
		t.Fatal(err)
	}
	got = nodeTotal(totals2(s, ctx), "node1")
	if got.Period != "2026-02" || got.RxBytes != 100 || got.TxBytes != 200 {
		t.Fatalf("february (post-reset) totals wrong: %+v", got)
	}

	// Time-series: only the two moving ticks (the +500/+500 within Jan and the
	// +100/+200 within Feb) wrote samples; the first reading and the month
	// rollover did not. Samples are stamped at insert time, so a wide window
	// captures both. (3rd arg is bucket size.)
	series, err := s.NodeTrafficSeries(ctx, "node1", time.Now().Add(-time.Hour), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	var sumRx, sumTx int64
	for _, b := range series {
		sumRx += b.RxBytes
		sumTx += b.TxBytes
	}
	if sumRx != 600 || sumTx != 700 {
		t.Fatalf("node series sum = rx %d tx %d, want 600/700 (samples=%+v)", sumRx, sumTx, series)
	}
}

func n1WithoutLimits(st model.State) model.Node {
	n, _ := st.NodeByID("node1")
	n.Limits = nil
	return n
}
func totals2(s *Store, ctx context.Context) []NodeTrafficTotal {
	t, _ := s.NodeTrafficTotals(ctx)
	return t
}
func nodeTotal(ts []NodeTrafficTotal, id string) NodeTrafficTotal {
	for _, t := range ts {
		if t.NodeID == id {
			return t
		}
	}
	return NodeTrafficTotal{}
}

// TestNodeResourceSamples covers backlog #3 (Nodes-tab CPU/Memory history):
// RecordNodeResourceSample writes a raw gauge sample, NodeResourceSeries
// buckets/averages it (not sums, unlike traffic), and PruneTrafficSamples also
// bounds this table.
func TestNodeResourceSamples(t *testing.T) {
	s := openOrSkip(t)
	ctx := context.Background()
	seed(t, s, model.WorkedExample())

	// Two samples for node1: load1=2 of 4 cores (50%) and load1=1 of 4 (25%) ->
	// bucket average 37.5%. Memory 2048/4096 then 1024/4096 -> average 1536 used.
	if err := s.RecordNodeResourceSample(ctx, "node1", 2.0, 4, 2048, 4096, 40, 80); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordNodeResourceSample(ctx, "node1", 1.0, 4, 1024, 4096, 40, 80); err != nil {
		t.Fatal(err)
	}
	// A different node must not bleed into node1's series.
	if err := s.RecordNodeResourceSample(ctx, "node2", 4.0, 4, 4096, 4096, 40, 80); err != nil {
		t.Fatal(err)
	}

	series, err := s.NodeResourceSeries(ctx, "node1", time.Now().Add(-time.Hour), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(series) != 1 {
		t.Fatalf("want 1 bucket, got %d (%+v)", len(series), series)
	}
	if got := series[0].CPUPercent; got < 37.4 || got > 37.6 {
		t.Fatalf("cpu_pct = %v, want ~37.5", got)
	}
	if series[0].MemUsedMB != 1536 || series[0].MemTotalMB != 4096 {
		t.Fatalf("mem = %d/%d, want 1536/4096", series[0].MemUsedMB, series[0].MemTotalMB)
	}

	// A future-only window sees nothing.
	empty, err := s.NodeResourceSeries(ctx, "node1", time.Now().Add(time.Hour), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(empty) != 0 {
		t.Fatalf("future window should be empty, got %+v", empty)
	}

	// PruneTrafficSamples also bounds node_resource_samples.
	if err := s.PruneTrafficSamples(ctx, time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	pruned, err := s.NodeResourceSeries(ctx, "node1", time.Now().Add(-time.Hour), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(pruned) != 0 {
		t.Fatalf("after prune, node1 series should be empty, got %+v", pruned)
	}
}

func TestNodeBillingAndHealth(t *testing.T) {
	s := openOrSkip(t)
	ctx := context.Background()
	seed(t, s, model.WorkedExample())

	until := time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)
	if err := s.SetNodeBilling(ctx, "node1", &model.NodeBilling{PaidUntil: &until, TermMonths: 3}); err != nil {
		t.Fatal(err)
	}
	// Invalid term rejected.
	if err := s.SetNodeBilling(ctx, "node1", &model.NodeBilling{TermMonths: 5}); err == nil {
		t.Fatal("term 5 should be rejected")
	}
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	if err := s.SetNodeHealth(ctx, "node1", model.HealthHealthy, &now); err != nil {
		t.Fatal(err)
	}
	st, _ := s.LoadState(ctx)
	n1, _ := st.NodeByID("node1")
	if n1.Billing == nil || n1.Billing.TermMonths != 3 || n1.Billing.PaidUntil == nil {
		t.Fatalf("billing not stored: %+v", n1.Billing)
	}
	if n1.Health != model.HealthHealthy || n1.LastSeenAt == nil {
		t.Fatalf("health/last_seen not set: %s %v", n1.Health, n1.LastSeenAt)
	}
	// A failed poll (lastSeen nil) updates health but keeps last_seen.
	if err := s.SetNodeHealth(ctx, "node1", model.HealthDegraded, nil); err != nil {
		t.Fatal(err)
	}
	st, _ = s.LoadState(ctx)
	n1, _ = st.NodeByID("node1")
	if n1.Health != model.HealthDegraded || n1.LastSeenAt == nil {
		t.Fatalf("degraded should keep last_seen: %s %v", n1.Health, n1.LastSeenAt)
	}
}

// A panel edit (e.g. rename) re-upserts the node from the form, which carries no
// health/last_seen. Those runtime fields, owned by the probe loop, must survive
// the upsert instead of resetting to "unknown"/NULL (which briefly showed the
// node as dead until the next poll).
func TestNodeMaintenance(t *testing.T) {
	s := openOrSkip(t)
	ctx := context.Background()
	seed(t, s, model.WorkedExample())

	// Default is not draining.
	st, _ := s.LoadState(ctx)
	if n, _ := st.NodeByID("node1"); n.Maintenance {
		t.Fatal("node1 should not start in maintenance")
	}

	if err := s.SetNodeMaintenance(ctx, "node1", true); err != nil {
		t.Fatal(err)
	}
	st, _ = s.LoadState(ctx)
	if n, _ := st.NodeByID("node1"); !n.Maintenance {
		t.Fatal("node1 should be draining after SetNodeMaintenance(true)")
	}

	// An ordinary upsert (panel rename form: no maintenance field) must not clobber it.
	n1, _ := st.NodeByID("node1")
	n1.Name = "node1-renamed"
	if err := s.UpsertNode(ctx, n1); err != nil {
		t.Fatalf("rename upsert: %v", err)
	}
	st, _ = s.LoadState(ctx)
	if n, _ := st.NodeByID("node1"); !n.Maintenance {
		t.Fatal("ordinary upsert clobbered the maintenance flag")
	}

	if err := s.SetNodeMaintenance(ctx, "node1", false); err != nil {
		t.Fatal(err)
	}
	st, _ = s.LoadState(ctx)
	if n, _ := st.NodeByID("node1"); n.Maintenance {
		t.Fatal("node1 should be back in rotation after SetNodeMaintenance(false)")
	}
}

func TestUpsertNodePreservesObservedHealth(t *testing.T) {
	s := openOrSkip(t)
	ctx := context.Background()
	seed(t, s, model.WorkedExample())

	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	if err := s.SetNodeHealth(ctx, "node1", model.HealthHealthy, &now); err != nil {
		t.Fatal(err)
	}

	// Re-upsert "node1" the way the panel form does: a fresh struct with no
	// Health/LastSeenAt, only a new name.
	pre, _ := s.LoadState(ctx)
	renamed, _ := pre.NodeByID("node1")
	renamed.Name = "node1-renamed"
	renamed.Health = ""      // form does not send health
	renamed.LastSeenAt = nil // form does not send last_seen
	if err := s.UpsertNode(ctx, renamed); err != nil {
		t.Fatalf("rename upsert: %v", err)
	}

	st, _ := s.LoadState(ctx)
	n1, _ := st.NodeByID("node1")
	if n1.Name != "node1-renamed" {
		t.Fatalf("rename not applied: %q", n1.Name)
	}
	if n1.Health != model.HealthHealthy {
		t.Fatalf("rename clobbered health: got %q, want healthy", n1.Health)
	}
	if n1.LastSeenAt == nil || !n1.LastSeenAt.Equal(now) {
		t.Fatalf("rename clobbered last_seen: %v", n1.LastSeenAt)
	}
}

func TestEventLogAppendListFilter(t *testing.T) {
	s := openOrSkip(t)
	ctx := context.Background()
	_, _ = s.pool.Exec(ctx, "TRUNCATE events")

	must := func(e model.Event) {
		if err := s.AppendEvent(ctx, e); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	must(model.Event{Kind: model.EventAdmin, Severity: model.SeverityInfo, Message: "saved user u1", Actor: "admin"})
	must(model.Event{Kind: model.EventAlert, Severity: model.SeverityCrit, Message: "node down"})
	must(model.Event{Kind: model.EventAdmin, Severity: model.SeverityWarn, Message: "deleted node n1", Actor: "admin"})
	// An operator-namespaced event must not surface in the admin namespace.
	must(model.Event{Kind: model.EventAdmin, Severity: model.SeverityInfo, Message: "saved user secretclient", Actor: "op1", OwnerID: "op1"})

	all, err := s.ListEvents(ctx, "", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("admin namespace: want 3 events, got %d", len(all))
	}
	// Newest first.
	if all[0].Message != "deleted node n1" {
		t.Fatalf("expected newest first, got %q", all[0].Message)
	}
	for _, e := range all {
		if e.OwnerID != "" {
			t.Fatalf("operator event leaked into admin namespace: %q", e.Message)
		}
	}
	// Filter by kind, within the admin namespace.
	admin, err := s.ListEvents(ctx, string(model.EventAdmin), "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(admin) != 2 {
		t.Fatalf("want 2 admin events, got %d", len(admin))
	}
	for _, e := range admin {
		if e.Kind != model.EventAdmin {
			t.Fatalf("filter leaked kind %q", e.Kind)
		}
	}
	// The operator sees only their own namespace.
	op, err := s.ListEvents(ctx, "", "op1", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(op) != 1 || op[0].Message != "saved user secretclient" {
		t.Fatalf("operator namespace: want 1 own event, got %v", op)
	}
}

func TestSettingsRoundTrip(t *testing.T) {
	s := openOrSkip(t)
	ctx := context.Background()

	// Fresh DB: settings default to the zero value (everything disabled).
	got, err := s.GetSettings(ctx)
	if err != nil {
		t.Fatalf("get settings: %v", err)
	}
	if got.Bot.Enabled || got.Alert.Enabled || got.Bot.Token != "" {
		t.Fatalf("expected zero-value settings, got %+v", got)
	}

	want := model.Settings{
		Bot:    model.BotSettings{Enabled: true, Token: "123:abc"},
		Alert:  model.AlertSettings{Enabled: true, Token: "456:def", ChatID: "-100123"},
		Backup: model.BackupSettings{Enabled: true, ChatID: "-100999", AgeRecipient: "age1abc", ChunkBytes: 1234},
	}
	if err := s.SaveSettings(ctx, want); err != nil {
		t.Fatalf("save settings: %v", err)
	}
	got, err = s.GetSettings(ctx)
	if err != nil {
		t.Fatalf("get settings: %v", err)
	}
	if !got.Bot.Enabled || got.Bot.Token != "123:abc" {
		t.Fatalf("bot settings round-trip wrong: %+v", got.Bot)
	}
	if !got.Alert.Enabled || got.Alert.Token != "456:def" || got.Alert.ChatID != "-100123" {
		t.Fatalf("alert settings round-trip wrong: %+v", got.Alert)
	}
	if !got.Backup.Enabled || got.Backup.ChatID != "-100999" || got.Backup.AgeRecipient != "age1abc" || got.Backup.ChunkBytes != 1234 {
		t.Fatalf("backup settings round-trip wrong: %+v", got.Backup)
	}

	// Saving again overwrites (upsert), not appends.
	if err := s.SaveSettings(ctx, model.Settings{Bot: model.BotSettings{Token: "new"}}); err != nil {
		t.Fatalf("re-save: %v", err)
	}
	got, _ = s.GetSettings(ctx)
	if got.Bot.Token != "new" || got.Alert.Token != "" || got.Bot.Enabled {
		t.Fatalf("upsert did not replace: %+v", got)
	}
}

func TestRolesAndOwnership(t *testing.T) {
	s := openOrSkip(t)
	ctx := context.Background()
	// Accounts persist across tests (reset() does not truncate admins); clean up.
	_, _ = s.pool.Exec(ctx, "DELETE FROM admins")

	if err := s.UpsertAdmin(ctx, "boss", "h1"); err != nil { // bootstrap admin
		t.Fatalf("create admin: %v", err)
	}
	if err := s.CreateAccount(ctx, "op1", "h2", model.RoleOperator); err != nil {
		t.Fatalf("create operator: %v", err)
	}
	// CreateAccount must not silently overwrite an existing account.
	if err := s.CreateAccount(ctx, "op1", "h3", model.RoleAdmin); err == nil {
		t.Fatal("CreateAccount should fail on an existing username")
	}

	boss, err := s.AdminByUsername(ctx, "boss")
	if err != nil || !boss.Role.IsAdmin() {
		t.Fatalf("boss role: %+v err=%v", boss, err)
	}
	op, err := s.AdminByUsername(ctx, "op1")
	if err != nil || op.Role != model.RoleOperator || op.IsBootstrapOwner() {
		t.Fatalf("op role: %+v err=%v", op, err)
	}

	// Telegram binding + reverse lookup (role-aware bot auth).
	id := int64(123456)
	if err := s.SetAccountTelegram(ctx, "op1", &id, "-100777"); err != nil {
		t.Fatalf("bind telegram: %v", err)
	}
	byTG, err := s.AccountByTelegramID(ctx, id)
	if err != nil || byTG.Username != "op1" || byTG.AlertChatID != "-100777" {
		t.Fatalf("AccountByTelegramID: %+v err=%v", byTG, err)
	}
	if _, err := s.AccountByTelegramID(ctx, 999); !errors.Is(err, ErrNoAdmin) {
		t.Fatalf("unbound id should be ErrNoAdmin, got %v", err)
	}

	// owner_id round-trips through a group + user owned by op1.
	if err := s.UpsertGroup(ctx, model.Group{ID: "g-op", Name: "op group", OwnerID: "op1"}); err != nil {
		t.Fatalf("upsert owned group: %v", err)
	}
	if err := s.UpsertUser(ctx, model.User{ID: "u-op", Username: "client1", GroupID: "g-op", Enabled: true, OwnerID: "op1"}); err != nil {
		t.Fatalf("upsert owned user: %v", err)
	}
	st, err := s.LoadState(ctx)
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	if ownerOf(st.Users, "u-op") != "op1" || ownerOf2(st.Groups, "g-op") != "op1" {
		t.Fatalf("owner_id did not round-trip: users=%+v groups=%+v", st.Users, st.Groups)
	}

	// Deleting the operator reassigns its rows to the admin namespace (NULL/"")
	// and must not be blocked by the owner_id FK.
	if err := s.DeleteAdmin(ctx, "op1"); err != nil {
		t.Fatalf("delete operator: %v", err)
	}
	st, _ = s.LoadState(ctx)
	if ownerOf(st.Users, "u-op") != "" || ownerOf2(st.Groups, "g-op") != "" {
		t.Fatalf("owned rows not reassigned to admin namespace: %+v %+v", st.Users, st.Groups)
	}

	// The last admin cannot be deleted.
	if err := s.DeleteAdmin(ctx, "boss"); err == nil {
		t.Fatal("deleting the last admin must fail")
	}
}

// TestRoleChange proves a role flip is non-destructive: the account's namespace
// and its owned resources are untouched, only the role column moves.
func TestRoleChange(t *testing.T) {
	s := openOrSkip(t)
	ctx := context.Background()
	_, _ = s.pool.Exec(ctx, "DELETE FROM admins")
	_, _ = s.pool.Exec(ctx, "DELETE FROM namespaces")

	if err := s.UpsertAdmin(ctx, "boss", "h1"); err != nil {
		t.Fatalf("create admin: %v", err)
	}
	if err := s.CreateAccount(ctx, "op1", "h2", model.RoleOperator); err != nil {
		t.Fatalf("create operator: %v", err)
	}
	if err := s.UpsertGroup(ctx, model.Group{ID: "g-op", Name: "op", OwnerID: "op1"}); err != nil {
		t.Fatalf("upsert owned group: %v", err)
	}
	if err := s.UpsertUser(ctx, model.User{ID: "u-op", Username: "c1", GroupID: "g-op", Enabled: true, OwnerID: "op1"}); err != nil {
		t.Fatalf("upsert owned user: %v", err)
	}

	if err := s.SetRole(ctx, "op1", model.RoleAdmin); err != nil {
		t.Fatalf("set role: %v", err)
	}
	a, err := s.AdminByUsername(ctx, "op1")
	if err != nil || a.Role != model.RoleAdmin {
		t.Fatalf("role not changed: %+v err=%v", a, err)
	}
	// Namespace unchanged -> still scoped to its own namespace, NOT the bootstrap owner.
	if a.Namespace() != "op1" || a.IsBootstrapOwner() {
		t.Fatalf("namespace must be unchanged and not bootstrap: ns=%q isBootstrap=%v", a.Namespace(), a.IsBootstrapOwner())
	}
	// The owned resource did not move.
	st, _ := s.LoadState(ctx)
	if ownerOf(st.Users, "u-op") != "op1" {
		t.Fatalf("resource owner_id changed on role flip: %+v", st.Users)
	}
	if err := s.SetRole(ctx, "ghost", model.RoleAdmin); !errors.Is(err, ErrNoAdmin) {
		t.Fatalf("SetRole on missing account should be ErrNoAdmin, got %v", err)
	}
}

// TestMultiMemberNamespace covers the new delete semantics: deleting a non-last
// member leaves the namespace's resources; deleting the last empties it (resources
// -> infra, namespaces row gone); the last admin of a populated namespace is held.
func TestMultiMemberNamespace(t *testing.T) {
	s := openOrSkip(t)
	ctx := context.Background()
	_, _ = s.pool.Exec(ctx, "DELETE FROM admins")
	_, _ = s.pool.Exec(ctx, "DELETE FROM namespaces")

	if err := s.UpsertAdmin(ctx, "boss", "h1"); err != nil { // fleet owner
		t.Fatalf("create admin: %v", err)
	}
	// Mint a namespace "team" with two members: an admin (lead) and an operator.
	if err := s.CreateNamespace(ctx, "team", "Team"); err != nil {
		t.Fatalf("create namespace: %v", err)
	}
	if err := s.CreateAccountIn(ctx, "lead", "h2", model.RoleAdmin, "team"); err != nil {
		t.Fatalf("create lead: %v", err)
	}
	if err := s.CreateAccountIn(ctx, "member", "h3", model.RoleOperator, "team"); err != nil {
		t.Fatalf("create member: %v", err)
	}
	if err := s.UpsertGroup(ctx, model.Group{ID: "g-team", Name: "team", OwnerID: "team"}); err != nil {
		t.Fatalf("upsert team group: %v", err)
	}
	if err := s.UpsertUser(ctx, model.User{ID: "u-team", Username: "c", GroupID: "g-team", Enabled: true, OwnerID: "team"}); err != nil {
		t.Fatalf("upsert team user: %v", err)
	}

	// Both members see the namespace via ListAdminsIn.
	mem, err := s.ListAdminsIn(ctx, "team")
	if err != nil || len(mem) != 2 {
		t.Fatalf("ListAdminsIn(team) = %d (%v)", len(mem), err)
	}

	// Cannot delete the last admin of a namespace while the operator remains.
	if err := s.DeleteAdmin(ctx, "lead"); err == nil {
		t.Fatal("deleting the last admin of a populated namespace must fail")
	}

	// Delete the operator (non-last): the namespace's resources stay put.
	if err := s.DeleteAdmin(ctx, "member"); err != nil {
		t.Fatalf("delete member: %v", err)
	}
	st, _ := s.LoadState(ctx)
	if ownerOf(st.Users, "u-team") != "team" {
		t.Fatalf("resources moved on non-last delete: %+v", st.Users)
	}

	// Now lead is the last account: deleting it empties the namespace -> resources
	// to infra ("") and the namespaces row is dropped.
	if err := s.DeleteAdmin(ctx, "lead"); err != nil {
		t.Fatalf("delete last member: %v", err)
	}
	st, _ = s.LoadState(ctx)
	if ownerOf(st.Users, "u-team") != "" {
		t.Fatalf("resources not reassigned to infra on emptying: %+v", st.Users)
	}
	var n int
	if err := s.pool.QueryRow(ctx, "SELECT count(*) FROM namespaces WHERE id='team'").Scan(&n); err != nil || n != 0 {
		t.Fatalf("namespaces row not dropped: count=%d err=%v", n, err)
	}
}

func ownerOf(us []model.User, id string) string {
	for _, u := range us {
		if u.ID == id {
			return u.OwnerID
		}
	}
	return "<missing>"
}

func ownerOf2(gs []model.Group, id string) string {
	for _, g := range gs {
		if g.ID == id {
			return g.OwnerID
		}
	}
	return "<missing>"
}
