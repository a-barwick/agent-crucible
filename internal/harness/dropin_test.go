package harness

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/a-barwick/agent-crucible/internal/agent"
	"github.com/a-barwick/agent-crucible/internal/fault"
	"github.com/a-barwick/agent-crucible/internal/runtime"
	"github.com/a-barwick/agent-crucible/internal/scenario"
)

func TestTicketLangGraphClean(t *testing.T) {
	if !runtime.HaveLangGraph() {
		t.Skip("langgraph not installed")
	}
	s := Run(context.Background(), Config{
		Seed: 3, Trials: 2, P: 0, Agent: agent.IDTicketLangGraph, Faults: fault.MVP,
	})
	if s.Survival != 1 {
		t.Fatalf("ticket langgraph survival %v counts=%v", s.Survival, s.Counts)
		for _, tr := range s.Trials {
			t.Logf("trial %d %s %s %v", tr.N, tr.Outcome, tr.Reason, tr.Violations)
		}
	}
}

func TestTicketADKClean(t *testing.T) {
	if !runtime.HaveLangGraph() {
		t.Skip("langgraph not installed")
	}
	s := Run(context.Background(), Config{
		Seed: 3, Trials: 2, P: 0, Agent: agent.IDTicketADK, Faults: fault.MVP,
	})
	if s.Survival != 1 {
		t.Fatalf("ticket adk survival %v counts=%v", s.Survival, s.Counts)
	}
}

func TestTicketEntryFileNotChamberWalk(t *testing.T) {
	if !runtime.HaveLangGraph() {
		t.Skip("langgraph not installed")
	}
	spec := agent.TicketLangGraphSpec()
	s := Run(context.Background(), Config{
		Seed: 3, Trials: 2, P: 0, Agent: agent.IDPasted, Spec: &spec, Faults: fault.MVP,
		Bundle: &scenario.Bundle{Spec: spec, Scenario: scenario.Ticket()},
	})
	if s.Survival != 1 {
		t.Fatalf("entry file survival %v counts=%v", s.Survival, s.Counts)
	}
	found := false
	for _, tr := range s.Trials {
		for _, ev := range tr.Events {
			if ev.Node == "search_ticket" || ev.Node == "update_ticket" || ev.Tool == "search_ticket" || ev.Tool == "update_ticket" {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("entry graph nodes never appeared — chamber probably compiled a walk. events=%v", s.Trials[0].Events)
	}
}

func TestTicketMalformedCritiqueNamesWriteTool(t *testing.T) {
	writes, emails := 1, 0
	notify := false
	bundle := ticketBundle(writes, emails, notify)
	s := Run(context.Background(), Config{
		Seed: 1, Trials: 12, P: 1, Agent: "pasted", Bundle: &bundle,
		Faults: []fault.Type{fault.Malformed},
	})
	if s.Survival > 0.25 {
		t.Fatalf("malformed ticket write should collapse, got %v", s.Survival)
	}
	text := s.Critique.Headline + " " + strings.Join(s.Critique.Paragraphs, " ")
	if strings.Contains(text, "CRM tool") {
		t.Fatalf("ticket critique still CRM-shaped: %s", text)
	}
	if !strings.Contains(text, "update_ticket") && !strings.Contains(text, "missing fields") {
		t.Fatalf("critique should name the write tool or missing fields: %s", text)
	}
}

func TestTicketFullCatalogFires(t *testing.T) {
	writes, emails := 1, 0
	notify := false
	bundle := ticketBundle(writes, emails, notify)
	bundle.Scenario.AltObjective = agent.TicketAltObjective
	bundle.Scenario.ContextBallast = "Prior notes (stale): discussed Globex renewal, Globex Q3."
	bundle.Scenario.StaleMemory = agent.Memory{DealID: "tkt-other", DealStatus: "Open", HasWritePerm: true}
	for _, ft := range fault.All {
		s := Run(context.Background(), Config{
			Seed: 4, Trials: 8, P: 1, Agent: "pasted", Bundle: &bundle,
			Faults: []fault.Type{ft},
		})
		seen := false
		for _, tr := range s.Trials {
			for _, f := range tr.Faults {
				if f == ft {
					seen = true
				}
			}
		}
		if !seen {
			t.Fatalf("fault %s never fired on ticket agent counts=%v", ft, s.Counts)
		}
	}
}

func TestHTTPProcessAgent(t *testing.T) {
	if !runtime.HaveLangGraph() {
		t.Skip("langgraph not installed")
	}
	root := runtime.FindRepoRoot()
	if root == "" {
		t.Skip("repo root not found")
	}
	entry := runtime.FindEntry("examples/http_agent.py")
	if _, err := os.Stat(entry); err != nil {
		t.Skip(err)
	}
	addr := "127.0.0.1:18092"
	cmd := exec.Command("python3", entry, "--addr", addr)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "PYTHONPATH="+filepath.Join(root, "runtime"))
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() }()
	url := "http://" + addr
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if runtimeHealthy(url) {
			break
		}
		time.Sleep(40 * time.Millisecond)
	}
	spec := agent.TicketLangGraphSpec()
	spec.Entry = ""
	spec.Endpoint = url
	s := Run(context.Background(), Config{
		Seed: 3, Trials: 2, P: 0, Agent: agent.IDRemote,
		RuntimeURL: url,
		Bundle:     &scenario.Bundle{Spec: spec, Scenario: scenario.Ticket()},
		Faults:     fault.MVP,
	})
	if s.Survival != 1 {
		t.Fatalf("http process survival %v counts=%v", s.Survival, s.Counts)
		for _, tr := range s.Trials {
			t.Logf("trial %d %s %s %v", tr.N, tr.Outcome, tr.Reason, tr.Violations)
		}
	}
}

