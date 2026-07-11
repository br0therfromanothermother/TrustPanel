package controller

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"trustpanel/internal/core/agentapi"
)

// Client pushes desired-state to a node agent over mTLS.
type Client struct {
	http *http.Client
}

// NewClient builds a controller HTTP client. tlsConfig should present the
// controller cert and trust the fleet CA (see BuildControllerTLSConfig). Pass
// nil for plaintext (tests only).
func NewClient(tlsConfig *tls.Config) *Client {
	tr := &http.Transport{TLSClientConfig: tlsConfig}
	return &Client{http: &http.Client{Timeout: 30 * time.Second, Transport: tr}}
}

// BuildControllerTLSConfig returns a client TLS config presenting the
// controller certificate and verifying the agent's certificate against the
// fleet CA and the node role. Hostname/SAN verification is skipped in favour of
// CA pinning + role check, so agent addresses (often bare IPs) need not match a
// SAN.
func BuildControllerTLSConfig(caPEM []byte, clientCert tls.Certificate) (*tls.Config, error) {
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("no CA certificates parsed from caPEM")
	}
	return &tls.Config{
		MinVersion:         tls.VersionTLS13,
		RootCAs:            pool,
		Certificates:       []tls.Certificate{clientCert},
		InsecureSkipVerify: true, // replaced by the manual chain+role check below
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			return verifyPeerRole(pool, rawCerts, roleNode)
		},
	}, nil
}

const (
	roleController = "controller"
	roleNode       = "node"
)

// verifyPeerRole parses the peer leaf, verifies it chains to the pool, and
// checks it carries the required role in its subject OU.
func verifyPeerRole(pool *x509.CertPool, rawCerts [][]byte, role string) error {
	if len(rawCerts) == 0 {
		return fmt.Errorf("no peer certificate")
	}
	leaf, err := x509.ParseCertificate(rawCerts[0])
	if err != nil {
		return err
	}
	inter := x509.NewCertPool()
	for _, raw := range rawCerts[1:] {
		if c, err := x509.ParseCertificate(raw); err == nil {
			inter.AddCert(c)
		}
	}
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: pool, Intermediates: inter,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err != nil {
		return err
	}
	for _, ou := range leaf.Subject.OrganizationalUnit {
		if ou == role {
			return nil
		}
	}
	return fmt.Errorf("peer certificate is not a %s (OU=%v)", role, leaf.Subject.OrganizationalUnit)
}

// PushDesiredState PUTs the desired-state to the agent and returns its result.
// A non-2xx that still carries a ReconcileResult body (e.g. 409 stale-leader,
// 422 rolled-back) is returned as the result, not an error.
func (c *Client) PushDesiredState(ctx context.Context, agentURL string, ds agentapi.DesiredState) (agentapi.ReconcileResult, error) {
	body, err := json.Marshal(ds)
	if err != nil {
		return agentapi.ReconcileResult{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, agentURL+"/v1/desired-state", bytes.NewReader(body))
	if err != nil {
		return agentapi.ReconcileResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return agentapi.ReconcileResult{}, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var res agentapi.ReconcileResult
	if err := json.Unmarshal(data, &res); err != nil {
		return agentapi.ReconcileResult{}, fmt.Errorf("agent returned %d: %s", resp.StatusCode, data)
	}
	return res, nil
}

// PromoteACME asks the agent to switch its node-local ACME issuance to Let's
// Encrypt production and reissue (POST /v1/acme/promote).
func (c *Client) PromoteACME(ctx context.Context, agentURL string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, agentURL+"/v1/acme/promote", nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("agent promote %d: %s", resp.StatusCode, data)
	}
	return nil
}

// PrepareStandby asks the PRIMARY node's agent to provision a control-plane
// standby (POST /v1/standby/primary). The agent does the privileged primary-side
// work and forwards the secret bundle to the standby's agent; the panel never
// handles the CA private key. A non-2xx that still carries a result body is
// returned as the result, not an error.
func (c *Client) PrepareStandby(ctx context.Context, primaryAgentURL string, in agentapi.PrepareStandbyRequest) (agentapi.PrepareStandbyResult, error) {
	body, err := json.Marshal(in)
	if err != nil {
		return agentapi.PrepareStandbyResult{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, primaryAgentURL+"/v1/standby/primary", bytes.NewReader(body))
	if err != nil {
		return agentapi.PrepareStandbyResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	// add-standby runs pg_basebackup on the standby and can take minutes; use a
	// long timeout but reuse the fleet client's mTLS transport.
	long := &http.Client{Timeout: 15 * time.Minute, Transport: c.http.Transport}
	resp, err := long.Do(req)
	if err != nil {
		return agentapi.PrepareStandbyResult{}, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var res agentapi.PrepareStandbyResult
	if err := json.Unmarshal(data, &res); err != nil {
		return agentapi.PrepareStandbyResult{}, fmt.Errorf("primary agent returned %d: %s", resp.StatusCode, data)
	}
	return res, nil
}

// Status fetches the agent's /v1/status (used for health and the leadership
// check on panel start).
func (c *Client) Status(ctx context.Context, agentURL string) (agentapi.StatusResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, agentURL+"/v1/status", nil)
	if err != nil {
		return agentapi.StatusResponse{}, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return agentapi.StatusResponse{}, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return agentapi.StatusResponse{}, fmt.Errorf("agent status %d: %s", resp.StatusCode, data)
	}
	var st agentapi.StatusResponse
	if err := json.Unmarshal(data, &st); err != nil {
		return agentapi.StatusResponse{}, err
	}
	return st, nil
}
