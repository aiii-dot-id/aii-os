package store

import "strings"

// .
// .
// .
// .
// .

// .
const (
	WorkStarted   = "started"
	WorkDelivered = "delivered"
	WorkHarvested = "harvested"
)

// .
type WorkEvent struct {
	Kind    string
	ID      string
	Project string
	// .
	// .
	Actor string
	// .
	// .
	// .
	Outcome string
	// .
	Evidence string
}

// .
func (s *Store) SetWorkObserver(fn func(WorkEvent)) {
	s.mu.Lock()
	s.workObserver = fn
	s.mu.Unlock()
}

// .
// .
func (s *Store) notifyWork(ev **WorkEvent) {
	if ev == nil || *ev == nil {
		return
	}
	s.mu.RLock()
	fn := s.workObserver
	s.mu.RUnlock()
	if fn != nil {
		fn(**ev)
	}
}

func workActor(description string) string {
	if strings.HasPrefix(description, subagentDescriptionPrefix) {
		return "subagent"
	}
	return "main"
}

// .
// .
// .
func outcomeClass(result string) string {
	for _, c := range []string{"served", "partial", "unserved"} {
		if strings.HasPrefix(result, c+":") {
			return c
		}
	}
	if strings.HasPrefix(result, "FAILED") {
		return "failed"
	}
	return ""
}

// .
func (s *Store) workRow(id string) (project, description string) {
	_ = s.db.QueryRow(`SELECT project_id, description FROM work_sessions WHERE id=?`, id).Scan(&project, &description)
	return project, description
}
