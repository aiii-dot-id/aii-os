package app

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/conversation"
	"github.com/aiii-dot-id/aii-os/internal/limits"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/updates"
)

func TestALocalModelsFenceIsATimeWhileNoOneSetsATokenFence(t *testing.T) {
	local := providerEntry{Name: "Local server", Local: true}
	hosted := providerEntry{Name: "Hosted"}
	for _, tc := range []struct {
		name   string
		cfg    Config
		entry  providerEntry
		tokens limits.Ceiling
		level  string
		time   time.Duration
	}{
		{"a local entry with nothing set", *defaultConfig(), local, limits.Unlimited, "local", 45 * time.Minute},
		{"a local entry whose provider sets a token fence", configWith(t, `{"Local server":{"agency":{"turn_token_budget":300000}}}`), local, limits.Of(300_000), "provider", 0},
		{"a local entry under a main token fence the operator set", *readConfigFile(t, `{"agency":{"turn_token_budget":900000}}`), local, limits.Of(900_000), "main", 0},
		{"a local entry with its time set", *readConfigFile(t, `{"agency":{"turn_seconds_local":600}}`), local, limits.Unlimited, "local", 10 * time.Minute},
		{"a local entry whose provider lifts the token fence", configWith(t, `{"Local server":{"agency":{"turn_token_budget":"unlimited"}}}`), local, limits.Unlimited, "provider", 0},
		{"a hosted entry with nothing set", *defaultConfig(), hosted, limits.Of(limits.DefaultTurnTokens), "default", 0},
		{"a hosted entry with the local time set", *readConfigFile(t, `{"agency":{"turn_seconds_local":600}}`), hosted, limits.Of(limits.DefaultTurnTokens), "default", 0},
	} {
		l := limitsFor(tc.cfg, tc.entry, "m")
		b := l.residentBounds()
		if b.TurnTokens != tc.tokens || l.Source[turnTokenBudgetSetting.key].Level != tc.level || b.TurnTime != tc.time {
			t.Errorf("%s: fenced at %s tokens (from %q) and a time of %s; want %s (from %q) and %s",
				tc.name, b.TurnTokens, l.Source[turnTokenBudgetSetting.key].Level, b.TurnTime, tc.tokens, tc.level, tc.time)
		}
	}
}

// The resident is held to it: switched to a local entry, its loop's next
// turn is fenced at the time and not the count; switched back, at the main
// token fence and no time. A child on a local route is given the same.
func TestTheResidentOnALocalModelIsHeldToItsTimeFence(t *testing.T) {
	app, _ := dispatchApp(t)
	defer stopApp(t, app)
	cfg := app.configSnapshot()
	switchTo := func(e providerEntry, model string) {
		t.Helper()
		if err := app.acquireTurn(t.Context()); err != nil {
			t.Fatal(err)
		}
		app.activateLLMRuntime(llm.New(&llm.ClientConfig{Endpoint: e.URL, Provider: "openai", Model: model}), e, cfg)
		app.releaseTurn()
	}
	switchTo(providerEntry{Name: "Local server", Local: true, URL: "http://127.0.0.1:1/v1", APIType: "openai"}, "small")
	if tokens, fence := loopFences(app); tokens != limits.Unlimited || fence != 45*time.Minute {
		t.Fatalf("on the local model the resident is fenced at %s tokens and %s; want no token fence and 45m0s", tokens, fence)
	}
	switchTo(providerEntry{Name: "test", URL: "https://x", APIType: "openai"}, "m")
	if tokens, fence := loopFences(app); tokens != limits.Of(limits.DefaultTurnTokens) || fence != 0 {
		t.Fatalf("switched back, the resident is fenced at %s tokens and %s; want the main fence and no time", tokens, fence)
	}
}

// loopFences is what the resident's loop holds for its next turn: the token
// fence and the time fence.
func loopFences(a *App) (limits.Ceiling, time.Duration) {
	a.cfgMu.RLock()
	defer a.cfgMu.RUnlock()
	l := reflect.ValueOf(a.conv).Elem().FieldByName("cfg")
	f := l.FieldByName("TurnTokenBudget")
	return limits.Ceiling{N: int(f.FieldByName("N").Int()), Unlimited: f.FieldByName("Unlimited").Bool()}, time.Duration(l.FieldByName("TurnTime").Int())
}

// The time is global, as the local wall is: an override that sets it is
// refused, naming what to set on the provider instead.
func TestTheLocalTimeFenceIsGlobalAndAnOverrideOfItIsRefusedWithTheRemedy(t *testing.T) {
	err := loadRefusal(t, `{"Local server":{"agency":{"turn_seconds_local":600}}}`)
	var refusal *CognitionRefusal
	if !errors.As(err, &refusal) || !strings.Contains(err.Error(), "set agency.turn_token_budget on the local provider to fence it in tokens instead") {
		t.Fatalf("an override of the local time loaded, or was refused without its remedy: %v", err)
	}
}

