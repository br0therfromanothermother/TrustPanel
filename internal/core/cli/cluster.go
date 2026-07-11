package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strings"
	"time"

	"trustpanel/internal/core/agentapi"
	"trustpanel/internal/core/bootstrap"
	"trustpanel/internal/core/cluster"
	"trustpanel/internal/core/model"
	"trustpanel/internal/core/pki"
	"trustpanel/internal/core/provision"
	"trustpanel/internal/core/store"
)

// RunCluster dispatches `trustpanel cluster <subcommand>`. Today the only
// subcommand is add-standby, which prepares a registered exit node to become a
// control-plane standby (a Postgres streaming replica + CA-key holder + a staged,
// disabled serve unit + an enabled watchdog) so a later `promote` can bring the
// panel up on it. It is run as root on the existing primary.
//
// This is the break-glass path: the panel's UI button drives the same work
// through the per-node root agents (see internal/core/agent/standby.go). The CLI
// stays because during a failover the panel may be down — root on the box is the
// reliable recovery path. Both share internal/core/cluster.
func RunCluster(args []string) {
	if len(args) < 1 {
		clusterUsage()
	}
	switch args[0] {
	case "add-standby":
		runAddStandby(args[1:])
	// Hidden privileged helpers invoked by the per-node agent via systemd-run as a
	// transient root unit (escaping the agent's strict sandbox). They read their
	// payload from stdin and are not part of the public CLI surface — see
	// internal/core/agent/standby.go.
	case "_primary-apply":
		runPrimaryApply()
	case "_primary-verify":
		runPrimaryVerify()
	case "_replica-apply":
		runReplicaApply()
	case "-h", "--help", "help":
		clusterUsage()
	default:
		fmt.Fprintln(os.Stderr, "cluster: unknown subcommand "+args[0])
		clusterUsage()
	}
}

// runPrimaryApply runs ConfigurePrimary from a PrimaryApplyInput on stdin. Step
// logs go to stderr; stdout is left clean.
func runPrimaryApply() {
	var in agentapi.PrimaryApplyInput
	if err := json.NewDecoder(os.Stdin).Decode(&in); err != nil {
		fmt.Fprintln(os.Stderr, "primary-apply: decode stdin:", err)
		os.Exit(1)
	}
	p := cluster.ParamsFromPrimaryApply(in, bootstrap.DefaultLayout())
	if err := cluster.ConfigurePrimary(context.Background(), cluster.LocalRunner{}, p, logToStderr); err != nil {
		fmt.Fprintln(os.Stderr, "primary-apply:", err)
		os.Exit(1)
	}
}

// runPrimaryVerify prints the primary's pg_stat_replication one-liner to stdout.
func runPrimaryVerify() {
	out, err := cluster.Verify(context.Background(), cluster.LocalRunner{})
	if err != nil {
		fmt.Fprintln(os.Stderr, "primary-verify:", err)
		os.Exit(1)
	}
	fmt.Println(out)
}

// runReplicaApply runs MakeReplica+StageControlPlane from a PrepareReplicaRequest
// (the secret bundle) on stdin. Step logs go to stderr.
func runReplicaApply() {
	var in agentapi.PrepareReplicaRequest
	if err := json.NewDecoder(io.LimitReader(os.Stdin, 8<<20)).Decode(&in); err != nil {
		fmt.Fprintln(os.Stderr, "replica-apply: decode stdin:", err)
		os.Exit(1)
	}
	if err := cluster.ApplyReplica(context.Background(), cluster.LocalRunner{}, in, bootstrap.DefaultLayout(), logToStderr); err != nil {
		fmt.Fprintln(os.Stderr, "replica-apply:", err)
		os.Exit(1)
	}
}

func logToStderr(s string) { fmt.Fprintln(os.Stderr, s) }

func clusterUsage() {
	fmt.Fprintln(os.Stderr, "usage: trustpanel cluster add-standby --node-id <id> [--standby-ssh-host <host>] [--standby-ssh-key <file>] [flags]")
	os.Exit(2)
}

