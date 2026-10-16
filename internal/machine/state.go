package machine

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"

	"github.com/aiii-dot-id/aii-os/internal/atomicfile"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
)

const (
	RecordedByRegister = "registered"

	RecordedAtFirstLink = "self-registered"
)

type Record struct {
	Slot      string `json:"slot"`
	Identity  string `json:"identity"`
	PublicKey string `json:"public_key"`
	How       string `json:"how"`
	At        string `json:"at"`
}

type State struct {
	Identities []Record

	Store    string
	Draining []string

	Refs []Ref

	Pending []Pending

	Unreferenced map[string]string

	Gone map[string]string

	other map[string]json.RawMessage
}

type Ref struct {
	Slot   string `json:"slot"`
	Plugin string `json:"plugin"`
	Entry  string `json:"entry"`
}

type Pending struct {
	Entry string               `json:"entry"`
	Since string               `json:"since"`
	Work  pluginhost.StoreWork `json:"work"`
	Adopt *pluginhost.Adoption `json:"adopt,omitempty"`
}

func (s *State) stateMembers() []struct {
	key string
	v   any
} {
	return []struct {
		key string
		v   any
	}{
		{"identities", &s.Identities}, {"store", &s.Store}, {"draining", &s.Draining}, {"refs", &s.Refs},
		{"pending", &s.Pending}, {"unreferenced", &s.Unreferenced}, {"gone", &s.Gone},
	}
}

func (s State) MarshalJSON() ([]byte, error) {
	out := make(map[string]any, len(s.other)+7)
	for k, v := range s.other {
		out[k] = v
	}
	ids := s.Identities
	if ids == nil {
		ids = []Record{}
	}
	out["identities"] = ids
	if s.Store != "" {
		out["store"] = s.Store
	}
	if len(s.Draining) > 0 {
		out["draining"] = s.Draining
	}
	if len(s.Refs) > 0 {
		out["refs"] = s.Refs
	}
	if len(s.Pending) > 0 {
		out["pending"] = s.Pending
	}
	if len(s.Unreferenced) > 0 {
		out["unreferenced"] = s.Unreferenced
	}
	if len(s.Gone) > 0 {
		out["gone"] = s.Gone
	}
	return json.Marshal(out)
}

func (s *State) UnmarshalJSON(b []byte) error {
	var all map[string]json.RawMessage
	if err := json.Unmarshal(b, &all); err != nil {
		return err
	}
	for _, m := range s.stateMembers() {
		if raw, ok := all[m.key]; ok {
			if err := json.Unmarshal(raw, m.v); err != nil {
				return fmt.Errorf("%s: %w", m.key, err)
			}
			delete(all, m.key)
		}
	}
	s.other = all
	return nil
}

func (s State) clone() State {
	next := s
	next.Identities = append([]Record(nil), s.Identities...)
	next.Draining = append([]string(nil), s.Draining...)
	next.Refs = append([]Ref(nil), s.Refs...)
	next.Pending = append([]Pending(nil), s.Pending...)
	next.Unreferenced = maps.Clone(s.Unreferenced)
	next.Gone = maps.Clone(s.Gone)
	return next
}

func (s State) record(slot string) (Record, bool) {
	for _, r := range s.Identities {
		if r.Slot == slot {
			return r, true
		}
	}
	return Record{}, false
}

func (s State) with(r Record) State {
	next := s.clone()
	next.Identities = nil
	for _, old := range s.Identities {
		if old.Slot != r.Slot {
			next.Identities = append(next.Identities, old)
		}
	}
	next.Identities = append(next.Identities, r)
	return next
}

func loadState(path string) (State, error) {
	raw, err := atomicfile.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return State{}, nil
	}
	if err != nil {
		return State{}, fmt.Errorf("read %s: %w", path, err)
	}
	var s State
	if err := json.Unmarshal(raw, &s); err != nil {
		return State{}, fmt.Errorf("%s does not read: %w (it is the machine service's own; move it aside to start with no identity recorded)", path, err)
	}
	return s, nil
}

func (s *Server) updateState(f func(*State) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.state.clone()
	if err := f(&next); err != nil {
		return err
	}
	if err := saveState(s.statePath, next); err != nil {
		return err
	}
	s.state = next
	return nil
}

func saveState(path string, s State) error {
	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("encode the machine service's state: %w", err)
	}
	raw = append(raw, '\n')
	published, err := atomicfile.WriteReplace(path, raw, 0o600)
	if err != nil {
		if published {
			return fmt.Errorf("write %s: published but not durable: %w", path, err)
		}
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
