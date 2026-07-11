// Package agentapi is the wire contract between the panel (controller) and the
// per-node agent. It holds only plain data types and no runtime dependencies,
// so the panel can construct desired-state without importing the agent runtime.
package agentapi

import "time"

// Service want-states.
const (
	WantRunning = "running"
	WantStopped = "stopped"
)

// Check kinds the agent knows how to run after writing files and before
// restarting services.
const (
	CheckSingBox     = "singbox-check"      // sing-box check -c <path>
	CheckTrustTunnel = "trusttunnel-export" // non-listening trusttunnel_endpoint export
)

// File is one config file in a desired-state push. Path must resolve inside one
// of the agent's allowlisted roots. Mode is the unix file mode.
type File struct {
	Path   string `json:"path"`
	Mode   uint32 `json:"mode"`
	SHA256 string `json:"sha256"` // hex sha256 of the DECODED content
	Body   string `json:"body"`
	// Encoding of Body: "" (raw text, default) or "base64" for binary files such
	// as sing-box rule-set (.srs). SHA256 is always over the decoded bytes.
	Encoding string `json:"encoding,omitempty"`
}

// Service is a systemd unit the agent manages, restricted to the agent's
// service allowlist.
type Service struct {
	Name string `json:"name"`
	Want string `json:"want"` // WantRunning | WantStopped
}

// Check is a validation step run after writing files, before restarting
// services. Path is the config file to validate (relative to the apply root).
type Check struct {
	Kind string `json:"kind"`
	Path string `json:"path"`
}

// DesiredState is the controller's pushed desired state for one node. Epoch
// fences stale controllers; RevisionID is monotonic; RevisionHash gives
// idempotency.
type DesiredState struct {
	Epoch        int64     `json:"epoch"`
	NodeID       string    `json:"node_id"`
	RevisionID   int64     `json:"revision_id"`
	RevisionHash string    `json:"revision_hash"`
	IssuedAt     time.Time `json:"issued_at"`
	Files        []File    `json:"files"`
	Services     []Service `json:"services"`
	Checks       []Check   `json:"checks"`
}

// Outcome enumerates the result of a reconcile.
type Outcome string

const (
	OutcomeApplied     Outcome = "applied"      // files changed and applied
	OutcomeNoChange    Outcome = "no-change"    // revision already applied (idempotent)
	OutcomeStaleLeader Outcome = "stale-leader" // epoch < last accepted (HTTP 409)
	OutcomeRejected    Outcome = "rejected"     // validation/precondition failure (HTTP 422)
	OutcomeRolledBack  Outcome = "rolled-back"  // checks/restart failed, files restored (HTTP 422)
)

// ReconcileResult is returned from PUT /v1/desired-state.
type ReconcileResult struct {
	Outcome           Outcome  `json:"outcome"`
	Changed           bool     `json:"changed"`
	Restarted         []string `json:"restarted,omitempty"`
	AppliedRevision   int64    `json:"applied_revision"`
	LastAcceptedEpoch int64    `json:"last_accepted_epoch"`
	Error             string   `json:"error,omitempty"`
}

// ServiceStatus is a unit's observed state reported in GET /v1/status.
type ServiceStatus struct {
	Name  string `json:"name"`
	State string `json:"state"` // active|inactive|failed|unknown
}

// UserTrafficStat is one user's cumulative byte counters as reported by the
// entry node's sing-box v2ray stats API (since sing-box start). The panel turns
// these absolute readings into accumulated deltas. Uplink is client→server
// bytes; Downlink is server→client.
type UserTrafficStat struct {
	Username      string `json:"username"`
	UplinkBytes   int64  `json:"uplink_bytes"`
	DownlinkBytes int64  `json:"downlink_bytes"`
}

// StatusResponse is returned from GET /v1/status. A (re)starting panel reads
// LastAcceptedEpoch here for its leadership check.
type StatusResponse struct {
	NodeID            string            `json:"node_id"`
	LastAcceptedEpoch int64             `json:"last_accepted_epoch"`
	AppliedRevision   int64             `json:"applied_revision"`
	AppliedHash       string            `json:"applied_hash"`
	Services          []ServiceStatus   `json:"services,omitempty"`
	InstalledVersions map[string]string `json:"installed_versions,omitempty"`
	// UserTraffic is per-user cumulative byte counters from the entry node's
	// sing-box v2ray stats API. Omitted when the stats API is unreachable or the
	// node is not an entry (degrade gracefully — never fail status on its account).
	UserTraffic []UserTrafficStat `json:"user_traffic,omitempty"`
	// TLSCert reports the entry node's ACME-managed TrustTunnel certificate. The
	// agent owns issuance/renewal locally; the panel only reads expiry here
	// to surface in Domain.tls_status. Omitted on exits or before first issuance.
	TLSCert *TLSCertStatus `json:"tls_cert,omitempty"`
	// System is the node's live resource usage for the Overview dashboard.
	System *SystemMetrics `json:"system,omitempty"`
}

