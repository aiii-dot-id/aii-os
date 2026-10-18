package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

// dutyOp is a plugin operation whose answer a test sets, and whose calls it
// counts: what a scheduled call is made of, seen from the plugin's side.
type dutyOp struct {
	name string
	mu   sync.Mutex
	res  tools.Result
	err  error
	args []string
	at   []time.Time
}

func (o *dutyOp) Name() string        { return o.name }
func (o *dutyOp) Description() string { return "scheduled operation" }
func (o *dutyOp) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (o *dutyOp) Execute(_ context.Context, args map[string]any) (tools.Result, error) {
	raw, _ := json.Marshal(args)
	o.mu.Lock()
	defer o.mu.Unlock()
	o.args = append(o.args, string(raw))
	o.at = append(o.at, time.Now())
	return o.res, o.err
}
func (o *dutyOp) set(res tools.Result, err error) {
	o.mu.Lock()
	o.res, o.err = res, err
	o.mu.Unlock()
}
func (o *dutyOp) times() []time.Time {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]time.Time{}, o.at...)
}
func (o *dutyOp) calls() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]string{}, o.args...)
}

// scheduled installs a plugin declaring one scheduled operation, and answers
// describe as an adapter's, when it is one.
func scheduled(t *testing.T, a *App, id, describe string) (*dutyOp, pluginhost.ScheduleDecl) {
	t.Helper()
	d := pluginhost.ScheduleDecl{Operation: "mail.sync", EverySeconds: 600}
	op := &dutyOp{name: pluginhost.ToolNameFor(id, d.Operation)}
	if err := a.toolReg.RegisterDynamic(op, id); err != nil {
		t.Fatal(err)
	}
	ap := &pluginhost.ActivePlugin{ID: id, Schedule: []pluginhost.ScheduleDecl{d}}
	if describe != "" {
		desc := "pl_" + id + "_describe"
		if err := a.toolReg.RegisterHostOp(&fakeOp{name: desc, out: describe}, id); err != nil {
			t.Fatal(err)
		}
		ap.Channel = &pluginhost.Channel{PluginID: id, Describe: desc, Send: "pl_" + id + "_send", Receive: "pl_" + id + "_receive"}
	}
	addPlugin(a, ap)
	return op, d
}

// A scheduled call's answer is taken as a webhook's is: its arrivals recorded
// once by id on the adapter's channel; and an adapter that acknowledges is
// told what the host held of the last answer, whichever call — webhook or
// scheduled — it came in.
func TestAScheduledCallTakesItsArrivalsAndTellsWhatWasRecorded(t *testing.T) {
	a := liveApp(t)
	const id = "com.example.mail"
	op, d := scheduled(t, a, id, `{"channel":"mail","receive":"webhook","acknowledges":true}`)
	op.set(tools.Result{Output: arrivals("m1", "m2")}, nil)

	a.scheduledCall(context.Background(), id, d)
	a.scheduledCall(context.Background(), id, d)
	if unseen, _ := a.store.InboundSince(0); len(unseen) != 2 || unseen[0].Channel != "mail" {
		t.Fatalf("two arrivals landed once each on the adapter's channel: %+v", unseen)
	}
	calls := op.calls()
	if len(calls) != 2 {
		t.Fatalf("calls: %v", calls)
	}
	if got, ok := recordedIn(t, calls[0]); !ok || len(got) != 0 {
		t.Errorf("the first call: recorded %v (handed %v)", got, ok)
	}
	if got, _ := recordedIn(t, calls[1]); strings.Join(got, ",") != "m1,m2" {
		t.Errorf("the second call: recorded %v, want [m1 m2]", got)
	}
}

