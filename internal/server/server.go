package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"time"

	"github.com/a-barwick/agent-crucible/internal/agent"
	"github.com/a-barwick/agent-crucible/internal/ai"
	"github.com/a-barwick/agent-crucible/internal/fault"
	"github.com/a-barwick/agent-crucible/internal/harness"
	"github.com/a-barwick/agent-crucible/internal/runtime"
	"github.com/a-barwick/agent-crucible/internal/scenario"
	"github.com/a-barwick/agent-crucible/internal/schema"
	"github.com/a-barwick/agent-crucible/web"
)

func New() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})
	mux.HandleFunc("GET /api/meta", handleMeta)
	mux.HandleFunc("POST /api/run", handleRun)
	mux.HandleFunc("POST /api/sweep", handleSweep)
	mux.HandleFunc("POST /api/replay", handleReplay)
	mux.HandleFunc("POST /api/generate", handleGenerate)

	static, err := fs.Sub(web.FS, ".")
	if err != nil {
		panic(err)
	}
	mux.Handle("/", http.FileServer(http.FS(static)))
	return mux
}

func handleMeta(w http.ResponseWriter, _ *http.Request) {
	rt := runtime.CurrentStatus()
	crm := agent.NewCRM(nil)
	writeJSON(w, http.StatusOK, map[string]any{
		"agent":     crm.Spec(),
		"agents":    agent.Catalog(rt.Ready, runtime.HaveNode()),
		"scenarios": scenario.Summaries(),
		"faults":    fault.Catalog(),
		"runtime":   rt,
		"ai":        ai.StatusFromEnv(),
		"presets": map[string]any{
			agent.IDTicketLangGraph: map[string]any{
				"spec": agent.TicketLangGraphSpec(), "scenario": scenario.Ticket(),
			},
			agent.IDTicketADK: map[string]any{
				"spec": agent.TicketADKSpec(), "scenario": scenario.Ticket(),
			},
			agent.IDNativeLangGraph: map[string]any{
				"spec": agent.NativeLangGraphSpec(), "scenario": scenario.Ticket(),
			},
			agent.IDNativeADK: map[string]any{
				"spec": agent.NativeADKSpec(), "scenario": scenario.Ticket(),
			},
			agent.IDNativeOpenAI: map[string]any{
				"spec": agent.NativeOpenAISpec(), "scenario": scenario.Ticket(),
			},
			agent.IDNativeJS: map[string]any{
				"spec": agent.NativeJSSpec(), "scenario": scenario.Ticket(),
			},
			agent.IDNativeReact: map[string]any{
				"spec": agent.NativeReactSpec(), "scenario": scenario.Ticket(),
			},
			agent.IDHTTPClosure: map[string]any{
				"spec": agent.HTTPClosureSpec(), "scenario": scenario.Ticket(),
			},
			agent.IDForeignHTTP: map[string]any{
				"spec": agent.ForeignHTTPSpec(), "scenario": scenario.Ticket(),
			},
		},
		"defaults": map[string]any{
			"seed": 42, "trials": 40, "p": 0, "p_max": 0.30, "step": 0.01,
			"faults":    fault.MVP,
			"objective": agent.DefaultObjective,
			"agent":     agent.IDCloser,
			"scenario":  scenario.CloseAcmeID,
		},
	})
}

type runReq struct {
	Seed       int64               `json:"seed"`
	Trials     int                 `json:"trials"`
	P          float64             `json:"p"`
	Faults     []fault.Type        `json:"faults"`
	MaxP       float64             `json:"max_p"`
	Step       float64             `json:"step"`
	Trial      int                 `json:"trial"`
	Agent      string              `json:"agent"`
	Scenario   string              `json:"scenario"`
	Spec       *agent.Spec         `json:"spec"`
	Bundle     *scenario.Bundle    `json:"bundle"`
	RuntimeURL string              `json:"runtime_url"`
	Extra      []scenario.Scenario `json:"extra_scenarios,omitempty"`
}

func cfgOf(r runReq) harness.Config {
	return harness.Config{
		Seed: r.Seed, Trials: r.Trials, P: r.P, Faults: r.Faults,
		Agent: r.Agent, Scenario: r.Scenario, Spec: r.Spec,
		Bundle: r.Bundle, RuntimeURL: r.RuntimeURL, Extra: r.Extra,
	}
}

func handleRun(w http.ResponseWriter, r *http.Request) {
	req, ok := readReq(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	writeJSON(w, http.StatusOK, harness.Run(ctx, cfgOf(req)))
}

func handleSweep(w http.ResponseWriter, r *http.Request) {
	req, ok := readReq(w, r)
	if !ok {
		return
	}
	maxP := req.MaxP
	if maxP == 0 {
		maxP = 0.30
	}
	step := req.Step
	if step == 0 {
		step = 0.01
	}
	if (agent.NeedsPython(req.Agent, req.Spec) || agent.NeedsNode(req.Agent, req.Spec)) && step < 0.05 {
		step = 0.05
	}
	timeout := 30 * time.Second
	if agent.NeedsPython(req.Agent, req.Spec) || agent.NeedsNode(req.Agent, req.Spec) {
		timeout = 120 * time.Second
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()
	writeJSON(w, http.StatusOK, harness.RunSweep(ctx, cfgOf(req), maxP, step))
}

func handleReplay(w http.ResponseWriter, r *http.Request) {
	req, ok := readReq(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	writeJSON(w, http.StatusOK, harness.Replay(ctx, cfgOf(req), req.Trial))
}

func handleGenerate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Seed  int64         `json:"seed"`
		N     int           `json:"n"`
		Tools []schema.Tool `json:"tools"`
	}
	if err := decodeBody(r, &req); err != nil {
		http.Error(w, "invalid json: "+err.Error(), http.StatusBadRequest)
		return
	}
	if len(req.Tools) == 0 {
		req.Tools = agent.CRMTools()
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	writeJSON(w, http.StatusOK, ai.Generate(ctx, req.Seed, req.Tools, req.N, ai.ClientFromEnv(ai.Config{})))
}

// maxBody caps a request. A pasted spec with fixtures is a few kilobytes; the
// endpoints are otherwise happy to buffer whatever a client sends.
const maxBody = 4 << 20

// readReq decodes a run request. An empty body means "use the defaults", which
// is why io.EOF is not an error — but it has to be recognised by comparing
// against io.EOF, not by matching the string "EOF", which also swallowed any
// future error whose message happened to render that way.
func readReq(w http.ResponseWriter, r *http.Request) (runReq, bool) {
	var req runReq
	if err := decodeBody(r, &req); err != nil {
		http.Error(w, "invalid json: "+err.Error(), http.StatusBadRequest)
		return req, false
	}
	return req, true
}

func decodeBody(r *http.Request, v any) error {
	err := json.NewDecoder(io.LimitReader(r.Body, maxBody)).Decode(v)
	if errors.Is(err, io.EOF) {
		return nil
	}
	return err
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}
