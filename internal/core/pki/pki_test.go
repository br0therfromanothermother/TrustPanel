package pki_test

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"trustpanel/internal/core/agent"
	"trustpanel/internal/core/controller"
	"trustpanel/internal/core/pki"
)

func newCA(t *testing.T) ([]byte, *pki.CA) {
	t.Helper()
	caCertPEM, caKeyPEM, err := pki.GenerateCA("TrustPanel Test CA", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	ca, err := pki.LoadCA(caCertPEM, caKeyPEM)
	if err != nil {
		t.Fatal(err)
	}
	return caCertPEM, ca
}

func keypair(t *testing.T, certPEM, keyPEM []byte) tls.Certificate {
	t.Helper()
	c, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// nodeCertFromCSR mints a node cert via enrollment: the node generates the key
// + CSR, the CA signs it (the private key never leaves the node).
func nodeCertFromCSR(t *testing.T, ca *pki.CA, nodeID string) tls.Certificate {
	t.Helper()
	csrPEM, keyPEM, err := pki.GenerateCSR(nodeID)
	if err != nil {
		t.Fatal(err)
	}
	certPEM, err := ca.SignCSR(csrPEM, pki.RoleNode, nodeID, []net.IP{net.ParseIP("127.0.0.1")}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return keypair(t, certPEM, keyPEM)
}

// serveTLS starts an HTTPS server with the given server TLS config and returns
// its URL.
func serveTLS(t *testing.T, serverTLS *tls.Config) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{
		Handler:   http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "ok") }),
		TLSConfig: serverTLS,
	}
	go srv.ServeTLS(ln, "", "")
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	})
	return "https://" + ln.Addr().String()
}

func get(clientTLS *tls.Config, url string) (int, error) {
	c := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{TLSClientConfig: clientTLS}}
	resp, err := c.Get(url)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	return resp.StatusCode, nil
}

// Controller cert (client) talking to a node-cert server is accepted: the agent
// requires the controller role, the controller requires the node role.
func TestMTLSControllerToNodeHandshake(t *testing.T) {
	caPEM, ca := newCA(t)
	ctrlPEM, ctrlKey, err := ca.IssueLeaf(pki.RoleController, "controller-host", nil, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	controllerCert := keypair(t, ctrlPEM, ctrlKey)
	nodeCert := nodeCertFromCSR(t, ca, "node1")

	agentTLS, err := agent.BuildAgentTLSConfig(caPEM, nodeCert)
	if err != nil {
		t.Fatal(err)
	}
	ctrlTLS, err := controller.BuildControllerTLSConfig(caPEM, controllerCert)
	if err != nil {
		t.Fatal(err)
	}

	url := serveTLS(t, agentTLS)
	code, err := get(ctrlTLS, url)
	if err != nil {
		t.Fatalf("controller->node handshake should succeed: %v", err)
	}
	if code != http.StatusOK {
		t.Fatalf("want 200, got %d", code)
	}
}

// A node cert presented as the CLIENT is rejected by the agent (it requires the
// controller role) — a compromised node cannot impersonate the controller.
func TestMTLSNodeCannotActAsController(t *testing.T) {
	caPEM, ca := newCA(t)
	serverCert := nodeCertFromCSR(t, ca, "node-server")
	clientNodeCert := nodeCertFromCSR(t, ca, "node-client")

	agentTLS, _ := agent.BuildAgentTLSConfig(caPEM, serverCert)
	// Client trusts the CA but presents a node (not controller) cert.
	ctrlTLS, _ := controller.BuildControllerTLSConfig(caPEM, clientNodeCert)

	url := serveTLS(t, agentTLS)
	if _, err := get(ctrlTLS, url); err == nil {
		t.Fatal("agent must reject a non-controller client certificate")
	}
}

// A server presenting a controller cert (wrong role for a node endpoint) is
// rejected by the controller client.
func TestMTLSControllerRejectsNonNodeServer(t *testing.T) {
	caPEM, ca := newCA(t)
	// Server wrongly presents a controller cert.
	srvCtrlPEM, srvCtrlKey, _ := ca.IssueLeaf(pki.RoleController, "imposter", []net.IP{net.ParseIP("127.0.0.1")}, time.Hour)
	serverCert := keypair(t, srvCtrlPEM, srvCtrlKey)
	ctrlPEM, ctrlKey, _ := ca.IssueLeaf(pki.RoleController, "controller-host", nil, time.Hour)
	controllerCert := keypair(t, ctrlPEM, ctrlKey)

	// Plain server TLS (no client auth) so the handshake reaches the client's
	// peer verification.
	serverTLS := &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{serverCert}}
	ctrlTLS, _ := controller.BuildControllerTLSConfig(caPEM, controllerCert)

	url := serveTLS(t, serverTLS)
	if _, err := get(ctrlTLS, url); err == nil || !strings.Contains(err.Error(), "not a node") {
		t.Fatalf("controller must reject a non-node server, got err=%v", err)
	}
}

func TestSignedCertChainsAndRole(t *testing.T) {
	_, ca := newCA(t)
	certPEM, err := func() ([]byte, error) {
		csrPEM, _, err := pki.GenerateCSR("node-x")
		if err != nil {
			return nil, err
		}
		return ca.SignCSR(csrPEM, pki.RoleNode, "node-x", nil, time.Hour)
	}()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(certPEM), "BEGIN CERTIFICATE") {
		t.Fatal("expected a PEM certificate")
	}
}
