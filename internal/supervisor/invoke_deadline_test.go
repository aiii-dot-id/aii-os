package supervisor

import (
	"context"
	"strings"
	"testing"
	"time"
)

// .
// .
// .
// .
// .
// .
// .
func TestInvokeHonoursItsDeadlineAgainstAChildThatNeverReads(t *testing.T) {
	sup, err := Start(Spec{PluginID: "deaf.example", Argv: []string{fakechildBin, "deaf"}}, &mockDispatcher{})
	if err != nil {
		t.Fatal(err)
	}
	defer sup.Close()

	// .
	frame := []byte(`{"jsonrpc":"2.0","id":"h","method":"invoke.call","params":{"operation":"x","arguments":{"pad":"` +
		strings.Repeat("p", 512*1024) + `"}}}`)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := sup.Invoke(ctx, frame); err == nil {
		t.Fatal("a child that never reads cannot succeed")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("the deadline was not honoured: %v", elapsed)
	}
}

// .
// .
func TestAQueuedInvokeLeavesOnItsOwnDeadline(t *testing.T) {
	sup, err := Start(Spec{PluginID: "deaf.example", Argv: []string{fakechildBin, "deaf"}}, &mockDispatcher{})
	if err != nil {
		t.Fatal(err)
	}
	defer sup.Close()

	// .
	held := make(chan struct{})
	go func() {
		defer close(held)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_, _ = sup.Invoke(ctx, []byte(`{"jsonrpc":"2.0","id":"h","method":"invoke.call","params":{"operation":"x","arguments":{"pad":"`+
			strings.Repeat("p", 512*1024)+`"}}}`))
	}()
	time.Sleep(80 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := sup.Invoke(ctx, []byte(`{"jsonrpc":"2.0","id":"h","method":"invoke.call","params":{"operation":"y"}}`)); err == nil {
		t.Fatal("the queued call cannot have succeeded")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("a queued call must leave on its own deadline: %v", elapsed)
	}
	<-held
}