// A call that fails is said — an arrival on the plugin's event route, never a
// wake, once a day per operation — and the operator's own states are not
// failures.
func TestAScheduledCallThatFailsIsSaidOnceADay(t *testing.T) {
	a := liveApp(t)
	const id = "com.example.mail"
	op, d := scheduled(t, a, id, "")
	ctx := context.Background()

	for _, tc := range []struct {
		name string
		res  tools.Result
		err  error
	}{
		{"the operator switched it off", tools.Result{Error: "access denied", ReasonCode: tools.ReasonOperatorDisabled}, nil},
		{"SAFE suspends it", tools.Result{Error: "SAFE: x", ReasonCode: tools.ReasonSafeSuspended}, nil},
	} {
		op.set(tc.res, tc.err)
		a.scheduledCall(ctx, id, d)
		if unseen, _ := a.store.InboundSince(0); len(unseen) != 0 {
			t.Fatalf("%s: said %+v", tc.name, unseen)
		}
	}

	// A contact the operator granted wake on that very route: a failure is
	// still an event and never a wake.
	contact(t, a, Contact{Name: "the plugin", Channel: "hook:" + id, Address: id, Wake: true})
	var woken sync.Mutex
	wakes := 0
	a.wakeParticipantFn = func(context.Context, string) (string, error) { woken.Lock(); wakes++; woken.Unlock(); return "", nil }
	op.set(tools.Result{Error: "the watch could not be renewed: 403 insufficient scope"}, nil)
	a.scheduledCall(ctx, id, d)
	time.Sleep(200 * time.Millisecond)
	woken.Lock()
	if wakes != 0 {
		t.Errorf("a failed scheduled call woke the identity %d times", wakes)
	}
	woken.Unlock()
	unseen, _ := a.store.InboundSince(0)
	if len(unseen) != 1 || unseen[0].Channel != "hook:"+id || unseen[0].Address != id ||
		!strings.Contains(unseen[0].Body, "mail.sync failed: the watch could not be renewed: 403 insufficient scope") {
		t.Fatalf("a failure is one event arrival naming the operation and why: %+v", unseen)
	}
	a.scheduledCall(ctx, id, d)
	if unseen, _ = a.store.InboundSince(0); len(unseen) != 1 {
		t.Fatalf("the same failure the same day is not said twice: %d", len(unseen))
	}
	op.set(tools.Result{}, errors.New("the worker is gone"))
	a.scheduledCall(ctx, id, d)
	if unseen, _ = a.store.InboundSince(0); len(unseen) != 1 {
		t.Errorf("a second failure the same day, another cause, is still one line a day: %d", len(unseen))
	}
	// An answer that cannot be taken is a failure too.
	op.set(tools.Result{Output: "<html>"}, nil)
	a.scheduledCall(ctx, id, pluginhost.ScheduleDecl{Operation: d.Operation, EverySeconds: d.EverySeconds})
	if unseen, _ = a.store.InboundSince(0); len(unseen) != 1 {
		t.Errorf("an answer that is not a result: %d rows", len(unseen))
	}
}

// The sweep converges one runner per scheduled operation: called once after
// the first delay and then at the interval; removed, it stops; unchanged, it
// is left alone.
func TestTheSweepRunsEachScheduledOperationAtItsIntervalAndStopsIt(t *testing.T) {
	a := liveApp(t)
	const id = "com.example.mail"
	a.scheduleFirst = 10 * time.Millisecond
	a.scheduleEvery = func(pluginhost.ScheduleDecl) time.Duration { return 150 * time.Millisecond }
	op, _ := scheduled(t, a, id, "")
	op.set(tools.Result{Output: "{}"}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	a.convergeSchedules(ctx)
	a.convergeSchedules(ctx) // unchanged: left alone, not doubled
	if len(a.scheduling) != 1 {
		t.Fatalf("runners: %d", len(a.scheduling))
	}
	// Load only lengthens what is measured here, so the test asserts the
	// least: three calls come, and each waits the interval after the last.
	deadline := time.Now().Add(20 * time.Second)
	for len(op.times()) < 3 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	at := op.times()
	if len(at) < 3 {
		t.Fatalf("the operation was called %d times in 20 s", len(at))
	}
	for i := 1; i < 3; i++ {
		if gap := at[i].Sub(at[i-1]); gap < 100*time.Millisecond {
			t.Errorf("call %d came %s after the last, at a 150 ms interval — a runner that does not wait is a poll", i+1, gap)
		}
	}

	var runner *scheduleRunner
	for _, r := range a.scheduling {
		runner = r
	}
	a.pluginMu.Lock()
	a.plugins = nil
	a.pluginMu.Unlock()
	a.convergeSchedules(ctx)
	if len(a.scheduling) != 0 {
		t.Fatalf("a plugin that went away keeps %d runners", len(a.scheduling))
	}
	select {
	case <-runner.done:
	case <-time.After(20 * time.Second):
		t.Fatal("a stopped runner did not exit")
	}
	time.Sleep(200 * time.Millisecond) // a call already begun may finish
	n := len(op.calls())
	time.Sleep(600 * time.Millisecond)
	if len(op.calls()) != n {
		t.Errorf("a stopped runner still calls: %d then %d", n, len(op.calls()))
	}
}

func TestAScheduleKeyNamesWhatChangesIt(t *testing.T) {
	a := pluginhost.ScheduleDecl{Operation: "x.y", EverySeconds: 600}
	b := pluginhost.ScheduleDecl{Operation: "x.y", EverySeconds: 1200}
	if scheduleKey("p", a) == scheduleKey("p", b) || scheduleKey("p", a) == scheduleKey("q", a) {
		t.Fatal(fmt.Sprint("a changed interval or plugin must be a new runner: ", scheduleKey("p", a), scheduleKey("p", b)))
	}
}
