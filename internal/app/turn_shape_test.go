package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/conversation"
	"github.com/aiii-dot-id/aii-os/internal/identity"
	"github.com/aiii-dot-id/aii-os/internal/limits"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

// stepsSentence is the work verb's steps= description in a tool list.
func stepsSentence(t *testing.T, defs []llm.ToolDefinition) string {
	t.Helper()
	for _, d := range defs {
		if d.Function.Name != "work" {
			continue
		}
		props, _ := d.Function.Parameters["properties"].(map[string]any)
		steps, _ := props["steps"].(map[string]any)
		s, _ := steps["description"].(string)
		return s
	}
	t.Fatal("the tool list offers no work verb")
	return ""
}

// loopCheckpoint is the checkpoint the resident's loop holds for its next
// turn, read as agencyHeld reads the loop's other bounds.
func loopCheckpoint(a *App) conversation.Checkpoint {
	a.cfgMu.RLock()
	defer a.cfgMu.RUnlock()
	c := reflect.ValueOf(a.conv).Elem().FieldByName("cfg").FieldByName("Checkpoint")
	calls := c.FieldByName("Calls")
	return conversation.Checkpoint{Calls: limits.Ceiling{N: int(calls.FieldByName("N").Int()), Unlimited: calls.FieldByName("Unlimited").Bool()}, GraceRounds: int(c.FieldByName("GraceRounds").Int()), Slack: int(c.FieldByName("Slack").Int())}
}

// The defaults are the numbers the constants were, each has one owner, and
// with nothing configured the tool list is the registry's own.
func TestTheTurnShapeDefaults(t *testing.T) {
	c := *defaultConfig()
	if calls, grace, chain := checkpointCallsSetting.value(c), graceRoundsSetting.n(c), continuationsSetting.value(c); calls != limits.Of(32) || grace != 4 || chain != limits.Of(8) {
		t.Errorf("in force with nothing set: %s calls, %d grace rounds, %s continuations; the code carried 32, 4, 8", calls, grace, chain)
	}
	if got, want := c.Agency.checkpoint(), (conversation.Checkpoint{Calls: checkpointCallsSetting.value(c), GraceRounds: graceRoundsSetting.n(c), Slack: slackSetting.n(c)}); got != want {
		t.Errorf("the loop's default checkpoint is %+v and the table's is %+v", got, want)
	}
	var registry string
	for _, v := range identity.Verbs() {
		if v.Name == "work" {
			registry = v.Params["properties"].(map[string]any)["steps"].(map[string]any)["description"].(string)
		}
	}
	a := &App{toolReg: tools.NewRegistry(t.TempDir(), nil, tools.Timeouts{})}
	if got := stepsSentence(t, a.buildToolDefinitions()); got != registry {
		t.Errorf("with no configuration the tool list's steps sentence is not the registry's:\n%s\n%s", got, registry)
	}
}

// THE TOOL LIST STATES THE CHECKPOINT ITS TURN IS HELD TO. A turn's list
// is written at the pair its loop hands it (toolDefinitions); the list
// measured outside a turn is written at the configuration's own.
func TestTheStepsSentenceStatesTheTurnsCheckpoint(t *testing.T) {
	a := &App{toolReg: tools.NewRegistry(t.TempDir(), nil, tools.Timeouts{}),
		cfg: &Config{Agency: AgencyConfig{TurnCheckpointCalls: limits.Of(500), TurnCheckpointGraceRounds: 1}}}
	for name, defs := range map[string][]llm.ToolDefinition{
		"the configuration's": a.buildToolDefinitions(),
		"a turn's":            appToolDefiner{a}.ToolDefinitions(conversation.Checkpoint{Calls: limits.Of(500), GraceRounds: 1}),
	} {
		got := stepsSentence(t, defs)
		for _, want := range []string{"(with none declared, at 500)", "gives you one more round to say"} {
			if !strings.Contains(got, want) {
				t.Errorf("%s list does not state the checkpoint (%q): %s", name, want, got)
			}
		}
	}
	// A turn's list follows what its loop hands it and nothing else: a
	// child's loop, or a turn begun before a reload, is told its own.
	if got := stepsSentence(t, appToolDefiner{a}.ToolDefinitions(conversation.Checkpoint{Calls: limits.Of(64), GraceRounds: 12})); !strings.Contains(got, "(with none declared, at 64), gives you 12 more rounds to say") {
		t.Errorf("a turn handed 64 and 12 was told: %s", got)
	}
}

