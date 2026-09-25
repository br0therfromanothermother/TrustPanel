// Package rulesets fetches and caches sing-box geoip/geosite rule-set (.srs)
// files for the panel to push to nodes via desired-state. The control plane (on
// the uncensored exit) downloads them once and distributes them, so entry nodes
// in censored regions never need to reach GitHub at runtime.
package rulesets

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// srsMagic is the leading magic of a sing-box binary rule-set ("SRS").
var srsMagic = []byte{0x53, 0x52, 0x53}

// DefaultGeoIPBase / DefaultGeoSiteBase are the SagerNet rule-set repos.
const (
	DefaultGeoIPBase   = "https://raw.githubusercontent.com/SagerNet/sing-geoip/rule-set/"
	DefaultGeoSiteBase = "https://raw.githubusercontent.com/SagerNet/sing-geosite/rule-set/"
)

// Preset names a source the panel already knows the address of, so choosing one
// is a click rather than a URL typed from memory.
type Preset struct {
	ID  string `json:"id"`
	URL string `json:"url"`
}

// PresetCustom marks a source whose address was typed in rather than chosen.
const PresetCustom = "custom"

// presets are the known sources per kind. Both publish files sing-box reads as
// they are, which is the only requirement: a repository in any other format is a
// conversion pipeline, not a different address, and is deliberately not offered.
var presets = map[string][]Preset{
	"geoip": {
		{ID: "sagernet", URL: DefaultGeoIPBase},
		{ID: "runetfreedom", URL: "https://raw.githubusercontent.com/runetfreedom/russia-v2ray-rules-dat/release/sing-box/rule-set-geoip/"},
	},
	"geosite": {
		{ID: "sagernet", URL: DefaultGeoSiteBase},
		{ID: "runetfreedom", URL: "https://raw.githubusercontent.com/runetfreedom/russia-v2ray-rules-dat/release/sing-box/rule-set-geosite/"},
	},
}

// Presets lists the known sources for kind.
func Presets(kind string) []Preset { return presets[kind] }

// PresetURL resolves a preset id to its address, or "" if kind has no such
// preset.
func PresetURL(kind, id string) string {
	for _, pr := range presets[kind] {
		if pr.ID == id {
			return pr.URL
		}
	}
	return ""
}

// SetBases points the provider at different sources. Cached bytes are dropped
// from memory because a tag names a file in a repository, and after this call it
// names a file in a different one.
func (p *Provider) SetBases(geoip, geosite string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.GeoIPBase, p.GeoSiteBase = geoip, geosite
	p.mem = map[string][]byte{}
}

// Provider resolves a rule-set tag (e.g. "geoip-ru", "geosite-youtube") to its
// .srs bytes, with a disk + memory cache. Safe for concurrent use.
type Provider struct {
	CacheDir    string
	GeoIPBase   string
	GeoSiteBase string
	Client      *http.Client

	mu  sync.Mutex
	mem map[string][]byte
}

// New builds a Provider caching under cacheDir.
func New(cacheDir string) *Provider {
	return &Provider{
		CacheDir:    cacheDir,
		GeoIPBase:   DefaultGeoIPBase,
		GeoSiteBase: DefaultGeoSiteBase,
		Client:      &http.Client{Timeout: 30 * time.Second},
		mem:         map[string][]byte{},
	}
}

