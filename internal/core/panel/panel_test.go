package panel

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"trustpanel/internal/core/agent"
	"trustpanel/internal/core/agentapi"
	"trustpanel/internal/core/cluster"
	"trustpanel/internal/core/controller"
	"trustpanel/internal/core/model"
	"trustpanel/internal/core/pki"
	"trustpanel/internal/core/provision"
	"trustpanel/internal/core/render"
	"trustpanel/internal/core/store"
	"trustpanel/internal/core/watchdog"
)

var testDSN string

func TestMain(m *testing.M) {
	if base := os.Getenv("TRUSTPANEL_TEST_DSN"); base != "" {
		// Glob (sorted) so newly-added migrations are picked up automatically — a
		// hardcoded list silently drifts (e.g. 0009 was missed, breaking the schema).
		migrations, err := filepath.Glob("../../../migrations/pg/*.sql")
		if err != nil {
			fmt.Println("panel test migration glob:", err)
		}
		sort.Strings(migrations)
		dsn, err := setupTestDB(base, "trustpanel_panel_test", migrations)
		if err != nil {
			fmt.Println("panel test db setup:", err)
		} else {
			testDSN = dsn
		}
	}
	os.Exit(m.Run())
}

// setupTestDB creates a fresh per-package database off the base DSN and applies
// the migrations, isolating this package from others under `go test ./...`.
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

func resetDB(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, testDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	_, err = conn.Exec(ctx,
		`TRUNCATE nodes, groups, users, route_policies, domains, user_traffic, admins CASCADE;
		 INSERT INTO control_plane (id) VALUES (true)
		   ON CONFLICT (id) DO UPDATE SET active_node_id=NULL, epoch=0, standby_node_ids='{}';`)
	if err != nil {
		t.Fatal(err)
	}
}

func newPanel(t *testing.T) (*Panel, *store.Store, controller.Layout) {
	t.Helper()
	if testDSN == "" {
		t.Skip("set TRUSTPANEL_TEST_DSN to run panel tests")
	}
	resetDB(t)
	st, err := store.Open(context.Background(), testDSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	layout := controller.Layout{TrustTunnelDir: t.TempDir(), SingBoxDir: t.TempDir()}
	fleet := controller.NewFleet(controller.NewClient(nil), layout, render.Options{})
	fleet.RuleSets = fakeRuleSets{}
	return New(st, fleet, NewSessionManager(time.Hour)), st, layout
}

// fakeRuleSets returns a valid (magic-prefixed) .srs for any tag.
type fakeRuleSets struct{}

func (fakeRuleSets) Get(_ context.Context, tag string) ([]byte, error) {
	return append([]byte("SRS\x01"), []byte(tag)...), nil
}

// client wraps an httptest server with a cookie jar so sessions persist.
func newClient(t *testing.T, p *Panel) (*http.Client, string) {
	t.Helper()
	ts := httptest.NewServer(p.Handler())
	t.Cleanup(ts.Close)
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar}, ts.URL
}

func do(t *testing.T, c *http.Client, method, url string, body any) (int, []byte) {
	t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req, _ := http.NewRequest(method, url, rdr)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	buf := new(bytes.Buffer)
	_, _ = buf.ReadFrom(resp.Body)
	return resp.StatusCode, buf.Bytes()
}

func TestAuthBootstrapAndLogin(t *testing.T) {
	p, _, _ := newPanel(t)
	c, url := newClient(t, p)

	// No admin yet: protected route is open (bootstrap).
	if code, _ := do(t, c, http.MethodGet, url+"/api/state", nil); code != http.StatusOK {
		t.Fatalf("bootstrap state should be open, got %d", code)
	}

	// Create the first admin.
	if code, body := do(t, c, http.MethodPost, url+"/api/admin",
		map[string]string{"username": "admin", "password": "supersecret"}); code != http.StatusOK {
		t.Fatalf("create admin: %d %s", code, body)
	}

	// Now protected routes require auth.
	c2, _ := newClient(t, p) // fresh jar, no session
	c2url := url
	if code, _ := do(t, c2, http.MethodGet, c2url+"/api/state", nil); code != http.StatusUnauthorized {
		t.Fatalf("state should require auth once an admin exists, got %d", code)
	}

	// Wrong password rejected.
	if code, _ := do(t, c2, http.MethodPost, url+"/api/auth/login",
		map[string]string{"username": "admin", "password": "wrong"}); code != http.StatusUnauthorized {
		t.Fatalf("wrong password should be 401, got %d", code)
	}

	// Correct login, then protected access works (same jar carries the cookie).
	if code, body := do(t, c2, http.MethodPost, url+"/api/auth/login",
		map[string]string{"username": "admin", "password": "supersecret"}); code != http.StatusOK {
		t.Fatalf("login: %d %s", code, body)
	}
	if code, _ := do(t, c2, http.MethodGet, url+"/api/state", nil); code != http.StatusOK {
		t.Fatalf("authed state should be 200, got %d", code)
	}

	// Logout revokes access.
	do(t, c2, http.MethodPost, url+"/api/auth/logout", nil)
	if code, _ := do(t, c2, http.MethodGet, url+"/api/state", nil); code != http.StatusUnauthorized {
		t.Fatalf("after logout should be 401, got %d", code)
	}
}

// TestNodeEditPreservesRealityKeys checks that editing an exit node with a
// key-light dial_in (no public_key/priv_key/short_id — e.g. because priv_key is
// redacted from reads) must NOT wipe the exit's Reality identity; the stored keys
// are merged back server-side.
func TestNodeEditPreservesRealityKeys(t *testing.T) {
	p, _, _ := newPanel(t)
	c, url := newClient(t, p) // no admin -> open

	exit := model.Node{
		ID: "node1", Name: "Exit 1", PublicRole: model.RoleExit,
		PublicIPs: []string{"203.0.113.21"}, AgentAddr: "203.0.113.21:8443",
		DialIn: &model.DialIn{Proto: model.DialInProtoVLESSReality, Port: 443, UUID: "u1",
			TargetSNI: "www.cdn.example", PublicKey: "PUBKEY123", PrivKey: "PRIVKEY456", ShortID: "ab12"},
	}
	if code, body := do(t, c, http.MethodPost, url+"/api/nodes", exit); code != http.StatusOK {
		t.Fatalf("create exit node: %d %s", code, body)
	}

	// A rename posting a key-light dial_in (the redacted round-trip case).
	edit := model.Node{
		ID: "node1", Name: "Exit 1 renamed", PublicRole: model.RoleExit,
		PublicIPs: []string{"203.0.113.21"}, AgentAddr: "203.0.113.21:8443",
		DialIn: &model.DialIn{Proto: model.DialInProtoVLESSReality, Port: 443, UUID: "u1", TargetSNI: "www.cdn.example"},
	}
	if code, body := do(t, c, http.MethodPost, url+"/api/nodes", edit); code != http.StatusOK {
		t.Fatalf("edit exit node: %d %s", code, body)
	}

	st, err := p.store.LoadState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got, ok := st.NodeByID("node1")
	if !ok || got.DialIn == nil {
		t.Fatalf("node1 dial_in missing after edit")
	}
	if got.DialIn.PublicKey != "PUBKEY123" || got.DialIn.PrivKey != "PRIVKEY456" || got.DialIn.ShortID != "ab12" {
		t.Errorf("reality keys wiped by key-light edit: %+v", got.DialIn)
	}
	if got.Name != "Exit 1 renamed" {
		t.Errorf("rename should still apply, got %q", got.Name)
	}

	// /api/state must NOT leak the private key, but must still expose the
	// public key + short_id (so the form round-trips via preserveDialInKeys).
	code, body := do(t, c, http.MethodGet, url+"/api/state", nil)
	if code != http.StatusOK {
		t.Fatalf("state: %d", code)
	}
	var view model.State
	if err := json.Unmarshal(body, &view); err != nil {
		t.Fatal(err)
	}
	vn, ok := view.NodeByID("node1")
	if !ok || vn.DialIn == nil {
		t.Fatalf("node1 dial_in missing in state view")
	}
	if vn.DialIn.PrivKey != "" {
		t.Errorf("priv_key must be redacted in /api/state, got %q", vn.DialIn.PrivKey)
	}
	if vn.DialIn.PublicKey != "PUBKEY123" || vn.DialIn.ShortID != "ab12" {
		t.Errorf("non-secret reality fields should survive the read: %+v", vn.DialIn)
	}
}

// TestFriendlyStoreErrors checks that raw Postgres errors (constraint
// names, SQLSTATE) must not leak to API clients, and a duplicate-username
// collision returns a generic message so it can't be used as a cross-namespace
// enumeration oracle.
func TestFriendlyStoreErrors(t *testing.T) {
	p, _, _ := newPanel(t)
	c, url := newClient(t, p) // no admin -> open

	mk := func(path string, body any) (int, []byte) { return do(t, c, http.MethodPost, url+path, body) }
	mk("/api/nodes", model.Node{ID: "n1", Name: "n1", PublicRole: model.RoleExit, PublicIPs: []string{"1.2.3.4"}, AgentAddr: "1.2.3.4:8443",
		DialIn: &model.DialIn{Proto: model.DialInProtoVLESSReality, Port: 443, UUID: "u", TargetSNI: "x"}})
	mk("/api/groups", model.Group{ID: "g1", Name: "G1", DefaultExitID: "n1"})
	if code, _ := mk("/api/users", userUpsertRequest{ID: "u1", Username: "alice", Enabled: true, GroupID: "g1"}); code != http.StatusOK {
		t.Fatalf("seed user: %d", code)
	}

	// Duplicate username -> generic message, no SQLSTATE/constraint leak.
	code, body := mk("/api/users", userUpsertRequest{ID: "u2", Username: "alice", Enabled: true, GroupID: "g1"})
	if code != http.StatusBadRequest {
		t.Fatalf("duplicate username should be 400, got %d", code)
	}
	if s := string(body); strings.Contains(s, "SQLSTATE") || strings.Contains(s, "constraint") || strings.Contains(s, "23505") {
		t.Errorf("duplicate-username error leaked raw pg detail: %s", s)
	}
	if !strings.Contains(string(body), "already taken") {
		t.Errorf("duplicate-username error should be friendly, got %s", body)
	}

	// Deleting a group that still has clients -> friendly RESTRICT message.
	code, body = do(t, c, http.MethodDelete, url+"/api/groups/g1", nil)
	if code != http.StatusBadRequest {
		t.Fatalf("delete group-with-users should be 400, got %d", code)
	}
	if s := string(body); strings.Contains(s, "SQLSTATE") || strings.Contains(s, "fkey") || strings.Contains(s, "23503") {
		t.Errorf("group-delete error leaked raw pg detail: %s", s)
	}
	if !strings.Contains(string(body), "clients") {
		t.Errorf("group-delete error should mention reassigning clients, got %s", body)
	}
}

func TestEntityCRUDViaAPI(t *testing.T) {
	p, _, _ := newPanel(t)
	c, url := newClient(t, p) // no admin -> open

	exit := model.Node{
		ID: "node1", Name: "Exit 1", PublicRole: model.RoleExit,
		PublicIPs: []string{"203.0.113.21"}, AgentAddr: "203.0.113.21:8443",
		DialIn: &model.DialIn{Proto: model.DialInProtoVLESSReality, Port: 443, UUID: "u1", TargetSNI: "www.cdn.example"},
	}
	if code, body := do(t, c, http.MethodPost, url+"/api/nodes", exit); code != http.StatusOK {
		t.Fatalf("create exit node: %d %s", code, body)
	}
	if code, body := do(t, c, http.MethodPost, url+"/api/groups",
		model.Group{ID: "g1", Name: "Group 1", DefaultExitID: "node1"}); code != http.StatusOK {
		t.Fatalf("create group: %d %s", code, body)
	}
	// User via API: password omitted -> server generates a secret.
	if code, body := do(t, c, http.MethodPost, url+"/api/users",
		userUpsertRequest{ID: "u1", Username: "alice", Enabled: true, GroupID: "g1"}); code != http.StatusOK {
		t.Fatalf("create user: %d %s", code, body)
	}

	// State reflects the created entities.
	code, body := do(t, c, http.MethodGet, url+"/api/state", nil)
	if code != http.StatusOK {
		t.Fatalf("state: %d", code)
	}
	var st model.State
	if err := json.Unmarshal(body, &st); err != nil {
		t.Fatal(err)
	}
	if len(st.Nodes) != 1 || len(st.Groups) != 1 || len(st.Users) != 1 {
		t.Fatalf("unexpected state counts: %+v", st)
	}
	if st.Nodes[0].DialIn == nil || st.Nodes[0].DialIn.UUID != "u1" {
		t.Errorf("dial_in not persisted: %+v", st.Nodes[0].DialIn)
	}

	// Invalid entity rejected (entry node must not carry dial_in).
	bad := model.Node{ID: "bad", PublicRole: model.RoleEntry, DialIn: &model.DialIn{Proto: model.DialInProtoVLESSReality, Port: 443, UUID: "x", TargetSNI: "y"}}
	if code, _ := do(t, c, http.MethodPost, url+"/api/nodes", bad); code != http.StatusBadRequest {
		t.Errorf("invalid node should be 400, got %d", code)
	}

	// Delete the user.
	if code, _ := do(t, c, http.MethodDelete, url+"/api/users/u1", nil); code != http.StatusOK {
		t.Errorf("delete user: %d", code)
	}
}

