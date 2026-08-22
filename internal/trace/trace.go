// Package trace records a trial as an ordered event list the UI can draw.
//
// A Recorder is safe to call from more than one goroutine. It is normally
// driven from a single graph walk, but a sidecar agent may use threads, and a
// timeline that dropped or interleaved events would be worse than useless —
// the whole point is that it replays.
package trace

import (
	"sync"

	"github.com/a-barwick/agent-crucible/internal/fault"
)

type Kind string

const (
	KindNodeEnter  Kind = "node_enter"
	KindNodeExit   Kind = "node_exit"
	KindToolCall   Kind = "tool_call"
	KindToolResult Kind = "tool_result"
	KindFault      Kind = "fault"
	KindState      Kind = "state"
	KindObjective  Kind = "objective"
	KindSideEffect Kind = "side_effect"
	KindVerdict    Kind = "verdict"
)

type Event struct {
	Seq     int            `json:"seq"`
	Tick    int64          `json:"tick"`
	Kind    Kind           `json:"kind"`
	Node    string         `json:"node,omitempty"`
	Tool    string         `json:"tool,omitempty"`
	Fault   fault.Type     `json:"fault,omitempty"`
	Message string         `json:"message"`
	OK      *bool          `json:"ok,omitempty"`
	Data    map[string]any `json:"data,omitempty"`
}

type Trace struct {
	Events []Event `json:"events"`
	seq    int
	mu     sync.Mutex
}

func New() *Trace { return &Trace{} }

type Recorder struct {
	tr   *Trace
	now  func() int64
	node string
}

func (t *Trace) Recorder(now func() int64) *Recorder {
	if now == nil {
		now = func() int64 { return 0 }
	}
	return &Recorder{tr: t, now: now}
}

func (r *Recorder) SetNode(name string) {
	r.tr.mu.Lock()
	r.node = name
	r.tr.mu.Unlock()
}

func (r *Recorder) Add(ev Event) {
	r.tr.mu.Lock()
	defer r.tr.mu.Unlock()
	r.add(ev)
}

func (r *Recorder) add(ev Event) {
	r.tr.seq++
	ev.Seq = r.tr.seq
	if ev.Tick == 0 {
		ev.Tick = r.now()
	}
	if ev.Node == "" {
		ev.Node = r.node
	}
	r.tr.Events = append(r.tr.Events, ev)
}

func (r *Recorder) NodeEnter(name string) {
	r.tr.mu.Lock()
	defer r.tr.mu.Unlock()
	r.node = name
	r.add(Event{Kind: KindNodeEnter, Node: name, Message: "enter " + name})
}

func (r *Recorder) NodeExit(name, next string) {
	r.Add(Event{Kind: KindNodeExit, Node: name, Message: "exit " + name + " → " + next})
}

func (r *Recorder) ToolCall(tool string, args map[string]any) {
	r.Add(Event{Kind: KindToolCall, Tool: tool, Message: "call " + tool, Data: args})
}

func (r *Recorder) ToolResult(tool string, ok bool, err string, data map[string]any) {
	msg := "result " + tool
	if err != "" {
		msg += " " + err
	}
	r.Add(Event{Kind: KindToolResult, Tool: tool, OK: &ok, Message: msg, Data: data})
}

func (r *Recorder) Fault(t fault.Type, target, msg string) {
	if msg == "" {
		msg = string(t)
	}
	r.Add(Event{Kind: KindFault, Fault: t, Tool: target, Message: msg})
}

func (r *Recorder) State(msg string, data map[string]any) {
	r.Add(Event{Kind: KindState, Message: msg, Data: data})
}

func (r *Recorder) Objective(msg string) {
	r.Add(Event{Kind: KindObjective, Message: msg})
}

func (r *Recorder) SideEffect(msg string, data map[string]any) {
	r.Add(Event{Kind: KindSideEffect, Message: msg, Data: data})
}

// Faults is the distinct faults that actually fired, in the order they landed.
// A fault that was armed but never applied is not one of them.
func (t *Trace) Faults() []fault.Type {
	t.mu.Lock()
	defer t.mu.Unlock()
	seen := map[fault.Type]bool{}
	var out []fault.Type
	for _, ev := range t.Events {
		if ev.Kind == KindFault && ev.Fault != "" && !seen[ev.Fault] {
			seen[ev.Fault] = true
			out = append(out, ev.Fault)
		}
	}
	return out
}

func (t *Trace) Nodes() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	var out []string
	for _, ev := range t.Events {
		if ev.Kind == KindNodeEnter {
			out = append(out, ev.Node)
		}
	}
	return out
}