// Get returns the .srs bytes for tag: memory, then the pinned copy on disk, then
// the mirror, and only then the network.
//
// The order matters more than it looks. A reconcile calls this, so every
// step before the last one is a node getting its configuration without anything
// remote having to answer. Taking a category out of the mirror also pins it: from
// that moment the bytes are the ones this rule routes by, and refreshing the
// mirror will not change them (Update does, when asked).
//
// The network is the last resort for a source that cannot be mirrored: a plain
// file server, which can neither be listed nor archived.
func (p *Provider) Get(ctx context.Context, tag string) ([]byte, error) {
	if err := validTag(tag); err != nil {
		return nil, err
	}
	p.mu.Lock()
	if b, ok := p.mem[tag]; ok {
		p.mu.Unlock()
		return b, nil
	}
	p.mu.Unlock()

	if p.CacheDir != "" {
		if b, err := os.ReadFile(filepath.Join(p.CacheDir, tag+".srs")); err == nil && isSRS(b) {
			p.put(tag, b)
			return b, nil
		}
		if b, err := p.FromMirror(tag); err == nil {
			p.pin(tag, b)
			return b, nil
		}
		if p.HasMirror(kindOf(tag)) {
			// The mirror is this source's file list. Asking the network for a
			// category it does not hold would spend a request to be told the same
			// thing, once per attempt, forever.
			return nil, fmt.Errorf("rule-set %q is not offered by the %s source; check the category name", tag, kindOf(tag))
		}
	}

	b, err := p.download(ctx, tag)
	if err != nil {
		return nil, err
	}
	p.pin(tag, b)
	return b, nil
}

// pin writes the copy a rule routes by and remembers it.
func (p *Provider) pin(tag string, b []byte) {
	if p.CacheDir != "" {
		if err := os.MkdirAll(p.CacheDir, 0o755); err == nil {
			_ = writeFileAtomic(filepath.Join(p.CacheDir, tag+".srs"), b, 0o644)
		}
	}
	p.put(tag, b)
}

// Pin puts a category's bytes in place as the copy its rules route by, without
// waiting for a reconcile to ask for them. It reports whether anything was
// written: a category already pinned is left exactly as it is, because that copy
// is the copy the nodes are running.
func (p *Provider) Pin(ctx context.Context, tag string) (bool, error) {
	if err := validTag(tag); err != nil {
		return false, err
	}
	if p.CacheDir == "" {
		return false, fmt.Errorf("no rule-set cache directory is configured")
	}
	if b, err := os.ReadFile(filepath.Join(p.CacheDir, tag+".srs")); err == nil && isSRS(b) {
		return false, nil
	}
	if b, err := p.FromMirror(tag); err == nil {
		p.pin(tag, b)
		return true, nil
	}
	if p.HasMirror(kindOf(tag)) {
		return false, fmt.Errorf("rule-set %q is not offered by the %s source; check the category name", tag, kindOf(tag))
	}
	b, err := p.download(ctx, tag)
	if err != nil {
		return false, err
	}
	p.pin(tag, b)
	return true, nil
}

func (p *Provider) put(tag string, b []byte) {
	p.mu.Lock()
	p.mem[tag] = b
	p.mu.Unlock()
}

func (p *Provider) download(ctx context.Context, tag string) ([]byte, error) {
	url := p.urlFor(tag)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := p.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", tag, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("rule-set %q not available (HTTP %d from %s); check the geoip/geosite category name", tag, resp.StatusCode, url)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, err
	}
	if !isSRS(b) {
		return nil, fmt.Errorf("rule-set %q: downloaded data is not a sing-box .srs file", tag)
	}
	return b, nil
}

func (p *Provider) urlFor(tag string) string {
	if strings.HasPrefix(tag, "geoip-") {
		return p.baseFor("geoip") + tag + ".srs"
	}
	return p.baseFor("geosite") + tag + ".srs"
}

func base(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

func validTag(tag string) error {
	if !strings.HasPrefix(tag, "geoip-") && !strings.HasPrefix(tag, "geosite-") {
		return fmt.Errorf("rule-set tag %q must start with geoip- or geosite-", tag)
	}
	if strings.ContainsAny(tag, "/\\.") {
		return fmt.Errorf("rule-set tag %q has invalid characters", tag)
	}
	return nil
}

func isSRS(b []byte) bool {
	return len(b) >= len(srsMagic) && string(b[:len(srsMagic)]) == string(srsMagic)
}
