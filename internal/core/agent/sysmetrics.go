package agent

import (
	"bufio"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	"trustpanel/internal/core/agentapi"
)

// CollectSystemMetrics reads live resource usage from /proc and statfs("/").
// Best-effort: a field that cannot be read is left zero rather than failing the
// whole status. Linux-only (the fleet is Ubuntu).
func CollectSystemMetrics() *agentapi.SystemMetrics {
	m := &agentapi.SystemMetrics{CPUCores: runtime.NumCPU()}
	m.Load1 = readLoad1()
	m.MemUsedMB, m.MemTotalMB = readMem()
	m.DiskUsedGB, m.DiskTotalGB = readDisk("/")
	m.NetRxBytes, m.NetTxBytes = readNet()
	m.UptimeSec = readUptime()
	return m
}

func readLoad1() float64 {
	b, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0
	}
	f := strings.Fields(string(b))
	if len(f) == 0 {
		return 0
	}
	v, _ := strconv.ParseFloat(f[0], 64)
	return v
}

// readMem returns used and total RAM in MiB (used = total - available).
func readMem() (usedMB, totalMB int64) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, 0
	}
	defer f.Close()
	var totalKB, availKB int64
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 {
			continue
		}
		switch fields[0] {
		case "MemTotal:":
			totalKB, _ = strconv.ParseInt(fields[1], 10, 64)
		case "MemAvailable:":
			availKB, _ = strconv.ParseInt(fields[1], 10, 64)
		}
	}
	totalMB = totalKB / 1024
	if totalKB > availKB {
		usedMB = (totalKB - availKB) / 1024
	}
	return usedMB, totalMB
}

// readDisk returns used and total disk for the filesystem at path, in GiB.
func readDisk(path string) (usedGB, totalGB int64) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, 0
	}
	bs := int64(st.Bsize)
	total := int64(st.Blocks) * bs
	free := int64(st.Bavail) * bs
	const gib = 1024 * 1024 * 1024
	totalGB = total / gib
	if total > free {
		usedGB = (total - free) / gib
	}
	return usedGB, totalGB
}

// netSkipPrefixes are virtual/loopback interfaces excluded from the node's
// public traffic total.
var netSkipPrefixes = []string{"lo", "veth", "docker", "br-", "virbr", "tun", "tap", "wg", "sing", "cni", "flannel"}

// readNet sums absolute rx/tx bytes across physical interfaces from
// /proc/net/dev.
func readNet() (rx, tx int64) {
	f, err := os.Open("/proc/net/dev")
	if err != nil {
		return 0, 0
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		i := strings.IndexByte(line, ':')
		if i < 0 {
			continue
		}
		iface := strings.TrimSpace(line[:i])
		if skipIface(iface) {
			continue
		}
		cols := strings.Fields(line[i+1:])
		if len(cols) < 9 {
			continue
		}
		r, _ := strconv.ParseInt(cols[0], 10, 64) // recv bytes
		t, _ := strconv.ParseInt(cols[8], 10, 64) // trans bytes
		rx += r
		tx += t
	}
	return rx, tx
}

func skipIface(name string) bool {
	for _, p := range netSkipPrefixes {
		if name == p || strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

func readUptime() int64 {
	b, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0
	}
	f := strings.Fields(string(b))
	if len(f) == 0 {
		return 0
	}
	v, _ := strconv.ParseFloat(f[0], 64)
	return int64(v)
}
