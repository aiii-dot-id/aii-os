package pluginhost

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/broker"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

// Hold the existing invoker seam after dispatch checks, while the operation
// owns its slot but before the WASM module takes its own lock. Closing the
// module here used to destroy a call the predecessor had already admitted.
type retirementHeldInvoker struct {
	inner   invoker
	entered chan struct{}
	proceed chan struct{}
}

func (p *retirementHeldInvoker) Invoke(ctx context.Context, frame []byte) ([]byte, error) {
	close(p.entered)
	select {
	case <-p.proceed:
		return p.inner.Invoke(ctx, frame)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestRetirementPreservesAnAdmittedCallUntilItLeaves(t *testing.T) {
	for _, quiet := range []bool{false, true} {
		for _, cancelled := range []bool{false, true} {
			name := map[bool]string{false: "deactivate", true: "quiet"}[quiet] + "/" + map[bool]string{false: "complete", true: "cancel"}[cancelled]
			t.Run(name, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
				defer cancel()
				reg := newRegistry(t)
				old, err := Activate(ctx, buildPkg(t, "org.example.retirement", "0.1.0", []string{"ping"}, "responder.wasm"), reg, nil)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = old.Deactivate(context.Background()) })
				next, err := ActivateShadow(ctx, buildPkg(t, "org.example.retirement", "0.2.0", []string{"ping"}, "responder.wasm"), reg, nil)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = next.Deactivate(context.Background()) })
				const tool = "pl_org_example_retirement_ping"
				route, ok := reg.Get(tool)
				if !ok {
					t.Fatal("missing old route")
				}
				operation := route.(*operationTool)
				probe := &retirementHeldInvoker{inner: operation.inv, entered: make(chan struct{}), proceed: make(chan struct{})}
				operation.inv = probe
				artifact := t.TempDir()
				old.artifactDir = artifact
				release := sync.OnceFunc(func() { close(probe.proceed) })
				callCtx, cancelCall := context.WithCancel(ctx)
				defer cancelCall()
				done := make(chan struct{})
				var result tools.Result
				var callErr error
				go func() {
					defer close(done)
					result, callErr = reg.Execute(callCtx, tool, nil)
				}()
				defer func() {
					release()
					select {
					case <-done:
					case <-ctx.Done():
						t.Error("invocation did not leave", ctx.Err())
					}
				}()
				select {
				case <-probe.entered:
				case <-ctx.Done():
					t.Fatal("call never reached invoker", ctx.Err())
				}
				// ONE WRITER AT A TIME: the successor takes nothing while the
				// predecessor's admitted call runs (HandOver); the switch is
				// refused and the predecessor keeps every name.
				if err := next.Redirect(old); !errors.Is(err, ErrHandOverBusy) || next.admitted.Load() || old.superseded.Load() {
					t.Fatalf("a switch beside an admitted call: %v", err)
				}
				closeOld := old.Deactivate
				if quiet {
					closeOld = old.CloseQuiet
				}
				if err := closeOld(ctx); !errors.Is(err, ErrRetirementBusy) {
					t.Errorf("busy retirement must retain custody: %v", err)
				}
				if _, err := os.Stat(artifact); err != nil {
					t.Error("busy retirement released its artifact", err)
				}
				if cancelled {
					cancelCall()
				} else {
					release()
				}
				select {
				case <-done:
				case <-ctx.Done():
					t.Fatal("call did not finish", ctx.Err())
				}
				if cancelled {
					if !errors.Is(callErr, context.Canceled) {
						t.Errorf("cancellation was not preserved: %v", callErr)
					}
				} else if callErr != nil || result.Error != "" || !strings.Contains(result.Output, `"echoed":true`) {
					t.Errorf("admitted call lost its predecessor: %+v %v", result, callErr)
				}
				// The call has left: the switch, then the retirement.
				if err := next.Redirect(old); err != nil {
					t.Fatal(err)
				}
				for i := 0; i < 2; i++ {
					if err := closeOld(ctx); err != nil {
						t.Fatal("idle retirement did not finish idempotently", err)
					}
				}
				if _, err := os.Stat(artifact); !os.IsNotExist(err) {
					t.Error("idle retirement retained its artifact", err)
				}
				if r, err := reg.Execute(ctx, tool, nil); err != nil || r.Error != "" || !strings.Contains(r.Output, `"echoed":true`) {
					t.Errorf("predecessor retirement damaged successor: %+v %v", r, err)
				}
			})
		}
	}
}

