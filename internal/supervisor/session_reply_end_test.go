package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

// Force termination to win select before the reader delivers the reply, then
// deliver it at the final decision boundary. No timing lottery or retry loop:
// one known acceptance/refusal must never turn into "sent, no reply". The
// absent/foreign-reply controls remain unknown; nothing is replayed.
func TestSessionControlKeepsTheReplyAtTheEndBoundary(t *testing.T) {
	for _, enqueue := range []bool{false, true} {
		for _, end := range []string{"cancel", "lane"} {
			for _, answer := range []string{"accepted", "refused", "absent", "foreign"} {
				t.Run(end+"/"+answer+map[bool]string{false: "/control", true: "/enqueue"}[enqueue], func(t *testing.T) {
					c, guest, cleanup := newSessionPair(t, 1, nil)
					defer cleanup()
					defer c.end()
					ctx, cancel := context.WithCancel(t.Context())
					defer cancel()
					idReady := make(chan json.RawMessage, 1)
					stepped := make(chan struct{}, 1)
					c.afterControlEnd = func() {
						id := <-idReady
						if answer == "accepted" {
							c.deliverReply(id, hostReply{result: json.RawMessage(`{"accepted":true}`)})
						}
						if answer == "refused" {
							c.deliverReply(id, hostReply{errObj: json.RawMessage(`{"code":-1,"message":"receipt refused"}`)})
						}
						if answer == "foreign" {
							c.deliverReply(json.RawMessage(`999`), hostReply{result: json.RawMessage(`{"accepted":true}`)})
						}
						stepped <- struct{}{}
					}
					type result struct {
						raw json.RawMessage
						err error
					}
					done := make(chan result, 1)
					var wait func(context.Context) (json.RawMessage, error)
					if enqueue {
						var err error
						wait, err = c.ControlEnqueue(ctx, "speech.session.playback_report", nil)
						if err != nil {
							t.Fatal(err)
						}
					} else {
						go func() { raw, err := c.Control(ctx, "speech.session.playback_report", nil); done <- result{raw, err} }()
					}
					id, _ := guest.readControl()
					idReady <- id
					if end == "cancel" {
						cancel()
					} else {
						c.end()
					}
					if enqueue {
						go func() { raw, err := wait(ctx); done <- result{raw, err} }()
					}
					select {
					case got := <-done:
						select {
						case <-stepped:
						default:
							t.Fatal("termination step was not reached")
						}
						var refused *SessionRefusedError
						switch answer {
						case "accepted":
							if got.err != nil || string(got.raw) != `{"accepted":true}` {
								t.Fatalf("known reply lost at end: %s %v", got.raw, got.err)
							}
						case "refused":
							if !errors.As(got.err, &refused) || refused.Op != "speech.session.playback_report" || string(refused.Err) != `{"code":-1,"message":"receipt refused"}` {
								t.Fatalf("known refusal lost at end: %v", got.err)
							}
						default:
							if !errors.Is(got.err, ErrAdmissionUnknown) {
								t.Fatalf("unknown reply was invented: %s %v", got.raw, got.err)
							}
						}
					case <-time.After(5 * time.Second):
						t.Fatal("control did not retire")
					}
					c.pendMu.Lock()
					left := len(c.pending)
					c.pendMu.Unlock()
					if left != 0 {
						t.Fatalf("pending reply leaked: %d", left)
					}
				})
			}
		}
	}
}
