package panel

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"trustpanel/internal/core/controller"
	"trustpanel/internal/core/idgen"
	"trustpanel/internal/core/model"
	"trustpanel/internal/core/pki"
	"trustpanel/internal/core/provision"
)

// ProvisionConfig enables the remote-install endpoint. nil => not configured.
type ProvisionConfig struct {
	CA             *pki.CA
	Layout         controller.Layout
	CertValidity   time.Duration
	TrustPanelBin  []byte
	SingBoxBin     []byte
	TrustTunnelBin []byte
	Units          func(role model.PublicRole) []provision.UnitFile

	// NewRunner builds a runner for the SSH params (real SSH in prod; injectable
	// for tests). The returned cleanup is called when provisioning finishes.
	NewRunner func(ctx context.Context, ssh provision.SSHParams) (provision.Runner, func(), error)
}

// EnableProvisioning wires the remote-install endpoint.
func (p *Panel) EnableProvisioning(cfg *ProvisionConfig) { p.prov = cfg }

// DefaultConnectSubdomain is the login subdomain the camouflage site advertises
// when the deployment does not configure one. Service-neutral on purpose: the
// portal link must read as a routine client login, never hint at what it fronts.
const DefaultConnectSubdomain = "portal"

// ProvisionRequest is the body of POST /api/nodes/provision.
type ProvisionRequest struct {
	Name      string   `json:"name"`
	Role      string   `json:"role"`
	PublicIPs []string `json:"public_ips"`
	// Entry: the VPN connection endpoint hostname served on this node (full host).
	Endpoint string `json:"endpoint"`
	// Entry: the deployment apex. Only consulted to establish the apex when it is
	// not set yet (the first entry defines the deployment's public face); once
	// stored in settings it is authoritative and this field is ignored.
	Apex string `json:"apex"`
	// Entry: also serve the apex as a camouflage-only decoy on this node. Honored
	// only while no other node hosts the apex yet.
	HostLanding bool                `json:"host_landing"`
	RealitySNI  string              `json:"reality_sni"`  // exit: borrowed SNI
	MakeStandby bool                `json:"make_standby"` // exit: also make it a control-plane standby (HA)
	SSH         provision.SSHParams `json:"ssh"`
	Hardening   provision.Hardening `json:"hardening"`
}

