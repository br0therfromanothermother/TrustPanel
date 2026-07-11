package agent

import (
	"context"
	"os/exec"
	"strings"

	"trustpanel/internal/core/agentapi"
)

// SystemdManager controls units via systemctl. It is the production
// ServiceManager; tests inject a fake.
type SystemdManager struct{}

func (SystemdManager) Restart(ctx context.Context, name string) error {
	return exec.CommandContext(ctx, "systemctl", "restart", name).Run()
}

func (SystemdManager) Stop(ctx context.Context, name string) error {
	return exec.CommandContext(ctx, "systemctl", "stop", name).Run()
}

func (SystemdManager) Status(ctx context.Context, name string) (string, error) {
	out, _ := exec.CommandContext(ctx, "systemctl", "is-active", name).Output()
	state := strings.TrimSpace(string(out))
	if state == "" {
		state = "unknown"
	}
	return state, nil
}

// ServicesStatus reports the observed state of the given units for /v1/status.
func (m SystemdManager) ServicesStatus(ctx context.Context, names []string) []agentapi.ServiceStatus {
	out := make([]agentapi.ServiceStatus, 0, len(names))
	for _, n := range names {
		state, _ := m.Status(ctx, n)
		out = append(out, agentapi.ServiceStatus{Name: n, State: state})
	}
	return out
}
