package render

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"trustpanel/internal/core/model"
)

func renderWorked(t *testing.T, nodeID string) CompiledNode {
	t.Helper()
	at := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	cn, err := RenderNode(model.WorkedExample(), nodeID, at, Options{})
	if err != nil {
		t.Fatalf("RenderNode(%q): %v", nodeID, err)
	}
	return cn
}

func fileByName(t *testing.T, cn CompiledNode, name string) string {
	t.Helper()
	for _, f := range cn.Files {
		if f.Path == name {
			return f.Content
		}
	}
	t.Fatalf("artifact %q not found in node %q", name, cn.NodeID)
	return ""
}

func parseSingbox(t *testing.T, content string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(content), &m); err != nil {
		t.Fatalf("sing-box config is not valid JSON: %v", err)
	}
	return m
}

// The render invariant: credentials.toml, sing-box inbound users[], and
// v2ray_api stats users[] all come from the same active-user projection.
func TestEntryThreeRenderInvariant(t *testing.T) {
	cn := renderWorked(t, "entryA")
	want := []string{"admin-device", "alice", "bob"}

	creds := fileByName(t, cn, "credentials.toml")
	for _, u := range want {
		if !strings.Contains(creds, `username = "`+u+`"`) {
			t.Errorf("credentials.toml missing user %q", u)
		}
	}

	sb := parseSingbox(t, fileByName(t, cn, "sing-box.json"))
	inbound := sb["inbounds"].([]any)[0].(map[string]any)
	gotInbound := usernamesFrom(inbound["users"].([]any), "username")
	assertEqualSlice(t, "inbound users", gotInbound, want)

	exp := sb["experimental"].(map[string]any)["v2ray_api"].(map[string]any)["stats"].(map[string]any)
	gotStats := toStringSlice(exp["users"].([]any))
	assertEqualSlice(t, "stats users", gotStats, want)
}

func TestEntryRouteCompilation(t *testing.T) {
	cn := renderWorked(t, "entryA")
	sb := parseSingbox(t, fileByName(t, cn, "sing-box.json"))
	route := sb["route"].(map[string]any)
	rules := route["rules"].([]any)

	// Domain/geosite rules need the destination domain sniffed (full-tunnel
	// clients connect by IP), and a geoip policy needs it resolved — so the order
	// is [sniff, resolve, …rules] with a dns block for resolve.
	if rules[0].(map[string]any)["action"] != "sniff" {
		t.Errorf("first rule should be the sniff rule, got %+v", rules[0])
	}
	if rules[1].(map[string]any)["action"] != "resolve" {
		t.Errorf("second rule should be the resolve rule (geoip needs it), got %+v", rules[1])
	}
	if _, ok := sb["dns"]; !ok {
		t.Errorf("a dns block is required for the resolve action")
	}

	// The guard rule routes .ru direct (no auth_user = fleet-wide) and references geoip-ru.
	guard := rules[2].(map[string]any)
	if guard["outbound"] != "direct" {
		t.Errorf("second rule should be the guard direct rule, got %+v", guard)
	}
	if _, hasAuth := guard["auth_user"]; hasAuth {
		t.Errorf("fleet-wide guard rule should have no auth_user, got %+v", guard["auth_user"])
	}
	if !containsStr(toStringSlice(guard["domain_suffix"].([]any)), "ru") {
		t.Errorf("guard rule should match domain_suffix ru, got %+v", guard["domain_suffix"])
	}
	if !containsStr(toStringSlice(guard["rule_set"].([]any)), "geoip-ru") {
		t.Errorf("guard rule should reference rule_set geoip-ru, got %+v", guard["rule_set"])
	}

	// The admin exclusive route -> exit-node1, scoped to admin-device only.
	adminRule := findRule(rules, "exit-node1")
	if adminRule == nil {
		t.Fatal("no rule routes to exit-node1")
	}
	assertEqualSlice(t, "admin rule auth_user", toStringSlice(adminRule["auth_user"].([]any)), []string{"admin-device"})
	if !containsStr(toStringSlice(adminRule["domain_suffix"].([]any)), "example.com") {
		t.Errorf("admin rule should match example.com")
	}

	if route["final"] != "direct" {
		t.Errorf("final should be direct, got %v", route["final"])
	}

	// Outbounds: direct + exit-node1 + exit-node2.
	tags := outboundTags(sb["outbounds"].([]any))
	for _, want := range []string{"direct", "exit-node1", "exit-node2"} {
		if !containsStr(tags, want) {
			t.Errorf("missing outbound %q (got %v)", want, tags)
		}
	}

	// geoip-ru must be a required rule-set and have a local rule_set def.
	if !containsStr(cn.RequiredRuleSets, "geoip-ru") {
		t.Errorf("geoip-ru should be a required rule-set, got %v", cn.RequiredRuleSets)
	}
	if route["rule_set"] == nil {
		t.Fatal("route.rule_set should define geoip-ru")
	}
}

