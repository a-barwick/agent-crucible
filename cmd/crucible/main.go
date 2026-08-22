package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net"
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
  crucible serve     [-addr 127.0.0.1:8080]
  crucible run       [-seed 42] [-trials 40] [-p 0.3] [-agent aether-closer] [-scenario close-acme]
                     [-entry examples/native_ticket.py] [-endpoint http://127.0.0.1:8092]
                     [-spec examples/native_ticket.json] [-faults all] [-json]
  crucible replay    [-seed 42] [-trial 0] [-p 0.3] [-agent ...] [-scenario ...] [-json]
  crucible agents
  crucible scenarios
  crucible generate  [-n 5] [-json]

Give it an agent file (entry), a foreign command (spec.command), an HTTP process (endpoint), or a pasted spec.
The runner is deterministic. Same seed, trial, p, and fault set replay bit-for-bit.
AI generates scenarios, scores ambiguous traces, and explains patterns. It does not pick faults.
`)
}

func serveCmd(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	// Loopback by default. POST /api/run runs local agent files and can reach
	// loopback services, so this is not an API to expose by accident.
	addr := fs.String("addr", "127.0.0.1:8080", "listen address")
	_ = fs.Parse(args)
	defer runtime.StopAll()
	if rt, err := runtime.EnsureLocal(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "python runtime: %v (langgraph/adk agents unavailable)\n", err)
	} else {
		fmt.Fprintf(os.Stderr, "python runtime %s\n", rt.URL)
	}
	if !loopbackAddr(*addr) {
		fmt.Fprintf(os.Stderr,
			"warning: %s is not loopback. The run API executes agent files from this checkout and has no authentication.\n",
			*addr)
	}
	h := server.New()
	s := &http.Server{Addr: *addr, Handler: h, ReadHeaderTimeout: 5 * time.Second}
	fmt.Fprintf(os.Stderr, "crucible listening on %s\n", *addr)
	if err := s.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func loopbackAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func runCmd(args []string) {
	// The sidecars live in package globals so a suite can reuse them; nothing
	// reaps them when the command returns.
	defer runtime.StopAll()
	cfg, asJSON := flags(args, false)
	timeout := 30 * time.Second
	if agent.NeedsPython(cfg.Agent, cfg.Spec) || agent.NeedsNode(cfg.Agent, cfg.Spec) {
		timeout = 120 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	suite := harness.Run(ctx, cfg)
	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(suite)
		return
	}
	fmt.Printf("suite %s  seed=%d  trials=%d  p=%.0f%%\n", suite.ID, cfg.Seed, len(suite.Trials), cfg.P*100)
	// Say what the percentages are over. Printing survival next to the
	// requested trial count reads as a score over trials that never ran.
	fmt.Printf("survival %.0f%%   safety %.0f%%   clean %.0f%%   (over %d scored)\n",
		suite.Survival*100, suite.Safety*100, suite.CleanRate*100, suite.Scored)
	fmt.Printf("counts %v\n", suite.Counts)
	if suite.Errored > 0 {
		fmt.Printf("\n%d of %d trials could not run: %s\n", suite.Errored, len(suite.Trials), suite.Error)
	}
	fmt.Println()
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
	defer runtime.StopAll()
	fs := flag.NewFlagSet("replay", flag.ExitOnError)
	seed := fs.Int64("seed", 42, "suite seed")
	trials := fs.Int("trials", 40, "suite size (for stream mixing)")
	n := fs.Int("trial", 0, "trial index")
	p := fs.Float64("p", 0.30, "failure probability")
	faults := fs.String("faults", "", "comma-separated fault types")
	ag := fs.String("agent", agent.IDCloser, "agent id")
	scn := fs.String("scenario", "", "scenario id")
	specFile := fs.String("spec", "", "JSON file with spec and optional scenario")
	entry := fs.String("entry", "", "Python agent file to drop in")
	endpoint := fs.String("endpoint", "", "HTTP process speaking POST /v1/run")
	asJSON := fs.Bool("json", false, "print JSON")
	_ = fs.Parse(args)
	cfg := attachDropIn(harness.Config{Seed: *seed, Trials: *trials, P: *p, Faults: parseFaults(*faults), Agent: *ag, Scenario: *scn}, *specFile, *entry, *endpoint)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	tr := harness.Replay(ctx, cfg, *n)
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(tr)
		return
	}
	if tr.Error != "" {
		fmt.Fprintf(os.Stderr, "trial %d could not run: %s\n", tr.N, tr.Error)
		os.Exit(1)
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
	faults := fs.String("faults", "", "comma-separated fault types, or 'all'")
	ag := fs.String("agent", agent.IDCloser, "agent id")
	scn := fs.String("scenario", "", "scenario id")
	specFile := fs.String("spec", "", "JSON file with spec and optional scenario")
	entry := fs.String("entry", "", "Python agent file to drop in")
	endpoint := fs.String("endpoint", "", "HTTP process speaking POST /v1/run")
	asJSON := fs.Bool("json", false, "print JSON")
	_ = fs.Parse(args)
	cfg := attachDropIn(harness.Config{
		Seed: *seed, Trials: *trials, P: *p, Faults: parseFaults(*faults),
		Agent: *ag, Scenario: *scn,
	}, *specFile, *entry, *endpoint)
	return cfg, *asJSON
}

func attachDropIn(cfg harness.Config, specFile, entry, endpoint string) harness.Config {
	if specFile != "" {
		raw, err := os.ReadFile(specFile)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		var bundle scenario.Bundle
		if err := json.Unmarshal(raw, &bundle); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		cfg.Bundle = &bundle
		if cfg.Agent == agent.IDCloser {
			cfg.Agent = agent.IDPasted
		}
	}
	if entry != "" {
		rt := "langgraph"
		if agent.JSEntry(entry) {
			rt = "js"
		}
		var target *agent.Spec
		switch {
		case cfg.Bundle != nil:
			target = &cfg.Bundle.Spec
		case cfg.Spec != nil:
			target = cfg.Spec
		default:
			cfg.Spec = &agent.Spec{}
			target = cfg.Spec
		}
		target.Entry = entry
		if target.Runtime == "" {
			target.Runtime = rt
		}
		if cfg.Agent == agent.IDCloser {
			cfg.Agent = agent.IDPasted
		}
	}
	if endpoint != "" {
		if cfg.Bundle != nil {
			cfg.Bundle.Spec.Endpoint = endpoint
		} else if cfg.Spec != nil {
			cfg.Spec.Endpoint = endpoint
		} else {
			cfg.Spec = &agent.Spec{Endpoint: endpoint}
		}
		cfg.RuntimeURL = endpoint
		if cfg.Agent == agent.IDCloser {
			cfg.Agent = agent.IDRemote
		}
	}
	return cfg
}

func agentsCmd() {
	st := runtime.CurrentStatus()
	for _, a := range agent.Catalog(st.Ready || runtime.HaveLangGraph(), runtime.HaveNode()) {
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
	out := ai.Generate(ctx, *seed, agent.CRMTools(), *n, ai.ClientFromEnv(ai.Config{}))
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

// parseFaults rejects names that are not in the catalog. Accepting them
// silently meant `-faults timout` ran with no faults at all and reported a
// perfect survival rate.
func parseFaults(s string) []fault.Type {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	if s == "all" {
		return append([]fault.Type(nil), fault.All...)
	}
	if s == "mvp" {
		return append([]fault.Type(nil), fault.MVP...)
	}
	known := map[fault.Type]bool{}
	for _, t := range fault.All {
		known[t] = true
	}
	var out []fault.Type
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		ft := fault.Type(p)
		if !known[ft] {
			fmt.Fprintf(os.Stderr, "unknown fault %q; known faults are %s (or 'all', 'mvp')\n", p, faultNames())
			os.Exit(2)
		}
		out = append(out, ft)
	}
	return out
}

func faultNames() string {
	names := make([]string, len(fault.All))
	for i, t := range fault.All {
		names[i] = string(t)
	}
	return strings.Join(names, ", ")
}
