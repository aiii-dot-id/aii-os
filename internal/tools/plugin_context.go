package tools

import (
	"context"
	"sort"
)

type contextProvider interface{ ContextProvider() bool }

type ContextProvider struct {
	Name, Plugin string
	Owner        any
}

func (r *Registry) ContextProviders() []ContextProvider {
	r.regMu.RLock()
	defer r.regMu.RUnlock()
	var out []ContextProvider
	for name, t := range r.tools {
		c, ok := t.(contextProvider)
		if !ok || !c.ContextProvider() || !r.contextEligibleLocked(name) {
			continue
		}
		owned, ok := t.(dispatchOwned)
		if !ok {
			continue
		}
		out = append(out, ContextProvider{Name: name, Plugin: r.sources[name], Owner: owned.DispatchOwner()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (r *Registry) contextEligibleLocked(name string) bool {
	plugin := r.sources[name]
	if plugin == "" || plugin == "builtin" || !r.hostOnly[name] || !r.ToolEnabled(name) {
		return false
	}
	selected := false
	for n := range r.tools {
		if r.sources[n] != plugin || r.hostOnly[n] {
			continue
		}
		_, standing := r.seatIndexLocked(n)
		if !standing && !r.offered[n] {
			continue
		}
		if !r.offered[n] || !r.ToolEnabled(n) || !r.seatMatchesLocked(n) {
			return false
		}
		selected = true
	}
	return selected
}

type contextSelectionKey struct{}

func WithContextSelection(ctx context.Context) context.Context {
	return context.WithValue(ctx, contextSelectionKey{}, true)
}

func (r *Registry) contextEligible(name string) bool {
	r.regMu.RLock()
	defer r.regMu.RUnlock()
	return r.contextEligibleLocked(name)
}
