// Package agent is the subject under test: a graph plus the tools it claims.
package agent

import (
	"context"

	"github.com/a-barwick/agent-crucible/internal/schema"
	"github.com/a-barwick/agent-crucible/internal/trace"
)

// Spec is what the UI and the runner know about an agent without running it.
// Paste a Spec (tools + graph) to drop an arbitrary tool-using agent into the chamber.
type Spec struct {
	Name        string                 `json:"name"`
	Framework   string                 `json:"framework"`
	Runtime     string                 `json:"runtime,omitempty"` // go | langgraph | adk
	Description string                 `json:"description"`
	Tools       []schema.Tool          `json:"tools"`
	Graph       GraphSpec              `json:"graph"`
	NodeTools   map[string]NodeBinding `json:"node_tools,omitempty"`
	Companies   []string               `json:"companies,omitempty"`
	Objective   string                 `json:"objective,omitempty"`
	Entry       string                 `json:"entry,omitempty"`    // Python file or module the sidecar imports
	Export      string                 `json:"export,omitempty"`   // run | build | graph | named callable
	Endpoint    string                 `json:"endpoint,omitempty"` // arbitrary process speaking POST /v1/run
	Bugs        []Bug                  `json:"bugs"`
}

// NodeBinding tells the generic runner what a named node does.
type NodeBinding struct {
	Kind     string            `json:"kind"` // plan|lookup|fetch|enrich|authorize|write|notify|tool
	Tool     string            `json:"tool,omitempty"`
	ArgsFrom map[string]string `json:"args_from,omitempty"`
	Save     map[string]string `json:"save,omitempty"`
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
	ThreadID  string
	Companies []string
	Partial   bool
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
