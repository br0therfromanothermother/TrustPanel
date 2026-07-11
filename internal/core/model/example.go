package model

import "time"

// WorkedExample returns the canonical topology used across tests:
// entryA (entry) + node1, node2 (exits); groups admins and everyone both
// defaulting to node2; admin-device in admins, alice/bob in everyone; a guard
// rule keeping .ru traffic on the entry, plus the exclusive routes
// "admins -> example.com -> node1" and "everyone -> example.com -> node2".
//
// It is used by render tests and as a demo seed; State.Validate passes on it.
func WorkedExample() State {
	at := time.Date(2026, 6, 12, 0, 0, 0, 0, time.UTC)
	exit := func(id, name, ip string) Node {
		return Node{
			ID: id, Name: name, PublicRole: RoleExit, PublicIPs: []string{ip},
			AgentAddr: ip + ":8443", Health: HealthHealthy, PGRole: PGNone,
			DialIn: &DialIn{
				Proto: DialInProtoVLESSReality, Port: 443,
				UUID:      "uuid-" + id,
				TargetSNI: "www.example-cdn.com",
				PublicKey: "pub-" + id, PrivKey: "priv-" + id, ShortID: "0123abcd",
			},
			CreatedAt: at, UpdatedAt: at,
		}
	}
	return State{
		ControlPlane: ControlPlane{
			ActiveNodeID: "node2", Epoch: 1, StandbyNodeIDs: []string{"node1"}, UpdatedAt: at,
		},
		Nodes: []Node{
			{
				ID: "entryA", Name: "Entry A", PublicRole: RoleEntry,
				PublicIPs: []string{"203.0.113.10"}, AgentAddr: "203.0.113.10:8443",
				Health: HealthHealthy, PGRole: PGNone, CreatedAt: at, UpdatedAt: at,
			},
			exit("node1", "Exit 1", "203.0.113.21"),
			func() Node {
				n := exit("node2", "Exit 2", "203.0.113.22")
				n.MgmtCapable = true
				n.PGRole = PGPrimary
				return n
			}(),
		},
		Groups: []Group{
			{ID: "admins", Name: "Admins", DefaultExitID: "node2", CreatedAt: at, UpdatedAt: at},
			{ID: "everyone", Name: "Everyone", DefaultExitID: "node2", CreatedAt: at, UpdatedAt: at},
		},
		Users: []User{
			{ID: "u-admin", Username: "admin-device", Secret: "s-admin", DisplayName: "Admin device", Enabled: true, GroupID: "admins", CreatedAt: at, UpdatedAt: at},
			{ID: "u-alice", Username: "alice", Secret: "s-alice", DisplayName: "Alice", Enabled: true, GroupID: "everyone", CreatedAt: at, UpdatedAt: at},
			{ID: "u-bob", Username: "bob", Secret: "s-bob", DisplayName: "Bob", Enabled: true, GroupID: "everyone", CreatedAt: at, UpdatedAt: at},
		},
		RoutePolicies: []RoutePolicy{
			{
				ID: "guard-ru", Name: "Keep .ru on entry", Priority: 100, Tier: TierGuard,
				MatchDomains: []string{".ru", ".рф", ".su"}, MatchGeoIP: []string{"ru"},
				Action: ActionDirect, CreatedAt: at, UpdatedAt: at,
			},
			{
				ID: "admin-example-node1", Name: "Admin example.com via node1", Priority: 90, Tier: TierExit,
				AppliesToGroupID: "admins", MatchDomains: []string{"example.com"},
				Action: ActionExit, ExitNodeID: "node1", FallbackKind: FallbackBlock,
				CreatedAt: at, UpdatedAt: at,
			},
			{
				ID: "example-others-node2", Name: "Others example.com via node2", Priority: 80, Tier: TierExit,
				AppliesToGroupID: "everyone", MatchDomains: []string{"example.com"},
				Action: ActionExit, ExitNodeID: "node2", FallbackKind: FallbackBlock,
				CreatedAt: at, UpdatedAt: at,
			},
		},
		Domains: []Domain{
			{ID: "d-entryA", Hostname: "cdn.example.com", Purpose: PurposeMainFallback, NodeID: "entryA", TLSStatus: "valid", CreatedAt: at, UpdatedAt: at},
		},
	}
}
