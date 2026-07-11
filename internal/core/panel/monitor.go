package panel

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"trustpanel/internal/core/cluster"
	"trustpanel/internal/core/model"
	"trustpanel/internal/core/store"
	"trustpanel/internal/core/watchdog"
)

// ---- external entry edge probe ----

// RunEdgeProbeLoop periodically probes each entry node's public :443 from the
// panel (off-node), so the UI dot reflects external reachability, not just the
// node's self-report. interval <= 0 disables it.
func (p *Panel) RunEdgeProbeLoop(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		return
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	p.ProbeEntriesOnce(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			p.ProbeEntriesOnce(ctx)
		}
	}
}

// ProbeEntriesOnce TCP-dials :443 on each entry node's first public IP and
// records the result. Reuses watchdog.TCPProber.
func (p *Panel) ProbeEntriesOnce(ctx context.Context) {
	st, err := p.store.LoadState(ctx)
	if err != nil {
		log.Printf("edge probe: load state: %v", err)
		return
	}
	live := make(map[string]bool, len(st.Nodes))
	for _, n := range st.Nodes {
		if !n.IsEntry() || len(n.PublicIPs) == 0 {
			continue
		}
		live[n.ID] = true
		addr := net.JoinHostPort(n.PublicIPs[0], "443")
		rec := edgeRecord{At: p.now(), Ok: true}
		if err := (watchdog.TCPProber{Address: addr, Timeout: 5 * time.Second}).Probe(ctx); err != nil {
			rec.Ok = false
			rec.Error = err.Error()
		}
		p.edgeMu.Lock()
		p.lastEdge[n.ID] = rec
		p.edgeMu.Unlock()
		// An entry unreachable from outside means clients can't connect even if the
		// agent is up — service impact, so Critical; recovery is silent.
		if p.edgeMon != nil {
			down := watchdog.MsgEntryUnreachable(n.Name, rec.Error)
			up := watchdog.MsgEntryReachable(n.Name)
			p.edgeMon.Observe(ctx, "edge:"+n.ID, rec.Ok, watchdog.SeverityCritical, down, up)
		}
	}
	// Drop records for nodes that are gone or no longer entries (e.g. flipped
	// entry->exit by a convert), so a frozen probe result can't linger.
	p.edgeMu.Lock()
	for id := range p.lastEdge {
		if !live[id] {
			delete(p.lastEdge, id)
		}
	}
	p.edgeMu.Unlock()
	if p.edgeMon != nil {
		p.edgeMon.Forget(prefixKeys("edge:", live))
	}
}

// prefixKeys turns a set of ids into the prefixed keys a Monitor tracks, so
// Forget drops state for ids no longer live.
func prefixKeys(prefix string, ids map[string]bool) map[string]bool {
	out := make(map[string]bool, len(ids))
	for id := range ids {
		out[prefix+id] = true
	}
	return out
}

// ---- replication health monitor ----

// RunReplicationProbeLoop periodically checks each expected standby's physical
// replication slot on the primary (the node the panel runs on), so the UI can
// show an HA warning when a standby's Postgres has died or fallen behind while
// its data plane keeps serving. interval <= 0 disables it.
func (p *Panel) RunReplicationProbeLoop(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		return
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	p.ProbeReplicationOnce(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			p.ProbeReplicationOnce(ctx)
		}
	}
}

