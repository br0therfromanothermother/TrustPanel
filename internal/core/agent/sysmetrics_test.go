package agent

import "testing"

func TestCollectSystemMetrics(t *testing.T) {
	m := CollectSystemMetrics()
	if m == nil {
		t.Fatal("nil metrics")
	}
	if m.CPUCores <= 0 {
		t.Errorf("cpu cores should be > 0, got %d", m.CPUCores)
	}
	if m.MemTotalMB <= 0 {
		t.Errorf("mem total should be > 0, got %d", m.MemTotalMB)
	}
	if m.MemUsedMB < 0 || m.MemUsedMB > m.MemTotalMB {
		t.Errorf("mem used out of range: used=%d total=%d", m.MemUsedMB, m.MemTotalMB)
	}
	if m.DiskTotalGB <= 0 {
		t.Errorf("disk total should be > 0, got %d", m.DiskTotalGB)
	}
	// net + uptime are environment-dependent; just ensure non-negative.
	if m.NetRxBytes < 0 || m.NetTxBytes < 0 || m.UptimeSec < 0 {
		t.Errorf("negative net/uptime: %+v", m)
	}
}
