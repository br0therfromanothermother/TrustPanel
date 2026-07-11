// Package clientcfg builds per-entry TrustTunnel client configs for a user.
// Native clients are single-endpoint, so each config targets one entry node;
// the operator hands out one per entry. The same user credentials are reused
// across entries — only the endpoint differs.
package clientcfg

import (
	"encoding/base64"
	"fmt"
	"sort"
	"strings"
	"time"

	"trustpanel/internal/core/model"
	"trustpanel/internal/paths"
)

// Config is a rendered client config for one (user, entry) pair.
type Config struct {
	UserID   string   `json:"user_id"`
	Username string   `json:"username"`
	EntryID  string   `json:"entry_id"`
	Hostname string   `json:"hostname"`
	Filename string   `json:"filename"`
	TOML     string   `json:"config_toml"`
	DeepLink string   `json:"deep_link"` // tt://?<payload> URI (carries credentials)
	QRLink   string   `json:"qr_link"`   // https://<host><qrpath>#tt=<payload> landing link
	Active   bool     `json:"active"`
	Warnings []string `json:"warnings,omitempty"`
}

// Options tunes client-side defaults.
type Options struct {
	DNSUpstreams []string // default ["tls://1.1.1.1"]
	Port         int      // endpoint port, default 443
}

func (o *Options) normalize() {
	if len(o.DNSUpstreams) == 0 {
		o.DNSUpstreams = []string{"tls://1.1.1.1"}
	}
	if o.Port <= 0 {
		o.Port = 443
	}
}

// Build renders the client config for a user pointed at a specific entry node.
func Build(state model.State, userID, entryNodeID string, opts Options, at time.Time) (Config, error) {
	opts.normalize()

	user, ok := findUser(state, userID)
	if !ok {
		return Config{}, fmt.Errorf("user %q not found", userID)
	}
	entry, ok := state.NodeByID(entryNodeID)
	if !ok {
		return Config{}, fmt.Errorf("entry node %q not found", entryNodeID)
	}
	if !entry.IsEntry() {
		return Config{}, fmt.Errorf("node %q is not an entry node", entryNodeID)
	}
	hostname, warnings := entryHostname(state, entry.ID)
	if entry.Maintenance {
		warnings = append(warnings, "entry is in maintenance; hand out a config for another entry until it returns")
	}
	exclusions := clientExclusions(state, user)

	cfg := Config{
		UserID:   user.ID,
		Username: user.Username,
		EntryID:  entry.ID,
		Hostname: hostname,
		Filename: fmt.Sprintf("trusttunnel-%s-%s.toml", user.Username, entry.ID),
		Active:   user.Active(at),
		Warnings: warnings,
	}
	if !cfg.Active {
		cfg.Warnings = append(cfg.Warnings, "user is disabled or expired; this config will not authenticate until re-enabled")
	}
	cfg.TOML = renderClientTOML(user, hostname, exclusions, opts)

	// A user with no secret can't authenticate, and an empty-password deep link is
	// rejected by ParseDeepLink on import — emitting one would be a silently broken
	// link. Warn and skip the deep link / QR instead of shipping a dead one.
	if strings.TrimSpace(user.Secret) == "" {
		cfg.Warnings = append(cfg.Warnings, "this client has no secret set — the deep link/QR are omitted and the config will not authenticate; set a password and re-export")
		return cfg, nil
	}
	// Deep link: the same credentials encoded as a tt://? URI (DEEP_LINK.md),
	// plus a landing link that renders it as a QR entirely in the browser on our
	// own camouflage origin (the #tt= fragment never reaches the server).
	payload := buildDeepLinkPayload(user, hostname, entryAddresses(entry, hostname, opts.Port), opts.DNSUpstreams)
	cfg.DeepLink = "tt://?" + payload
	// The QR landing page is hosted on the public-facing apex (fallback-site) so
	// the shared link is the plain brand domain, not the connection portal.
	cfg.QRLink = "https://" + landingHostname(state, entry.ID, hostname) + paths.DefaultQRPath + "#tt=" + payload
	return cfg, nil
}

// landingHostname returns the host to serve the QR/deeplink landing page on: the
// entry's public fallback-site domain (the apex, e.g. example.com) if present,
// otherwise the given fallback (the main-fallback/portal hostname).
func landingHostname(state model.State, nodeID, fallback string) string {
	for _, d := range state.Domains {
		if d.NodeID == nodeID && d.Purpose == model.PurposeFallbackSite && d.Hostname != "" {
			return d.Hostname
		}
	}
	return fallback
}

// entryAddresses returns the dial targets for the deep link: the entry's public
// IPs (DNS-independent, like the spec example), falling back to the hostname.
func entryAddresses(entry model.Node, hostname string, port int) []string {
	var a []string
	for _, ip := range entry.PublicIPs {
		if ip = strings.TrimSpace(ip); ip != "" {
			a = append(a, fmt.Sprintf("%s:%d", ip, port))
		}
	}
	if len(a) == 0 {
		a = append(a, fmt.Sprintf("%s:%d", hostname, port))
	}
	return a
}

