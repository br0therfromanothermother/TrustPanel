// Package controller is the panel side of the control channel: it turns a
// rendered node config (render.CompiledNode) into an agentapi.DesiredState and
// pushes it to the node's agent over mTLS.
//
// The revision hash covers content only (files + services + checks), NOT the
// epoch, so a promote that re-pushes identical config with a new epoch
// reconciles as no-change.
package controller

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"sort"
	"time"

	"trustpanel/internal/core/agentapi"
	"trustpanel/internal/core/model"
	"trustpanel/internal/core/render"
)

// Managed systemd unit names.
const (
	ServiceTrustTunnel = "trusttunnel.service"
	ServiceSingBox     = "trustpanel-singbox.service"
)

// runningSet is the set of units a node in the given role must have running.
func runningSet(role model.PublicRole) map[string]bool {
	switch role {
	case model.RoleEntry:
		return map[string]bool{ServiceTrustTunnel: true, ServiceSingBox: true}
	case model.RoleExit:
		return map[string]bool{ServiceSingBox: true}
	default:
		return nil
	}
}

// Layout maps render artifact names to absolute paths on the node. These paths
// must sit inside the agent's allowlisted roots.
type Layout struct {
	TrustTunnelDir string
	SingBoxDir     string
}

func DefaultLayout() Layout {
	return Layout{TrustTunnelDir: "/etc/trusttunnel", SingBoxDir: "/etc/trustpanel/singbox"}
}

// Roots returns the directories the agent must allowlist for this layout.
func (l Layout) Roots() []string { return []string{l.TrustTunnelDir, l.SingBoxDir} }

func (l Layout) pathFor(artifact string) (string, bool) {
	switch base := filepath.Base(artifact); base {
	case "credentials.toml", "vpn.toml", "hosts.toml", "rules.toml":
		return filepath.Join(l.TrustTunnelDir, base), true
	case "sing-box.json":
		return filepath.Join(l.SingBoxDir, "sing-box.json"), true
	default:
		return "", false
	}
}

// BuildDesiredState wraps a compiled node into a desired-state for its agent.
// extraFiles are pre-resolved files (already absolute paths), e.g. binary
// geoip/geosite rule-sets the panel distributes for routing policies.
func BuildDesiredState(state model.State, compiled render.CompiledNode, layout Layout, extraFiles []agentapi.File, revisionID int64, issuedAt time.Time) (agentapi.DesiredState, error) {
	files := make([]agentapi.File, 0, len(compiled.Files)+len(extraFiles))
	var checks []agentapi.Check
	for _, a := range compiled.Files {
		abs, ok := layout.pathFor(a.Path)
		if !ok {
			return agentapi.DesiredState{}, fmt.Errorf("no layout path for artifact %q", a.Path)
		}
		files = append(files, agentapi.File{Path: abs, Mode: a.Mode, SHA256: a.SHA256, Body: a.Content})
		switch filepath.Base(abs) {
		case "sing-box.json":
			checks = append(checks, agentapi.Check{Kind: agentapi.CheckSingBox, Path: abs})
		case "vpn.toml":
			checks = append(checks, agentapi.Check{Kind: agentapi.CheckTrustTunnel, Path: abs})
		}
	}
	files = append(files, extraFiles...)

	node, _ := state.NodeByID(compiled.NodeID)
	ds := agentapi.DesiredState{
		Epoch:      state.ControlPlane.Epoch,
		NodeID:     compiled.NodeID,
		RevisionID: revisionID,
		IssuedAt:   issuedAt,
		Files:      files,
		Services:   servicesForNode(node, compiled.Role),
		Checks:     checks,
	}
	ds.RevisionHash = revisionHash(ds)
	return ds, nil
}

// servicesForNode computes the desired service set for a node. The role fixes
// which units must run; the node's ManagedServices (its agent allowlist) adds
// any unit that the agent manages but the current role no longer needs, pushed
// WantStopped. Stops are ordered first so a unit vacating a port (e.g. an entry
// trusttunnel freeing :443) stops before the unit that reclaims it (sing-box
// Reality) is (re)started. Empty ManagedServices = exactly the running set.
func servicesForNode(n model.Node, role model.PublicRole) []agentapi.Service {
	running := runningSet(role)
	var stop []agentapi.Service
	for _, s := range n.ManagedServices {
		if !running[s] {
			stop = append(stop, agentapi.Service{Name: s, Want: agentapi.WantStopped})
		}
	}
	sort.Slice(stop, func(i, j int) bool { return stop[i].Name < stop[j].Name })
	start := make([]agentapi.Service, 0, len(running))
	for name := range running {
		start = append(start, agentapi.Service{Name: name, Want: agentapi.WantRunning})
	}
	sort.Slice(start, func(i, j int) bool { return start[i].Name < start[j].Name })
	return append(stop, start...)
}

// revisionHash is a deterministic content hash (epoch-independent).
func revisionHash(ds agentapi.DesiredState) string {
	files := append([]agentapi.File(nil), ds.Files...)
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	services := append([]agentapi.Service(nil), ds.Services...)
	sort.Slice(services, func(i, j int) bool { return services[i].Name < services[j].Name })
	checks := append([]agentapi.Check(nil), ds.Checks...)
	sort.Slice(checks, func(i, j int) bool { return checks[i].Path < checks[j].Path })

	h := sha256.New()
	fmt.Fprintf(h, "node=%s\n", ds.NodeID)
	for _, f := range files {
		fmt.Fprintf(h, "f|%s|%d|%s\n", f.Path, f.Mode, f.SHA256)
	}
	for _, s := range services {
		fmt.Fprintf(h, "s|%s|%s\n", s.Name, s.Want)
	}
	for _, c := range checks {
		fmt.Fprintf(h, "c|%s|%s\n", c.Kind, c.Path)
	}
	return hex.EncodeToString(h.Sum(nil))
}
