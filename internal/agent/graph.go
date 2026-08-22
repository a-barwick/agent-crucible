package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/a-barwick/agent-crucible/internal/clock"
	"github.com/a-barwick/agent-crucible/internal/trace"
)

// NodeFunc runs one graph node and returns the next node name.
type NodeFunc func(ctx context.Context, st *State, bus Bus, rec *trace.Recorder) (next string, err error)

// Graph is a LangGraph-shaped runtime: named nodes, explicit edges, shared
// state, and a checkpointer. The Python sidecar is the real LangGraph compile.
type Graph struct {
	Name         string
	Start        string
	Nodes        map[string]NodeFunc
	MaxSteps     int
	Clock        *clock.Clock
	Checkpointer Checkpointer
}

func (g *Graph) Run(ctx context.Context, st *State, bus Bus, rec *trace.Recorder, hook Hook) (Result, error) {
	max := g.MaxSteps
	if max <= 0 {
		max = 16
	}
	node := g.Start
	steps := 0
	if g.Checkpointer != nil && st.ThreadID != "" {
		g.Checkpointer.Put(st.ThreadID, Checkpoint{State: *st, Node: g.Start})
	}
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
		if g.Checkpointer != nil && st.ThreadID != "" {
			g.Checkpointer.Put(st.ThreadID, Checkpoint{State: *st, Node: node, Step: steps + 1})
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
		Intent:   st.Intent.SyncAliases(),
		Claimed:  claimOf(st),
		Steps:    steps,
	}, nil
}

func claimOf(st *State) Claim {
	return Claim{
		Wrote:    st.Wrote,
		Notified: st.Notified,
		DealID:   st.DealID,
		RecordID: st.DealID,
		Status:   st.Status,
		Error:    st.LastError,
	}
}

// ParseIntent is the fallback parser used by the judge and ScriptedModel.
func ParseIntent(objective string) Intent {
	return ParseIntentWith(objective, nil)
}

func ParseIntentWith(objective string, companies []string) Intent {
	if len(companies) == 0 {
		companies = []string{"Acme Corp", "Acme Supplies"}
	}
	in := Intent{Company: companies[0], DealAction: "close_won", Notify: true}
	best, bestLen := "", 0
	for _, c := range companies {
		if containsFold(objective, c) && len(c) > bestLen {
			best = c
			bestLen = len(c)
		}
	}
	if best != "" {
		in.Company = best
	} else if containsFold(objective, "Acme") {
		for _, c := range companies {
			if c == "Acme Corp" {
				in.Company = c
				break
			}
		}
	}
	switch {
	case containsFold(objective, "refund"):
		in.DealAction = "refund"
		in.Notify = false
	case containsFold(objective, "On-Hold") || containsFold(objective, "on hold") || containsFold(objective, "do not close") || containsFold(objective, "Stop."):
		in.DealAction = "on_hold"
	case containsFold(objective, "resolve"):
		in.DealAction = "resolve"
		in.Notify = false
	case containsFold(objective, "Closed-Won") || containsFold(objective, "close"):
		in.DealAction = "close_won"
	default:
		in.DealAction = "none"
	}
	if containsFold(objective, "do not email") {
		in.Notify = false
	} else if containsFold(objective, "email") {
		in.Notify = true
	}
	return in.SyncAliases()
}

// ParseModelIntent prefers planner JSON; missing notify stays false.
func ParseModelIntent(text, fallback string, companies []string) Intent {
	text = strings.TrimSpace(text)
	if i := strings.Index(text, "{"); i >= 0 {
		if j := strings.LastIndex(text, "}"); j > i {
			text = text[i : j+1]
		}
	}
	var in Intent
	if err := json.Unmarshal([]byte(text), &in); err == nil && (in.Company != "" || in.Entity != "" || in.DealAction != "" || in.Action != "") {
		in = in.SyncAliases()
		if in.Company == "" {
			in.Company = ParseIntentWith(fallback, companies).Company
			in = in.SyncAliases()
		}
		return in
	}
	return ParseIntentWith(fallback, companies)
}

func containsFold(s, sub string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(sub))
}

// ActionStatus is the world status a deal action is supposed to write.
func ActionStatus(action string) string {
	switch action {
	case "close_won":
		return "Closed-Won"
	case "on_hold":
		return "On-Hold"
	case "refund":
		return "Refunded"
	case "resolve":
		return "Resolved"
	default:
		return ""
	}
}
