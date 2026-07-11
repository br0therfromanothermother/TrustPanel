package jobs

import (
	"fmt"
	"testing"
	"time"
)

func waitDone(t *testing.T, m *Manager, id string) Snapshot {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		s, ok := m.Get(id)
		if ok && s.Status != Running {
			return s
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("job did not finish")
	return Snapshot{}
}

func TestJobSucceeds(t *testing.T) {
	m := NewManager()
	id := m.Start("install", func(log func(string)) error {
		log("step 1")
		log("step 2")
		return nil
	})
	s := waitDone(t, m, id)
	if s.Status != Succeeded {
		t.Fatalf("want succeeded, got %s (%s)", s.Status, s.Error)
	}
	if len(s.Log) != 2 || s.Log[0] != "step 1" {
		t.Errorf("unexpected log: %v", s.Log)
	}
	if s.FinishedAt == nil {
		t.Error("finished_at should be set")
	}
}

func TestJobFails(t *testing.T) {
	m := NewManager()
	id := m.Start("install", func(log func(string)) error {
		log("trying")
		return fmt.Errorf("boom")
	})
	s := waitDone(t, m, id)
	if s.Status != Failed || s.Error != "boom" {
		t.Fatalf("want failed/boom, got %s/%s", s.Status, s.Error)
	}
}

func TestJobPanicRecovered(t *testing.T) {
	m := NewManager()
	id := m.Start("install", func(log func(string)) error {
		panic("kaboom")
	})
	s := waitDone(t, m, id)
	if s.Status != Failed {
		t.Fatalf("panicking job should be failed, got %s", s.Status)
	}
}

func TestGetUnknown(t *testing.T) {
	if _, ok := NewManager().Get("nope"); ok {
		t.Error("unknown job should not be found")
	}
}
