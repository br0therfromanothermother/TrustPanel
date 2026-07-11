package cli

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"strings"
	"time"

	"trustpanel/internal/core/idgen"
	"trustpanel/internal/core/model"
	"trustpanel/internal/core/provision"
	"trustpanel/internal/core/store"
)

// RunProvision onboards a fresh node over SSH: enroll (CSR signed by the fleet
// CA), upload binaries, install units, enable+start. SSH is used only here.
func RunProvision(args []string) {
	fs := flag.NewFlagSet("provision", flag.ExitOnError)
	caDir := fs.String("ca", "/etc/trustpanel/pki", "dir with ca.crt/ca.key")
	sshHost := fs.String("ssh-host", "", "node SSH host/IP")
	sshUser := fs.String("ssh-user", "root", "node SSH user")
	sshPort := fs.Int("ssh-port", 22, "node SSH port")
	sshKey := fs.String("ssh-key", "", "SSH private key file")
	knownHosts := fs.String("known-hosts", "", "known_hosts file for host-key pinning")
	nodeID := fs.String("node-id", "", "node id (matches the panel)")
	role := fs.String("role", "", "node role: entry|exit")
	ipsCSV := fs.String("ips", "", "comma-separated node public IPs (node cert SANs)")
	tpBin := fs.String("trustpanel-bin", "", "local trustpanel binary to upload")
	sbBin := fs.String("singbox-bin", "", "local sing-box binary to upload")
	ttBin := fs.String("trusttunnel-bin", "", "local trusttunnel_endpoint binary (entry only)")
	unitsDir := fs.String("units-dir", "deploy/systemd", "dir with systemd unit files")
	refreshUnits := fs.Bool("refresh-units", false, "upgrade an already-enrolled node: re-push binaries/units/agent.env, daemon-reload and restart — no re-enrollment, keeps the agent's leadership state")
	makeStandby := fs.Bool("make-standby", false, "after install, also make this exit a control-plane standby (HA); requires --role exit + --dsn, run on the primary as root")
	dsn := fs.String("dsn", "", "primary Postgres DSN for --make-standby (default: TRUSTPANEL_DSN or /etc/trustpanel/serve.env)")
	realitySNI := fs.String("reality-sni", "", "borrowed SNI for the exit's Reality dial-in (required with --make-standby)")
	// Entry-only: bring a CLI-provisioned entry up fully configured (node-local
	// ACME + camouflage brand + DB registration), mirroring the panel installer.
	domain := fs.String("domain", "", "entry: deployment apex domain (camouflage brand + apex-fallback detection)")
	entryDomain := fs.String("entry-domain", "", "entry: this node's TLS hostname (equal to --domain ⇒ portal+apex split)")
	brand := fs.String("brand", "", "entry: fallback-site brand name")
	connectSub := fs.String("connect-subdomain", "vpn", "entry: login subdomain for the VPN endpoint when --entry-domain is the apex")
	acmeEmail := fs.String("acme-email", "", "entry: ACME contact (default admin@<domain>)")
	acmeStaging := fs.Bool("acme-staging", false, "entry: issue the TLS cert from Let's Encrypt staging (flip to production later)")
	nodeName := fs.String("node-name", "", "entry: display name (default node-id)")
	_ = fs.Parse(args)

	pr := model.PublicRole(*role)
	if *sshHost == "" || !pr.Valid() {
		log.Fatal("provision: --ssh-host and --role (entry|exit) are required")
	}
	// node-id is auto-generated for a fresh install when not given; --refresh-units
	// upgrades an existing node and must name it explicitly.
	if *refreshUnits && strings.TrimSpace(*nodeID) == "" {
		log.Fatal("provision: --refresh-units requires --node-id (it upgrades an existing node)")
	}
	if strings.TrimSpace(*nodeID) == "" {
		base := *nodeName
		if strings.TrimSpace(base) == "" {
			base = string(pr)
		}
		*nodeID = idgen.New(base, string(pr))
		log.Printf("provision: generated node id %s", *nodeID)
	}
	if *refreshUnits {
		if *makeStandby {
			log.Fatal("provision: --refresh-units and --make-standby are mutually exclusive")
		}
		refreshNodeUnits(*nodeID, pr, *sshHost, *sshUser, *sshPort, *sshKey, *knownHosts,
			*unitsDir, *tpBin, *sbBin, *ttBin,
			*domain, *entryDomain, *connectSub, *acmeEmail, *brand, *acmeStaging)
		return
	}
	if *makeStandby {
		if pr != model.RoleExit {
			log.Fatal("provision: --make-standby requires --role exit (a standby is a control-plane replica + exit)")
		}
		if strings.TrimSpace(*realitySNI) == "" {
			log.Fatal("provision: --make-standby requires --reality-sni for the exit's dial-in")
		}
		if out, err := (localRunner{}).Run(context.Background(), "id -u"); err != nil || strings.TrimSpace(out) != "0" {
			log.Fatal("provision: --make-standby must run as root on the primary (it edits the primary's Postgres)")
		}
	}
	ca, err := loadCA(*caDir)
	if err != nil {
		log.Fatalf("provision: load ca: %v", err)
	}

	binaries := map[string][]byte{}
	mustUpload := func(local, remote string) {
		if local == "" {
			log.Fatalf("provision: a binary is required for %s", remote)
		}
		b, err := os.ReadFile(local)
		if err != nil {
			log.Fatalf("provision: read %s: %v", local, err)
		}
		binaries[remote] = b
	}
	mustUpload(*tpBin, "/usr/local/bin/trustpanel")
	mustUpload(*sbBin, "/usr/local/bin/sing-box")

	if pr == model.RoleEntry {
		mustUpload(*ttBin, "/opt/trusttunnel/trusttunnel_endpoint")
	}
	units, err := provisionUnits(*unitsDir, pr)
	if err != nil {
		log.Fatalf("provision: %v", err)
	}

	runner := provision.SSHRunner{
		Host: *sshHost, User: *sshUser, Port: *sshPort, KeyFile: *sshKey,
		KnownHosts: *knownHosts, ConnectTimeout: 10 * time.Second,
	}
	if *knownHosts != "" {
		if err := runner.EnsureHostKey(context.Background()); err != nil {
			log.Fatalf("provision: pin host key: %v", err)
		}
	}

	agentEnv := "TRUSTPANEL_NODE_ID=" + *nodeID + "\n"
	var planFiles []provision.PlannedFile
	var entryDomainRows []model.Domain
	if pr == model.RoleEntry {
		var extraEnv string
		extraEnv, planFiles, entryDomainRows = entryProvisionExtras(*nodeID, *domain, *entryDomain,
			*connectSub, *acmeEmail, *brand, *acmeStaging)
		agentEnv += extraEnv
	}

	p := &provision.Provisioner{CA: ca, CertValidity: 90 * 24 * time.Hour}
	plan := provision.Plan{
		NodeID:   *nodeID,
		Role:     pr,
		IPs:      parseIPList(*ipsCSV),
		Binaries: binaries,
		AgentEnv: agentEnv,
		Files:    planFiles,
		Units:    units,
	}
	res, err := p.Provision(context.Background(), runner, plan)
	if err != nil {
		log.Fatalf("provision: %v", err)
	}
	fmt.Printf("provisioned %s (%s): %s\n", res.NodeID, pr, strings.Join(res.Steps, " -> "))

	if pr == model.RoleEntry {
		registerEntryNode(*nodeID, *nodeName, *ipsCSV, entryDomainRows, *dsn)
	}

	if *makeStandby {
		makeStandbyAfterProvision(*nodeID, *ipsCSV, *realitySNI, *dsn, *caDir, runner)
	}
}

