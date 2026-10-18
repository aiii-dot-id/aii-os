package app

import (
	"context"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/tools"
	"slices"
	"sync"
	"testing"
	"time"
)

func TestPluginCommitNotificationsCoalesceWithoutTouchingListeners(t *testing.T) {
	owned := []*pluginhost.ActivePlugin{{ID: "owned by sweep"}}
	a := &App{bgCtx: context.Background(), sweepPoke: make(chan struct{}, 1), listeningSet: owned}
	var wg sync.WaitGroup
	for range 150 {
		wg.Add(1)
		go func() { defer wg.Done(); a.afterPluginChange() }()
	}
	wg.Wait()
	if !slices.Equal(a.listeningSet, owned) {
		t.Fatal("activation touched the sweep-owned fingerprint")
	}
	if len(a.sweepPoke) != 1 {
		t.Fatalf("notifications did not coalesce: %d", len(a.sweepPoke))
	}
}

type heldChannelDescription struct {
	*countingOp
	entered, release chan struct{}
}

func (d *heldChannelDescription) Execute(ctx context.Context, _ map[string]any) (tools.Result, error) {
	close(d.entered)
	select {
	case <-ctx.Done():
		return tools.Result{}, ctx.Err()
	case <-d.release:
		return tools.Result{Output: d.out}, nil
	}
}

func TestPluginCommitDoesNotWaitBehindTheSweepsDescribeCall(t *testing.T) {
	a := liveApp(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a.bgCtx = ctx
	a.sweepPoke = make(chan struct{}, 1)
	desc := &heldChannelDescription{countingOp: &countingOp{name: "owner_describe", out: `{"channel":"mail","receive":"webhook"}`}, entered: make(chan struct{}), release: make(chan struct{})}
	if err := a.toolReg.RegisterHostOp(desc, "org.example.owner"); err != nil {
		t.Fatal(err)
	}
	setPlugins(a, []*pluginhost.ActivePlugin{{ID: "org.example.owner", Channel: &pluginhost.Channel{Describe: desc.name}}})
	swept := make(chan struct{})
	committed := make(chan struct{})
	go func() { a.convergeChannels(ctx); close(swept) }()
	<-desc.entered
	defer func() { close(desc.release); <-swept; <-committed }()
	go func() { a.afterPluginChange(); close(committed) }()
	select {
	case <-committed:
	case <-time.After(2 * time.Second):
		t.Fatal("activation blocked on the sweep's adapter I/O")
	}
	if len(a.sweepPoke) != 1 {
		t.Fatal("busy sweep lost its follow-up notification")
	}
}

func TestPluginCommitSignalReachesTheLiveSweepAndItsListener(t *testing.T) {
	t.Chdir(t.TempDir())
	a := liveApp(t)
	ctx, cancel := context.WithCancel(context.Background())
	a.bgCtx = ctx
	a.sweepPoke = make(chan struct{}, 1)
	_, recv := installBlockingAdapter(t, a, "org.example.sweep", "mail", 1)
	ended := make(chan struct{})
	defer func() {
		cancel()
		<-ended
		recv.releaseAll()
		for _, l := range a.listening {
			<-l.done
		}
		if a.facility != nil {
			a.facility.Close()
		}
	}()
	// With no filesystem/trust notifications, only the activation signal can
	// cause this real sweep loop to converge and start receive.
	go func() { a.runPluginSweep(ctx, nil, nil); close(ended) }()
	a.afterPluginChange()
	select {
	case <-recv.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("commit notification never reached listener convergence")
	}
	// Stop the owner before inspecting its map; no test races the live writer.
	cancel()
	<-ended
	if len(a.listening) != 1 {
		t.Fatalf("listener count: %d", len(a.listening))
	}
}
