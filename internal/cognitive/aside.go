package cognitive

import "sync"

type setAside struct {
	mu  sync.Mutex
	why map[string]string
}

func (s *setAside) add(id, why string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.why == nil {
		s.why = map[string]string{}
	}
	s.why[id] = why
}

func (s *setAside) has(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.why[id]
	return ok
}

func (s *setAside) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.why)
}

func (s *setAside) forget() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.why = nil
}
