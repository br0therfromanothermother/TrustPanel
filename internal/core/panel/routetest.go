package panel

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"trustpanel/internal/core/render"
)

// singboxGeo evaluates geoip/geosite membership with the real sing-box binary
// against the panel's cached .srs rule-sets, so the route tester gives the same
// geo verdict a node would. An empty bin/dir (provisioning off) makes every geo
// lookup an error, which the simulator surfaces as "not evaluable here".
// RuleSetFetcher fetches/caches a .srs by tag (e.g. rulesets.Provider). When
// present the tester can evaluate any valid category on demand, not only those
// already used by a policy.
type RuleSetFetcher interface {
	Get(ctx context.Context, tag string) ([]byte, error)
}

type singboxGeo struct {
	bin   string // sing-box binary path
	dir   string // dir holding geoip-*.srs / geosite-*.srs
	fetch RuleSetFetcher
}

// NewSingboxGeo builds the route-tester geo evaluator. bin is the sing-box
// binary path, dir holds the cached geoip-*/geosite-*.srs rule-sets, and fetch
// (optional) downloads missing ones on demand into dir.
func NewSingboxGeo(bin, dir string, fetch RuleSetFetcher) render.GeoMatcher {
	return singboxGeo{bin: bin, dir: dir, fetch: fetch}
}

func (g singboxGeo) GeoIP(country string, ip netip.Addr) (bool, error) {
	return g.match("geoip-"+strings.ToLower(country), ip.String())
}

func (g singboxGeo) GeoSite(category, domain string) (bool, error) {
	return g.match("geosite-"+strings.ToLower(category), domain)
}

func (g singboxGeo) match(tag, arg string) (bool, error) {
	if g.bin == "" || g.dir == "" {
		return false, fmt.Errorf("geo matching unavailable")
	}
	path := filepath.Join(g.dir, tag+".srs")
	if _, err := os.Stat(path); err != nil && g.fetch != nil {
		// best-effort download into dir (panel runs on the uncensored exit)
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		_, _ = g.fetch.Get(ctx, tag)
		cancel()
	}
	if _, err := os.Stat(path); err != nil {
		return false, fmt.Errorf("rule-set %s not available", tag)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// `rule-set match` exits 0 on both hit and miss; it prints "match rules.[…]"
	// only on a hit. A non-zero exit means a real error (bad srs/arg).
	out, err := exec.CommandContext(ctx, g.bin, "rule-set", "match", path, arg, "-f", "binary").CombinedOutput()
	if err != nil {
		return false, fmt.Errorf("rule-set match %s: %v", tag, err)
	}
	return strings.Contains(string(out), "match rules"), nil
}

type routeTestReq struct {
	GroupID string `json:"group_id"`
	Target  string `json:"target"`
}

// handleRouteTest simulates routing for a (group, target) pair: it resolves the
// target (DNS for a domain), then walks the compiled rule order to report which
// node the connection egresses through and which rule decided it.
func (p *Panel) handleRouteTest(w http.ResponseWriter, r *http.Request) {
	var req routeTestReq
	if !decode(w, r, &req) {
		return
	}
	target := strings.TrimSpace(req.Target)
	if target == "" {
		writeErr(w, http.StatusBadRequest, "target (IP or domain) is required")
		return
	}
	st, err := p.store.LoadState(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	var ips []netip.Addr
	var resolveWarn string
	if addr, err := netip.ParseAddr(target); err == nil {
		ips = []netip.Addr{addr}
	} else {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		got, lerr := net.DefaultResolver.LookupNetIP(ctx, "ip", target)
		if lerr != nil {
			resolveWarn = "could not resolve " + target + ": " + lerr.Error() + " — geoip/CIDR rules cannot be evaluated"
		}
		for _, a := range got {
			ips = append(ips, a.Unmap())
		}
	}

	res := render.Simulate(st, req.GroupID, target, ips, p.geo)
	if resolveWarn != "" {
		res.Warnings = append([]string{resolveWarn}, res.Warnings...)
	}
	writeJSON(w, http.StatusOK, res)
}
