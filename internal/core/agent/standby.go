package agent

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"trustpanel/internal/core/agentapi"
	"trustpanel/internal/core/bootstrap"
	"trustpanel/internal/core/cluster"
	"trustpanel/internal/core/pki"
)

// errNoSystemdRun signals that systemd-run is unavailable, so the caller should
// run the privileged step in-process instead (dev / test environments).
var errNoSystemdRun = errors.New("systemd-run not available")

// runPrivileged executes `trustpanel cluster <op>` as a transient root unit via
// systemd-run, feeding payload to its stdin and returning its stdout. The agent
// runs in a strict systemd sandbox (ProtectSystem=strict, NoNewPrivileges), so it
// cannot itself wipe the Postgres data dir, run sudo -u postgres, edit pg_hba, or
// manage system units — the add-standby steps need all of that. systemd-run asks
// PID1 to spawn the work OUTSIDE the agent's sandbox as full root; the secret
// bundle travels the stdin pipe and never touches disk. This keeps the agent
// itself narrow for its normal declarative reconcile and escalates only for this
// one explicit, typed operation.
func runPrivileged(ctx context.Context, op string, payload []byte) (string, error) {
	if _, err := exec.LookPath("systemd-run"); err != nil {
		return "", errNoSystemdRun
	}
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	suffix, _ := randomHex(4)
	cmd := exec.CommandContext(ctx, "systemd-run",
		"--pipe", "--collect", "--quiet",
		"--unit=trustpanel-standby-"+strings.TrimPrefix(op, "_")+"-"+suffix,
		exe, "cluster", op)
	cmd.Stdin = bytes.NewReader(payload)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		return "", fmt.Errorf("%v: %s", err, msg)
	}
	return stdout.String(), nil
}

func lastLine(s string) string {
	lines := strings.Split(s, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if t := strings.TrimSpace(lines[i]); t != "" {
			return t
		}
	}
	return ""
}

// StandbyConfig enables the (privileged) add-standby handlers on this agent. It
// is set by RunAgent on every node; the handlers fail gracefully on nodes that
// lack the material they need (e.g. the primary handler needs the locally-held
// CA private key, which only the control-plane node has).
//
// This is the security-sensitive expansion of the agent's surface: unlike the
// narrow declarative reconcile, these handlers run a fixed, typed provisioning
// flow as root (Postgres replication setup, file staging, unit enable). They are
// still NOT an arbitrary command channel — they only run internal/core/cluster's
// vetted steps — and remain gated by the same mTLS + controller-role auth as
// every other agent endpoint.
type StandbyConfig struct {
	PKIDir   string // ca.crt, ca.key, controller.crt, controller.key
	Layout   bootstrap.Layout
	Validity time.Duration // standby controller cert lifetime
}

// EnableStandbyProvisioning turns on POST /v1/standby/{primary,replica}.
func (s *Server) EnableStandbyProvisioning(cfg StandbyConfig) {
	if cfg.Validity <= 0 {
		cfg.Validity = 90 * 24 * time.Hour
	}
	s.standby = &cfg
}

