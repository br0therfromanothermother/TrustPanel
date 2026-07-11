package model

import (
	"testing"
	"time"
)

func TestWorkedExampleValidates(t *testing.T) {
	if err := WorkedExample().Validate(); err != nil {
		t.Fatalf("worked example should validate: %v", err)
	}
}

func TestNodeValidate(t *testing.T) {
	tests := []struct {
		name    string
		node    Node
		wantErr bool
	}{
		{"entry ok", Node{ID: "e", PublicRole: RoleEntry}, false},
		{"exit needs dial_in", Node{ID: "x", PublicRole: RoleExit}, true},
		{"entry must not have dial_in", Node{ID: "e", PublicRole: RoleEntry, DialIn: &DialIn{Proto: DialInProtoVLESSReality, Port: 443, UUID: "u", TargetSNI: "s"}}, true},
		{"bad role", Node{ID: "n", PublicRole: "single"}, true},
		{"exit ok", Node{ID: "x", PublicRole: RoleExit, DialIn: &DialIn{Proto: DialInProtoVLESSReality, Port: 443, UUID: "u", TargetSNI: "s"}}, false},
		{"exit bad proto", Node{ID: "x", PublicRole: RoleExit, DialIn: &DialIn{Proto: "shadowsocks", Port: 443, UUID: "u", TargetSNI: "s"}}, true},
		{"exit missing sni", Node{ID: "x", PublicRole: RoleExit, DialIn: &DialIn{Proto: DialInProtoVLESSReality, Port: 443, UUID: "u"}}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.node.Validate(); (err != nil) != tt.wantErr {
				t.Fatalf("Validate() err=%v wantErr=%v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateUsername(t *testing.T) {
	good := []string{"alice", "admin-device", "user.1", "a_b@c"}
	bad := []string{"", "  ", " lead", "trail ", "with\ttab", "ctrl\x01"}
	for _, u := range good {
		if err := ValidateUsername(u); err != nil {
			t.Errorf("username %q should be valid: %v", u, err)
		}
	}
	for _, u := range bad {
		if err := ValidateUsername(u); err == nil {
			t.Errorf("username %q should be invalid", u)
		}
	}
}

// An id containing '/' or '\\' breaks the plain REST delete path, so it is
// rejected at validation.
func TestUserIDRejectsPathHostileChars(t *testing.T) {
	base := User{Username: "alice", GroupID: "g1"}
	for _, id := range []string{"a/b", "a\\b", "x/"} {
		u := base
		u.ID = id
		if err := u.Validate(); err == nil {
			t.Errorf("user id %q should be rejected", id)
		}
	}
	ok := base
	ok.ID = "a-b_c.1"
	if err := ok.Validate(); err != nil {
		t.Errorf("plain id should be valid: %v", err)
	}
}

func TestRoutePolicyValidate(t *testing.T) {
	base := RoutePolicy{ID: "p", Tier: TierExit, Action: ActionExit, ExitNodeID: "x", MatchDomains: []string{"example.com"}}
	if err := base.Validate(); err != nil {
		t.Fatalf("base exit policy should validate: %v", err)
	}

	noMatch := base
	noMatch.MatchDomains = nil
	if err := noMatch.Validate(); err == nil {
		t.Error("policy without any match target should be invalid")
	}

	exitNoNode := base
	exitNoNode.ExitNodeID = ""
	if err := exitNoNode.Validate(); err == nil {
		t.Error("exit action without exit_node_id should be invalid")
	}

	guardToExit := RoutePolicy{ID: "g", Tier: TierGuard, Action: ActionExit, ExitNodeID: "x", MatchGeoIP: []string{"ru"}}
	if err := guardToExit.Validate(); err == nil {
		t.Error("guard-tier rule routing to an exit should be invalid")
	}

	badCIDR := RoutePolicy{ID: "c", Tier: TierGuard, Action: ActionDirect, MatchCIDRs: []string{"not-a-cidr"}}
	if err := badCIDR.Validate(); err == nil {
		t.Error("invalid cidr should be rejected")
	}

	okCIDR := RoutePolicy{ID: "c", Tier: TierGuard, Action: ActionDirect, MatchCIDRs: []string{"10.0.0.0/8", "1.1.1.1"}}
	if err := okCIDR.Validate(); err != nil {
		t.Errorf("valid cidr/ip should pass: %v", err)
	}
}

func TestStateValidateCrossRefs(t *testing.T) {
	// User referencing a missing group must fail.
	s := WorkedExample()
	s.Users[0].GroupID = "ghost"
	if err := s.Validate(); err == nil {
		t.Error("user with unknown group should fail cross-ref validation")
	}

	// Policy exit pointing at an entry (not an exit) must fail.
	s = WorkedExample()
	s.RoutePolicies[1].ExitNodeID = "entryA"
	if err := s.Validate(); err == nil {
		t.Error("exit policy targeting a non-exit node should fail")
	}

	// Duplicate username must fail.
	s = WorkedExample()
	s.Users[1].Username = s.Users[0].Username
	if err := s.Validate(); err == nil {
		t.Error("duplicate username should fail")
	}
}

func TestUserActiveAndExpiry(t *testing.T) {
	now := time.Date(2026, 6, 12, 0, 0, 0, 0, time.UTC)
	past := now.Add(-time.Hour)
	future := now.Add(time.Hour)

	u := User{ID: "u", Username: "a", GroupID: "g", Enabled: true}
	if !u.Active(now) {
		t.Error("enabled user with no expiry should be active")
	}
	u.ExpiresAt = &past
	if u.Active(now) {
		t.Error("expired user should not be active")
	}
	u.ExpiresAt = &future
	if !u.Active(now) {
		t.Error("user expiring in the future should be active")
	}
	u.Enabled = false
	if u.Active(now) {
		t.Error("disabled user should not be active")
	}
}

func TestActiveUsersSorted(t *testing.T) {
	now := time.Date(2026, 6, 12, 0, 0, 0, 0, time.UTC)
	got := WorkedExample().ActiveUsers(now)
	if len(got) != 3 {
		t.Fatalf("want 3 active users, got %d", len(got))
	}
	want := []string{"admin-device", "alice", "bob"}
	for i, u := range got {
		if u.Username != want[i] {
			t.Errorf("active users not sorted: index %d = %q, want %q", i, u.Username, want[i])
		}
	}
}