// A turn the time fence ended is continued as one the token fence ended,
// and says which fence it was: to the fresh leg and in a child's notice.
func TestATurnEndedByItsTimeFenceIsContinuedAndSaysSo(t *testing.T) {
	r := capReasonOf(conversation.Result{ContinuedAtTimeFence: true})
	if r != capTimeFence || r.how() != "because it spent its time" {
		t.Fatalf("a turn ended at its time fence reads as %v, %q", r, r.how())
	}
	notice := subagentNotice("inspect the lantern", "partial: UNFINISHED — the sub-agent stopped "+r.how()+" before it was done")
	if !strings.HasSuffix(notice, "partial (time)") {
		t.Fatalf("a child's notice does not name the time fence: %q", notice)
	}
}

// THE SETTING SURVIVES A ROLLBACK TO 0.1.23. 0.1.23 reads config.json
// strictly and refuses a key it does not know. This one is written only
// when the operator sets it: a configuration that leaves it unset carries
// no such key. Set through the door during an update that has not settled,
// it is in the newer file only: the rollback puts back the bytes the
// previous version left, which never held it, and keeps the newer file
// beside them.
func TestTheLocalTimeFenceIsWrittenOnlyWhenSetAndARollbackPutsBackTheFileWithoutIt(t *testing.T) {
	if raw := readText(t, writeConfigFile(t)); strings.Contains(raw, "turn_seconds_local") {
		t.Fatal("rig: the previous version's file names the key")
	}
	if raw, err := json.Marshal(*defaultConfig()); err != nil || strings.Contains(string(raw), "turn_seconds_local") {
		t.Fatalf("a configuration that leaves the local time unset writes it: %v", err)
	}
	path := writeConfigFile(t)
	keepConfigUnderUpdate(path, Current(), updates.ErrBootRecoveryPending)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	a := New(cfg)
	if _, err := a.applyConfigChange(map[string]any{"agency.turn_seconds_local": float64(600)}); err != nil {
		t.Fatalf("the door did not take the local time: %v", err)
	}
	newer := readText(t, path)
	if !strings.Contains(newer, `"turn_seconds_local": 600`) {
		t.Fatalf("the door set the local time and the file does not hold it, so this proves nothing: %s", newer)
	}
	if err := putBackConfigKept(path, Current(), true); err != nil {
		t.Fatal(err)
	}
	if got := readText(t, path); got != before || strings.Contains(got, "turn_seconds_local") {
		t.Fatalf("the restored program reads %q, not the bytes it left", got)
	}
	if got := readText(t, path+configRolledBack); got != newer {
		t.Fatalf("what the newer version left must be kept beside the file: %q", got)
	}
}

// A child routed to a local model is fenced the same way: its token fence
// lifted and its time the local one, while a child on a hosted model keeps
// the count.
func TestAChildRoutedToALocalModelIsFencedByTime(t *testing.T) {
	app, dir := dispatchApp(t)
	t.Cleanup(func() { stopApp(t, app) })
	writeTestProviders(t, dir,
		providerEntry{Name: "test", APIType: "openai", URL: "https://x", APIKey: "sk-x", DefaultModel: "m", Default: true},
		providerEntry{Name: "Bench", APIType: "openai", URL: "http://127.0.0.1:1/v1", APIKey: "sk-b", DefaultModel: "bench-1", Models: []string{"bench-1"}, Local: true})
	app.cfgMu.Lock()
	app.cfg.Agency.Roles = map[string]RoleRoute{"bench": {Provider: "Bench", Model: "bench-1"}}
	app.cfgMu.Unlock()
	target := app.resolveRunTarget("bench")
	if target.fellBack() || target.provider != "Bench" {
		t.Fatalf("rig: the role did not route: %+v", target.cause)
	}
	if got := target.limits.Value(turnTokenBudgetSetting); got != limits.Unlimited || target.limits.turnTime != 45*time.Minute {
		t.Fatalf("a child on a local model is fenced at %s tokens and %s; want no token fence and 45m0s", got, target.limits.turnTime)
	}
	if active := app.resolveRunTarget(""); active.limits.turnTime != 0 || active.limits.Value(turnTokenBudgetSetting) != limits.Of(limits.DefaultTurnTokens) {
		t.Fatalf("a child on the hosted model is fenced at %s tokens and %s", active.limits.Value(turnTokenBudgetSetting), active.limits.turnTime)
	}
}
