package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/identity"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

// THE WORK VERB'S TWO BOUNDS ARE THE OPERATOR'S, FROM THE DOOR, AT THE NEXT
// WRITE (agency.work_line_max_chars, prompt.standing_max_chars). On a
// booted identity, with nothing set, the defaults hold: a standing state at
// its default is taken and shown whole in the working state every
// prompt carries, and one more character is refused naming the setting. Set at the
// settings door, the next write is held to the operator's number, which
// can only reach the verb through the port both boots wire.
func TestTheWorkBoundsAreTheOperatorsFromTheDoorAtTheNextWrite(t *testing.T) {
	app, _ := dispatchApp(t)
	defer stopApp(t, app)
	ctx := context.Background()
	work := func(args map[string]any) (string, error) {
		return app.engine.ExecuteAction(ctx, "verb", "work", args)
	}

	at := strings.Repeat("s", identity.DefaultStandingMaxChars-1) + "Z"
	if _, err := work(map[string]any{"action": "update", "standing": at}); err != nil {
		t.Fatalf("a standing state at the default bound (%d) was refused: %v", identity.DefaultStandingMaxChars, err)
	}
	facts, err := app.buildWorkState(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(facts, "\n"+at) {
		t.Fatalf("a standing state at the bound is not shown whole in the working state (%d characters)", len(at))
	}
	if _, err := work(map[string]any{"action": "update", "standing": at + "s"}); err == nil || !strings.Contains(err.Error(), fmt.Sprintf("is %d characters, and your operator's setting prompt.standing_max_chars allows at most %d", identity.DefaultStandingMaxChars+1, identity.DefaultStandingMaxChars)) {
		t.Fatalf("one over the default standing bound: got %v, want the refusal naming prompt.standing_max_chars", err)
	}

	if _, err := app.applyConfigChange(map[string]any{"prompt.standing_max_chars": float64(200), "agency.work_line_max_chars": float64(150)}); err != nil {
		t.Fatalf("the door refused the two bounds: %v", err)
	}
	if _, err := work(map[string]any{"action": "update", "standing": strings.Repeat("s", 200)}); err != nil {
		t.Fatalf("a standing state at the operator's 200 was refused: %v", err)
	}
	if _, err := work(map[string]any{"action": "update", "standing": strings.Repeat("s", 201)}); err == nil || !strings.Contains(err.Error(), "allows at most 200") {
		t.Fatalf("a standing state over the operator's 200: got %v", err)
	}
	if _, err := work(map[string]any{"action": "start", "description": "bounded"}); err != nil {
		t.Fatal(err)
	}
	if _, err := work(map[string]any{"action": "update", "next_move": strings.Repeat("n", 150)}); err != nil {
		t.Fatalf("a next move at the operator's 150 was refused: %v", err)
	}
	if _, err := work(map[string]any{"action": "update", "next_move": strings.Repeat("n", 151)}); err == nil || !strings.Contains(err.Error(), "agency.work_line_max_chars allows at most 150") {
		t.Fatalf("a next move over the operator's 150: got %v", err)
	}
}

// THE PLAYBOOK IS CUT AT THE OPERATOR'S BOUND (prompt.playbook_max_bytes),
// AND AT THE DEFAULT THE CUT IS THE ONE THE CODE PRINTED. With nothing set
// a long playbook shows its first 2,000 bytes and the line the code
// carried, byte for byte; set at the door, the next push cuts where the
// operator said and says that number.
func TestThePlaybookIsCutAtTheOperatorsBound(t *testing.T) {
	app, home := stockSlotApp(t, nil)
	book := filepath.Join(home, playbookRel("long"))
	if err := os.MkdirAll(filepath.Dir(book), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(book, []byte(strings.Repeat("a", 5000)), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	text, err := app.playbook(ctx, "long")
	if want := strings.Repeat("a", 2000) + "\n… (cut at 2000 bytes; read playbooks/long/SKILL.md for the rest)"; err != nil || text != want {
		t.Fatalf("at the default the playbook is not what the code showed: err %v, %d bytes ending %q", err, len(text), text[max(0, len(text)-70):])
	}
	if _, err := app.applyConfigChange(map[string]any{"prompt.playbook_max_bytes": float64(500)}); err != nil {
		t.Fatalf("the door refused prompt.playbook_max_bytes 500: %v", err)
	}
	text, err = app.playbook(ctx, "long")
	if want := strings.Repeat("a", 500) + "\n… (cut at 500 bytes; read playbooks/long/SKILL.md for the rest)"; err != nil || text != want {
		t.Fatalf("at the operator's 500 the playbook is %d bytes ending %q (err %v)", len(text), text[max(0, len(text)-70):], err)
	}
}

func TestTheRhythmLineLooksBackFortyEightHours(t *testing.T) {
	projection, err := store.NewMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer projection.Close()
	now := time.Now()
	for _, at := range []time.Time{now, now.Add(-30 * time.Hour), now.Add(-50 * time.Hour)} {
		if err := projection.InsertTurnMetric(store.TurnMetric{TsMs: at.UnixMilli(), Calls: 4, ReadOnly: 2}); err != nil {
			t.Fatal(err)
		}
	}
	line := func(cfg *Config) string {
		t.Helper()
		ws, err := (&App{store: projection, cfg: cfg}).buildTurnFacts(t.Context(), false)
		if err != nil {
			t.Fatal(err)
		}
		i := strings.Index(ws, "### Rhythm")
		if i < 0 {
			t.Fatalf("no rhythm line:\n%s", ws)
		}
		return strings.SplitN(ws[i:], "\n", 2)[0]
	}
	if got, want := line(&Config{}), "### Rhythm — last 48h: 2 turns, 8 calls, 50% read-only, 0 spawns, 0 harvests"; got != want {
		t.Fatalf("the rhythm line is\n%q\nwant the line the code printed, over 48 hours\n%q", got, want)
	}
}

// boundProvider is a plugin's context provider for one plugin of its own,
// recording the time its read was given.
type boundProvider struct {
	name, plugin string
	mu           sync.Mutex
	given        []time.Duration
}

func (p *boundProvider) Name() string               { return p.name }
func (p *boundProvider) Description() string        { return "Read local task context" }
func (p *boundProvider) Parameters() map[string]any { return map[string]any{"type": "object"} }
func (p *boundProvider) ContextProvider() bool      { return true }
func (p *boundProvider) DispatchOwner() any         { return p }
func (p *boundProvider) Execute(ctx context.Context, _ map[string]any) (tools.Result, error) {
	p.mu.Lock()
	if d, ok := ctx.Deadline(); ok {
		p.given = append(p.given, time.Until(d))
	}
	p.mu.Unlock()
	return tools.Result{Output: `{"text":"context from ` + p.plugin + `","detail_route":"search"}`}, nil
}

type boundSearch struct{ boundProvider }

func (*boundSearch) ContextProvider() bool { return false }
func (s *boundSearch) Discovery() tools.Discovery {
	return tools.Discovery{Plugin: s.plugin, Operation: "search", Effects: "read.internal", MaxResultBytes: 4096, Primary: true}
}

// THE PLUGIN CONTEXT READ IS THE OPERATOR'S (plugins.context_providers,
// plugins.context_timeout_ms), AND AT THE DEFAULT IT IS THE READ THE CODE
// MADE. Three plugins offer context. With nothing set two are read, each
// given at most 250 ms, and the third is named as not read in the line the
// code carried, byte for byte. With the operator's three and 40 ms, all
// three are read, each given at most 40 ms, and nothing is named as left.
func TestThePluginContextReadIsTheOperatorsBound(t *testing.T) {
	read := func(cfg *Config) ([]*boundProvider, []string, string) {
		t.Helper()
		a := New(cfg)
		a.toolReg = tools.NewRegistry(t.TempDir(), nil, tools.Timeouts{})
		var ps []*boundProvider
		for _, n := range []string{"a", "b", "c"} {
			plugin := "org.example.context" + n
			if err := a.toolReg.RegisterDynamic(&boundSearch{boundProvider{name: "pl_" + n + "_search", plugin: plugin}}, plugin); err != nil {
				t.Fatal(err)
			}
			p := &boundProvider{name: "pl_" + n + "_context", plugin: plugin}
			if err := a.toolReg.RegisterHostOp(p, plugin); err != nil {
				t.Fatal(err)
			}
			ps = append(ps, p)
		}
		var names []string
		left := ""
		for _, s := range a.pluginContext(t.Context(), "the task") {
			names = append(names, s.Name)
			if s.Name == "Additional plugin context" {
				left = s.Content
			}
		}
		return ps, names, left
	}
	given := func(p *boundProvider) []time.Duration {
		p.mu.Lock()
		defer p.mu.Unlock()
		return append([]time.Duration(nil), p.given...)
	}

	ps, names, left := read(&Config{})
	if len(given(ps[0])) != 1 || len(given(ps[1])) != 1 || len(given(ps[2])) != 0 {
		t.Fatalf("with nothing set the providers read were %d, %d, %d times; want the first two once each", len(given(ps[0])), len(given(ps[1])), len(given(ps[2])))
	}
	if want := "Additional selected plugin context was not requested within this decision's bounded read limit."; left != want {
		t.Fatalf("with nothing set the line for the provider not read is %q; want the line the code carried %q (sections %v)", left, want, names)
	}
	for _, p := range ps[:2] {
		if d := given(p)[0]; d > 250*time.Millisecond || d <= 0 {
			t.Fatalf("with nothing set %s was given %s; want at most the 250 ms the code carried", p.name, d)
		}
	}

	ps, names, left = read(&Config{Plugins: PluginsConfig{ContextProviders: 3, ContextTimeoutMs: 40}})
	for _, p := range ps {
		g := given(p)
		if len(g) != 1 {
			t.Fatalf("at the operator's three providers %s was read %d times (sections %v)", p.name, len(g), names)
		}
		if g[0] > 40*time.Millisecond || g[0] <= 0 {
			t.Fatalf("at the operator's 40 ms %s was given %s", p.name, g[0])
		}
	}
	if left != "" {
		t.Fatalf("with every provider read, one was still named as not read: %q", left)
	}
}

// THE UPDATE CHECK'S CADENCE IS THE OPERATOR'S, FROM THE DOOR TO THE
// CHECKER AND THE CARD (updates.check_interval_minutes). On a booted
// identity with nothing set the checker's period is the hour the code
// carried and the card is told 60, which it says as "Checks run once an
// hour."; set at the door to 30, the checker the boot wired reads 30
// minutes and the card is told 30.
func TestTheUpdateCadenceIsTheOperatorsFromTheDoorToTheChecker(t *testing.T) {
	app, _ := dispatchApp(t)
	defer stopApp(t, app)
	if app.updateChecker == nil {
		t.Fatal("rig: the live boot built no update checker")
	}
	if got := app.updateChecker.Interval(); got != time.Hour {
		t.Fatalf("with nothing set the checker waits %s; want the hour the code carried", got)
	}
	if got := app.updateStateView().CheckIntervalMinutes; got != 60 {
		t.Fatalf("with nothing set the card is told %d minutes; want 60", got)
	}
	if _, err := app.applyConfigChange(map[string]any{"updates.check_interval_minutes": float64(30)}); err != nil {
		t.Fatalf("the door refused updates.check_interval_minutes 30: %v", err)
	}
	if got := app.updateChecker.Interval(); got != 30*time.Minute {
		t.Fatalf("at the operator's 30 the checker waits %s", got)
	}
	if got := app.updateStateView().CheckIntervalMinutes; got != 30 {
		t.Fatalf("at the operator's 30 the card is told %d minutes", got)
	}
}
