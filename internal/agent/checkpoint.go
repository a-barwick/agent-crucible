package agent

import "sync"

// Checkpoint is one graph snapshot keyed by thread id.
type Checkpoint struct {
	State State  `json:"state"`
	Node  string `json:"node,omitempty"`
	Step  int    `json:"step,omitempty"`
}

// Checkpointer is the LangGraph MemorySaver contract: persist state per thread.
type Checkpointer interface {
	Put(threadID string, cp Checkpoint)
	Get(threadID string) (Checkpoint, bool)
}

// MemorySaver is an in-process checkpointer. Same role as
// langgraph.checkpoint.memory.InMemorySaver.
type MemorySaver struct {
	mu sync.Mutex
	m  map[string]Checkpoint
}

func NewMemorySaver() *MemorySaver {
	return &MemorySaver{m: map[string]Checkpoint{}}
}

func (s *MemorySaver) Put(threadID string, cp Checkpoint) {
	if threadID == "" {
		return
	}
	s.mu.Lock()
	s.m[threadID] = cp
	s.mu.Unlock()
}

func (s *MemorySaver) Get(threadID string) (Checkpoint, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp, ok := s.m[threadID]
	return cp, ok
}
