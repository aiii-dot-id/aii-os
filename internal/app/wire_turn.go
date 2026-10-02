package app

import "github.com/aiii-dot-id/aii-os/internal/dashboard"

func (a *App) wireTurnHooks(h *dashboard.WSHandler) {
	h.HandleMessage = a.handleMessage
	h.ObserveChat = a.observeChat
	h.RecentTurns = a.recentTurnViews

	h.AdmitChat = a.admitOperatorChat
	h.ReleaseTurn = a.EndTurn
	h.AcquireTurn = a.acquireTurn

	h.TurnActive = a.TurnActive
	h.Steer = a.Steer
	h.CancelTurn = a.CancelTurn
	h.PendingSteers = a.PendingSteers
}
