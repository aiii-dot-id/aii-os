package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/supervisor"
)

// .
// .
// .
// .
// .
// .
func TestAFallbackIsFencedByTheSessionItAnswered(t *testing.T) {
	ctx := context.Background()
	refusal := &supervisor.SessionRefusedError{Op: "speech.session.synthesize", Err: json.RawMessage(`{"code":-32000,"message":"not eligible"}`)}
	for _, fence := range []string{"barge-in", "abort", "engine heard the operator", "drain", "session end"} {
		t.Run(fence, func(t *testing.T) {
			a := newVoiceApp(t)
			h, engine := newTrackedSession(a, "vs-taken", true)
			h.v = &fallbackAckEngine{fakeEngineSession: engine, ackErr: refusal}
			var mu sync.Mutex
			var minted []string
			var refs []dashboard.VoiceReplyRef
			var hushes []dashboard.VoiceHush
			prev := voiceFallbackMint
			voiceFallbackMint = func(_ *App, text string) string {
				mu.Lock()
				defer mu.Unlock()
				minted = append(minted, text)
				return "fb-1"
			}
			t.Cleanup(func() { voiceFallbackMint = prev })
			a.voiceReplySink = func(r dashboard.VoiceReplyRef, _ string) { mu.Lock(); refs = append(refs, r); mu.Unlock() }
			a.voiceHushSink = func(v dashboard.VoiceHush) { mu.Lock(); hushes = append(hushes, v); mu.Unlock() }
			stubWake(t, func() (string, error) { return "The engine would not say this.", nil })
			if err := a.observeVoice(ctx, heardUtterance{Text: "a question", Answer: true, Operator: true,
				SessionID: h.id, Gen: h.gen.Load(), Source: "test native engine"}); err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			if len(minted) != 1 || len(refs) != 1 || refs[0].Route != "cloud" || refs[0].SynthesisID != "fb-1" || !refs[0].Fallback || refs[0].TextOnly {
				mu.Unlock()
				t.Fatalf("the definite refusal was not taken over under custody: minted=%v refs=%+v", minted, refs)
			}
			if len(hushes) != 0 {
				mu.Unlock()
				t.Fatalf("hushed before any fence: %+v", hushes)
			}
			mu.Unlock()
			if out, _ := h.replyOutcome.Load().(string); !strings.Contains(out, "spoken by the cloud voice") {
				t.Errorf("the session's diagnostic does not say who spoke: %q", out)
			}
			switch fence {
			case "barge-in":
				if err := h.Interrupt(ctx); err != nil {
					t.Fatal(err)
				}
			case "abort":
				if err := h.Close(ctx, "abort"); err != nil {
					t.Fatal(err)
				}
			case "engine heard the operator":
				a.voiceObserved(pluginhost.Event{Type: "speech_start", SessionID: h.id})
			case "drain":
				if err := h.Close(ctx, "drain"); err != nil {
					t.Fatal(err)
				}
			case "session end":
				// .
				// .
			}
			mu.Lock()
			defer mu.Unlock()
			if fence == "drain" || fence == "session end" {
				if len(hushes) != 0 {
					t.Fatalf("%s is not a fence for a taken-over reply: %+v", fence, hushes)
				}
				return
			}
			if len(hushes) != 1 || hushes[0].SessionID != h.id || hushes[0].SynthesisID != "fb-1" || hushes[0].Route != "cloud" {
				t.Fatalf("%s did not hush the taken-over reply: %+v", fence, hushes)
			}
			// .
			mu.Unlock()
			_ = h.Interrupt(ctx)
			mu.Lock()
			if len(hushes) != 1 {
				t.Fatalf("a spent fence hushed again: %+v", hushes)
			}
		})
	}
}

