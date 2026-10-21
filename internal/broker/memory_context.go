package broker

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"sort"
	"strconv"
)

type memoryContext struct {
	Applicability     string `json:"applicability"`
	ProjectID         string `json:"project_id,omitempty"`
	WorkSessionID     string `json:"work_session_id,omitempty"`
	SourceIncarnation string `json:"source_incarnation,omitempty"`
}

type memorySelectionContext struct {
	Selection     string `json:"selection"`
	ProjectID     string `json:"project_id,omitempty"`
	WorkSessionID string `json:"work_session_id,omitempty"`
	Reason        string `json:"reason,omitempty"`
}

func selectionContextOf(s store.PluginMemorySelection) memorySelectionContext {
	c := memorySelectionContext{Selection: s.Kind}
	switch s.Kind {
	case "task", "relevant":
		c.WorkSessionID, c.ProjectID = s.WorkSession, s.Project
		if s.Kind == "relevant" && s.WorkSession == "" {
			c.Reason = "no_session"
		}
	case "project":
		c.ProjectID = s.Project
	}
	return c
}

func contextOfMemory(m store.PluginMemory) memoryContext {
	return memoryContext{m.Applicability, m.Project, m.WorkSession, m.SourceIncarnation}
}

func (b *Binding) memorySelection(kind string) (store.PluginMemorySelection, error) {
	s := store.PluginMemorySelection{Kind: kind}
	switch kind {
	case "", "legacy":
		return s, nil
	case "task", "project", "reusable", "relevant":
	default:
		return s, fmt.Errorf("unknown memory selection %q", kind)
	}
	scope := b.scope.Load()
	if scope != nil && scope.SessionReason == "session_lookup_failed" {
		return s, fmt.Errorf("executing work session lookup failed")
	}
	if scope != nil && scope.WorkSession != "" {
		ws, err := b.host.cfg.Store.WorkSessionByID(scope.WorkSession)
		if err != nil {
			return s, fmt.Errorf("executing work session unavailable: %w", err)
		}
		if ws == nil {
			return s, fmt.Errorf("executing work session unavailable")
		}
		s.WorkSession, s.Project = ws.ID, ws.Project
	}
	if kind == "task" && s.WorkSession == "" || kind == "project" && s.Project == "" {
		return s, fmt.Errorf("memory %s selection requires a resolvable executing work session", kind)
	}
	return s, nil
}

func (b *Binding) memoryWriteContext(kind string) (memoryContext, error) {
	if kind == "" {
		kind = "legacy"
	}
	if kind == "relevant" {
		return memoryContext{}, fmt.Errorf("relevant is a read selection, not applicability")
	}
	selection, err := b.memorySelection(kind)
	if err != nil {
		return memoryContext{}, err
	}
	c := memoryContext{Applicability: kind}
	if kind == "legacy" {
		return c, nil
	}

	if selection.WorkSession == "" && kind != "reusable" {
		return c, fmt.Errorf("scoped memory writes require a resolvable executing work session")
	}

	if scope := b.scope.Load(); selection.WorkSession == "" && scope != nil && scope.DeliveredSession != "" {
		ws, err := b.host.cfg.Store.WorkSessionByID(scope.DeliveredSession)
		if err != nil {
			return c, fmt.Errorf("delivered work session unavailable: %w", err)
		}
		if ws != nil && ws.Status == "delivered" {
			selection.WorkSession, selection.Project = ws.ID, ws.Project
		}
	}
	c.WorkSessionID, c.ProjectID = selection.WorkSession, selection.Project
	c.SourceIncarnation = b.host.cfg.Store.InteractionIncarnation()
	return c, nil
}

func (b *Binding) validateMemoryEvidence(ctx context.Context, refs []store.MemoryEvidenceRef) (string, error) {
	if len(refs) > 16 {
		return "", fmt.Errorf("memory evidence exceeds 16 references")
	}
	refs = append([]store.MemoryEvidenceRef{}, refs...)
	seen := map[string]bool{}
	for i := range refs {
		r := &refs[i]
		n, err := strconv.ParseUint(r.Sequence, 10, 64)
		hash, hashErr := hex.DecodeString(r.SHA256)
		if r.ID == "" || len(r.ID) > 256 || err != nil || n == 0 || strconv.FormatUint(n, 10) != r.Sequence || hashErr != nil || len(hash) != 32 || hex.EncodeToString(hash) != r.SHA256 || r.Incarnation != b.host.cfg.Store.InteractionIncarnation() || r.Availability != "" || seen[r.ID] {
			return "", fmt.Errorf("memory evidence requires unique retained IDs, canonical sequence/hash and the current source incarnation; availability is host-owned")
		}
		seen[r.ID] = true
		status, err := b.host.cfg.Store.PluginMemoryEvidenceStatus(ctx, *r)
		if err != nil {
			return "", err
		}
		if status != "available" {
			return "", fmt.Errorf("memory evidence %s is %s", r.ID, status)
		}
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i].ID < refs[j].ID })
	raw, err := json.Marshal(refs)
	return string(raw), err
}

func (b *Binding) memoryEvidence(ctx context.Context, m store.PluginMemory) ([]store.MemoryEvidenceRef, error) {
	refs := []store.MemoryEvidenceRef{}
	if m.Evidence != "" {
		if err := json.Unmarshal([]byte(m.Evidence), &refs); err != nil {
			return nil, err
		}
	}
	for i := range refs {
		status, err := b.host.cfg.Store.PluginMemoryEvidenceStatus(ctx, refs[i])
		if err != nil {
			return nil, err
		}
		refs[i].Availability = status
	}
	return refs, nil
}
