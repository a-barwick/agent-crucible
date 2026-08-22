package server

import (
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"time"

	"github.com/a-barwick/agent-crucible/internal/agent"
	"github.com/a-barwick/agent-crucible/internal/fault"
	"github.com/a-barwick/agent-crucible/internal/harness"
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

	static, err := fs.Sub(web.FS, ".")
	if err != nil {
		panic(err)
	}
	mux.Handle("/", http.FileServer(http.FS(static)))
	return mux
}

func handleMeta(w http.ResponseWriter, _ *http.Request) {
	crm := agent.NewCRM(nil)
	writeJSON(w, http.StatusOK, map[string]any{
		"agent":  crm.Spec(),
		"faults": fault.Catalog(),
		"defaults": map[string]any{
			"seed": 42, "trials": 40, "p": 0, "p_max": 0.30, "step": 0.01,
			"faults":    fault.MVP,
			"objective": agent.DefaultObjective,
		},
	})
}

type runReq struct {
	Seed   int64        `json:"seed"`
	Trials int          `json:"trials"`
	P      float64      `json:"p"`
	Faults []fault.Type `json:"faults"`
	MaxP   float64      `json:"max_p"`
	Step   float64      `json:"step"`
	Trial  int          `json:"trial"`
}

func cfgOf(r runReq) harness.Config {
	return harness.Config{Seed: r.Seed, Trials: r.Trials, P: r.P, Faults: r.Faults}
}

func handleRun(w http.ResponseWriter, r *http.Request) {
	req, ok := readReq(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
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
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	writeJSON(w, http.StatusOK, harness.RunSweep(ctx, cfgOf(req), maxP, step))
}

func handleReplay(w http.ResponseWriter, r *http.Request) {
	req, ok := readReq(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	writeJSON(w, http.StatusOK, harness.Replay(ctx, cfgOf(req), req.Trial))
}

func readReq(w http.ResponseWriter, r *http.Request) (runReq, bool) {
	var req runReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err.Error() != "EOF" {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return req, false
	}
	return req, true
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}