// ProbeReplicationOnce reads pg_replication_slots on the local primary and records
// each expected standby's slot health (active + bytes behind), keyed by node id.
// A standby with no slot at all is recorded Missing. Records for nodes no longer
// listed as standbys are dropped so a stale entry can't linger.
func (p *Panel) ProbeReplicationOnce(ctx context.Context) {
	st, err := p.store.LoadState(ctx)
	if err != nil {
		log.Printf("replication probe: load state: %v", err)
		return
	}
	expected := st.ControlPlane.StandbyNodeIDs
	if len(expected) == 0 {
		// No HA standbys configured — clear any stale records and skip the query.
		p.replMu.Lock()
		p.lastRepl = map[string]replRecord{}
		p.replMu.Unlock()
		if p.replMon != nil {
			p.replMon.Forget(nil)
		}
		return
	}
	slots, err := p.store.ReplicationSlots(ctx)
	if err != nil {
		log.Printf("replication probe: read slots: %v", err)
		return
	}
	bySlot := make(map[string]store.ReplSlot, len(slots))
	for _, s := range slots {
		bySlot[s.Name] = s
	}
	nameByID := make(map[string]string, len(st.Nodes))
	for _, n := range st.Nodes {
		nameByID[n.ID] = n.Name
	}
	now := p.now()
	live := make(map[string]bool, len(expected))
	recs := make(map[string]replRecord, len(expected))
	p.replMu.Lock()
	for _, id := range expected {
		live[id] = true
		rec := replRecord{At: now}
		if s, ok := bySlot[cluster.SlotName(id)]; ok {
			rec.Active, rec.BytesBehind = s.Active, s.BytesBehind
		} else {
			rec.Missing = true
		}
		p.lastRepl[id] = rec
		recs[id] = rec
	}
	for id := range p.lastRepl {
		if !live[id] {
			delete(p.lastRepl, id)
		}
	}
	p.replMu.Unlock()
	p.observeReplication(ctx, recs, nameByID, st)
}

// replFarBehindBytes is how far a standby's replication slot may lag before it is
// treated as unhealthy (HA degraded). The data plane is unaffected, so this is a
// silent (Low) alert.
const replFarBehindBytes int64 = 128 << 20 // 128 MiB

// observeReplication feeds each expected standby's slot health to the replication
// monitor. Missing/inactive/far-behind is a control-plane (HA) concern only — the
// standby's VPN data plane keeps serving — so it is Low (silent); recovery too.
func (p *Panel) observeReplication(ctx context.Context, recs map[string]replRecord, nameByID map[string]string, st model.State) {
	if p.replMon == nil {
		return
	}
	keep := make(map[string]bool, len(recs))
	for id, rec := range recs {
		key := "repl:" + id
		keep[key] = true
		name := nameByID[id]
		if name == "" {
			name = id
		}
		healthy, why := replHealthy(rec)
		// "data plane unaffected" only holds when the standby isn't ALSO carrying
		// live egress. If it is a live exit, a degraded slot may coincide with a
		// dead node blackholing its groups, so don't assert the data plane is fine —
		// point at the node's own health, which the critical node-down alert covers.
		liveExitFor := strings.Join(egressDependents(st, id), ", ")
		down := watchdog.MsgReplicationDegraded(name, why, liveExitFor)
		up := watchdog.MsgReplicationRestored(name)
		p.replMon.Observe(ctx, key, healthy, watchdog.SeverityLow, down, up)
	}
	p.replMon.Forget(keep)
}

// replHealthy classifies a standby slot record; the reason string is used in the
// alert when unhealthy.
func replHealthy(rec replRecord) (ok bool, why string) {
	switch {
	case rec.Missing:
		return false, "no replication slot (never provisioned or dropped)"
	case !rec.Active:
		return false, "slot inactive (standby not streaming)"
	case rec.BytesBehind != nil && *rec.BytesBehind > replFarBehindBytes:
		return false, fmt.Sprintf("%d MiB behind", *rec.BytesBehind>>20)
	default:
		return true, ""
	}
}

// ---- Telegram availability monitor ----

// RunBotHealthLoop periodically checks whether the configured bot/alert tokens
// can reach Telegram (getMe). It deliberately runs on a slow cadence and only
// logs on a status *transition*, so a down/revoked channel is noticed without
// spamming. interval <= 0 disables it.
func (p *Panel) RunBotHealthLoop(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		return
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	p.CheckBotHealthOnce(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			p.CheckBotHealthOnce(ctx)
		}
	}
}