// handleStandbyPrimary runs the primary side of add-standby on the control-plane
// node (where this agent runs), then forwards the secret bundle to the standby's
// agent. Called by the panel over mTLS. The CA private key it reads locally goes
// only to the standby agent, never back to the panel.
func (s *Server) handleStandbyPrimary(w http.ResponseWriter, req *http.Request) {
	if s.standby == nil {
		writeJSON(w, http.StatusBadRequest, agentapi.PrepareStandbyResult{Error: "standby provisioning not enabled on this node"})
		return
	}
	var in agentapi.PrepareStandbyRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, req.Body, 1<<20)).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, agentapi.PrepareStandbyResult{Error: "decode body: " + err.Error()})
		return
	}
	// Defence in depth: the panel already required a human-formed confirmation;
	// reject a request whose confirm does not name the target standby.
	if in.Confirm == "" || in.Confirm != in.StandbyID {
		writeJSON(w, http.StatusUnprocessableEntity, agentapi.PrepareStandbyResult{Error: "confirm must equal standby_id"})
		return
	}
	if in.StandbyID == in.PrimaryID || in.StandbyAddr == "" || in.PrimaryIP == "" || in.StandbyIP == "" {
		writeJSON(w, http.StatusUnprocessableEntity, agentapi.PrepareStandbyResult{Error: "primary/standby ids, ips and standby_addr are required and must differ"})
		return
	}

	res, err := s.prepareStandbyPrimary(req.Context(), *s.standby, in)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, agentapi.PrepareStandbyResult{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) prepareStandbyPrimary(ctx context.Context, cfg StandbyConfig, in agentapi.PrepareStandbyRequest) (agentapi.PrepareStandbyResult, error) {
	l := cfg.Layout
	// Load the locally-held CA (only the control-plane node has ca.key 0600).
	caCert, err := os.ReadFile(cfg.PKIDir + "/ca.crt")
	if err != nil {
		return agentapi.PrepareStandbyResult{}, fmt.Errorf("read ca.crt: %w", err)
	}
	caKey, err := os.ReadFile(cfg.PKIDir + "/ca.key")
	if err != nil {
		return agentapi.PrepareStandbyResult{}, fmt.Errorf("read ca.key (is this the control-plane node?): %w", err)
	}
	ca, err := pki.LoadCA(caCert, caKey)
	if err != nil {
		return agentapi.PrepareStandbyResult{}, fmt.Errorf("load CA: %w", err)
	}

	replUser := in.ReplUser
	if replUser == "" {
		replUser = "replicator"
	}
	replPass, err := randomHex(24)
	if err != nil {
		return agentapi.PrepareStandbyResult{}, fmt.Errorf("generate replication password: %w", err)
	}
	slot := cluster.SlotName(in.StandbyID)
	sans := parseIPs(in.StandbyIPs)

	ctrlCert, ctrlKey, err := ca.IssueLeaf(pki.RoleController, in.StandbyID+".controller", sans, cfg.Validity)
	if err != nil {
		return agentapi.PrepareStandbyResult{}, fmt.Errorf("issue controller cert: %w", err)
	}

	apply := agentapi.PrimaryApplyInput{
		PrimaryID: in.PrimaryID, StandbyID: in.StandbyID,
		PrimaryIP: in.PrimaryIP, StandbyIP: in.StandbyIP,
		ReplUser: replUser, ReplPass: replPass, ReplSlot: slot,
	}
	if err := s.applyPrimary(ctx, l, apply); err != nil {
		return agentapi.PrepareStandbyResult{}, fmt.Errorf("primary-side: %w", err)
	}

	// serve.env / deployment.env are read locally (the standby gets a
	// byte-identical config; its DSN points at 127.0.0.1 = its own replica).
	serveEnv, _ := os.ReadFile(l.EtcDir + "/serve.env")
	deployEnv, _ := os.ReadFile(l.EtcDir + "/deployment.env")

	bundle := agentapi.PrepareReplicaRequest{
		PrimaryID: in.PrimaryID, StandbyID: in.StandbyID,
		PrimaryIP: in.PrimaryIP, StandbyIP: in.StandbyIP,
		ReplUser: replUser, ReplPass: replPass, ReplSlot: slot,
		ServeEnv:  base64.StdEncoding.EncodeToString(serveEnv),
		DeployEnv: base64.StdEncoding.EncodeToString(deployEnv),
		CACertPEM: base64.StdEncoding.EncodeToString(ca.CertPEM()),
		CAKeyPEM:  base64.StdEncoding.EncodeToString(caKey),
		CtrlCert:  base64.StdEncoding.EncodeToString(ctrlCert),
		CtrlKey:   base64.StdEncoding.EncodeToString(ctrlKey),
		Confirm:   in.StandbyID,
	}
	if err := s.pushReplicaBundle(ctx, cfg, caCert, in.StandbyAddr, bundle); err != nil {
		return agentapi.PrepareStandbyResult{}, fmt.Errorf("standby-side (agent %s): %w", in.StandbyAddr, err)
	}

	repl := s.verifyPrimary(ctx)
	return agentapi.PrepareStandbyResult{OK: true, ReplSlot: slot, Replication: repl}, nil
}

// applyPrimary runs ConfigurePrimary outside the agent sandbox (transient root
// unit), falling back to in-process when systemd-run is absent (dev/test).
func (s *Server) applyPrimary(ctx context.Context, l bootstrap.Layout, in agentapi.PrimaryApplyInput) error {
	payload, err := json.Marshal(in)
	if err != nil {
		return err
	}
	if _, err := runPrivileged(ctx, "_primary-apply", payload); err != nil {
		if errors.Is(err, errNoSystemdRun) {
			return cluster.ConfigurePrimary(ctx, cluster.LocalRunner{}, cluster.ParamsFromPrimaryApply(in, l), nil)
		}
		return err
	}
	return nil
}

// verifyPrimary returns the primary's pg_stat_replication one-liner (best effort).
func (s *Server) verifyPrimary(ctx context.Context) string {
	out, err := runPrivileged(ctx, "_primary-verify", nil)
	if errors.Is(err, errNoSystemdRun) {
		out, _ = cluster.Verify(ctx, cluster.LocalRunner{})
		return out
	}
	if err != nil {
		return ""
	}
	return lastLine(out)
}

