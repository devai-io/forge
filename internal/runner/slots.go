package runner

import (
	"context"
	"sync"
)

// slots bounds how many runs this machine works on at once. The limit comes
// from Forge's settings with every heartbeat (agent.json's max_concurrent
// caps it), so it can change while runs are in flight: lowering it lets the
// running ones finish and holds new claims until there is room.
type slots struct {
	mu      sync.Mutex
	used    int
	limit   int
	changed chan struct{}
}

func newSlots(limit int) *slots {
	return &slots{limit: max(limit, 1), changed: make(chan struct{})}
}

// signal wakes everyone waiting; the caller holds mu.
func (s *slots) signal() {
	close(s.changed)
	s.changed = make(chan struct{})
}

// acquire waits for a free slot; false when ctx ends first.
func (s *slots) acquire(ctx context.Context) bool {
	for {
		s.mu.Lock()
		if s.used < s.limit {
			s.used++
			s.mu.Unlock()
			return true
		}
		ch := s.changed
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			return false
		case <-ch:
		}
	}
}

func (s *slots) release() {
	s.mu.Lock()
	s.used--
	s.signal()
	s.mu.Unlock()
}

func (s *slots) setLimit(n int) {
	s.mu.Lock()
	if n = max(n, 1); n != s.limit {
		s.limit = n
		s.signal()
	}
	s.mu.Unlock()
}

func (s *slots) max() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.limit
}

// drain waits until no run holds a slot.
func (s *slots) drain() {
	for {
		s.mu.Lock()
		if s.used == 0 {
			s.mu.Unlock()
			return
		}
		ch := s.changed
		s.mu.Unlock()
		<-ch
	}
}