func TestEntryVLESSOutboundShape(t *testing.T) {
	cn := renderWorked(t, "entryA")
	sb := parseSingbox(t, fileByName(t, cn, "sing-box.json"))
	var node1 map[string]any
	for _, o := range sb["outbounds"].([]any) {
		m := o.(map[string]any)
		if m["tag"] == "exit-node1" {
			node1 = m
		}
	}
	if node1 == nil {
		t.Fatal("exit-node1 outbound missing")
	}
	if node1["type"] != "vless" || node1["uuid"] != "uuid-node1" || node1["flow"] != realityFlow {
		t.Errorf("unexpected vless outbound: %+v", node1)
	}
	tls := node1["tls"].(map[string]any)
	if tls["server_name"] != "www.example-cdn.com" {
		t.Errorf("reality server_name wrong: %v", tls["server_name"])
	}
	reality := tls["reality"].(map[string]any)
	if reality["public_key"] != "pub-node1" || reality["short_id"] != "0123abcd" {
		t.Errorf("reality params wrong: %+v", reality)
	}
}

func TestEntryTrustTunnelConfig(t *testing.T) {
	cn := renderWorked(t, "entryA")
	// Entry now renders 5 files: credentials, sing-box, vpn, hosts, rules.
	names := map[string]bool{}
	for _, f := range cn.Files {
		names[f.Path] = true
	}
	for _, want := range []string{"credentials.toml", "sing-box.json", "vpn.toml", "hosts.toml", "rules.toml"} {
		if !names[want] {
			t.Errorf("entry missing artifact %q", want)
		}
	}

	vpn := fileByName(t, cn, "vpn.toml")
	// Forwards to the local sing-box over SOCKS5 with extended_auth=false.
	if !strings.Contains(vpn, `[forward_protocol.socks5]`) ||
		!strings.Contains(vpn, `address = "127.0.0.1:2080"`) ||
		!strings.Contains(vpn, `extended_auth = false`) {
		t.Errorf("vpn.toml forward_protocol wrong:\n%s", vpn)
	}
	if !strings.Contains(vpn, `[reverse_proxy]`) || !strings.Contains(vpn, `path_mask = "/"`) {
		t.Errorf("vpn.toml reverse_proxy missing:\n%s", vpn)
	}

	hosts := fileByName(t, cn, "hosts.toml")
	if !strings.Contains(hosts, `hostname = "cdn.example.com"`) || !strings.Contains(hosts, `[[main_hosts]]`) {
		t.Errorf("hosts.toml main host wrong:\n%s", hosts)
	}
}