// The side-by-side update seam (S12): a candidate stands up as a shadow
// with NOTHING registered, crosses the health gate, then an atomic
// redirect points the live name at it and hands the predecessor's names
// away — so the predecessor's later quiet close leaves the name alone.
func TestActivateShadowRegistersNothingThenRedirects(t *testing.T) {
	reg := newRegistry(t)
	oldAp, err := Activate(context.Background(), buildPkg(t, "org.example.up", "0.1.0", []string{"ping"}, "responder.wasm"), reg, nil)
	if err != nil {
		t.Fatalf("Activate the running release: %v", err)
	}
	const name = "pl_org_example_up_ping"
	if _, ok := reg.Get(name); !ok {
		t.Fatalf("the running release registered %s", name)
	}

	newAp, err := ActivateShadow(context.Background(), buildPkg(t, "org.example.up", "0.2.0", []string{"ping"}, "responder.wasm"), reg, nil)
	if err != nil {
		t.Fatalf("ActivateShadow: %v", err)
	}
	if len(newAp.ToolNames) != 0 {
		t.Fatalf("a shadow registers no names, got %v", newAp.ToolNames)
	}
	if got, _ := reg.Get(name); got == nil {
		t.Fatal("the running release still owns the name during the shadow")
	}
	if err := newAp.Health(context.Background()); err != nil {
		t.Fatalf("the candidate is healthy: %v", err)
	}

	if err := newAp.Redirect(oldAp); err != nil {
		t.Fatalf("redirect: %v", err)
	}
	if len(newAp.ToolNames) != 1 || newAp.ToolNames[0] != name {
		t.Fatalf("the candidate owns the name after redirect: %v", newAp.ToolNames)
	}
	if len(oldAp.ToolNames) != 0 {
		t.Fatalf("the predecessor released its names: %v", oldAp.ToolNames)
	}
	if !oldAp.WaitIdle(2 * time.Second) {
		t.Fatal("an idle predecessor drains at once")
	}
	if err := oldAp.CloseQuiet(context.Background()); err != nil {
		t.Fatalf("CloseQuiet: %v", err)
	}
	if _, ok := reg.Get(name); !ok {
		t.Fatal("the name survives the predecessor's quiet close — it is the candidate's now")
	}
	t.Cleanup(func() { _ = newAp.Deactivate(context.Background()) })
}

// A candidate that exposes no operation is not a successor: the health
// gate refuses it, so an update built on it never redirects.
func TestHealthRefusesAnOperationlessCandidate(t *testing.T) {
	reg := newRegistry(t)
	ap, err := ActivateShadow(context.Background(), buildPkg(t, "org.example.empty", "0.1.0", []string{"ping"}, "responder.wasm"), reg, nil)
	if err != nil {
		t.Fatalf("ActivateShadow: %v", err)
	}
	ap.pending = nil // a candidate with no operations
	if err := ap.Health(context.Background()); err == nil {
		t.Fatal("health must refuse a candidate that exposes no operations")
	}
	t.Cleanup(func() { _ = ap.CloseQuiet(context.Background()) })
}

