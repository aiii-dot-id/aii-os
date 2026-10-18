package app

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/tools"
)

// stallingDescribe is an adapter still starting, then slow: its first
// describe fails, and every later one waits until released.
type stallingDescribe struct {
	name    string
	calls   atomic.Int32
	release chan struct{}
}

func (d *stallingDescribe) Name() string        { return d.name }
func (d *stallingDescribe) Description() string { return "a describe that fails, then stalls" }
func (d *stallingDescribe) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (d *stallingDescribe) Execute(ctx context.Context, _ map[string]any) (tools.Result, error) {
	if d.calls.Add(1) == 1 {
		return tools.Result{Error: "still starting"}, nil
	}
	select {
	case <-d.release:
		return tools.Result{Output: `{"channel":"sms","receive":"webhook"}`}, nil
	case <-ctx.Done():
		return tools.Result{}, ctx.Err()
	}
}

// A READER DOES NOT WAIT ON AN ADAPTER'S DESCRIBE. The route cache held its
// lock across every adapter's describe, and a set with an adapter that had
// not answered was described again, whole, by every webhook and every
// outbox pass: one adapter still starting made each of them wait out its
// describe. A reader now reads the last round and wakes the sweep, which
// asks the adapter again; describe runs outside the lock.
func TestAReaderDoesNotWaitOnAnAdaptersDescribe(t *testing.T) {
	a := liveApp(t)
	sms := &stallingDescribe{name: "pl_org.example.sms_describe", release: make(chan struct{})}
	for _, op := range []struct {
		plugin string
		op     tools.Tool
	}{
		{"org.example.mail", &fakeOp{name: "pl_org.example.mail_describe", out: `{"channel":"mail","receive":"webhook"}`}},
		{"org.example.mail", &fakeOp{name: "pl_org.example.mail_send", out: `{"receipt":"ok"}`}},
		{"org.example.mail", &fakeOp{name: "pl_org.example.mail_receive", out: "[]"}},
		{"org.example.sms", sms},
		{"org.example.sms", &fakeOp{name: "pl_org.example.sms_send", out: `{"receipt":"ok"}`}},
		{"org.example.sms", &fakeOp{name: "pl_org.example.sms_receive", out: "[]"}},
	} {
		if err := a.toolReg.RegisterHostOp(op.op, op.plugin); err != nil {
			t.Fatal(err)
		}
	}
	var once sync.Once
	release := func() { once.Do(func() { close(sms.release) }) }
	t.Cleanup(release)
	addPlugin(a, activeChannel("org.example.mail", "pl_org.example.mail_send", "pl_org.example.mail_receive", "pl_org.example.mail_describe"))
	addPlugin(a, activeChannel("org.example.sms", "pl_org.example.sms_send", "pl_org.example.sms_receive", sms.name))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a.sweepPoke = make(chan struct{}, 1)

	a.convergeChannels(ctx) // the sweep's round: mail answers, sms is still starting
	read := func(what string) map[string]channelRoute {
		t.Helper()
		got := make(chan map[string]channelRoute, 1)
		go func() { got <- a.channelRoutes(context.Background()) }()
		select {
		case r := <-got:
			return r
		case <-time.After(2 * time.Second):
			t.Fatalf("a reader waited on an adapter's describe (%s)", what)
			return nil
		}
	}
	if r := read("after a round that sms did not answer"); r["mail"].Plugin != "org.example.mail" {
		t.Fatalf("the reader lost what the round answered: %+v", r)
	}
	if len(a.sweepPoke) != 1 {
		t.Fatal("the reader did not ask the sweep to ask sms again")
	}

	swept := make(chan struct{})
	go func() { a.convergeChannels(ctx); close(swept) }()
	for sms.calls.Load() < 2 {
		time.Sleep(time.Millisecond)
	}
	if r := read("while the sweep asks sms again"); r["mail"].Plugin != "org.example.mail" {
		t.Fatalf("the reader lost what the last round answered: %+v", r)
	}
	release()
	<-swept
	if r := read("after sms answered"); r["sms"].Plugin != "org.example.sms" || r["mail"].Plugin != "org.example.mail" {
		t.Fatalf("the sweep's second round is not what readers read: %+v", r)
	}
}