func TestExitRender(t *testing.T) {
	cn := renderWorked(t, "node1")
	if cn.Role != model.RoleExit {
		t.Fatalf("expected exit role, got %v", cn.Role)
	}
	sb := parseSingbox(t, fileByName(t, cn, "sing-box.json"))
	inbound := sb["inbounds"].([]any)[0].(map[string]any)
	if inbound["type"] != "vless" {
		t.Errorf("exit inbound should be vless, got %v", inbound["type"])
	}
	tls := inbound["tls"].(map[string]any)["reality"].(map[string]any)
	if tls["private_key"] != "priv-node1" {
		t.Errorf("exit reality private_key wrong: %v", tls["private_key"])
	}
	out := sb["outbounds"].([]any)[0].(map[string]any)
	if out["type"] != "direct" || out["tag"] != "freedom" {
		t.Errorf("exit outbound should be direct/freedom, got %+v", out)
	}
	// Exit nodes carry no user credentials.
	for _, f := range cn.Files {
		if f.Path == "credentials.toml" {
			t.Error("exit node should not render credentials.toml")
		}
	}
}

func TestExpiredUserExcluded(t *testing.T) {
	st := model.WorkedExample()
	past := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := range st.Users {
		if st.Users[i].Username == "bob" {
			st.Users[i].ExpiresAt = &past
		}
	}
	cn, err := RenderNode(st, "entryA", time.Date(2026, 6, 12, 0, 0, 0, 0, time.UTC), Options{})
	if err != nil {
		t.Fatal(err)
	}
	creds := fileByName(t, cn, "credentials.toml")
	if strings.Contains(creds, `"bob"`) {
		t.Error("expired user bob should be excluded from credentials")
	}
}

