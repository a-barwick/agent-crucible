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

	"github.com/a-barwick/agent-crucible/internal/fault"
	"github.com/a-barwick/agent-crucible/internal/harness"
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
  crucible serve  [-addr :8080]
  crucible run    [-seed 42] [-trials 40] [-p 0.3] [-faults timeout,malformed,...] [-json]
  crucible replay [-seed 42] [-trial 0] [-p 0.3] [-faults ...] [-json]

The runner is deterministic. Same seed, trial, p, and fault set replay bit-for-bit.
`)
}

func serveCmd(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := fs.String("addr", ":8080", "listen address")
	_ = fs.Parse(args)
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
	asJSON := fs.Bool("json", false, "print JSON")
	_ = fs.Parse(args)
	cfg := harness.Config{Seed: *seed, Trials: *trials, P: *p, Faults: parseFaults(*faults)}
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
	asJSON := fs.Bool("json", false, "print JSON")
	_ = fs.Parse(args)
	return harness.Config{Seed: *seed, Trials: *trials, P: *p, Faults: parseFaults(*faults)}, *asJSON
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