func TestASupersededReleaseCannotPublishOrWithdraw(t *testing.T) {
	reg := newRegistry(t)
	st := newBrokerStore(t)
	h, err := broker.New(broker.Config{Store: st, Grants: map[string]broker.Grant{"org.example.up": {Tools: true}}})
	if err != nil {
		t.Fatal(err)
	}
	oldAp, err := Activate(context.Background(), buildPkg(t, "org.example.up", "0.1.0", []string{"ping"}, "responder.wasm"), reg, &Options{Broker: h})
	if err != nil {
		t.Fatal(err)
	}
	// It publishes freely while it is the release.
	if _, err := oldAp.PublishTool(broker.PublishedTool{Name: "dyn.one", Summary: "s", Effects: "read.internal"}); err != nil {
		t.Fatalf("the running release publishes: %v", err)
	}

	newAp, err := ActivateShadow(context.Background(), buildPkg(t, "org.example.up", "0.2.0", []string{"ping"}, "responder.wasm"), reg, &Options{Broker: h})
	if err != nil {
		t.Fatal(err)
	}
	if err := newAp.Redirect(oldAp); err != nil {
		t.Fatalf("redirect: %v", err)
	}

	if _, err := oldAp.PublishTool(broker.PublishedTool{Name: "dyn.two", Summary: "s", Effects: "read.internal"}); err == nil {
		t.Fatal("a superseded release must not admit new work")
	} else if !strings.Contains(err.Error(), "superseded") {
		t.Fatalf("and it must say why: %v", err)
	}
	if err := oldAp.WithdrawTool("dyn.one"); err == nil {
		t.Fatal("a superseded release must not withdraw, the name may be the successor's now")
	}
	// The successor is unaffected and publishes as the release it is.
	if _, err := newAp.PublishTool(broker.PublishedTool{Name: "dyn.three", Summary: "s", Effects: "read.internal"}); err != nil {
		t.Fatalf("the successor publishes: %v", err)
	}
}

func TestAnOverlappingPublishOrWithdrawCannotSlipThroughRetirement(t *testing.T) {
	reg := newRegistry(t)
	st := newBrokerStore(t)
	h, err := broker.New(broker.Config{Store: st, Grants: map[string]broker.Grant{"org.example.up": {Tools: true}}})
	if err != nil {
		t.Fatal(err)
	}
	oldAp, err := Activate(context.Background(), buildPkg(t, "org.example.up", "0.1.0", []string{"ping"}, "responder.wasm"), reg, &Options{Broker: h})
	if err != nil {
		t.Fatal(err)
	}
	newAp, err := ActivateShadow(context.Background(), buildPkg(t, "org.example.up", "0.2.0", []string{"ping"}, "responder.wasm"), reg, &Options{Broker: h})
	if err != nil {
		t.Fatal(err)
	}

	// HOLD THE REDIRECT IN THE WINDOW ITSELF — after the registry has
	// changed hands, before the predecessor is retired — and let a
	// publish run there. With the lock taken only afterwards, that
	// publish finds a free mutex and a false flag and registers a live
	// tool on a release that no longer owns anything.
	swapped, release := make(chan struct{}), make(chan struct{})
	prev := redirectSwapped
	var once sync.Once
	redirectSwapped = func() { once.Do(func() { close(swapped); <-release }) }
	t.Cleanup(func() { redirectSwapped = prev })

	done := make(chan error, 1)
	go func() { done <- newAp.Redirect(oldAp) }()
	<-swapped
	pubDone := make(chan error, 1)
	go func() {
		_, err := oldAp.PublishTool(broker.PublishedTool{Name: "dyn.sneak", Summary: "s", Effects: "read.internal"})
		pubDone <- err
	}()
	// Give the publish every chance to get in before retirement lands.
	time.Sleep(150 * time.Millisecond)
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("redirect: %v", err)
	}
	pubErr := <-pubDone

	if _, ok := reg.Get(ToolNameFor("org.example.up", "dyn.sneak")); ok {
		t.Fatal("a publish inside the swap window must not leave a live tool on the retired release")
	}
	if pubErr == nil {
		t.Fatal("and it must be told it was refused, not answered with success")
	}
	if _, ok := reg.Get("pl_org_example_up_ping"); !ok {
		t.Fatal("the successor's declared tool is still the registry's")
	}
}

func TestConcurrentTeardownsAreSerialized(t *testing.T) {
	reg := newRegistry(t)
	ap, err := Activate(context.Background(), buildPkg(t, "org.example.teardown", "0.1.0", []string{"ping"}, "responder.wasm"), reg, nil)
	if err != nil {
		t.Fatalf("Activate: %v", err)
	}
	const name = "pl_org_example_teardown_ping"
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _ = ap.Deactivate(context.Background()) }()
	go func() { defer wg.Done(); _ = ap.CloseQuiet(context.Background()) }()
	wg.Wait()
	if _, ok := reg.Get(name); ok {
		t.Fatal("Deactivate deregisters the names, whichever teardown ran first")
	}
}