// entryProvisionExtras builds the entry-only agent.env additions (node-local
// ACME), the fallback.env file, and the domain rows to register — mirroring the
// panel installer (panel.entryDomains/agentEnv) so a CLI-provisioned entry comes
// up fully configured (TLS + camouflage brand + routing) with no manual steps.
func entryProvisionExtras(nodeID, apex, entryDomain, connectSub, acmeEmail, brand string, staging bool) (agentEnv string, files []provision.PlannedFile, domains []model.Domain) {
	sub := strings.TrimSpace(connectSub)
	if sub == "" {
		sub = "vpn"
	}
	apex = strings.TrimSpace(apex)
	d := strings.TrimSpace(entryDomain)
	var acmeDomains []string
	domains, acmeDomains = cliEntryDomains(nodeID, apex, sub, d)
	var b strings.Builder
	if len(acmeDomains) > 0 {
		email := strings.TrimSpace(acmeEmail)
		if email == "" && apex != "" {
			email = "admin@" + apex
		}
		fmt.Fprintf(&b, "TRUSTPANEL_ACME_DOMAINS=%s\n", strings.Join(acmeDomains, ","))
		if email != "" {
			fmt.Fprintf(&b, "TRUSTPANEL_ACME_EMAIL=%s\n", email)
		}
		if staging {
			b.WriteString("TRUSTPANEL_ACME_STAGING=1\n")
		}
	}
	if brand != "" || apex != "" {
		files = append(files, provision.PlannedFile{
			Path: "/etc/trustpanel/fallback.env", Mode: 0o644,
			Body: []byte(fmt.Sprintf("TRUSTPANEL_BRAND=%s\nTRUSTPANEL_DOMAIN=%s\nTRUSTPANEL_CONNECT_SUBDOMAIN=%s\n", brand, apex, sub)),
		})
	}
	return b.String(), files, domains
}

