package cluster

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// LocalRunner executes commands and writes files on the local machine. It is the
// runner the per-node agents use to do add-standby work on the box they run on
// (the primary's agent for the primary side, the standby's agent for the standby
// side). It satisfies both CmdRunner and provision.Runner.
type LocalRunner struct{}

// Run executes cmd via the shell, returning combined output. On failure the
// output is wrapped into the error so callers see what the command printed.
func (LocalRunner) Run(ctx context.Context, cmd string) (string, error) {
	out, err := exec.CommandContext(ctx, "sh", "-c", cmd).CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// Put writes content to remotePath with the given mode, creating parent dirs. It
// writes to a temp file and renames for atomicity (matching the SSH runner's
// Put semantics).
func (LocalRunner) Put(_ context.Context, content []byte, remotePath string, mode os.FileMode) error {
	if dir := filepath.Dir(remotePath); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	tmp := remotePath + ".tmp"
	if err := os.WriteFile(tmp, content, mode); err != nil {
		return err
	}
	if err := os.Chmod(tmp, mode); err != nil { // WriteFile mode is pre-umask
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, remotePath)
}
