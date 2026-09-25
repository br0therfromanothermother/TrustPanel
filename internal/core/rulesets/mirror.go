package rulesets

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// A mirror is the whole of one source, kept on the control plane: every category
// the repository publishes, as one snapshot taken at one instant.
//
// Names and bytes come from one place. Taking the names from a listing of the
// repository tree and the bytes from raw file fetches means two addresses, two
// rate limits, two ways to fail and nothing keeping them in agreement: the
// listing can offer a category whose file then answers 404. An archive of the
// branch
// is one artifact: the list of categories is the list of files in it, and the
// two cannot disagree.
//
// What it buys, in order of importance: a reconcile never needs the network (a
// category named in a rule is already on disk. A GitHub outage can no longer
// hold up a node's whole configuration push); "does this category exist" is
// answered locally and instantly, in the panel and in the bot, with the same
// answer; and a fresh install has every category from its first minute rather
// than downloading them one at a time, forever, at the worst moment.
//
// The mirror is not what the nodes route by. What a rule names is pinned: its
// bytes are taken from the mirror once and then left alone until someone asks
// for an update (see Update / SetRuleSetAutoUpdate). Refreshing the mirror never
// changes what is already in use.

const (
	// mirrorDir holds one directory per kind under the cache, plus one manifest
	// file per kind. It is deliberately inside CacheDir so one setting still says
	// where everything lives, and deliberately a subdirectory so Cached(), which
	// reads the pinned copies, never walks into it.
	mirrorDir = "mirror"

	// The extraction limits. The two default sources are ~1900 and ~240 files of
	// a few hundred bytes each; these caps are far above that and far below
	// anything that could fill a disk from a hostile or broken archive.
	maxMirrorFiles = 50000
	maxMirrorFile  = 8 << 20
	maxMirrorTotal = 256 << 20
)

// MirrorResult records one refresh.
type MirrorResult struct {
	Kind   string
	Commit string
	// Unchanged is set when the source is still at the commit the mirror was
	// taken from, or when the archive turned out to hold exactly what is already
	// mirrored. Nothing was written in that case.
	Unchanged bool
	Count     int
	Added     []string
	Removed   []string
	Changed   []string
	At        time.Time
}

// manifest is the mirror's own record of a generation: which commit it came
// from and what each category's bytes were. Keeping the identities next to the
// files makes "what changed since last week" answerable without keeping
// a second copy of the whole source.
type manifest struct {
	Commit string            `json:"commit"`
	At     time.Time         `json:"at"`
	Files  map[string]string `json:"files"` // tag -> ContentID
}

// Mirror refreshes the local copy of one source and reports what changed.
//
// It asks the repository for its current commit first: an unchanged commit means
// there is nothing to download, which is the normal weekly answer. When the
// archive is fetched, it is extracted to a temporary directory and checked file
// by file before anything is put in place, which leaves a truncated download or an error
// page served with a 200 can never become the catalogue.
func (p *Provider) Mirror(ctx context.Context, kind string) (MirrorResult, error) {
	if p.CacheDir == "" {
		return MirrorResult{}, fmt.Errorf("no rule-set cache directory is configured")
	}
	src, ok := githubSource(p.baseFor(kind))
	if !ok {
		return MirrorResult{}, fmt.Errorf("mirroring needs a GitHub source; %s cannot be mirrored", p.baseFor(kind))
	}
	now := time.Now()
	old := p.readManifest(kind)

	// The commit check is an optimisation and never the authority: if it cannot
	// be had (rate limit, network, a source that answers differently) the archive
	// is fetched and the comparison below gives the same answer, just for the
	// price of a megabyte.
	commit, _ := p.headCommit(ctx, src)
	if commit != "" && commit == old.Commit && p.mirrorHas(kind, old) {
		return MirrorResult{Kind: kind, Commit: commit, Unchanged: true, Count: len(old.Files), At: old.At}, nil
	}

	files, err := p.fetchArchive(ctx, src, kind)
	if err != nil {
		return MirrorResult{}, err
	}
	if len(files) == 0 {
		return MirrorResult{}, fmt.Errorf("mirror %s: %s holds no %s-*.srs files", kind, src.archive, kind)
	}

	next := manifest{Commit: commit, At: now, Files: make(map[string]string, len(files))}
	for tag, body := range files {
		next.Files[tag] = ContentID(body)
	}
	res := diffManifests(kind, old, next)
	res.At, res.Commit, res.Count = now, commit, len(files)
	if len(res.Added) == 0 && len(res.Removed) == 0 && len(res.Changed) == 0 && p.mirrorHas(kind, old) {
		// The bytes are the ones already mirrored. Writing them again would move
		// every timestamp and claim a change that did not happen; the commit is
		// still recorded, letting the next check take the cheap way out.
		res.Unchanged = true
		old.Commit, old.At = commit, now
		_ = p.writeManifest(kind, old)
		return res, nil
	}
	if err := p.swapMirror(kind, files); err != nil {
		return MirrorResult{}, err
	}
	if err := p.writeManifest(kind, next); err != nil {
		return MirrorResult{}, err
	}
	return res, nil
}

