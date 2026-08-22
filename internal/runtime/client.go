package runtime

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/a-barwick/agent-crucible/internal/agent"
	"github.com/a-barwick/agent-crucible/internal/trace"
)

type RemoteOpts struct {
	Kind string // langgraph | adk
	URL  string
	Spec *agent.Spec
}

// Remote is an agent.Agent that runs in the Python sidecar.
type Remote struct {
	Kind   string
	URL    string
	SpecIn *agent.Spec
	cli    *http.Client
}

func NewRemote(ctx context.Context, opts RemoteOpts) (*Remote, error) {
	url := opts.URL
	if url == "" && opts.Spec != nil {
		url = opts.Spec.Endpoint
	}
	if url == "" {
		p, err := EnsureLocal(ctx)
		if err != nil {
			return nil, err
		}
		url = p.URL
	}
	kind := opts.Kind
	if kind == "" && opts.Spec != nil {
		kind = opts.Spec.Runtime
	}
	if kind == "" {
		kind = "langgraph"
	}
	return &Remote{
		Kind:   kind,
		URL:    url,
		SpecIn: opts.Spec,
		cli:    &http.Client{Timeout: 20 * time.Second},
	}, nil
}

func (r *Remote) Spec() agent.Spec {
	if r.SpecIn != nil {
		return *r.SpecIn
	}
	s := agent.NewCRM(nil).Spec()
	switch r.Kind {
	case "adk":
		s.Name = agent.IDCloserADK
		s.Framework = "adk"
		s.Runtime = "adk"
		s.Description = "ADK Agent + Runner + SessionService. Same fragile closer, real ADK loop."
	default:
		s.Name = agent.IDCloserLangGraph
		s.Framework = "langgraph"
		s.Runtime = "langgraph"
		s.Description = "Real LangGraph StateGraph + InMemorySaver + chat model."
	}
	return s
}

func (r *Remote) Run(ctx context.Context, st agent.State, bus agent.Bus, rec *trace.Recorder, hook agent.Hook) (agent.Result, error) {
	cb, err := NewCallback()
	if err != nil {
		return agent.Result{}, err
	}
	defer cb.Close()
	tok := newToken()
	cb.Register(tok, &Session{Ctx: ctx, Bus: bus, Hook: hook, Rec: rec, St: &st})
	defer cb.Unregister(tok)

	req := RunRequest{
		Runtime:   r.Kind,
		ThreadID:  st.ThreadID,
		Objective: st.Objective,
		Memory:    st.Memory,
		Junk:      st.Junk,
		Companies: st.Companies,
		Partial:   st.Partial,
		Spec:      r.SpecIn,
		Callback:  cb.URL(),
		Token:     tok,
	}
	if r.SpecIn != nil {
		req.Entry = r.SpecIn.Entry
		req.Export = r.SpecIn.Export
		if req.Runtime == "" && r.SpecIn.Runtime != "" {
			req.Runtime = r.SpecIn.Runtime
		}
	}
	raw, err := json.Marshal(req)
	if err != nil {
		return agent.Result{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, r.URL+"/v1/run", bytes.NewReader(raw))
	if err != nil {
		return agent.Result{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	res, err := r.cli.Do(httpReq)
	if err != nil {
		return agent.Result{}, err
	}
	defer res.Body.Close()
	slurp, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 300 {
		return agent.Result{}, fmt.Errorf("runtime %d: %s", res.StatusCode, slurp)
	}
	var out RunResponse
	if err := json.Unmarshal(slurp, &out); err != nil {
		return agent.Result{}, err
	}
	if out.Error != "" && out.Claimed.Error == "" {
		out.Claimed.Error = out.Error
	}
	if out.Checkpoint {
		rec.State("checkpointer saved thread "+st.ThreadID, map[string]any{"runtime": out.Runtime})
	}
	return agent.Result{
		Terminal: out.Terminal,
		Intent:   out.Intent,
		Claimed:  out.Claimed,
		Steps:    out.Steps,
	}, nil
}

func newToken() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