// CheckBotHealthOnce calls Telegram getMe for the management and alert tokens
// and records the classified status. Logs only when a status changes.
func (p *Panel) CheckBotHealthOnce(ctx context.Context) {
	s, err := p.store.GetSettings(ctx)
	if err != nil {
		log.Printf("bot health: load settings: %v", err)
		return
	}
	p.recordBotHealth(ctx, "bot", s.Bot.Enabled, s.Bot.Token)
	p.recordBotHealth(ctx, "alert", s.Alert.Enabled, s.Alert.Token)

	// Dead-man stamp: if the α (primary-side) sender can currently reach Telegram,
	// record it. This replicates to the standby, whose watchdog raises β if the
	// stamp goes stale while the box is alive (serve dead / TG unreachable / α
	// token revoked — all of which stop the stamp). Only stamp on a confirmed-ok
	// α channel, so a broken α naturally lets the stamp age out.
	if p.botHealthFor(alphaKey(s)).Status == "ok" {
		if err := p.store.StampAlertHeartbeat(ctx); err != nil {
			log.Printf("bot health: stamp alert heartbeat: %v", err)
		}
	}
}

// alphaKey returns the bot-health key whose token the α sender uses: the
// management bot when configured (it does double duty), else the dedicated alert
// bot. Mirrors alertSenderCreds.
func alphaKey(s model.Settings) string {
	if s.Bot.Enabled && s.Bot.Token != "" {
		return "bot"
	}
	return "alert"
}

func (p *Panel) recordBotHealth(ctx context.Context, key string, enabled bool, token string) {
	status, detail := "unconfigured", ""
	if enabled && token != "" {
		status, detail = telegramGetMe(ctx, token)
	}
	p.botHealthMu.Lock()
	prev, had := p.botHealth[key]
	p.botHealth[key] = botHealthRecord{Status: status, Detail: detail, At: p.now()}
	p.botHealthMu.Unlock()
	// Push on transition. A configured channel that stops answering Telegram is a
	// degraded-but-not-blind condition (the other channel + the standby watchdog
	// still observe), so it is Low (silent). "unconfigured"/"ok" are healthy.
	// When the *management* token (key "bot" = the α sender) is the one down,
	// this send goes through that same dead token and only logs — the standby
	// watchdog (β) reports it cross-node.
	if p.botMon != nil {
		healthy := status == "ok" || status == "unconfigured"
		down := watchdog.MsgBotChannelDegraded(botChannelLabel(key), status, detail)
		up := watchdog.MsgBotChannelRestored(botChannelLabel(key), status)
		p.botMon.Observe(ctx, "bot:"+key, healthy, watchdog.SeverityLow, down, up)
	}
	if !had || prev.Status != status {
		switch status {
		case "ok", "unconfigured":
			if had { // don't announce the very first "unconfigured" at boot
				log.Printf("bot health: %s channel -> %s", key, status)
			}
		default:
			log.Printf("bot health: %s channel -> %s (%s)", key, status, detail)
		}
	}
}

// botChannelLabel renders the human name of a bot-health key for alert text.
func botChannelLabel(key string) string {
	switch key {
	case "bot":
		return "management bot (commands)"
	case "alert":
		return "alert bot (β, standby)"
	default:
		return key + " channel"
	}
}

// telegramGetMe classifies a token's reachability: ok | unauthorized (revoked)
// | unreachable. It is a tiny standalone call (no client state needed).
func telegramGetMe(ctx context.Context, token string) (status, detail string) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	url := fmt.Sprintf("https://api.telegram.org/bot%s/getMe", token)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "unreachable", redactToken(err.Error(), token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "unreachable", redactToken(err.Error(), token)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusOK:
		return "ok", ""
	case resp.StatusCode == http.StatusUnauthorized:
		return "unauthorized", "token rejected (revoked or wrong)"
	default:
		return "unreachable", fmt.Sprintf("HTTP %d", resp.StatusCode)
	}
}

// redactToken strips a Telegram bot token out of transport error text before
// it's surfaced anywhere (alerts, logs). A failed request's error embeds the
// full request URL verbatim (https://api.telegram.org/bot<TOKEN>/getMe), which
// would otherwise leak the token in cleartext.
func redactToken(s, token string) string {
	if token == "" {
		return s
	}
	return strings.ReplaceAll(s, token, "<redacted>")
}
