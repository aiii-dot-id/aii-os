package app

import (
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

func requestShapes() []requestShape {
	var shapes []requestShape
	for _, group := range [][]requestShape{
		turnShapes(),
		subagentShapes(),
		facilityShapes(),
		wakeShapes(),
		postureShapes(),
		providerShapes(),
	} {
		shapes = append(shapes, group...)
	}
	return shapes
}

// turnShapes are the resident's turns: each way one opens, on a newly born
// identity and on a realistic one, and the request after a tool round.
func turnShapes() []requestShape {
	var shapes []requestShape
	// Each way a resident turn opens, on the golden fixture's newly
	// born identity (TestEachTurnPathSendsItsPinnedPrompt's paths).
	for _, p := range turnPaths() {
		shapes = append(shapes, requestShape{
			name:  "turn_" + p.name,
			about: turnPathAbout[p.name] + ", on a newly born identity",
			tools: "resident",
			send: func(t *testing.T) sentRequest {
				a, dir := goldenTurnApp(t)
				obs := fixtureModel(t, a, answerNoted)
				p.run(t, a)
				waitUntil(t, func() bool { return !a.TurnActive() }, "the turn to end")
				return sentRequest{body: onlyRequest(t, obs.snapshot()), dir: dir}
			},
		})
	}
	// Again, on the realistic identity: every part of the prompt
	// carries something (whole_prompt_identity_test.go). Its charter is
	// already affirmed, so the chartered path is the operator path here.
	for _, p := range turnPaths() {
		if p.name == "chartered" {
			continue
		}
		shapes = append(shapes, requestShape{
			name:  "realistic_" + p.name,
			about: turnPathAbout[p.name] + ", on a realistic identity",
			tools: "resident_with_plugin",
			send: func(t *testing.T) sentRequest {
				a, dir := realisticApp(t)
				obs := fixtureModel(t, a, answerNoted)
				p.run(t, a)
				waitUntil(t, func() bool { return !a.TurnActive() }, "the turn to end")
				return sentRequest{body: onlyRequest(t, obs.snapshot()), dir: dir}
			},
		})
	}
	// The request after a tool round, inside the same turn.
	return append(shapes, requestShape{
		name:  "followup_after_tool_round",
		about: "the second request of an operator's turn, after the model read a file",
		tools: "resident",
		send: func(t *testing.T) sentRequest {
			a, dir := goldenTurnApp(t)
			if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("GOLDEN_NOTES a line in a file the identity reads\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			// The model names the file as an identity may, relative to its
			// home: an absolute path would be the fixture's directory,
			// escaped inside the call's arguments as each platform spells it.
			obs := fixtureModel(t, a, func(n int, w http.ResponseWriter) {
				if n == 1 {
					replyingWith(callReply("", toolCall("call_read_1", "read", `{"file_path":"notes.txt"}`)))(n, w)
					return
				}
				answerNoted(n, w)
			})
			operatorWords(t, a, "GOLDEN_FOLLOWUP read my notes and tell me what they say")
			reqs := obs.snapshot()
			if len(reqs) != 2 {
				t.Fatalf("the turn sent %d requests, want 2", len(reqs))
			}
			return sentRequest{body: reqs[1], dir: dir}
		},
	})
}

// subagentShapes are the first request of a sub-agent's leg, spawned by
// the identity's own work verb, from a newly born identity and from a
// realistic one, whose working truth the leg is sent folded to routes.
func subagentShapes() []requestShape {
	var shapes []requestShape
	for _, leg := range []struct {
		name, about, tools string
		app                func(t *testing.T) (*App, string)
	}{
		{"subagent_leg", "the first request of a sub-agent's leg, spawned with work action=spawn", "resident", goldenTurnApp},
		{"subagent_leg_realistic", "the first request of a sub-agent's leg spawned by the realistic identity: its working truth folded to routes", "resident_with_plugin", realisticApp},
	} {
		shapes = append(shapes, requestShape{
			name: leg.name, about: leg.about, tools: leg.tools,
			send: func(t *testing.T) sentRequest {
				a, dir := leg.app(t)
				obs := spawnAndHarvest(t, a)
				return sentRequest{body: requestCarrying(t, obs, "[sub-agent, depth 1]"), dir: dir, more: spawnedSessions(t, a)}
			},
		})
	}
	return shapes
}

// spawnedGoal is the goal the identity spawns its sub-agent with.
const spawnedGoal = "GOLDEN_SUBGOAL count the lines of notes.txt and report the number"

// spawnAndHarvest has the identity spawn a sub-agent with its own work verb
// and waits for the turn the delivery wakes to end: the leg's request and
// the harvest's are then both in hand.
func spawnAndHarvest(t *testing.T, a *App) *contScript {
	t.Helper()
	obs := fixtureModel(t, a, answerNoted)
	if out, err := a.engine.ExecuteAction(t.Context(), "verb", "work", map[string]any{"action": "spawn", "goal": spawnedGoal}); err != nil {
		t.Fatalf("spawn: %v %v", out, err)
	}
	waitUntil(t, func() bool { return requestCarrying(t, obs, "your sub-agent finished") != "" }, "the harvest wake's request")
	waitUntil(t, func() bool { return !a.TurnActive() }, "the harvest turn to end")
	return obs
}

// spawnedSessions normalises the session a spawn opened: the work verb
// names it by a random id, and its notes path by that id's hash
// (sessionNotesTarget).
func spawnedSessions(t *testing.T, a *App) func(string) string {
	t.Helper()
	rows, err := a.store.DB().Query(`SELECT id FROM work_sessions WHERE description = ?`, store.SubagentDescription(spawnedGoal))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil || len(ids) != 1 {
		t.Fatalf("%d spawned sessions, want 1 (%v)", len(ids), err)
	}
	return func(s string) string {
		s = strings.ReplaceAll(s, sessionNotesTarget(ids[0]), "work/<sha256 of the session id>/notes.md")
		return strings.ReplaceAll(s, ids[0], "ws_<spawned>")
	}
}

func wakeShapes() []requestShape {
	return []requestShape{
		{
			name: "wake_harvest", about: "the wake a sub-agent's delivery opens: the host's harvest fact as the current message", tools: "resident",
			send: func(t *testing.T) sentRequest {
				a, dir := goldenTurnApp(t)
				obs := spawnAndHarvest(t, a)
				spawned := spawnedSessions(t, a)
				return sentRequest{body: requestCarrying(t, obs, "your sub-agent finished"), dir: dir, more: func(s string) string { return wakeTimes(spawned(s)) }}
			},
		},
		{
			name: "wake_timer", about: "the identity's own alarm, set with work action=alarm.set and fired by the scheduler", tools: "resident",
			send: func(t *testing.T) sentRequest {
				a, dir := goldenTurnApp(t)
				obs := fixtureModel(t, a, answerNoted)
				if out, err := a.engine.ExecuteAction(t.Context(), "verb", "work", map[string]any{
					"action": "alarm.set", "id": "golden_alarm", "duration": "50ms", "message": "GOLDEN_ALARM stand up and stretch",
				}); err != nil {
					t.Fatalf("alarm.set: %v %v", out, err)
				}
				waitUntil(t, func() bool { return requestCarrying(t, obs, "your own alarm woke you") != "" }, "the alarm's wake request")
				waitUntil(t, func() bool { return !a.TurnActive() }, "the alarm's turn to end")
				return sentRequest{body: requestCarrying(t, obs, "your own alarm woke you"), dir: dir, more: wakeTimes}
			},
		},
		{
			name: "wake_continuation", about: "the next leg of work a turn's declared budget cut off", tools: "resident",
			send: func(t *testing.T) sentRequest {
				a, dir := goldenTurnApp(t)
				obs := fixtureModel(t, a, answerNoted)
				if err := a.store.StartWorkSession(t.Context(), "ws_golden_work", "GOLDEN_WORK the work a budget cut off"); err != nil {
					t.Fatal(err)
				}
				a.continueCappedTurn("ws_golden_work", 1, capDeclared, "")
				waitUntil(t, func() bool { return !a.TurnActive() }, "the continuation's turn to end")
				return sentRequest{body: onlyRequest(t, obs.snapshot()), dir: dir}
			},
		},
		{
			name: "wake_arrival", about: "a message from a contact the operator lets wake the identity, framed by the channel's admission", tools: "resident",
			send: func(t *testing.T) sentRequest {
				a, dir := goldenTurnApp(t)
				obs := fixtureModel(t, a, answerNoted)
				contact(t, a, Contact{Name: "Robin", Channel: "mail", Address: "robin@example.test", Wake: true})
				body := "GOLDEN_WAKE_ARRIVAL are you free on Friday?"
				if _, err := a.store.RecordInbound("in_mail_golden_wake", "mail", "robin@example.test", body); err != nil {
					t.Fatal(err)
				}
				a.carryInbound("in_mail_golden_wake", channelRoute{Channel: "mail"}, arrival{ID: "golden_wake", From: "robin@example.test", Body: body})
				waitUntil(t, func() bool { return !a.TurnActive() }, "the arrival's turn to end")
				return sentRequest{body: onlyRequest(t, obs.snapshot()), dir: dir}
			},
		},
		{
			name: "wake_voice_room", about: "someone speaking aloud in the room, in a conversation the operator opened", tools: "resident",
			send: func(t *testing.T) sentRequest {
				a, dir := goldenTurnApp(t)
				obs := fixtureModel(t, a, answerNoted)
				if err := a.observeVoice(goldenTurnCtx(t), heardUtterance{Source: "plugin org.example.voice", Text: "GOLDEN_ROOM what time is the review?", Speaker: "speaker 2", Answer: true}); err != nil {
					t.Fatal(err)
				}
				waitUntil(t, func() bool { return !a.TurnActive() }, "the room's turn to end")
				return sentRequest{body: onlyRequest(t, obs.snapshot()), dir: dir}
			},
		},
		{
			name: "wake_ask_answer", about: "the operator's answer to the identity's question card, carried as their message", tools: "resident",
			send: func(t *testing.T) sentRequest {
				a, dir := goldenTurnApp(t)
				obs := fixtureModel(t, a, answerNoted)
				owed := "GOLDEN_ASK aisle or window for the long leg?"
				if err := a.store.StartWorkSession(t.Context(), "ws_golden_trip", "GOLDEN_TRIP book the flights"); err != nil {
					t.Fatal(err)
				}
				if err := a.store.UpdateWorkPlan(t.Context(), "ws_golden_trip", nil, nil, nil, nil, nil, &owed); err != nil {
					t.Fatal(err)
				}
				if err := a.proposeAsk("ws_golden_trip", owed, []string{"aisle", "window"}, ""); err != nil {
					t.Fatal(err)
				}
				views := a.askViews()
				if len(views) != 1 {
					t.Fatalf("%d cards, want 1", len(views))
				}
				text, settle, err := a.answerAsk(dashboard.AskAnswer{ID: views[0].ID, Answer: "chose", Choice: "window"})
				if err != nil {
					t.Fatal(err)
				}
				// As the page carries a press (dashboard wsAsk): an answer,
				// through the operator's chat door.
				ctx := interaction.WithSaid(goldenTurnCtx(t), interaction.SaidAnswer)
				if err := a.acquireTurn(ctx); err != nil {
					t.Fatal(err)
				}
				_, err = a.observeChat(ctx, text, func(kind, name, args string) {})
				settle(err == nil)
				if err != nil {
					t.Fatal(err)
				}
				waitUntil(t, func() bool { return !a.TurnActive() }, "the answer's turn to end")
				return sentRequest{body: onlyRequest(t, obs.snapshot()), dir: dir, more: askIDs}
			},
		},
	}
}

// turnPathAbout says what opens each turn path, for a shape's head.
var turnPathAbout = map[string]string{
	"operator":    "the operator's typed words",
	"spoken":      "the operator's spoken words",
	"leftover":    "two queued messages left when a turn ended",
	"system":      "a fact the host states",
	"timer":       "the identity's own alarm, in the golden's words",
	"chartered":   "the operator's typed words to an identity holding a charter and both parts of Ring 3",
	"participant": "a contact's message, in the golden's words",
}

// turnPathNamed is the turn path of that name.
func turnPathNamed(t *testing.T, name string) turnPath {
	t.Helper()
	for _, p := range turnPaths() {
		if p.name == name {
			return p
		}
	}
	t.Fatalf("no turn path %q", name)
	return turnPath{}
}

// operatorWords is an operator's typed turn, run to its end.
func operatorWords(t *testing.T, a *App, words string) {
	t.Helper()
	ctx := interaction.WithSaid(goldenTurnCtx(t), interaction.SaidTyped) // as the chat door marks what the operator types
	if err := a.acquireTurn(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := a.observeChat(ctx, words, func(kind, name, args string) {}); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, func() bool { return !a.TurnActive() }, "the turn to end")
}

// wakeTimes are the times a wake's own fact says it fired or was
// delivered (timerNotice, wakeSubagentDelivery): the clock, normalised.
func wakeTimes(s string) string {
	return wakeTimePattern.ReplaceAllString(s, "<time>")
}

var wakeTimePattern = regexp.MustCompile(`\b\d{2}:\d{2}:\d{2} [A-Z]{3,4} [A-Z][a-z]{2} [A-Z][a-z]{2} \d{1,2}\b`)

func askIDs(s string) string {
	return askIDPattern.ReplaceAllString(s, "ask-<id>")
}

var askIDPattern = regexp.MustCompile(`\bask-[0-9a-f]{16}\b`)

// requestCarrying is the body of the one request whose messages carry
// marker, or "" when none has yet; more than one fails.
func requestCarrying(t *testing.T, obs *contScript, marker string) string {
	t.Helper()
	var found []string
	for _, body := range obs.snapshot() {
		if strings.Contains(requestText(t, body), marker) {
			found = append(found, body)
		}
	}
	if len(found) > 1 {
		t.Fatalf("%d requests carried %q, want one", len(found), marker)
	}
	if len(found) == 0 {
		return ""
	}
	return found[0]
}

// onlyRequest is the one request a shape sent.
func onlyRequest(t *testing.T, reqs []string) string {
	t.Helper()
	if len(reqs) != 1 {
		t.Fatalf("the shape sent %d requests, want 1", len(reqs))
	}
	return reqs[0]
}

// replyingWith is a fake provider that gives every request the one reply.
func replyingWith(reply string) func(int, http.ResponseWriter) {
	return func(_ int, w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(reply))
	}
}

// answerNoted answers every request with the word "noted".
var answerNoted = replyingWith(textReply("noted"))
