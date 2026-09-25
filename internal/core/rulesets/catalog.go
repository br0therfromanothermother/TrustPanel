package rulesets

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ContentID identifies a rule-set by its bytes. It is the git blob hash, sha1
// over "blob <length>\0" followed by the content, because that is exactly the
// identity a GitHub tree listing reports for every file it lists. Computing it
// locally means one listing answers "is my copy current?" for every rule-set at
// once, without downloading any of them.
//
// Nothing here depends on sha1's collision resistance: the two sides are the
// same file or they are not, and an attacker who can serve chosen bytes from the
// source has already won by serving them.
func ContentID(b []byte) string {
	h := sha1.New()
	fmt.Fprintf(h, "blob %d\x00", len(b))
	h.Write(b)
	return hex.EncodeToString(h.Sum(nil))
}

// CatalogEntry is one category a source offers: the tag routing would name it
// by, the size of the compiled file, and the identity of its current contents.
type CatalogEntry struct {
	Tag       string
	Size      int64
	ContentID string
}

// Catalog lists every category a source offers for kind ("geoip" or "geosite").
//
// A sing-box repository is a directory of .srs files with no manifest, so the
// listing is the catalogue and the file names are the category names. GitHub
// will list a whole branch in one request, which is why that is the only source
// shape supported here: a bare file server offers no way to enumerate itself,
// and guessing category names is not enumeration.
func (p *Provider) Catalog(ctx context.Context, kind string) ([]CatalogEntry, error) {
	base := p.baseFor(kind)
	api, prefix, ok := githubTree(base)
	if !ok {
		return nil, fmt.Errorf("listing categories needs a GitHub source; %s cannot be enumerated", base)
	}
	return p.catalogFrom(ctx, kind, api, prefix)
}

// catalogFrom is Catalog once the listing URL is known.
func (p *Provider) catalogFrom(ctx context.Context, kind, api, prefix string) ([]CatalogEntry, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, api, nil)
	if err != nil {
		return nil, err
	}
	// GitHub rejects a request with no User-Agent outright. Go's default header
	// satisfies it, so this call must not strip the header to look anonymous.
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := p.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("list categories: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("list categories: %s answered HTTP %d%s", api, resp.StatusCode, rateHint(resp))
	}
	var tree struct {
		Tree []struct {
			Path string `json:"path"`
			Type string `json:"type"`
			Size int64  `json:"size"`
			SHA  string `json:"sha"`
		} `json:"tree"`
		Truncated bool `json:"truncated"`
	}
	if err := json.Unmarshal(body, &tree); err != nil {
		return nil, fmt.Errorf("list categories: %w", err)
	}
	// A truncated listing is a partial catalogue, and a partial catalogue read as
	// a complete one would report live categories as withdrawn. Refuse it.
	if tree.Truncated {
		return nil, fmt.Errorf("list categories: %s returned a partial listing", api)
	}
	out := make([]CatalogEntry, 0, len(tree.Tree))
	for _, e := range tree.Tree {
		if e.Type != "blob" || !strings.HasPrefix(e.Path, prefix) {
			continue
		}
		name := strings.TrimPrefix(e.Path, prefix)
		if strings.Contains(name, "/") || !strings.HasSuffix(name, ".srs") {
			continue
		}
		tag := strings.TrimSuffix(name, ".srs")
		if !strings.HasPrefix(tag, kind+"-") {
			continue
		}
		out = append(out, CatalogEntry{Tag: tag, Size: e.Size, ContentID: e.SHA})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("list categories: %s offers no %s-*.srs files", api, kind)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Tag < out[j].Tag })
	return out, nil
}

// rateHint turns GitHub's "no requests left" into the one sentence that explains
// it, since the bare 403 reads like a permission problem and is not one.
func rateHint(resp *http.Response) string {
	if resp.StatusCode == http.StatusForbidden && resp.Header.Get("X-RateLimit-Remaining") == "0" {
		return " (hourly request allowance spent; it refills on its own)"
	}
	return ""
}

// githubTree maps a raw.githubusercontent.com base to the API call that lists
// its branch in one request, plus the path prefix the files sit under. It
// reports false for any other host, which is the signal that the source can be
// downloaded from but not enumerated.
func githubTree(base string) (api, prefix string, ok bool) {
	u, err := url.Parse(base)
	if err != nil || u.Scheme != "https" || u.Host != "raw.githubusercontent.com" {
		return "", "", false
	}
	// /<owner>/<repo>/<ref>/<dir...>/
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 3 {
		return "", "", false
	}
	owner, repo, ref := parts[0], parts[1], parts[2]
	if dir := parts[3:]; len(dir) > 0 {
		prefix = strings.Join(dir, "/") + "/"
	}
	api = "https://api.github.com/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo) +
		"/git/trees/" + url.PathEscape(ref) + "?recursive=1"
	return api, prefix, true
}

// CachedSet is one .srs the control plane already holds.
type CachedSet struct {
	Tag       string
	Kind      string
	Size      int64
	ContentID string
	FetchedAt time.Time
}

// Cached lists the rule-sets on disk. This is the honest answer to "what do we
// actually have": the database rows describing them are derived from this walk,
// never the other way round, so a cache wiped by hand reads as an empty cache
// rather than as a fleet of files that are no longer there.
func (p *Provider) Cached() ([]CachedSet, error) {
	if p.CacheDir == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(p.CacheDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []CachedSet
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".srs") {
			continue
		}
		tag := strings.TrimSuffix(name, ".srs")
		kind := kindOf(tag)
		if kind == "" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(p.CacheDir, name))
		if err != nil || !isSRS(b) {
			continue
		}
		var at time.Time
		if fi, err := e.Info(); err == nil {
			at = fi.ModTime()
		}
		out = append(out, CachedSet{Tag: tag, Kind: kind, Size: int64(len(b)), ContentID: ContentID(b), FetchedAt: at})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Tag < out[j].Tag })
	return out, nil
}

// kindOf reports which database a tag belongs to, or "" if it belongs to
// neither.
func kindOf(tag string) string {
	switch {
	case strings.HasPrefix(tag, "geoip-"):
		return "geoip"
	case strings.HasPrefix(tag, "geosite-"):
		return "geosite"
	}
	return ""
}

// baseFor reads a source address under the lock SetBases writes it under, so a
// source changed from the UI cannot be read half-applied by an in-flight fetch.
func (p *Provider) baseFor(kind string) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if kind == "geoip" {
		return base(p.GeoIPBase, DefaultGeoIPBase)
	}
	return base(p.GeoSiteBase, DefaultGeoSiteBase)
}

func (p *Provider) client() *http.Client {
	if p.Client != nil {
		return p.Client
	}
	return http.DefaultClient
}
