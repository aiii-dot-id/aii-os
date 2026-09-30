package app

import (
	"context"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

func (a *App) heldMail(ctx context.Context, after string) (dashboard.MailRepairState, error) {
	if a.store == nil {
		return dashboard.MailRepairState{}, &store.OutboxRepairError{Requirement: "an open identity"}
	}
	rows, err := a.store.HeldMessages(ctx, after)
	if err != nil {
		return dashboard.MailRepairState{}, err
	}
	state := dashboard.MailRepairState{Messages: rows}
	if len(rows) == store.HeldMessagePageSize {
		state.Next = rows[len(rows)-1].ID
	}
	seen := map[dashboard.MailRoute]bool{}
	for _, c := range a.configSnapshot().Contacts {
		route := dashboard.MailRoute{Name: c.Name, Channel: c.Channel}
		if c.Name != "" && c.Channel != "" && c.Address != "" && !seen[route] {
			state.Routes = append(state.Routes, route)
			seen[route] = true
		}
	}
	return state, nil
}

func (a *App) repairMail(ctx context.Context, request dashboard.MailRepairRequest) error {
	if a.store == nil {
		return &store.OutboxRepairError{Requirement: "an open identity"}
	}
	if reason, safe := a.SafeMode(); safe {
		return &store.FrozenError{Reason: reason}
	}
	a.cfgMu.RLock()
	defer a.cfgMu.RUnlock()
	canonical := ""
	found := false
	for _, c := range a.cfg.Contacts {
		if !strings.EqualFold(strings.TrimSpace(c.Name), strings.TrimSpace(request.Recipient)) {
			continue
		}
		if canonical != "" && canonical != c.Name {
			return &store.OutboxRepairError{Requirement: "an unambiguous current contact name"}
		}
		canonical = c.Name
		if c.Channel == request.Channel && c.Address != "" {
			found = true
		}
	}
	if !found {
		return &store.OutboxRepairError{Requirement: "a recipient and channel in the current operator contacts"}
	}
	if err := a.store.RepairHeldMessage(ctx, request.ID, request.SHA256, canonical, request.Channel); err != nil {
		return err
	}
	a.pokeOutbox()
	return nil
}

func (a *App) inspectHeldMail(ctx context.Context, id string) (dashboard.MailRepairState, error) {
	state, err := a.heldMail(ctx, "")
	if err != nil {
		return state, err
	}
	message, err := a.store.InspectHeldMessage(ctx, id)
	if err != nil {
		return dashboard.MailRepairState{}, err
	}
	state.Messages = []store.HeldMessage{message}
	state.Next = ""
	return state, nil
}
