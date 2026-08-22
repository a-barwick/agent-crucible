package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/a-barwick/agent-crucible/internal/cluster"
	"github.com/a-barwick/agent-crucible/internal/critique"
	"github.com/a-barwick/agent-crucible/internal/fault"
	"github.com/a-barwick/agent-crucible/internal/judge"
)

// Evidence is one trial the explainer can read. Paragraphs come from
// these traces, not from a switch on fault type.
type Evidence struct {
	N          int           `json:"n"`
	Outcome    judge.Outcome `json:"outcome"`
	Faults     []fault.Type  `json:"faults"`
	Violations []string      `json:"violations"`
	Events     []string      `json:"events"`
}

type ExplainInput struct {
	Trials   int
	P        float64
	Survival float64
	Clean    float64
	ByFault  []cluster.Cluster
	ByShape  []cluster.Cluster
	Samples  []Evidence
	Client   Client
}

type finding struct {
	Node     string
	Advice   string
	Evidence string
	N        int
	Rate     float64
	Fault    fault.Type
}

func Explain(ctx context.Context, in ExplainInput) critique.Critique {
	findings := mine(in.Samples, in.ByFault)
	var paras []string
	var fixes []critique.Fix
	seenFix := map[string]bool{}
	for _, f := range findings {
		if f.N == 0 {
			continue
		}
		paras = append(paras, fmt.Sprintf(
			"On %d trials the graph logged %q. Completion under that evidence was %s.",
			f.N, f.Evidence, pct(f.Rate),
		))
		if f.Node != "" && f.Advice != "" && !seenFix[f.Node+"|"+f.Advice] {
			seenFix[f.Node+"|"+f.Advice] = true
			fixes = append(fixes, critique.Fix{Node: f.Node, Advice: f.Advice})
		}
	}

	// If we have no traces yet (unit tests, empty suite), fall back to rates.
	if len(paras) == 0 {
		for _, c := range in.ByFault {
			if c.Fault == "" || c.N == 0 {
				continue
			}
			paras = append(paras, fmt.Sprintf(
				"%s fired on %d trials and completed %s of the time.",
				c.Fault.Label(), c.N, pct(c.Rate),
			))
		}
	}

	headline := fmt.Sprintf(
		"The agent completed %s of clean runs but only %s when tool failures were injected at %.0f%%.",
		pct(in.Clean), pct(in.Survival), in.P*100,
	)
	if in.P == 0 {
		headline = fmt.Sprintf("The agent completed %s of %d unperturbed runs.", pct(in.Clean), in.Trials)
		if in.Clean == 1 {
			paras = append([]string{"Cold steel: no faults, every trial finished the objective. Drag the slider."}, paras...)
		}
	}
	if in.P > 0 && in.Survival < in.Clean {
		headline = pickHeadline(in, headline)
	}

	if in.Client != nil && in.P > 0 {
		pack, _ := json.Marshal(map[string]any{
			"headline": headline, "survival": in.Survival, "clean": in.Clean,
			"p": in.P, "findings": findings, "clusters": in.ByShape,
		})
		text, err := in.Client.Complete(ctx,
			"You write architecture critiques for tool-using agents. Return JSON {headline, paragraphs}. Keep the numbers exactly. Name the node that should change.",
			string(pack),
		)
		if err == nil {
			var parsed struct {
				Headline   string   `json:"headline"`
				Paragraphs []string `json:"paragraphs"`
			}
			if json.Unmarshal([]byte(extractJSONObject(text)), &parsed) == nil {
				if parsed.Headline != "" {
					headline = parsed.Headline
				}
				if len(parsed.Paragraphs) > 0 {
					paras = append(parsed.Paragraphs, paras...)
				}
			}
		}
	}

	if len(paras) == 0 && in.P > 0 {
		paras = append(paras, "Faults fired but the explainer found no node-level evidence. Inspect the timeline.")
	}

	return critique.Critique{Headline: headline, Paragraphs: paras, Fixes: fixes}
}

func mine(samples []Evidence, byFault []cluster.Cluster) []finding {
	rate := map[fault.Type]cluster.Cluster{}
	for _, c := range byFault {
		rate[c.Fault] = c
	}
	type key struct{ ev, node string }
	counts := map[key]int{}
	for _, s := range samples {
		for _, ev := range s.Events {
			k := key{ev: ev, node: nodeOf(ev)}
			counts[k]++
		}
	}
	var out []finding
	for k, n := range counts {
		advice, node := adviceFor(k.ev)
		if node == "" {
			node = k.node
		}
		if advice == "" && k.ev == "" {
			continue
		}
		if !interesting(k.ev) {
			continue
		}
		f := finding{Node: node, Advice: advice, Evidence: k.ev, N: n}
		if ft := faultOf(k.ev); ft != "" {
			f.Fault = ft
			f.Rate = rate[ft].Rate
		}
		out = append(out, f)
	}
	// Stable-ish: more frequent first.
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j].N > out[i].N || (out[j].N == out[i].N && out[j].Evidence < out[i].Evidence) {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	if len(out) > 8 {
		out = out[:8]
	}
	return out
}

