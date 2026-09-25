package store

import (
	"context"
	"time"

	"trustpanel/internal/core/model"
)

// EnsureRuleSetSources creates the two source rows on first run. They are rows
// rather than settings fields because everything else about a source (when it
// was last reachable, what it offered then) belongs next to the address, not in
// a blob that would have to be rewritten on every check.
func (s *Store) EnsureRuleSetSources(ctx context.Context, geoipURL, geositeURL string) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO rule_set_sources (kind, preset, url) VALUES ('geoip', 'sagernet', $1), ('geosite', 'sagernet', $2)
		 ON CONFLICT (kind) DO NOTHING`, geoipURL, geositeURL)
	return err
}

// RuleSetSources returns both sources, geoip first.
func (s *Store) RuleSetSources(ctx context.Context) ([]model.RuleSetSource, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT kind, preset, url, checked_at, check_error, catalog, catalog_prev, catalog_at
		   FROM rule_set_sources ORDER BY kind`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.RuleSetSource
	for rows.Next() {
		var v model.RuleSetSource
		if err := rows.Scan(&v.Kind, &v.Preset, &v.URL, &v.CheckedAt, &v.CheckError,
			&v.Catalog, &v.CatalogPrev, &v.CatalogAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// SetRuleSetSource repoints one kind at another repository. It clears the stored
// catalogue: those names described the old repository, and serving them for the
// new one would offer categories that may not exist there.
func (s *Store) SetRuleSetSource(ctx context.Context, kind, preset, url string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE rule_set_sources
		    SET preset = $2, url = $3,
		        catalog = '{}', catalog_prev = '{}', catalog_at = NULL,
		        checked_at = NULL, check_error = ''
		  WHERE kind = $1 AND url IS DISTINCT FROM $3`, kind, preset, url)
	return err
}

// RecordCatalog stores the category names a source offered at now.
//
// The previous generation is kept only when the list actually changed: a check
// that finds nothing new must not overwrite the one snapshot that still explains
// what changed last time.
func (s *Store) RecordCatalog(ctx context.Context, kind string, catalog []string, now time.Time) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE rule_set_sources
		    SET catalog_prev = CASE WHEN catalog = $2 THEN catalog_prev ELSE catalog END,
		        catalog = $2, catalog_at = $3, checked_at = $3, check_error = ''
		  WHERE kind = $1`, kind, catalog, now)
	return err
}

// RecordCatalogError records a failed check without touching the catalogue, so
// an unreachable source keeps serving the names it last offered instead of
// reporting that every category vanished.
func (s *Store) RecordCatalogError(ctx context.Context, kind, msg string, now time.Time) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE rule_set_sources SET checked_at = $2, check_error = $3 WHERE kind = $1`, kind, now, msg)
	return err
}

// RuleSets returns the known rule-sets, by tag.
func (s *Store) RuleSets(ctx context.Context) ([]model.RuleSet, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT tag, kind, size, content_id, fetched_at, upstream_id, upstream_size,
		        checked_at, upstream_gone, auto_update,
		        prev_size, prev_content_id, prev_fetched_at
		   FROM rule_sets ORDER BY tag`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.RuleSet
	for rows.Next() {
		var v model.RuleSet
		if err := rows.Scan(&v.Tag, &v.Kind, &v.Size, &v.ContentID, &v.FetchedAt,
			&v.UpstreamID, &v.UpstreamSize, &v.CheckedAt, &v.UpstreamGone, &v.AutoUpdate,
			&v.PrevSize, &v.PrevContentID, &v.PrevFetchedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// RecordRuleSetLocal writes what is on disk for one rule-set. It touches only
// the local columns: a check whose source listing failed still knows what it
// holds, and must not blank out what the last successful check learned upstream.
func (s *Store) RecordRuleSetLocal(ctx context.Context, v model.RuleSet) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO rule_sets (tag, kind, size, content_id, fetched_at)
		 VALUES ($1,$2,$3,$4,$5)
		 ON CONFLICT (tag) DO UPDATE SET
		   kind = EXCLUDED.kind, size = EXCLUDED.size,
		   content_id = EXCLUDED.content_id, fetched_at = EXCLUDED.fetched_at`,
		v.Tag, v.Kind, v.Size, v.ContentID, v.FetchedAt)
	return err
}

// RecordRuleSetUpstream writes what the source listing showed for one rule-set.
// auto_update is the operator's choice and is never written here.
func (s *Store) RecordRuleSetUpstream(ctx context.Context, tag, upstreamID string, upstreamSize int64, gone bool, now time.Time) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE rule_sets
		    SET upstream_id = $2, upstream_size = $3, upstream_gone = $4, checked_at = $5
		  WHERE tag = $1`, tag, upstreamID, upstreamSize, gone, now)
	return err
}

// SetRuleSetAutoUpdate flips whether a rule-set follows its source without
// being asked.
func (s *Store) SetRuleSetAutoUpdate(ctx context.Context, tag string, on bool) error {
	_, err := s.pool.Exec(ctx, `UPDATE rule_sets SET auto_update = $2 WHERE tag = $1`, tag, on)
	return err
}

// PruneRuleSets drops rows for rule-sets that are no longer on disk, so a row
// never outlives the file it describes.
func (s *Store) PruneRuleSets(ctx context.Context, keep []string) error {
	if keep == nil {
		keep = []string{} // an empty cache must delete every row, not send NULL
	}
	_, err := s.pool.Exec(ctx, `DELETE FROM rule_sets WHERE tag <> ALL($1)`, keep)
	return err
}

// RecordRuleSetUpdated writes the new copy in place of the old one and keeps the
// old one's details alongside, which makes a rollback offerable.
//
// content_id is set equal to upstream_id because that is the state just
// reached: the
// bytes were taken from the source, so the set is current by construction and
// must not keep reading as "an update is available" until the next check.
func (s *Store) RecordRuleSetUpdated(ctx context.Context, tag string, size int64, contentID string, at time.Time) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE rule_sets
		    SET prev_size = size, prev_content_id = content_id, prev_fetched_at = fetched_at,
		        size = $2, content_id = $3, fetched_at = $4, upstream_id = $3, upstream_size = $2,
		        upstream_gone = false
		  WHERE tag = $1`, tag, size, contentID, at)
	return err
}

// RecordRuleSetRolledBack puts the kept copy's details back as the current ones
// and forgets it, so the undo is one step deep and the UI stops offering it.
//
// upstream_id is left alone: the source still holds what prompted the update,
// and the next check should say so again. A rollback rejects one version, it
// does not pretend the source never moved.
func (s *Store) RecordRuleSetRolledBack(ctx context.Context, tag string, size int64, contentID string, at time.Time) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE rule_sets
		    SET size = $2, content_id = $3, fetched_at = COALESCE(prev_fetched_at, $4),
		        prev_size = 0, prev_content_id = '', prev_fetched_at = NULL
		  WHERE tag = $1`, tag, size, contentID, at)
	return err
}

// AutoUpdateRuleSets returns the tags that follow their source without being
// asked.
func (s *Store) AutoUpdateRuleSets(ctx context.Context) ([]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT tag FROM rule_sets WHERE auto_update ORDER BY tag`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var tag string
		if err := rows.Scan(&tag); err != nil {
			return nil, err
		}
		out = append(out, tag)
	}
	return out, rows.Err()
}