// MirrorTags lists what the mirror holds for one kind, in the same shape the
// a source listing returns. This is the catalogue.
func (p *Provider) MirrorTags(kind string) ([]CatalogEntry, error) {
	dir := p.mirrorPath(kind)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("no mirror of the %s source yet", kind)
		}
		return nil, err
	}
	man := p.readManifest(kind)
	out := make([]CatalogEntry, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".srs") {
			continue
		}
		tag := strings.TrimSuffix(name, ".srs")
		if kindOf(tag) != kind {
			continue
		}
		var size int64
		if fi, err := e.Info(); err == nil {
			size = fi.Size()
		}
		out = append(out, CatalogEntry{Tag: tag, Size: size, ContentID: man.Files[tag]})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("the %s mirror is empty", kind)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Tag < out[j].Tag })
	return out, nil
}

// HasMirror reports whether this kind's source is mirrored locally. Where it is,
// the mirror answers "does this category exist" on its own: it is a copy of the
// same listing the files would be fetched from.
func (p *Provider) HasMirror(kind string) bool {
	if kind == "" || p.CacheDir == "" {
		return false
	}
	entries, err := os.ReadDir(p.mirrorPath(kind))
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".srs") {
			return true
		}
	}
	return false
}

// MirrorAt reports when the mirror of a kind was last taken, zero if there is
// none.
func (p *Provider) MirrorAt(kind string) time.Time { return p.readManifest(kind).At }

// FromMirror returns one category's bytes out of the mirror.
func (p *Provider) FromMirror(tag string) ([]byte, error) {
	if err := validTag(tag); err != nil {
		return nil, err
	}
	kind := kindOf(tag)
	if kind == "" || p.CacheDir == "" {
		return nil, os.ErrNotExist
	}
	b, err := os.ReadFile(filepath.Join(p.mirrorPath(kind), tag+".srs"))
	if err != nil {
		return nil, err
	}
	if !isSRS(b) {
		return nil, fmt.Errorf("the mirrored copy of %q is not a rule-set", tag)
	}
	return b, nil
}

func (p *Provider) mirrorPath(kind string) string {
	return filepath.Join(p.CacheDir, mirrorDir, kind)
}

func (p *Provider) manifestPath(kind string) string {
	return filepath.Join(p.CacheDir, mirrorDir, kind+".json")
}

func (p *Provider) readManifest(kind string) manifest {
	var m manifest
	b, err := os.ReadFile(p.manifestPath(kind))
	if err != nil {
		return manifest{Files: map[string]string{}}
	}
	if err := json.Unmarshal(b, &m); err != nil || m.Files == nil {
		return manifest{Files: map[string]string{}}
	}
	return m
}

