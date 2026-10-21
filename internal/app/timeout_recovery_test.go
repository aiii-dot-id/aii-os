package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/conversation"
	"github.com/aiii-dot-id/aii-os/internal/limits"
	"github.com/aiii-dot-id/aii-os/internal/llm"
)

// The failed HTTP call is not retried. Recovery is a fresh resident turn,
// with the previous partial reply and its marker, after the gate is released.
func TestATimedOutResidentTurnResumesRecordedWork(t *testing.T) {
	for _, mode := range []string{"request_no_work", "request_work", "turn_deadline"} {
		t.Run(mode, func(t *testing.T) {
			work := mode != "request_no_work"
			a, _ := dispatchApp(t)
			defer stopApp(t, a)
			obs := &contScript{}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				b, _ := io.ReadAll(r.Body)
				n := obs.add(string(b))
				w.Header().Set("Content-Type", "application/json")
				switch n {
				case 1:
					if work {
						fmt.Fprint(w, callReply("I am checking the existing work", toolCall("once", "work", `{"action":"start","description":"timeout recovery fixture"}`)))
					} else {
						<-r.Context().Done()
					}
				case 2:
					if work {
						<-r.Context().Done()
					} else {
						fmt.Fprint(w, textReply("Recovered the unfinished request"))
					}
				default:
					fmt.Fprint(w, textReply("Recovered the unfinished request"))
				}
			}))
			defer srv.Close()
			timeout, wall := 1, 20*time.Second
			if mode == "turn_deadline" {
				timeout, wall = 600, time.Second
			}
			a.llmSwap.Swap(llm.New(&llm.ClientConfig{Endpoint: srv.URL, Model: "fake", MaxOutputTokens: 4096, Retries: -1, TimeoutSeconds: timeout, NoStream: true}))
			ctx, cancel := context.WithTimeout(t.Context(), wall)
			defer cancel()
			if e := a.acquireTurn(ctx); e != nil {
				t.Fatal(e)
			}
			if _, e := a.observeChat(ctx, "Finish this request", func(string, string, string) {}); !errors.Is(e, context.DeadlineExceeded) {
				t.Fatalf("original timeout must remain a failure: %v", e)
			}
			waitFor(t, "automatic timeout recovery", func() bool {
				var n int
				e := a.store.DB().QueryRow(`SELECT COUNT(*) FROM conversations WHERE role='resident' AND content='Recovered the unfinished request'`).Scan(&n)
				return e == nil && n == 1 && !a.TurnActive()
			})
			reqs := obs.snapshot()
			want := 2
			if work {
				want = 3
			}
			if len(reqs) != want {
				t.Fatalf("requests=%d want%d", len(reqs), want)
			}
			last := reqs[len(reqs)-1]
			if !strings.Contains(last, "timeout checkpoint — continuation 1") || !strings.Contains(last, "completed tool calls are not to be replayed") {
				t.Fatal("recovery lacks scoped timeout instruction")
			}
			if work {
				if !strings.Contains(last, "I am checking the existing work") || strings.Count(last, "[the reply above is incomplete — the turn ran out of time]") != 1 {
					t.Fatal("partial history or cause missing")
				}
				var n int
				if e := a.store.DB().QueryRow(`SELECT COUNT(*) FROM work_sessions WHERE description='timeout recovery fixture'`).Scan(&n); e != nil || n != 1 {
					t.Fatalf("work creation repeated: %d %v", n, e)
				}
				if e := a.store.DB().QueryRow(`SELECT COUNT(*) FROM conversations WHERE kind='notice' AND content='[the reply above is incomplete — the turn ran out of time]'`).Scan(&n); e != nil || n != 1 {
					t.Fatalf("marker count=%d %v", n, e)
				}
			}
			a.turn.mu.Lock()
			chain := a.turn.contChain
			a.turn.mu.Unlock()
			if chain != 0 {
				t.Fatalf("natural reply did not reset chain:%d", chain)
			}
		})
	}
}

