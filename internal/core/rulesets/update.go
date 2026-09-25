package rulesets

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// prevSuffix names the generation an update replaced. It deliberately does not
// end in .srs: the cache walk and the desired-state push both key off that
// suffix, so a kept copy is invisible to everything except a rollback.
const prevSuffix = ".srs.prev"

// Change records what an update or a rollback did to one rule-set.
type Change struct {
	Tag     string
	OldSize int64
	OldID   string
	NewSize int64
	NewID   string
	At      time.Time
}

// Update fetches a rule-set from its source and puts it in place of the cached
// copy, keeping the copy it replaced.
//
// The download lands in a temporary file first and is checked before anything
// is replaced, so a truncated transfer or an error page served with a 200 can
// never become the list a node routes by. If the bytes turn out to be identical
// the cache is left alone: rewriting the file would move its timestamp and claim
// a change that did not happen.
func (p *Provider) Update(ctx context.Context, tag string) (Change, error) {
	if err := validTag(tag); err != nil {
		return Change{}, err
	}
	if p.CacheDir == "" {
		return Change{}, fmt.Errorf("no rule-set cache directory is configured")
	}
	// The mirror holds what the source offers; going to the network here would ask
	// the same question twice and could answer it differently.
	body, err := p.FromMirror(tag)
	if err != nil {
		if body, err = p.download(ctx, tag); err != nil {
			return Change{}, err
		}
	}
	if err := os.MkdirAll(p.CacheDir, 0o755); err != nil {
		return Change{}, err
	}
	cur := filepath.Join(p.CacheDir, tag+".srs")
	ch := Change{Tag: tag, NewSize: int64(len(body)), NewID: ContentID(body), At: time.Now()}
	if old, err := os.ReadFile(cur); err == nil {
		ch.OldSize, ch.OldID = int64(len(old)), ContentID(old)
		if ch.OldID == ch.NewID {
			return ch, nil
		}
	}

	tmp, err := os.CreateTemp(p.CacheDir, tag+".*.part")
	if err != nil {
		return Change{}, err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		return Change{}, err
	}
	if err := tmp.Close(); err != nil {
		return Change{}, err
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return Change{}, err
	}
	// Keep the copy being replaced before the rename, not after: after it, there
	// is nothing left to keep.
	if ch.OldID != "" {
		if err := copyFile(cur, filepath.Join(p.CacheDir, tag+prevSuffix)); err != nil {
			return Change{}, fmt.Errorf("keep the previous %s: %w", tag, err)
		}
	}
	if err := os.Rename(tmpName, cur); err != nil {
		return Change{}, err
	}
	p.put(tag, body)
	return ch, nil
}

// Rollback puts the kept copy back and drops it, so the undo is one step deep
// and says so: after a rollback there is nothing further to go back to.
//
// The source still holds whatever prompted the update, so the next check will
// report it as available again. That is the intended reading: a rollback
// rejects one version, it does not stop the world.
func (p *Provider) Rollback(_ context.Context, tag string) (Change, error) {
	if err := validTag(tag); err != nil {
		return Change{}, err
	}
	if p.CacheDir == "" {
		return Change{}, fmt.Errorf("no rule-set cache directory is configured")
	}
	prev := filepath.Join(p.CacheDir, tag+prevSuffix)
	body, err := os.ReadFile(prev)
	if err != nil {
		return Change{}, fmt.Errorf("no previous copy of %s is kept", tag)
	}
	if !isSRS(body) {
		return Change{}, fmt.Errorf("the kept copy of %s is not a rule-set", tag)
	}
	cur := filepath.Join(p.CacheDir, tag+".srs")
	ch := Change{Tag: tag, NewSize: int64(len(body)), NewID: ContentID(body), At: time.Now()}
	if old, err := os.ReadFile(cur); err == nil {
		ch.OldSize, ch.OldID = int64(len(old)), ContentID(old)
	}
	if err := copyFile(prev, cur); err != nil {
		return Change{}, err
	}
	_ = os.Remove(prev)
	p.put(tag, body)
	return ch, nil
}

// HasPrevious reports whether a kept copy exists for tag.
func (p *Provider) HasPrevious(tag string) bool {
	if p.CacheDir == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(p.CacheDir, tag+prevSuffix))
	return err == nil
}

// copyFile writes src to dst through a temporary file in the same directory, so
// dst is either the old content or the new one and never half of each.
func copyFile(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
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
	if err := os.Chmod(name, 0o644); err != nil {
		return err
	}
	return os.Rename(name, dst)
}
