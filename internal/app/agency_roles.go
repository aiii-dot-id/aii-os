package app

import (
	"maps"
	"slices"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
)

func roleStates(roles map[string]RoleRoute) []dashboard.RoleState {
	if len(roles) == 0 {
		return nil
	}
	out := make([]dashboard.RoleState, 0, len(roles))
	for _, name := range slices.Sorted(maps.Keys(roles)) {
		r := roles[name]
		out = append(out, dashboard.RoleState{Name: name, Provider: r.Provider, Model: r.Model, Description: r.Description})
	}
	return out
}
