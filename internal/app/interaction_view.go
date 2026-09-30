package app

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/aiii-dot-id/aii-os/internal/interaction"
)

func (a *App) QueryInteractions(q interaction.Query) (*interaction.Page, error) {
	if err := q.Normalize(); err != nil {
		return nil, err
	}
	var p *interaction.Page
	var err error
	if q.Source == "recorded" {
		if a.store == nil {
			return nil, &interaction.Error{Code: "INTERACTION_SOURCE_UNAVAILABLE", Detail: "recorded history unavailable"}
		}
		p, err = a.store.QueryInteractions(q)
	} else {
		if a.engine == nil {
			return nil, &interaction.Error{Code: "INTERACTION_SOURCE_UNAVAILABLE", Detail: "transient owner unavailable"}
		}
		inc, rows, lost := a.transientInteractions()
		p, err = interaction.MemoryPage(q, inc, rows, lost)
	}
	if err != nil {
		return nil, err
	}
	if a.keyPair != nil {
		p.Identity = a.keyPair.Fingerprint()
	}
	interaction.Seal(p)
	return p, nil
}
func (a *App) ReadInteraction(req interaction.ReadRequest) (*interaction.ReadResult, error) {
	if err := interaction.ValidateRead(&req); err != nil {
		return nil, err
	}
	if req.Source == "recorded" {
		if a.store == nil {
			return nil, &interaction.Error{Code: "INTERACTION_SOURCE_UNAVAILABLE", Detail: "recorded history unavailable"}
		}
		return a.store.ReadInteraction(req)
	}
	if a.engine == nil {
		return nil, &interaction.Error{Code: "INTERACTION_SOURCE_UNAVAILABLE", Detail: "transient owner unavailable"}
	}
	inc, rows, _ := a.transientInteractions()
	if inc != req.Incarnation {
		return nil, &interaction.Error{Code: "INTERACTION_SOURCE_CHANGED", Detail: "transient source retired"}
	}
	for _, r := range rows {
		if r.ID == req.ID {
			raw, err := json.Marshal(r)
			if err != nil {
				return nil, err
			}
			return interaction.DetailRead(req, raw)
		}
	}
	return nil, &interaction.Error{Code: "INTERACTION_CONTENT_UNAVAILABLE", Detail: "transient record was not retained"}
}
func (a *App) wireInteractionView() {
	if a.dashboard == nil {
		return
	}
	if a.store != nil {
		a.store.SetInteractionObserver(a.dashboard.PokeInteractions)
	}
	if a.engine != nil {
		a.engine.SetInteractionObserver(a.dashboard.PokeInteractions)
	}
	a.dashboard.PokeInteractions()
}

func (a *App) transientInteractions() (string, []interaction.Record, uint64) {
	if a.safeTools == nil {
		return a.engine.TransientInteractions()
	}
	t := a.safeTools
	t.mu.Lock()
	defer t.mu.Unlock()
	inc, rows, lost := a.engine.TransientInteractions()
	for _, e := range t.events {
		id := fmt.Sprintf("safe_tool_%x", sha256.Sum256([]byte(fmt.Sprintf("%s/%d", e.TurnID, e.Ordinal))))
		details := interaction.Details{Tool: e.Tool, Model: e.Model, Ordinal: e.Ordinal, Origin: "safe_tool", Reason: "arguments and result bodies are not retained"}
		if e.StartSequence > 0 {
			rows = append(rows, interaction.Record{ID: id, Sequence: e.StartSequence, Kind: interaction.ToolCall, Role: interaction.System, TurnID: e.TurnID, CreatedAt: e.StartedAt, RecordedAt: e.StartedAt, Details: details})
		}
		if e.DoneSequence > 0 {
			parent, kind := id, interaction.ToolResult
			if e.ExecutionID != "" {
				parent = e.DurableParent
				details.Reason = "attempt recorded durably; completion retained only in this process; arguments and result bodies not retained"
				if parent == "" {
					kind = interaction.Notice
				}
			}
			outcome := interaction.Succeeded
			if e.Failed {
				outcome = interaction.Failed
			}
			rows = append(rows, interaction.Record{ID: id + "_result", RelatedID: parent, Sequence: e.DoneSequence, Kind: kind, Role: interaction.System, TurnID: e.TurnID, CreatedAt: e.DoneAt, RecordedAt: e.DoneAt, Outcome: outcome, Details: details})
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Sequence < rows[j].Sequence })
	return inc, rows, lost + t.lost
}
