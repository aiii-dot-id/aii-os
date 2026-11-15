package app

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

// A SCHEDULED OPERATION'S WORK GOES ON WHILE IT ADVANCES (H2, the operator
// 2026-11-10: "Approve bounded continuation with a 5-second gap; target M1").
// A call that answers more work, with a progress count above the last, is
// called again after the gap, counted from the end of the call; anything else
// waits the declared interval. Nothing about a chain is stored.

func TestNextWaitFollowsTheFourRules(t *testing.T) {
	now := time.Date(2026, 11, 10, 21, 0, 0, 0, time.UTC)
	const interval, gap = 600 * time.Second, 5 * time.Second
	at := func(d time.Duration) string { return now.Add(d).Format(time.RFC3339) }
	for _, c := range []struct {
		name      string
		out       continuation
		answered  bool
		last      uint64
		want      time.Duration
		continues bool
		bad       bool
	}{
		{"more work that advanced continues at the gap", continuation{MoreWork: true, Progress: 3}, true, 2, gap, true, false},
		{"more work that did not advance ends the chain", continuation{MoreWork: true, Progress: 2}, true, 2, interval, false, false},
		{"a count that went back ends the chain", continuation{MoreWork: true, Progress: 1}, true, 2, interval, false, false},
		{"work that is done waits the interval", continuation{Progress: 9}, true, 2, interval, false, false},
		{"a first answer of zero continues nothing", continuation{MoreWork: true, Progress: 0}, true, 0, interval, false, false},
		{"a call with no answer taken continues nothing", continuation{MoreWork: true, Progress: 9}, false, 2, interval, false, false},
		{"a later retry time is honoured", continuation{MoreWork: true, Progress: 3, RetryNotBefore: at(40 * time.Second)}, true, 2, 40 * time.Second, true, false},
		{"an earlier retry time still waits the gap", continuation{MoreWork: true, Progress: 3, RetryNotBefore: at(time.Second)}, true, 2, gap, true, false},
		{"a retry time past the interval is honoured, not cut to it", continuation{MoreWork: true, Progress: 3, RetryNotBefore: at(time.Hour)}, true, 2, time.Hour, true, false},
		{"a chain that ends keeps the retry time past the interval", continuation{MoreWork: true, Progress: 2, RetryNotBefore: at(time.Hour)}, true, 2, time.Hour, false, false},
		{"a retry time before the interval does not shorten it", continuation{Progress: 9, RetryNotBefore: at(time.Minute)}, true, 2, interval, false, false},
		{"a retry time the host cannot read continues nothing, and says so", continuation{MoreWork: true, Progress: 3, RetryNotBefore: "soon"}, true, 2, interval, false, true},
	} {
		w, err := nextWait(c.out, c.answered, c.last, now, interval, gap)
		if w.wait != c.want || w.continues != c.continues || (err != nil) != c.bad {
			t.Errorf("%s: waited %s, continues %v (err %v); want %s, %v (an error: %v)", c.name, w.wait, w.continues, err, c.want, c.continues, c.bad)
		}
	}

	// THE CAP: a retry time past a week from the answer's
	// receipt is cut to that, and the row keeps the cut absolute time.
	week := scheduleRetryCap
	w, err := nextWait(continuation{RetryNotBefore: at(30 * 24 * time.Hour)}, true, 0, now, interval, gap)
	if err != nil || w.wait != week || !w.clamped || !w.retryAt.Equal(now.Add(week)) {
		t.Fatalf("a month asked for: wait %s, clamped %v, retryAt %s (%v)", w.wait, w.clamped, w.retryAt, err)
	}
	// A retry time shorter than the interval is still kept as the row's (D4-R1).
	w, err = nextWait(continuation{RetryNotBefore: at(5 * time.Minute)}, true, 0, now, interval, gap)
	if err != nil || w.wait != interval || w.clamped || !w.retryAt.Equal(now.Add(5*time.Minute)) {
		t.Fatalf("five minutes inside a ten-minute interval: wait %s, retryAt %s (%v)", w.wait, w.retryAt, err)
	}
}

// chainOp answers each call from answer, by its index, after taking hold, and
// keeps when each call began and ended.
type chainOp struct {
	name   string
	hold   time.Duration
	answer func(i int) (tools.Result, error)
	mu     sync.Mutex
	spans  [][2]time.Time
}

