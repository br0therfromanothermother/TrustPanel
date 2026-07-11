package render

import (
	"net/netip"
	"testing"

	"trustpanel/internal/core/model"
)

// fakeGeo: an IP is "in" a country if listed; a domain is in a geosite category
// if listed. Mirrors what sing-box rule-set match would answer.
type fakeGeo struct {
	ip   map[string][]string // country -> ips
	site map[string][]string // category -> domains
}

func (f fakeGeo) GeoIP(cc string, ip netip.Addr) (bool, error) {
	for _, s := range f.ip[cc] {
		if s == ip.String() {
			return true, nil
		}
	}
	return false, nil
}
func (f fakeGeo) GeoSite(cat, domain string) (bool, error) {
	for _, d := range f.site[cat] {
		if d == domain {
			return true, nil
		}
	}
	return false, nil
}

func baseState() model.State {
	return model.State{
		Nodes: []model.Node{
			{ID: "exit1", Name: "Exit One", PublicRole: model.RoleExit},
			{ID: "exit2", Name: "Exit Two", PublicRole: model.RoleExit},
		},
		Groups: []model.Group{
			{ID: "default", Name: "default"},
			{ID: "g1", Name: "team", DefaultExitID: "exit1"},
		},
	}
}

func ips(ss ...string) []netip.Addr {
	var out []netip.Addr
	for _, s := range ss {
		out = append(out, netip.MustParseAddr(s))
	}
	return out
}

func TestSimulateDomainGuardDirect(t *testing.T) {
	st := baseState()
	st.RoutePolicies = []model.RoutePolicy{
		{ID: "p1", Name: "ru-domains", Tier: model.TierGuard, Action: model.ActionDirect,
			AppliesToGroupID: "g1", MatchDomains: []string{"ru"}},
	}
	r := Simulate(st, "g1", "ya.ru", nil, fakeGeo{})
	if r.Decision != "direct" || r.DecidedBy != "ru-domains" {
		t.Fatalf("want direct by ru-domains, got %s by %s", r.Decision, r.DecidedBy)
	}
	// guru must NOT match suffix "ru"
	r = Simulate(st, "g1", "guru.com", nil, fakeGeo{})
	if r.Decision != "exit" || r.ExitNodeID != "exit1" {
		t.Fatalf("guru.com should fall to group default exit1, got %s/%s", r.Decision, r.ExitNodeID)
	}
}

func TestSimulateGeoIP(t *testing.T) {
	st := baseState()
	st.RoutePolicies = []model.RoutePolicy{
		{ID: "p1", Name: "ru-geo", Tier: model.TierGuard, Action: model.ActionDirect,
			AppliesToGroupID: "g1", MatchGeoIP: []string{"ru"}},
	}
	geo := fakeGeo{ip: map[string][]string{"ru": {"5.255.255.5"}}}
	// RU IP -> direct
	if r := Simulate(st, "g1", "5.255.255.5", nil, geo); r.Decision != "direct" {
		t.Fatalf("RU ip want direct, got %s", r.Decision)
	}
	// DE IP -> not RU -> group default exit
	if r := Simulate(st, "g1", "188.40.0.1", nil, geo); r.Decision != "exit" || r.ExitNodeID != "exit1" {
		t.Fatalf("DE ip want exit1, got %s/%s", r.Decision, r.ExitNodeID)
	}
}

func TestSimulateAndSemantics(t *testing.T) {
	st := baseState()
	// domain ru AND geoip us -> intersection
	st.RoutePolicies = []model.RoutePolicy{
		{ID: "p1", Name: "ru-and-us", Tier: model.TierGuard, Action: model.ActionDirect,
			AppliesToGroupID: "g1", MatchDomains: []string{"ru"}, MatchGeoIP: []string{"us"}},
	}
	geo := fakeGeo{ip: map[string][]string{"us": {"8.8.8.8"}}}
	// ya.ru resolves to a US ip -> both true -> direct
	if r := Simulate(st, "g1", "ya.ru", ips("8.8.8.8"), geo); r.Decision != "direct" {
		t.Fatalf("ru+us both true want direct, got %s (%s)", r.Decision, r.Reason)
	}
	// ya.ru resolves to a non-US ip -> geo false -> no match -> group default
	if r := Simulate(st, "g1", "ya.ru", ips("1.2.3.4"), geo); r.Decision != "exit" {
		t.Fatalf("ru+(not us) want fall-through to exit, got %s", r.Decision)
	}
}

func TestSimulateExclusion(t *testing.T) {
	st := baseState()
	st.RoutePolicies = []model.RoutePolicy{
		{ID: "p1", Name: "ru-domains", Tier: model.TierGuard, Action: model.ActionDirect,
			AppliesToGroupID: "g1", MatchDomains: []string{"ru"}, ExcludeDomains: []string{"bank.ru"}},
	}
	// excluded domain takes the group default exit, not direct
	if r := Simulate(st, "g1", "bank.ru", nil, fakeGeo{}); r.Decision != "exit" || r.ExitNodeID != "exit1" {
		t.Fatalf("bank.ru excluded -> exit1, got %s/%s", r.Decision, r.ExitNodeID)
	}
	// a non-excluded .ru still goes direct
	if r := Simulate(st, "g1", "ya.ru", nil, fakeGeo{}); r.Decision != "direct" {
		t.Fatalf("ya.ru want direct, got %s", r.Decision)
	}
}

