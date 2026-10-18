package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
)

// hangingModel is a model that answers nothing until its caller gives up
// (or the test ends), and says when it was asked.
func hangingModel(t *testing.T, a *App) <-chan struct{} {
	t.Helper()
	asked := make(chan struct{}, 1)
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case asked <- struct{}{}:
		default:
		}
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	t.Cleanup(func() { close(release); srv.Close() })
	a.llmSwap.Swap(llm.New(&llm.ClientConfig{Endpoint: srv.URL, Model: "fake", MaxOutputTokens: 4096, Retries: -1, TimeoutSeconds: 600}))
	return asked
}

// A CONTINUATION ENDS WITH THE APP. Its budget was rooted on Background,
// so only the stop hook could reach it, and a turn that registered after
// the hook fired ran its whole budget while Shutdown waited. Rooted on
// the app's lifetime, ending the lifetime ends the turn, whether or not
// the hook ever saw it.
func TestAContinuationEndsWithTheAppsLifetime(t *testing.T) {
	a, _ := dispatchApp(t)
	t.Cleanup(func() { stopApp(t, a) })
	asked := hangingModel(t, a)
	done := make(chan struct{})
	go func() {
		a.continueCappedTurn("ws_lifetime", 1, capDeclared, "")
		close(done)
	}()
	select {
	case <-asked:
	case <-time.After(20 * time.Second):
		t.Fatal("the continuation never asked the model")
	}
	a.bgCancel() // the lifetime ends; the stop hook is not fired
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the continuation outlived the app's lifetime")
	}
	// The leg's request was with the model when the lifetime ended: it had
	// started, and what the model did with the request is not known. "Did
	// not run" is for a leg that never started, and is not said of this one.
	// It was cut short before it said a word, and that is said (row 9.51,
	// extended to a leg cut during its first request): no cut leg is silent.
	if got, want := outboxReply(t, a, "continuation_"), "The next part of ws_lifetime was cut short: the identity was stopping."; got != want {
		t.Errorf("a leg cut with its request at the model is told to the operator as %q, want %q", got, want)
	}
}

// A LEG STILL WAITING FOR THE TURN WHEN THE IDENTITY STOPS NEVER STARTED,
// and the operator's page is told so, with the stop as the cause: the reply
// before it promised it (a session the store does not hold is named by its
// id).
func TestALegWaitingForTheTurnWhenTheIdentityStopsIsToldAsNotRun(t *testing.T) {
	a, _ := dispatchApp(t)
	t.Cleanup(func() { stopApp(t, a) })
	if err := a.acquireTurn(t.Context()); err != nil { // another turn holds the gate
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		a.continueCappedTurn("ws_lifetime", 1, capDeclared, "")
		close(done)
	}()
	a.bgCancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the waiting leg outlived the app's lifetime")
	}
	a.releaseTurn()
	if got, want := outboxReply(t, a, "continuation_"), "The next part of ws_lifetime did not run: the identity was stopping."; got != want {
		t.Errorf("the operator is told %q, want %q", got, want)
	}
}

// SPOKEN WORDS GIVE WAY TO SHUTDOWN. A final was handled on Background:
// parked behind an internal pass, the words kept waiting after Shutdown
// began, reached the gate ahead of Shutdown's own quiesce when the pass
// ended, and ran a whole turn while Shutdown waited behind it. Handled on
// the app's lifetime, they stop waiting when Shutdown begins, and Shutdown
// returns once the pass does.
func TestParkedSpokenWordsGiveWayToShutdown(t *testing.T) {
	a := newVoiceApp(t)
	answered := make(chan struct{}, 1)
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	stubWake(t, func() (string, error) {
		answered <- struct{}{}
		<-release // a turn as long as a model's
		return "", nil
	})
	h, _ := newTrackedSession(a, "vs-stop", true)
	pass := facilityGate{a}
	if !pass.TryBeginTurn() {
		t.Fatal("could not take the turn gate")
	}
	a.voiceEngineEvent(nil, finalEvent(h.id, "are you still there?"), dropEvent)
	time.Sleep(100 * time.Millisecond) // the words park behind the pass

	stopped := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		stopped <- a.Shutdown(ctx)
	}()
	for a.lifetime().Err() == nil {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond) // Shutdown reaches its own quiesce
	pass.EndTurn()
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatalf("shutdown: %v", err)
		}
	case <-answered:
		t.Fatal("the parked words took the turn after Shutdown began")
	case <-time.After(10 * time.Second):
		t.Fatal("Shutdown did not return after the pass ended")
	}
	select {
	case <-answered:
		t.Fatal("the parked words ran a turn after Shutdown")
	default:
	}
}

