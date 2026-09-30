package app

import (
	"context"
	"fmt"
	"runtime/debug"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

const scheduleTimeout = 90 * time.Second

const defaultScheduleFirst = 30 * time.Second

func (a *App) scheduleFirstDelay() time.Duration {
	if a.scheduleFirst > 0 {
		return a.scheduleFirst
	}
	return defaultScheduleFirst
}

func (a *App) scheduleWait(d pluginhost.ScheduleDecl) time.Duration {
	if a.scheduleEvery != nil {
		return a.scheduleEvery(d)
	}
	return time.Duration(d.EverySeconds) * time.Second
}

type scheduleRunner struct {
	cancel context.CancelFunc
	done   chan struct{}
}

func scheduleKey(pluginID string, d pluginhost.ScheduleDecl) string {
	return fmt.Sprintf("%s|%s|%d", pluginID, d.Operation, d.EverySeconds)
}

func (a *App) convergeSchedules(ctx context.Context) {
	if a.scheduling == nil {
		a.scheduling = map[string]*scheduleRunner{}
	}
	want := map[string][2]interface{}{}
	a.pluginMu.Lock()
	for _, p := range a.plugins {
		if p == nil {
			continue
		}
		for _, d := range p.Schedule {
			want[scheduleKey(p.ID, d)] = [2]interface{}{p.ID, d}
		}
	}
	a.pluginMu.Unlock()
	for key, r := range a.scheduling {
		if _, still := want[key]; !still {
			r.cancel()
			delete(a.scheduling, key)
		}
	}
	for key, w := range want {
		if _, live := a.scheduling[key]; live {
			continue
		}
		id, d := w[0].(string), w[1].(pluginhost.ScheduleDecl)
		rctx, cancel := context.WithCancel(ctx)
		r := &scheduleRunner{cancel: cancel, done: make(chan struct{})}
		if !a.runBackground(func() { defer close(r.done); a.runSchedule(rctx, id, d) }) {
			cancel()
			logsink.Info("schedule.refusal", "%s: %s not scheduled: the runtime is stopping", id, d.Operation)
			continue
		}
		a.scheduling[key] = r
	}
}

func (a *App) runSchedule(ctx context.Context, pluginID string, d pluginhost.ScheduleDecl) {
	defer func() {
		if p := recover(); p != nil {
			logsink.Error("schedule.error", "%s: %s PANICKED (contained; it is scheduled again when the plugin is installed again or the identity restarts): %v\n%s", pluginID, d.Operation, p, debug.Stack())
		}
	}()
	logsink.Info("schedule.start", "%s: %s every %ds", pluginID, d.Operation, d.EverySeconds)
	defer logsink.Info("schedule.end", "%s: %s stopped", pluginID, d.Operation)
	timer := time.NewTimer(a.scheduleFirstDelay())
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		a.scheduledCall(ctx, pluginID, d)
		timer.Reset(a.scheduleWait(d))
	}
}

func (a *App) scheduledCall(ctx context.Context, pluginID string, d pluginhost.ScheduleDecl) {
	ctx, cancel := context.WithTimeout(ctx, scheduleTimeout)
	defer cancel()
	route := a.webhookRoute(ctx, pluginID)
	args := map[string]interface{}{}
	if route.Acknowledges {
		args["recorded"] = a.heldFor(pluginID)
	}
	res, err := a.toolReg.Execute(ctx, pluginhost.ToolNameFor(pluginID, d.Operation), args)
	if ctx.Err() != nil && err == nil && res.Error == "" {
		return
	}
	if err == nil && (res.ReasonCode == tools.ReasonSafeSuspended || res.ReasonCode == tools.ReasonOperatorDisabled) {
		return
	}
	if err == nil && res.Error != "" {
		err = fmt.Errorf("%s", res.Error)
	}
	if err == nil {
		_, err = a.takeAnswer(route, pluginID, res.Output)
	}
	if err != nil {
		logsink.Warn("schedule.error", "%s: %s failed: %v", pluginID, d.Operation, err)
		a.reportScheduleFailure(pluginID, d.Operation, err)
	}
}

func (a *App) reportScheduleFailure(pluginID, operation string, cause error) {
	route := channelRoute{Channel: "hook:" + pluginID, Plugin: pluginID, Hook: true}
	why := strings.Join(strings.Fields(cause.Error()), " ")
	if len(why) > 400 {
		why = why[:400] + "…"
	}
	in := arrival{
		ID:   "sched-" + operation + "-" + time.Now().UTC().Format("2006-01-02"),
		From: pluginID,
		Body: "the scheduled call " + operation + " failed: " + why + " — the plugin's duty it carries is not being done until it succeeds; the plugin's page shows its state",
	}
	if _, _, err := a.carryArrivals(route, []arrival{in}); err != nil {
		logsink.Warn("schedule.error", "%s: could not report that %s failed: %v", pluginID, operation, err)
	}
}