func TestDrainedExitRoutesLocally(t *testing.T) {
	state := model.State{
		Nodes: []model.Node{
			{ID: "en", Name: "Entry", PublicRole: model.RoleEntry, PublicIPs: []string{"1.1.1.1"}, AgentAddr: "1.1.1.1:8443"},
			{ID: "ex", Name: "Exit", PublicRole: model.RoleExit, PublicIPs: []string{"2.2.2.2"}, AgentAddr: "2.2.2.2:8443",
				Maintenance: true,
				DialIn:      &model.DialIn{Proto: model.DialInProtoVLESSReality, Port: 443, UUID: "u", TargetSNI: "www.cdn77.com", PublicKey: "k", PrivKey: "p", ShortID: "ab"}},
		},
		Groups: []model.Group{{ID: "g", Name: "G", DefaultExitID: "ex"}},
		Users:  []model.User{{ID: "u1", Username: "alice", Secret: "s", Enabled: true, GroupID: "g"}},
	}
	cn, err := RenderNode(state, "en", time.Now(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	sb := parseSingbox(t, fileByName(t, cn, "sing-box.json"))
	// The group's default route must fall back to local egress, not the drained exit.
	rules := sb["route"].(map[string]any)["rules"].([]any)
	last := rules[len(rules)-1].(map[string]any)
	if last["outbound"] != "direct" {
		t.Errorf("drained exit's group should route direct, got %v", last["outbound"])
	}
	if findRule(rules, "exit-ex") != nil {
		t.Error("no rule should route to a drained exit")
	}
	// And the drained exit must not be dialed as an outbound at all.
	if containsStr(outboundTags(sb["outbounds"].([]any)), "exit-ex") {
		t.Error("drained exit must not appear as an outbound")
	}
	// The operator is warned that traffic is egressing locally.
	warned := false
	for _, w := range cn.Warnings {
		if strings.Contains(w, "maintenance") {
			warned = true
		}
	}
	if !warned {
		t.Errorf("expected a maintenance warning, got %v", cn.Warnings)
	}
}

// An exclusion on a group with NO default exit must route the excluded
// domain to `direct` (the group's normal path), not be dropped — otherwise the
// excluded domain falls through to the block/exit policy it was meant to bypass.
func TestExclusionNoDefaultExitRoutesDirect(t *testing.T) {
	state := model.State{
		Nodes:  []model.Node{{ID: "en", Name: "Entry", PublicRole: model.RoleEntry, PublicIPs: []string{"1.1.1.1"}, AgentAddr: "1.1.1.1:8443"}},
		Groups: []model.Group{{ID: "g", Name: "G"}}, // no default exit
		Users:  []model.User{{ID: "u1", Username: "alice", Secret: "s", Enabled: true, GroupID: "g"}},
		RoutePolicies: []model.RoutePolicy{
			{ID: "p1", Name: "block-danger", Tier: model.TierExit, Action: model.ActionBlock,
				AppliesToGroupID: "g", MatchDomains: []string{"danger"}, ExcludeDomains: []string{"safe.danger"}},
		},
	}
	cn, err := RenderNode(state, "en", time.Now(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	sb := parseSingbox(t, fileByName(t, cn, "sing-box.json"))
	rules := sb["route"].(map[string]any)["rules"].([]any)
	// There must be an exclusion rule sending safe.danger to direct, ordered before
	// the block rule so it wins.
	var exclIdx, blockIdx = -1, -1
	for i, r := range rules {
		m := r.(map[string]any)
		ds, _ := m["domain_suffix"].([]any)
		if len(ds) == 1 && ds[0] == "safe.danger" {
			if m["outbound"] != "direct" {
				t.Errorf("excluded domain should route direct, got %v", m["outbound"])
			}
			exclIdx = i
		}
		if m["action"] == "reject" {
			blockIdx = i
		}
	}
	if exclIdx < 0 {
		t.Fatalf("no exclusion rule emitted for safe.danger; rules=%v", rules)
	}
	if blockIdx >= 0 && exclIdx > blockIdx {
		t.Errorf("exclusion rule must precede the block rule (excl=%d block=%d)", exclIdx, blockIdx)
	}
}

// A namespace policy with "all users" (no group) must be scoped to that
// namespace's own clients via auth_user — never compiled with an empty auth_user,
// which would block/route every namespace's traffic (the cross-namespace leak).
func TestNamespacePolicyScopedToOwner(t *testing.T) {
	state := model.State{
		Nodes: []model.Node{
			{ID: "en", Name: "Entry", PublicRole: model.RoleEntry, PublicIPs: []string{"1.1.1.1"}, AgentAddr: "1.1.1.1:8443"},
		},
		Groups: []model.Group{
			{ID: "g1", Name: "G1", OwnerID: "ns1"},
			{ID: "g2", Name: "G2", OwnerID: "ns2"},
		},
		Users: []model.User{
			{ID: "u1", Username: "alice", Secret: "s", Enabled: true, GroupID: "g1", OwnerID: "ns1"},
			{ID: "u2", Username: "bob", Secret: "s", Enabled: true, GroupID: "g2", OwnerID: "ns2"},
		},
		RoutePolicies: []model.RoutePolicy{
			// ns1's "all users -> block example.com". AppliesToGroupID empty.
			{ID: "p", Name: "ns1-block", Tier: model.TierExit, OwnerID: "ns1",
				MatchDomains: []string{"example.com"}, Action: model.ActionBlock},
		},
	}
	cn, err := RenderNode(state, "en", time.Now(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	sb := parseSingbox(t, fileByName(t, cn, "sing-box.json"))
	rules := sb["route"].(map[string]any)["rules"].([]any)

	var block map[string]any
	for _, r := range rules {
		m := r.(map[string]any)
		ds, _ := m["domain_suffix"].([]any)
		if m["action"] == "reject" && containsStr(toStringSlice(ds), "example.com") {
			block = m
		}
	}
	if block == nil {
		t.Fatalf("no reject rule for example.com; rules=%v", rules)
	}
	au, ok := block["auth_user"].([]any)
	if !ok {
		t.Fatal("namespace block rule must carry auth_user (empty = leaks to all namespaces)")
	}
	got := toStringSlice(au)
	assertEqualSlice(t, "ns1 block auth_user", got, []string{"alice"})
	if containsStr(got, "bob") {
		t.Error("ns1 policy leaked into ns2's client (bob)")
	}
}

// A namespace policy with no active clients must be dropped, not emitted with an
// empty auth_user (which would match everyone).
func TestNamespacePolicyWithNoClientsDropped(t *testing.T) {
	state := model.State{
		Nodes: []model.Node{
			{ID: "en", Name: "Entry", PublicRole: model.RoleEntry, PublicIPs: []string{"1.1.1.1"}, AgentAddr: "1.1.1.1:8443"},
		},
		Groups: []model.Group{{ID: "g2", Name: "G2", OwnerID: "ns2"}},
		Users:  []model.User{{ID: "u2", Username: "bob", Secret: "s", Enabled: true, GroupID: "g2", OwnerID: "ns2"}},
		RoutePolicies: []model.RoutePolicy{
			// ns1 owns the policy but has no active clients on this entry.
			{ID: "p", Name: "ns1-block", Tier: model.TierExit, OwnerID: "ns1",
				MatchDomains: []string{"example.com"}, Action: model.ActionBlock},
		},
	}
	cn, err := RenderNode(state, "en", time.Now(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	sb := parseSingbox(t, fileByName(t, cn, "sing-box.json"))
	for _, r := range sb["route"].(map[string]any)["rules"].([]any) {
		m := r.(map[string]any)
		ds, _ := m["domain_suffix"].([]any)
		if m["action"] == "reject" && containsStr(toStringSlice(ds), "example.com") {
			t.Fatalf("ns1 policy with no clients must be dropped, not emitted: %v", m)
		}
	}
}

// ---- helpers ----

func usernamesFrom(users []any, key string) []string {
	out := make([]string, 0, len(users))
	for _, u := range users {
		out = append(out, u.(map[string]any)[key].(string))
	}
	return out
}

func toStringSlice(v []any) []string {
	out := make([]string, 0, len(v))
	for _, e := range v {
		out = append(out, e.(string))
	}
	return out
}

func outboundTags(outbounds []any) []string {
	out := make([]string, 0, len(outbounds))
	for _, o := range outbounds {
		out = append(out, o.(map[string]any)["tag"].(string))
	}
	return out
}

func findRule(rules []any, outbound string) map[string]any {
	for _, r := range rules {
		m := r.(map[string]any)
		if m["outbound"] == outbound {
			return m
		}
	}
	return nil
}

func containsStr(s []string, want string) bool {
	for _, e := range s {
		if e == want {
			return true
		}
	}
	return false
}

func assertEqualSlice(t *testing.T, label string, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: len %d != %d (got %v want %v)", label, len(got), len(want), got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("%s: index %d = %q, want %q", label, i, got[i], want[i])
		}
	}
}

func TestGuardExclusionRule(t *testing.T) {
	state := model.State{
		Nodes: []model.Node{
			{ID: "en", Name: "Entry", PublicRole: model.RoleEntry, PublicIPs: []string{"1.1.1.1"}, AgentAddr: "1.1.1.1:8443"},
			{ID: "ex", Name: "Exit", PublicRole: model.RoleExit, PublicIPs: []string{"2.2.2.2"}, AgentAddr: "2.2.2.2:8443",
				DialIn: &model.DialIn{Proto: model.DialInProtoVLESSReality, Port: 443, UUID: "u", TargetSNI: "www.cdn77.com", PublicKey: "k", PrivKey: "p", ShortID: "ab"}},
		},
		Groups: []model.Group{{ID: "g", Name: "G", DefaultExitID: "ex"}},
		Users:  []model.User{{ID: "u1", Username: "alice", Secret: "s", Enabled: true, GroupID: "g"}},
		RoutePolicies: []model.RoutePolicy{{
			ID: "p", Name: "ru-guard", Tier: model.TierGuard, AppliesToGroupID: "g",
			MatchDomains: []string{".ru"}, ExcludeDomains: []string{"bank.ru"}, Action: model.ActionDirect,
		}},
	}
	cn, err := RenderNode(state, "en", time.Now(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	sb := parseSingbox(t, fileByName(t, cn, "sing-box.json"))
	rules := sb["route"].(map[string]any)["rules"].([]any)

	// Find the exclusion rule (bank.ru -> exit-ex) and the guard rule (.ru -> direct).
	excIdx, guardIdx := -1, -1
	for i, r := range rules {
		m := r.(map[string]any)
		ds, _ := m["domain_suffix"].([]any)
		if containsStr(toStringSlice(ds), "bank.ru") && m["outbound"] == "exit-ex" {
			excIdx = i
		}
		if containsStr(toStringSlice(ds), "ru") && m["outbound"] == "direct" {
			guardIdx = i
		}
	}
	if excIdx < 0 {
		t.Fatalf("no exclusion rule (bank.ru -> exit-ex); rules=%v", rules)
	}
	if guardIdx < 0 {
		t.Fatalf("no guard rule (.ru -> direct)")
	}
	if excIdx > guardIdx {
		t.Errorf("exclusion rule (%d) must come before the guard rule (%d) to win", excIdx, guardIdx)
	}
}

// A fleet mandate compiles above guard (and exit) and may route to an exit —
// the fleet-wide exclusive route that no namespace can override.
func TestFleetMandatePrecedence(t *testing.T) {
	state := model.State{
		Nodes: []model.Node{
			{ID: "en", Name: "Entry", PublicRole: model.RoleEntry, PublicIPs: []string{"1.1.1.1"}, AgentAddr: "1.1.1.1:8443"},
			{ID: "ex", Name: "Exit", PublicRole: model.RoleExit, PublicIPs: []string{"2.2.2.2"}, AgentAddr: "2.2.2.2:8443",
				DialIn: &model.DialIn{Proto: model.DialInProtoVLESSReality, Port: 443, UUID: "u", TargetSNI: "www.cdn77.com", PublicKey: "k", PrivKey: "p", ShortID: "ab"}},
		},
		Groups: []model.Group{{ID: "g", Name: "G", DefaultExitID: "ex"}},
		Users:  []model.User{{ID: "u1", Username: "alice", Secret: "s", Enabled: true, GroupID: "g"}},
		RoutePolicies: []model.RoutePolicy{
			// Fleet mandate: force example.com out the compliance exit, fleet-wide
			// (no group => everyone, all namespaces). owner_id "" = infra.
			{ID: "m", Name: "force-exit", Tier: model.TierFleet, MatchDomains: []string{"example.com"},
				Action: model.ActionExit, ExitNodeID: "ex"},
			// A guard rule that would send the same domain direct if it ran first.
			{ID: "p", Name: "keep-direct", Tier: model.TierGuard, MatchDomains: []string{"example.com"},
				Action: model.ActionDirect},
		},
	}
	cn, err := RenderNode(state, "en", time.Now(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	sb := parseSingbox(t, fileByName(t, cn, "sing-box.json"))
	rules := sb["route"].(map[string]any)["rules"].([]any)

	fleetIdx, guardIdx := -1, -1
	for i, r := range rules {
		m := r.(map[string]any)
		ds, _ := m["domain_suffix"].([]any)
		if !containsStr(toStringSlice(ds), "example.com") {
			continue
		}
		switch m["outbound"] {
		case "exit-ex":
			fleetIdx = i
			// Fleet-wide: matches everyone, so no auth_user scoping.
			if _, scoped := m["auth_user"]; scoped {
				t.Error("fleet-wide mandate must not be scoped to specific users")
			}
		case "direct":
			guardIdx = i
		}
	}
	if fleetIdx < 0 {
		t.Fatalf("no fleet mandate rule (example.com -> exit-ex); rules=%v", rules)
	}
	if guardIdx < 0 {
		t.Fatal("no guard rule (example.com -> direct)")
	}
	if fleetIdx > guardIdx {
		t.Errorf("fleet mandate (%d) must compile before the guard rule (%d) to win", fleetIdx, guardIdx)
	}
}