// buildDeepLinkPayload serializes the config into the TLV wire format from
// DEEP_LINK.md and returns it base64url-encoded (no padding). Field order
// mirrors the reference encoder; per the spec, order is not significant.
func buildDeepLinkPayload(user model.User, hostname string, addresses, dns []string) string {
	var p []byte
	tlv(&p, 0x00, []byte{0x01})     // version = 1
	tlv(&p, 0x01, []byte(hostname)) // hostname (SNI / cert name)
	tlv(&p, 0x05, []byte(user.Username))
	tlv(&p, 0x06, []byte(user.Secret))
	for _, a := range addresses { // one TLV per address
		tlv(&p, 0x02, []byte(a))
	}
	if len(dns) > 0 {
		var arr []byte // String[]: each element is a varint length + UTF-8 bytes
		for _, d := range dns {
			putVarint(&arr, uint64(len(d)))
			arr = append(arr, d...)
		}
		tlv(&p, 0x0D, arr)
	}
	return base64.RawURLEncoding.EncodeToString(p)
}

// tlv appends one Tag–Length–Value entry (tag and length as TLS/QUIC varints).
func tlv(b *[]byte, tag uint64, val []byte) {
	putVarint(b, tag)
	putVarint(b, uint64(len(val)))
	*b = append(*b, val...)
}

// putVarint appends v using the QUIC/TLS variable-length integer encoding
// (RFC 9000 §16): the two MSBs of the first byte select a 1/2/4/8-byte form.
func putVarint(b *[]byte, v uint64) {
	switch {
	case v < 1<<6:
		*b = append(*b, byte(v))
	case v < 1<<14:
		*b = append(*b, byte(0x40|v>>8), byte(v))
	case v < 1<<30:
		*b = append(*b, byte(0x80|v>>24), byte(v>>16), byte(v>>8), byte(v))
	default:
		*b = append(*b, byte(0xc0|v>>56), byte(v>>48), byte(v>>40), byte(v>>32),
			byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
	}
}

func renderClientTOML(user model.User, hostname string, exclusions []string, opts Options) string {
	var b strings.Builder
	b.WriteString("# Generated by TrustPanel.\n")
	b.WriteString("loglevel = \"info\"\n")
	b.WriteString("vpn_mode = \"general\"\n")
	b.WriteString("killswitch_enabled = true\n")
	b.WriteString("killswitch_allow_ports = []\n")
	b.WriteString("post_quantum_group_enabled = true\n")
	b.WriteString("exclusions = " + tomlStringList(exclusions) + "\n\n")

	b.WriteString("[endpoint]\n")
	b.WriteString("hostname = " + tomlString(hostname) + "\n")
	b.WriteString("addresses = " + tomlStringList([]string{fmt.Sprintf("%s:%d", hostname, opts.Port)}) + "\n")
	b.WriteString("has_ipv6 = true\n")
	b.WriteString("username = " + tomlString(user.Username) + "\n")
	b.WriteString("password = " + tomlString(user.Secret) + "\n")
	b.WriteString("client_random = \"\"\n")
	b.WriteString("skip_verification = false\n")
	b.WriteString("certificate = \"\"\n")
	b.WriteString("dns_upstreams = " + tomlStringList(opts.DNSUpstreams) + "\n")
	b.WriteString("upstream_protocol = \"http2\"\n")
	b.WriteString("anti_dpi = false\n\n")

	b.WriteString("[listener.tun]\n")
	b.WriteString("bound_if = \"\"\n")
	b.WriteString("included_routes = [\"0.0.0.0/0\", \"2000::/3\"]\n")
	b.WriteString("excluded_routes = [\"10.0.0.0/8\", \"172.16.0.0/12\", \"192.168.0.0/16\"]\n")
	b.WriteString("mtu_size = 1280\n")
	return b.String()
}

// clientExclusions derives client-side split-routing globs from the guard-tier
// direct policies that apply to the user (fleet-wide or the user's group). The
// server-side guard remains authoritative; this is a client convenience.
func clientExclusions(state model.State, user model.User) []string {
	set := map[string]bool{}
	for _, p := range state.RoutePolicies {
		if p.Disabled {
			continue
		}
		if p.Tier != model.TierGuard || p.Action != model.ActionDirect {
			continue
		}
		if p.AppliesToGroupID != "" && p.AppliesToGroupID != user.GroupID {
			continue
		}
		for _, d := range p.MatchDomains {
			d = strings.TrimPrefix(strings.TrimSpace(strings.ToLower(d)), ".")
			if d != "" {
				set["*."+d] = true
			}
		}
	}
	out := make([]string, 0, len(set))
	for g := range set {
		out = append(out, g)
	}
	sort.Strings(out)
	return out
}

func entryHostname(state model.State, nodeID string) (string, []string) {
	var first string
	for _, d := range state.Domains {
		if d.NodeID != nodeID {
			continue
		}
		if d.Purpose == model.PurposeMainFallback {
			return d.Hostname, nil
		}
		if first == "" {
			first = d.Hostname
		}
	}
	if first != "" {
		return first, []string{fmt.Sprintf("entry %q has no main-fallback domain; using %q", nodeID, first)}
	}
	return "", []string{fmt.Sprintf("entry %q has no domain assigned", nodeID)}
}

func findUser(state model.State, userID string) (model.User, bool) {
	for _, u := range state.Users {
		if u.ID == userID {
			return u, true
		}
	}
	return model.User{}, false
}

func tomlString(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return `"` + r.Replace(s) + `"`
}

func tomlStringList(items []string) string {
	if len(items) == 0 {
		return "[]"
	}
	quoted := make([]string, len(items))
	for i, it := range items {
		quoted[i] = tomlString(it)
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}