// cliEntryDomains mirrors panel.entryDomains: when the entry's TLS hostname is
// the deployment apex, the VPN endpoint goes on the login subdomain
// (main-fallback) and the apex is a landing legend (fallback-site), sharing one
// cert; a custom (non-apex) domain is used directly. Empty domain => no TLS yet.
func cliEntryDomains(nodeID, apex, sub, d string) (domains []model.Domain, acmeDomains []string) {
	if d == "" {
		return nil, nil
	}
	if apex != "" && d == apex {
		portal := sub + "." + apex
		domains = append(domains,
			model.Domain{ID: domID(portal), Hostname: portal, Purpose: model.PurposeMainFallback, NodeID: nodeID},
			model.Domain{ID: domID(d), Hostname: d, Purpose: model.PurposeFallbackSite, NodeID: nodeID})
		return domains, []string{portal, d}
	}
	domains = append(domains, model.Domain{ID: domID(d), Hostname: d, Purpose: model.PurposeMainFallback, NodeID: nodeID})
	return domains, []string{d}
}

// slugify reduces s to a lowercase [a-z0-9-] slug (runs of other chars collapse
// to single dashes, trimmed at the ends).
func slugify(s string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			prevDash = false
		} else if !prevDash {
			b.WriteRune('-')
			prevDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

// domID derives a deterministic domain row id from the hostname so re-running
// provision upserts the same row instead of creating duplicates.
func domID(host string) string { return "dom-" + slugify(host) }

// registerEntryNode records the freshly provisioned entry + its domains in the
// control-plane DB so the reconcile loop pushes its TrustTunnel config and the
// panel can manage it. Runs on the primary (reads the DSN from serve.env).
func registerEntryNode(nodeID, nodeName, ipsCSV string, domains []model.Domain, dsn string) {
	ctx := context.Background()
	dsnStr := connDSN(dsn)
	if dsnStr == "" {
		log.Fatal("provision: entry registration needs a DSN (pass --dsn, set TRUSTPANEL_DSN, or run where /etc/trustpanel/serve.env exists)")
	}
	st, err := store.Open(ctx, dsnStr)
	if err != nil {
		log.Fatalf("provision: open store for entry registration: %v", err)
	}
	defer st.Close()
	ips := splitCSV(ipsCSV)
	name := strings.TrimSpace(nodeName)
	if name == "" {
		name = nodeID
	}
	node := model.Node{
		ID: nodeID, Name: name, PublicRole: model.RoleEntry,
		PublicIPs: ips, AgentAddr: firstIP(ips) + ":8443",
	}
	if err := node.Validate(); err != nil {
		log.Fatalf("provision: invalid entry node: %v", err)
	}
	if err := st.UpsertNode(ctx, node); err != nil {
		log.Fatalf("provision: register entry node: %v", err)
	}
	for _, d := range domains {
		if err := st.UpsertDomain(ctx, d); err != nil {
			log.Fatalf("provision: register domain %s: %v", d.Hostname, err)
		}
	}
	log.Printf("registered entry node %s (%s) with %d domain(s)", nodeID, name, len(domains))
}

// refreshNodeUnits is the upgrade path for an already-enrolled node: it re-pushes
// whichever binaries are given plus the systemd units and agent.env, then reloads
// and restarts. It does not load the CA, re-enroll, or reset agent state, so the
// node keeps its identity and leadership/epoch state across the upgrade. Binaries
// are optional here (a units-only refresh is valid); only those passed are sent.
func refreshNodeUnits(nodeID string, pr model.PublicRole, sshHost, sshUser string, sshPort int,
	sshKey, knownHosts, unitsDir, tpBin, sbBin, ttBin string,
	domain, entryDomain, connectSub, acmeEmail, brand string, acmeStaging bool) {
	// RefreshUnits rewrites /etc/trustpanel/agent.env from plan.AgentEnv. For an
	// entry that env carries the node-local ACME config (domains/email/staging) —
	// rebuild it here or the refresh would silently strip ACME and the agent would
	// stop managing the TLS cert. Require the same domain inputs provision needs.
	if pr == model.RoleEntry && strings.TrimSpace(entryDomain) == "" && strings.TrimSpace(domain) == "" {
		log.Fatal("provision: --refresh-units on an entry needs --entry-domain (and --domain/--brand) so agent.env keeps its ACME config")
	}
	binaries := map[string][]byte{}
	optUpload := func(local, remote string) {
		if local == "" {
			return
		}
		b, err := os.ReadFile(local)
		if err != nil {
			log.Fatalf("provision: read %s: %v", local, err)
		}
		binaries[remote] = b
	}
	optUpload(tpBin, "/usr/local/bin/trustpanel")
	optUpload(sbBin, "/usr/local/bin/sing-box")
	if pr == model.RoleEntry {
		optUpload(ttBin, "/opt/trusttunnel/trusttunnel_endpoint")
	}
	units, err := provisionUnits(unitsDir, pr)
	if err != nil {
		log.Fatalf("provision: %v", err)
	}

	runner := provision.SSHRunner{
		Host: sshHost, User: sshUser, Port: sshPort, KeyFile: sshKey,
		KnownHosts: knownHosts, ConnectTimeout: 10 * time.Second,
	}
	if knownHosts != "" {
		if err := runner.EnsureHostKey(context.Background()); err != nil {
			log.Fatalf("provision: pin host key: %v", err)
		}
	}

	agentEnv := "TRUSTPANEL_NODE_ID=" + nodeID + "\n"
	var planFiles []provision.PlannedFile
	if pr == model.RoleEntry {
		var extraEnv string
		extraEnv, planFiles, _ = entryProvisionExtras(nodeID, domain, entryDomain,
			connectSub, acmeEmail, brand, acmeStaging)
		agentEnv += extraEnv
	}

	p := &provision.Provisioner{}
	plan := provision.Plan{
		NodeID:   nodeID,
		Role:     pr,
		Binaries: binaries,
		AgentEnv: agentEnv,
		Files:    planFiles,
		Units:    units,
	}
	res, err := p.RefreshUnits(context.Background(), runner, plan)
	if err != nil {
		log.Fatalf("provision: refresh-units: %v", err)
	}
	fmt.Printf("refreshed %s (%s): %s\n", res.NodeID, pr, strings.Join(res.Steps, " -> "))
}

// makeStandbyAfterProvision registers the freshly installed exit in the DB (so it
// is a known node) and then layers the control-plane standby onto it via the
// shared add-standby flow. Runs on the primary as root (it edits the primary's
// Postgres). The break-glass twin of the panel installer's "make standby"
// checkbox.
func makeStandbyAfterProvision(nodeID, ipsCSV, realitySNI, dsn, caDir string, sb provision.SSHRunner) {
	ctx := context.Background()
	connDSN, serveEnv, deployEnv := resolveDSN(dsn)
	if connDSN == "" {
		log.Fatal("provision: --make-standby needs a DSN (pass --dsn, set TRUSTPANEL_DSN, or run where /etc/trustpanel/serve.env exists)")
	}

	st, err := store.Open(ctx, connDSN)
	if err != nil {
		log.Fatalf("provision: --make-standby open store: %v", err)
	}
	defer st.Close()

	// Register the exit node so the add-standby flow can resolve it from state.
	ips := strings.Split(ipsCSV, ",")
	for i := range ips {
		ips[i] = strings.TrimSpace(ips[i])
	}
	di, err := provision.NewRealityDialIn(realitySNI)
	if err != nil {
		log.Fatalf("provision: generate reality keys: %v", err)
	}
	node := model.Node{
		ID: nodeID, Name: nodeID, PublicRole: model.RoleExit,
		PublicIPs: ips, AgentAddr: firstIP(ips) + ":8443",
		DialIn: di,
	}
	if err := node.Validate(); err != nil {
		log.Fatalf("provision: invalid exit node: %v", err)
	}
	if err := st.UpsertNode(ctx, node); err != nil {
		log.Fatalf("provision: register exit node: %v", err)
	}
	log.Printf("registered exit node %s; making it a control-plane standby…", nodeID)

	if err := runStandbyFlow(ctx, st, localRunner{}, sb, nodeID, standbyFlowOpts{
		sshHost: sb.Host, pkiDir: caDir, serveEnv: serveEnv, deployEnv: deployEnv,
	}); err != nil {
		log.Fatalf("provision: --make-standby: %v", err)
	}
}

func parseIPList(csv string) []net.IP {
	var ips []net.IP
	for _, s := range strings.Split(csv, ",") {
		if s = strings.TrimSpace(s); s != "" {
			if ip := net.ParseIP(s); ip != nil {
				ips = append(ips, ip)
			}
		}
	}
	return ips
}
