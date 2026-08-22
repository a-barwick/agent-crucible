package agent

import (
	"context"
	"fmt"

	"github.com/a-barwick/agent-crucible/internal/clock"
	"github.com/a-barwick/agent-crucible/internal/trace"
)

// NodeFunc runs one graph node and returns the next node name.
type NodeFunc func(ctx context.Context, st *State, bus Bus, rec *trace.Recorder) (next string, err error)

// Graph is a tiny LangGraph stand-in: named nodes, explicit edges, shared state.
type Graph struct {
	Name     string
	Start    string
	Nodes    map[string]NodeFunc
	MaxSteps int
	Clock    *clock.Clock
}

func (g *Graph) Run(ctx context.Context, st *State, bus Bus, rec *trace.Recorder, hook Hook) (Result, error) {
	max := g.MaxSteps
	if max <= 0 {
		max = 16
	}
	node := g.Start
	steps := 0
	for node != "" && node != "end" && node != "abort" {
		if err := ctx.Err(); err != nil {
			return Result{Terminal: "abort", Steps: steps}, err
		}
		fn, ok := g.Nodes[node]
		if !ok {
			return Result{Terminal: "abort", Steps: steps}, fmt.Errorf("unknown node %q", node)
		}
		if hook != nil {
			hook.BeforeNode(ctx, node, st, rec)
		}
		rec.NodeEnter(node)
		if g.Clock != nil {
			g.Clock.Advance(1)
		}
		next, err := fn(ctx, st, bus, rec)
		if err != nil {
			rec.NodeExit(node, "abort")
			return Result{
				Terminal: "abort",
				Intent:   st.Intent,
				Claimed:  claimOf(st),
				Steps:    steps + 1,
			}, err
		}
		rec.NodeExit(node, next)
		steps++
		node = next
		if steps >= max {
			st.LastError = "max_steps"
			node = "abort"
		}
	}
	terminal := node
	if terminal == "" {
		terminal = "end"
	}
	return Result{
		Terminal: terminal,
		Intent:   st.Intent,
		Claimed:  claimOf(st),
		Steps:    steps,
	}, nil
}

func claimOf(st *State) Claim {
	return Claim{
		Wrote:    st.Wrote,
		Notified: st.Notified,
		DealID:   st.DealID,
		Status:   st.Status,
		Error:    st.LastError,
	}
}

// ParseIntent is a stand-in for a planner LLM: string matching, not a model.
// The runner stays deterministic; "AI generates scenarios" lives elsewhere.
func ParseIntent(objective string) Intent {
	in := Intent{Company: "Acme Corp", DealAction: "close_won", Notify: true}
	low := objective
	if containsFold(low, "Acme Supplies") {
		in.Company = "Acme Supplies"
	} else if containsFold(low, "Acme") {
		in.Company = "Acme Corp"
	}
	switch {
	case containsFold(low, "On-Hold") || containsFold(low, "on hold") || containsFold(low, "do not close") || containsFold(low, "Stop."):
		in.DealAction = "on_hold"
	case containsFold(low, "Closed-Won") || containsFold(low, "close"):
		in.DealAction = "close_won"
	default:
		in.DealAction = "none"
	}
	if containsFold(low, "do not email") || containsFold(low, "do not email anyone") {
		in.Notify = false
	} else if containsFold(low, "email") {
		in.Notify = true
	}
	return in
}

func containsFold(s, sub string) bool {
	return len(s) >= len(sub) && indexFold(s, sub) >= 0
}

func indexFold(s, sub string) int {
	// ASCII fold is enough for the fixture copy.
	ls := []byte(s)
	lb := []byte(sub)
	for i := range ls {
		if ls[i] >= 'A' && ls[i] <= 'Z' {
			ls[i] += 32
		}
	}
	for i := range lb {
		if lb[i] >= 'A' && lb[i] <= 'Z' {
			lb[i] += 32
		}
	}
	ss, sb := string(ls), string(lb)
	for i := 0; i+len(sb) <= len(ss); i++ {
		if ss[i:i+len(sb)] == sb {
			return i
		}
	}
	return -1
}