// NOTHING IS ADMITTED ONCE THE IDENTITY STOPS. The admission takers never
// looked at the lifetime: after Shutdown the gate is free again, so a
// typed message took it and was acked as accepted, and its turn would run
// on a closed runtime. Typed, facility and spoken takers refuse now, while
// Shutdown's own quiesce (a blocking acquire) still takes the turn and
// closes: the control half.
func TestNothingIsAdmittedOnceTheIdentityStops(t *testing.T) {
	a := liveApp(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := a.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown did not quiesce and close: %v", err)
	}
	if a.TryBeginTurn() {
		a.releaseTurn()
		t.Error("a turn began after Shutdown")
	}
	if g := (facilityGate{a}); g.TryBeginTurn() {
		g.EndTurn()
		t.Error("a facility pass began after Shutdown")
	}
	if a.awaitTurnGate(context.Background(), nil) {
		a.releaseTurn()
		t.Error("spoken words took the turn after Shutdown")
	}
	steered, err := a.admitOperatorChat(context.Background(), "hello?")
	if err == nil {
		if !steered {
			a.releaseTurn()
		}
		t.Fatalf("a typed message after Shutdown was admitted (steered=%t)", steered)
	}
	if !errors.Is(err, errStopping) {
		t.Errorf("the refusal does not say the identity is stopping: %v", err)
	}
}

// THE APP'S OWN JOBS END WITH IT. The dashboard's hooks ran their network
// calls on Background inside the app's job owner, so Shutdown, and the
// restart that follows an update, waited out each call's own timeout:
// ten seconds for a model list, a minute for a public name, two for the
// catalog. Rooted on the lifetime, they end when Shutdown begins.
func TestABackgroundModelListEndsWithTheApp(t *testing.T) {
	dir := t.TempDir()
	asked := make(chan struct{}, 1)
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case asked <- struct{}{}:
		default:
		}
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	t.Cleanup(func() { close(release); srv.Close() })
	writeTestProviders(t, dir, providerEntry{Name: "local", APIType: "openai", URL: srv.URL, Local: true, DefaultModel: "m", Default: true})
	a := New(&Config{SourcePath: filepath.Join(dir, "config.json")})
	hooks := a.buildLiveHandler()
	if !a.runBackground(func() { _, _, _ = hooks.DiscoverModels("local", srv.URL, "") }) {
		t.Fatal("the job was refused")
	}
	select {
	case <-asked:
	case <-time.After(10 * time.Second):
		t.Fatal("the model list was never asked")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := a.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown waited on the model list: %v", err)
	}
}

// hungEngine is a speech engine that is alive and answers no control.
type hungEngine struct{ *fakeEngineSession }

func (hungEngine) InterruptFor(ctx context.Context, _, _, _ string) error {
	<-ctx.Done()
	return ctx.Err()
}
func (hungEngine) PlaybackReportFor(ctx context.Context, _ string, _ pluginhost.PlaybackReport) error {
	<-ctx.Done()
	return ctx.Err()
}
func (hungEngine) FinishInputFor(ctx context.Context, _, _ string, _ int64) error {
	<-ctx.Done()
	return ctx.Err()
}
func (hungEngine) CloseFor(ctx context.Context, _, _, _ string) error {
	<-ctx.Done()
	return ctx.Err()
}

// A HUNG ENGINE DOES NOT HOLD SHUTDOWN. The page's interrupt, receipt and
// finish run in the app's job owner and waited on the engine without a
// bound. An engine alive but not answering held the job, Shutdown's join
// waited on it, and the engine is closed only after that join, so Shutdown
// never returned. Each control is bounded now, as the app's own are, and
// an unanswered one reports that it was not answered.
func TestAHungEngineDoesNotHoldShutdown(t *testing.T) {
	a := newVoiceApp(t)
	h, f := newTrackedSession(a, "vs-hung", true)
	h.v = hungEngine{f}
	interrupted := make(chan error, 1)
	if !a.runBackground(func() { interrupted <- h.Interrupt(context.Background()) }) {
		t.Fatal("the job was refused")
	}
	time.Sleep(50 * time.Millisecond) // the interrupt is in flight
	ctx, cancel := context.WithTimeout(context.Background(), pluginhost.SessionControlBound+5*time.Second)
	defer cancel()
	if err := a.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown with an interrupt in flight against a hung engine: %v", err)
	}
	if err := <-interrupted; err == nil {
		t.Fatal("an interrupt the engine never answered reported success")
	}
}