func (p *Panel) handleProvision(w http.ResponseWriter, r *http.Request) {
	if p.prov == nil {
		writeErr(w, http.StatusServiceUnavailable, "remote provisioning is not configured on this panel")
		return
	}
	var req ProvisionRequest
	if !decode(w, r, &req) {
		return
	}
	role := model.PublicRole(req.Role)
	if !role.Valid() {
		writeErr(w, http.StatusBadRequest, "role must be entry or exit")
		return
	}
	if strings.TrimSpace(req.Name) == "" || len(req.PublicIPs) == 0 || strings.TrimSpace(req.SSH.Host) == "" {
		writeErr(w, http.StatusBadRequest, "name, public_ips and ssh.host are required")
		return
	}

	// Provisioning is infra (the route is infraOnly-wrapped): operators never reach
	// here. HA/standby is shared infra, so any admin may install a standby; the
	// guard is defense in depth.
	acct := p.account(r)
	if !acct.CanManageInfra() && req.MakeStandby {
		writeErr(w, http.StatusForbidden, "only admins can install a node as a control-plane standby")
		return
	}

	// make_standby layers a control-plane replica onto the new exit once it is up.
	// Validate the preconditions now (synchronously) so the operator gets an
	// immediate error rather than a job that fails halfway.
	var primaryForStandby model.Node
	if req.MakeStandby {
		if role != model.RoleExit {
			writeErr(w, http.StatusBadRequest, "make_standby requires an exit node (a standby is a control-plane replica + exit)")
			return
		}
		st, err := p.store.LoadState(r.Context())
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		prim, ok := findPrimary(st)
		if !ok {
			writeErr(w, http.StatusBadRequest, "make_standby: could not resolve the control-plane primary to replicate from")
			return
		}
		primaryForStandby = *prim
	}

	nodeID := idgen.New(req.Name, "node")
	node := model.Node{
		ID: nodeID, Name: req.Name, PublicRole: role,
		PublicIPs: req.PublicIPs, AgentAddr: req.PublicIPs[0] + ":8443",
		OwnerID: "", // nodes are shared infra (no per-namespace node ownership)
	}
	var domains []model.Domain
	var acmeDomains []string
	switch role {
	case model.RoleExit:
		sni := strings.TrimSpace(req.RealitySNI)
		if sni == "" {
			writeErr(w, http.StatusBadRequest, "reality_sni is required for an exit node")
			return
		}
		di, err := provision.NewRealityDialIn(sni)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "generate reality keys: "+err.Error())
			return
		}
		node.DialIn = di
	case model.RoleEntry:
		endpoint := strings.TrimSpace(req.Endpoint)
		if endpoint == "" {
			writeErr(w, http.StatusBadRequest, "endpoint hostname is required for an entry node")
			return
		}
		st, err := p.store.LoadState(r.Context())
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		settings, err := p.store.GetSettings(r.Context())
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		// The apex is a deployment-wide singleton. It is normally already stored
		// (seeded at install or by the first entry); the first entry may establish
		// it here.
		apex := strings.TrimSpace(settings.Fleet.Apex)
		newApex := ""
		if apex == "" {
			apex = strings.TrimSpace(req.Apex)
			newApex = apex
		}
		apexHosted := false
		for _, d := range st.Domains {
			if strings.EqualFold(d.Hostname, endpoint) {
				writeErr(w, http.StatusBadRequest, "hostname "+endpoint+" is already served by another node")
				return
			}
			if apex != "" && strings.EqualFold(d.Hostname, apex) {
				apexHosted = true
			}
		}
		// Only the first entry (apex not yet hosted) carries the apex decoy; a
		// distinct endpoint hostname is required for the split to mean anything.
		hostLanding := req.HostLanding && apex != "" && !apexHosted && !strings.EqualFold(endpoint, apex)
		if newApex != "" {
			settings.Fleet.Apex = newApex
			if err := p.store.SaveSettings(r.Context(), settings); err != nil {
				writeErr(w, http.StatusInternalServerError, "save apex: "+err.Error())
				return
			}
		}
		domains, acmeDomains = entryDomains(nodeID, endpoint, apex, hostLanding)
	}
	if err := node.Validate(); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	makeStandby := req.MakeStandby
	jobID := p.jobs.Start("provision "+req.Name, func(log func(string)) error {
		ctx := context.Background()
		if err := p.provisionNode(ctx, log, node, domains, acmeDomains, req.SSH, req.Hardening); err != nil {
			return err
		}
		log("node " + nodeID + " (" + req.Name + ") registered in the panel")
		if !makeStandby {
			return nil
		}
		// Chain into add-standby: wait for the freshly installed agent, then layer
		// the control-plane replica on via the primary's agent.
		if primaryForStandby.ID == node.ID {
			return fmt.Errorf("make_standby: primary and standby are the same node")
		}
		if err := p.waitAgentReady(ctx, log, node, 3*time.Minute); err != nil {
			return err
		}
		return p.provisionStandby(ctx, log, primaryForStandby, node)
	})
	writeJSON(w, http.StatusOK, map[string]string{"job_id": jobID, "node_id": nodeID})
}

// entryDomains computes an entry node's TLS host bindings + the ACME SAN list.
// The endpoint hostname is always the VPN connection endpoint. When hostLanding
// is set, this node additionally serves the deployment apex as a camouflage-only
// decoy on the same certificate (the first entry fronts the apex; later entries
// only carry their own endpoint). Both share one cert.
func entryDomains(nodeID, endpoint, apex string, hostLanding bool) (domains []model.Domain, acmeDomains []string) {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return nil, nil
	}
	domains = append(domains,
		model.Domain{ID: idgen.New(endpoint, "dom"), Hostname: endpoint, Purpose: model.PurposeEndpoint, NodeID: nodeID})
	acmeDomains = []string{endpoint}
	if hostLanding && apex != "" && !strings.EqualFold(apex, endpoint) {
		domains = append(domains,
			model.Domain{ID: idgen.New(apex, "dom"), Hostname: apex, Purpose: model.PurposeDecoy, NodeID: nodeID})
		acmeDomains = append(acmeDomains, apex)
	}
	return domains, acmeDomains
}

