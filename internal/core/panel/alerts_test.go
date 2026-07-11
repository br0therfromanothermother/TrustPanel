package panel

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"trustpanel/internal/core/model"
	"trustpanel/internal/core/watchdog"
)

func TestAlertSenderCreds(t *testing.T) {
	cases := []struct {
		name           string
		s              model.Settings
		wantTok, wantC string
	}{
		{
			name:    "management bot does double duty (α)",
			s:       model.Settings{Bot: model.BotSettings{Enabled: true, Token: "MGMT"}, Alert: model.AlertSettings{Enabled: true, ChatID: "-100"}},
			wantTok: "MGMT", wantC: "-100",
		},
		{
			name:    "falls back to dedicated alert token when no mgmt bot",
			s:       model.Settings{Alert: model.AlertSettings{Enabled: true, Token: "ALERT", ChatID: "-100"}},
			wantTok: "ALERT", wantC: "-100",
		},
		{
			name:    "alert channel disabled -> no creds",
			s:       model.Settings{Bot: model.BotSettings{Enabled: true, Token: "MGMT"}, Alert: model.AlertSettings{Enabled: false, ChatID: "-100"}},
			wantTok: "", wantC: "",
		},
		{
			name:    "management bot stays primary even with a backup (β) token set",
			s:       model.Settings{Bot: model.BotSettings{Enabled: true, Token: "MGMT"}, Alert: model.AlertSettings{Enabled: true, Token: "BETA", ChatID: "-100"}},
			wantTok: "MGMT", wantC: "-100",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tok, chat := alertSenderCreds(c.s)
			if tok != c.wantTok || chat != c.wantC {
				t.Fatalf("alertSenderCreds = (%q,%q), want (%q,%q)", tok, chat, c.wantTok, c.wantC)
			}
		})
	}
}

// TestTestChannelValidation covers the /api/settings/test-channel guard branches
// (incl. the separate/backup alert-bot config checks) without contacting Telegram
// — each case is rejected before any network call.
func TestTestChannelValidation(t *testing.T) {
	p, st, _ := newPanel(t)
	ctx := context.Background()
	hash, _ := HashPassword("supersecret")
	if err := st.UpsertAdmin(ctx, "boss", hash); err != nil {
		t.Fatal(err)
	}
	c, url := newClient(t, p)
	tok := login(t, c, url, "boss", "supersecret")

	cases := []struct{ channel, wantErr string }{
		{"bot", "management bot is not configured"},
		{"alert", "alert channel is not configured"},
		{"backup", "backup channel is not configured"},
		{"nope", "unknown channel"},
	}
	for _, tc := range cases {
		t.Run(tc.channel, func(t *testing.T) {
			code, body := doTok(t, c, http.MethodPost, url+"/api/settings/test-channel", tok, map[string]string{"channel": tc.channel})
			if code != http.StatusBadRequest {
				t.Fatalf("channel %q: want 400, got %d (%s)", tc.channel, code, body)
			}
			if !strings.Contains(string(body), tc.wantErr) {
				t.Fatalf("channel %q: body %q missing %q", tc.channel, body, tc.wantErr)
			}
		})
	}
}

func TestReplHealthy(t *testing.T) {
	behind := func(b int64) *int64 { return &b }
	cases := []struct {
		name   string
		rec    replRecord
		ok     bool
		whyHas string
	}{
		{"missing", replRecord{Missing: true}, false, "no replication slot"},
		{"inactive", replRecord{Active: false}, false, "inactive"},
		{"far behind", replRecord{Active: true, BytesBehind: behind(replFarBehindBytes + 1)}, false, "behind"},
		{"healthy near", replRecord{Active: true, BytesBehind: behind(1024)}, true, ""},
		{"healthy unknown lag", replRecord{Active: true}, true, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ok, why := replHealthy(c.rec)
			if ok != c.ok {
				t.Fatalf("replHealthy ok=%v want %v (why=%q)", ok, c.ok, why)
			}
			if c.whyHas != "" && !strings.Contains(why, c.whyHas) {
				t.Fatalf("why %q missing %q", why, c.whyHas)
			}
		})
	}
}

