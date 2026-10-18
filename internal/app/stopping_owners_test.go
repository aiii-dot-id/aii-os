package app

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestASignInEndsWithItsApp(t *testing.T) {
	f := newSignInFixture(t, "codex")
	ctx, cancel := context.WithCancel(context.Background())
	f.a.bgCtx, f.a.bgCancel = ctx, cancel
	if _, err := f.a.SignInProvider(f.name); err != nil {
		t.Fatal(err)
	}
	stopApp(t, f.a)
	if v := f.a.signInView("provider:" + f.name); v == nil || v.Status != "expired" {
		t.Fatalf("a pending sign-in was not settled by the time Shutdown returned: %+v", v)
	}
	_, err := f.a.SignInProvider(f.name)
	if !errors.Is(err, errStopping) {
		t.Fatalf("a sign-in after the stop was not refused as stopping: %v", err)
	}
	if v := f.a.signInView("provider:" + f.name); v == nil || v.Status == "pending" {
		t.Fatalf("the refused sign-in was left pending: %+v", v)
	}
}

func TestAListenerStoppedWhileTheAppStopsIsCancelledAtOnce(t *testing.T) {
	a := liveApp(t)
	_, recv := installBlockingAdapter(t, a, "org.example.slow", "matrix", 60)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a.convergeChannels(ctx) // its own context: only the stop can end this receive
	<-recv.entered
	l := a.listening["org.example.slow"]
	a.bgMu.Lock()
	a.stopping = true // Shutdown has begun; no goroutine of the app starts now
	a.bgMu.Unlock()
	a.stopListener(l)
	select {
	case <-l.done:
	case <-time.After(5 * time.Second):
		t.Fatal("a listener stopped while the app stops waited out its adapter's budget")
	}
	if recv.cancelled.Load() != 1 {
		t.Fatalf("the receive ended by cancellation once, got %d", recv.cancelled.Load())
	}
}