// PrepareStandbyRequest is sent by the panel (non-root serve) to the PRIMARY
// node's agent to provision a new control-plane standby. It carries NO secrets:
// the primary agent does the primary-side Postgres work locally, mints the
// standby's controller cert from the locally-held CA, and forwards the secret
// bundle (incl. the CA private key) straight to the standby's agent over mTLS —
// so the crown jewel never transits the panel process. Confirm must equal
// StandbyID (a human-formed value carried from the UI's "are you sure?" step).
type PrepareStandbyRequest struct {
	PrimaryID   string   `json:"primary_id"`
	StandbyID   string   `json:"standby_id"`
	PrimaryIP   string   `json:"primary_ip"`
	StandbyIP   string   `json:"standby_ip"`
	StandbyIPs  []string `json:"standby_ips"`  // standby controller cert SANs
	StandbyAddr string   `json:"standby_addr"` // host:port of the standby's agent
	ReplUser    string   `json:"repl_user"`
	Confirm     string   `json:"confirm"`
}

// PrepareStandbyResult is returned from POST /v1/standby/primary.
type PrepareStandbyResult struct {
	OK          bool   `json:"ok"`
	ReplSlot    string `json:"repl_slot,omitempty"`
	Replication string `json:"replication,omitempty"` // primary pg_stat_replication, one line
	Error       string `json:"error,omitempty"`
}

// PrepareReplicaRequest is the agent->agent (primary -> standby) bundle carrying
// the secrets the standby needs to become a streaming replica and a future
// control plane. It is NEVER seen by the panel. Byte fields are base64 (raw text
// envs too, for a single uniform decode path).
type PrepareReplicaRequest struct {
	PrimaryID string `json:"primary_id"`
	StandbyID string `json:"standby_id"`
	PrimaryIP string `json:"primary_ip"`
	StandbyIP string `json:"standby_ip"`
	ReplUser  string `json:"repl_user"`
	ReplPass  string `json:"repl_pass"`
	ReplSlot  string `json:"repl_slot"`
	ServeEnv  string `json:"serve_env"`   // base64
	DeployEnv string `json:"deploy_env"`  // base64 (may be empty)
	CACertPEM string `json:"ca_cert_pem"` // base64
	CAKeyPEM  string `json:"ca_key_pem"`  // base64, crown jewel
	CtrlCert  string `json:"ctrl_cert"`   // base64, standby's controller cert
	CtrlKey   string `json:"ctrl_key"`    // base64, standby's controller key
	Confirm   string `json:"confirm"`
}

// PrepareReplicaResult is returned from POST /v1/standby/replica.
type PrepareReplicaResult struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// PrimaryApplyInput is the small payload the primary agent pipes to the
// privileged helper (`trustpanel cluster _primary-apply`) over stdin. The agent
// itself is a strict systemd sandbox (ProtectSystem=strict, NoNewPrivileges) and
// cannot run `sudo -u postgres`, edit pg_hba, or restart postgres; the helper
// runs as a transient root unit spawned by PID1, outside that sandbox. No secret
// here beyond the freshly-generated replication password (also sent to the
// standby in the replica bundle).
type PrimaryApplyInput struct {
	PrimaryID string `json:"primary_id"`
	StandbyID string `json:"standby_id"`
	PrimaryIP string `json:"primary_ip"`
	StandbyIP string `json:"standby_ip"`
	ReplUser  string `json:"repl_user"`
	ReplPass  string `json:"repl_pass"`
	ReplSlot  string `json:"repl_slot"`
}

// SystemMetrics is the node's live resource usage (read from /proc + statfs),
// reported by every agent for the Overview dashboard. NetRx/TxBytes are absolute
// since-boot interface counters (the panel accumulates monthly deltas).
type SystemMetrics struct {
	CPUCores    int     `json:"cpu_cores"`
	Load1       float64 `json:"load1"`
	MemUsedMB   int64   `json:"mem_used_mb"`
	MemTotalMB  int64   `json:"mem_total_mb"`
	DiskUsedGB  int64   `json:"disk_used_gb"`
	DiskTotalGB int64   `json:"disk_total_gb"`
	NetRxBytes  int64   `json:"net_rx_bytes"`
	NetTxBytes  int64   `json:"net_tx_bytes"`
	UptimeSec   int64   `json:"uptime_sec"`
}

// TLSCertStatus is the observed state of the entry node's TrustTunnel cert.
type TLSCertStatus struct {
	Domains   []string   `json:"domains,omitempty"`
	NotAfter  *time.Time `json:"not_after,omitempty"`
	Issuer    string     `json:"issuer,omitempty"`     // leaf issuer label, e.g. "(STAGING) Let's Encrypt"
	LastError string     `json:"last_error,omitempty"` // last issuance/renewal error, if any
}