func TestBulkUsers(t *testing.T) {
	p, _, _ := newPanel(t)
	c, url := newClient(t, p) // no admin -> open (synthetic admin owns infra)

	mk := func(id, grp string) {
		if code, body := do(t, c, http.MethodPost, url+"/api/groups", model.Group{ID: grp, Name: grp}); code != http.StatusOK && code != http.StatusBadRequest {
			t.Fatalf("group %s: %d %s", grp, code, body)
		}
		if code, body := do(t, c, http.MethodPost, url+"/api/users",
			userUpsertRequest{ID: id, Username: id, Enabled: true, GroupID: grp}); code != http.StatusOK {
			t.Fatalf("user %s: %d %s", id, code, body)
		}
	}
	mk("u1", "g1")
	mk("u2", "g1")
	mk("u3", "g1")
	do(t, c, http.MethodPost, url+"/api/groups", model.Group{ID: "g2", Name: "g2"})

	usersByID := func() map[string]model.User {
		_, body := do(t, c, http.MethodGet, url+"/api/state", nil)
		var st model.State
		if err := json.Unmarshal(body, &st); err != nil {
			t.Fatal(err)
		}
		m := map[string]model.User{}
		for _, u := range st.Users {
			m[u.ID] = u
		}
		return m
	}
	bulk := func(req bulkUsersRequest) (int, int) {
		code, body := do(t, c, http.MethodPost, url+"/api/users/bulk", req)
		if code != http.StatusOK {
			t.Fatalf("bulk %s: %d %s", req.Action, code, body)
		}
		var res struct{ Applied, Failed int }
		if err := json.Unmarshal(body, &res); err != nil {
			t.Fatal(err)
		}
		return res.Applied, res.Failed
	}

	// Disable two.
	if a, f := bulk(bulkUsersRequest{IDs: []string{"u1", "u2"}, Action: "disable"}); a != 2 || f != 0 {
		t.Fatalf("disable: applied=%d failed=%d", a, f)
	}
	m := usersByID()
	if m["u1"].Enabled || m["u2"].Enabled || !m["u3"].Enabled {
		t.Fatalf("disable wrong: %+v", m)
	}

	// Re-enable one, move two to g2.
	bulk(bulkUsersRequest{IDs: []string{"u1"}, Action: "enable"})
	bulk(bulkUsersRequest{IDs: []string{"u2", "u3"}, Action: "set_group", GroupID: "g2"})
	m = usersByID()
	if !m["u1"].Enabled || m["u2"].GroupID != "g2" || m["u3"].GroupID != "g2" {
		t.Fatalf("enable/set_group wrong: %+v", m)
	}

	// Set then clear expiry.
	exp := time.Date(2030, 1, 2, 23, 59, 59, 0, time.UTC)
	bulk(bulkUsersRequest{IDs: []string{"u1"}, Action: "set_expiry", ExpiresAt: &exp})
	if m = usersByID(); m["u1"].ExpiresAt == nil || !m["u1"].ExpiresAt.Equal(exp) {
		t.Fatalf("set_expiry wrong: %v", m["u1"].ExpiresAt)
	}
	bulk(bulkUsersRequest{IDs: []string{"u1"}, Action: "clear_expiry"})
	if m = usersByID(); m["u1"].ExpiresAt != nil {
		t.Fatalf("clear_expiry wrong: %v", m["u1"].ExpiresAt)
	}

	// Delete one.
	if a, _ := bulk(bulkUsersRequest{IDs: []string{"u3"}, Action: "delete"}); a != 1 {
		t.Fatalf("delete applied=%d", a)
	}
	if _, ok := usersByID()["u3"]; ok {
		t.Fatal("u3 should be deleted")
	}

	// Bad inputs.
	if code, _ := do(t, c, http.MethodPost, url+"/api/users/bulk", bulkUsersRequest{IDs: []string{"u1"}, Action: "nope"}); code != http.StatusBadRequest {
		t.Errorf("unknown action should be 400, got %d", code)
	}
	if code, _ := do(t, c, http.MethodPost, url+"/api/users/bulk", bulkUsersRequest{Action: "disable"}); code != http.StatusBadRequest {
		t.Errorf("empty ids should be 400, got %d", code)
	}
}

