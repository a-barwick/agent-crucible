package runtime

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"sync"

	"github.com/a-barwick/agent-crucible/internal/agent"
	"github.com/a-barwick/agent-crucible/internal/schema"
	"github.com/a-barwick/agent-crucible/internal/trace"
)

// Session is one trial the sidecar can call back into.
type Session struct {
	Ctx  context.Context
	Bus  agent.Bus
	Hook agent.Hook
	Rec  *trace.Recorder
	St   *agent.State
}

// Callback is a localhost HTTP server the Python graph uses for tools and hooks.
type Callback struct {
	srv  *http.Server
	url  string
	mu   sync.Mutex
	sess map[string]*Session
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
	c.srv = &http.Server{Handler: mux}
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
	tok := r.Header.Get("Authorization")
	if len(tok) > 7 && tok[:7] == "Bearer " {
		tok = tok[7:]
	}
	c.mu.Lock()
	s := c.sess[tok]
	c.mu.Unlock()
	return s
}

func (c *Callback) handleTool(w http.ResponseWriter, r *http.Request) {
	s := c.session(r)
	if s == nil {
		http.Error(w, "unknown session", http.StatusUnauthorized)
		return
	}
	var req ToolReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	res, err := s.Bus.Call(s.Ctx, req.Tool, req.Args)
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
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
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

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