func (p *Provider) writeManifest(kind string, m manifest) error {
	if err := os.MkdirAll(filepath.Join(p.CacheDir, mirrorDir), 0o755); err != nil {
		return err
	}
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return writeFileAtomic(p.manifestPath(kind), b, 0o644)
}

// mirrorHas reports whether the files a manifest describes are actually on disk,
// so a manifest left behind by a wiped cache cannot be read as a mirror.
func (p *Provider) mirrorHas(kind string, m manifest) bool {
	if len(m.Files) == 0 {
		return false
	}
	entries, err := os.ReadDir(p.mirrorPath(kind))
	if err != nil {
		return false
	}
	n := 0
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".srs") {
			n++
		}
	}
	return n == len(m.Files)
}

// diffManifests reports what one generation did to the one before it.
func diffManifests(kind string, old, next manifest) MirrorResult {
	res := MirrorResult{Kind: kind}
	for tag, id := range next.Files {
		was, had := old.Files[tag]
		switch {
		case !had:
			res.Added = append(res.Added, tag)
		case was != id:
			res.Changed = append(res.Changed, tag)
		}
	}
	for tag := range old.Files {
		if _, still := next.Files[tag]; !still {
			res.Removed = append(res.Removed, tag)
		}
	}
	sort.Strings(res.Added)
	sort.Strings(res.Removed)
	sort.Strings(res.Changed)
	// A first mirror has nothing to compare against: every category is "added",
	// which is true but is not news anybody wants reported.
	if len(old.Files) == 0 {
		res.Added = nil
	}
	return res
}

// swapMirror puts a freshly extracted generation in place of the old one. The
// files are written to a temporary directory first and the directory is swapped
// in one rename: there is no moment at which the mirror is half of each.
func (p *Provider) swapMirror(kind string, files map[string][]byte) error {
	root := filepath.Join(p.CacheDir, mirrorDir)
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(root, kind+".new-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	for tag, body := range files {
		if err := os.WriteFile(filepath.Join(tmp, tag+".srs"), body, 0o644); err != nil {
			return err
		}
	}
	final := p.mirrorPath(kind)
	old := final + ".old"
	_ = os.RemoveAll(old)
	if err := os.Rename(final, old); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.Rename(tmp, final); err != nil {
		_ = os.Rename(old, final) // put the previous generation back
		return err
	}
	_ = os.RemoveAll(old)
	return nil
}

// githubSrc is a source addressed as a GitHub branch: where to get its archive,
// and which directory inside it holds the rule-sets.
type githubSrc struct {
	owner, repo, ref string
	prefix           string
	archive          string
}

// githubSource reads a raw.githubusercontent.com base address as a repository,
// a branch and a directory inside it: the same shape a listing requires, since a
// bare file server cannot be enumerated or archived either.
func githubSource(base string) (githubSrc, bool) {
	u, err := url.Parse(base)
	if err != nil || u.Scheme != "https" || u.Host != "raw.githubusercontent.com" {
		return githubSrc{}, false
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 3 {
		return githubSrc{}, false
	}
	s := githubSrc{owner: parts[0], repo: parts[1], ref: parts[2]}
	if dir := parts[3:]; len(dir) > 0 {
		s.prefix = strings.Join(dir, "/") + "/"
	}
	s.archive = "https://codeload.github.com/" + url.PathEscape(s.owner) + "/" + url.PathEscape(s.repo) +
		"/tar.gz/refs/heads/" + url.PathEscape(s.ref)
	return s, true
}

// headCommit asks what the branch currently points at. GitHub will answer with
// the bare hash for the price of one small request, which answers "has
// anything changed at all?" cheaper than the answer it saves.
func (p *Provider) headCommit(ctx context.Context, src githubSrc) (string, error) {
	api := "https://api.github.com/repos/" + url.PathEscape(src.owner) + "/" + url.PathEscape(src.repo) +
		"/commits/" + url.PathEscape(src.ref)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, api, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github.sha")
	resp, err := p.client().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<10))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("commit of %s/%s@%s: HTTP %d%s", src.owner, src.repo, src.ref, resp.StatusCode, rateHint(resp))
	}
	sha := strings.TrimSpace(string(body))
	if len(sha) < 7 || strings.ContainsAny(sha, " \n<") {
		return "", fmt.Errorf("commit of %s/%s@%s: unexpected answer", src.owner, src.repo, src.ref)
	}
	return sha, nil
}

