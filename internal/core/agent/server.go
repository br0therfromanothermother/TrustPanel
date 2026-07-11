package agent

import (
	"context"
	"encoding/json"
	"net/http"

	"trustpanel/internal/core/agentapi"
)

// StatusSource supplies live node info for GET /v1/status.
type StatusSource interface {
	Services(ctx context.Context) []agentapi.ServiceStatus
	InstalledVersions() map[string]string
}

// UserTrafficSource is an optional capability of a StatusSource: entry nodes
// that can reach a sing-box v2ray stats API report per-user byte counters here.
// It is consulted via a type assertion so non-entry status sources need not
// implement it.
type UserTrafficSource interface {
	UserTraffic(ctx context.Context) []agentapi.UserTrafficStat
}

// CertStatusSource is an optional capability of a StatusSource: entry nodes
// with an ACME-managed TrustTunnel cert report its expiry here. Consulted
// via type assertion so exits need not implement it.
type CertStatusSource interface {
	CertStatus() *agentapi.TLSCertStatus
}

// CertPromoter is an optional capability of a StatusSource: entry nodes with
// node-local ACME can switch from the staging directory to production and
// reissue on demand (POST /v1/acme/promote). Consulted via type assertion.
type CertPromoter interface {
	PromoteToProduction(ctx context.Context) error
}

// SystemSource is an optional capability of a StatusSource: every node reports
// live resource usage (CPU/mem/disk/net) for the Overview dashboard.
type SystemSource interface {
	SystemMetrics() *agentapi.SystemMetrics
}

// Server exposes the agent control API. TLS/mTLS is configured by the caller
// (see ControllerClientTLS); the handlers enforce the epoch fence via the
// reconciler.
type Server struct {
	nodeID    string
	reconcile *Reconciler
	store     *Store
	status    StatusSource
	standby   *StandbyConfig // nil unless add-standby provisioning is enabled
}

func NewServer(nodeID string, r *Reconciler, store *Store, status StatusSource) *Server {
	return &Server{nodeID: nodeID, reconcile: r, store: store, status: status}
}

// Handler returns the agent's HTTP mux.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /v1/desired-state", s.handleDesiredState)
	mux.HandleFunc("GET /v1/status", s.handleStatus)
	mux.HandleFunc("POST /v1/acme/promote", s.handlePromoteACME)
	mux.HandleFunc("POST /v1/standby/primary", s.handleStandbyPrimary)
	mux.HandleFunc("POST /v1/standby/replica", s.handleStandbyReplica)
	return mux
}

// handlePromoteACME switches the node's ACME issuance to Let's Encrypt production
// and reissues the cert. No-op-able: nodes without node-local ACME return 400.
func (s *Server) handlePromoteACME(w http.ResponseWriter, req *http.Request) {
	cp, ok := s.status.(CertPromoter)
	if !ok || cp == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "node has no ACME-managed certificate"})
		return
	}
	if err := cp.PromoteToProduction(req.Context()); err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "promoted"})
}

func (s *Server) handleDesiredState(w http.ResponseWriter, req *http.Request) {
	var ds agentapi.DesiredState
	if err := json.NewDecoder(http.MaxBytesReader(w, req.Body, 8<<20)).Decode(&ds); err != nil {
		writeJSON(w, http.StatusBadRequest, agentapi.ReconcileResult{
			Outcome: agentapi.OutcomeRejected, Error: "decode body: " + err.Error(),
		})
		return
	}
	res := s.reconcile.Apply(req.Context(), ds)
	writeJSON(w, statusForOutcome(res.Outcome), res)
}

func (s *Server) handleStatus(w http.ResponseWriter, req *http.Request) {
	resp := agentapi.StatusResponse{
		NodeID:            s.nodeID,
		LastAcceptedEpoch: s.store.LastAcceptedEpoch(),
		AppliedRevision:   s.store.AppliedRevision(),
		AppliedHash:       s.store.AppliedHash(),
	}
	if s.status != nil {
		resp.Services = s.status.Services(req.Context())
		resp.InstalledVersions = s.status.InstalledVersions()
		if uts, ok := s.status.(UserTrafficSource); ok {
			resp.UserTraffic = uts.UserTraffic(req.Context())
		}
		if cs, ok := s.status.(CertStatusSource); ok {
			resp.TLSCert = cs.CertStatus()
		}
		if ss, ok := s.status.(SystemSource); ok {
			resp.System = ss.SystemMetrics()
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

func statusForOutcome(o agentapi.Outcome) int {
	switch o {
	case agentapi.OutcomeApplied, agentapi.OutcomeNoChange:
		return http.StatusOK
	case agentapi.OutcomeStaleLeader:
		return http.StatusConflict // 409
	default: // rejected, rolled-back
		return http.StatusUnprocessableEntity // 422
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