// pushReplicaBundle calls the standby agent's /v1/standby/replica over mTLS,
// presenting the control-plane's controller cert. The CA private key in the
// bundle therefore flows only primary-agent -> standby-agent, never via the
// panel.
func (s *Server) pushReplicaBundle(ctx context.Context, cfg StandbyConfig, caPEM []byte, standbyAddr string, bundle agentapi.PrepareReplicaRequest) error {
	cert, err := tls.LoadX509KeyPair(cfg.PKIDir+"/controller.crt", cfg.PKIDir+"/controller.key")
	if err != nil {
		return fmt.Errorf("load controller cert (needed to reach the standby agent): %w", err)
	}
	tlsCfg, err := outboundControllerTLS(caPEM, cert)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 10 * time.Minute, Transport: &http.Transport{TLSClientConfig: tlsCfg}}

	body, err := json.Marshal(bundle)
	if err != nil {
		return err
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+standbyAddr+"/v1/standby/replica", bytes.NewReader(body))
	if err != nil {
		return err
	}
	r.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(r)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var out agentapi.PrepareReplicaResult
	if err := json.Unmarshal(data, &out); err != nil {
		return fmt.Errorf("standby agent returned %d: %s", resp.StatusCode, data)
	}
	if !out.OK {
		if out.Error != "" {
			return fmt.Errorf("%s", out.Error)
		}
		return fmt.Errorf("standby agent returned %d", resp.StatusCode)
	}
	return nil
}

// handleStandbyReplica runs the standby side locally: it is the standby's own
// root agent receiving the secret bundle from the primary agent. The bundle is
// never seen by the panel.
func (s *Server) handleStandbyReplica(w http.ResponseWriter, req *http.Request) {
	if s.standby == nil {
		writeJSON(w, http.StatusBadRequest, agentapi.PrepareReplicaResult{Error: "standby provisioning not enabled on this node"})
		return
	}
	var in agentapi.PrepareReplicaRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, req.Body, 4<<20)).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, agentapi.PrepareReplicaResult{Error: "decode body: " + err.Error()})
		return
	}
	if in.Confirm == "" || in.Confirm != in.StandbyID {
		writeJSON(w, http.StatusUnprocessableEntity, agentapi.PrepareReplicaResult{Error: "confirm must equal standby_id"})
		return
	}
	if err := s.prepareReplica(req.Context(), *s.standby, in); err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, agentapi.PrepareReplicaResult{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, agentapi.PrepareReplicaResult{OK: true})
}

func (s *Server) prepareReplica(ctx context.Context, cfg StandbyConfig, in agentapi.PrepareReplicaRequest) error {
	payload, err := json.Marshal(in)
	if err != nil {
		return err
	}
	if _, err := runPrivileged(ctx, "_replica-apply", payload); err != nil {
		if errors.Is(err, errNoSystemdRun) {
			return cluster.ApplyReplica(ctx, cluster.LocalRunner{}, in, cfg.Layout, nil)
		}
		return err
	}
	return nil
}

// outboundControllerTLS builds a client TLS config presenting the controller
// cert and verifying the peer agent's node-role cert against the fleet CA. It
// mirrors controller.BuildControllerTLSConfig but is kept here so the agent does
// not import the controller package (which imports agent in its tests).
func outboundControllerTLS(caPEM []byte, clientCert tls.Certificate) (*tls.Config, error) {
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
			if len(rawCerts) == 0 {
				return fmt.Errorf("no peer certificate")
			}
			leaf, err := x509.ParseCertificate(rawCerts[0])
			if err != nil {
				return err
			}
			inter := x509.NewCertPool()
			for _, raw := range rawCerts[1:] {
				if c, e := x509.ParseCertificate(raw); e == nil {
					inter.AddCert(c)
				}
			}
			if _, err := leaf.Verify(x509.VerifyOptions{Roots: pool, Intermediates: inter,
				KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err != nil {
				return err
			}
			if !hasRole(leaf, RoleNode) {
				return fmt.Errorf("standby agent cert is not a node (OU=%v)", leaf.Subject.OrganizationalUnit)
			}
			return nil
		},
	}, nil
}

func parseIPs(ss []string) []net.IP {
	out := make([]net.IP, 0, len(ss))
	for _, s := range ss {
		if ip := net.ParseIP(s); ip != nil {
			out = append(out, ip)
		}
	}
	return out
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
