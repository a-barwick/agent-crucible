package runtime

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/a-barwick/agent-crucible/internal/agent"
	"github.com/a-barwick/agent-crucible/internal/trace"
)

func jsOpts(opts RemoteOpts) bool {
	if opts.Kind == "js" || opts.Kind == "javascript" || opts.Kind == "node" {
		return true
	}
	if opts.Spec != nil && (agent.JSRuntime(opts.Spec.Runtime) || agent.JSEntry(opts.Spec.Entry)) {
		return true
	}
	return false
}

type RemoteOpts struct {
	Kind string // langgraph | adk
	URL  string
	Spec *agent.Spec
}

// AllowRemoteEndpointEnv lifts the loopback restriction on agent endpoints.
const AllowRemoteEndpointEnv = "CRUCIBLE_ALLOW_REMOTE_ENDPOINT"

// maxRunBody caps what a sidecar can return for one trial. A result is a small
// object; a process that streams gigabytes at the runner is broken.
const maxRunBody = 8 << 20

// checkEndpoint refuses to POST a trial anywhere but the local machine.
// spec.endpoint comes straight from the request body, so without this the run
// API is an open proxy: point it at an internal service and the response comes
// back inside the trial. Set CRUCIBLE_ALLOW_REMOTE_ENDPOINT for a real remote
// sidecar on a network you trust.
func checkEndpoint(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("bad agent endpoint %q: %w", raw, err)
	}
	switch u.Scheme {
	case "http", "https":
	default:
		return fmt.Errorf("agent endpoint must be http or https, got %q", u.Scheme)
	}
	if allowRemoteEndpoint() {
		return nil
	}
	host := u.Hostname()
	if host == "localhost" {
		return nil
	}
	ip := net.ParseIP(host)
	if ip != nil && ip.IsLoopback() {
		return nil
	}
	return fmt.Errorf("agent endpoint %q is not loopback; set %s to allow it", raw, AllowRemoteEndpointEnv)
}

func allowRemoteEndpoint() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(AllowRemoteEndpointEnv))) {
	case "1", "true", "yes":
		return true
	}
	return false
}

// ChamberError is a failure to run a trial rather than a verdict about the
// agent: a sidecar that could not import the entry file, a callback that could
// not be reached, a request the sidecar rejected. The harness reports these
// separately so a broken interpreter does not read as a fragile agent.
type ChamberError struct{ Msg string }

func (e *ChamberError) Error() string { return e.Msg }

// Remote is an agent.Agent that runs in the Python sidecar.
type Remote struct {
	Kind   string
	URL    string
	SpecIn *agent.Spec
	cli    *http.Client
}

func NewRemote(ctx context.Context, opts RemoteOpts) (*Remote, error) {
	target := opts.URL
	if target == "" && opts.Spec != nil {
		target = opts.Spec.Endpoint
	}
	js := jsOpts(opts)
	if target == "" {
		if js {
			p, err := EnsureNode(ctx)
			if err != nil {
				return nil, err
			}
			target = p.URL
		} else {
			p, err := EnsureLocal(ctx)
			if err != nil {
				return nil, err
			}
			target = p.URL
		}
	} else if err := checkEndpoint(target); err != nil {
		return nil, err
	}
	kind := opts.Kind
	if kind == "" && opts.Spec != nil {
		kind = opts.Spec.Runtime
	}
	if js {
		kind = "js"
	}
	if kind == "" {
		kind = "langgraph"
	}
	return &Remote{
		Kind:   kind,
		URL:    target,
		SpecIn: opts.Spec,
		// No client timeout: the caller's context already carries the trial
		// deadline, and a shorter fixed timeout here would cut off runs the
		// CLI explicitly allowed 120s for.
		cli: &http.Client{},
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
	cb, err := Shared()
	if err != nil {
		return agent.Result{}, err
	}
	tok, err := newToken()
	if err != nil {
		return agent.Result{}, err
	}
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
	slurp, _ := io.ReadAll(io.LimitReader(res.Body, maxRunBody))
	if res.StatusCode >= 300 {
		// The agent never returned a verdict: either the sidecar could not
		// start it, or it rejected our request. Both are ours to fix.
		return agent.Result{}, &ChamberError{Msg: fmt.Sprintf("sidecar %d: %s", res.StatusCode, strings.TrimSpace(string(slurp)))}
	}
	var out RunResponse
	if err := json.Unmarshal(slurp, &out); err != nil {
		return agent.Result{}, &ChamberError{Msg: fmt.Sprintf("sidecar sent unreadable JSON: %v", err)}
	}
	if out.ChamberError {
		msg := out.Error
		if msg == "" {
			msg = "sidecar reported a chamber error with no detail"
		}
		return agent.Result{}, &ChamberError{Msg: msg}
	}
	if out.Error != "" && out.Claimed.Error == "" {
		out.Claimed.Error = out.Error
	}
	if out.AgentError != "" {
		rec.State("agent raised inside the sidecar", map[string]any{"traceback": out.AgentError})
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

// newToken is the sidecar's only credential for calling back into a trial, so
// a short read must be an error rather than a predictable all-zero token.
func newToken() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("callback token: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}
