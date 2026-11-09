package app

import (
	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

func nextStage(stage string) string {
	switch stage {
	case store.StageSupervised, "":
		return store.StageTrusted
	case store.StageTrusted:
		return store.StageAutonomous
	}
	return ""
}

func (a *App) wideningOffer() *dashboard.WideningOffer {
	if a.store == nil {
		return nil
	}
	rel, err := a.store.CurrentOperatorRelationship()
	if err != nil || rel == nil {
		return nil
	}
	stage := a.operatorStage()
	to := nextStage(stage)
	if to == "" {
		return nil
	}
	since, err := a.store.LastWideningSeq(rel.ID)
	if err != nil {
		logsink.Warn("work.error", "the last widening could not be read: %v", err)
		return nil
	}
	completed, abandoned, err := a.store.WideningCounts(rel.ID, since)
	if err != nil {
		logsink.Warn("work.error", "the identity's endings could not be read: %v", err)
		return nil
	}

	audited := 0
	for _, c := range completed {
		if c.Audited {
			audited++
		}
	}
	if abandoned > 0 || audited < a.wideningCompletions() {
		return nil
	}
	digest := store.AutonomyApprovalDigest(rel.ID, to)
	offer := &dashboard.WideningOffer{Counterpart: rel.ID, Name: rel.CounterpartName, From: stage, To: to, Digest: digest,
		Press: store.ApprovalPressContent(a.identityNameForPress(), to, digest)}
	for _, c := range completed {
		offer.Completions = append(offer.Completions, dashboard.WideningCompletionView{ID: c.ID, Description: c.Description, Result: c.Result, At: c.At, Audited: c.Audited})
	}
	return offer
}

func (a *App) identityNameForPress() string {
	if name := a.resolveDisplayName(); name != "" {
		return name
	}
	return "your identity"
}

func (a *App) resumeView() *dashboard.ResumeView {
	if a.store == nil {
		return nil
	}
	ws, err := a.store.ActiveWorkSession()
	if err != nil || ws == nil || ws.ResumesOn == "" {
		return nil
	}
	v := &dashboard.ResumeView{Session: ws.ID, Description: ws.Description, Promise: ws.Promise, On: ws.ResumesOn}
	if kind, who, found, err := a.store.PromiseOf(ws.Promise); err == nil && found {
		v.Kind, v.Counterpart = kind, who
	}
	return v
}
