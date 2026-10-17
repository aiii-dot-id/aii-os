package app

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/audio"
	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
)

func TestAVoiceOpenWaitsForACloseInFlight(t *testing.T) {
	type held struct {
		a       *App
		h       *voiceHandle
		ended   chan struct{}
		reached func() int
		f       *fakeEngineSession
	}
	rig := func(t *testing.T, wait int) held {
		t.Helper()
		a := speechRouteApp(t, "", "id.engine.first")
		a.cfg.Speech.OpenWaitSeconds = wait
		engine, _ := runningEngine(t, a, "id.engine.first")
		var mu sync.Mutex
		n := 0
		prev := voiceEngineOpen
		t.Cleanup(func() { voiceEngineOpen = prev })
		voiceEngineOpen = func(_ *pluginhost.VoiceSession, _ context.Context, _ string, _ *audio.Binding, _ map[string]any) error {
			mu.Lock()
			defer mu.Unlock()
			n++
			return nil
		}
		plane := audio.NewPlane()
		if err := plane.Register(&audio.Endpoint{ID: "held-spk", Sink: audio.NewCaptureSink(mono16k)}); err != nil {
			t.Fatal(err)
		}
		b, err := plane.BindOutput("vs-held", "held-spk", true)
		if err != nil {
			t.Fatal(err)
		}
		f := &fakeEngineSession{}
		ended := make(chan struct{})
		h := &voiceHandle{id: "vs-held", v: f, b: b, done: ended, engine: engine}
		a.voiceSessions.Store(h.id, h)
		a.voiceModes.Store(h.id, false)
		return held{a, h, ended, func() int { mu.Lock(); defer mu.Unlock(); return n }, f}
	}
	// Every open here is one that takes nothing by itself: no microphone and
	// no gesture of the operator's, so no session is asked to yield to it.
	openOn := func(ctx context.Context, a *App, speaker string) chan error {
		done := make(chan error, 1)
		go func() {
			_, err := a.OpenVoiceSession(ctx, "", speaker, voiceOutputMode, nil)
			done <- err
		}()
		return done
	}
	open := func(ctx context.Context, a *App) chan error { return openOn(ctx, a, "spk-2") }
	bg := context.Background()
	atOnce := func(t *testing.T, r held, why string) {
		t.Helper()
		select {
		case err := <-openOn(bg, r.a, "spk-3"): // a speaker no earlier open of the case holds
			if err != nil || r.reached() != 1 {
				t.Fatalf("%s: %v, reached the engine %d time(s)", why, err, r.reached())
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("%s, AND THE OPEN WAITED (reached the engine %d time(s))", why, r.reached())
		}
	}

	t.Run("a close asked for is waited on, and the open follows it", func(t *testing.T) {
		r := rig(t, 0)
		if err := r.h.Close(bg, "abort"); err != nil {
			t.Fatal(err)
		}
		opened := open(bg, r.a)
		select {
		case err := <-opened:
			t.Fatalf("THE OPEN WENT TO THE ENGINE WHILE THE SESSION IN ITS WAY WAS STILL CLOSING (%d reached it): %v", r.reached(), err)
		case <-time.After(150 * time.Millisecond):
		}
		if r.reached() != 0 {
			t.Fatalf("the open reached the engine %d time(s) before the close in flight finished", r.reached())
		}
		// The wait holds no turn: another page's open is not kept from its own.
		turn, cancel := context.WithTimeout(bg, time.Second)
		release, err := r.a.voiceOpens.take(turn)
		cancel()
		if err != nil {
			t.Fatalf("AN OPEN WAITING FOR A CLOSE HELD THE TURN EVERY OTHER OPEN NEEDS: %v", err)
		}
		release()
		close(r.ended) // the engine's word that the session is over
		select {
		case err := <-opened:
			if err != nil || r.reached() != 1 {
				t.Fatalf("after the close finished the open: %v, reached the engine %d time(s)", err, r.reached())
			}
		case <-time.After(5 * time.Second):
			t.Fatal("the open never proceeded after the close finished")
		}
	})

	t.Run("a close asked while the open waited for its turn is waited on too", func(t *testing.T) {
		r := rig(t, 0)
		release, err := r.a.voiceOpens.take(bg) // another open is being answered
		if err != nil {
			t.Fatal(err)
		}
		opened := open(bg, r.a) // nothing is ending yet: it goes on to wait for its turn
		time.Sleep(100 * time.Millisecond)
		if err := r.h.Close(bg, "abort"); err != nil {
			t.Fatal(err)
		}
		release()
		select {
		case err := <-opened:
			t.Fatalf("THE OPEN TOOK ITS TURN AND WENT TO THE ENGINE WHILE A SESSION ASKED TO END SINCE WAS STILL ENDING (%d reached it): %v", r.reached(), err)
		case <-time.After(150 * time.Millisecond):
		}
		close(r.ended)
		if err := <-opened; err != nil || r.reached() != 1 {
			t.Fatalf("after the close finished the open: %v, reached the engine %d time(s)", err, r.reached())
		}
	})

	t.Run("the drain the host asks for after the input's completion is waited on too", func(t *testing.T) {
		r := rig(t, 0)
		r.a.voiceDrainAfterInput(bg, r.h)
		if r.h.closing.Load() {
			t.Fatal("rig: a drain is not a close of the operator's")
		}
		opened := open(bg, r.a)
		select {
		case err := <-opened:
			t.Fatalf("THE OPEN WENT TO THE ENGINE WHILE THE SESSION WAS DRAINING TO ITS END (%d reached it): %v", r.reached(), err)
		case <-time.After(150 * time.Millisecond):
		}
		close(r.ended)
		if err := <-opened; err != nil || r.reached() != 1 {
			t.Fatalf("after the drain finished the open: %v, reached the engine %d time(s)", err, r.reached())
		}
	})

	t.Run("a live session on another page is not waited for and not taken", func(t *testing.T) {
		r := rig(t, 0)
		refusal := errors.New("voicesession: session vs-held is still open — close it before opening another")
		voiceEngineOpen = func(_ *pluginhost.VoiceSession, _ context.Context, _ string, _ *audio.Binding, _ map[string]any) error {
			return refusal
		}
		select {
		case err := <-open(bg, r.a):
			if err == nil || !errors.Is(err, dashboard.ErrVoiceEngineRefused) {
				t.Fatalf("an open beside a live session: %v; the engine's refusal is the answer", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("an open beside a LIVE session waited; only a session being ended is waited on (reached %d)", r.reached())
		}
		r.f.mu.Lock()
		defer r.f.mu.Unlock()
		if len(r.f.closed) != 0 {
			t.Fatalf("a live session was asked to close for an open that takes nothing: %v", r.f.closed)
		}
	})

	t.Run("a session being ended on another engine holds nobody", func(t *testing.T) {
		r := rig(t, 0)
		r.h.engine = &pluginhost.ActivePlugin{}
		if err := r.h.Close(bg, "abort"); err != nil {
			t.Fatal(err)
		}
		atOnce(t, r, "the session being ended is another engine's")
	})

	t.Run("an ask the engine would not take is not waited for", func(t *testing.T) {
		r := rig(t, 0)
		r.f.current = "another-session" // the engine refuses a control that names vs-held
		if err := r.h.Close(bg, "abort"); err == nil {
			t.Fatal("rig: the engine took the close")
		}
		atOnce(t, r, "the engine refused the close")
	})

	t.Run("a close that never finishes is given the setting, counted from when it was asked", func(t *testing.T) {
		r := rig(t, 1)
		asked := time.Now()
		if err := r.h.Close(bg, "abort"); err != nil {
			t.Fatal(err)
		}
		time.Sleep(600 * time.Millisecond)
		select {
		case err := <-open(bg, r.a):
			since := time.Since(asked)
			if err != nil || r.reached() != 1 || since < 900*time.Millisecond || since > 1400*time.Millisecond {
				t.Fatalf("with speech.open_wait_seconds 1, an open 0.6 s after the ask: %v, reached the engine %d time(s), %s after the ask; the session has one second from the ask", err, r.reached(), since.Round(time.Millisecond))
			}
		case <-time.After(4 * time.Second):
			t.Fatal("the open was held past speech.open_wait_seconds by a close that never finished")
		}
		// And once its time is up it holds no later open at all.
		r2 := held{r.a, r.h, r.ended, func() int { return r.reached() - 1 }, r.f}
		atOnce(t, r2, "the session was asked to end longer ago than the setting")
	})

	t.Run("an open given up while it waits goes to no engine", func(t *testing.T) {
		r := rig(t, 0)
		if err := r.h.Close(bg, "abort"); err != nil {
			t.Fatal(err)
		}
		left := errors.New("the page went away")
		ctx, leave := context.WithCancelCause(bg)
		opened := open(ctx, r.a)
		time.Sleep(100 * time.Millisecond)
		leave(left)
		select {
		case err := <-opened:
			if err == nil || !errors.Is(err, left) || !strings.Contains(err.Error(), "given up while a session in its way was ending") || r.reached() != 0 {
				t.Fatalf("an open given up while it waited: %v, reached the engine %d time(s)", err, r.reached())
			}
		case <-time.After(2 * time.Second):
			t.Fatal("an open given up while it waited went on waiting")
		}
	})
}

// THE WAIT IS THE OPERATOR'S: speech.open_wait_seconds, taken at the door and
// from the file within 1 to 60, in force at the next open, read back as it
// is in force; absent or 0 is the default.
func TestTheVoiceOpenWaitIsASettingOfTheOperators(t *testing.T) {
	a := newVoiceApp(t)
	applyDefaults(a.cfg)
	a.cfg.Prompt.RecentTurns = 20
	if got := a.configSnapshot().Speech.openWait(); got != 5*time.Second {
		t.Fatalf("with nothing stated the wait is %s; it is five seconds", got)
	}
	for _, edge := range []int{1, 60} {
		if st, err := a.applyConfigChange(map[string]any{"speech.open_wait_seconds": edge}); err != nil || st.Speech.OpenWaitSeconds != edge {
			t.Fatalf("the door refused %d seconds, an end of the range: %v", edge, err)
		}
	}
	st, err := a.applyConfigChange(map[string]any{"speech.open_wait_seconds": 12})
	if err != nil {
		t.Fatal(err)
	}
	if got := a.configSnapshot().Speech.openWait(); got != 12*time.Second || st.Speech.OpenWaitSeconds != 12 || len(st.RestartRequired) != 0 {
		t.Fatalf("the door set 12 seconds: in force %s, read back %d, restart %v", got, st.Speech.OpenWaitSeconds, st.RestartRequired)
	}
	loaded, err := LoadConfig(a.configSnapshot().SourcePath)
	if err != nil || loaded.Speech.OpenWaitSeconds != 12 {
		t.Fatalf("the saved wait did not survive a load: %v, %+v", err, loaded)
	}
	for _, bad := range []int{-1, 61} {
		if _, err := a.applyConfigChange(map[string]any{"speech.open_wait_seconds": bad}); err == nil || !strings.Contains(err.Error(), "speech.open_wait_seconds") {
			t.Fatalf("the door took a wait of %d seconds: %v", bad, err)
		}
		// And a file that states it is refused by the loader, by its key.
		path := filepath.Join(t.TempDir(), "config.json")
		written, err := os.ReadFile(a.configSnapshot().SourcePath)
		if err != nil || !bytes.Contains(written, []byte(`"open_wait_seconds": 12`)) {
			t.Fatalf("rig: the saved file does not state the wait as written: %v", err)
		}
		if err := os.WriteFile(path, bytes.Replace(written, []byte(`"open_wait_seconds": 12`), []byte(`"open_wait_seconds": `+strconv.Itoa(bad)), 1), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadConfig(path); err == nil || !strings.Contains(err.Error(), "speech.open_wait_seconds") {
			t.Fatalf("a file stating a wait of %d seconds was loaded: %v", bad, err)
		}
	}
	if st, err = a.applyConfigChange(map[string]any{"speech.open_wait_seconds": 0}); err != nil || st.Speech.OpenWaitSeconds != 5 {
		t.Fatalf("0 takes the default: %v, read back %d", err, st.Speech.OpenWaitSeconds)
	}
}
