// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
package facility

import (
	"fmt"
	"sort"
)

// .
// .
// .
const (
	// .
	// .
	// .
	// .
	TransportLocal = "sev_transport.local"

	// .
	// .
	// .
	// .
	OperatorPresenceFresh = "sev_operator_presence.fresh"

	// .
	// .
	// .
	// .
	ForegroundLifecycle = "sev_foreground.lifecycle"
)

// .
type Facility struct {
	// .
	Name string
	// .
	// .
	// .
	Provider string
	// .
	// .
	// .
	// .
	Live func() bool
}

// .
type Status struct {
	Name     string
	Provider string
	Live     bool
}

// .
// .
type Set struct {
	byName map[string]Facility
	names  []string
}

// .
// .
func NewSet(facilities ...Facility) (*Set, error) {
	s := &Set{byName: make(map[string]Facility, len(facilities))}
	for _, f := range facilities {
		if f.Name == "" {
			return nil, fmt.Errorf("facility: refusing an unnamed facility")
		}
		if _, dup := s.byName[f.Name]; dup {
			return nil, fmt.Errorf("facility: %s advertised twice", f.Name)
		}
		s.byName[f.Name] = f
		s.names = append(s.names, f.Name)
	}
	sort.Strings(s.names)
	return s, nil
}

// .
// .
// .
// .
func (s *Set) Has(name string) bool {
	if s == nil {
		return false
	}
	_, ok := s.byName[name]
	return ok
}

// .
func (s *Set) Names() []string {
	if s == nil {
		return nil
	}
	return append([]string(nil), s.names...)
}

// .
func (s *Set) Snapshot() []Status {
	if s == nil {
		return nil
	}
	out := make([]Status, 0, len(s.names))
	for _, name := range s.names {
		f := s.byName[name]
		live := true
		if f.Live != nil {
			live = f.Live()
		}
		out = append(out, Status{Name: name, Provider: f.Provider, Live: live})
	}
	return out
}