func (o *chainOp) Name() string        { return o.name }
func (o *chainOp) Description() string { return "scheduled operation" }
func (o *chainOp) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (o *chainOp) Execute(context.Context, map[string]any) (tools.Result, error) {
	began := time.Now()
	o.mu.Lock()
	i := len(o.spans)
	o.spans = append(o.spans, [2]time.Time{began})
	o.mu.Unlock()
	time.Sleep(o.hold)
	res, err := o.answer(i)
	o.mu.Lock()
	o.spans[i][1] = time.Now()
	o.mu.Unlock()
	return res, err
}
func (o *chainOp) calls() [][2]time.Time {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([][2]time.Time(nil), o.spans...)
}

func answerJSON(v any) tools.Result {
	raw, _ := json.Marshal(v)
	return tools.Result{Output: string(raw)}
}

// chain runs one scheduled operation whose answers op gives, with a 30 s
// interval no test waits through and the gap given, until stop is cancelled.
func chain(t *testing.T, gap time.Duration, op *chainOp) (stop context.CancelFunc, a *App) {
	t.Helper()
	a = liveApp(t)
	return startChain(t, a, gap, op), a
}

// startChain is chain on an App the test already holds (its store, its rows).
func startChain(t *testing.T, a *App, gap time.Duration, op *chainOp) context.CancelFunc {
	t.Helper()
	const id = "com.example.scan"
	a.scheduleFirst = 10 * time.Millisecond
	a.scheduleEvery = func(pluginhost.ScheduleDecl) time.Duration { return 30 * time.Second }
	a.scheduleGap = gap
	d := pluginhost.ScheduleDecl{Operation: "scan.advance", EverySeconds: 600}
	op.name = pluginhost.ToolNameFor(id, d.Operation)
	if err := a.toolReg.RegisterDynamic(op, id); err != nil {
		t.Fatal(err)
	}
	a.pluginMu.Lock()
	a.plugins = nil
	a.pluginMu.Unlock()
	addPlugin(a, &pluginhost.ActivePlugin{ID: id, Schedule: []pluginhost.ScheduleDecl{d}})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	a.convergeSchedules(ctx)
	return cancel
}

// quiet waits until the operation has been called n times, then a while more,
// and returns what it saw.
func quiet(t *testing.T, op *chainOp, n int) [][2]time.Time {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for len(op.calls()) < n && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(600 * time.Millisecond) // ten gaps: a chain that did not end calls again in this
	return op.calls()
}

func TestAnOperationThatAdvancesIsCalledAgainAfterTheGapUntilItsWorkIsDone(t *testing.T) {
	const gap, hold = 60 * time.Millisecond, 40 * time.Millisecond
	op := &chainOp{hold: hold, answer: func(i int) (tools.Result, error) {
		return answerJSON(map[string]any{"more_work": i < 3, "progress": i + 1}), nil
	}}
	chain(t, gap, op)
	calls := quiet(t, op, 4)
	if len(calls) != 4 {
		t.Fatalf("three answers of more work that advanced, then done: %d calls, want 4", len(calls))
	}
	for i := 1; i < len(calls); i++ {
		// The gap is counted from the end of the call before: the slot is the
		// identity's for that long.
		if since := calls[i][0].Sub(calls[i-1][1]); since < gap-10*time.Millisecond {
			t.Errorf("call %d began %s after the last ended; the gap is %s, from the end of the call", i+1, since, gap)
		}
		if calls[i][0].Before(calls[i-1][1]) {
			t.Errorf("call %d overlapped the one before", i+1)
		}
	}
}

func TestAnOperationThatDoesNotAdvanceEndsItsChain(t *testing.T) {
	op := &chainOp{answer: func(int) (tools.Result, error) {
		return answerJSON(map[string]any{"more_work": true, "progress": 7}), nil
	}}
	chain(t, 30*time.Millisecond, op)
	if calls := quiet(t, op, 2); len(calls) != 2 {
		t.Fatalf("a count that stood still after its first call: %d calls, want 2 (the first advanced from nothing; the second did not)", len(calls))
	}
}