func TestTimeoutRecoveryHonorsStopsRecordingAndConfiguration(t *testing.T) {
	for _, name := range []string{"operator_stop", "unnamed_cancel", "not_recorded", "other_error", "disabled", "bound", "timer_owner", "unknown_source", "constrained", "shutdown"} {
		t.Run(name, func(t *testing.T) {
			a, _ := dispatchApp(t)
			defer stopApp(t, a)
			ctx := withTurnSource(t.Context(), turnSourceOperator)
			e := error(context.DeadlineExceeded)
			kept := true
			switch name {
			case "operator_stop":
				c, cancel := context.WithCancelCause(ctx)
				cancel(conversation.StoppedByOperator)
				ctx = c
			case "unnamed_cancel":
				c, cancel := context.WithCancel(ctx)
				cancel()
				ctx = c
			case "constrained":
				names := []string{}
				var x error
				ctx, x = conversation.WithRequestOptions(ctx, conversation.RequestOptions{AllowedTools: &names})
				if x != nil {
					t.Fatal(x)
				}
			case "shutdown":
				a.bgCancel()
			case "timer_owner":
				ctx = withTurnSource(ctx, turnSourceTimer)
			case "unknown_source":
				ctx = withTurnSource(ctx, "")
			case "not_recorded":
				kept = false
			case "other_error":
				e = errors.New("fixture non-timeout")
			case "disabled":
				off := false
				a.cfgMu.Lock()
				a.cfg.Agency.TurnContinuation = &off
				a.cfgMu.Unlock()
			case "bound":
				a.turn.mu.Lock()
				a.turn.contChain = continuationsSetting.value(a.configSnapshot()).N
				a.turn.mu.Unlock()
			}
			a.resumeTimedOutTurn(ctx, e, kept)
			a.turn.mu.Lock()
			chain := a.turn.contChain
			a.turn.mu.Unlock()
			if name != "bound" && chain != 0 {
				t.Fatalf("refused recovery incremented chain:%d", chain)
			}
			// Background admission is synchronous, so these refusals cannot hide a later turn.
			var n int
			if err := a.store.DB().QueryRow(`SELECT COUNT(*) FROM conversations WHERE content LIKE '[timeout checkpoint%'`).Scan(&n); err != nil || n != 0 {
				t.Fatalf("unexpected recovery:%d %v", n, err)
			}
			if name == "disabled" || name == "bound" {
				if got := outboxReply(t, a, "continuation_timeout_"); !strings.Contains(got, "Nothing continues by itself:") {
					t.Fatalf("missing visible stand-down: %q", got)
				}
			}
		})
	}
}

func TestTimeoutRecoveryDoesNotTakeOverDifferentWork(t *testing.T) {
	for _, target := range []string{"", "ws_missing", "ws_old"} {
		t.Run("target_"+target, func(t *testing.T) {
			a, _ := dispatchApp(t)
			defer stopApp(t, a)
			if target == "ws_old" {
				if e := a.store.StartWorkSession(t.Context(), target, "old fixture"); e != nil {
					t.Fatal(e)
				}
			}
			if e := a.store.StartWorkSession(t.Context(), "ws_current", "current fixture"); e != nil {
				t.Fatal(e)
			}
			a.continueCappedTurn(target, 1, capDeadline, "")
			if got := outboxReply(t, a, "continuation_1_"); !strings.Contains(got, "work selection changed") {
				t.Fatalf("missing stand-down:%q", got)
			}
			ws, e := a.store.ActiveWorkSession()
			if e != nil || ws == nil || ws.ID != "ws_current" {
				t.Fatalf("current work changed:%+v %v", ws, e)
			}
		})
	}
}

func TestRepeatedTimeoutRecoveryStopsAtConfiguredBound(t *testing.T) {
	a, _ := dispatchApp(t)
	defer stopApp(t, a)
	a.cfgMu.Lock()
	a.cfg.Agency.MaxTurnContinuations = limits.Of(1)
	a.cfgMu.Unlock()
	obs := &contScript{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		obs.add(string(b))
		<-r.Context().Done()
	}))
	defer srv.Close()
	a.llmSwap.Swap(llm.New(&llm.ClientConfig{Endpoint: srv.URL, Model: "fake", Retries: -1, TimeoutSeconds: 1, NoStream: true}))
	if e := a.acquireTurn(t.Context()); e != nil {
		t.Fatal(e)
	}
	if _, e := a.observeChat(t.Context(), "Finish the unanswered request", func(string, string, string) {}); !errors.Is(e, context.DeadlineExceeded) {
		t.Fatalf("original error:%v", e)
	}
	waitFor(t, "bounded timeout stand-down", func() bool {
		rows, e := a.store.UndeliveredFor("operator")
		if e != nil {
			return false
		}
		for _, r := range rows {
			if strings.Contains(r.Content, "limit (agency.max_turn_continuations)") && !a.TurnActive() {
				return true
			}
		}
		return false
	})
	if n := len(obs.snapshot()); n != 2 {
		t.Fatalf("sent %d requests, want original plus one fresh leg", n)
	}
}
