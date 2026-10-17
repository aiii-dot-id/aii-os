package app

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestAnEngineChangeTakesEffectAtTheNextVoiceSession(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name, before, after string
		installed           []string
	}{
		{"one engine named, then another", "id.engine.first", "id.engine.second", nil},
		{"the default, then another engine named", "", "id.engine.second", nil},
		{"another engine named, then the default", "id.engine.second", "", nil},
		{"an engine named, then one installed and not running", "id.engine.first", "id.engine.idle", []string{"id.engine.idle"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := speechRouteApp(t, "", c.before)
			for _, id := range c.installed {
				engineInstalledHere(t, a, id, "voice_interface")
			}
			first, _ := runningEngine(t, a, "id.engine.first")
			second, _ := runningEngine(t, a, "id.engine.second")
			reached := opensReach(t)
			given := typedRepliesReach(t)

			open, err := openSpeaking(a, "spk-2")
			if err != nil {
				t.Fatal(err)
			}
			chooseSpeech(a, "", c.after)
			// THE PAGE KEEPS ITS LANE: nothing it is told names a service, so
			// it neither gives the engine's lane up nor reads replies itself.
			if v := a.replyVoice(); v != "" {
				t.Fatalf("an engine change told the page a service speaks (%q): it gives the open lane up for a voice that is not there", v)
			}
			a.settleVoice(ctx, "typed after the change")
			if g := given(); len(g) != 1 || g[0] != open.id {
				t.Fatalf("the reply after the change was given to %v, want the open session %s until it retires", g, open.id)
			}

			// The session retires (the page left earbuds); the next one opens
			// on the engine now chosen. (The open was recorded, not run: a
			// double stands where the engine admits the close.)
			open.v = &fakeEngineSession{}
			if err := open.Close(ctx, "abort"); err != nil {
				t.Fatal(err)
			}
			want := a.speechEngineFor(c.after)
			next, err := openSpeaking(a, "spk-3")
			if want == nil {
				if err == nil || !strings.Contains(err.Error(), c.after) {
					t.Fatalf("the next session opened (%v) although the engine chosen is not running: %v", lanesOf(reached(), first, second), err)
				}
				a.settleVoice(ctx, "typed with no session to speak it")
				if g := given(); len(g) != 1 {
					t.Fatalf("a reply was given to a retired session: %v", g)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if r := reached(); len(r) != 2 || r[1] != want.Voice.Load() {
				t.Fatalf("the next session opened on %v, want %s", lanesOf(r, first, second), want.ID)
			}
			a.settleVoice(ctx, "typed in the next session")
			if g := given(); len(g) != 2 || g[1] != next.id {
				t.Fatalf("the reply in the next session was given to %v, want %s", g, next.id)
			}
		})
	}
}

// AN ENGINE THAT RESTARTS ENDS ITS SESSION, AND THE NEXT ONE IS THE CHOICE.
// The open session on the engine chosen before is over when its engine's
// lane ends; no reply is given to it, and the page's next open reaches the
// engine now chosen.
func TestAnEngineRestartHandsTheVoiceToTheEngineChosen(t *testing.T) {
	ctx := context.Background()
	a := speechRouteApp(t, "", "id.engine.first")
	first, stopFirst := runningEngine(t, a, "id.engine.first")
	second, _ := runningEngine(t, a, "id.engine.second")
	reached := opensReach(t)
	given := typedRepliesReach(t)
	open, err := openSpeaking(a, "spk-2")
	if err != nil {
		t.Fatal(err)
	}
	chooseSpeech(a, "", "id.engine.second")
	stopFirst()
	// The session's end is the engine's word, delivered off the caller.
	deadline := time.Now().Add(5 * time.Second)
	for _, held := a.voiceSessions.Load(open.id); held && time.Now().Before(deadline); _, held = a.voiceSessions.Load(open.id) {
		time.Sleep(time.Millisecond)
	}
	if _, held := a.voiceSessions.Load(open.id); held {
		t.Fatal("fixture: the session outlived its engine's lane")
	}
	a.settleVoice(ctx, "typed while no session speaks")
	if g := given(); len(g) != 0 {
		t.Fatalf("a reply was given to the session of an engine that stopped: %v", g)
	}
	next, err := openSpeaking(a, "spk-3")
	if err != nil {
		t.Fatal(err)
	}
	if r := reached(); len(r) != 2 || r[1] != second.Voice.Load() {
		t.Fatalf("after the restart the next session opened on %v, want %s", lanesOf(r, first, second), second.ID)
	}
	a.settleVoice(ctx, "typed in the next session")
	if g := given(); len(g) != 1 || g[0] != next.id {
		t.Fatalf("the reply was given to %v, want %s", g, next.id)
	}
}

// A CONVERSATION KEEPS THE SPEAKING ENGINE IT OPENED UNDER. The same
// policy for a conversation's replies: an engine chosen to speak while it
// is open applies to the next conversation, whichever way the choice
// moves; a service, which speaks outside any session, applies at once —
// as the page gives an engine's output lane up for one at once.
func TestAConversationKeepsTheSpeakingEngineItOpenedUnder(t *testing.T) {
	cases := []struct {
		name, at, now, spoke string
	}{
		{"opened with the hearing engine speaking, another chosen", "", "id.engine.b", "a"},
		{"opened with another engine speaking, the default chosen", "id.engine.b", "", "b"},
		{"opened with the hearing engine speaking, a service chosen", "", "ElevenLabs", "service"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := speechRouteApp(t, "", "id.engine.b")
			engA, _ := runningEngine(t, a, "id.engine.a")
			engB, _ := runningEngine(t, a, "id.engine.b")
			opensReach(t)
			var minted []string
			prev := voiceFallbackMint
			voiceFallbackMint = func(_ *App, text string) string { minted = append(minted, text); return "svc-1" }
			t.Cleanup(func() { voiceFallbackMint = prev })
			// B's output session, held by a page in earbuds.
			out, err := openSpeaking(a, "spk-2")
			if err != nil || out.engine != engB {
				t.Fatalf("fixture: B's output session did not open on B: %v", err)
			}
			outOn := &fakeEngineSession{}
			out.v = outOn
			// The conversation opens on A under the choice at, then the
			// choice moves to now.
			chooseSpeech(a, "", c.at)
			hear, err := openHearing(a)
			if err != nil || hear.engine != engA {
				t.Fatalf("fixture: the conversation did not open on A: %v", err)
			}
			hearOn := &fakeEngineSession{}
			hear.v = hearOn
			chooseSpeech(a, "", c.now)
			stubWake(t, func() (string, error) { return "the answer", nil })
			if err := a.observeVoice(context.Background(), heardUtterance{Text: "a question", Answer: true, Operator: true,
				SessionID: hear.id, Gen: hear.gen.Load(), Source: "speech engine id.engine.a"}); err != nil {
				t.Fatal(err)
			}
			a.bgWG.Wait()
			got := map[string]int{"a": len(hearOn.synthed()), "b": len(outOn.synthed()), "service": len(minted)}
			for who, n := range got {
				want := 0
				if who == c.spoke {
					want = 1
				}
				if n != want {
					t.Fatalf("the reply was synthesized by %v, want %s alone", got, c.spoke)
				}
			}
		})
	}
}