// THE DOOR'S NUMBER REACHES THE NEXT TURN, AND A FILE'S IS HELD TO THE
// ROUND CEILING. A change at the settings door is in the loop for its next
// turn and in the list that turn would be sent, with no restart. A grace
// that leaves no round of agency.max_tool_rounds for a checkpoint would
// end every turn at the round limit, uncontinued, under a tool list that
// promised both: the door refuses it by its key, and the loader holds a
// file's to the default and says so. Zero is the default; less is refused.
func TestTheOperatorsCheckpointReachesTheTurn(t *testing.T) {
	app, dir := dispatchApp(t)
	defer stopApp(t, app)
	if got := loopCheckpoint(app); got != (conversation.Checkpoint{Calls: limits.Of(32), GraceRounds: 4, Slack: 2}) {
		t.Fatalf("rig: the loop began at %+v", got)
	}
	st, err := app.applyConfigChange(map[string]any{"agency.turn_checkpoint_calls": float64(500), "agency.turn_checkpoint_grace_rounds": float64(2)})
	if err != nil {
		t.Fatalf("the door refused a checkpoint inside its bounds: %v", err)
	}
	if len(st.RestartRequired) != 0 {
		t.Errorf("the checkpoint applies at the next turn; the door asked for a restart of %v", st.RestartRequired)
	}
	if got := loopCheckpoint(app); got != (conversation.Checkpoint{Calls: limits.Of(500), GraceRounds: 2, Slack: 2}) {
		t.Errorf("after the door's change the loop holds %+v for its next turn; want 500 and 2", got)
	}
	if got := stepsSentence(t, app.buildToolDefinitions()); !strings.Contains(got, "(with none declared, at 500), gives you two more rounds to say") {
		t.Errorf("after the door's change the tool list says: %s", got)
	}

	// The seeded round ceiling is thirty: twenty-eight is the most a grace
	// may be, and twenty-nine is refused naming the grace and the ceiling.
	if _, err := app.applyConfigChange(map[string]any{"agency.turn_checkpoint_grace_rounds": float64(28)}); err != nil {
		t.Errorf("a grace of 28 under 30 rounds leaves a round and was refused: %v", err)
	}
	var refusal *CognitionRefusal
	_, err = app.applyConfigChange(map[string]any{"agency.turn_checkpoint_grace_rounds": float64(29)})
	if !errors.As(err, &refusal) || refusal.Key != "agency.turn_checkpoint_grace_rounds" || !strings.Contains(refusal.Requirement, "agency.max_tool_rounds (30)") {
		t.Errorf("a grace of 29 under 30 rounds must be refused, typed, naming both: %v", err)
	}
	if got := loopCheckpoint(app); got.GraceRounds != 28 {
		t.Errorf("a refused change moved the loop's grace to %d", got.GraceRounds)
	}

	// A file's: held to the most that leaves a round, said, loaded; a lower
	// ceiling beside a grace the file names is the same rule from the other
	// side. Never held to a default that does not fit.
	for body, c := range map[string]struct {
		said string
		held int
	}{
		`{"agency":{"turn_checkpoint_grace_rounds":29}}`:                     {"agency.turn_checkpoint_grace_rounds 29 is too many to leave a round of agency.max_tool_rounds (30) for a checkpoint — no turn of the identity's would be checkpointed or continued: 28 is in force", 28},
		`{"agency":{"turn_checkpoint_grace_rounds":8,"max_tool_rounds":9}}`:  {"agency.turn_checkpoint_grace_rounds 8 is too many to leave a round of agency.max_tool_rounds (9) for a checkpoint — no turn of the identity's would be checkpointed or continued: 7 is in force", 7},
		`{"agency":{"turn_checkpoint_grace_rounds":8,"max_tool_rounds":10}}`: {"", 8},
		`{"agency":{"max_tool_rounds":3}}`:                                   {"", 4}, // the default grace beside a small ceiling: as it has always run
	} {
		path := filepath.Join(dir, "held.json")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		logs := logsink.CaptureForTest(t)
		cfg, err := ReadConfig(path)
		if err != nil {
			t.Errorf("%s: A RULE BETWEEN TWO SETTINGS STOPPED THE LOAD: %v", body, err)
			continue
		}
		held := cfg.cognitionHeld["agency.turn_checkpoint_grace_rounds"]
		if got := graceRoundsSetting.n(*cfg); got != c.held {
			t.Errorf("%s: a grace of %d is in force; want %d", body, got, c.held)
		}
		if c.said == "" {
			if held != "" || validCognition(*cfg) != nil {
				t.Errorf("%s: a grace that fits was held or would be refused: %q %v", body, held, validCognition(*cfg))
			}
			continue
		}
		if !strings.Contains(held, c.said) || !strings.Contains(held, fmt.Sprintf("set it at most %d", c.held)) || !strings.Contains(logs.String(), held) {
			t.Errorf("%s: the loader's sentence is %q; want it to say %q and to advise at most %d, in the log too", body, held, c.said, c.held)
		}
		if err := validCognition(*cfg); err != nil {
			t.Errorf("%s: what the loader holds is not what the door would take: %v", body, err)
		}
	}
	for _, key := range []string{"turn_checkpoint_calls", "turn_checkpoint_grace_rounds", "max_turn_continuations"} {
		path := filepath.Join(dir, "negative.json")
		if err := os.WriteFile(path, []byte(`{"agency":{"`+key+`":-1}}`), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadConfig(path); !errors.As(err, &refusal) || refusal.Key != "agency."+key {
			t.Errorf("agency.%s below zero must be refused by its key: %v", key, err)
		}
	}
}

// THE GRACE LEAVES A CHILD WITH LEGS A ROUND TOO. A child that continues
// itself in legs is held to the same checkpoint, under its own round
// ceiling, agency.subagent_max_tool_rounds, which is never above the
// resident's. The rule between the grace and the rounds read the
// resident's ceiling alone: a grace of nine passed beside a child's ten
// rounds, and no leg of any child was ever asked to checkpoint; each ran
// to its round ceiling. A child without legs has no checkpoint, so its
// ceiling does not bound the grace.
func TestTheGraceLeavesAChildWithLegsARoundForItsCheckpoint(t *testing.T) {
	app, dir := dispatchApp(t)
	defer stopApp(t, app)
	app.cfgMu.Lock()
	app.cfg.Agency.SubagentMaxToolRounds = limits.Of(10) // the resident's stays thirty
	app.cfgMu.Unlock()
	var refusal *CognitionRefusal
	_, err := app.applyConfigChange(map[string]any{"agency.turn_checkpoint_grace_rounds": float64(9)})
	if !errors.As(err, &refusal) || refusal.Key != "agency.turn_checkpoint_grace_rounds" ||
		!strings.Contains(refusal.Requirement, "9 leaves no round of agency.subagent_max_tool_rounds (10) for a checkpoint — no leg of a child's would be checkpointed or continued, though the identity's own turns still would") ||
		!strings.Contains(refusal.Requirement, "set at most 8, or 0 for the default (4)") {
		t.Errorf("a grace of 9 beside a child's 10 rounds must be refused by the grace's key, naming the child's ceiling: %v", err)
	}
	if _, err := app.applyConfigChange(map[string]any{"agency.turn_checkpoint_grace_rounds": float64(8)}); err != nil {
		t.Errorf("a grace of 8 leaves a child's 10 rounds a round and was refused: %v", err)
	}
	off := false
	app.cfgMu.Lock()
	app.cfg.Agency.SubagentContinuation = &off
	app.cfgMu.Unlock()
	if _, err := app.applyConfigChange(map[string]any{"agency.turn_checkpoint_grace_rounds": float64(9)}); err != nil {
		t.Errorf("with a child's legs switched off a grace of 9 under the resident's 30 rounds was refused: %v", err)
	}

	for body, said := range map[string]string{
		`{"agency":{"turn_checkpoint_grace_rounds":9,"subagent_max_tool_rounds":10}}`:                               "agency.turn_checkpoint_grace_rounds 9 is too many to leave a round of agency.subagent_max_tool_rounds (10) for a checkpoint — no leg of a child's would be checkpointed or continued, though the identity's own turns still would: 8 is in force",
		`{"agency":{"turn_checkpoint_grace_rounds":8,"subagent_max_tool_rounds":10}}`:                               "",
		`{"agency":{"turn_checkpoint_grace_rounds":9,"subagent_max_tool_rounds":10,"subagent_continuation":false}}`: "",
	} {
		path := filepath.Join(dir, "held.json")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg, err := ReadConfig(path)
		if err != nil {
			t.Errorf("%s: A RULE BETWEEN TWO SETTINGS STOPPED THE LOAD: %v", body, err)
			continue
		}
		held := cfg.cognitionHeld["agency.turn_checkpoint_grace_rounds"]
		if said == "" {
			if held != "" || validCognition(*cfg) != nil {
				t.Errorf("%s: a grace that fits was held or would be refused: %q %v", body, held, validCognition(*cfg))
			}
			continue
		}
		if !strings.Contains(held, said) || graceRoundsSetting.value(*cfg) != limits.Of(8) || validCognition(*cfg) != nil {
			t.Errorf("%s: the loader's sentence is %q and %s grace rounds are in force; want it to say %q and 8, the most that fits, in force",
				body, held, graceRoundsSetting.value(*cfg), said)
		}
	}
}

func TestTheOperatorsSlackReachesTheTurn(t *testing.T) {
	app, _ := dispatchApp(t)
	defer stopApp(t, app)
	if _, err := app.applyConfigChange(map[string]any{"agency.turn_checkpoint_slack": float64(3)}); err != nil {
		t.Fatalf("the door refused a slack of 3: %v", err)
	}
	if got := loopCheckpoint(app); got.Slack != 3 {
		t.Errorf("after the door's change the loop holds a slack of %d for its next turn; want 3", got.Slack)
	}
	if got := stepsSentence(t, app.buildToolDefinitions()); !strings.Contains(got, "the turn checkpoints at three times this many calls") {
		t.Errorf("after the door's change the tool list says: %s", got)
	}
}

func TestAGraceIsHeldToTheMostThatFitsAndTheAdviceIsTrue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "held.json")
	if err := os.WriteFile(path, []byte(`{"agency":{"turn_checkpoint_grace_rounds":4,"subagent_max_tool_rounds":5}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := ReadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	held := cfg.cognitionHeld["agency.turn_checkpoint_grace_rounds"]
	if got := graceRoundsSetting.n(*cfg); got != 3 || validCognition(*cfg) != nil {
		t.Errorf("a grace of 4 beside a child's 5 rounds is held to %d (%v); want 3, the most that leaves a round", got, validCognition(*cfg))
	}
	if want := "no leg of a child's would be checkpointed or continued, though the identity's own turns still would: 3 is in force"; !strings.Contains(held, want) || strings.Contains(held, "the default") {
		t.Errorf("the loader's sentence is %q; want it to say %q and not to call the default a cure", held, want)
	}

	// No grace fits under a child's two rounds. From a file the default
	// stays, and the sentence says that it does not fit either and which
	// ceiling to raise; the door advises raising it, never "set at most 0",
	// and offers no default that does not fit.
	path = filepath.Join(t.TempDir(), "none.json")
	if err := os.WriteFile(path, []byte(`{"agency":{"turn_checkpoint_grace_rounds":1,"subagent_max_tool_rounds":2}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if cfg, err = ReadConfig(path); err != nil {
		t.Fatal(err)
	}
	want := "agency.turn_checkpoint_grace_rounds 1 is too many to leave a round of agency.subagent_max_tool_rounds (2) for a checkpoint, and no grace leaves one; " +
		"until it is raised to at least 3, no leg of a child's would be checkpointed or continued, though the identity's own turns still would: " +
		"the default, 4, is in force, and the file is left as it is; raise agency.subagent_max_tool_rounds to at least 3, or leave the grace unset"
	if held := cfg.cognitionHeld["agency.turn_checkpoint_grace_rounds"]; held != want || graceRoundsSetting.value(*cfg) != limits.Of(4) {
		t.Errorf("a file's grace beside a child's two rounds loads as %s, said %q; want the default, said %q", graceRoundsSetting.value(*cfg), held, want)
	}

	app, _ := dispatchApp(t)
	defer stopApp(t, app)
	app.cfgMu.Lock()
	app.cfg.Agency.SubagentMaxToolRounds = limits.Of(2)
	app.cfgMu.Unlock()
	var refusal *CognitionRefusal
	_, err = app.applyConfigChange(map[string]any{"agency.turn_checkpoint_grace_rounds": float64(1)})
	if !errors.As(err, &refusal) || !strings.Contains(refusal.Requirement, "raise agency.subagent_max_tool_rounds to at least 3, or leave the grace unset") ||
		strings.Contains(refusal.Requirement, "at most 0") || strings.Contains(refusal.Requirement, "for the default") {
		t.Errorf("a grace beside a child's two rounds is refused as: %v", err)
	}
}

func TestAResidentTurnEndsAtTheOperatorsCheckpoint(t *testing.T) {
	app, dir := dispatchApp(t)
	defer stopApp(t, app)
	if _, err := app.applyConfigChange(map[string]any{"agency.turn_checkpoint_calls": float64(2), "agency.turn_checkpoint_grace_rounds": float64(1)}); err != nil {
		t.Fatal(err)
	}
	if err := writeFileForTest(filepath.Join(dir, "probe.txt"), "probe"); err != nil {
		t.Fatal(err)
	}
	obs := &contScript{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		n := obs.add(string(body))
		w.Header().Set("Content-Type", "application/json")
		// It reads for ever: only a turn's ending is answered in words.
		if !strings.Contains(string(body), "Budget checkpoint: reply now") && !strings.Contains(string(body), "round limit. Please respond") {
			_, _ = io.WriteString(w, callReply("", toolCall(fmt.Sprintf("c%d", n), "read", `{"file_path":"probe.txt"}`)))
			return
		}
		_, _ = io.WriteString(w, textReply("stopped at the checkpoint"))
	}))
	defer srv.Close()
	app.llmSwap.Swap(llm.New(&llm.ClientConfig{Endpoint: srv.URL, Model: "fake", MaxOutputTokens: 4096, Retries: -1}))

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if err := app.acquireTurn(ctx); err != nil {
		t.Fatalf("acquire turn: %v", err)
	}
	if _, err := app.observeChat(ctx, "read the probe until you are stopped", func(kind, name, args string) {}); err != nil {
		t.Fatalf("turn failed: %v", err)
	}

	reqs := obs.snapshot()
	if len(reqs) != 4 {
		t.Fatalf("the turn took %d requests, want 4: two reads to the floor, one grace round, one ending without tools", len(reqs))
	}
	for i, req := range reqs[:3] {
		if !strings.Contains(req, "(with none declared, at 2), gives you one more round to say") {
			t.Errorf("request %d's tool list does not state the checkpoint the turn is held to", i+1)
		}
	}
	if ask := jsonStringFrom(reqs[2], "This turn has spent 2 calls with no declared budget"); !strings.Contains(ask, "You have 1 more round;") {
		t.Errorf("the third request does not carry the checkpoint ask with the operator's grace: %q", ask)
	}
	if !strings.Contains(reqs[3], "Budget checkpoint: reply now") {
		t.Error("the fourth request is not the checkpoint's ending")
	}
}

