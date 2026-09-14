package app

import (
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// .
// .
// .
// .
// .
// .
func (a *App) workObserved(ev store.WorkEvent) {
	payload := map[string]interface{}{"session": ev.ID, "project": ev.Project, "actor": ev.Actor}
	switch ev.Kind {
	case store.WorkStarted:
		a.emitPluginEvent(pluginhost.TopicWorkStarted, payload)
	case store.WorkDelivered:
		payload["outcome"] = ev.Outcome
		payload["evidence"] = ev.Evidence
		a.emitPluginEvent(pluginhost.TopicWorkDelivered, payload)
		a.withdrawAsk(ev.ID, "the session it belonged to has delivered")
	case store.WorkHarvested:
		a.emitPluginEvent(pluginhost.TopicWorkHarvested, payload)
	}
}
