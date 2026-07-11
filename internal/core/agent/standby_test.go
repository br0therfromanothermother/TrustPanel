package agent

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"trustpanel/internal/core/agentapi"
	"trustpanel/internal/core/bootstrap"
)

func postJSON(t *testing.T, url string, body any) (int, []byte) {
	t.Helper()
	b, _ := json.Marshal(body)
	resp, err := http.Post(url, "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatalf("post %s: %v", url, err)
	}
	defer resp.Body.Close()
	buf := new(bytes.Buffer)
	_, _ = buf.ReadFrom(resp.Body)
	return resp.StatusCode, buf.Bytes()
}

// TestStandbyDisabled: with provisioning not enabled, both routes refuse with 400.
func TestStandbyDisabled(t *testing.T) {
	s := NewServer("n1", nil, nil, nil)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	if code, _ := postJSON(t, ts.URL+"/v1/standby/primary", agentapi.PrepareStandbyRequest{}); code != http.StatusBadRequest {
		t.Errorf("primary disabled = %d, want 400", code)
	}
	if code, _ := postJSON(t, ts.URL+"/v1/standby/replica", agentapi.PrepareReplicaRequest{}); code != http.StatusBadRequest {
		t.Errorf("replica disabled = %d, want 400", code)
	}
}

// TestStandbyConfirmGuard: a request whose confirm does not name the target is
// rejected before any privileged work runs.
func TestStandbyConfirmGuard(t *testing.T) {
	s := NewServer("n1", nil, nil, nil)
	s.EnableStandbyProvisioning(StandbyConfig{PKIDir: t.TempDir(), Layout: bootstrap.DefaultLayout(), Validity: time.Hour})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	// primary: confirm != standby_id
	code, body := postJSON(t, ts.URL+"/v1/standby/primary", agentapi.PrepareStandbyRequest{
		PrimaryID: "p", StandbyID: "s", PrimaryIP: "10.0.0.1", StandbyIP: "10.0.0.2",
		StandbyAddr: "10.0.0.2:8443", Confirm: "wrong",
	})
	if code != http.StatusUnprocessableEntity {
		t.Errorf("primary bad confirm = %d (%s), want 422", code, body)
	}

	// replica: confirm != standby_id
	code, body = postJSON(t, ts.URL+"/v1/standby/replica", agentapi.PrepareReplicaRequest{
		StandbyID: "s", Confirm: "wrong",
	})
	if code != http.StatusUnprocessableEntity {
		t.Errorf("replica bad confirm = %d (%s), want 422", code, body)
	}
}

// TestStandbyPrimaryRequiresFields: matching confirm but missing addr/ips is 422.
func TestStandbyPrimaryRequiresFields(t *testing.T) {
	s := NewServer("n1", nil, nil, nil)
	s.EnableStandbyProvisioning(StandbyConfig{PKIDir: t.TempDir(), Layout: bootstrap.DefaultLayout(), Validity: time.Hour})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	code, body := postJSON(t, ts.URL+"/v1/standby/primary", agentapi.PrepareStandbyRequest{
		PrimaryID: "p", StandbyID: "s", Confirm: "s", // no IPs / addr
	})
	if code != http.StatusUnprocessableEntity {
		t.Errorf("missing fields = %d (%s), want 422", code, body)
	}
}

func TestOutboundControllerTLSRejectsBadCA(t *testing.T) {
	if _, err := outboundControllerTLS([]byte("not a pem"), tls.Certificate{}); err == nil {
		t.Error("outboundControllerTLS should reject an unparseable CA")
	}
}

func TestRandomHexAndParseIPs(t *testing.T) {
	a, err := randomHex(16)
	if err != nil {
		t.Fatal(err)
	}
	if len(a) != 32 {
		t.Errorf("randomHex(16) len = %d, want 32", len(a))
	}
	b, _ := randomHex(16)
	if a == b {
		t.Error("randomHex should not repeat")
	}
	ips := parseIPs([]string{"10.0.0.2", "garbage", "127.0.0.1"})
	if len(ips) != 2 {
		t.Errorf("parseIPs kept %d, want 2 (dropping garbage)", len(ips))
	}
}