func TestBulkUsersScoping(t *testing.T) {
	p, st, _ := newPanel(t)
	ctx := context.Background()
	hash, _ := HashPassword("supersecret")
	if err := st.UpsertAdmin(ctx, "boss", hash); err != nil {
		t.Fatal(err)
	}
	// A co-owner admin (scoped to namespace "team") is the actor — namespace
	// isolation now applies to scoped admins, not just operators.
	coOwner(t, st, ctx, "lead", "leadpassw1", "team")
	// Infra-namespace group + user (another namespace, owner "").
	if err := st.UpsertGroup(ctx, model.Group{ID: "g-adm", Name: "admin grp"}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertUser(ctx, model.User{ID: "u-adm", Username: "adminclient", GroupID: "g-adm", Enabled: true}); err != nil {
		t.Fatal(err)
	}

	op, opURL := newClient(t, p)
	tok := login(t, op, opURL, "lead", "leadpassw1")
	// The co-owner's own group + user (land in namespace "team").
	if code, body := doTok(t, op, http.MethodPost, opURL+"/api/groups", tok, model.Group{ID: "g-op", Name: "op grp"}); code != http.StatusOK {
		t.Fatalf("co-owner group: %d %s", code, body)
	}
	if code, body := doTok(t, op, http.MethodPost, opURL+"/api/users", tok,
		userUpsertRequest{ID: "u-op", Username: "opclient", Enabled: true, GroupID: "g-op"}); code != http.StatusOK {
		t.Fatalf("co-owner user: %d %s", code, body)
	}

	// Bulk-disabling a mix: only the co-owner's own user is touched; the other
	// namespace's is reported failed, not silently modified.
	code, body := doTok(t, op, http.MethodPost, opURL+"/api/users/bulk", tok,
		bulkUsersRequest{IDs: []string{"u-op", "u-adm"}, Action: "disable"})
	if code != http.StatusOK {
		t.Fatalf("bulk: %d %s", code, body)
	}
	var res struct {
		Applied, Failed int
	}
	json.Unmarshal(body, &res)
	if res.Applied != 1 || res.Failed != 1 {
		t.Fatalf("scoped bulk should apply 1, fail 1; got applied=%d failed=%d", res.Applied, res.Failed)
	}
	// The other namespace's user must be untouched.
	adm, _, _ := st.User(ctx, "u-adm")
	if !adm.Enabled {
		t.Fatal("a scoped admin must not disable another namespace's client")
	}

	// Moving own users into another namespace's group is forbidden (403).
	if code, _ := doTok(t, op, http.MethodPost, opURL+"/api/users/bulk", tok,
		bulkUsersRequest{IDs: []string{"u-op"}, Action: "set_group", GroupID: "g-adm"}); code != http.StatusForbidden {
		t.Errorf("scoped set_group into another namespace should be 403, got %d", code)
	}
}

func TestNodeMaintenanceEndpoint(t *testing.T) {
	p, _, _ := newPanel(t)
	c, url := newClient(t, p) // no admin -> open

	exit := model.Node{
		ID: "node1", Name: "Exit 1", PublicRole: model.RoleExit,
		PublicIPs: []string{"203.0.113.21"}, AgentAddr: "203.0.113.21:8443",
		DialIn: &model.DialIn{Proto: model.DialInProtoVLESSReality, Port: 443, UUID: "u1", TargetSNI: "www.cdn.example"},
	}
	if code, body := do(t, c, http.MethodPost, url+"/api/nodes", exit); code != http.StatusOK {
		t.Fatalf("create exit node: %d %s", code, body)
	}

	maintOf := func() bool {
		_, body := do(t, c, http.MethodGet, url+"/api/state", nil)
		var st model.State
		if err := json.Unmarshal(body, &st); err != nil {
			t.Fatal(err)
		}
		n, _ := st.NodeByID("node1")
		return n.Maintenance
	}

	if maintOf() {
		t.Fatal("node1 should not start in maintenance")
	}
	if code, body := do(t, c, http.MethodPost, url+"/api/nodes/node1/maintenance", map[string]bool{"maintenance": true}); code != http.StatusOK {
		t.Fatalf("drain: %d %s", code, body)
	}
	if !maintOf() {
		t.Fatal("node1 should be draining after POST maintenance=true")
	}
	if code, _ := do(t, c, http.MethodPost, url+"/api/nodes/node1/maintenance", map[string]bool{"maintenance": false}); code != http.StatusOK {
		t.Fatalf("resume: %d", code)
	}
	if maintOf() {
		t.Fatal("node1 should be back in rotation after POST maintenance=false")
	}
}

func TestAccountManagementAPI(t *testing.T) {
	p, st, _ := newPanel(t)
	ctx := context.Background()
	hash, _ := HashPassword("supersecret")
	if err := st.UpsertAdmin(ctx, "boss", hash); err != nil {
		t.Fatal(err)
	}

	adm, url := newClient(t, p)
	tok := login(t, adm, url, "boss", "supersecret")

	// Bootstrap owner creates an operator via the API. Operators only ever reach
	// the bot, so a Telegram id is mandatory; the password here is optional but
	// supplied anyway to exercise that path too.
	if code, body := doTok(t, adm, http.MethodPost, url+"/api/admins", tok,
		map[string]any{"username": "op1", "password": "operatorpw1", "role": "operator", "telegram_id": 600100}); code != http.StatusOK {
		t.Fatalf("create operator: %d %s", code, body)
	}
	a, err := st.AdminByUsername(ctx, "op1")
	if err != nil || a.Role != model.RoleOperator {
		t.Fatalf("operator not created with role: %+v err=%v", a, err)
	}

	// An operator has no panel: even with valid credentials, login is refused.
	// Its only surface is the bot.
	op, opURL := newClient(t, p)
	if code, body := do(t, op, http.MethodPost, opURL+"/api/auth/login",
		map[string]string{"username": "op1", "password": "operatorpw1"}); code != http.StatusForbidden {
		t.Fatalf("operator panel login should be 403, got %d %s", code, body)
	}

	// A co-owner admin can log in and bind its own Telegram id (the bot then
	// resolves it); self-service is available to every panel account.
	coOwner(t, st, ctx, "lead", "leadpassw1", "team")
	lc, lURL := newClient(t, p)
	ltok := login(t, lc, lURL, "lead", "leadpassw1")
	if code, body := doTok(t, lc, http.MethodPost, lURL+"/api/account/telegram", ltok,
		map[string]any{"telegram_id": 555111, "alert_chat_id": "-100abc"}); code != http.StatusOK {
		t.Fatalf("co-owner bind telegram: %d %s", code, body)
	}
	bound, err := st.AccountByTelegramID(ctx, 555111)
	if err != nil || bound.Username != "lead" || bound.AlertChatID != "-100abc" {
		t.Fatalf("telegram binding not resolvable: %+v err=%v", bound, err)
	}

	// A co-owner admin cannot manage accounts (bootstrap-only).
	if code, _ := doTok(t, lc, http.MethodPost, lURL+"/api/admins", ltok,
		map[string]string{"username": "x", "password": "passwordxx", "role": "operator"}); code != http.StatusForbidden {
		t.Errorf("co-owner creating accounts should be 403, got %d", code)
	}
}

// TestMemberManagement covers account management as the bootstrap owner's
// exclusive surface (decisions 7/11): co-owner admins and operators cannot manage
// members; operators have no panel; the bootstrap owner mints/flips roles with the
// last-admin guards intact.
func TestMemberManagement(t *testing.T) {
	p, st, _ := newPanel(t)
	ctx := context.Background()
	hash, _ := HashPassword("supersecret")
	if err := st.UpsertAdmin(ctx, "boss", hash); err != nil { // bootstrap owner
		t.Fatal(err)
	}
	adm, url := newClient(t, p)
	tok := login(t, adm, url, "boss", "supersecret")

	// Bootstrap owner seeds a namespace "team" with a co-owner admin (lead) and a
	// plain operator (solo, its own namespace).
	if code, body := doTok(t, adm, http.MethodPost, url+"/api/admins", tok,
		map[string]string{"username": "lead", "password": "leadpassw1", "role": "admin", "namespace": "team"}); code != http.StatusOK {
		t.Fatalf("seed co-owner admin: %d %s", code, body)
	}
	lead, err := st.AdminByUsername(ctx, "lead")
	if err != nil || lead.Namespace() != "team" || lead.IsBootstrapOwner() {
		t.Fatalf("lead must be a co-owner admin, not bootstrap: %+v err=%v", lead, err)
	}
	if code, body := doTok(t, adm, http.MethodPost, url+"/api/admins", tok,
		map[string]any{"username": "solo", "password": "solopassw1", "role": "operator", "telegram_id": 600200}); code != http.StatusOK {
		t.Fatalf("seed operator: %d %s", code, body)
	}

	// A co-owner admin can log in but cannot manage members at all (bootstrap-only).
	lc, lURL := newClient(t, p)
	ltok := login(t, lc, lURL, "lead", "leadpassw1")
	if code, _ := doTok(t, lc, http.MethodGet, lURL+"/api/admins", ltok, nil); code != http.StatusForbidden {
		t.Errorf("co-owner listing accounts should be 403, got %d", code)
	}
	if code, _ := doTok(t, lc, http.MethodPost, lURL+"/api/admins", ltok,
		map[string]string{"username": "m1", "password": "memberpw12", "role": "operator"}); code != http.StatusForbidden {
		t.Errorf("co-owner creating a member should be 403, got %d", code)
	}
	if code, _ := doTok(t, lc, http.MethodPost, lURL+"/api/admins/solo/role", ltok,
		map[string]string{"role": "admin"}); code != http.StatusForbidden {
		t.Errorf("co-owner changing a role should be 403, got %d", code)
	}

	// An operator has no panel at all.
	sc, sURL := newClient(t, p)
	if code, _ := do(t, sc, http.MethodPost, sURL+"/api/auth/login",
		map[string]string{"username": "solo", "password": "solopassw1"}); code != http.StatusForbidden {
		t.Errorf("operator login should be 403, got %d", code)
	}

	// The bootstrap owner can flip a sole-member namespace freely (no one to
	// strand): promote solo, then demote it back.
	if code, body := doTok(t, adm, http.MethodPost, url+"/api/admins/solo/role", tok,
		map[string]string{"role": "admin"}); code != http.StatusOK {
		t.Fatalf("promote solo: %d %s", code, body)
	}
	// solo already had a Telegram id bound at creation, so demoting it back needs
	// no extra field.
	if code, body := doTok(t, adm, http.MethodPost, url+"/api/admins/solo/role", tok,
		map[string]string{"role": "operator"}); code != http.StatusOK {
		t.Fatalf("demote sole-member namespace admin should succeed: %d %s", code, body)
	}

	// Last-admin guard: seed a second admin into "team", demote it (lead remains),
	// then demoting the sole remaining admin "lead" while a member remains fails.
	if code, body := doTok(t, adm, http.MethodPost, url+"/api/admins", tok,
		map[string]string{"username": "m1", "password": "memberpw12", "role": "admin", "namespace": "team"}); code != http.StatusOK {
		t.Fatalf("seed second team admin: %d %s", code, body)
	}
	// m1 has no Telegram id yet, so demoting it to operator must supply one.
	if code, body := doTok(t, adm, http.MethodPost, url+"/api/admins/m1/role", tok,
		map[string]string{"role": "operator"}); code != http.StatusBadRequest {
		t.Errorf("demote without telegram id should be 400, got %d %s", code, body)
	}
	if code, _ := doTok(t, adm, http.MethodPost, url+"/api/admins/m1/role", tok,
		map[string]any{"role": "operator", "telegram_id": 600300}); code != http.StatusOK {
		t.Errorf("demote with another admin present should succeed, got %d", code)
	}
	if code, _ := doTok(t, adm, http.MethodPost, url+"/api/admins/lead/role", tok,
		map[string]string{"role": "operator"}); code != http.StatusBadRequest {
		t.Errorf("demoting the last admin of a namespace with members should be 400, got %d", code)
	}
}

// TestOperatorPasswordOptionalTelegramRequired covers the role-gated required
// fields (role == access surface): an operator's only surface is the bot, so a
// Telegram id is mandatory and a panel password is optional at creation; an
// admin needs the reverse. A role change must backfill whichever field the
// account never had, but not one it already carries from an earlier stint.
func TestOperatorPasswordOptionalTelegramRequired(t *testing.T) {
	p, st, _ := newPanel(t)
	ctx := context.Background()
	hash, _ := HashPassword("supersecret")
	if err := st.UpsertAdmin(ctx, "boss", hash); err != nil {
		t.Fatal(err)
	}
	adm, url := newClient(t, p)
	tok := login(t, adm, url, "boss", "supersecret")

	// Creating an operator without a Telegram id is rejected.
	if code, _ := doTok(t, adm, http.MethodPost, url+"/api/admins", tok,
		map[string]string{"username": "nop1", "role": "operator"}); code != http.StatusBadRequest {
		t.Errorf("operator without telegram id should be 400, got %d", code)
	}
	// Creating an operator with a Telegram id but no password succeeds, and the
	// account is left with no password hash at all.
	if code, body := doTok(t, adm, http.MethodPost, url+"/api/admins", tok,
		map[string]any{"username": "nop1", "role": "operator", "telegram_id": 700100}); code != http.StatusOK {
		t.Fatalf("passwordless operator create: %d %s", code, body)
	}
	nop1, err := st.AdminByUsername(ctx, "nop1")
	if err != nil || nop1.PasswordHash != "" {
		t.Fatalf("passwordless operator should have no password hash: %+v err=%v", nop1, err)
	}

	// Creating an admin without a password is rejected.
	if code, _ := doTok(t, adm, http.MethodPost, url+"/api/admins", tok,
		map[string]string{"username": "nad1", "role": "admin"}); code != http.StatusBadRequest {
		t.Errorf("admin without password should be 400, got %d", code)
	}

	// Promoting the passwordless operator to admin without a password is
	// rejected; supplying one succeeds and the account gains a hash.
	if code, _ := doTok(t, adm, http.MethodPost, url+"/api/admins/nop1/role", tok,
		map[string]string{"role": "admin"}); code != http.StatusBadRequest {
		t.Errorf("promote without password should be 400, got %d", code)
	}
	if code, body := doTok(t, adm, http.MethodPost, url+"/api/admins/nop1/role", tok,
		map[string]string{"role": "admin", "password": "brandnewpw1"}); code != http.StatusOK {
		t.Fatalf("promote with password: %d %s", code, body)
	}
	nop1, err = st.AdminByUsername(ctx, "nop1")
	if err != nil || nop1.PasswordHash == "" {
		t.Fatalf("promoted account should now have a password hash: %+v err=%v", nop1, err)
	}

	// Demoting it back to operator needs no Telegram id — it already has one
	// from creation.
	if code, body := doTok(t, adm, http.MethodPost, url+"/api/admins/nop1/role", tok,
		map[string]string{"role": "operator"}); code != http.StatusOK {
		t.Fatalf("demote with pre-existing telegram id: %d %s", code, body)
	}

	// An admin created via the admin-only path has no Telegram id; demoting it
	// to operator without one is rejected, and supplying one succeeds.
	if code, body := doTok(t, adm, http.MethodPost, url+"/api/admins", tok,
		map[string]string{"username": "nad2", "role": "admin", "password": "adminpassw1"}); code != http.StatusOK {
		t.Fatalf("create plain admin: %d %s", code, body)
	}
	if code, _ := doTok(t, adm, http.MethodPost, url+"/api/admins/nad2/role", tok,
		map[string]string{"role": "operator"}); code != http.StatusBadRequest {
		t.Errorf("demote without telegram id should be 400, got %d", code)
	}
	if code, body := doTok(t, adm, http.MethodPost, url+"/api/admins/nad2/role", tok,
		map[string]any{"role": "operator", "telegram_id": 700200}); code != http.StatusOK {
		t.Fatalf("demote with telegram id: %d %s", code, body)
	}
}

// coOwner mints a co-owner admin: a scoped admin (Role=admin) in its own
// namespace `ns`. Unlike the bootstrap owner it can log into the panel but is
// scoped to its namespace for clients/groups/traffic. It is the entity that
// replaced the panel-scoped operator — operators have no panel now.
func coOwner(t *testing.T, st *store.Store, ctx context.Context, user, pass, ns string) {
	t.Helper()
	hash, _ := HashPassword(pass)
	if err := st.CreateNamespace(ctx, ns, ns); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateAccountIn(ctx, user, hash, model.RoleAdmin, ns); err != nil {
		t.Fatal(err)
	}
}

func login(t *testing.T, c *http.Client, url, user, pass string) string {
	t.Helper()
	code, body := do(t, c, http.MethodPost, url+"/api/auth/login",
		map[string]string{"username": user, "password": pass})
	if code != http.StatusOK {
		t.Fatalf("login %s: %d %s", user, code, body)
	}
	var resp struct {
		CSRF string `json:"csrf_token"`
	}
	json.Unmarshal(body, &resp)
	return resp.CSRF
}

// doTok is like do but attaches the per-session CSRF token (required for
// state-changing requests once an account exists).
func doTok(t *testing.T, c *http.Client, method, url, tok string, body any) (int, []byte) {
	t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req, _ := http.NewRequest(method, url, rdr)
	req.Header.Set("X-CSRF-Token", tok)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	buf := new(bytes.Buffer)
	_, _ = buf.ReadFrom(resp.Body)
	return resp.StatusCode, buf.Bytes()
}

// TestCoOwnerScoping covers a co-owner admin (a scoped admin in its own
// namespace): it shares infra with the bootstrap owner but is isolated from other
// namespaces' clients/groups, and cannot manage accounts. Operators have no panel
// at all (asserted via login 403).
func TestCoOwnerScoping(t *testing.T) {
	p, st, _ := newPanel(t)
	ctx := context.Background()

	// Bootstrap owner + an infra-namespace group/user, and a co-owner admin "lead"
	// in namespace "team".
	hash, _ := HashPassword("supersecret")
	if err := st.UpsertAdmin(ctx, "boss", hash); err != nil {
		t.Fatal(err)
	}
	coOwner(t, st, ctx, "lead", "leadpassw1", "team")
	if err := st.UpsertGroup(ctx, model.Group{ID: "g-adm", Name: "admin grp"}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertUser(ctx, model.User{ID: "u-adm", Username: "adminclient", GroupID: "g-adm", Enabled: true}); err != nil {
		t.Fatal(err)
	}

	op, opURL := newClient(t, p)
	tok := login(t, op, opURL, "lead", "leadpassw1")

	// Account management is bootstrap-only — a co-owner gets 403.
	if code, _ := doTok(t, op, http.MethodGet, opURL+"/api/admins", tok, nil); code != http.StatusForbidden {
		t.Errorf("co-owner listing accounts should be 403, got %d", code)
	}
	// But shared infra (settings) IS reachable by any admin.
	if code, _ := doTok(t, op, http.MethodGet, opURL+"/api/settings", tok, nil); code != http.StatusOK {
		t.Errorf("co-owner should reach shared-infra settings, got %d", code)
	}

	// Session reports the admin role + namespace, and owns_infra (shared infra) but
	// not is_bootstrap.
	_, sbody := do(t, op, http.MethodGet, opURL+"/api/session", nil)
	if !strings.Contains(string(sbody), `"role":"admin"`) || !strings.Contains(string(sbody), `"namespace":"team"`) {
		t.Errorf("session should report admin role in team: %s", sbody)
	}
	if !strings.Contains(string(sbody), `"owns_infra":true`) || !strings.Contains(string(sbody), `"is_bootstrap":false`) {
		t.Errorf("co-owner session should manage infra but not be bootstrap: %s", sbody)
	}

	// Co-owner creates its own group + user (land in "team").
	if code, body := doTok(t, op, http.MethodPost, opURL+"/api/groups", tok,
		model.Group{ID: "g-op", Name: "op grp"}); code != http.StatusOK {
		t.Fatalf("co-owner create group: %d %s", code, body)
	}
	if code, body := doTok(t, op, http.MethodPost, opURL+"/api/users", tok,
		userUpsertRequest{ID: "u-op", Username: "opclient", Enabled: true, GroupID: "g-op"}); code != http.StatusOK {
		t.Fatalf("co-owner create user: %d %s", code, body)
	}

	// The event log is namespace-scoped: the co-owner reads its OWN journal, never
	// the infra namespace's events.
	{
		code, body := do(t, op, http.MethodGet, opURL+"/api/events", nil)
		if code != http.StatusOK {
			t.Fatalf("co-owner events: %d %s", code, body)
		}
		var ev struct {
			Events []model.Event `json:"events"`
		}
		if err := json.Unmarshal(body, &ev); err != nil {
			t.Fatal(err)
		}
		if len(ev.Events) == 0 {
			t.Errorf("co-owner should see its own events, got none")
		}
		for _, e := range ev.Events {
			if e.OwnerID != "team" {
				t.Errorf("co-owner event leaked from namespace %q: %+v", e.OwnerID, e)
			}
		}
	}

	// Scoped read: the co-owner sees only its own user; aggregate counts the other.
	code, body := do(t, op, http.MethodGet, opURL+"/api/state", nil)
	if code != http.StatusOK {
		t.Fatalf("co-owner state: %d", code)
	}
	var resp struct {
		Users     []model.User  `json:"users"`
		Groups    []model.Group `json:"groups"`
		Aggregate struct {
			OtherUsers int `json:"other_users"`
		} `json:"aggregate"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Users) != 1 || resp.Users[0].ID != "u-op" {
		t.Errorf("co-owner should see only own user, got %+v", resp.Users)
	}
	if resp.Aggregate.OtherUsers != 1 {
		t.Errorf("aggregate should count the other namespace's client, got %d", resp.Aggregate.OtherUsers)
	}

	// Cross-namespace client writes are refused.
	if code, _ := doTok(t, op, http.MethodDelete, opURL+"/api/users/u-adm", tok, nil); code != http.StatusForbidden {
		t.Errorf("co-owner deleting another namespace's user should be 403, got %d", code)
	}
	if code, _ := doTok(t, op, http.MethodPost, opURL+"/api/users", tok,
		userUpsertRequest{ID: "u-adm", Username: "adminclient", Enabled: false, GroupID: "g-adm"}); code != http.StatusForbidden {
		t.Errorf("co-owner editing another namespace's user should be 403, got %d", code)
	}
	// Attaching its user to another namespace's group is refused.
	if code, _ := doTok(t, op, http.MethodPost, opURL+"/api/users", tok,
		userUpsertRequest{ID: "u-op2", Username: "opclient2", Enabled: true, GroupID: "g-adm"}); code != http.StatusForbidden {
		t.Errorf("co-owner using another namespace's group should be 403, got %d", code)
	}

	// Infra-tier routing is SHARED: a co-owner admin may author guard- and
	// fleet-tier policies, and they are stamped infra-owned ("").
	if code, body := doTok(t, op, http.MethodPost, opURL+"/api/route-policies", tok,
		model.RoutePolicy{ID: "g1", Tier: model.TierGuard, Action: model.ActionDirect, MatchDomains: []string{"x.com"}}); code != http.StatusOK {
		t.Fatalf("co-owner creating guard policy should be 200, got %d %s", code, body)
	}
	if code, body := doTok(t, op, http.MethodPost, opURL+"/api/route-policies", tok,
		model.RoutePolicy{ID: "m1", Tier: model.TierFleet, Action: model.ActionDirect, MatchDomains: []string{"z.com"}}); code != http.StatusOK {
		t.Fatalf("co-owner creating fleet mandate should be 200, got %d %s", code, body)
	}
	finalSt, err := st.LoadState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := findPolicy(finalSt, "m1"); got.OwnerID != "" {
		t.Errorf("fleet mandate must be infra-owned, got owner_id %q", got.OwnerID)
	}

	// A co-owner must NOT be able to launder its own guard rule (owner "")
	// into an exit-tier rule targeting a group it doesn't own. The infra->exit flip
	// is refused (the original is infra-owned = another namespace to the co-owner),
	// and the stored rule stays a guard rule owned "".
	if code, _ := doTok(t, op, http.MethodPost, opURL+"/api/route-policies", tok,
		model.RoutePolicy{ID: "g1", Tier: model.TierExit, Action: model.ActionExit, ExitNodeID: "exit1",
			AppliesToGroupID: "g-adm", MatchDomains: []string{"x.com"}}); code != http.StatusForbidden {
		t.Errorf("co-owner infra->exit laundering should be 403, got %d", code)
	}
	afterSt, _ := st.LoadState(ctx)
	if got, ok := findPolicy(afterSt, "g1"); !ok || got.Tier != model.TierGuard || got.OwnerID != "" {
		t.Errorf("g1 must stay a guard rule owned infra, got tier=%q owner=%q", got.Tier, got.OwnerID)
	}

	// The bootstrap owner is scoped to its OWN namespace by default (the
	// cross-namespace lens is off on a fresh login), so it sees only its own
	// client and counts the co-owner's in the aggregate.
	adm, admURL := newClient(t, p)
	admTok := login(t, adm, admURL, "boss", "supersecret")
	_, abody := do(t, adm, http.MethodGet, admURL+"/api/state", nil)
	var ast struct {
		Users     []model.User `json:"users"`
		Aggregate struct {
			OtherUsers int `json:"other_users"`
		} `json:"aggregate"`
	}
	json.Unmarshal(abody, &ast)
	if len(ast.Users) != 1 || ast.Users[0].ID != "u-adm" {
		t.Errorf("bootstrap owner should be scoped to own namespace by default, got %+v", ast.Users)
	}
	if ast.Aggregate.OtherUsers != 1 {
		t.Errorf("bootstrap owner aggregate should count the co-owner's client, got %d", ast.Aggregate.OtherUsers)
	}

	// With the cross-namespace lens enabled it sees everyone's clients.
	if code, body := doTok(t, adm, http.MethodPost, admURL+"/api/dev/cross-namespace-view", admTok,
		map[string]bool{"enabled": true}); code != http.StatusOK {
		t.Fatalf("enable cross-namespace view: %d %s", code, body)
	}
	_, abody = do(t, adm, http.MethodGet, admURL+"/api/state", nil)
	ast.Users = nil
	json.Unmarshal(abody, &ast)
	if len(ast.Users) != 2 {
		t.Errorf("bootstrap owner with lens should see both users, got %d", len(ast.Users))
	}
}

// TestDomainsAreSharedInfra covers the redesign: nodes/domains are shared infra,
// so any admin (the bootstrap owner OR a co-owner) may manage any domain — there
// is no per-node domain ownership anymore. Operators have no domain surface at all
// (no panel).
func TestDomainsAreSharedInfra(t *testing.T) {
	p, st, _ := newPanel(t)
	ctx := context.Background()

	hash, _ := HashPassword("supersecret")
	if err := st.UpsertAdmin(ctx, "boss", hash); err != nil {
		t.Fatal(err)
	}
	coOwner(t, st, ctx, "lead", "leadpassw1", "team")
	mkNode := func(id string) model.Node {
		return model.Node{ID: id, Name: id, PublicRole: model.RoleExit,
			PublicIPs: []string{"1.2.3.4"}, AgentAddr: "1.2.3.4:8443",
			DialIn: &model.DialIn{Proto: model.DialInProtoVLESSReality, Port: 443, UUID: "u", TargetSNI: "x"}}
	}
	if err := st.UpsertNode(ctx, mkNode("n1")); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertDomain(ctx, model.Domain{ID: "d-boss", Hostname: "boss.example.com", Purpose: model.PurposeMainFallback, NodeID: "n1"}); err != nil {
		t.Fatal(err)
	}

	// A co-owner admin may create a domain on any node (shared infra) ...
	op, opURL := newClient(t, p)
	tok := login(t, op, opURL, "lead", "leadpassw1")
	if code, body := doTok(t, op, http.MethodPost, opURL+"/api/domains", tok,
		model.Domain{ID: "d-lead", Hostname: "lead.example.com", Purpose: model.PurposeMainFallback, NodeID: "n1"}); code != http.StatusOK {
		t.Fatalf("co-owner create domain: %d %s", code, body)
	}
	// ... and delete a domain another admin created.
	if code, body := doTok(t, op, http.MethodDelete, opURL+"/api/domains/d-boss", tok, nil); code != http.StatusOK {
		t.Fatalf("co-owner delete shared domain: %d %s", code, body)
	}
}

func TestReconcileViaAPI(t *testing.T) {
	p, st, layout := newPanel(t)
	ctx := context.Background()

	// Seed the worked example through the store.
	state := model.WorkedExample()
	for _, n := range state.Nodes {
		if err := st.UpsertNode(ctx, n); err != nil {
			t.Fatal(err)
		}
	}
	for _, g := range state.Groups {
		st.UpsertGroup(ctx, g)
	}
	for _, u := range state.Users {
		st.UpsertUser(ctx, u)
	}
	for _, pol := range state.RoutePolicies {
		st.UpsertRoutePolicy(ctx, pol)
	}

	// Start in-process agents and point the fleet at them.
	urls := map[string]string{}
	for _, n := range state.Nodes {
		urls[n.ID] = startAgent(t, n.ID, layout)
	}
	p.fleet.URLFor = func(n model.Node) string { return urls[n.ID] }

	c, url := newClient(t, p)
	code, body := do(t, c, http.MethodPost, url+"/api/reconcile", nil)
	if code != http.StatusOK {
		t.Fatalf("reconcile: %d %s", code, body)
	}
	var resp struct {
		Revision int64                          `json:"revision"`
		Nodes    map[string]reconcileNodeResult `json:"nodes"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatal(err)
	}
	for _, n := range state.Nodes {
		if resp.Nodes[n.ID].Outcome != string(agentapi.OutcomeApplied) {
			t.Errorf("node %s: want applied, got %+v", n.ID, resp.Nodes[n.ID])
		}
	}
}

// TestDeletedAccountLosesAccess checks that a deleted account's live session
// must lose access at once (protected() re-validates the
// account exists), and must NOT resolve to a synthetic fleet owner (the old
// fail-open). A fired co-owner could otherwise keep acting as admin until TTL.
func TestDeletedAccountLosesAccess(t *testing.T) {
	p, st, _ := newPanel(t)
	ctx := context.Background()
	hash, _ := HashPassword("supersecret")
	if err := st.UpsertAdmin(ctx, "boss", hash); err != nil {
		t.Fatal(err)
	}
	// A co-owner admin (can log into the panel) for the deletion check.
	coOwner(t, st, ctx, "lead", "leadpassw1", "team")

	// lead logs in and can read its scoped state.
	op, opURL := newClient(t, p)
	login(t, op, opURL, "lead", "leadpassw1")
	if code, _ := do(t, op, http.MethodGet, opURL+"/api/state", nil); code != http.StatusOK {
		t.Fatalf("lead should reach /api/state before deletion, got %d", code)
	}

	// The bootstrap owner deletes lead.
	adm, admURL := newClient(t, p)
	admTok := login(t, adm, admURL, "boss", "supersecret")
	if code, body := doTok(t, adm, http.MethodDelete, admURL+"/api/admins/lead", admTok, nil); code != http.StatusOK {
		t.Fatalf("delete lead: %d %s", code, body)
	}

	// lead's still-cookied session is now rejected (not escalated to bootstrap owner).
	if code, _ := do(t, op, http.MethodGet, opURL+"/api/state", nil); code != http.StatusUnauthorized {
		t.Errorf("deleted lead should be 401 on /api/state, got %d", code)
	}
	// And it certainly cannot reach an infra-only surface as a synthetic admin.
	if code, _ := do(t, op, http.MethodGet, opURL+"/api/settings", nil); code != http.StatusUnauthorized {
		t.Errorf("deleted lead hitting infra endpoint should be 401, got %d", code)
	}
}

// TestAccountResolverFailsClosed unit-tests account(): a live session naming an
// account that does not resolve must yield a powerless account (no infra), never
// a fleet owner.
func TestAccountResolverFailsClosed(t *testing.T) {
	p, _, _ := newPanel(t)
	tok, _, err := p.sessions.Create("ghost") // session for an account that does not exist
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/state", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: tok})
	acct := p.account(req)
	if acct.CanManageInfra() || acct.IsBootstrapOwner() {
		t.Errorf("unresolved-account session must NOT manage infra or be bootstrap, got %+v", acct)
	}
	if acct.Namespace() != unresolvedNamespace {
		t.Errorf("unresolved account should carry the sentinel namespace, got %q", acct.Namespace())
	}
}

// TestRevokeUser confirms a user's sessions are dropped (delete/demote path).
func TestRevokeUser(t *testing.T) {
	m := NewSessionManager(time.Hour)
	a1, _, _ := m.Create("alice")
	a2, _, _ := m.Create("alice")
	b1, _, _ := m.Create("bob")
	if got := m.RevokeUser("alice"); got != 2 {
		t.Errorf("RevokeUser(alice) = %d, want 2", got)
	}
	if _, ok := m.Validate(a1); ok {
		t.Error("alice session a1 should be revoked")
	}
	if _, ok := m.Validate(a2); ok {
		t.Error("alice session a2 should be revoked")
	}
	if _, ok := m.Validate(b1); !ok {
		t.Error("bob session must survive")
	}
}

// TestLoginThrottle checks that repeated failed logins lock the username out,
// a correct password is rejected while locked, and success clears it.
func TestLoginThrottle(t *testing.T) {
	l := newLoginThrottle()
	if d := l.retryAfter("bob"); d != 0 {
		t.Fatalf("fresh user should not be locked, got %s", d)
	}
	for i := 0; i < loginFreeAttempts; i++ {
		l.fail("bob")
	}
	if d := l.retryAfter("bob"); d != 0 {
		t.Errorf("within free allowance should not lock yet, got %s", d)
	}
	l.fail("bob") // one past the allowance
	if d := l.retryAfter("bob"); d <= 0 {
		t.Errorf("should be locked after exceeding free attempts, got %s", d)
	}
	l.success("bob")
	if d := l.retryAfter("bob"); d != 0 {
		t.Errorf("success must clear the lockout, got %s", d)
	}
}

// TestLoginLockoutHTTP checks the lockout end to end: after enough wrong
// passwords the endpoint returns 429 even for the correct password.
func TestLoginLockoutHTTP(t *testing.T) {
	p, st, _ := newPanel(t)
	hash, _ := HashPassword("supersecret")
	if err := st.UpsertAdmin(context.Background(), "boss", hash); err != nil {
		t.Fatal(err)
	}
	c, url := newClient(t, p)
	for i := 0; i < loginFreeAttempts+1; i++ {
		do(t, c, http.MethodPost, url+"/api/auth/login", map[string]string{"username": "boss", "password": "wrong"})
	}
	code, _ := do(t, c, http.MethodPost, url+"/api/auth/login", map[string]string{"username": "boss", "password": "supersecret"})
	if code != http.StatusTooManyRequests {
		t.Errorf("correct password while locked out should be 429, got %d", code)
	}
}

// TestClientPasswordMinLength checks that an explicit short password is
// rejected; blank (auto-generate / keep) is fine.
func TestClientPasswordMinLength(t *testing.T) {
	p, st, _ := newPanel(t)
	hash, _ := HashPassword("supersecret")
	if err := st.UpsertAdmin(context.Background(), "boss", hash); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertGroup(context.Background(), model.Group{ID: "g1", Name: "g1"}); err != nil {
		t.Fatal(err)
	}
	adm, admURL := newClient(t, p)
	tok := login(t, adm, admURL, "boss", "supersecret")

	if code, _ := doTok(t, adm, http.MethodPost, admURL+"/api/users", tok,
		userUpsertRequest{ID: "u1", Username: "c1", Enabled: true, GroupID: "g1", Password: "1234"}); code != http.StatusBadRequest {
		t.Errorf("short explicit password should be 400, got %d", code)
	}
	if code, body := doTok(t, adm, http.MethodPost, admURL+"/api/users", tok,
		userUpsertRequest{ID: "u2", Username: "c2", Enabled: true, GroupID: "g1"}); code != http.StatusOK {
		t.Errorf("blank password (auto-generate) should be 200, got %d %s", code, body)
	}
}

// TestAutoSyncCoalesces covers the ux-review item #6 auto-sync: a write marks the
// panel dirty and RunAutoSyncLoop reconciles once after the debounce window,
// collapsing a burst of edits into a single push.
func TestAutoSyncCoalesces(t *testing.T) {
	p, _, _ := newPanel(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go p.RunAutoSyncLoop(ctx, 40*time.Millisecond)

	before := atomic.LoadInt64(&p.reconcileCount)
	// A burst of dirty signals within one window must collapse to one reconcile.
	for i := 0; i < 5; i++ {
		p.markDirty()
	}

	// Wait for the coalesced reconcile to land (poll up to ~2s).
	deadline := time.Now().Add(2 * time.Second)
	for atomic.LoadInt64(&p.reconcileCount) == before {
		if time.Now().After(deadline) {
			t.Fatalf("auto-sync did not reconcile after markDirty (count stuck at %d)", before)
		}
		time.Sleep(5 * time.Millisecond)
	}
	got := atomic.LoadInt64(&p.reconcileCount) - before
	if got != 1 {
		t.Errorf("burst of 5 edits should coalesce to 1 reconcile, got %d", got)
	}

	// A later edit triggers a fresh reconcile (the loop keeps serving).
	mid := atomic.LoadInt64(&p.reconcileCount)
	p.markDirty()
	deadline = time.Now().Add(2 * time.Second)
	for atomic.LoadInt64(&p.reconcileCount) == mid {
		if time.Now().After(deadline) {
			t.Fatalf("auto-sync did not reconcile on the second edit")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestClientConfigExportViaAPI(t *testing.T) {
	p, st, _ := newPanel(t)
	ctx := context.Background()
	seedState := model.WorkedExample()
	for _, n := range seedState.Nodes {
		if err := st.UpsertNode(ctx, n); err != nil {
			t.Fatal(err)
		}
	}
	for _, g := range seedState.Groups {
		st.UpsertGroup(ctx, g)
	}
	for _, u := range seedState.Users {
		st.UpsertUser(ctx, u)
	}
	for _, pol := range seedState.RoutePolicies {
		st.UpsertRoutePolicy(ctx, pol)
	}
	for _, d := range seedState.Domains {
		if err := st.UpsertDomain(ctx, d); err != nil {
			t.Fatal(err)
		}
	}

	c, url := newClient(t, p) // no admin -> open
	code, body := do(t, c, http.MethodGet, url+"/api/users/u-alice/client-config?entry=entryA", nil)
	if code != http.StatusOK {
		t.Fatalf("client-config: %d %s", code, body)
	}
	var cfg struct {
		Username string `json:"username"`
		Hostname string `json:"hostname"`
		TOML     string `json:"config_toml"`
	}
	if err := json.Unmarshal(body, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Username != "alice" || cfg.Hostname != "cdn.example.com" {
		t.Fatalf("unexpected client config: %+v", cfg)
	}
	if !strings.Contains(cfg.TOML, `password = "s-alice"`) {
		t.Errorf("client TOML missing password:\n%s", cfg.TOML)
	}

	// Missing entry param -> 400.
	if code, _ := do(t, c, http.MethodGet, url+"/api/users/u-alice/client-config", nil); code != http.StatusBadRequest {
		t.Errorf("missing entry should be 400, got %d", code)
	}
}

func TestReconcileLoopConverges(t *testing.T) {
	p, st, layout := newPanel(t)
	ctx := context.Background()
	seedState := model.WorkedExample()
	for _, n := range seedState.Nodes {
		if err := st.UpsertNode(ctx, n); err != nil {
			t.Fatal(err)
		}
	}
	for _, g := range seedState.Groups {
		st.UpsertGroup(ctx, g)
	}
	for _, u := range seedState.Users {
		st.UpsertUser(ctx, u)
	}
	for _, pol := range seedState.RoutePolicies {
		st.UpsertRoutePolicy(ctx, pol)
	}
	urls := map[string]string{}
	for _, n := range seedState.Nodes {
		urls[n.ID] = startAgent(t, n.ID, layout)
	}
	p.fleet.URLFor = func(n model.Node) string { return urls[n.ID] }

	lctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go p.RunReconcileLoop(lctx, 20*time.Millisecond)

	deadline := time.Now().Add(3 * time.Second)
	for atomic.LoadInt64(&p.reconcileCount) < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if got := atomic.LoadInt64(&p.reconcileCount); got < 2 {
		t.Fatalf("background loop should reconcile repeatedly, got %d", got)
	}
}

// provFake is a fake provision.Runner: it answers gen-csr with a real CSR and
// the lock-out-guard check with OK.
type provFake struct{}

func (provFake) Run(_ context.Context, cmd string) (string, error) {
	if strings.Contains(cmd, "ca gen-csr") {
		id := "n"
		f := strings.Fields(cmd)
		for i, w := range f {
			if w == "--node-id" && i+1 < len(f) {
				id = f[i+1]
			}
		}
		csr, _, err := pki.GenerateCSR(id)
		return string(csr), err
	}
	if strings.Contains(cmd, "echo OK") {
		return "OK", nil
	}
	return "", nil
}
func (provFake) Put(context.Context, []byte, string, os.FileMode) error { return nil }

func TestProvisionEndpoint(t *testing.T) {
	p, st, _ := newPanel(t)
	caCert, caKey, _ := pki.GenerateCA("test", time.Hour)
	ca, _ := pki.LoadCA(caCert, caKey)
	p.EnableProvisioning(&ProvisionConfig{
		CA: ca, CertValidity: time.Hour,
		TrustPanelBin: []byte("ELF"), SingBoxBin: []byte("ELF"),
		Units: func(model.PublicRole) []provision.UnitFile {
			return []provision.UnitFile{{Name: "trustpanel-agent.service", Content: []byte("[Service]"), Enable: true}}
		},
		NewRunner: func(context.Context, provision.SSHParams) (provision.Runner, func(), error) {
			return provFake{}, func() {}, nil
		},
	})

	c, url := newClient(t, p)
	code, body := do(t, c, http.MethodPost, url+"/api/nodes/provision", map[string]any{
		"name": "Exit NL", "role": "exit", "public_ips": []string{"203.0.113.21"}, "reality_sni": "www.example-cdn.com",
		"ssh":       map[string]any{"host": "203.0.113.21", "user": "root", "password": "x"},
		"hardening": map[string]any{"enabled": true, "sudo_user": "ops", "ssh_pubkey": "ssh-ed25519 AAA", "ssh_port": 3222, "disable_password_auth": true},
	})
	if code != http.StatusOK {
		t.Fatalf("provision: %d %s", code, body)
	}
	var start struct {
		JobID  string `json:"job_id"`
		NodeID string `json:"node_id"`
	}
	_ = json.Unmarshal(body, &start)
	if start.JobID == "" || start.NodeID == "" {
		t.Fatalf("missing job/node id: %s", body)
	}

	// Poll the job to completion.
	var status string
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		_, jb := do(t, c, http.MethodGet, url+"/api/jobs/"+start.JobID, nil)
		var j struct{ Status, Error string }
		_ = json.Unmarshal(jb, &j)
		status = j.Status
		if status != "running" {
			if status != "succeeded" {
				t.Fatalf("job failed: %s", j.Error)
			}
			break
		}
		time.Sleep(15 * time.Millisecond)
	}
	if status != "succeeded" {
		t.Fatalf("job did not succeed, last status %q", status)
	}

	// The node now exists with auto-generated Reality dial_in.
	got, err := st.LoadState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	n, ok := got.NodeByID(start.NodeID)
	if !ok {
		t.Fatalf("node %s not created", start.NodeID)
	}
	if n.DialIn == nil || n.DialIn.TargetSNI != "www.example-cdn.com" || n.DialIn.PublicKey == "" || n.DialIn.PrivKey == "" || n.DialIn.UUID == "" {
		t.Fatalf("reality dial_in not generated: %+v", n.DialIn)
	}
}

// TestProvisionExitBackfillsDefaultGroupExit covers the "silent direct egress"
// bug: a group with no default_exit_id yet must be pointed at a newly
// provisioned exit, exactly like bootstrap's first exit — otherwise a fresh
// entry routes direct instead of through the Reality tunnel until an operator
// manually sets it (see the README's Multi-node note).
func TestProvisionExitBackfillsDefaultGroupExit(t *testing.T) {
	p, st, _ := newPanel(t)
	ctx := context.Background()
	// An existing exit, standing in for one an operator already pointed a
	// group at (default_exit_id has an FK to nodes, so it must be real).
	existingExit := model.Node{ID: "exit-existing", Name: "Existing Exit", PublicRole: model.RoleExit,
		PublicIPs: []string{"203.0.113.99"}, AgentAddr: "203.0.113.99:8443",
		DialIn: &model.DialIn{Proto: model.DialInProtoVLESSReality, Port: 443, UUID: "u1",
			TargetSNI: "www.example-cdn.com", PublicKey: "pub1", PrivKey: "priv1", ShortID: "0123abcd"}}
	if err := st.UpsertNode(ctx, existingExit); err != nil {
		t.Fatal(err)
	}
	// A group with no default exit yet, as a fresh deployment would have
	// before its first exit is provisioned.
	if err := st.UpsertGroup(ctx, model.Group{ID: "default", Name: "default"}); err != nil {
		t.Fatal(err)
	}
	// A second group that already has an operator-chosen exit — the backfill
	// must not clobber it.
	if err := st.UpsertGroup(ctx, model.Group{ID: "pinned", Name: "pinned", DefaultExitID: existingExit.ID}); err != nil {
		t.Fatal(err)
	}

	caCert, caKey, _ := pki.GenerateCA("test", time.Hour)
	ca, _ := pki.LoadCA(caCert, caKey)
	p.EnableProvisioning(&ProvisionConfig{
		CA: ca, CertValidity: time.Hour,
		TrustPanelBin: []byte("ELF"), SingBoxBin: []byte("ELF"),
		Units: func(model.PublicRole) []provision.UnitFile {
			return []provision.UnitFile{{Name: "trustpanel-agent.service", Content: []byte("[Service]"), Enable: true}}
		},
		NewRunner: func(context.Context, provision.SSHParams) (provision.Runner, func(), error) {
			return provFake{}, func() {}, nil
		},
	})

	c, url := newClient(t, p)
	code, body := do(t, c, http.MethodPost, url+"/api/nodes/provision", map[string]any{
		"name": "Exit NL", "role": "exit", "public_ips": []string{"203.0.113.21"}, "reality_sni": "www.example-cdn.com",
		"ssh": map[string]any{"host": "203.0.113.21", "user": "root", "password": "x"},
	})
	if code != http.StatusOK {
		t.Fatalf("provision: %d %s", code, body)
	}
	var start struct {
		JobID  string `json:"job_id"`
		NodeID string `json:"node_id"`
	}
	_ = json.Unmarshal(body, &start)

	deadline := time.Now().Add(3 * time.Second)
	var jobStatus string
	for time.Now().Before(deadline) {
		_, jb := do(t, c, http.MethodGet, url+"/api/jobs/"+start.JobID, nil)
		var j struct{ Status string }
		_ = json.Unmarshal(jb, &j)
		jobStatus = j.Status
		if jobStatus != "" && jobStatus != "running" {
			break
		}
		time.Sleep(15 * time.Millisecond)
	}
	if jobStatus != "succeeded" {
		t.Fatalf("provision job did not succeed, last status %q", jobStatus)
	}

	got, err := st.LoadState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	groupsByID := map[string]model.Group{}
	for _, g := range got.Groups {
		groupsByID[g.ID] = g
	}
	if def, ok := groupsByID["default"]; !ok || def.DefaultExitID != start.NodeID {
		t.Fatalf("default group's default exit = %+v, want backfilled to %q", def, start.NodeID)
	}
	if pinned, ok := groupsByID["pinned"]; !ok || pinned.DefaultExitID != existingExit.ID {
		t.Fatalf("pinned group's default exit was clobbered: %+v", pinned)
	}
}

// TestProvisionMakeStandbyValidation covers the synchronous guards on the
// install-as-standby flag: it must target an exit, and a control-plane primary
// must exist to replicate from. (The happy path spawns an async add-standby job,
// exercised by the add-standby agent tests.)
func TestProvisionMakeStandbyValidation(t *testing.T) {
	p, _, _ := newPanel(t)
	caCert, caKey, _ := pki.GenerateCA("test", time.Hour)
	ca, _ := pki.LoadCA(caCert, caKey)
	p.EnableProvisioning(&ProvisionConfig{
		CA: ca, CertValidity: time.Hour, TrustPanelBin: []byte("ELF"), SingBoxBin: []byte("ELF"),
		Units: func(model.PublicRole) []provision.UnitFile {
			return []provision.UnitFile{{Name: "trustpanel-agent.service", Content: []byte("[Service]"), Enable: true}}
		},
		NewRunner: func(context.Context, provision.SSHParams) (provision.Runner, func(), error) {
			return provFake{}, func() {}, nil
		},
	})
	c, url := newClient(t, p)

	// make_standby on an entry → 400.
	code, body := do(t, c, http.MethodPost, url+"/api/nodes/provision", map[string]any{
		"name": "Entry X", "role": "entry", "public_ips": []string{"203.0.113.5"}, "make_standby": true,
		"ssh": map[string]any{"host": "203.0.113.5", "user": "root", "password": "x"},
	})
	if code != http.StatusBadRequest || !strings.Contains(string(body), "requires an exit") {
		t.Fatalf("entry+make_standby want 400 exit-required, got %d %s", code, body)
	}

	// make_standby exit but no control-plane primary registered → 400.
	code, body = do(t, c, http.MethodPost, url+"/api/nodes/provision", map[string]any{
		"name": "Exit Y", "role": "exit", "public_ips": []string{"203.0.113.6"}, "reality_sni": "www.cdn.example",
		"make_standby": true,
		"ssh":          map[string]any{"host": "203.0.113.6", "user": "root", "password": "x"},
	})
	if code != http.StatusBadRequest || !strings.Contains(string(body), "primary") {
		t.Fatalf("exit+make_standby with no primary want 400 primary, got %d %s", code, body)
	}
}

// TestOperatorProvisioning proves the operator node lifecycle: an operator can
// provision a node into its own namespace (but not as a standby), then drain its
// own node — while remaining locked out of another namespace's node.
// TestProvisioningIsInfra: provisioning is shared infra. A co-owner admin may
// provision (node lands infra-owned, "") and manage any node; an operator has no
// panel and cannot provision at all.
func TestProvisioningIsInfra(t *testing.T) {
	p, st, _ := newPanel(t)
	ctx := context.Background()
	caCert, caKey, _ := pki.GenerateCA("test", time.Hour)
	ca, _ := pki.LoadCA(caCert, caKey)
	p.EnableProvisioning(&ProvisionConfig{
		CA: ca, CertValidity: time.Hour, TrustPanelBin: []byte("ELF"), SingBoxBin: []byte("ELF"),
		Units: func(model.PublicRole) []provision.UnitFile {
			return []provision.UnitFile{{Name: "trustpanel-agent.service", Content: []byte("[Service]"), Enable: true}}
		},
		NewRunner: func(context.Context, provision.SSHParams) (provision.Runner, func(), error) {
			return provFake{}, func() {}, nil
		},
	})
	hash, _ := HashPassword("supersecret")
	if err := st.UpsertAdmin(ctx, "boss", hash); err != nil {
		t.Fatal(err)
	}
	coOwner(t, st, ctx, "lead", "leadpassw1", "team")
	if err := st.CreateAccount(ctx, "op1", hash, model.RoleOperator); err != nil {
		t.Fatal(err)
	}
	// A pre-existing node any admin may manage (shared infra).
	if err := st.UpsertNode(ctx, model.Node{ID: "adm-node", Name: "admin exit", PublicRole: model.RoleExit,
		PublicIPs: []string{"1.2.3.4"}, AgentAddr: "1.2.3.4:8443",
		DialIn: &model.DialIn{Proto: model.DialInProtoVLESSReality, Port: 443, UUID: "u", TargetSNI: "x"}}); err != nil {
		t.Fatal(err)
	}

	// An operator has no panel — cannot even log in.
	noc, noURL := newClient(t, p)
	if code, _ := do(t, noc, http.MethodPost, noURL+"/api/auth/login",
		map[string]string{"username": "op1", "password": "supersecret"}); code != http.StatusForbidden {
		t.Fatalf("operator login should be 403, got %d", code)
	}

	// A co-owner admin provisions a node (shared infra).
	op, opURL := newClient(t, p)
	tok := login(t, op, opURL, "lead", "leadpassw1")
	code, body := doTok(t, op, http.MethodPost, opURL+"/api/nodes/provision", tok, map[string]any{
		"name": "New Exit", "role": "exit", "public_ips": []string{"203.0.113.31"}, "reality_sni": "www.cdn.example",
		"ssh": map[string]any{"host": "203.0.113.31", "user": "root", "password": "x"},
	})
	if code != http.StatusOK {
		t.Fatalf("co-owner provision: %d %s", code, body)
	}
	var start struct {
		JobID  string `json:"job_id"`
		NodeID string `json:"node_id"`
	}
	_ = json.Unmarshal(body, &start)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		_, jb := doTok(t, op, http.MethodGet, opURL+"/api/jobs/"+start.JobID, tok, nil)
		var j struct{ Status, Error string }
		_ = json.Unmarshal(jb, &j)
		if j.Status != "running" {
			if j.Status != "succeeded" {
				t.Fatalf("co-owner provision job failed: %s", j.Error)
			}
			break
		}
		time.Sleep(15 * time.Millisecond)
	}
	got, _ := st.LoadState(ctx)
	n, ok := got.NodeByID(start.NodeID)
	if !ok || n.OwnerID != "" {
		t.Fatalf("provisioned node owner = %q, want \"\" infra (node found=%v)", n.OwnerID, ok)
	}

	// A co-owner admin may drain any node (shared infra), including the pre-existing one.
	if code, body := doTok(t, op, http.MethodPost, opURL+"/api/nodes/adm-node/maintenance", tok,
		map[string]any{"maintenance": true}); code != http.StatusOK {
		t.Fatalf("co-owner drain shared node: %d %s", code, body)
	}
}

// cannedTrafficStatus is an in-process agent StatusSource that also implements
// agent.UserTrafficSource, returning fixed per-user counters.
type cannedTrafficStatus struct {
	traffic []agentapi.UserTrafficStat
}

func (cannedTrafficStatus) Services(context.Context) []agentapi.ServiceStatus { return nil }
func (cannedTrafficStatus) InstalledVersions() map[string]string              { return nil }
func (c cannedTrafficStatus) UserTraffic(context.Context) []agentapi.UserTrafficStat {
	return c.traffic
}

// cannedServiceStatus is an in-process agent StatusSource that reports fixed
// per-unit systemd state, used to simulate a live agent process sitting on
// top of a dead data-plane service.
type cannedServiceStatus struct {
	services []agentapi.ServiceStatus
}

func (c cannedServiceStatus) Services(context.Context) []agentapi.ServiceStatus { return c.services }
func (cannedServiceStatus) InstalledVersions() map[string]string                { return nil }

func TestPollStatsDegradesOnDeadDataPlaneService(t *testing.T) {
	p, st, layout := newPanel(t)
	ctx := context.Background()
	state := model.WorkedExample()
	for _, n := range state.Nodes {
		if err := st.UpsertNode(ctx, n); err != nil {
			t.Fatal(err)
		}
	}
	for _, g := range state.Groups {
		st.UpsertGroup(ctx, g)
	}
	for _, u := range state.Users {
		st.UpsertUser(ctx, u)
	}

	// entryA's agent answers fine, but reports its sing-box unit as inactive —
	// exactly the "agent alive, data plane dead" scenario the health dot must
	// not paper over with green.
	urls := map[string]string{}
	for _, n := range state.Nodes {
		if n.ID == "entryA" {
			urls[n.ID] = startAgentWithStatus(t, n.ID, layout, cannedServiceStatus{services: []agentapi.ServiceStatus{
				{Name: controller.ServiceSingBox, State: "inactive"},
				{Name: controller.ServiceTrustTunnel, State: "active"},
			}})
		} else {
			urls[n.ID] = startAgent(t, n.ID, layout)
		}
	}
	p.fleet.URLFor = func(n model.Node) string { return urls[n.ID] }

	if err := p.PollStatsOnce(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := st.LoadState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	n, ok := got.NodeByID("entryA")
	if !ok {
		t.Fatal("entryA not found")
	}
	if n.Health != model.HealthDegraded {
		t.Fatalf("entryA health = %q, want degraded (sing-box reported inactive)", n.Health)
	}
}

func startAgentWithStatus(t *testing.T, nodeID string, layout controller.Layout, src agent.StatusSource) string {
	t.Helper()
	ag, err := agent.OpenStore(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	r := agent.NewReconciler(agent.Config{
		NodeID: nodeID, Roots: layout.Roots(),
		ServiceAllowlist: []string{controller.ServiceTrustTunnel, controller.ServiceSingBox},
	}, ag, fakeSvc{}, okChecker{})
	ts := httptest.NewServer(agent.NewServer(nodeID, r, ag, src).Handler())
	t.Cleanup(ts.Close)
	return ts.URL
}

func TestStatsLoopAccumulates(t *testing.T) {
	p, st, layout := newPanel(t)
	ctx := context.Background()
	state := model.WorkedExample()
	for _, n := range state.Nodes {
		if err := st.UpsertNode(ctx, n); err != nil {
			t.Fatal(err)
		}
	}
	for _, g := range state.Groups {
		st.UpsertGroup(ctx, g)
	}
	for _, u := range state.Users {
		st.UpsertUser(ctx, u)
	}

	// Only the entry node (entryA) reports traffic; exits get a plain agent.
	urls := map[string]string{}
	for _, n := range state.Nodes {
		if n.IsEntry() {
			urls[n.ID] = startAgentWithStatus(t, n.ID, layout, cannedTrafficStatus{traffic: []agentapi.UserTrafficStat{
				// Well above the online floor (~200 KB over the 2-min window) so the
				// active-now overlay lights up; deltas below still accumulate exactly.
				{Username: "alice", UplinkBytes: 100_000, DownlinkBytes: 400_000},
				{Username: "ghost", UplinkBytes: 5, DownlinkBytes: 5}, // unknown user -> skipped
			}})
		} else {
			urls[n.ID] = startAgent(t, n.ID, layout)
		}
	}
	p.fleet.URLFor = func(n model.Node) string { return urls[n.ID] }

	// First poll records the absolute reading; the unknown "ghost" user is
	// skipped (no matching user_id).
	if err := p.PollStatsOnce(ctx); err != nil {
		t.Fatal(err)
	}
	totals, err := st.UserTrafficTotals(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if alice := totalFor(totals, "u-alice"); alice == nil || alice.RxBytes != 100_000 || alice.TxBytes != 400_000 {
		t.Fatalf("after first poll: %+v", alice)
	}

	// A second poll with a higher reading adds only the delta.
	urls2 := startAgentWithStatus(t, "entryA", layout, cannedTrafficStatus{traffic: []agentapi.UserTrafficStat{
		{Username: "alice", UplinkBytes: 250_000, DownlinkBytes: 1_000_000},
	}})
	p.fleet.URLFor = func(n model.Node) string {
		if n.IsEntry() {
			return urls2
		}
		return urls[n.ID]
	}
	if err := p.PollStatsOnce(ctx); err != nil {
		t.Fatal(err)
	}

	// The GET /api/traffic endpoint reflects the accumulated totals.
	c, url := newClient(t, p)
	code, body := do(t, c, http.MethodGet, url+"/api/traffic", nil)
	if code != http.StatusOK {
		t.Fatalf("GET /api/traffic: %d %s", code, body)
	}
	var resp struct {
		Users []struct {
			store.UserTrafficTotal
			Active   bool  `json:"active"`
			RecentRx int64 `json:"recent_rx_bytes"`
			RecentTx int64 `json:"recent_tx_bytes"`
		} `json:"users"`
		ActiveCount int `json:"active_count"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatal(err)
	}
	var alice *store.UserTrafficTotal
	var aliceActive bool
	var aliceRecentRx, aliceRecentTx int64
	for i := range resp.Users {
		if resp.Users[i].UserID == "u-alice" {
			alice = &resp.Users[i].UserTrafficTotal
			aliceActive = resp.Users[i].Active
			aliceRecentRx, aliceRecentTx = resp.Users[i].RecentRx, resp.Users[i].RecentTx
		}
	}
	if alice == nil || alice.RxBytes != 250_000 || alice.TxBytes != 1_000_000 || alice.TotalBytes != 1_250_000 {
		t.Fatalf("after second poll via /api/traffic: %+v", alice)
	}
	if len(resp.Users) != 3 { // every user listed, even zero-traffic ones
		t.Fatalf("want 3 users, got %d", len(resp.Users))
	}
	// Active-now overlay: alice moved bytes both polls (past the online floor), so
	// she is active and the recent throughput is the sum of both ticks' deltas
	// (100k+150k rx, 400k+600k tx).
	if !aliceActive || aliceRecentRx != 250_000 || aliceRecentTx != 1_000_000 {
		t.Fatalf("alice activity: active=%v recent rx/tx=%d/%d, want true 250000/1000000", aliceActive, aliceRecentRx, aliceRecentTx)
	}
	if resp.ActiveCount != 1 {
		t.Fatalf("active_count = %d, want 1", resp.ActiveCount)
	}
}

// defaultExitOf reloads state and returns the group's current default exit id.
func defaultExitOf(t *testing.T, st *store.Store, groupID string) string {
	t.Helper()
	state, err := st.LoadState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range state.Groups {
		if g.ID == groupID {
			return g.DefaultExitID
		}
	}
	t.Fatalf("group %s not found", groupID)
	return ""
}

func nodeDrained(t *testing.T, st *store.Store, nodeID string) bool {
	t.Helper()
	state, err := st.LoadState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	n, ok := state.NodeByID(nodeID)
	if !ok {
		t.Fatalf("node %s not found", nodeID)
	}
	return n.Maintenance
}

// TestEgressFailoverAutoReassignsAndResets drives reviewEgressFailover through a
// full outage: a sustained exit outage past the debounce auto-moves its groups to
// a healthy exit and drains it, the move is idempotent while the outage persists,
// and recovery clears the streak WITHOUT reverting the groups (F-026/#7).
func TestEgressFailoverAutoReassignsAndResets(t *testing.T) {
	p, st, _ := newPanel(t)
	ctx := context.Background()
	seed := model.WorkedExample()
	for _, n := range seed.Nodes {
		if err := st.UpsertNode(ctx, n); err != nil {
			t.Fatal(err)
		}
	}
	for _, g := range seed.Groups { // admins + everyone both default to node2
		if err := st.UpsertGroup(ctx, g); err != nil {
			t.Fatal(err)
		}
	}
	// Short debounce so the test doesn't need real time; a controllable clock.
	if err := st.SaveSettings(ctx, model.Settings{Panel: model.PanelSettings{EgressFailoverSeconds: 60}}); err != nil {
		t.Fatal(err)
	}
	clock := time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC)
	p.now = func() time.Time { return clock }

	// node2 is the live exit for both groups; node2 goes degraded, node1 stays healthy.
	degraded := map[string]model.NodeHealth{"entryA": model.HealthHealthy, "node1": model.HealthHealthy, "node2": model.HealthDegraded}

	// First observation only starts the streak — nothing moves yet.
	p.reviewEgressFailover(ctx, seed, degraded)
	if got := defaultExitOf(t, st, "everyone"); got != "node2" {
		t.Fatalf("before debounce: everyone should still egress via node2, got %s", got)
	}
	if p.exitFailedOver["node2"] {
		t.Fatal("before debounce: must not have failed over yet")
	}

	// Past the debounce, the groups auto-move to the healthy exit and node2 drains.
	clock = clock.Add(61 * time.Second)
	p.reviewEgressFailover(ctx, seed, degraded)
	for _, gid := range []string{"admins", "everyone"} {
		if got := defaultExitOf(t, st, gid); got != "node1" {
			t.Fatalf("after failover: %s should egress via node1, got %s", gid, got)
		}
	}
	if !nodeDrained(t, st, "node2") {
		t.Fatal("after failover: dead exit node2 should be drained")
	}
	if !p.exitFailedOver["node2"] {
		t.Fatal("after failover: node2 should be marked failed-over")
	}

	// A repeat poll while still degraded is idempotent (already failed over).
	clock = clock.Add(61 * time.Second)
	p.reviewEgressFailover(ctx, seed, degraded)
	if got := defaultExitOf(t, st, "everyone"); got != "node1" {
		t.Fatalf("repeat poll must not re-move: got %s", got)
	}

	// Recovery clears the outage bookkeeping but does NOT auto-revert the groups.
	recovered := map[string]model.NodeHealth{"entryA": model.HealthHealthy, "node1": model.HealthHealthy, "node2": model.HealthHealthy}
	p.reviewEgressFailover(ctx, seed, recovered)
	if p.exitFailedOver["node2"] || !p.exitDegradedSince["node2"].IsZero() {
		t.Fatal("recovery should clear the outage streak state")
	}
	if got := defaultExitOf(t, st, "everyone"); got != "node1" {
		t.Fatalf("recovery must not auto-revert: everyone should stay on node1, got %s", got)
	}
}

// TestEgressFailoverNoHealthyTarget checks that when no healthy exit exists the
// groups are left in place (not blindly moved to a bad node), the dead exit is
// NOT drained, and the "no target" state is recorded so the alert fires once.
func TestEgressFailoverNoHealthyTarget(t *testing.T) {
	p, st, _ := newPanel(t)
	ctx := context.Background()
	seed := model.WorkedExample()
	for _, n := range seed.Nodes {
		if err := st.UpsertNode(ctx, n); err != nil {
			t.Fatal(err)
		}
	}
	for _, g := range seed.Groups {
		if err := st.UpsertGroup(ctx, g); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.SaveSettings(ctx, model.Settings{Panel: model.PanelSettings{EgressFailoverSeconds: 60}}); err != nil {
		t.Fatal(err)
	}
	clock := time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC)
	p.now = func() time.Time { return clock }

	// Both exits are degraded -> no healthy exit to move to.
	allBad := map[string]model.NodeHealth{"entryA": model.HealthHealthy, "node1": model.HealthDegraded, "node2": model.HealthDegraded}
	p.reviewEgressFailover(ctx, seed, allBad) // start streak
	clock = clock.Add(61 * time.Second)
	p.reviewEgressFailover(ctx, seed, allBad) // due, but no target

	if got := defaultExitOf(t, st, "everyone"); got != "node2" {
		t.Fatalf("no-target: groups must stay put, got %s", got)
	}
	if nodeDrained(t, st, "node2") {
		t.Fatal("no-target: dead exit must not be drained when nothing could be moved")
	}
	if !p.exitBlackholed["node2"] {
		t.Fatal("no-target: node2 should be marked blackholed (alert fired once)")
	}
	if p.exitFailedOver["node2"] {
		t.Fatal("no-target: must not be marked failed-over")
	}
}

func totalFor(totals []store.UserTrafficTotal, userID string) *store.UserTrafficTotal {
	for i := range totals {
		if totals[i].UserID == userID {
			return &totals[i]
		}
	}
	return nil
}

func startAgent(t *testing.T, nodeID string, layout controller.Layout) string {
	t.Helper()
	ag, err := agent.OpenStore(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	r := agent.NewReconciler(agent.Config{
		NodeID: nodeID, Roots: layout.Roots(),
		ServiceAllowlist: []string{controller.ServiceTrustTunnel, controller.ServiceSingBox},
	}, ag, fakeSvc{}, okChecker{})
	ts := httptest.NewServer(agent.NewServer(nodeID, r, ag, nil).Handler())
	t.Cleanup(ts.Close)
	return ts.URL
}

type fakeSvc struct{}

func (fakeSvc) Restart(context.Context, string) error          { return nil }
func (fakeSvc) Stop(context.Context, string) error             { return nil }
func (fakeSvc) Status(context.Context, string) (string, error) { return "active", nil }

type okChecker struct{}

func (okChecker) Check(context.Context, string, string) error { return nil }

// recordingRunner is a provFake that also records the files written, so we can
// assert provisioning stamps fallback.env + agent.env onto entries.
type recordingRunner struct {
	provFake
	files map[string][]byte
}

func (r *recordingRunner) Put(_ context.Context, b []byte, path string, _ os.FileMode) error {
	if r.files == nil {
		r.files = map[string][]byte{}
	}
	r.files[path] = append([]byte(nil), b...)
	return nil
}

func TestProvisionEntryAutoConfig(t *testing.T) {
	p, st, _ := newPanel(t)
	caCert, caKey, _ := pki.GenerateCA("test", time.Hour)
	ca, _ := pki.LoadCA(caCert, caKey)
	rec := &recordingRunner{}
	p.EnableProvisioning(&ProvisionConfig{
		CA: ca, CertValidity: time.Hour,
		TrustPanelBin: []byte("ELF"), SingBoxBin: []byte("ELF"), TrustTunnelBin: []byte("ELF"),
		Brand: "Acme CDN", Domain: "acme.example", ACMEEmail: "ops@acme.example",
		Units: func(model.PublicRole) []provision.UnitFile {
			return []provision.UnitFile{{Name: "trustpanel-agent.service", Content: []byte("[Service]"), Enable: true}}
		},
		NewRunner: func(context.Context, provision.SSHParams) (provision.Runner, func(), error) {
			return rec, func() {}, nil
		},
	})

	c, url := newClient(t, p)
	code, body := do(t, c, http.MethodPost, url+"/api/nodes/provision", map[string]any{
		"name": "Entry A", "role": "entry", "public_ips": []string{"203.0.113.30"}, "domain": "acme.example",
		"ssh": map[string]any{"host": "203.0.113.30", "user": "root", "password": "x"},
	})
	if code != http.StatusOK {
		t.Fatalf("provision: %d %s", code, body)
	}
	var start struct {
		JobID  string `json:"job_id"`
		NodeID string `json:"node_id"`
	}
	_ = json.Unmarshal(body, &start)

	deadline := time.Now().Add(3 * time.Second)
	var status string
	for time.Now().Before(deadline) {
		_, jb := do(t, c, http.MethodGet, url+"/api/jobs/"+start.JobID, nil)
		var j struct{ Status, Error string }
		_ = json.Unmarshal(jb, &j)
		if status = j.Status; status != "running" {
			if status != "succeeded" {
				t.Fatalf("job failed: %s", j.Error)
			}
			break
		}
		time.Sleep(15 * time.Millisecond)
	}
	if status != "succeeded" {
		t.Fatalf("job did not succeed: %q", status)
	}

	fb := string(rec.files["/etc/trustpanel/fallback.env"])
	if !strings.Contains(fb, "TRUSTPANEL_BRAND=Acme CDN") || !strings.Contains(fb, "TRUSTPANEL_DOMAIN=acme.example") {
		t.Errorf("fallback.env not stamped: %q", fb)
	}
	ae := string(rec.files["/etc/trustpanel/agent.env"])
	if !strings.Contains(ae, "TRUSTPANEL_ACME_DOMAINS=vpn.acme.example,acme.example") {
		t.Errorf("agent.env ACME domains wrong: %q", ae)
	}
	if !strings.Contains(ae, "TRUSTPANEL_ACME_EMAIL=ops@acme.example") {
		t.Errorf("agent.env ACME email missing: %q", ae)
	}

	// Both the portal (main-fallback / connection point) and apex (fallback-site) domains registered.
	got, _ := st.LoadState(context.Background())
	var n int
	for _, d := range got.Domains {
		if d.NodeID == start.NodeID {
			n++
		}
	}
	if n != 2 {
		t.Errorf("want 2 domains for entry, got %d", n)
	}
}

// captureAlerter records alert messages for assertions.
type captureAlerter struct {
	msgs []string
	sevs []watchdog.Severity
}

func (c *captureAlerter) Alert(_ context.Context, sev watchdog.Severity, m string) error {
	c.msgs = append(c.msgs, m)
	c.sevs = append(c.sevs, sev)
	return nil
}

func TestBillingAlert(t *testing.T) {
	p, st, _ := newPanel(t)
	ctx := context.Background()
	if err := st.UpsertNode(ctx, model.Node{ID: "exit1", Name: "Exit 1", PublicRole: model.RoleExit,
		PublicIPs: []string{"1.2.3.4"}, AgentAddr: "1.2.3.4:8443",
		DialIn: &model.DialIn{Proto: model.DialInProtoVLESSReality, Port: 443, UUID: "u", TargetSNI: "x"}}); err != nil {
		t.Fatal(err)
	}
	// due in 3 days -> alert; another node far out -> no alert.
	soon := time.Now().Add(72 * time.Hour)
	if err := st.SetNodeBilling(ctx, "exit1", &model.NodeBilling{PaidUntil: &soon, TermMonths: 1}); err != nil {
		t.Fatal(err)
	}
	cap := &captureAlerter{}
	p.SetBillingAlerts(cap, 7)
	p.CheckBillingDue(ctx)
	if len(cap.msgs) != 1 || !strings.Contains(cap.msgs[0], "due in") || !strings.Contains(cap.msgs[0], "Exit 1") {
		t.Fatalf("expected one due alert, got %v", cap.msgs)
	}
	// Move payment far out -> no alert.
	far := time.Now().Add(40 * 24 * time.Hour)
	_ = st.SetNodeBilling(ctx, "exit1", &model.NodeBilling{PaidUntil: &far, TermMonths: 1})
	cap.msgs = nil
	p.CheckBillingDue(ctx)
	if len(cap.msgs) != 0 {
		t.Fatalf("should not alert when far out, got %v", cap.msgs)
	}
	// Overdue -> alert saying it's overdue.
	past := time.Now().Add(-48 * time.Hour)
	_ = st.SetNodeBilling(ctx, "exit1", &model.NodeBilling{PaidUntil: &past, TermMonths: 1})
	cap.msgs = nil
	p.CheckBillingDue(ctx)
	if len(cap.msgs) != 1 || !strings.Contains(cap.msgs[0], "overdue by") {
		t.Fatalf("expected overdue alert, got %v", cap.msgs)
	}
}

func TestConfigHealthAlert(t *testing.T) {
	p, st, _ := newPanel(t)
	ctx := context.Background()
	// One enabled client in namespace "op1", and NO entry node in the fleet.
	if err := st.CreateNamespace(ctx, "op1", "op1"); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertGroup(ctx, model.Group{ID: "g-op", Name: "op grp", OwnerID: "op1"}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertUser(ctx, model.User{ID: "u-op", Username: "opclient", Enabled: true, GroupID: "g-op", OwnerID: "op1"}); err != nil {
		t.Fatal(err)
	}
	cap := &captureAlerter{}
	p.SetConfigHealthAlerts(cap)

	// No usable entry -> one "cannot get a working config" alert for the namespace.
	p.CheckConfigHealth(ctx)
	if len(cap.msgs) != 1 || !strings.Contains(cap.msgs[0], "cannot get a working config") {
		t.Fatalf("expected one config-health alert, got %v", cap.msgs)
	}
	// Sustained outage is not re-sent (in-memory dedup on the bad↔ok transition).
	cap.msgs = nil
	p.CheckConfigHealth(ctx)
	if len(cap.msgs) != 0 {
		t.Fatalf("sustained outage should not re-alert, got %v", cap.msgs)
	}
	// Add a healthy in-rotation entry -> one recovery alert, then steady silence.
	if err := st.UpsertNode(ctx, model.Node{ID: "entry1", Name: "Entry 1", PublicRole: model.RoleEntry,
		Health: model.HealthHealthy, PublicIPs: []string{"1.2.3.4"}, AgentAddr: "1.2.3.4:8443"}); err != nil {
		t.Fatal(err)
	}
	cap.msgs = nil
	p.CheckConfigHealth(ctx)
	if len(cap.msgs) != 1 || !strings.Contains(cap.msgs[0], "restored") {
		t.Fatalf("expected one recovery alert, got %v", cap.msgs)
	}
	cap.msgs = nil
	p.CheckConfigHealth(ctx)
	if len(cap.msgs) != 0 {
		t.Fatalf("healthy fleet should be silent, got %v", cap.msgs)
	}
}

// TestConvertHelpers exercises the DB-mutation steps of the convert-to-two-node
// job (the network-dependent provision + health gate are covered by the live run
// and the existing provision/reconcile tests).
func TestConvertHelpers(t *testing.T) {
	p, st, _ := newPanel(t)
	ctx := context.Background()

	// Seed a single box A: an mgmt entry holding the control plane, two groups,
	// and A's two TLS host rows (login subdomain + apex landing).
	a := model.Node{ID: "a", Name: "Box A", PublicRole: model.RoleEntry, MgmtCapable: true,
		PublicIPs: []string{"203.0.113.1"}, AgentAddr: "127.0.0.1:8443", PGRole: model.PGPrimary,
		ManagedServices: []string{"trusttunnel.service", "trustpanel-singbox.service"}}
	if err := st.UpsertNode(ctx, a); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Promote(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	for _, g := range []model.Group{{ID: "default", Name: "default"}, {ID: "g2", Name: "team"}} {
		if err := st.UpsertGroup(ctx, g); err != nil {
			t.Fatal(err)
		}
	}
	for _, d := range []model.Domain{
		{ID: "d-portal", Hostname: "portal.a.example", Purpose: model.PurposeMainFallback, NodeID: "a"},
		{ID: "d-apex", Hostname: "a.example", Purpose: model.PurposeFallbackSite, NodeID: "a"},
	} {
		if err := st.UpsertDomain(ctx, d); err != nil {
			t.Fatal(err)
		}
	}

	state, err := st.LoadState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	src, ok := findConvertSource(state)
	if !ok || src.ID != "a" {
		t.Fatalf("findConvertSource = %v, %v; want a", src, ok)
	}

	// Flip A to exit (precondition for a valid group default_exit_id).
	aExit := a
	aExit.PublicRole = model.RoleExit
	aExit.DialIn = &model.DialIn{Proto: model.DialInProtoVLESSReality, Port: 443, UUID: "u", TargetSNI: "sni"}
	if err := st.UpsertNode(ctx, aExit); err != nil {
		t.Fatal(err)
	}
	// ManagedServices must survive the flip upsert (COALESCE preserve).
	if fl, _ := st.LoadState(ctx); func() bool {
		n, _ := fl.NodeByID("a")
		return len(n.ManagedServices) != 2
	}() {
		t.Fatalf("managed_services lost across flip upsert")
	}

	// Egress: all groups now tunnel through A.
	n, err := p.switchEgress(ctx, nil, "a")
	if err != nil || n != 2 {
		t.Fatalf("switchEgress = %d, %v; want 2", n, err)
	}
	fl, _ := st.LoadState(ctx)
	for _, g := range fl.Groups {
		if g.DefaultExitID != "a" {
			t.Errorf("group %s default_exit_id = %q", g.ID, g.DefaultExitID)
		}
	}

	// Variant 1: A's domain rows are dropped (B's own rows live elsewhere).
	if err := p.repointDomains(ctx, 1, "a", "b", nil); err != nil {
		t.Fatal(err)
	}
	if fl, _ = st.LoadState(ctx); len(fl.Domains) != 0 {
		t.Fatalf("variant 1 should drop A's domains, got %+v", fl.Domains)
	}

	// Variant 2: A's rows are reassigned to B (re-seed first).
	for _, d := range []model.Domain{
		{ID: "d-portal", Hostname: "portal.a.example", Purpose: model.PurposeMainFallback, NodeID: "a"},
		{ID: "d-apex", Hostname: "a.example", Purpose: model.PurposeFallbackSite, NodeID: "a"},
	} {
		if err := st.UpsertDomain(ctx, d); err != nil {
			t.Fatal(err)
		}
	}
	// Need node b to exist for the FK.
	b := model.Node{ID: "b", Name: "Box B", PublicRole: model.RoleEntry, PublicIPs: []string{"203.0.113.2"}, AgentAddr: "203.0.113.2:8443"}
	if err := st.UpsertNode(ctx, b); err != nil {
		t.Fatal(err)
	}
	if err := p.repointDomains(ctx, 2, "a", "b", nil); err != nil {
		t.Fatal(err)
	}
	fl, _ = st.LoadState(ctx)
	if len(fl.Domains) != 2 {
		t.Fatalf("variant 2 should keep 2 domains, got %d", len(fl.Domains))
	}
	for _, d := range fl.Domains {
		if d.NodeID != "b" {
			t.Errorf("domain %s not reassigned to b: node=%s", d.Hostname, d.NodeID)
		}
	}
}

func TestConvertDomainHelpers(t *testing.T) {
	// Variant 1: operator-supplied hostnames are used verbatim — first is the
	// client endpoint (main-fallback), the rest are extra SANs (fallback-site),
	// and all go into the cert (acme list).
	doms, acme := literalEntryDomains("b", []string{"lk.privatecdn.ru", "privatecdn.ru"})
	if len(doms) != 2 || len(acme) != 2 {
		t.Fatalf("want 2 rows + 2 SANs, got %d rows, %d SANs", len(doms), len(acme))
	}
	if doms[0].Hostname != "lk.privatecdn.ru" || doms[0].Purpose != model.PurposeMainFallback {
		t.Errorf("first domain should be the main-fallback endpoint, got %+v", doms[0])
	}
	if doms[1].Purpose != model.PurposeFallbackSite {
		t.Errorf("extra domain should be fallback-site, got %q", doms[1].Purpose)
	}
	if doms[0].NodeID != "b" || acme[0] != "lk.privatecdn.ru" {
		t.Errorf("node id / acme SAN mismatch: %+v / %v", doms[0], acme)
	}

	// Variant 2: B inherits A's existing hostnames — derived from A's domain rows.
	st := model.State{Domains: []model.Domain{
		{Hostname: "lk.live-vkplay.ru", NodeID: "a"},
		{Hostname: "live-vkplay.ru", NodeID: "a"},
		{Hostname: "other.example", NodeID: "z"},
	}}
	hosts := nodeHostnames(st, "a")
	if len(hosts) != 2 || hosts[0] != "lk.live-vkplay.ru" || hosts[1] != "live-vkplay.ru" {
		t.Fatalf("nodeHostnames(a) = %v; want A's two rows only", hosts)
	}

	if got := trimmedNonEmpty([]string{" a ", "", "  ", "b"}); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("trimmedNonEmpty = %v", got)
	}
}

// loginCSRF creates the admin, logs in, and returns the per-session CSRF token.
func loginCSRF(t *testing.T, c *http.Client, url string) string {
	t.Helper()
	if code, body := do(t, c, http.MethodPost, url+"/api/admin",
		map[string]string{"username": "admin", "password": "supersecret"}); code != http.StatusOK {
		t.Fatalf("create admin: %d %s", code, body)
	}
	code, body := do(t, c, http.MethodPost, url+"/api/auth/login",
		map[string]string{"username": "admin", "password": "supersecret"})
	if code != http.StatusOK {
		t.Fatalf("login: %d %s", code, body)
	}
	var resp struct {
		CSRF string `json:"csrf_token"`
	}
	if err := json.Unmarshal(body, &resp); err != nil || resp.CSRF == "" {
		t.Fatalf("login response missing csrf_token: %s (%v)", body, err)
	}
	return resp.CSRF
}

func doCSRF(t *testing.T, c *http.Client, url, csrf string, body any) (int, []byte) {
	t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("post %s: %v", url, err)
	}
	defer resp.Body.Close()
	buf := new(bytes.Buffer)
	_, _ = buf.ReadFrom(resp.Body)
	return resp.StatusCode, buf.Bytes()
}

func TestAddStandbyAuthAndValidation(t *testing.T) {
	p, st, _ := newPanel(t)
	c, url := newClient(t, p)
	csrf := loginCSRF(t, c, url)

	// A registered exit node to (try to) make a standby.
	exit := model.Node{
		ID: "exit-b", Name: "Exit B", PublicRole: model.RoleExit,
		PublicIPs: []string{"203.0.113.9"}, AgentAddr: "203.0.113.9:8443",
		DialIn: &model.DialIn{Proto: model.DialInProtoVLESSReality, Port: 443, UUID: "u-b", TargetSNI: "www.example.com"},
	}
	if err := st.UpsertNode(context.Background(), exit); err != nil {
		t.Fatal(err)
	}

	// Missing CSRF header → 403.
	if code, body := doCSRF(t, c, url+"/api/cluster/add-standby", "", map[string]string{"node_id": "exit-b", "confirm": "exit-b"}); code != http.StatusForbidden {
		t.Fatalf("no CSRF should be 403, got %d %s", code, body)
	}

	// CSRF present but confirm mismatch → 400.
	if code, body := doCSRF(t, c, url+"/api/cluster/add-standby", csrf, map[string]string{"node_id": "exit-b", "confirm": "nope"}); code != http.StatusBadRequest {
		t.Fatalf("confirm mismatch should be 400, got %d %s", code, body)
	}

	// CSRF present, unknown node → 400.
	if code, body := doCSRF(t, c, url+"/api/cluster/add-standby", csrf, map[string]string{"node_id": "ghost", "confirm": "ghost"}); code != http.StatusBadRequest {
		t.Fatalf("unknown node should be 400, got %d %s", code, body)
	}
}

// TestReplicationProbeAndOverview verifies the standby replication healthcheck:
// a standby whose physical slot exists is reported (active=false here, since no
// real replica is attached), a standby with NO slot is reported Missing, and both
// surface on /api/overview. This is what drives the UI's yellow HA-warning dot.
func TestReplicationProbeAndOverview(t *testing.T) {
	p, st, _ := newPanel(t)
	ctx := context.Background()
	c, url := newClient(t, p)
	_ = loginCSRF(t, c, url) // sets the session cookie for GET /api/overview

	mkExit := func(id, ip string) model.Node {
		return model.Node{
			ID: id, Name: id, PublicRole: model.RoleExit,
			PublicIPs: []string{ip}, AgentAddr: ip + ":8443", Health: model.HealthHealthy,
			DialIn: &model.DialIn{Proto: model.DialInProtoVLESSReality, Port: 443, UUID: "u-" + id, TargetSNI: "www.example.com"},
		}
	}
	for _, n := range []model.Node{mkExit("repl-ok", "203.0.113.31"), mkExit("repl-missing", "203.0.113.32")} {
		if err := st.UpsertNode(ctx, n); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.SetStandbys(ctx, []string{"repl-ok", "repl-missing"}); err != nil {
		t.Fatal(err)
	}

	// Create the physical slot only for repl-ok (named the way the cluster code does).
	conn, err := pgx.Connect(ctx, testDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	slot := cluster.SlotName("repl-ok")
	_, _ = conn.Exec(ctx, "SELECT pg_drop_replication_slot($1)", slot)
	if _, err := conn.Exec(ctx, "SELECT pg_create_physical_replication_slot($1)", slot); err != nil {
		t.Fatalf("create slot: %v", err)
	}
	defer conn.Exec(ctx, "SELECT pg_drop_replication_slot($1)", slot)

	p.ProbeReplicationOnce(ctx)

	p.replMu.Lock()
	okRec, okSeen := p.lastRepl["repl-ok"]
	missRec, missSeen := p.lastRepl["repl-missing"]
	p.replMu.Unlock()
	if !okSeen || okRec.Missing {
		t.Errorf("repl-ok should have a slot recorded, got seen=%v rec=%+v", okSeen, okRec)
	}
	if okRec.Active {
		t.Errorf("repl-ok slot has no consumer; should be inactive")
	}
	if !missSeen || !missRec.Missing {
		t.Errorf("repl-missing should be recorded Missing, got seen=%v rec=%+v", missSeen, missRec)
	}

	// /api/overview surfaces the replication block per standby.
	code, body := do(t, c, http.MethodGet, url+"/api/overview", nil)
	if code != http.StatusOK {
		t.Fatalf("overview: %d %s", code, body)
	}
	var ov struct {
		Nodes []struct {
			ID          string `json:"id"`
			Replication *struct {
				Active  bool `json:"active"`
				Missing bool `json:"missing"`
			} `json:"replication"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(body, &ov); err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{} // node id -> missing
	for _, n := range ov.Nodes {
		if n.Replication != nil {
			got[n.ID] = n.Replication.Missing
		}
	}
	if miss, ok := got["repl-ok"]; !ok || miss {
		t.Errorf("overview repl-ok should have replication present & not missing, got ok=%v miss=%v", ok, miss)
	}
	if miss, ok := got["repl-missing"]; !ok || !miss {
		t.Errorf("overview repl-missing should have replication present & missing, got ok=%v miss=%v", ok, miss)
	}
}

func TestSaveSettingsPreservesBackup(t *testing.T) {
	p, st, _ := newPanel(t)
	ctx := context.Background()

	// Configure the alert token + a backup block via the save handler.
	save := func(body string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/settings", strings.NewReader(body))
		rec := httptest.NewRecorder()
		p.handleSaveSettings(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("save settings: HTTP %d: %s", rec.Code, rec.Body.String())
		}
	}
	save(`{"alert":{"enabled":true,"token":"456:def","chat_id":"-100123"},
	       "backup":{"enabled":true,"chat_id":"-100999","age_recipient":"age1abc","chunk_bytes":4096,
	                 "local_enabled":false,"interval_hours":6,"keep":30,"verify_enabled":true,"verify_interval_days":3}}`)

	got, err := st.GetSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Backup.Enabled || got.Backup.ChatID != "-100999" || got.Backup.AgeRecipient != "age1abc" || got.Backup.ChunkBytes != 4096 {
		t.Fatalf("backup not persisted: %+v", got.Backup)
	}
	if got.Backup.LocalOn() || got.Backup.IntervalHours != 6 || got.Backup.Keep != 30 || !got.Backup.VerifyOn() || got.Backup.VerifyIntervalDays != 3 {
		t.Fatalf("backup schedule not persisted: %+v", got.Backup)
	}

	// A subsequent save that leaves the alert token blank ("keep") must not wipe
	// the backup block.
	save(`{"alert":{"enabled":true,"token":"","chat_id":"-100123"},
	       "backup":{"enabled":true,"chat_id":"-100999","age_recipient":"age1abc","chunk_bytes":4096}}`)
	got, _ = st.GetSettings(ctx)
	if got.Alert.Token != "456:def" {
		t.Fatalf("alert token should have been kept, got %q", got.Alert.Token)
	}
	if !got.Backup.Enabled || got.Backup.AgeRecipient != "age1abc" {
		t.Fatalf("backup wiped on re-save: %+v", got.Backup)
	}
	// Omitting the toggles on re-save must not silently flip a backup off.
	if got.Backup.LocalOn() {
		t.Fatalf("local_enabled should have been kept false on re-save: %+v", got.Backup)
	}

	// The masked GET view should surface the backup block (age recipient is public).
	greq := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	grec := httptest.NewRecorder()
	p.handleGetSettings(grec, greq)
	var view struct {
		Backup struct {
			Enabled            bool   `json:"enabled"`
			ChatID             string `json:"chat_id"`
			AgeRecipient       string `json:"age_recipient"`
			ChunkBytes         int    `json:"chunk_bytes"`
			LocalEnabled       bool   `json:"local_enabled"`
			IntervalHours      int    `json:"interval_hours"`
			Keep               int    `json:"keep"`
			VerifyEnabled      bool   `json:"verify_enabled"`
			VerifyIntervalDays int    `json:"verify_interval_days"`
		} `json:"backup"`
	}
	if err := json.Unmarshal(grec.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if !view.Backup.Enabled || view.Backup.AgeRecipient != "age1abc" || view.Backup.ChunkBytes != 4096 {
		t.Fatalf("get view missing backup: %+v", view.Backup)
	}
	// Effective schedule values surface (defaults applied for the re-save that
	// omitted the numerics: interval 0 -> 24, keep 0 -> 14); the toggle stayed off.
	if view.Backup.LocalEnabled || view.Backup.IntervalHours != 24 || view.Backup.Keep != 14 || !view.Backup.VerifyEnabled || view.Backup.VerifyIntervalDays != 7 {
		t.Fatalf("get view schedule wrong: %+v", view.Backup)
	}
}

// TestAlertSharesBotChannelFlag checks that the settings view flags when the
// primary (α) and backup (β) alert senders resolve to the same bot (no distinct
// management bot), so the UI can warn that the two test buttons check one channel.
func TestAlertSharesBotChannelFlag(t *testing.T) {
	p, _, _ := newPanel(t)
	get := func() bool {
		t.Helper()
		grec := httptest.NewRecorder()
		p.handleGetSettings(grec, httptest.NewRequest(http.MethodGet, "/api/settings", nil))
		var v struct {
			Alert struct {
				SharesBotChannel bool `json:"shares_bot_channel"`
			} `json:"alert"`
		}
		if err := json.Unmarshal(grec.Body.Bytes(), &v); err != nil {
			t.Fatal(err)
		}
		return v.Alert.SharesBotChannel
	}
	save := func(body string) {
		t.Helper()
		rec := httptest.NewRecorder()
		p.handleSaveSettings(rec, httptest.NewRequest(http.MethodPost, "/api/settings", strings.NewReader(body)))
		if rec.Code != http.StatusOK {
			t.Fatalf("save: %d %s", rec.Code, rec.Body.String())
		}
	}
	// Alert on, no management bot -> α falls back to the alert token = β: coincide.
	save(`{"alert":{"enabled":true,"token":"222:bbb","chat_id":"-100"}}`)
	if !get() {
		t.Errorf("with no mgmt bot, alert should report shares_bot_channel=true")
	}
	// A distinct management bot -> α uses it, β uses alert: independent.
	save(`{"bot":{"enabled":true,"token":"111:aaa"},"alert":{"enabled":true,"token":"222:bbb","chat_id":"-100"}}`)
	if get() {
		t.Errorf("with a distinct mgmt bot, channels are independent (shares_bot_channel=false)")
	}
}

// TestSaveSettingsPartialDoesNotClobberAlertBot checks that a partial POST that
// omits the alert/bot sections must NOT silently disable them or blank the alert
// chat_id — the *bool/*string guards keep the stored values (like Backup).
func TestSaveSettingsPartialDoesNotClobberAlertBot(t *testing.T) {
	p, st, _ := newPanel(t)
	ctx := context.Background()
	save := func(body string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/settings", strings.NewReader(body))
		rec := httptest.NewRecorder()
		p.handleSaveSettings(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("save settings: HTTP %d: %s", rec.Code, rec.Body.String())
		}
	}
	// Configure both channels on.
	save(`{"bot":{"enabled":true,"token":"111:aaa"},"alert":{"enabled":true,"token":"222:bbb","chat_id":"-100777"}}`)

	// A partial POST touching only the bot section must not disable/blank alert.
	save(`{"bot":{"enabled":true,"token":""}}`)
	got, err := st.GetSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Alert.Enabled {
		t.Errorf("omitted alert section must not disable alerting")
	}
	if got.Alert.ChatID != "-100777" {
		t.Errorf("omitted alert chat_id must be kept, got %q", got.Alert.ChatID)
	}
	if got.Alert.Token != "222:bbb" {
		t.Errorf("omitted alert token must be kept, got %q", got.Alert.Token)
	}
	// An explicit disable still works.
	save(`{"alert":{"enabled":false,"token":"","chat_id":"-100777"}}`)
	got, _ = st.GetSettings(ctx)
	if got.Alert.Enabled {
		t.Errorf("explicit alert enabled=false should disable")
	}
}
