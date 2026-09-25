// Package jobs is a small in-memory async job tracker for long operations
// (e.g. SSH provisioning) whose progress the UI polls.
package jobs

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

type Status string

const (
	Running   Status = "running"
	Succeeded Status = "succeeded"
	Failed    Status = "failed"
)

// Snapshot is a point-in-time view of a job, safe to serialize.
type Snapshot struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Status     Status     `json:"status"`
	Log        []string   `json:"log"`
	Error      string     `json:"error,omitempty"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

type job struct {
	mu   sync.Mutex
	snap Snapshot
}

func (j *job) append(line string) {
	j.mu.Lock()
	j.snap.Log = append(j.snap.Log, line)
	j.mu.Unlock()
}

func (j *job) finish(err error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	now := time.Now()
	j.snap.FinishedAt = &now
	if err != nil {
		j.snap.Status = Failed
		j.snap.Error = err.Error()
	} else {
		j.snap.Status = Succeeded
	}
}

func (j *job) get() Snapshot {
	j.mu.Lock()
	defer j.mu.Unlock()
	s := j.snap
	s.Log = append([]string(nil), j.snap.Log...)
	return s
}

// Manager tracks running jobs.
type Manager struct {
	mu   sync.Mutex
	jobs map[string]*job
	idFn func() string
}

func NewManager() *Manager {
	return &Manager{jobs: map[string]*job{}, idFn: randID}
}

// Start runs fn in a goroutine, returning a job id immediately. fn reports
// progress by calling log(); its returned error marks the job failed.
func (m *Manager) Start(name string, fn func(log func(string)) error) string {
	id := m.idFn()
	j := &job{snap: Snapshot{ID: id, Name: name, Status: Running, StartedAt: time.Now()}}
	m.mu.Lock()
	m.jobs[id] = j
	m.mu.Unlock()
	go func() {
		defer func() {
			if r := recover(); r != nil {
				j.append("panic: " + toStr(r))
				j.finish(errPanic)
			}
		}()
		j.finish(fn(j.append))
	}()
	return id
}

// Get returns a snapshot of the job.
func (m *Manager) Get(id string) (Snapshot, bool) {
	m.mu.Lock()
	j, ok := m.jobs[id]
	m.mu.Unlock()
	if !ok {
		return Snapshot{}, false
	}
	return j.get(), true
}

var errPanic = panicErr("job panicked")

type panicErr string

func (e panicErr) Error() string { return string(e) }

func toStr(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	if e, ok := v.(error); ok {
		return e.Error()
	}
	return "unknown"
}

func randID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
