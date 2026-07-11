package rulesets

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fakeSRS(tag string) []byte { return append([]byte("SRS\x01"), []byte(tag)...) }

func TestProviderDownloadCacheAnd404(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if strings.Contains(r.URL.Path, "geoip-ru.srs") {
			w.Write(fakeSRS("geoip-ru"))
			return
		}
		http.NotFound(w, r) // unknown category -> 404
	}))
	defer srv.Close()

	dir := t.TempDir()
	p := New(dir)
	p.GeoIPBase = srv.URL + "/"
	p.GeoSiteBase = srv.URL + "/"

	// download + disk cache
	b, err := p.Get(context.Background(), "geoip-ru")
	if err != nil || !isSRS(b) {
		t.Fatalf("get geoip-ru: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "geoip-ru.srs")); err != nil {
		t.Errorf("not cached to disk: %v", err)
	}
	// second get served from mem (no extra hit)
	h := hits
	if _, err := p.Get(context.Background(), "geoip-ru"); err != nil {
		t.Fatal(err)
	}
	if hits != h {
		t.Errorf("expected cache hit, made another request")
	}
	// 404 -> clear error, not cached
	if _, err := p.Get(context.Background(), "geosite-nope"); err == nil || !strings.Contains(err.Error(), "not available") {
		t.Fatalf("expected not-available error, got %v", err)
	}
	// invalid tag rejected
	if _, err := p.Get(context.Background(), "bogus"); err == nil {
		t.Fatal("invalid tag should error")
	}
}