func runAddStandby(args []string) {
	fs := flag.NewFlagSet("cluster add-standby", flag.ExitOnError)
	dsn := fs.String("dsn", "", "primary Postgres DSN (default: TRUSTPANEL_DSN, else read /etc/trustpanel/serve.env)")
	nodeID := fs.String("node-id", "", "registered exit node id to make a standby (required)")
	primaryID := fs.String("primary-id", "", "primary (active) node id (default: control_plane.active_node_id)")
	primaryIP := fs.String("primary-ip", "", "primary IP the standby replicates from (default: primary node's first public IP)")
	standbyIP := fs.String("standby-ip", "", "standby IP to allow in pg_hba/ufw (default: standby node's first public IP)")
	sshHost := fs.String("standby-ssh-host", "", "standby SSH host (default: standby IP)")
	sshUser := fs.String("standby-ssh-user", "user", "standby SSH user (passwordless sudo)")
	sshPort := fs.Int("standby-ssh-port", 3222, "standby SSH port")
	sshKey := fs.String("standby-ssh-key", "", "standby SSH private key file (empty: use the SSH agent, e.g. a forwarded agent — keeps the key off this host)")
	knownHosts := fs.String("known-hosts", "/var/lib/trustpanel/known_hosts", "known_hosts file for host-key pinning")
	pkiDir := fs.String("pki-dir", "/etc/trustpanel/pki", "local PKI dir (reads ca.crt + ca.key)")
	tpBin := fs.String("trustpanel-bin", "", "trustpanel binary to push to the standby (default: this executable)")
	replUser := fs.String("repl-user", "replicator", "Postgres replication role to create on the primary")
	replPass := fs.String("repl-password", "", "replication role password (generated if empty)")
	validity := fs.Duration("cert-validity", 90*24*time.Hour, "standby controller cert lifetime")
	_ = fs.Parse(args)

	if strings.TrimSpace(*nodeID) == "" {
		log.Fatal("cluster add-standby: --node-id is required")
	}

	ctx := context.Background()
	prim := localRunner{}

	// Preflight: must run as root on the primary (sudo -u postgres, pg_hba edits).
	if out, err := prim.Run(ctx, "id -u"); err != nil || strings.TrimSpace(out) != "0" {
		log.Fatalf("cluster add-standby: must run as root on the primary (id -u = %q)", strings.TrimSpace(out))
	}

	connDSN, serveEnv, deployEnv := resolveDSN(*dsn)
	if connDSN == "" {
		log.Fatal("cluster add-standby: no DSN (pass --dsn, set TRUSTPANEL_DSN, or run where /etc/trustpanel/serve.env exists)")
	}
	if len(serveEnv) == 0 {
		// Synthesize from the DSN so the standby still gets a serve.env. Quote the
		// value (see serveEnv in bootstrap) so a manual `source` keeps it intact.
		serveEnv = []byte("TRUSTPANEL_DSN=\"" + connDSN + "\"\n")
	}

	st, err := store.Open(ctx, connDSN)
	if err != nil {
		log.Fatalf("cluster add-standby: open store: %v", err)
	}
	defer st.Close()

	sb := provision.SSHRunner{
		User: *sshUser, Port: *sshPort, KeyFile: *sshKey,
		KnownHosts: *knownHosts, ConnectTimeout: 15 * time.Second,
	}
	if err := runStandbyFlow(ctx, st, prim, sb, *nodeID, standbyFlowOpts{
		primaryID: *primaryID, primaryIP: *primaryIP, standbyIP: *standbyIP,
		sshHost: *sshHost, pkiDir: *pkiDir, binPath: *tpBin,
		replUser: *replUser, replPass: *replPass, validity: *validity,
		serveEnv: serveEnv, deployEnv: deployEnv,
	}); err != nil {
		log.Fatalf("cluster add-standby: %v", err)
	}
}

