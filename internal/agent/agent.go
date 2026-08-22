// Package agent is the subject under test: a graph plus the tools it claims.
package agent

import (
	"context"

	"github.com/a-barwick/agent-crucible/internal/schema"
	"github.com/a-barwick/agent-crucible/internal/trace"
)

// Spec is what the UI and the runner know about an agent without running it.
type Spec struct {
	Name        string        `json:"name"`
	Framework   string        `json:"framework"`
	Description string        `json:"description"`
	Tools       []schema.Tool `json:"tools"`
	Graph       GraphSpec     `json:"graph"`
	Bugs        []Bug         `json:"bugs"`
}

type Bug struct {
	ID    string `json:"id"`
	Node  string `json:"node"`
	Title string `json:"title"`
	Why   string `json:"why"`
}

type GraphSpec struct {
	Start string   `json:"start"`
	Nodes []string `json:"nodes"`
	Edges []Edge   `json:"edges"`
}

type Edge struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// Result is what a graph run leaves behind besides the world mutations.
type Result struct {
	Terminal string `json:"terminal"`
	Intent   Intent `json:"intent"`
	Claimed  Claim  `json:"claimed"`
	Steps    int    `json:"steps"`
}

// Intent is the planner's parse of the (possibly stale) objective.
type Intent struct {
	Company    string `json:"company"`
	DealAction string `json:"deal_action"` // close_won | on_hold | none
	Notify     bool   `json:"notify"`
}

// Claim is what the agent believes it did. The judge compares this to the world.
type Claim struct {
	Wrote    bool   `json:"wrote"`
	Notified bool   `json:"notified"`
	DealID   string `json:"deal_id"`
	Status   string `json:"status"`
	Error    string `json:"error"`
}

// Memory is the checkpoint the graph trusts more than a fresh fetch.
type Memory struct {
	DealID       string `json:"deal_id,omitempty"`
	DealStatus   string `json:"deal_status,omitempty"`
	Amount       int    `json:"amount,omitempty"`
	OwnerID      string `json:"owner_id,omitempty"`
	HasWritePerm bool   `json:"has_write_perm,omitempty"`
	Company      string `json:"company,omitempty"`
}

// State is the LangGraph-style reducible blob passed between nodes.
type State struct {
	Objective string
	Intent    Intent
	Memory    Memory
	ContactID string
	AE        string
	DealID    string
	Status    string
	Amount    int
	CloseDate string
	OwnerID   string
	Permitted bool
	Wrote     bool
	Notified  bool
	LastError string
	History   []string
	Junk      string // context-pressure ballast
}

// Bus is how the agent reaches tools. The harness wraps the world with faults.
type Bus interface {
	Call(ctx context.Context, tool string, args map[string]any) (schema.Result, error)
}

// Hook is invoked at node boundaries so the harness can change the objective.
type Hook interface {
	BeforeNode(ctx context.Context, name string, st *State, rec *trace.Recorder)
}

// Agent is anything the chamber can torture.
type Agent interface {
	Spec() Spec
	Run(ctx context.Context, st State, bus Bus, rec *trace.Recorder, hook Hook) (Result, error)
}
