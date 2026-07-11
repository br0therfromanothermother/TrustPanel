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

// Get returns the .srs bytes for tag, from memory, then disk, then download.
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
	}

	b, err := p.download(ctx, tag)
	if err != nil {
		return nil, err
	}
	if p.CacheDir != "" {
		if err := os.MkdirAll(p.CacheDir, 0o755); err == nil {
			_ = os.WriteFile(filepath.Join(p.CacheDir, tag+".srs"), b, 0o644)
		}
	}
	p.put(tag, b)
	return b, nil
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
		return nil, fmt.Errorf("rule-set %q not available (HTTP %d from %s) — check the geoip/geosite category name", tag, resp.StatusCode, url)
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
	switch {
	case strings.HasPrefix(tag, "geoip-"):
		return base(p.GeoIPBase, DefaultGeoIPBase) + tag + ".srs"
	default: // geosite-
		return base(p.GeoSiteBase, DefaultGeoSiteBase) + tag + ".srs"
	}
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
