package cli

import (
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"sort"
	"strings"
)

// RunFirewall applies the control-port allow-list the controller pushes to this
// node. It is invoked by the trustpanel-firewall.service unit (driven by a
// timer) as `trustpanel firewall --allow <file> --port <n>`.
//
// It is deliberately fail-open: a missing or empty list leaves the port open,
// and rules are added before the broad "open" rule is removed, so a controller
// whose IP is in the list never loses access mid-apply. ufw being absent or
// inactive is a no-op: enabling ufw is the hardening step's job, not this one.
func RunFirewall(args []string) {
	fs := flag.NewFlagSet("firewall", flag.ExitOnError)
	allow := fs.String("allow", "", "path to the control-port allow-list file")
	port := fs.Int("port", 8443, "control port to scope")
	_ = fs.Parse(args)

	if *allow == "" {
		fmt.Fprintln(os.Stderr, "firewall: --allow is required")
		os.Exit(2)
	}
	run := func(name string, a ...string) (string, error) {
		out, err := exec.Command(name, a...).CombinedOutput()
		return string(out), err
	}
	if err := applyFirewall(run, *allow, *port); err != nil {
		fmt.Fprintln(os.Stderr, "firewall: "+err.Error())
		os.Exit(1)
	}
}

type cmdRunner func(name string, args ...string) (string, error)

// applyFirewall reconciles ufw so the control port is reachable only from the
// listed source IPs. It no-ops when ufw is missing or inactive.
func applyFirewall(run cmdRunner, allowPath string, port int) error {
	if _, err := exec.LookPath("ufw"); err != nil {
		return nil // no ufw on this box: nothing to enforce
	}
	status, err := run("ufw", "status")
	if err != nil {
		return fmt.Errorf("ufw status: %w", err)
	}
	st := parseUfwState(status, port)
	if !st.active {
		return nil // ufw not enabled yet; the hardening step owns that
	}

	desired := readAllowList(allowPath)
	for _, cmd := range planUfwRules(st, desired, port) {
		if out, err := run("ufw", cmd...); err != nil {
			// Abort on the first failure. Rules are ordered add-before-remove, so
			// aborting always leaves the port at least as reachable as it was, and
			// never strands the controller behind a half-applied restriction.
			return fmt.Errorf("ufw %s: %w: %s", strings.Join(cmd, " "), err, strings.TrimSpace(out))
		}
	}
	return nil
}

// ufwState is the part of `ufw status` that concerns one port.
type ufwState struct {
	active    bool
	broadOpen bool            // the port is allowed from anywhere
	perIP     map[string]bool // source IPs individually allowed to the port
}

// parseUfwState reads `ufw status` output for the rules touching port. IPv6
// duplicate lines ufw emits for a broad rule are folded into broadOpen; only
// explicit IPv4/IPv6 source addresses populate perIP.
func parseUfwState(status string, port int) ufwState {
	st := ufwState{perIP: map[string]bool{}}
	portTok := fmt.Sprintf("%d/tcp", port)
	for _, line := range strings.Split(status, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Status:") {
			st.active = strings.TrimSpace(strings.TrimPrefix(line, "Status:")) == "active"
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 || fields[0] != portTok || !containsField(fields, "ALLOW") {
			continue
		}
		from := fields[len(fields)-1]
		if from == "(v6)" && len(fields) >= 2 {
			from = fields[len(fields)-2] // "Anywhere (v6)"
		}
		if from == "Anywhere" {
			st.broadOpen = true
			continue
		}
		// Store the canonical form so the diff against the desired list (also
		// canonicalized) is insensitive to how the address was written.
		if ip := net.ParseIP(from); ip != nil {
			st.perIP[ip.String()] = true
		}
	}
	return st
}

func containsField(fields []string, want string) bool {
	for _, f := range fields {
		if f == want {
			return true
		}
	}
	return false
}

// planUfwRules is the pure decision: given the current ufw state, the desired
// source IPs and the port, it returns the ufw argument lists to run, in order.
// Adds come first, then the broad-open removal, then removal of any source no
// longer wanted, so the control channel is never closed before its replacement
// rule exists. Returns nil when nothing needs to change.
func planUfwRules(st ufwState, desired []string, port int) [][]string {
	portTCP := fmt.Sprintf("%d/tcp", port)
	portStr := fmt.Sprintf("%d", port)

	// Empty list = fail-open: keep the port open, drop any leftover scoping.
	if len(desired) == 0 {
		var cmds [][]string
		if !st.broadOpen {
			cmds = append(cmds, []string{"allow", portTCP})
		}
		for _, ip := range sortedSet(st.perIP) {
			cmds = append(cmds, []string{"delete", "allow", "from", ip, "to", "any", "port", portStr, "proto", "tcp"})
		}
		return cmds
	}

	want := map[string]bool{}
	for _, ip := range desired {
		want[ip] = true
	}
	var cmds [][]string
	for _, ip := range sortedSet(want) {
		if !st.perIP[ip] {
			cmds = append(cmds, []string{"allow", "from", ip, "to", "any", "port", portStr, "proto", "tcp"})
		}
	}
	if st.broadOpen {
		cmds = append(cmds, []string{"delete", "allow", portTCP})
	}
	for _, ip := range sortedSet(st.perIP) {
		if !want[ip] {
			cmds = append(cmds, []string{"delete", "allow", "from", ip, "to", "any", "port", portStr, "proto", "tcp"})
		}
	}
	return cmds
}

func sortedSet(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// readAllowList parses the allow-list file into valid source IPs. A missing file
// yields an empty list (fail-open); blank lines, comments and unparseable lines
// are skipped so a malformed entry can never brick the port.
func readAllowList(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		ip := net.ParseIP(line)
		if ip == nil {
			continue
		}
		canon := ip.String() // canonical form, matched against parsed ufw output
		if seen[canon] {
			continue
		}
		seen[canon] = true
		out = append(out, canon)
	}
	sort.Strings(out)
	return out
}
