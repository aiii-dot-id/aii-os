package app

import (
	"context"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/cognitive"
	"github.com/aiii-dot-id/aii-os/internal/dashboard"
)

const (
	passTurnHolding = "turn_holding"
	passStall       = "stall"
)

func (a *App) PassNow(ctx context.Context) (dashboard.PassNowResult, error) {
	set := a.cognition.rhythm.Load()
	if set == nil || a.timeFac == nil {
		return dashboard.PassNowResult{Reason: cognitive.PressAbsent}, nil
	}
	held := func() string { return "" }
	if set.dream != nil {
		held = set.dream.Held
	}
	return passNowDecision(a.TurnActive, held, func() (cognitive.BringForwardResult, error) {
		return a.timeFac.BringAlarmForward("rhythm")
	})
}

func passNowDecision(turnActive func() bool, held func() string, bring func() (cognitive.BringForwardResult, error)) (dashboard.PassNowResult, error) {
	if turnActive != nil && turnActive() {
		return dashboard.PassNowResult{Reason: passTurnHolding}, nil
	}
	if held != nil {
		if why := held(); why != "" {
			return dashboard.PassNowResult{Reason: why}, nil
		}
	}
	res, err := bring()
	if err != nil {
		return dashboard.PassNowResult{}, err
	}
	if !res.Moved {
		return dashboard.PassNowResult{Reason: res.Reason}, nil
	}
	return dashboard.PassNowResult{Moved: true, AskedAt: time.UnixMilli(res.To).UTC().Format("2006-01-02T15:04:05.000Z07:00")}, nil
}