func TestPickFailoverTarget(t *testing.T) {
	p := &Panel{}
	// e1 is the dead exit; e2/e3/e4 are candidates; ent is an entry (ineligible).
	exit := func(id string) model.Node { return model.Node{ID: id, PublicRole: model.RoleExit} }
	base := []model.Node{exit("e1"), exit("e2"), exit("e3"), exit("e4"),
		{ID: "ent", PublicRole: model.RoleEntry}}
	healthy := func(ids ...string) map[string]model.NodeHealth {
		h := map[string]model.NodeHealth{}
		for _, id := range ids {
			h[id] = model.HealthHealthy
		}
		return h
	}
	// Group counts steer the "most groups" tie-break: e2 carries 2, e3 carries 1.
	groups := []model.Group{
		{ID: "g1", DefaultExitID: "e2"}, {ID: "g2", DefaultExitID: "e2"},
		{ID: "g3", DefaultExitID: "e3"},
	}

	t.Run("prefers the exit already carrying the most groups", func(t *testing.T) {
		st := model.State{Nodes: base, Groups: groups}
		got := p.pickFailoverTarget(st, "e1", healthy("e2", "e3", "e4"))
		if got == nil || got.ID != "e2" {
			t.Fatalf("want e2 (2 groups), got %+v", got)
		}
	})
	t.Run("breaks a tie on lowest id", func(t *testing.T) {
		st := model.State{Nodes: base, Groups: []model.Group{{ID: "g1", DefaultExitID: "e3"}, {ID: "g2", DefaultExitID: "e4"}}}
		got := p.pickFailoverTarget(st, "e1", healthy("e3", "e4"))
		if got == nil || got.ID != "e3" {
			t.Fatalf("want e3 (tie -> lowest id), got %+v", got)
		}
	})
	t.Run("skips drained and unhealthy candidates", func(t *testing.T) {
		nodes := []model.Node{exit("e1"), exit("e2"), exit("e3")}
		nodes[1].Maintenance = true // e2 drained
		st := model.State{Nodes: nodes, Groups: groups}
		got := p.pickFailoverTarget(st, "e1", healthy("e2", "e3")) // e2 healthy but drained
		if got == nil || got.ID != "e3" {
			t.Fatalf("want e3 (e2 drained), got %+v", got)
		}
		// With only a degraded/unknown alternative, there is no target.
		if got := p.pickFailoverTarget(st, "e1", healthy("e2")); got != nil {
			t.Fatalf("want nil (e2 drained, e3 not healthy), got %+v", got)
		}
	})
	t.Run("no exit but the dead one", func(t *testing.T) {
		st := model.State{Nodes: []model.Node{exit("e1"), {ID: "ent", PublicRole: model.RoleEntry}}}
		if got := p.pickFailoverTarget(st, "e1", healthy("e1", "ent")); got != nil {
			t.Fatalf("want nil, got %+v", got)
		}
	})
}

func TestObserveNodeAlertsCriticalAndRecovers(t *testing.T) {
	cap := &captureAlerter{}
	p := &Panel{}
	p.SetMonitorAlerts(cap)
	n := model.Node{ID: "n1", Name: "exit1", PublicRole: model.RoleExit}
	// The exit is the live egress for the "default" group.
	st := model.State{
		Nodes:  []model.Node{n},
		Groups: []model.Group{{ID: "default", Name: "Default", DefaultExitID: "n1"}},
	}
	ctx := context.Background()
	// Two unhealthy samples (threshold 2) -> one Critical DOWN, deduped.
	p.observeNode(ctx, n, false, context.DeadlineExceeded, st)
	p.observeNode(ctx, n, false, context.DeadlineExceeded, st)
	p.observeNode(ctx, n, false, context.DeadlineExceeded, st)
	if len(cap.msgs) != 1 || !strings.Contains(cap.msgs[0], watchdog.GlyphDown) {
		t.Fatalf("want one DOWN alert, got %v", cap.msgs)
	}
	// A dead active exit flags the egress impact, but the broadcast leg must NOT
	// name the dependent group — group names are namespace-private and this leg
	// fans out to every account (the per-namespace failover alert names them). It
	// still identifies the exit and says its groups lose egress.
	if !strings.Contains(cap.msgs[0], "EXIT node") || !strings.Contains(cap.msgs[0], "lose egress") {
		t.Errorf("exit-down alert should flag the egress impact, got %q", cap.msgs[0])
	}
	if strings.Contains(cap.msgs[0], "Default") {
		t.Errorf("broadcast exit-down alert must NOT leak the group name across namespaces, got %q", cap.msgs[0])
	}
	if cap.sevs[0] != watchdog.SeverityCritical {
		t.Fatalf("node down must be critical, got %v", cap.sevs[0])
	}
	// Recovery -> silent UP.
	p.observeNode(ctx, n, true, nil, st)
	if len(cap.msgs) != 2 || !strings.Contains(cap.msgs[1], watchdog.GlyphUp) {
		t.Fatalf("want recovery alert, got %v", cap.msgs)
	}
	if cap.sevs[1] != watchdog.SeverityLow {
		t.Fatalf("recovery must be silent, got %v", cap.sevs[1])
	}
}