// fetchArchive downloads the branch archive and returns the rule-sets in it,
// by tag. Everything about the archive is treated as hostile: paths are read as
// names and never as places to write, sizes are capped, and a file that is not
// a sing-box rule-set is not a category no matter what it is called.
func (p *Provider) fetchArchive(ctx context.Context, src githubSrc, kind string) (map[string][]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src.archive, nil)
	if err != nil {
		return nil, err
	}
	resp, err := p.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("mirror %s: %w", kind, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("mirror %s: %s answered HTTP %d%s", kind, src.archive, resp.StatusCode, rateHint(resp))
	}
	gz, err := gzip.NewReader(io.LimitReader(resp.Body, maxMirrorTotal))
	if err != nil {
		return nil, fmt.Errorf("mirror %s: %w", kind, err)
	}
	defer gz.Close()

	out := map[string][]byte{}
	tr := tar.NewReader(gz)
	var total int64
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("mirror %s: %w", kind, err)
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		tag, ok := archiveTag(h.Name, src.prefix, kind)
		if !ok {
			continue
		}
		if h.Size > maxMirrorFile {
			continue
		}
		body, err := io.ReadAll(io.LimitReader(tr, maxMirrorFile+1))
		if err != nil {
			return nil, fmt.Errorf("mirror %s: %w", kind, err)
		}
		if len(body) > maxMirrorFile || !isSRS(body) {
			continue
		}
		total += int64(len(body))
		if total > maxMirrorTotal || len(out) >= maxMirrorFiles {
			return nil, fmt.Errorf("mirror %s: the archive is larger than a rule-set repository can reasonably be", kind)
		}
		out[tag] = body
	}
	return out, nil
}

// archiveTag turns a path inside the archive into a category name, or reports
// that it is not one. The archive's own top directory (repo-branch/) is stripped
// first; after that the path must be exactly the source's rule-set directory
// plus one <kind>-<name>.srs file.
func archiveTag(name, prefix, kind string) (string, bool) {
	// An entry that walks out of its own archive, or names an absolute place on
	// this machine, is not a category however it ends. Nothing here would write
	// to such a path, since the tag is validated and the write goes to our own
	// directory, but an entry like that is malformed for this purpose, and the
	// honest answer to "is this a category?" is no.
	if strings.HasPrefix(name, "/") || name != path.Clean(name) || hasDotDot(name) {
		return "", false
	}
	i := strings.IndexByte(name, '/')
	if i < 0 {
		return "", false
	}
	rel := name[i+1:]
	if !strings.HasPrefix(rel, prefix) {
		return "", false
	}
	base := strings.TrimPrefix(rel, prefix)
	if base == "" || strings.Contains(base, "/") || !strings.HasSuffix(base, ".srs") {
		return "", false
	}
	tag := strings.TrimSuffix(base, ".srs")
	if kindOf(tag) != kind || validTag(tag) != nil {
		return "", false
	}
	return tag, true
}

// hasDotDot reports whether any segment of a slash path is "..".
func hasDotDot(name string) bool {
	for _, seg := range strings.Split(name, "/") {
		if seg == ".." {
			return true
		}
	}
	return false
}

// writeFileAtomic writes through a temporary file in the same directory. A
// reader sees either the old bytes or the new ones.
func writeFileAtomic(dst string, b []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(dst), filepath.Base(dst)+".*.part")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(name, mode); err != nil {
		return err
	}
	return os.Rename(name, dst)
}
