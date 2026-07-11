package controller

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"time"

	"trustpanel/internal/core/agentapi"
	"trustpanel/internal/core/model"
	"trustpanel/internal/core/render"
)

// RuleSetProvider resolves a geoip/geosite rule-set tag (e.g. "geoip-ru") to its
// binary .srs bytes. The panel distributes these to nodes for routing policies.
type RuleSetProvider interface {
	Get(ctx context.Context, tag string) ([]byte, error)
}

// Fleet pushes rendered desired-state to every node's agent. It is the panel's
// reconcile loop body: load State, render each node, push to its agent.
type Fleet struct {
	client *Client
	layout Layout
	opts   render.Options
	// URLFor returns the agent base URL for a node; defaults to https://AgentAddr.
	URLFor func(model.Node) string
	// RuleSets distributes geoip/geosite .srs files for routing policies; nil
	// means geo policies cannot be applied (rendered tags will error).
	RuleSets RuleSetProvider
}

func NewFleet(client *Client, layout Layout, opts render.Options) *Fleet {
	return &Fleet{
		client: client, layout: layout, opts: opts,
		URLFor: func(n model.Node) string { return "https://" + n.AgentAddr },
	}
}

// NodeOutcome is the per-node result of a fleet reconcile.
type NodeOutcome struct {
	Result agentapi.ReconcileResult
	Err    error
	// Warnings are the render's compile-time correctness warnings about the pushed
	// desired state (e.g. an exclusion skipped, a drained exit falling back to
	// local egress).
	Warnings []string
}

// Reconcile renders and pushes desired-state to every node in the state. The
// epoch comes from state.ControlPlane; revisionID should increase per push.
// Nodes are processed independently; a failure on one does not stop others.
func (f *Fleet) Reconcile(ctx context.Context, state model.State, revisionID int64, at time.Time) map[string]NodeOutcome {
	out := make(map[string]NodeOutcome, len(state.Nodes))
	for _, n := range state.Nodes {
		out[n.ID] = f.reconcileNode(ctx, state, n, revisionID, at)
	}
	return out
}

// Status fetches a node agent's /v1/status over the fleet's mTLS client, using
// the same URL derivation as reconcile (node.AgentAddr). Used by the panel
// stats loop.
func (f *Fleet) Status(ctx context.Context, n model.Node) (agentapi.StatusResponse, error) {
	return f.client.Status(ctx, f.URLFor(n))
}

// PromoteACME tells a node's agent to switch ACME issuance to production and
// reissue, over the same mTLS channel as reconcile.
func (f *Fleet) PromoteACME(ctx context.Context, n model.Node) error {
	return f.client.PromoteACME(ctx, f.URLFor(n))
}

// PrepareStandby asks the primary node's agent to provision the standby (over
// the same mTLS channel as reconcile). The panel handles no secrets: the primary
// agent forwards the CA material straight to the standby's agent.
func (f *Fleet) PrepareStandby(ctx context.Context, primary model.Node, in agentapi.PrepareStandbyRequest) (agentapi.PrepareStandbyResult, error) {
	return f.client.PrepareStandby(ctx, f.URLFor(primary), in)
}

func (f *Fleet) reconcileNode(ctx context.Context, state model.State, n model.Node, revisionID int64, at time.Time) NodeOutcome {
	compiled, err := render.RenderNode(state, n.ID, at, f.opts)
	if err != nil {
		return NodeOutcome{Err: err}
	}
	extra, err := f.ruleSetFiles(ctx, compiled.RequiredRuleSets)
	if err != nil {
		return NodeOutcome{Err: err}
	}
	ds, err := BuildDesiredState(state, compiled, f.layout, extra, revisionID, at)
	if err != nil {
		return NodeOutcome{Err: err}
	}
	res, err := f.client.PushDesiredState(ctx, f.URLFor(n), ds)
	return NodeOutcome{Result: res, Err: err, Warnings: compiled.Warnings}
}

// ruleSetFiles fetches each required geoip/geosite .srs and turns it into a
// base64 desired-state file at <SingBoxDir>/rulesets/<tag>.srs (matching the
// relative "rulesets/<tag>.srs" path the render emits, resolved by sing-box -D).
func (f *Fleet) ruleSetFiles(ctx context.Context, tags []string) ([]agentapi.File, error) {
	if len(tags) == 0 {
		return nil, nil
	}
	if f.RuleSets == nil {
		return nil, fmt.Errorf("routing policy needs rule-sets %v but no rule-set provider is configured", tags)
	}
	out := make([]agentapi.File, 0, len(tags))
	for _, tag := range tags {
		b, err := f.RuleSets.Get(ctx, tag)
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(b)
		out = append(out, agentapi.File{
			Path:     f.layout.SingBoxDir + "/rulesets/" + tag + ".srs",
			Mode:     0o644,
			SHA256:   hex.EncodeToString(sum[:]),
			Body:     base64.StdEncoding.EncodeToString(b),
			Encoding: "base64",
		})
	}
	return out, nil
}
