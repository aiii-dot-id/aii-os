package app

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/supervisor"
	"github.com/coder/websocket"
)

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
func TestCanceledLocalReplyCannotReviveThroughCloudFallback(t *testing.T) {
	var fallback atomic.Int32
	prevMint := voiceFallbackMint
	voiceFallbackMint = func(*App, string) string { fallback.Add(1); return "cloud-fallback" }
	t.Cleanup(func() { voiceFallbackMint = prevMint })
	for _, action := range []string{"barge-in", "abort", "session ended", "ended before binding",
		"canceled context", "closing at captured generation", "driver replaced", "unknown acknowledgement",
		"unknown and fence failed", "definite refusal", "superseded acknowledgement", "superseded and fence failed"} {
		t.Run(action, func(t *testing.T) {
			fallback.Store(0)
			a := newVoiceApp(t)
			h, engine := newTrackedSession(a, "vs-fenced", true)
			var cloud atomic.Int32
			s := dashboard.New("127.0.0.1", 0, &dashboard.WSHandler{
				GetStats:   func() (*dashboard.StatsResponse, error) { return &dashboard.StatsResponse{}, nil },
				GetConfig:  func() (*dashboard.ConfigState, error) { return &dashboard.ConfigState{}, nil },
				SpeakAhead: func(string) string { cloud.Add(1); return "cloud-stale-reply" },
			})
			addr, err := s.Start(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer s.Shutdown(context.Background())
			a.dashboard = s
			a.voiceReplySink = s.BroadcastVoiceReply
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			transport := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
			defer transport.CloseIdleConnections()
			conn, _, err := websocket.Dial(ctx, "wss://"+addr+"/ws", &websocket.DialOptions{
				HTTPClient: &http.Client{Transport: transport},
				HTTPHeader: http.Header{"Origin": []string{"https://" + addr}},
			})
			if err != nil {
				t.Fatal(err)
			}
			defer conn.CloseNow()
			readUntil := func(kind string) dashboard.ServerMessage {
				t.Helper()
				for i := 0; i < 20; i++ {
					_, data, err := conn.Read(ctx)
					if err != nil {
						t.Fatal(err)
					}
					var m dashboard.ServerMessage
					if err := json.Unmarshal(data, &m); err != nil {
						t.Fatal(err)
					}
					if m.Type == kind {
						return m
					}
				}
				t.Fatalf("missing %s frame", kind)
				return dashboard.ServerMessage{}
			}
			readUntil("status")
			// .
			h.replyOutcome.Store("reply admitted")
			callCtx := ctx
			wantEnqueued := 0
			switch action {
			case "ended before binding":
				a.voiceSessions.Delete(h.id)
			case "canceled context":
				var stop context.CancelFunc
				callCtx, stop = context.WithCancel(ctx)
				stop()
			case "closing at captured generation":
				if err := h.Close(ctx, "abort"); err != nil {
					t.Fatal(err)
				}
			case "driver replaced":
				engine.current = "vs-replacement"
			case "unknown acknowledgement", "unknown and fence failed":
				h.v = &fallbackAckEngine{fakeEngineSession: engine, ackErr: supervisor.ErrAdmissionUnknown}
				wantEnqueued = 1
				if action == "unknown and fence failed" {
					engine.failInterrupt = errors.New("fence refused")
				}
			case "definite refusal":
				h.v = &fallbackAckEngine{fakeEngineSession: engine, ackErr: &supervisor.SessionRefusedError{
					Op: "speech.session.synthesize", Err: json.RawMessage(`{"code":-32000,"message":"not eligible"}`)}}
				wantEnqueued = 1
			case "superseded acknowledgement", "superseded and fence failed":
				engine.onSynthesize = func() { a.voiceObserved(pluginhost.Event{Type: "speech_start", SessionID: h.id}) }
				wantEnqueued = 1
				if action == "superseded and fence failed" {
					engine.failInterrupt = errors.New("fence refused")
				}
			}
			stubWake(t, func() (string, error) {
				switch action {
				case "barge-in":
					if err := h.Interrupt(ctx); err != nil {
						return "", err
					}
				case "abort":
					if err := h.Close(ctx, "abort"); err != nil {
						return "", err
					}
				case "session ended":
					h.supersede()
					a.voiceSessions.Delete(h.id)
				}
				return "This reply was canceled before it existed.", nil
			})
			if err := a.observeVoice(callCtx, heardUtterance{Text: "old question", Answer: true, Operator: true,
				SessionID: h.id, Gen: h.gen.Load(), Source: "test native engine"}); err != nil {
				t.Fatal(err)
			}
			if len(engine.synthed()) != wantEnqueued {
				t.Fatalf("native dispatches = %v, want %d", engine.synthed(), wantEnqueued)
			}
			if a.voiceStaleReplies.Load() != 1 {
				t.Fatal("probe did not exercise the native refusal")
			}
			// .
			// .
			// .
			// .
			m := readUntil("response")
			if n := cloud.Load(); n != 0 {
				t.Errorf("%s refused native speech but minted %d cloud synthesis; response provenance=%+v", action, n, m.VoiceReply)
			}
			if action == "definite refusal" {
				if fallback.Load() != 1 {
					t.Errorf("a definite refusal must be taken over exactly once: minted %d", fallback.Load())
				}
				if m.VoiceReply == nil || m.VoiceReply.Route != "cloud" || m.VoiceReply.SynthesisID != "cloud-fallback" || !m.VoiceReply.Fallback || m.VoiceReply.TextOnly || m.VoiceReply.SessionID != h.id {
					t.Errorf("the taken-over reply lost its custody provenance at the page: %+v", m.VoiceReply)
				}
			} else {
				if fallback.Load() != 0 {
					t.Errorf("%s is not a definite refusal and must not be taken over: minted %d", action, fallback.Load())
				}
				if m.VoiceReply == nil || m.VoiceReply.Route != "plugin" || !m.VoiceReply.TextOnly || m.VoiceReply.SynthesisID != "" || m.VoiceReply.SessionID != h.id {
					t.Errorf("%s lost its no-speech fence at the page: %+v", action, m.VoiceReply)
				}
			}
			if m.Message != "This reply was canceled before it existed." {
				t.Errorf("text lost: %+v", m)
			}
			// .
			// .
			recovery, next := newTrackedSession(a, "vs-recovery", true)
			stubWake(t, func() (string, error) { return "The fresh recovery reply.", nil })
			if err := a.observeVoice(ctx, heardUtterance{Text: "new question", Answer: true, Operator: true,
				SessionID: recovery.id, Gen: recovery.gen.Load(), Source: "test native engine"}); err != nil {
				t.Fatal(err)
			}
			m = readUntil("response")
			if len(next.synthed()) != 1 || m.VoiceReply == nil || m.VoiceReply.Route != "plugin" || m.VoiceReply.SessionID != recovery.id || cloud.Load() != 0 {
				t.Fatalf("recovery changed routes or was lost: synth=%v response=%+v cloud=%d", next.synthed(), m, cloud.Load())
			}
			// .
			s.BroadcastResponse("identity", "An ordinary typed reply.")
			m = readUntil("response")
			if cloud.Load() != 1 || m.VoiceReply == nil || m.VoiceReply.Route != "cloud" {
				t.Fatalf("ordinary cloud path was disabled: cloud=%d response=%+v", cloud.Load(), m)
			}
		})
	}
}

// .
// .
type fallbackAckEngine struct {
	*fakeEngineSession
	ackErr error
}

func (f *fallbackAckEngine) SynthesizeForEnqueue(ctx context.Context, sid, id, text string) (func(context.Context) error, error) {
	_, err := f.fakeEngineSession.SynthesizeForEnqueue(ctx, sid, id, text)
	if err != nil {
		return nil, err
	}
	return func(context.Context) error { return f.ackErr }, nil
}

// .
// .
func TestVoiceSettlementUsesThisReplyVerdictNotLastDiagnostic(t *testing.T) {
	for _, admitted := range []bool{false, true} {
		t.Run(map[bool]string{false: "old-success", true: "old-refusal"}[admitted], func(t *testing.T) {
			a := newVoiceApp(t)
			h, _ := newTrackedSession(a, "vs-verdict", true)
			b := a.voiceBindingFor(heardUtterance{SessionID: h.id})
			a.holdVoice(b)
			var refs []dashboard.VoiceReplyRef
			a.voiceReplySink = func(r dashboard.VoiceReplyRef, _ string) { refs = append(refs, r) }
			previous := voiceSynthesize
			voiceSynthesize = func(a *App, _ context.Context, sid string, _ uint64, text string) replyVerdict {
				if admitted {
					h.replyOutcome.Store("another reply was refused")
					a.voiceReplySink(dashboard.VoiceReplyRef{SessionID: sid, SynthesisID: "current", Route: "plugin"}, text)
					return replyAdmitted
				}
				h.replyOutcome.Store("reply admitted")
				return replySuperseded
			}
			t.Cleanup(func() { voiceSynthesize = previous })
			a.settleVoice(context.Background(), "the current reply")
			if b.spoken != admitted || len(refs) != 1 || refs[0].Route != "plugin" || refs[0].TextOnly == admitted || !a.voiceReplyShown.Swap(false) {
				t.Fatalf("diagnostic became routing authority: spoken=%v refs=%+v", b.spoken, refs)
			}
		})
	}
}
