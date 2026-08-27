// Package facility is the host facility set — the "facilities down"
// half of the seam principle (docs/PLATFORM_SEAMS.md §1): the HOST
// advertises named, platform-implemented services; variants declare
// facility: requires-predicates; selection is the intersection.
//
// The shape is adopted from the C substrate contract sev_facility.h:
// a facility has a stable identifier, a provider info string (who
// implements it on this platform), and a status — reduced here to the
// Go host's need (identity + provider + liveness probe) rather than
// ported field-for-field: the C provider struct's degraded-flag
// envelope belongs to facility BACKENDS, which arrive with the audio
// seam; the SET only answers membership and operator-visible status.
//
// Facility NAMES are C-owned and never invented here
// (sev_manifest.h:300-334 owns the predicate spellings; the voice
// packet and sev_facility.h own the vocabulary). This package defines
// constants only for the names the Go host can advertise TODAY —
// audio/keystore et al. join when their backends exist, because an
// advertisement without an implementation would be a lie the selector
// then acts on.
package facility

import (
	"fmt"
	"sort"
)

// The C-owned facility names the Go host currently advertises
// (predicate spelling minus the "facility:" class prefix —
// sev_manifest.h:300, 320-322).
const (
	// TransportLocal: a local BBB transport exists. Structurally true
	// wherever a plugin can run at all — stdio on desktop, in-process
	// on mobile — which is why the C admission satisfies its predicate
	// unconditionally (plugin_host_install.c:1055-1056).
	TransportLocal = "sev_transport.local"

	// OperatorPresenceFresh: the host can answer "is the operator
	// present, freshly" — backed by the dashboard's session presence
	// (sessionConns + grace window) OR'd with the mobile shell's
	// foreground truth.
	OperatorPresenceFresh = "sev_operator_presence.fresh"

	// ForegroundLifecycle: the host delivers foreground/background
	// lifecycle transitions — the mobile shells' SetForeground binding.
	// Desktop builds do not advertise it (no lifecycle exists to
	// deliver).
	ForegroundLifecycle = "sev_foreground.lifecycle"
)

// Facility is one advertised host facility.
type Facility struct {
	// Name is the C-owned facility identifier (never invented).
	Name string
	// Provider is the operator-visible provider info string — WHO
	// implements this facility on this platform (the sev_facility
	// provider_id/platform_id pair, flattened to one line).
	Provider string
	// Live reports current status. nil means structurally live (the
	// facility exists by construction). A false answer does NOT remove
	// membership: the facility is still provided, momentarily idle —
	// selection matches membership, status is telemetry.
	Live func() bool
}

// Status is one operator-visible row of the advertised set.
type Status struct {
	Name     string
	Provider string
	Live     bool
}

// Set is the host's advertised facility set, assembled by the app per
// platform at startup and read-only afterwards.
type Set struct {
	byName map[string]Facility
	names  []string // sorted, for stable operator output
}

// NewSet assembles the set. A duplicate or empty name is a wiring
// defect in the app's per-platform assembly and refuses loudly.
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

// Has reports membership: does this host provide the named facility?
// A nil Set advertises nothing — fail-closed for every name (the
// structurally-always sev_transport.local is the SELECTOR's rule,
// adopted from C, not smuggled in here).
func (s *Set) Has(name string) bool {
	if s == nil {
		return false
	}
	_, ok := s.byName[name]
	return ok
}

// Names lists the advertised facility names, sorted.
func (s *Set) Names() []string {
	if s == nil {
		return nil
	}
	return append([]string(nil), s.names...)
}

// Snapshot reports the operator-visible set with current status.
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