// standbyFlowOpts carries the resolved inputs to runStandbyFlow; most are
// optional (defaults derived from state/layout/this executable).
type standbyFlowOpts struct {
	primaryID, primaryIP, standbyIP string
	sshHost                         string // SSH host (default: standby IP)
	pkiDir                          string // local CA dir (ca.crt + ca.key)
	binPath                         string // trustpanel binary to push (default: this exe)
	replUser, replPass              string
	validity                        time.Duration
	serveEnv, deployEnv             []byte
}

// runStandbyFlow turns the already-registered exit nodeID into a control-plane
// standby: resolve primary + IPs from state, load the CA, mint the standby
// controller cert, read the binary/trusttunnel, then run cluster.AddStandby over
// the SSH runner sb (this fn pins its host key). prim is the local primary
// command runner. Shared by `cluster add-standby` and `provision --make-standby`.
func runStandbyFlow(ctx context.Context, st *store.Store, prim cluster.CmdRunner, sb provision.SSHRunner, nodeID string, o standbyFlowOpts) error {
	state, err := st.LoadState(ctx)
	if err != nil {
		return fmt.Errorf("load state: %w", err)
	}
	pid := o.primaryID
	if pid == "" {
		pid = state.ControlPlane.ActiveNodeID
	}
	if pid == "" {
		pid = soleMgmtCapable(state, nodeID)
	}
	primNode, ok := state.NodeByID(pid)
	if !ok {
		return fmt.Errorf("primary node %q not found (pass --primary-id)", pid)
	}
	sbNode, ok := state.NodeByID(nodeID)
	if !ok {
		return fmt.Errorf("standby node %q not registered — register it first", nodeID)
	}
	if pid == nodeID {
		return fmt.Errorf("primary and standby must differ")
	}
	pip := firstNonEmpty(o.primaryIP, firstIP(primNode.PublicIPs))
	sip := firstNonEmpty(o.standbyIP, firstIP(sbNode.PublicIPs))
	if pip == "" || sip == "" {
		return fmt.Errorf("could not resolve IPs (primary=%q standby=%q); pass --primary-ip/--standby-ip", pip, sip)
	}

	pkiDir := firstNonEmpty(o.pkiDir, "/etc/trustpanel/pki")
	caCert, err := os.ReadFile(pkiDir + "/ca.crt")
	if err != nil {
		return fmt.Errorf("read ca.crt: %w", err)
	}
	caKey, err := os.ReadFile(pkiDir + "/ca.key")
	if err != nil {
		return fmt.Errorf("read ca.key: %w", err)
	}
	ca, err := pki.LoadCA(caCert, caKey)
	if err != nil {
		return fmt.Errorf("load CA: %w", err)
	}

	binPath := o.binPath
	if binPath == "" {
		exe, e := os.Executable()
		if e != nil {
			return fmt.Errorf("cannot find own binary: %w", e)
		}
		binPath = exe
	}
	binary, err := os.ReadFile(binPath)
	if err != nil {
		return fmt.Errorf("read trustpanel binary %q: %w", binPath, err)
	}

	l := bootstrap.DefaultLayout()
	var trusttunnel []byte
	if b, e := os.ReadFile(l.ShareDir + "/trusttunnel_endpoint"); e == nil {
		trusttunnel = b
	}

	validity := o.validity
	if validity <= 0 {
		validity = 90 * 24 * time.Hour
	}
	sbIPs := append(parseIPList(strings.Join(sbNode.PublicIPs, ",")), net.ParseIP("127.0.0.1"))
	ctrlCert, ctrlKey, err := ca.IssueLeaf(pki.RoleController, nodeID+".controller", sbIPs, validity)
	if err != nil {
		return fmt.Errorf("issue controller cert: %w", err)
	}

	sb.Host = firstNonEmpty(o.sshHost, sip)
	if sb.KnownHosts != "" {
		if err := sb.EnsureHostKey(ctx); err != nil {
			return fmt.Errorf("pin standby host key: %w", err)
		}
	}

	replUser := strings.TrimSpace(o.replUser)
	if replUser == "" {
		replUser = "replicator"
	}
	serveEnv := o.serveEnv
	if len(serveEnv) == 0 {
		serveEnv = []byte("TRUSTPANEL_DSN=\"host=127.0.0.1 port=5432 sslmode=disable\"\n")
	}

	p := cluster.Params{
		PrimaryID: pid, StandbyID: nodeID,
		PrimaryIP: pip, StandbyIP: sip,
		StandbyIPs:     sbIPs,
		ReplUser:       replUser,
		ReplPass:       orRandom(o.replPass, 24),
		ReplSlot:       cluster.SlotName(nodeID),
		ServeEnv:       serveEnv,
		DeployEnv:      o.deployEnv,
		CACertPEM:      ca.CertPEM(),
		CAKeyPEM:       caKey,
		ControllerCert: ctrlCert,
		ControllerKey:  ctrlKey,
		Binary:         binary,
		TrustTunnel:    trusttunnel,
		Layout:         l,
		Validity:       validity,
	}
	if err := cluster.AddStandby(ctx, prim, sb, st, p, func(s string) { log.Println("add-standby:", s) }); err != nil {
		return err
	}
	fmt.Print(cluster.Summary(p))
	return nil
}