// provisionNode runs the SSH provisioning for a prepared node and, on success,
// records the node + its domains in the store. Shared by the install endpoint and
// the convert-to-two-node job. log streams progress.
func (p *Panel) provisionNode(ctx context.Context, log func(string), node model.Node, domains []model.Domain, acmeDomains []string, ssh provision.SSHParams, hardening provision.Hardening) error {
	cfg := p.prov
	runner, cleanup, err := cfg.NewRunner(ctx, ssh)
	if err != nil {
		return fmt.Errorf("ssh connect: %w", err)
	}
	defer cleanup()
	// Entry nodes inherit the deployment's apex/brand/ACME defaults (Settings →
	// Defaults) so they come up fully configured with no manual post-install step.
	var apex, brand, acmeEmail, connectSub string
	if node.PublicRole == model.RoleEntry {
		if s, err := p.store.GetSettings(ctx); err == nil {
			apex = strings.TrimSpace(s.Fleet.Apex)
			brand = strings.TrimSpace(s.Fleet.Brand)
			acmeEmail = strings.TrimSpace(s.Fleet.ACMEEmail)
			connectSub = strings.TrimSpace(s.Fleet.ConnectSubdomain)
		}
	}
	if connectSub == "" {
		connectSub = DefaultConnectSubdomain
	}
	plan := provision.Plan{
		NodeID: node.ID, Role: node.PublicRole, IPs: parseIPs(node.PublicIPs),
		Binaries: binariesFor(cfg, node.PublicRole),
		AgentEnv: agentEnv(node.ID, node.PublicRole, acmeDomains, apex, acmeEmail),
		Units:    cfg.Units(node.PublicRole),
	}
	// Entry nodes also get fallback.env so the camouflage site shows the
	// deployment's brand/apex (+ login subdomain) with no manual step.
	if node.PublicRole == model.RoleEntry && (brand != "" || apex != "") {
		plan.Files = append(plan.Files, provision.PlannedFile{
			Path: "/etc/trustpanel/fallback.env", Mode: 0o644,
			Body: []byte(fmt.Sprintf("TRUSTPANEL_BRAND=%s\nTRUSTPANEL_DOMAIN=%s\nTRUSTPANEL_CONNECT_SUBDOMAIN=%s\n",
				brand, apex, connectSub)),
		})
	}
	if hardening.Enabled {
		plan.Hardening = &hardening
	}
	prov := &provision.Provisioner{CA: cfg.CA, CertValidity: cfg.CertValidity, Log: log}
	if _, err := prov.Provision(ctx, runner, plan); err != nil {
		return err
	}
	if err := p.store.UpsertNode(ctx, node); err != nil {
		return fmt.Errorf("save node: %w", err)
	}
	if node.PublicRole == model.RoleExit {
		// Same backfill as bootstrap's first exit (see the README's Multi-node
		// note): a group with no default exit yet egresses direct instead of
		// through this exit's Reality tunnel. Only touches still-unset groups,
		// so provisioning a second/third exit later is a no-op here.
		if err := p.store.BackfillDefaultExit(ctx, node.ID); err != nil {
			return fmt.Errorf("backfill default exit: %w", err)
		}
	}
	for _, d := range domains {
		if err := p.store.UpsertDomain(ctx, d); err != nil {
			return fmt.Errorf("save domain %s: %w", d.Hostname, err)
		}
	}
	return nil
}

// agentEnv builds /etc/trustpanel/agent.env: the node id always, plus ACME
// settings for entry nodes so the agent issues the TLS cert on first start with
// no manual configuration.
func agentEnv(nodeID string, role model.PublicRole, acmeDomains []string, apex, acmeEmail string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "TRUSTPANEL_NODE_ID=%s\n", nodeID)
	if role == model.RoleEntry && len(acmeDomains) > 0 {
		email := acmeEmail
		if email == "" && apex != "" {
			email = "admin@" + apex
		}
		fmt.Fprintf(&b, "TRUSTPANEL_ACME_DOMAINS=%s\n", strings.Join(acmeDomains, ","))
		if email != "" {
			fmt.Fprintf(&b, "TRUSTPANEL_ACME_EMAIL=%s\n", email)
		}
	}
	return b.String()
}

func (p *Panel) handleJob(w http.ResponseWriter, r *http.Request) {
	s, ok := p.jobs.Get(r.PathValue("id"))
	if !ok {
		writeErr(w, http.StatusNotFound, "job not found")
		return
	}
	writeJSON(w, http.StatusOK, s)
}

func binariesFor(cfg *ProvisionConfig, role model.PublicRole) map[string][]byte {
	b := map[string][]byte{}
	if cfg.TrustPanelBin != nil {
		b["/usr/local/bin/trustpanel"] = cfg.TrustPanelBin
	}
	if cfg.SingBoxBin != nil {
		b["/usr/local/bin/sing-box"] = cfg.SingBoxBin
	}
	if role == model.RoleEntry && cfg.TrustTunnelBin != nil {
		b["/opt/trusttunnel/trusttunnel_endpoint"] = cfg.TrustTunnelBin
	}
	return b
}

func parseIPs(ss []string) []net.IP {
	var out []net.IP
	for _, s := range ss {
		if ip := net.ParseIP(strings.TrimSpace(s)); ip != nil {
			out = append(out, ip)
		}
	}
	return out
}
