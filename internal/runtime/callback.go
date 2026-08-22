package runtime

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/a-barwick/agent-crucible/internal/agent"
	"github.com/a-barwick/agent-crucible/internal/schema"
	"github.com/a-barwick/agent-crucible/internal/trace"
)

// maxCallbackBody caps a single callback payload. Tool args are small; a
// sidecar that streams megabytes at us is broken, not interesting.
const maxCallbackBody = 1 << 20

// Session is one trial the sidecar can call back into.
//
// The world, the trace recorder and the injector are all single-threaded by
// design — they record an ordered timeline. Sidecars are not: the Node server
// handles requests concurrently and a Python agent may use threads. mu
// serialises the callbacks for one trial so the timeline stays an ordering
// rather than a race.
type Session struct {
	Ctx  context.Context
	Bus  agent.Bus
	Hook agent.Hook
	Rec  *trace.Recorder
	St   *agent.State

	mu sync.Mutex
}

// Callback is a localhost HTTP server the Python graph uses for tools and hooks.
// One server is shared by every trial; sessions are keyed by bearer token.
type Callback struct {
	srv  *http.Server
	url  string
	mu   sync.Mutex
	sess map[string]*Session
}

var (
	sharedCbMu sync.Mutex
	sharedCb   *Callback
)

// Shared returns the process-wide callback server, starting it on first use.
// A suite runs hundreds of trials; standing up and tearing down a listener for
// each one burns file descriptors and ports for no benefit, since a token
// already scopes a sidecar to its own trial.
func Shared() (*Callback, error) {
	sharedCbMu.Lock()
	defer sharedCbMu.Unlock()
	if sharedCb != nil {
		return sharedCb, nil
	}
	c, err := NewCallback()
	if err != nil {
		return nil, err
	}
	sharedCb = c
	return c, nil
}

func NewCallback() (*Callback, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	c := &Callback{sess: map[string]*Session{}, url: "http://" + ln.Addr().String()}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /tool", c.handleTool)
	mux.HandleFunc("POST /before_node", c.handleBefore)
	mux.HandleFunc("POST /state", c.handleState)
	c.srv = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = c.srv.Serve(ln) }()
	return c, nil
}

func (c *Callback) URL() string { return c.url }

func (c *Callback) Register(token string, s *Session) {
	c.mu.Lock()
	c.sess[token] = s
	c.mu.Unlock()
}

func (c *Callback) Unregister(token string) {
	c.mu.Lock()
	delete(c.sess, token)
	c.mu.Unlock()
}

func (c *Callback) Close() {
	if c.srv != nil {
		_ = c.srv.Close()
	}
}

func (c *Callback) session(r *http.Request) *Session {
	tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	c.mu.Lock()
	s := c.sess[tok]
	c.mu.Unlock()
	return s
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(io.LimitReader(r.Body, maxCallbackBody)).Decode(v); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return false
	}
	return true
}

func (c *Callback) handleTool(w http.ResponseWriter, r *http.Request) {
	s := c.session(r)
	if s == nil {
		http.Error(w, "unknown session", http.StatusUnauthorized)
		return
	}
	var req ToolReq
	if !decode(w, r, &req) {
		return
	}
	s.mu.Lock()
	res, err := s.Bus.Call(s.Ctx, req.Tool, req.Args)
	s.mu.Unlock()
	if err != nil {
		res = schema.Result{OK: false, Error: err.Error()}
	}
	writeJSON(w, res)
}

func (c *Callback) handleBefore(w http.ResponseWriter, r *http.Request) {
	s := c.session(r)
	if s == nil {
		http.Error(w, "unknown session", http.StatusUnauthorized)
		return
	}
	var req BeforeReq
	if !decode(w, r, &req) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Rec != nil && req.Name != "" {
		s.Rec.NodeEnter(req.Name)
	}
	if s.Hook != nil {
		s.Hook.BeforeNode(s.Ctx, req.Name, s.St, s.Rec)
	}
	writeJSON(w, BeforeResp{
		Objective: s.St.Objective,
		Partial:   s.St.Partial,
		Memory:    s.St.Memory,
		Junk:      s.St.Junk,
		Intent:    s.St.Intent,
	})
}

func (c *Callback) handleState(w http.ResponseWriter, r *http.Request) {
	s := c.session(r)
	if s == nil {
		http.Error(w, "unknown session", http.StatusUnauthorized)
		return
	}
	var req StateReq
	if !decode(w, r, &req) {
		return
	}
	s.mu.Lock()
	if req.Message != "" && s.Rec != nil {
		s.Rec.State(req.Message, req.Data)
	}
	s.mu.Unlock()
	writeJSON(w, map[string]any{"ok": true})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
