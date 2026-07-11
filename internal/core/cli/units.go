package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"trustpanel/internal/core/model"
	"trustpanel/internal/core/provision"
)

// provisionUnits reads the systemd unit templates for a node of the given role
// from unitsDir and returns them with the correct enable flags. It is the single
// source of truth for the provisioning unit set, shared by the panel installer
// (buildProvisionConfig) and the break-glass `trustpanel provision` CLI so the
// two paths cannot drift.
//
// Only the agent (and, on entry nodes, the standalone fallback origin) are
// started at provision time. sing-box and trusttunnel are installed but left
// disabled: their configs — and, for an entry, the ACME cert — arrive with the
// first reconcile, and the agent starts them then. Enabling them now would run
// `systemctl enable --now` against a unit with no config and abort provisioning.
//
// An error is returned if any unit template is missing or empty (an empty file
// installs a systemd-"masked" unit that then fails to enable mid-provision).
func provisionUnits(unitsDir string, role model.PublicRole) ([]provision.UnitFile, error) {
	specs := []struct {
		name   string
		enable bool
	}{
		{"trustpanel-agent.service", true},
		{"trustpanel-singbox.service", false},
	}
	if role == model.RoleEntry {
		specs = append(specs,
			struct {
				name   string
				enable bool
			}{"trustpanel-fallback.service", true},
			struct {
				name   string
				enable bool
			}{"trusttunnel.service", false},
		)
	}
	units := make([]provision.UnitFile, 0, len(specs))
	for _, s := range specs {
		b, err := os.ReadFile(filepath.Join(unitsDir, s.name))
		if err != nil {
			return nil, fmt.Errorf("read unit %s: %w", s.name, err)
		}
		if len(b) == 0 {
			return nil, fmt.Errorf("unit %s is empty (would install a masked unit)", s.name)
		}
		units = append(units, provision.UnitFile{Name: s.name, Content: b, Enable: s.enable})
	}
	return units, nil
}
