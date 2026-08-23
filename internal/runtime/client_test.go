package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/a-barwick/agent-crucible/internal/agent"
	"github.com/a-barwick/agent-crucible/internal/clock"
	"github.com/a-barwick/agent-crucible/internal/fault"
	"github.com/a-barwick/agent-crucible/internal/rng"
	"github.com/a-barwick/agent-crucible/internal/schema"
	"github.com/a-barwick/agent-crucible/internal/trace"
	"github.com/a-barwick/agent-crucible/internal/world"
)

func TestRemoteAgainstMockSidecar(t *testing.T) {
	var sawCallback bool
	sidec := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/run" {
			http.NotFound(w, r)
			return
		}
		var req RunRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		body, _ := json.Marshal(BeforeReq{Name: "plan"})
		cbReq, _ := http.NewRequest(http.MethodPost, req.Callback+"/before_node", bytes.NewReader(body))
		cbReq.Header.Set("Authorization", "Bearer "+req.Token)
		cbReq.Header.Set("Content-Type", "application/json")
		if res, err := http.DefaultClient.Do(cbReq); err == nil {
			res.Body.Close()
			sawCallback = true
		}
		_ = json.NewEncoder(w).Encode(RunResponse{
			Terminal:   "end",
			Runtime:    "mock",
			Checkpoint: true,
			Steps:      1,
			Intent:     agent.Intent{Company: "Acme Corp", DealAction: "close_won", Notify: true},
			Claimed:    agent.Claim{Wrote: true, Notified: true, DealID: "deal-acme-1", Status: "Closed-Won"},
		})
	}))
	defer sidec.Close()

	clk := clock.New()
	w := world.SeedCloseAcme()
	tr := trace.New()
	rec := tr.Recorder(clk.Now)
	inj := fault.New(rng.Stream(1, 0), 0, fault.MVP)
	bus := &agent.FaultBus{World: w, Inj: inj, Rec: rec, Clock: clk}
	ag, err := NewRemote(context.Background(), RemoteOpts{Kind: "langgraph", URL: sidec.URL})
	if err != nil {
		t.Fatal(err)
	}
	res, err := ag.Run(context.Background(), agent.State{Objective: agent.DefaultObjective, ThreadID: "mock"}, bus, rec, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Claimed.Wrote || !sawCallback {
		t.Fatalf("res=%+v callback=%v", res, sawCallback)
	}
}

// TestEndpointPathIsNotDoubled: an endpoint is documented as a server speaking
// POST /v1/run, so pasting that URL in full is the obvious thing to do. The
// client used to append the path regardless and ask for /v1/run/v1/run.
func TestEndpointPathIsNotDoubled(t *testing.T) {
	var paths []string
	sidec := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.URL.Path != "/v1/run" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(RunResponse{Terminal: "end", Runtime: "mock", Steps: 1})
	}))
	defer sidec.Close()

	for _, given := range []string{sidec.URL, sidec.URL + "/", sidec.URL + "/v1/run", sidec.URL + "/v1/run/"} {
		paths = nil
		ag, err := NewRemote(context.Background(), RemoteOpts{Kind: "langgraph", URL: given})
		if err != nil {
			t.Fatalf("%s: %v", given, err)
		}
		clk := clock.New()
		tr := trace.New()
		rec := tr.Recorder(clk.Now)
		bus := &agent.FaultBus{
			World: world.SeedCloseAcme(),
			Inj:   fault.New(rng.Stream(1, 0), 0, fault.MVP),
			Rec:   rec, Clock: clk,
		}
		st := agent.State{Objective: agent.DefaultObjective, ThreadID: "mock"}
		if _, err := ag.Run(context.Background(), st, bus, rec, nil); err != nil {
			t.Fatalf("%s: %v", given, err)
		}
		if len(paths) != 1 || paths[0] != "/v1/run" {
			t.Fatalf("endpoint %q asked for %v, want [/v1/run]", given, paths)
		}
	}
}

func TestToolCallbackHitsWorld(t *testing.T) {
	cb, err := NewCallback()
	if err != nil {
		t.Fatal(err)
	}
	defer cb.Close()
	clk := clock.New()
	w := world.SeedCloseAcme()
	tr := trace.New()
	rec := tr.Recorder(clk.Now)
	inj := fault.New(rng.Stream(1, 0), 0, fault.MVP)
	bus := &agent.FaultBus{World: w, Inj: inj, Rec: rec, Clock: clk}
	st := agent.State{Objective: agent.DefaultObjective}
	cb.Register("tok", &Session{Ctx: context.Background(), Bus: bus, Rec: rec, St: &st})
	raw, _ := json.Marshal(ToolReq{Tool: "lookup_contact", Args: map[string]any{"company": "Acme Corp"}})
	req, _ := http.NewRequest(http.MethodPost, cb.URL()+"/tool", bytes.NewReader(raw))
	req.Header.Set("Authorization", "Bearer tok")
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var got schema.Result
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if !got.OK || got.Data["id"] != "ct-acme" {
		t.Fatalf("%+v", got)
	}
}