// readEnvFiles reads the primary's serve.env + deployment.env (best effort) so
// the standby gets a byte-identical config (the DSN points at 127.0.0.1, which
// on the standby is its own local replica — correct after promote).
func readEnvFiles() (serveEnv, deployEnv []byte) {
	serveEnv, _ = os.ReadFile("/etc/trustpanel/serve.env")
	deployEnv, _ = os.ReadFile("/etc/trustpanel/deployment.env")
	return serveEnv, deployEnv
}

func dsnFromEnv(env []byte) string {
	for _, line := range strings.Split(string(env), "\n") {
		line = strings.TrimSpace(line)
		if v, ok := strings.CutPrefix(line, "TRUSTPANEL_DSN="); ok {
			return unquoteEnvValue(strings.TrimSpace(v))
		}
	}
	return ""
}

// unquoteEnvValue strips a single pair of matched surrounding quotes, matching
// how systemd's EnvironmentFile parser treats a quoted value. serve.env now
// quotes the DSN so a manual `source serve.env` keeps the space-containing
// keyword value intact instead of word-splitting it to `host=127.0.0.1`.
func unquoteEnvValue(v string) string {
	if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
		return v[1 : len(v)-1]
	}
	return v
}

// connDSN resolves the control-plane DSN from, in order: an explicit value
// (a --dsn flag), $TRUSTPANEL_DSN, then TRUSTPANEL_DSN= in the local
// /etc/trustpanel/serve.env. Returns "" when none yields one; callers fatal in
// their own idiom. This is the resolution every CLI subcommand that needs the DB
// shares (serve, bot, cluster, provision) so a box with serve.env staged works
// without an exported env var.
func connDSN(explicit string) string {
	if explicit != "" {
		return explicit
	}
	if v := os.Getenv("TRUSTPANEL_DSN"); v != "" {
		return v
	}
	serveEnv, _ := readEnvFiles()
	return dsnFromEnv(serveEnv)
}

// resolveDSN is connDSN plus the primary's serve.env and deployment.env (best
// effort), for the callers that go on to replicate those files to a new standby —
// add-standby and provision --make-standby.
func resolveDSN(explicit string) (dsn string, serveEnv, deployEnv []byte) {
	serveEnv, deployEnv = readEnvFiles()
	dsn = explicit
	if dsn == "" {
		dsn = os.Getenv("TRUSTPANEL_DSN")
	}
	if dsn == "" {
		dsn = dsnFromEnv(serveEnv)
	}
	return dsn, serveEnv, deployEnv
}

func soleMgmtCapable(state model.State, exclude string) string {
	var found string
	for _, n := range state.Nodes {
		if n.MgmtCapable && n.ID != exclude {
			if found != "" {
				return "" // ambiguous
			}
			found = n.ID
		}
	}
	return found
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}
