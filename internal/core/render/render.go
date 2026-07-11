// Package render compiles the model.State into per-node desired-state
// artifacts: TrustTunnel credentials.toml and sing-box.json for entry nodes,
// sing-box.json for exit nodes.
//
// The entry render upholds one invariant: the active-user set drives THREE
// places from one projection — credentials.toml [[client]], sing-box inbound
// users[], and v2ray_api stats users[]. Verified against sing-box v1.13.13.
package render

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"trustpanel/internal/core/model"
)

const (
	DefaultMixedListen       = "127.0.0.1"
	DefaultMixedPort         = 2080
	DefaultV2RayAPIListen    = "127.0.0.1:8088"
	DefaultRuleSetDir        = "rulesets"
	DefaultTrustTunnelListen = "0.0.0.0:443"
	DefaultFallbackOrigin    = "127.0.0.1:8080"
	DefaultCertChainPath     = "certs/cert.pem"
	DefaultPrivateKeyPath    = "certs/key.pem"

	credentialsPath = "credentials.toml"
	singboxPath     = "sing-box.json"
	vpnPath         = "vpn.toml"
	hostsPath       = "hosts.toml"
	rulesPath       = "rules.toml"

	realityFlow     = "xtls-rprx-vision"
	utlsFingerprint = "chrome"
)

// Artifact is one rendered file destined for a node.
type Artifact struct {
	Path    string `json:"path"`
	Mode    uint32 `json:"mode"`
	Content string `json:"content"`
	SHA256  string `json:"sha256"`
}

// CompiledNode is the rendered desired-state for one node.
type CompiledNode struct {
	NodeID           string           `json:"node_id"`
	Role             model.PublicRole `json:"role"`
	Files            []Artifact       `json:"files"`
	RequiredRuleSets []string         `json:"required_rule_sets,omitempty"`
	Warnings         []string         `json:"warnings,omitempty"`
}

// Options tunes the local listeners and TrustTunnel endpoint settings the panel
// renders into node configs.
type Options struct {
	MixedListen       string
	MixedPort         int
	V2RayAPIListen    string
	RuleSetDir        string
	TrustTunnelListen string
	FallbackOrigin    string
	CertChainPath     string
	PrivateKeyPath    string
}

func (o *Options) normalize() {
	if o.MixedListen == "" {
		o.MixedListen = DefaultMixedListen
	}
	if o.MixedPort <= 0 {
		o.MixedPort = DefaultMixedPort
	}
	if o.V2RayAPIListen == "" {
		o.V2RayAPIListen = DefaultV2RayAPIListen
	}
	if o.RuleSetDir == "" {
		o.RuleSetDir = DefaultRuleSetDir
	}
	if o.TrustTunnelListen == "" {
		o.TrustTunnelListen = DefaultTrustTunnelListen
	}
	if o.FallbackOrigin == "" {
		o.FallbackOrigin = DefaultFallbackOrigin
	}
	if o.CertChainPath == "" {
		o.CertChainPath = DefaultCertChainPath
	}
	if o.PrivateKeyPath == "" {
		o.PrivateKeyPath = DefaultPrivateKeyPath
	}
}

// RenderNode compiles desired-state for the given node by its public role.
func RenderNode(state model.State, nodeID string, at time.Time, opts Options) (CompiledNode, error) {
	opts.normalize()
	node, ok := state.NodeByID(nodeID)
	if !ok {
		return CompiledNode{}, fmt.Errorf("node %q not found", nodeID)
	}
	switch node.PublicRole {
	case model.RoleEntry:
		return renderEntry(state, node, at, opts)
	case model.RoleExit:
		return renderExit(node, opts)
	default:
		return CompiledNode{}, fmt.Errorf("node %q has invalid public_role %q", nodeID, node.PublicRole)
	}
}

func newArtifact(path string, mode uint32, content string) Artifact {
	sum := sha256.Sum256([]byte(content))
	return Artifact{Path: path, Mode: mode, Content: content, SHA256: hex.EncodeToString(sum[:])}
}