// .
// .
// .
func TestANewerFallbackHushesTheOlderOne(t *testing.T) {
	ctx := context.Background()
	refusal := &supervisor.SessionRefusedError{Op: "speech.session.synthesize", Err: json.RawMessage(`{"code":-32000,"message":"busy"}`)}
	a := newVoiceApp(t)
	h, engine := newTrackedSession(a, "vs-twice", true)
	h.v = &fallbackAckEngine{fakeEngineSession: engine, ackErr: refusal}
	var mu sync.Mutex
	n := 0
	var hushes []dashboard.VoiceHush
	prev := voiceFallbackMint
	voiceFallbackMint = func(*App, string) string { mu.Lock(); defer mu.Unlock(); n++; return "fb-" + string(rune('0'+n)) }
	t.Cleanup(func() { voiceFallbackMint = prev })
	a.voiceReplySink = func(dashboard.VoiceReplyRef, string) {}
	a.voiceHushSink = func(v dashboard.VoiceHush) { mu.Lock(); hushes = append(hushes, v); mu.Unlock() }
	for _, q := range []string{"first", "second"} {
		stubWake(t, func() (string, error) { return "answer to " + q, nil })
		if err := a.observeVoice(ctx, heardUtterance{Text: q, Answer: true, Operator: true,
			SessionID: h.id, Gen: h.gen.Load(), Source: "test native engine"}); err != nil {
			t.Fatal(err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(hushes) != 1 || hushes[0].SynthesisID != "fb-1" || !strings.Contains(hushes[0].Reason, "newer reply") {
		t.Fatalf("the older taken-over reply was not hushed for the newer one: %+v", hushes)
	}
}

// .
// .
// .
// .
func TestAFallbackIsNotMintedPastTheFence(t *testing.T) {
	a := newVoiceApp(t)
	h, _ := newTrackedSession(a, "vs-late", true)
	b := a.voiceBindingFor(heardUtterance{SessionID: h.id, Gen: h.gen.Load()})
	a.holdVoice(b)
	minted := 0
	prev := voiceFallbackMint
	voiceFallbackMint = func(*App, string) string { minted++; return "fb-never" }
	t.Cleanup(func() { voiceFallbackMint = prev })
	var refs []dashboard.VoiceReplyRef
	a.voiceReplySink = func(r dashboard.VoiceReplyRef, _ string) { refs = append(refs, r) }
	prevSynth := voiceSynthesize
	voiceSynthesize = func(*App, context.Context, string, uint64, string) replyVerdict {
		h.supersede()
		return replyRefusedDefinite
	}
	t.Cleanup(func() { voiceSynthesize = prevSynth })
	a.settleVoice(context.Background(), "a reply the engine refused")
	if minted != 0 {
		t.Fatalf("a fallback was minted past the fence: %d", minted)
	}
	if len(refs) != 1 || refs[0].Route != "plugin" || !refs[0].TextOnly || refs[0].Fallback {
		t.Fatalf("the reply did not stay text-only: %+v", refs)
	}
	if !a.voiceReplyShown.Swap(false) {
		t.Fatal("the text-only reply was not shown")
	}
	if out, _ := h.replyOutcome.Load().(string); !strings.Contains(out, "superseded before another voice") {
		t.Errorf("the diagnostic does not name the late fence: %q", out)
	}
}

// .
// .
// .
func TestOnlyTheEnginesRefusalIsDefinite(t *testing.T) {
	refused := &supervisor.SessionRefusedError{Op: "speech.session.synthesize", Err: json.RawMessage(`{"code":-32000}`)}
	for _, c := range []struct {
		name string
		err  error
		want bool
	}{
		{"the engine's refusal", refused, true},
		{"the engine's refusal, wrapped", errors.Join(errors.New("admission"), refused), true},
		{"admission unknown", supervisor.ErrAdmissionUnknown, false},
		{"a stale session", pluginhost.ErrStaleSession, false},
		{"a deadline", context.DeadlineExceeded, false},
		{"no error", nil, false},
	} {
		if got := refusedByEngine(c.err); got != c.want {
			t.Errorf("%s: definite=%v, want %v", c.name, got, c.want)
		}
	}
	_ = time.Second
}
