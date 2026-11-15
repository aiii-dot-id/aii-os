package app

import (
	"context"
	"encoding/json"
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

const scheduleContinueGap = 5 * time.Second

func (a *App) scheduleContinue() time.Duration {
	if a.scheduleGap > 0 {
		return a.scheduleGap
	}
	return scheduleContinueGap
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
	want := map[string][2]any{}
	a.pluginMu.Lock()
	for _, p := range a.plugins {
		if p == nil {
			continue
		}
		for _, d := range p.Schedule {
			want[scheduleKey(p.ID, d)] = [2]any{p.ID, d}
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

	first := a.scheduleFirstDelay()
	hold := a.scheduleHoldFor(pluginID, d.Operation)
	blocked := hold.load(ctx, time.Now())

	floor := time.Now().Add(first)
	due := func() time.Time {
		if want, _ := hold.state(); want.After(floor) {
			return want
		}
		return floor
	}
	timer := time.NewTimer(nextWake(hold, due(), time.Now()))
	defer timer.Stop()

	var last uint64
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		now := time.Now()
		if blocked != nil {
			if blocked = hold.load(ctx, now); blocked != nil {
				if ctx.Err() == nil {
					cause := fmt.Errorf("not called: %w; read again in %s", blocked, first)
					logsink.Warn("schedule.refusal", "%s: %s %v", pluginID, d.Operation, cause)
					a.reportScheduleFailure(pluginID, d.Operation, cause)
				}
				floor = now.Add(first)
				timer.Reset(first)
				continue
			}
		}
		hold.persist(ctx, now)
		if at := due(); now.Before(at) {

			timer.Reset(nextWake(hold, at, now))
			continue
		}
		interval := a.scheduleWait(d)
		out, answered := a.scheduledCall(ctx, pluginID, d)
		now = time.Now()
		w, err := nextWait(out, answered, last, now, interval, a.scheduleContinue())
		if err != nil {
			logsink.Warn("schedule.error", "%s: %s answered a retry time the host cannot read (%v); the next call waits the interval", pluginID, d.Operation, err)
		}
		if w.clamped {
			logsink.Warn("schedule.decision", "%s: %s asked not to be called before %s; the cap is %s from its answer, so it is called after %s", pluginID, d.Operation, out.RetryNotBefore, scheduleRetryCap, w.retryAt.UTC().Format(time.RFC3339))
		}
		hold.set(ctx, w.retryAt, now)
		last = 0
		if w.continues {
			last = out.Progress
		}
		floor = now.Add(w.wait)
		timer.Reset(nextWake(hold, due(), now))
	}
}

func nextWake(h *scheduleHold, callAt, now time.Time) time.Duration {
	d := callAt.Sub(now)
	if d < 0 {
		d = 0
	}
	if _, stored := h.state(); !stored && d > h.a.holdRetry() {
		d = h.a.holdRetry()
	}
	return d
}

func nextWait(out continuation, answered bool, last uint64, now time.Time, interval, gap time.Duration) (w waitChoice, err error) {
	w.wait = interval
	if !answered {
		return w, nil
	}
	w.continues = out.MoreWork && out.Progress > last
	if w.continues {
		w.wait = gap
	}
	if out.RetryNotBefore != "" {
		at, perr := time.Parse(time.RFC3339, out.RetryNotBefore)
		if perr != nil {
			return waitChoice{wait: interval}, perr
		}
		if limit := now.Add(scheduleRetryCap); at.After(limit) {
			at, w.clamped = limit, true
		}
		if at.After(now) {
			w.retryAt = at
			if d := at.Sub(now); d > w.wait {
				w.wait = d
			}
		}
	}
	return w, nil
}

type waitChoice struct {
	wait      time.Duration
	continues bool
	retryAt   time.Time
	clamped   bool
}

const scheduleUndescribedLog = "%s: %s not called: the plugin is a channel adapter whose channel is not described yet; the next interval asks again"

type continuation struct {
	MoreWork       bool   `json:"more_work"`
	Progress       uint64 `json:"progress"`
	RetryNotBefore string `json:"retry_not_before"`
}

func (a *App) scheduledCall(ctx context.Context, pluginID string, d pluginhost.ScheduleDecl) (continuation, bool) {
	ctx, cancel := context.WithTimeout(ctx, scheduleTimeout)
	defer cancel()
	route, routed := a.webhookRoute(ctx, pluginID)
	if !routed {

		logsink.Warn("schedule.refusal", scheduleUndescribedLog, pluginID, d.Operation)
		return continuation{}, false
	}
	args := map[string]any{}
	if route.Acknowledges {
		args["recorded"] = a.heldFor(pluginID)
	}
	res, err := a.toolReg.Execute(pluginhost.Scheduled(ctx), pluginhost.ToolNameFor(pluginID, d.Operation), args)
	if ctx.Err() != nil && err == nil && res.Error == "" {
		return continuation{}, false
	}
	if err == nil && (res.ReasonCode == tools.ReasonSafeSuspended || res.ReasonCode == tools.ReasonOperatorDisabled) {
		return continuation{}, false
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
		return continuation{}, false
	}
	var c continuation
	if strings.TrimSpace(res.Output) != "" {
		if err := json.Unmarshal([]byte(res.Output), &c); err != nil {
			logsink.Warn("schedule.error", "%s: %s answered more work in a shape the host cannot read (%v); the next call waits the interval", pluginID, d.Operation, err)
			return continuation{}, true
		}
	}
	return c, true
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