func TestNativeSourcesHaveNoCallback(t *testing.T) {
	root := runtime.FindRepoRoot()
	if root == "" {
		t.Skip("repo root not found")
	}
	for _, rel := range []string{
		"examples/native_ticket.py",
		"examples/native_adk.py",
		"examples/native_openai.py",
		"examples/native_ticket.mjs",
		"examples/native_react.py",
		"examples/http_closure.py",
		"examples/foreign_task.py",
	} {
		raw, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		body := string(raw)
		if strings.Contains(body, "cb.retry_tool") || strings.Contains(body, "cb.before") || strings.Contains(body, "from crucible_rt.callback") || strings.Contains(body, "new Callback") {
			t.Fatalf("%s still talks to the chamber callback", rel)
		}
	}
}

func TestNativeSourcesCallHTTP(t *testing.T) {
	root := runtime.FindRepoRoot()
	if root == "" {
		t.Skip("repo root not found")
	}
	for _, rel := range []string{
		"examples/native_ticket.py",
		"examples/native_adk.py",
		"examples/native_openai.py",
		"examples/native_ticket.mjs",
		"examples/native_react.py",
		"examples/http_closure.py",
		"examples/foreign_task.py",
	} {
		raw, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		body := string(raw)
		if !strings.Contains(body, "tickets.example") {
			t.Fatalf("%s does not call tickets.example", rel)
		}
		if strings.Contains(body, "was not intercepted") {
			t.Fatalf("%s still stubs HTTP instead of calling it: %s", rel, body[0:80])
		}
	}
}

