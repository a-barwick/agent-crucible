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

func runtimeHealthy(url string) bool {
	cli := &http.Client{Timeout: 200 * time.Millisecond}
	res, err := cli.Get(url + "/health")
	if err != nil {
		return false
	}
	defer res.Body.Close()
	return res.StatusCode == 200
}
