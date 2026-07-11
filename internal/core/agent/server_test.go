package agent

import (
	"bytes"
	"context"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"trustpanel/internal/core/agentapi"
)

type nilStatus struct{}

func (nilStatus) Services(context.Context) []agentapi.ServiceStatus { return nil }
func (nilStatus) InstalledVersions() map[string]string {
	return map[string]string{"sing-box": "1.13.13"}
}

func newTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	root := t.TempDir()
	store, err := OpenStore(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	r := NewReconciler(Config{NodeID: "n1", Roots: []string{root}, ServiceAllowlist: []string{"svc-a"}},
		store, &fakeServices{failRestart: map[string]bool{}}, &fakeChecker{failKind: map[string]bool{}})
	return NewServer("n1", r, store, nilStatus{}), root
}

func putDesiredState(t *testing.T, h http.Handler, d agentapi.DesiredState) (int, agentapi.ReconcileResult) {
	t.Helper()
	body, _ := json.Marshal(d)
	req := httptest.NewRequest(http.MethodPut, "/v1/desired-state", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	var res agentapi.ReconcileResult
	_ = json.Unmarshal(rr.Body.Bytes(), &res)
	return rr.Code, res
}

func TestServerOutcomeMapping(t *testing.T) {
	srv, root := newTestServer(t)
	h := srv.Handler()
	p := filepath.Join(root, "c.json")

	// applied -> 200
	code, res := putDesiredState(t, h, ds("n1", 2, 1, []agentapi.File{mkFile(p, "x")}, nil, nil))
	if code != http.StatusOK || res.Outcome != agentapi.OutcomeApplied {
		t.Fatalf("applied: code=%d res=%+v", code, res)
	}

	// stale leader -> 409
	code, _ = putDesiredState(t, h, ds("n1", 1, 2, []agentapi.File{mkFile(p, "y")}, nil, nil))
	if code != http.StatusConflict {
		t.Fatalf("stale leader should be 409, got %d", code)
	}

	// rejected (wrong node) -> 422
	code, _ = putDesiredState(t, h, ds("other", 3, 3, []agentapi.File{mkFile(p, "z")}, nil, nil))
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("wrong node should be 422, got %d", code)
	}
}

func TestServerStatus(t *testing.T) {
	srv, root := newTestServer(t)
	h := srv.Handler()
	p := filepath.Join(root, "c.json")
	putDesiredState(t, h, ds("n1", 7, 3, []agentapi.File{mkFile(p, "x")}, nil, nil))

	req := httptest.NewRequest(http.MethodGet, "/v1/status", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	var st agentapi.StatusResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	if st.NodeID != "n1" || st.LastAcceptedEpoch != 7 || st.AppliedRevision != 3 {
		t.Fatalf("unexpected status: %+v", st)
	}
	if st.InstalledVersions["sing-box"] != "1.13.13" {
		t.Errorf("versions not reported: %+v", st.InstalledVersions)
	}
}

// trafficStatus implements StatusSource + UserTrafficSource; it returns canned
// traffic, or an error (modeled as nil, the graceful-degrade case) when fail.
type trafficStatus struct {
	traffic []agentapi.UserTrafficStat
	fail    bool
}

func (s trafficStatus) Services(context.Context) []agentapi.ServiceStatus { return nil }
func (trafficStatus) InstalledVersions() map[string]string                { return nil }
func (s trafficStatus) UserTraffic(context.Context) []agentapi.UserTrafficStat {
	if s.fail {
		return nil // stats API unreachable -> omit, don't fail status
	}
	return s.traffic
}

func statusOf(t *testing.T, src StatusSource) agentapi.StatusResponse {
	t.Helper()
	store, err := OpenStore(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	r := NewReconciler(Config{NodeID: "n1", Roots: []string{t.TempDir()}, ServiceAllowlist: []string{"svc-a"}},
		store, &fakeServices{failRestart: map[string]bool{}}, &fakeChecker{failKind: map[string]bool{}})
	h := NewServer("n1", r, store, src).Handler()
	req := httptest.NewRequest(http.MethodGet, "/v1/status", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	var st agentapi.StatusResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	return st
}

func TestServerStatusUserTraffic(t *testing.T) {
	// Present when the (fake) stats source returns data.
	st := statusOf(t, trafficStatus{traffic: []agentapi.UserTrafficStat{
		{Username: "alice", UplinkBytes: 10, DownlinkBytes: 20},
	}})
	if len(st.UserTraffic) != 1 || st.UserTraffic[0].Username != "alice" ||
		st.UserTraffic[0].UplinkBytes != 10 || st.UserTraffic[0].DownlinkBytes != 20 {
		t.Fatalf("UserTraffic not surfaced: %+v", st.UserTraffic)
	}

	// Omitted when the stats source degrades (error -> nil).
	if st := statusOf(t, trafficStatus{fail: true}); st.UserTraffic != nil {
		t.Fatalf("UserTraffic should be omitted on error, got %+v", st.UserTraffic)
	}

	// A plain StatusSource without the UserTrafficSource capability omits it.
	if st := statusOf(t, nilStatus{}); st.UserTraffic != nil {
		t.Fatalf("non-traffic source should omit UserTraffic, got %+v", st.UserTraffic)
	}
}

func TestHasRole(t *testing.T) {
	controller := &x509.Certificate{Subject: pkix.Name{OrganizationalUnit: []string{RoleController}}}
	node := &x509.Certificate{Subject: pkix.Name{OrganizationalUnit: []string{RoleNode}}}
	if !hasRole(controller, RoleController) {
		t.Error("controller cert should have controller role")
	}
	if hasRole(node, RoleController) {
		t.Error("node cert must not pass controller role check")
	}
}