func interesting(ev string) bool {
	needles := []string{
		"accepted empty", "ignored permission", "trusted stale",
		"hijacked", "defaulted missing", "truncated", "cancelled",
		"cost ceiling", "duplicate delivery", "missing fields",
		"authorize skipped", "retry after timeout",
	}
	low := strings.ToLower(ev)
	for _, n := range needles {
		if strings.Contains(low, n) {
			return true
		}
	}
	return strings.HasPrefix(low, "malformed") || strings.HasPrefix(low, "tool timeout") ||
		strings.HasPrefix(low, "missing permission") || strings.HasPrefix(low, "stale memory")
}

func adviceFor(ev string) (advice, node string) {
	low := strings.ToLower(ev)
	switch {
	case strings.Contains(low, "accepted empty") || strings.Contains(low, "missing fields") || strings.Contains(low, "malformed"):
		return "Add validation before the write node. Required fields on the write tool must be present or the edge should abort.", "write"
	case strings.Contains(low, "ignored permission") || strings.Contains(low, "missing permission"):
		return "Gate the write node on a live permission edge. A 403 is a hard stop, not a transport success.", "authorize"
	case strings.Contains(low, "authorize skipped") || strings.Contains(low, "defaulted missing"):
		return "Do not skip or default check_permission. Missing allowed is a deny.", "authorize"
	case strings.Contains(low, "trusted stale") || strings.Contains(low, "stale memory"):
		return "Treat memory as a cache with a generation. A successful fetch must invalidate it.", "enrich"
	case strings.Contains(low, "hijacked"):
		return "Pin the company to the current objective, not the longest context window.", "lookup"
	case strings.Contains(low, "duplicate"):
		return "Deduplicate by (run_id, tool, args) before any side effect. The notify node needs the same key.", "write"
	case strings.Contains(low, "truncated") || strings.Contains(low, "partial"):
		return "Schema-validate planner JSON. Missing notify or company is a re-prompt, not a silent default.", "plan"
	case strings.Contains(low, "cancelled") || strings.Contains(low, "objective"):
		return "Re-enter plan on every inbound user event. Cancel must be a first-class edge into abort.", "plan"
	case strings.Contains(low, "cost ceiling"):
		return "Surface budget exhaustion as a terminal abort and roll back claimed side effects.", "write"
	case strings.Contains(low, "timeout") || strings.Contains(low, "retry after"):
		return "Retry with backoff and re-enter plan after a terminal timeout. Do not walk notify after a write that never acknowledged.", "lookup"
	default:
		return "", ""
	}
}

func nodeOf(ev string) string {
	for _, n := range []string{"plan", "lookup", "fetch", "enrich", "authorize", "write", "notify"} {
		if strings.Contains(strings.ToLower(ev), n) {
			return n
		}
	}
	return ""
}

func faultOf(ev string) fault.Type {
	low := strings.ToLower(ev)
	switch {
	case strings.Contains(low, "malformed") || strings.Contains(low, "missing fields") || strings.Contains(low, "empty success"):
		return fault.Malformed
	case strings.Contains(low, "timeout"):
		return fault.Timeout
	case strings.Contains(low, "duplicate"):
		return fault.Duplicate
	case strings.Contains(low, "stale"):
		return fault.StaleMemory
	case strings.Contains(low, "permission") || strings.Contains(low, "403"):
		return fault.Permission
	case strings.Contains(low, "truncated") || strings.Contains(low, "partial"):
		return fault.PartialModel
	case strings.Contains(low, "hijack") || strings.Contains(low, "ballast"):
		return fault.ContextPressure
	case strings.Contains(low, "cost"):
		return fault.CostCeiling
	case strings.Contains(low, "cancel") || strings.Contains(low, "objective"):
		return fault.ObjectiveChange
	default:
		return ""
	}
}

func pickHeadline(in ExplainInput, fallback string) string {
	var worst cluster.Cluster
	worst.Rate = 2
	faults := 0
	for _, c := range in.ByFault {
		if c.Fault == "" || c.N < 2 {
			continue
		}
		faults++
		if c.Rate < worst.Rate {
			worst = c
		}
	}
	if worst.Fault == fault.Malformed && faults <= 2 {
		return fmt.Sprintf(
			"The agent completed %s of clean runs but only %s when the CRM tool returned a successful response with missing fields. The graph treats semantic failure as transport success. Add validation before the write node.",
			pct(in.Clean), pct(worst.Rate),
		)
	}
	if worst.Fault != "" {
		return fmt.Sprintf(
			"The agent completed %s of clean runs but only %s when tool failures were injected at %.0f%%. %s recovered %s of the time.",
			pct(in.Clean), pct(in.Survival), in.P*100, worst.Fault.Label(), pct(worst.Rate),
		)
	}
	return fallback
}

func pct(f float64) string {
	return fmt.Sprintf("%.0f%%", f*100)
}
