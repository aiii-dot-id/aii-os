package app

import (
	"encoding/json"
	"net/http"
	"sync/atomic"
	"testing"
)

// jsonQuote is s as a JSON string literal.
func jsonQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func TestATurnsToolSinkIsAttachedForTheTurnAndDetachedAfter(t *testing.T) {
	sinkAttached := func(a *App) bool {
		a.turn.mu.Lock()
		defer a.turn.mu.Unlock()
		return a.turn.toolEmit != nil
	}
	for _, path := range []string{"typed", "spoken"} {
		t.Run(path, func(t *testing.T) {
			a, dir := goldenTurnApp(t)
			var during atomic.Bool
			fixtureModel(t, a, func(n int, w http.ResponseWriter) {
				during.Store(sinkAttached(a))
				w.Header().Set("Content-Type", "application/json")
				if n == 1 {
					_, _ = w.Write([]byte(callReply("", toolCall("c1", "ls", `{"path":`+jsonQuote(dir)+`}`))))
					return
				}
				_, _ = w.Write([]byte(textReply("noted")))
			})
			ctx := goldenTurnCtx(t)
			if err := a.acquireTurn(ctx); err != nil {
				t.Fatal(err)
			}
			var streamed atomic.Int32
			switch path {
			case "typed":
				if _, err := a.observeChat(ctx, "list the folder", func(kind, name, args string) { streamed.Add(1) }); err != nil {
					t.Fatal(err)
				}
				if streamed.Load() == 0 {
					t.Error("the chat's own sink received none of its turn's tool events")
				}
			case "spoken":
				_, err := a.wake(ctx, roleOperator, voiceMarker+"list the folder")
				a.releaseTurn()
				if err != nil {
					t.Fatal(err)
				}
			}
			if !during.Load() {
				t.Error("no sink was attached while the turn ran")
			}
			if sinkAttached(a) {
				t.Error("the turn ended and its sink is still attached")
			}
		})
	}
}
