package agent

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"

	"trustpanel/internal/core/agentapi"
)

// ExecChecker runs real post-write validations against config files. Binary
// paths are allowlisted by construction (only these two are ever invoked).
type ExecChecker struct {
	SingBoxBin     string
	TrustTunnelBin string
}

func (c ExecChecker) Check(ctx context.Context, kind, absPath string) error {
	switch kind {
	case agentapi.CheckSingBox:
		if c.SingBoxBin == "" {
			return fmt.Errorf("sing-box binary not configured")
		}
		out, err := exec.CommandContext(ctx, c.SingBoxBin, "check", "-c", absPath).CombinedOutput()
		if err != nil {
			return fmt.Errorf("%v: %s", err, out)
		}
		return nil
	case agentapi.CheckTrustTunnel:
		if c.TrustTunnelBin == "" {
			return fmt.Errorf("trusttunnel binary not configured")
		}
		// Non-listening export check: ask the endpoint to render a client config
		// for one user against the entry's hostname. This parses vpn.toml/
		// hosts.toml exactly as the running server would, so a bad config fails
		// here before restart. The binary requires --address and --client_config,
		// which we read back out of the rendered files.
		dir := filepath.Dir(absPath)
		host := firstTomlValue(filepath.Join(dir, "hosts.toml"), "hostname")
		client := firstTomlValue(filepath.Join(dir, "credentials.toml"), "username")
		if host == "" {
			return fmt.Errorf("trusttunnel check: no hostname in hosts.toml")
		}
		if client == "" {
			// No users yet: nothing to export, but the server config is still
			// valid to serve the fallback site. Skip the export gate.
			return nil
		}
		cmd := exec.CommandContext(ctx, c.TrustTunnelBin,
			"--address", host, "--client_config", client, "--format", "toml", "vpn.toml", "hosts.toml")
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("%v: %s", err, out)
		}
		return nil
	default:
		return fmt.Errorf("unknown check kind %q", kind)
	}
}

var tomlKVRe = regexp.MustCompile(`(?m)^\s*([A-Za-z_]+)\s*=\s*"([^"]*)"`)

// firstTomlValue returns the first value of key in a simple `key = "value"`
// TOML file (the format render emits), or "" if absent/unreadable.
func firstTomlValue(path, key string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, m := range tomlKVRe.FindAllStringSubmatch(string(b), -1) {
		if m[1] == key {
			return m[2]
		}
	}
	return ""
}
