package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/a-barwick/agent-crucible/internal/agent"
	"github.com/a-barwick/agent-crucible/internal/ai"
	"github.com/a-barwick/agent-crucible/internal/fault"
	"github.com/a-barwick/agent-crucible/internal/harness"
	"github.com/a-barwick/agent-crucible/internal/runtime"
	"github.com/a-barwick/agent-crucible/internal/scenario"
	"github.com/a-barwick/agent-crucible/internal/server"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "serve":
		serveCmd(os.Args[2:])
	case "run":
		runCmd(os.Args[2:])
	case "replay":
		replayCmd(os.Args[2:])
	case "agents":
		agentsCmd()
	case "scenarios":
		scenariosCmd()
	case "generate":
		generateCmd(os.Args[2:])
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `crucible — a torture chamber for tool-using agents

Usage:
  crucible serve     [-addr :8080]
  crucible run       [-seed 42] [-trials 40] [-p 0.3] [-agent aether-closer] [-scenario close-acme] [-faults ...] [-json]
  crucible replay    [-seed 42] [-trial 0] [-p 0.3] [-agent ...] [-scenario ...] [-json]
  crucible agents
  crucible scenarios
  crucible generate  [-n 5] [-json]

The runner is deterministic. Same seed, trial, p, and fault set replay bit-for-bit.
AI generates scenarios, scores ambiguous traces, and explains patterns. It does not pick faults.
`)
}

func serveCmd(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := fs.String("addr", ":8080", "listen address")
	_ = fs.Parse(args)
	if rt, err := runtime.EnsureLocal(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "python runtime: %v (langgraph/adk agents unavailable)\n", err)
	} else {
		fmt.Fprintf(os.Stderr, "python runtime %s\n", rt.URL)
		defer rt.Stop()
	}
	h := server.New()
	s := &http.Server{Addr: *addr, Handler: h, ReadHeaderTimeout: 5 * time.Second}
	fmt.Fprintf(os.Stderr, "crucible listening on %s\n", *addr)
	if err := s.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func runCmd(args []string) {
	cfg, asJSON := flags(args, false)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	suite := harness.Run(ctx, cfg)
	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(suite)
		return
	}
	fmt.Printf("suite %s  seed=%d  trials=%d  p=%.0f%%\n", suite.ID, cfg.Seed, cfg.Trials, cfg.P*100)
	fmt.Printf("survival %.0f%%   safety %.0f%%   clean %.0f%%\n",
		suite.Survival*100, suite.Safety*100, suite.CleanRate*100)
	fmt.Printf("counts %v\n\n", suite.Counts)
	fmt.Println(suite.Critique.Headline)
	for _, p := range suite.Critique.Paragraphs {
		fmt.Println()
		fmt.Println(p)
	}
	if len(suite.Critique.Fixes) > 0 {
		fmt.Println("\nfixes:")
		for _, f := range suite.Critique.Fixes {
			fmt.Printf("  [%s] %s\n", f.Node, f.Advice)
		}
	}
	fmt.Println("\nclusters:")
	for _, c := range suite.ByFault {
		fmt.Printf("  %-22s n=%-3d completed=%.0f%%\n", c.ID, c.N, c.Rate*100)
	}
}

func replayCmd(args []string) {
	fs := flag.NewFlagSet("replay", flag.ExitOnError)
	seed := fs.Int64("seed", 42, "suite seed")
	trials := fs.Int("trials", 40, "suite size (for stream mixing)")
	n := fs.Int("trial", 0, "trial index")
	p := fs.Float64("p", 0.30, "failure probability")
	faults := fs.String("faults", "", "comma-separated fault types")
	ag := fs.String("agent", agent.IDCloser, "agent id")
	scn := fs.String("scenario", scenario.CloseAcmeID, "scenario id")
	asJSON := fs.Bool("json", false, "print JSON")
	_ = fs.Parse(args)
	cfg := harness.Config{Seed: *seed, Trials: *trials, P: *p, Faults: parseFaults(*faults), Agent: *ag, Scenario: *scn}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tr := harness.Replay(ctx, cfg, *n)
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(tr)
		return
	}
	fmt.Printf("trial %d  %s  %s\n", tr.N, tr.Outcome, tr.Reason)
	if len(tr.Faults) > 0 {
		fmt.Printf("faults %v\n", tr.Faults)
	}
	if len(tr.Violations) > 0 {
		fmt.Printf("violations %v\n", tr.Violations)
	}
	fmt.Println()
	for _, ev := range tr.Events {
		mark := " "
		if ev.Kind == "fault" {
			mark = "!"
		}
		fmt.Printf("%s %3d t=%-3d %-12s %-10s %s\n", mark, ev.Seq, ev.Tick, ev.Kind, ev.Node, ev.Message)
	}
}

func flags(args []string, _ bool) (harness.Config, bool) {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	seed := fs.Int64("seed", 42, "suite seed")
	trials := fs.Int("trials", 40, "trial count")
	p := fs.Float64("p", 0.30, "tool failure probability")
	faults := fs.String("faults", "", "comma-separated fault types (default: MVP five)")
	ag := fs.String("agent", agent.IDCloser, "agent id (aether-closer, aether-closer-langgraph, aether-closer-adk, pasted)")
	scn := fs.String("scenario", scenario.CloseAcmeID, "scenario id")
	asJSON := fs.Bool("json", false, "print JSON")
	_ = fs.Parse(args)
	return harness.Config{Seed: *seed, Trials: *trials, P: *p, Faults: parseFaults(*faults), Agent: *ag, Scenario: *scn}, *asJSON
}

func agentsCmd() {
	st := runtime.CurrentStatus()
	for _, a := range agent.Catalog(st.Ready || runtime.HaveLangGraph()) {
		mark := " "
		if a.Available || a.Runtime == "in-process" || a.Runtime == "spec" {
			mark = "*"
		}
		fmt.Printf("%s %-24s %-14s %s\n", mark, a.ID, a.Framework, a.Description)
	}
}

func scenariosCmd() {
	for _, s := range scenario.Summaries() {
		fmt.Printf("%-16s  %s\n    %s\n", s.ID, s.Name, s.Objective)
	}
}

func generateCmd(args []string) {
	fs := flag.NewFlagSet("generate", flag.ExitOnError)
	n := fs.Int("n", 5, "how many")
	seed := fs.Int64("seed", 42, "seed")
	asJSON := fs.Bool("json", false, "print JSON")
	_ = fs.Parse(args)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out := ai.Generate(ctx, *seed, agent.CRMTools(), *n, ai.FromEnv(ai.Config{}))
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(out)
		return
	}
	for _, d := range out {
		fmt.Printf("%s [%s] %s\n  %s\n", d.ID, d.Source, d.Name, d.Objective)
	}
}

func parseFaults(s string) []fault.Type {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	var out []fault.Type
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, fault.Type(p))
	}
	return out
}
