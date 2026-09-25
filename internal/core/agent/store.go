package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"trustpanel/internal/core/agentapi"
)

// localState is the agent's persistent state. It survives agent restarts so the
// epoch fence holds and the cached desired-state can be re-applied on boot.
type localState struct {
	LastAcceptedEpoch int64                  `json:"last_accepted_epoch"`
	AppliedRevision   int64                  `json:"applied_revision"`
	AppliedHash       string                 `json:"applied_hash"`
	Cached            *agentapi.DesiredState `json:"cached,omitempty"`
}

// Store persists localState to a JSON file with atomic writes.
type Store struct {
	path string
	mu   sync.Mutex
	st   localState
}

// OpenStore loads agent state from path, creating empty state if absent.
func OpenStore(path string) (*Store, error) {
	s := &Store{path: path}
	data, err := os.ReadFile(path)
	switch {
	case os.IsNotExist(err):
		return s, nil
	case err != nil:
		return nil, fmt.Errorf("read agent state: %w", err)
	}
	if err := json.Unmarshal(data, &s.st); err != nil {
		return nil, fmt.Errorf("parse agent state %q: %w", path, err)
	}
	return s, nil
}

func (s *Store) snapshot() localState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.st
}

func (s *Store) LastAcceptedEpoch() int64 { return s.snapshot().LastAcceptedEpoch }
func (s *Store) AppliedRevision() int64   { return s.snapshot().AppliedRevision }
func (s *Store) AppliedHash() string      { return s.snapshot().AppliedHash }

// Cached returns a copy of the last applied desired-state, if any.
func (s *Store) Cached() *agentapi.DesiredState {
	st := s.snapshot()
	return st.Cached
}

// bumpEpoch persists last_accepted_epoch = max(current, epoch). Used whenever a
// non-stale controller request is seen, even if its payload is later rejected.
func (s *Store) bumpEpoch(epoch int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if epoch > s.st.LastAcceptedEpoch {
		s.st.LastAcceptedEpoch = epoch
		return s.saveLocked()
	}
	return nil
}

// commitApplied persists the applied revision/hash, caches the desired-state,
// and advances the epoch, atomically.
func (s *Store) commitApplied(ds agentapi.DesiredState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ds.Epoch > s.st.LastAcceptedEpoch {
		s.st.LastAcceptedEpoch = ds.Epoch
	}
	s.st.AppliedRevision = ds.RevisionID
	s.st.AppliedHash = ds.RevisionHash
	cp := ds
	s.st.Cached = &cp
	return s.saveLocked()
}

func (s *Store) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s.st, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(s.path, data, 0o600)
}