func TestHTTPClosureHasNoToolObjects(t *testing.T) {
	root := runtime.FindRepoRoot()
	if root == "" {
		t.Skip("repo root not found")
	}
	raw, err := os.ReadFile(filepath.Join(root, "examples/http_closure.py"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	if strings.Contains(body, "@tool") || strings.Contains(body, "TOOLS =") || strings.Contains(body, "DISPATCH =") {
		t.Fatalf("http_closure.py should not export tool objects")
	}
}

func TestNativeLangGraphClean(t *testing.T) {
	if !runtime.HaveLangGraph() {
		t.Skip("langgraph not installed")
	}
	s := Run(context.Background(), Config{
		Seed: 3, Trials: 2, P: 0, Agent: agent.IDNativeLangGraph, Faults: fault.MVP,
	})
	if s.Survival != 1 {
		t.Fatalf("native langgraph survival %v counts=%v", s.Survival, s.Counts)
		for _, tr := range s.Trials {
			t.Logf("trial %d %s %s %v", tr.N, tr.Outcome, tr.Reason, tr.Violations)
		}
	}
}

func TestNativeADKClean(t *testing.T) {
	if !runtime.HaveLangGraph() {
		t.Skip("langgraph not installed")
	}
	s := Run(context.Background(), Config{
		Seed: 3, Trials: 2, P: 0, Agent: agent.IDNativeADK, Faults: fault.MVP,
	})
	if s.Survival != 1 {
		t.Fatalf("native adk survival %v counts=%v", s.Survival, s.Counts)
	}
}

func TestNativeOpenAIClean(t *testing.T) {
	if !runtime.HaveLangGraph() {
		t.Skip("langgraph not installed")
	}
	s := Run(context.Background(), Config{
		Seed: 3, Trials: 2, P: 0, Agent: agent.IDNativeOpenAI, Faults: fault.MVP,
	})
	if s.Survival != 1 {
		t.Fatalf("native openai survival %v counts=%v", s.Survival, s.Counts)
	}
}

func TestNativeJSClean(t *testing.T) {
	if !runtime.HaveNode() {
		t.Skip("node not installed")
	}
	s := Run(context.Background(), Config{
		Seed: 3, Trials: 2, P: 0, Agent: agent.IDNativeJS, Faults: fault.MVP,
	})
	if s.Survival != 1 {
		t.Fatalf("native js survival %v counts=%v", s.Survival, s.Counts)
		for _, tr := range s.Trials {
			t.Logf("trial %d %s %s %v", tr.N, tr.Outcome, tr.Reason, tr.Violations)
		}
	}
}

func TestNativeEntryWithoutSpec(t *testing.T) {
	if !runtime.HaveLangGraph() {
		t.Skip("langgraph not installed")
	}
	entry := runtime.FindEntry("examples/native_ticket.py")
	spec := agent.Spec{Entry: entry, Runtime: "langgraph", Tools: agent.TicketTools()}
	s := Run(context.Background(), Config{
		Seed: 3, Trials: 2, P: 0, Agent: agent.IDPasted, Spec: &spec, Faults: fault.MVP,
	})
	if s.Survival != 1 {
		t.Fatalf("pasted native entry survival %v counts=%v", s.Survival, s.Counts)
	}
	if s.Scenario != scenario.TicketID && s.Config.Scenario != scenario.TicketID {
		t.Fatalf("drop-in entry should default to ticket, got %q / %q", s.Scenario, s.Config.Scenario)
	}
}

func TestNativeMalformedCollapses(t *testing.T) {
	if !runtime.HaveLangGraph() {
		t.Skip("langgraph not installed")
	}
	s := Run(context.Background(), Config{
		Seed: 1, Trials: 12, P: 1, Agent: agent.IDNativeLangGraph,
		Faults: []fault.Type{fault.Malformed},
	})
	if s.Survival > 0.25 {
		t.Fatalf("native malformed should collapse, got %v counts=%v", s.Survival, s.Counts)
	}
	text := s.Critique.Headline + " " + strings.Join(s.Critique.Paragraphs, " ")
	if strings.Contains(text, "CRM tool") {
		t.Fatalf("native critique still CRM-shaped: %s", text)
	}
}

func TestHTTPClosureClean(t *testing.T) {
	if !runtime.HaveLangGraph() {
		t.Skip("langgraph not installed")
	}
	s := Run(context.Background(), Config{
		Seed: 3, Trials: 2, P: 0, Agent: agent.IDHTTPClosure, Faults: fault.MVP,
	})
	if s.Survival != 1 {
		t.Fatalf("http-closure survival %v counts=%v", s.Survival, s.Counts)
		for _, tr := range s.Trials {
			t.Logf("trial %d %s %s %v", tr.N, tr.Outcome, tr.Reason, tr.Violations)
		}
	}
}

func TestNativeReactClean(t *testing.T) {
	if !runtime.HaveLangGraph() {
		t.Skip("langgraph not installed")
	}
	s := Run(context.Background(), Config{
		Seed: 3, Trials: 2, P: 0, Agent: agent.IDNativeReact, Faults: fault.MVP,
	})
	if s.Survival != 1 {
		t.Fatalf("native-react survival %v counts=%v", s.Survival, s.Counts)
		for _, tr := range s.Trials {
			t.Logf("trial %d %s %s %v", tr.N, tr.Outcome, tr.Reason, tr.Violations)
		}
	}
}

func TestForeignHTTPClean(t *testing.T) {
	if !runtime.HaveLangGraph() {
		t.Skip("python runtime needed")
	}
	s := Run(context.Background(), Config{
		Seed: 3, Trials: 2, P: 0, Agent: agent.IDForeignHTTP, Faults: fault.MVP,
	})
	if s.Survival != 1 {
		t.Fatalf("foreign-http survival %v counts=%v", s.Survival, s.Counts)
		for _, tr := range s.Trials {
			t.Logf("trial %d %s %s %v", tr.N, tr.Outcome, tr.Reason, tr.Violations)
		}
	}
}

func TestForeignWrapCommand(t *testing.T) {
	if !runtime.HaveLangGraph() {
		t.Skip("python runtime needed")
	}
	spec := agent.ForeignHTTPSpec()
	spec.Entry = ""
	spec.Runtime = "wrap"
	s := Run(context.Background(), Config{
		Seed: 3, Trials: 2, P: 0, Agent: agent.IDPasted, Spec: &spec, Faults: fault.MVP,
	})
	if s.Survival != 1 {
		t.Fatalf("foreign wrap command survival %v counts=%v", s.Survival, s.Counts)
		for _, tr := range s.Trials {
			t.Logf("trial %d %s %s %v", tr.N, tr.Outcome, tr.Reason, tr.Violations)
		}
	}
}

func TestHTTPClosureMalformedCollapses(t *testing.T) {
	if !runtime.HaveLangGraph() {
		t.Skip("langgraph not installed")
	}
	s := Run(context.Background(), Config{
		Seed: 1, Trials: 12, P: 1, Agent: agent.IDHTTPClosure,
		Faults: []fault.Type{fault.Malformed},
	})
	if s.Survival > 0.25 {
		t.Fatalf("http-closure malformed should collapse, got %v counts=%v", s.Survival, s.Counts)
	}
}

func TestHTTPClosureFullCatalogFires(t *testing.T) {
	if !runtime.HaveLangGraph() {
		t.Skip("langgraph not installed")
	}
	for _, ft := range fault.All {
		s := Run(context.Background(), Config{
			Seed: 4, Trials: 8, P: 1, Agent: agent.IDHTTPClosure,
			Faults: []fault.Type{ft},
		})
		seen := false
		for _, tr := range s.Trials {
			for _, f := range tr.Faults {
				if f == ft {
					seen = true
				}
			}
		}
		if !seen {
			t.Fatalf("fault %s never fired on http-closure counts=%v", ft, s.Counts)
		}
	}
}

func TestNativeFullCatalogFires(t *testing.T) {
	if !runtime.HaveLangGraph() {
		t.Skip("langgraph not installed")
	}
	for _, ft := range fault.All {
		s := Run(context.Background(), Config{
			Seed: 4, Trials: 8, P: 1, Agent: agent.IDNativeLangGraph,
			Faults: []fault.Type{ft},
		})
		seen := false
		for _, tr := range s.Trials {
			for _, f := range tr.Faults {
				if f == ft {
					seen = true
				}
			}
		}
		if !seen {
			t.Fatalf("fault %s never fired on native agent counts=%v", ft, s.Counts)
		}
	}
}

func runtimeHealthy(url string) bool {
	cli := &http.Client{Timeout: 200 * time.Millisecond}
	res, err := cli.Get(url + "/health")
	if err != nil {
		return false
	}
	defer res.Body.Close()
	return res.StatusCode == 200
}
