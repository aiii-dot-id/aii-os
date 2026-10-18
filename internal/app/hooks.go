package app

import "github.com/aiii-dot-id/aii-os/internal/dashboard"

func (a *App) buildLiveHandler() *dashboard.WSHandler {
	h := &dashboard.WSHandler{
		Background: a.runBackground,
		Speaker:    "identity",
		GetStats:   a.statsState,
		Restart:    a.Restart,
	}
	a.wireTurnHooks(h)
	a.wireAskHooks(h)
	a.wireWorkHooks(h)
	a.wireInteractionHooks(h)
	a.wireRecordHooks(h)
	a.wireVoiceHooks(h)
	a.wireMessageHooks(h)
	a.wireIdentityViewHooks(h)
	a.wireContinuityHooks(h)
	a.pn.WireHooks(h)
	a.wireProviderHooks(h)
	a.wireSignInHooks(h)
	a.wireAuthProfileHooks(h)
	a.wirePluginHooks(h)
	a.wireProjectHooks(h)
	a.wireSettingsHooks(h)
	a.wireDashboardAccessHooks(h)
	return h
}
