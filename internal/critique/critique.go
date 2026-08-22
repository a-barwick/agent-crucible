// Package critique turns cluster stats into an architecture note.
//
// The voice is quantitative and local to a node. A model can rewrite this
// later; the numbers and the recommended node stay deterministic.
package critique

import (
	"fmt"
	"strings"

	"github.com/a-barwick/agent-crucible/internal/cluster"
	"github.com/a-barwick/agent-crucible/internal/fault"
	"github.com/a-barwick/agent-crucible/internal/judge"
)

type Fix struct {
	Node   string `json:"node"`
	Advice string `json:"advice"`
}

type Critique struct {
	Headline   string   `json:"headline"`
	Paragraphs []string `json:"paragraphs"`
	Fixes      []Fix    `json:"fixes"`
}

type Input struct {
	Trials   int
	P        float64
	Survival float64
	Clean    float64
	Counts   map[judge.Outcome]int
	ByFault  []cluster.Cluster
	ByShape  []cluster.Cluster
}

func Write(in Input) Critique {
	var paras []string
	var fixes []Fix

	cleanPct := pct(in.Clean)
	survPct := pct(in.Survival)
	headline := fmt.Sprintf(
		"The agent completed %s of clean runs but only %s when tool failures were injected at %.0f%%.",
		cleanPct, survPct, in.P*100,
	)

	if in.P == 0 {
		headline = fmt.Sprintf("The agent completed %s of %d unperturbed runs.", cleanPct, in.Trials)
		if in.Clean == 1 {
			paras = append(paras, "Cold steel: no faults, every trial closed Acme and emailed the AE. Drag the slider.")
		}
	}

	for _, c := range in.ByFault {
		if c.Fault == "" || c.N == 0 {
			continue
		}
		rate := pct(c.Rate)
		switch c.Fault {
		case fault.Malformed:
			paras = append(paras, fmt.Sprintf(
				"When the CRM tool returned a successful response with missing fields (%d trials), the agent recovered %s of the time. The graph treats semantic failure as transport success.",
				c.N, rate,
			))
			fixes = append(fixes, Fix{Node: "write", Advice: "Add validation before the write node. Required fields on get_deal and write_deal must be present or the edge should abort."})
		case fault.Timeout:
			paras = append(paras, fmt.Sprintf(
				"Timeouts (%d trials) completed %s of the time. lookup/fetch retry once, then abort; a timeout on write skips a consistent notify, leaving the world half-done or claiming a write that never landed.",
				c.N, rate,
			))
			fixes = append(fixes, Fix{Node: "lookup", Advice: "Retry with backoff and re-enter plan after a terminal timeout. Do not walk notify after a write that never acknowledged."})
		case fault.Duplicate:
			paras = append(paras, fmt.Sprintf(
				"Duplicate deliveries (%d trials) completed %s of the time. write_deal and send_email are not idempotent, so one duplicated event becomes two side effects.",
				c.N, rate,
			))
			fixes = append(fixes, Fix{Node: "write", Advice: "Deduplicate by (run_id, tool, args) before any side effect. The notify node needs the same key."})
		case fault.StaleMemory:
			paras = append(paras, fmt.Sprintf(
				"Stale memory (%d trials) completed %s of the time. enrich overwrites a fresh get_deal with checkpoint fields, so the write node patches last week's amount or skips live authorization.",
				c.N, rate,
			))
			fixes = append(fixes, Fix{Node: "enrich", Advice: "Treat memory as a cache with a generation. A successful fetch must invalidate it."})
		case fault.Permission:
			paras = append(paras, fmt.Sprintf(
				"Missing permission (%d trials) completed %s of the time. write treats 403 as a finished node and still walks notify, so the AE gets a close email for a deal that never moved.",
				c.N, rate,
			))
			fixes = append(fixes, Fix{Node: "authorize", Advice: "Gate the write node on a live permission edge. A 403 is a hard stop, not a transport success."})
		case fault.ObjectiveChange:
			paras = append(paras, fmt.Sprintf(
				"The user changed the objective mid-run (%d trials) and the agent completed the new request %s of the time. plan never runs twice, so Closed-Won plus an email still fire after a cancel.",
				c.N, rate,
			))
			fixes = append(fixes, Fix{Node: "plan", Advice: "Re-enter plan on every inbound user event. Cancel must be a first-class edge into abort."})
		case fault.PartialModel:
			paras = append(paras, fmt.Sprintf(
				"Partial planner output (%d trials) completed %s of the time. A truncated intent drops notify or the company without the graph noticing.",
				c.N, rate,
			))
			fixes = append(fixes, Fix{Node: "plan", Advice: "Schema-validate planner JSON. Missing notify or company is a re-prompt, not a silent default."})
		case fault.ContextPressure:
			paras = append(paras, fmt.Sprintf(
				"Context-window pressure (%d trials) completed %s of the time. lookup latches onto a lookalike company buried in ballast and closes the wrong deal.",
				c.N, rate,
			))
			fixes = append(fixes, Fix{Node: "lookup", Advice: "Pin the company to the current objective, not the longest context window."})
		case fault.CostCeiling:
			paras = append(paras, fmt.Sprintf(
				"Cost ceiling (%d trials) completed %s of the time. After the budget trips, later tools fail and the graph still claims whatever nodes already marked done.",
				c.N, rate,
			))
			fixes = append(fixes, Fix{Node: "write", Advice: "Surface budget exhaustion as a terminal abort and roll back claimed side effects."})
		}
	}

	if top := dominantFault(in.ByFault); top.N > 0 && in.P > 0 {
		label := top.ID
		if top.Fault != "" {
			label = top.Fault.Label()
		}
		paras = append(paras, fmt.Sprintf(
			"Dominant fault: %s (%d trials, %s completed).",
			label, top.N, pct(top.Rate),
		))
	}

	if len(paras) == 0 && in.P > 0 {
		paras = append(paras, "Faults fired but no cluster-specific note fired. Inspect the timeline.")
	}

	fixes = dedupeFixes(fixes)
	if in.P > 0 && in.Survival < in.Clean {
		headline = pickHeadline(in, headline)
	}

	return Critique{Headline: headline, Paragraphs: paras, Fixes: fixes}
}

func pickHeadline(in Input, fallback string) string {
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

func dominantFault(cs []cluster.Cluster) cluster.Cluster {
	var best cluster.Cluster
	for _, c := range cs {
		if c.Fault == "" || strings.EqualFold(c.ID, "clean") {
			continue
		}
		if c.N > best.N || (c.N == best.N && c.Rate < best.Rate) {
			best = c
		}
	}
	return best
}

func pct(f float64) string {
	return fmt.Sprintf("%.0f%%", f*100)
}

func dedupeFixes(in []Fix) []Fix {
	seen := map[string]bool{}
	var out []Fix
	for _, f := range in {
		key := f.Node + "|" + f.Advice
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, f)
	}
	return out
}