// TestForgetPreservesDownCounter reproduces the real poll cadence: each stats
// poll observes every node's health and THEN forgets absent nodes. A node that
// stays down across polls must keep its fail counter and eventually alert. The
// pre-fix code forgot with bare ids while observeNode keyed "node:"+id, so every
// poll wiped the counter and the node-down alert never fired.
func TestForgetPreservesDownCounter(t *testing.T) {
	cap := &captureAlerter{}
	p := &Panel{}
	p.nodeMon = watchdog.NewMonitor(cap, 2) // ~2 polls before declaring a node down
	n := model.Node{ID: "n1", Name: "exit1", PublicRole: model.RoleExit}
	st := model.State{Nodes: []model.Node{n}}
	live := map[string]bool{"n1": true} // n1 stays registered even while down
	ctx := context.Background()
	// Poll 1: down sample, then the inter-poll forget of live nodes.
	p.observeNode(ctx, n, false, context.DeadlineExceeded, st)
	p.forgetAbsentNodes(live)
	if len(cap.msgs) != 0 {
		t.Fatalf("one down sample must not alert yet, got %v", cap.msgs)
	}
	// Poll 2: second down sample survives the forget and crosses the threshold.
	p.observeNode(ctx, n, false, context.DeadlineExceeded, st)
	p.forgetAbsentNodes(live)
	if len(cap.msgs) != 1 || !strings.Contains(cap.msgs[0], watchdog.GlyphDown) {
		t.Fatalf("two down polls must alert once; forget must not wipe the counter, got %v", cap.msgs)
	}
}

func TestObserveReplicationSilentDegrade(t *testing.T) {
	cap := &captureAlerter{}
	p := &Panel{}
	p.SetMonitorAlerts(cap)
	ctx := context.Background()
	recs := map[string]replRecord{"sb": {Missing: true}}
	names := map[string]string{"sb": "exit2"}
	// The standby is NOT a live exit here, so the alert may claim the data plane
	// is unaffected.
	st := model.State{}
	// Threshold 2 for repl.
	p.observeReplication(ctx, recs, names, st)
	p.observeReplication(ctx, recs, names, st)
	if len(cap.msgs) != 1 || !strings.Contains(cap.msgs[0], "exit2") {
		t.Fatalf("want one degrade alert naming the standby, got %v", cap.msgs)
	}
	if !strings.Contains(cap.msgs[0], "the data plane is unaffected") {
		t.Errorf("a non-exit standby's degrade should still say data plane unaffected, got %q", cap.msgs[0])
	}
	if cap.sevs[0] != watchdog.SeverityLow {
		t.Fatalf("replication degrade must be silent (HA only), got %v", cap.sevs[0])
	}
}

// TestObserveReplicationLiveExitNotUnaffected checks that when the degraded
// standby is ALSO the live exit for a group, the alert must NOT claim the data
// plane is unaffected (that false reassurance masks a total outage).
func TestObserveReplicationLiveExitNotUnaffected(t *testing.T) {
	cap := &captureAlerter{}
	p := &Panel{}
	p.SetMonitorAlerts(cap)
	ctx := context.Background()
	recs := map[string]replRecord{"n1": {Missing: true}}
	names := map[string]string{"n1": "exit1"}
	st := model.State{
		Nodes:  []model.Node{{ID: "n1", Name: "exit1", PublicRole: model.RoleExit}},
		Groups: []model.Group{{ID: "default", Name: "Default", DefaultExitID: "n1"}},
	}
	p.observeReplication(ctx, recs, names, st)
	p.observeReplication(ctx, recs, names, st)
	if len(cap.msgs) != 1 {
		t.Fatalf("want one degrade alert, got %v", cap.msgs)
	}
	if strings.Contains(cap.msgs[0], "the data plane is unaffected") {
		t.Errorf("a live-exit standby's degrade must not claim data plane unaffected, got %q", cap.msgs[0])
	}
	if !strings.Contains(cap.msgs[0], "Default") {
		t.Errorf("should point at the affected group, got %q", cap.msgs[0])
	}
}