// A CHILD WITH LEGS IS HELD TO THE SAME CHECKPOINT, and its own tool list
// says so. A floor of 2 and a grace of 1 end its first leg after three
// reads, with seventeen rounds of its ceiling unspent, and the second leg
// delivers; under the constants the three reads were one leg.
func TestAChildLegEndsAtTheOperatorsCheckpoint(t *testing.T) {
	srv, _ := scriptedChild(t, 3)
	a, st, _ := legFixture(t, srv.URL, AgencyConfig{MaxToolRounds: limits.Of(20), SubagentMaxToolRounds: limits.Of(20), SubagentMaxToolCalls: limits.Of(64), SubagentMaxLegs: limits.Of(4), SubagentWallSeconds: 5,
		TurnCheckpointCalls: limits.Of(2), TurnCheckpointGraceRounds: 1})
	id := spawnChild(t, a, st, "list the directory until stopped")
	ws := waitDelivered(t, st, id)
	if !strings.Contains(ws.Result, "legs=2/4") {
		t.Fatalf("the child's first leg did not end at the operator's checkpoint: %q", ws.Result)
	}
}

// THE CHAIN'S BOUND IS THE OPERATOR'S, AND READ WHEN A TURN ENDS. Two
// continuations in a row and the third stands down; raised, the next
// capped turn is continued again. (That the operator's message starts a
// new chain is held beside the default bound:
// TestTheContinuationChainStandsDownAfterEightLegs.)
func TestTheContinuationChainStandsDownAtTheOperatorsBound(t *testing.T) {
	app, _ := dispatchApp(t)
	bound := func(n int) {
		app.cfgMu.Lock()
		app.cfg.Agency.MaxTurnContinuations = limits.Of(n)
		app.cfgMu.Unlock()
	}
	bound(2)
	if err := app.store.StartWorkSession(t.Context(), "ws_bound", "a task that never finishes"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := app.acquireTurn(ctx); err != nil {
		t.Fatalf("acquire turn: %v", err)
	}
	// The legs wait behind the held gate and none reaches a model: what is
	// under test is the decision (as in the test of the default bound).
	t.Cleanup(func() {
		stopped := make(chan struct{})
		go func() { stopApp(t, app); close(stopped) }()
		<-app.bgCtx.Done()
		app.releaseTurn()
		select {
		case <-stopped:
		case <-time.After(15 * time.Second):
			t.Error("Stop did not return after the waiting legs were cancelled")
		}
	})

	logs := logsink.CaptureForTest(t)
	for leg := 1; leg <= 2; leg++ {
		if scheduled, why := app.scheduleContinuation(capDeclared, ""); !scheduled {
			t.Fatalf("capped turn %d was not continued (%q); the bound is two", leg, why)
		}
	}
	if scheduled, _ := app.scheduleContinuation(capDeclared, ""); scheduled {
		t.Fatal("a third consecutive capped turn was continued past agency.max_turn_continuations 2")
	}
	if want := "2 consecutive capped turns on ws_bound, and agency.max_turn_continuations is 2"; !strings.Contains(logs.String(), want) {
		t.Errorf("the stand-down does not say the chain and the bound (%q):\n%s", want, logs.String())
	}
	bound(10)
	if scheduled, why := app.scheduleContinuation(capDeclared, ""); !scheduled {
		t.Fatalf("the bound raised to 10 and the fourth capped turn was still refused (%q): it is not read when the turn ends", why)
	}
}

// THE RELOAD'S LINE STATES THE THREE, as it states the other ceilings.
func TestAReloadStatesTheTurnShape(t *testing.T) {
	out := reloadLog(t, func(c *Config) {
		c.Agency.TurnCheckpointCalls, c.Agency.TurnCheckpointGraceRounds, c.Agency.MaxTurnContinuations = limits.Of(500), 2, limits.Of(9)
	})
	if want := "its checkpoint at 500 calls undeclared or 2 times a declared estimate, with 2 rounds after, 9 continuations in a row"; !strings.Contains(out, want) {
		t.Errorf("the reload did not state the turn's shape (%q):\n%s", want, out)
	}
}
