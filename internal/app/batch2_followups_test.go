package app

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/llm"
)

func modelCall(app *App, ordinal int, name, args string) (text string, failed, refused bool, code string) {
	var tc llm.ToolCall
	tc.Function.Name, tc.Function.Arguments, tc.EmissionOrdinal = name, args, ordinal
	o := app.executeToolCall(context.Background(), tc)
	return o.Text, o.Failed, o.Refused, o.ReasonCode
}

// A START WHOSE DECISION THE CARD CANNOT CARRY STARTS NOTHING. The card's
// ceiling of open questions was met only after the session was committed,
// so the identity was told the start failed while its session stood, and
// started another. The card's limits are asked first now, and nothing is
// written when they refuse.
func TestAStartWhoseDecisionTheCardCannotCarryStartsNothing(t *testing.T) {
	app, _ := dispatchApp(t)
	defer stopApp(t, app)
	app.asks.mu.Lock()
	if app.asks.asks == nil {
		app.asks.asks = map[string]*pendingAsk{}
	}
	for i := range asksPending {
		id := fmt.Sprintf("ask-full-%d", i)
		app.asks.asks[id] = &pendingAsk{ID: id, Kind: askKindClarify, Session: fmt.Sprintf("ws_other_%d", i), Digest: id}
	}
	app.asks.mu.Unlock()
	var before int
	if err := app.store.DB().QueryRow(`SELECT COUNT(*) FROM work_sessions`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	text, failed, _, _ := modelCall(app, 1, "work", `{"action":"start","description":"ask at start","decision_needed":"which way?"}`)
	if !failed || !strings.Contains(text, "rejected_before_effect (work.decision)") || !strings.Contains(text, "nothing was written") {
		t.Fatalf("a start whose card is refused was not refused before effect: %s", text)
	}
	var after int
	if err := app.store.DB().QueryRow(`SELECT COUNT(*) FROM work_sessions`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("the refused start left a session: %d sessions, was %d", after, before)
	}
}

func TestANoteWithTheOldPrivateArgumentIsSaidThroughTheSeam(t *testing.T) {
	app, _ := dispatchApp(t)
	defer stopApp(t, app)
	text, failed, refused, code := modelCall(app, 1, "note", `{"content":"a thing I keep to myself","private":true}`)
	if failed || refused {
		t.Fatalf("note private= was refused at the seam (%s): %s", code, text)
	}
	if !strings.Contains(text, "That argument is now called unmetabolized. It never hid a note") {
		t.Fatalf("note private= was not told its true name: %s", text)
	}
	if _, _, refused, _ := modelCall(app, 2, "recall", `{"query":"x","private":true}`); !refused {
		t.Fatal("private= was let through to an organ that never read it")
	}
}

// A START'S FORECAST IS RECORDED ON THE SESSION IT STARTED, by the id the
// verb made: the newest active session, read after the start, is another
// start's when two run at once.
func TestAStartsForecastNamesTheSessionItStarted(t *testing.T) {
	app, _ := dispatchApp(t)
	defer stopApp(t, app)
	started := regexp.MustCompile(`Started work session (ws_\S+):`)
	const n = 12
	var wg sync.WaitGroup
	ids := make([]string, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			text, failed, _, _ := modelCall(app, i+1, "work", fmt.Sprintf(`{"action":"start","description":"concurrent %d","steps":%d}`, i, i+2))
			if m := started.FindStringSubmatch(text); !failed && m != nil {
				ids[i] = m[1]
			} else {
				t.Errorf("start %d: %s", i, text)
			}
		}()
	}
	wg.Wait()
	for i, id := range ids {
		var steps int
		err := app.store.DB().QueryRow(`SELECT json_extract(details,'$.work.steps') FROM conversations
			WHERE content='Work forecast accepted' AND json_extract(details,'$.work_session_id')=?`, id).Scan(&steps)
		if err != nil || steps != i+2 {
			t.Errorf("start %d (%s): its forecast is %d (%v), want %d", i, id, steps, err, i+2)
		}
	}
}

// A START IGNORES WHAT IS EMPTY: standing="" at start leaves the standing
// state as it was; an update's empty standing still clears it.
func TestAStartsEmptyStandingKeepsTheStandingState(t *testing.T) {
	app, _ := dispatchApp(t)
	defer stopApp(t, app)
	if text, failed, _, _ := modelCall(app, 1, "work", `{"action":"update","standing":"waiting on the build"}`); failed {
		t.Fatal(text)
	}
	if text, failed, _, _ := modelCall(app, 2, "work", `{"action":"start","description":"empties at start","standing":"","focus":""}`); failed {
		t.Fatal(text)
	}
	got, _, err := app.store.StandingStateAuthored()
	if err != nil || got != "waiting on the build" {
		t.Fatalf("an empty standing= at start changed the standing state: %q %v", got, err)
	}
	if text, failed, _, _ := modelCall(app, 3, "work", `{"action":"update","standing":""}`); failed {
		t.Fatal(text)
	}
	if got, _, err := app.store.StandingStateAuthored(); err != nil || got != "" {
		t.Fatalf("an update's empty standing= no longer clears it: %q %v", got, err)
	}
}

// A SHELL COMMAND SENT TO WORK IS REFUSED AT THE SEAM, typed, naming what
// work takes, and nothing runs or is written: the organ's own pre-emption
// of it, which the seam made unreachable, is gone.
func TestAShellCommandSentToWorkIsRefusedAtTheSeam(t *testing.T) {
	app, dir := dispatchApp(t)
	defer stopApp(t, app)
	marker := dir + "/command-must-not-run"
	var before int
	if err := app.store.DB().QueryRow(`SELECT COUNT(*) FROM work_sessions`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	text, _, refused, code := modelCall(app, 1, "work", fmt.Sprintf(`{"command":%q,"cwd":%q}`, "touch "+marker, dir))
	if !refused || code != "UNKNOWN_ARGUMENT" || !strings.Contains(text, "work does not take command, cwd;") {
		t.Fatalf("a command sent to work was not refused at the seam: %s %s", code, text)
	}
	var after int
	if err := app.store.DB().QueryRow(`SELECT COUNT(*) FROM work_sessions`).Scan(&after); err != nil || after != before {
		t.Fatalf("the refused call wrote a session: %d to %d %v", before, after, err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("the command ran: %v", err)
	}
}

func TestAnOrgansFailureCarriesItsReason(t *testing.T) {
	app, _ := dispatchApp(t)
	defer stopApp(t, app)
	for _, c := range []struct{ name, args, code string }{
		{"work", `{"action":"deliver","result":"served: done"}`, "work.no_session"},
		{"work", `{"action":"start","description":"x","family":"Not A Slug"}`, "start.family"},
		{"commit", `{"variant":"intention.create"}`, "commit.intention.create"},
		{"recall", `{"query":"x","source":"standing"}`, "recall.standing"},
	} {
		text, failed, _, code := modelCall(app, 1, c.name, c.args)
		if !failed || code != c.code {
			t.Errorf("%s %s: failed %v, reason %q, want %q (%s)", c.name, c.args, failed, code, c.code, text)
		}
	}
}