// With no group default exit, an excluded domain must take the group's
// normal path (direct), NOT fall through to the policy and get blocked/exited.
func TestSimulateExclusionNoDefaultExit(t *testing.T) {
	st := baseState() // group "default" has no default_exit_id
	st.RoutePolicies = []model.RoutePolicy{
		{ID: "p1", Name: "block-danger", Tier: model.TierExit, Action: model.ActionBlock,
			AppliesToGroupID: "default", MatchDomains: []string{"danger"}, ExcludeDomains: []string{"safe.danger"}},
	}
	// The excluded domain bypasses the block and egresses locally (direct).
	if r := Simulate(st, "default", "safe.danger", nil, fakeGeo{}); r.Decision != "direct" {
		t.Fatalf("safe.danger excluded (no default exit) -> direct, got %s (by %s)", r.Decision, r.DecidedBy)
	}
	// A non-excluded danger domain is still blocked.
	if r := Simulate(st, "default", "evil.danger", nil, fakeGeo{}); r.Decision != "block" {
		t.Fatalf("evil.danger want block, got %s", r.Decision)
	}
}

// The simulator must treat a drained exit as local direct, mirroring the
// renderer's resolveExit — otherwise it reports traffic exiting a node that is in
// maintenance while the compiler actually falls back to direct.
func TestSimulateDrainedExitIsDirect(t *testing.T) {
	st := baseState()
	st.Nodes[0].Maintenance = true // exit1 drained; g1 defaults to exit1
	st.RoutePolicies = []model.RoutePolicy{
		{ID: "p1", Name: "to-exit1", Tier: model.TierExit, Action: model.ActionExit,
			ExitNodeID: "exit1", MatchDomains: []string{"example.com"}},
	}
	r := Simulate(st, "g1", "example.com", nil, fakeGeo{})
	if r.Decision != "direct" {
		t.Fatalf("drained exit1 -> direct, got %s/%s", r.Decision, r.ExitNodeID)
	}
	if len(r.Warnings) == 0 {
		t.Errorf("a drained-exit route should warn, got none")
	}
	// The group-default path to a drained exit is likewise direct.
	if r := Simulate(st, "g1", "other.com", nil, fakeGeo{}); r.Decision != "direct" {
		t.Errorf("group-default to a drained exit -> direct, got %s", r.Decision)
	}
}

func TestSimulateBlockAndOrdering(t *testing.T) {
	st := baseState()
	st.RoutePolicies = []model.RoutePolicy{
		{ID: "p1", Name: "block-gov", Tier: model.TierGuard, Priority: 100, Action: model.ActionBlock,
			MatchGeoSite: []string{"category-gov-ru"}}, // all users (no group)
		{ID: "p2", Name: "to-exit2", Tier: model.TierExit, Priority: 50, Action: model.ActionExit,
			ExitNodeID: "exit2", MatchDomains: []string{"example.com"}},
	}
	geo := fakeGeo{site: map[string][]string{"category-gov-ru": {"gov.ru"}}}
	if r := Simulate(st, "g1", "gov.ru", nil, geo); r.Decision != "block" || r.DecidedBy != "block-gov" {
		t.Fatalf("gov.ru want block by block-gov, got %s/%s", r.Decision, r.DecidedBy)
	}
	if r := Simulate(st, "g1", "example.com", nil, geo); r.Decision != "exit" || r.ExitNodeID != "exit2" {
		t.Fatalf("example.com want exit2, got %s/%s", r.Decision, r.ExitNodeID)
	}
}

// A namespace policy with "all users" applies only to groups in that namespace;
// an infra policy ("" owner) applies everywhere. Mirrors the compiler's
// auth_user scoping so the tester does not lie about cross-namespace reach.
func TestSimulateNamespaceScope(t *testing.T) {
	st := model.State{
		Groups: []model.Group{
			{ID: "g1", Name: "ns1 team", OwnerID: "ns1"},
			{ID: "g2", Name: "ns2 team", OwnerID: "ns2"},
		},
		RoutePolicies: []model.RoutePolicy{
			// ns1's "all my clients -> block example.com" (no group).
			{ID: "p1", Name: "ns1-block", Tier: model.TierExit, OwnerID: "ns1",
				Action: model.ActionBlock, MatchDomains: []string{"example.com"}},
		},
	}
	// ns1's own group: blocked.
	if r := Simulate(st, "g1", "example.com", nil, fakeGeo{}); r.Decision != "block" {
		t.Fatalf("ns1 group want block, got %s", r.Decision)
	}
	// ns2's group must NOT be reached by ns1's policy (no default exit -> direct).
	if r := Simulate(st, "g2", "example.com", nil, fakeGeo{}); r.Decision == "block" {
		t.Fatalf("ns1 policy leaked into ns2 group: got block")
	}
}

func TestSimulateAllUsersScope(t *testing.T) {
	st := baseState()
	st.RoutePolicies = []model.RoutePolicy{
		{ID: "p1", Name: "block-all-ads", Tier: model.TierGuard, Action: model.ActionBlock,
			MatchDomains: []string{"ads.example"}}, // applies to all groups
	}
	// even the bare default group (no default exit) hits the all-users block
	if r := Simulate(st, "default", "ads.example", nil, fakeGeo{}); r.Decision != "block" {
		t.Fatalf("ads.example want block for default group, got %s", r.Decision)
	}
	// unmatched + default group has no exit -> direct
	if r := Simulate(st, "default", "other.example", nil, fakeGeo{}); r.Decision != "direct" {
		t.Fatalf("unmatched in default group want direct, got %s", r.Decision)
	}
}