func TestAnOperationThatFailsOrIsRefusedContinuesNothing(t *testing.T) {
	for name, answer := range map[string]func(int) (tools.Result, error){
		"failed": func(int) (tools.Result, error) { return tools.Result{}, errors.New("guest trapped") },
		"refused": func(int) (tools.Result, error) {
			r := answerJSON(map[string]any{"more_work": true, "progress": 1})
			r.Error = "the operation refused"
			return r, nil
		},
		"suspended": func(int) (tools.Result, error) {
			r := answerJSON(map[string]any{"more_work": true, "progress": 1})
			r.ReasonCode = tools.ReasonSafeSuspended
			return r, nil
		},
		"unreadable continuation": func(int) (tools.Result, error) {
			return answerJSON(map[string]any{"more_work": true, "progress": "halfway"}), nil
		},
		"zero progress": func(int) (tools.Result, error) {
			return answerJSON(map[string]any{"more_work": true, "progress": 0}), nil
		},
		"negative progress": func(int) (tools.Result, error) {
			return answerJSON(map[string]any{"more_work": true, "progress": -3}), nil
		},
		"progress as an object": func(int) (tools.Result, error) {
			return answerJSON(map[string]any{"more_work": true, "progress": map[string]any{"done": 1}}), nil
		},
		"unreadable retry": func(int) (tools.Result, error) {
			return answerJSON(map[string]any{"more_work": true, "progress": 1, "retry_not_before": "soon"}), nil
		},
	} {
		t.Run(name, func(t *testing.T) {
			op := &chainOp{answer: answer}
			chain(t, 30*time.Millisecond, op)
			if calls := quiet(t, op, 1); len(calls) != 1 {
				t.Fatalf("%d calls, want 1: only an answer taken in whole continues the work", len(calls))
			}
		})
	}
}

func TestAChainStopsWhenItsRunnerIsStopped(t *testing.T) {
	op := &chainOp{answer: func(i int) (tools.Result, error) {
		return answerJSON(map[string]any{"more_work": true, "progress": i + 1}), nil
	}}
	stop, a := chain(t, 30*time.Millisecond, op)
	deadline := time.Now().Add(20 * time.Second)
	for len(op.calls()) < 3 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if len(op.calls()) < 3 {
		t.Fatalf("a chain that advances is called again: %d calls in 20 s", len(op.calls()))
	}
	var runner *scheduleRunner
	for _, r := range a.scheduling {
		runner = r
	}
	stop()
	select {
	case <-runner.done:
	case <-time.After(20 * time.Second):
		t.Fatal("a stopped runner did not exit in the middle of a chain")
	}
	n := len(op.calls())
	time.Sleep(300 * time.Millisecond)
	if got := len(op.calls()); got != n {
		t.Fatalf("a stopped chain was called again: %d then %d", n, got)
	}
}

// A webhook's answer is decoded as it was before H2: a member named like the
// continuation's, in a shape the continuation does not have, is ignored there,
// as every unknown member always was.
func TestAWebhookAnswerIgnoresTheContinuationsNames(t *testing.T) {
	a := liveApp(t)
	const id = "com.example.hook"
	route := channelRoute{Channel: "hook:" + id, Plugin: id, Hook: true}
	if _, err := a.takeAnswer(route, id, `{"progress":"halfway","more_work":"yes","retry_not_before":7}`); err != nil {
		t.Fatalf("A WEBHOOK ANSWER WAS REFUSED FOR A MEMBER ONLY A SCHEDULED ANSWER READS: %v", err)
	}
}

// A runner waiting out a future retry deadline stops when it is stopped: the
// wait is the loop's own timer, and cancellation or unload ends it.
func TestARunnerWaitingOnARetryDeadlineStopsWhenItIsStopped(t *testing.T) {
	op := &chainOp{answer: func(int) (tools.Result, error) {
		return answerJSON(map[string]any{"more_work": true, "progress": 1, "retry_not_before": time.Now().Add(time.Hour).UTC().Format(time.RFC3339)}), nil
	}}
	stop, a := chain(t, 30*time.Millisecond, op)
	deadline := time.Now().Add(20 * time.Second)
	for len(op.calls()) < 1 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if len(op.calls()) != 1 {
		t.Fatalf("the first call: %d calls", len(op.calls()))
	}
	var runner *scheduleRunner
	for _, r := range a.scheduling {
		runner = r
	}
	stop()
	select {
	case <-runner.done:
	case <-time.After(20 * time.Second):
		t.Fatal("a runner waiting on a retry deadline did not stop")
	}
	if n := len(op.calls()); n != 1 {
		t.Fatalf("a runner stopped during its retry wait called again: %d calls", n)
	}
}
