package broker

import (
	"context"
	"encoding/json"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/memory"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
)

// memory_test.go — the memory operations under the same hostile
// battery as kv: the three rings, structural scoping, the smart
// ingest's three outcomes, explicit supersession, the recall
// arguments and the detail read, the tier's temp scope, the ceilings,
// and SAFE.

func memParams(op, args string) string {
	if args == "" {
		args = "{}"
	}
	return `{"operation":"` + op + `","arguments":` + args + `}`
}

func memHost(t *testing.T, cfg Config) (*Host, *Binding) {
	t.Helper()
	st := newStore(t)
	if cfg.Grants == nil {
		cfg.Grants = map[string]Grant{"p": {Memory: true}}
	}
	h := newHost(t, st, cfg)
	return h, h.Bind("p", packagefmt.TierT1, []string{"ring4.memory"})
}

type rememberedResult struct {
	ID        string `json:"id"`
	Outcome   string `json:"outcome"`
	Of        string `json:"of"`
	CreatedAt string `json:"created_at"`
	Scope     string `json:"scope"`
}

type recallResult struct {
	Hits []struct {
		ID          string  `json:"id"`
		Text        string  `json:"text"`
		Snippet     string  `json:"snippet"`
		Match       string  `json:"match"`
		Score       float64 `json:"score"`
		Strength    float64 `json:"strength"`
		Attribution string  `json:"attribution"`
		Ring        int     `json:"ring"`
		Time        string  `json:"time"`
		Accesses    int64   `json:"accesses"`
		Class       string  `json:"class"`
	} `json:"hits"`
	Status    string `json:"status"`
	Matched   int    `json:"matched"`
	Shown     int    `json:"shown"`
	Policy    string `json:"policy"`
	Truncated bool   `json:"truncated"`
}

func remember(t *testing.T, b *Binding, args string) rememberedResult {
	t.Helper()
	m := dispatch(t, b, memParams("memory.remember", args))
	wantResult(t, m, statusSucceeded, "")
	var r rememberedResult
	if err := json.Unmarshal(m["operation_result"], &r); err != nil {
		t.Fatal(err)
	}
	return r
}

func recall(t *testing.T, b *Binding, args string) recallResult {
	t.Helper()
	m := dispatch(t, b, memParams("memory.recall", args))
	wantResult(t, m, statusSucceeded, "")
	var r recallResult
	if err := json.Unmarshal(m["operation_result"], &r); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestMemoryRunsTheThreeRings(t *testing.T) {
	st := newStore(t)
	h := newHost(t, st, Config{Grants: map[string]Grant{"p": {Memory: true}, "q": {KV: true}}})

	// Ring 0: the signed envelope must declare ring4.memory.
	undeclared := h.Bind("p", packagefmt.TierT1, []string{"ring4.kv"})
	wantErrorReason(t, dispatch(t, undeclared, memParams("memory.remember", `{"text":"x"}`)), reasonNotInEnvelope)
	// Ring 1: the operator must grant memory by name; kv is not memory.
	ungranted := h.Bind("q", packagefmt.TierT1, []string{"ring4.memory"})
	m := dispatch(t, ungranted, memParams("memory.recall", `{"query":"x"}`))
	wantErrorReason(t, m, reasonPolicyDeny)
	if !strings.Contains(string(m["message"]), "plugins.grants.q.memory") {
		t.Fatalf("the denial must say where the grant lives: %s", m["message"])
	}
	assertNoReceipts(t, st, "q")
	// Ring 2: the operation's own declared scope.
	granted := h.Bind("p", packagefmt.TierT1, []string{"ring4.memory"})
	granted.BeginOperation(OperationScope{Operation: "op", Declared: true, Capabilities: []string{"ring4.kv"}})
	wantErrorReason(t, dispatch(t, granted, memParams("memory.remember", `{"text":"x"}`)), reasonNotInEnvelope)
	granted.EndOperation()
	granted.BeginOperation(OperationScope{Operation: "op", Effects: EffectsReadExternal, Declared: true, Capabilities: []string{"ring4.memory"}})
	wantErrorReason(t, dispatch(t, granted, memParams("memory.remember", `{"text":"x"}`)), reasonPolicyDeny)
	// A read is admitted under a read effect class.
	r := recall(t, granted, `{"query":"x"}`)
	if r.Status != "found_nothing" {
		t.Fatalf("recall under a read scope: %+v", r)
	}
	granted.EndOperation()
}

func TestMemoryIsStructurallyScopedToThePlugin(t *testing.T) {
	st := newStore(t)
	h := newHost(t, st, Config{Grants: map[string]Grant{"a": {Memory: true}, "b": {Memory: true}}})
	a := h.Bind("a", packagefmt.TierT1, []string{"ring4.memory"})
	b := h.Bind("b", packagefmt.TierT1, []string{"ring4.memory"})

	got := remember(t, a, `{"text":"the operator prefers terse summaries"}`)
	if got.Outcome != "created" || got.ID == "" || got.Of != "" || got.Scope != "persistent" || got.CreatedAt == "" {
		t.Fatalf("first remember = %+v", got)
	}
	if r := recall(t, b, `{"query":"summaries"}`); r.Status != "found_nothing" || len(r.Hits) != 0 {
		t.Fatalf("plugin b reached plugin a's memory: %+v", r)
	}
	m := dispatch(t, b, memParams("memory.recall", `{"id":"`+got.ID+`"}`))
	wantResult(t, m, statusFailed, reasonMemoryNotFound)

	r := recall(t, a, `{"query":"summaries"}`)
	if r.Status != "found" || r.Matched != 1 || r.Shown != 1 || len(r.Hits) != 1 || r.Policy != "carrd" || r.Truncated {
		t.Fatalf("plugin a's recall = %+v", r)
	}
	hit := r.Hits[0]
	if hit.ID != got.ID || hit.Match != "both" || hit.Attribution != "plugin" || hit.Ring != 4 || hit.Time == "" || hit.Strength < 0.99 || hit.Class != "operational" || hit.Snippet == "" {
		t.Fatalf("hit = %+v", hit)
	}
	// A recall reinforces what it returned.
	if r2 := recall(t, a, `{"query":"summaries"}`); r2.Hits[0].Accesses != 1 {
		t.Fatalf("the first recall did not reinforce: accesses %d", r2.Hits[0].Accesses)
	}
	// Both plugins' calls are receipted, host-authored.
	var n int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM plugin_receipts WHERE plugin_id = 'a' AND operation LIKE 'memory.%'`).Scan(&n); err != nil || n < 3 {
		t.Fatalf("receipts for a = %d (%v), want at least 3", n, err)
	}
}

func TestRememberDecidesCreatedReinforcedOrUpdated(t *testing.T) {
	_, b := memHost(t, Config{})
	first := remember(t, b, `{"text":"the operator prefers terse summaries"}`)
	if first.Outcome != "created" {
		t.Fatalf("first = %+v", first)
	}
	again := remember(t, b, `{"text":"The operator prefers terse summaries."}`)
	if again.Outcome != "reinforced" || again.ID != first.ID || again.Of != first.ID {
		t.Fatalf("the same text again must reinforce the memory held: %+v", again)
	}
	r := recall(t, b, `{"query":"terse"}`)
	if len(r.Hits) != 1 || r.Hits[0].Accesses != 1 {
		t.Fatalf("a reinforcement writes the access record: %+v", r)
	}
	corrected := remember(t, b, `{"text":"the operator prefers terse summaries, always"}`)
	if corrected.Outcome != "updated" || corrected.Of != first.ID || corrected.ID == first.ID {
		t.Fatalf("a near-duplicate must supersede: %+v", corrected)
	}
	r = recall(t, b, `{"query":"terse summaries"}`)
	if len(r.Hits) != 1 || r.Hits[0].ID != corrected.ID {
		t.Fatalf("only the successor is current: %+v", r)
	}
	other := remember(t, b, `{"text":"the harbour bell rings at noon"}`)
	if other.Outcome != "created" {
		t.Fatalf("an unrelated text is new: %+v", other)
	}
}

func TestRememberSupersedesExplicitly(t *testing.T) {
	_, b := memHost(t, Config{})
	a := remember(t, b, `{"text":"the deploy is on Tuesday"}`)
	c := remember(t, b, `{"text":"the deploy moved to Thursday","supersedes":"`+a.ID+`"}`)
	if c.Outcome != "updated" || c.Of != a.ID {
		t.Fatalf("explicit supersession = %+v", c)
	}
	r := recall(t, b, `{"query":"deploy"}`)
	if len(r.Hits) != 1 || r.Hits[0].ID != c.ID {
		t.Fatalf("the superseded memory still surfaces: %+v", r)
	}
	// The old memory stays for audit, readable by id, marked.
	m := dispatch(t, b, memParams("memory.recall", `{"id":"`+a.ID+`"}`))
	wantResult(t, m, statusSucceeded, "")
	if !strings.Contains(string(m["operation_result"]), `"superseded_by":"`+c.ID+`"`) {
		t.Fatalf("the detail read must show the supersession: %s", m["operation_result"])
	}
	// Superseding what is not current, or not the plugin's, fails by name.
	m = dispatch(t, b, memParams("memory.remember", `{"text":"third","supersedes":"`+a.ID+`"}`))
	wantResult(t, m, statusFailed, reasonMemoryNotFound)
	m = dispatch(t, b, memParams("memory.remember", `{"text":"third","supersedes":"pm_nothing"}`))
	wantResult(t, m, statusFailed, reasonMemoryNotFound)
}

func TestRecallArgumentsAreHeld(t *testing.T) {
	_, b := memHost(t, Config{})
	// Ten distinct memories sharing two words: the smart ingest must
	// see them as new, not as near-duplicates of each other.
	for _, place := range []string{"harbour dawn tide", "ridge dusk wind", "forest noon rain", "desert night stars", "river morning fog",
		"mountain evening snow", "valley midnight owls", "island sunrise gulls", "meadow afternoon bees", "canyon twilight bats"} {
		if got := remember(t, b, `{"text":"beacon sighting over the `+place+`"}`); got.Outcome != "created" {
			t.Fatalf("%s: %+v", place, got)
		}
	}
	r := recall(t, b, `{"query":"beacon","limit":3}`)
	if len(r.Hits) != 3 || r.Status != "partial" || r.Matched != 10 || r.Shown != 3 || !r.Truncated {
		t.Fatalf("limit 3 of 10: %+v", r)
	}
	r = recall(t, b, `{"query":"beacon ridge","exact":true}`)
	if r.Status != "found_nothing" {
		t.Fatalf("exact forces the phrase in order: %+v", r)
	}
	r = recall(t, b, `{"query":"sighting beacon","decay":"none"}`)
	if r.Policy != "none" || len(r.Hits) != 7 {
		t.Fatalf("pure retrieval, default limit: %+v", r)
	}
	for _, bad := range []string{`{"query":""}`, `{"query":"x","limit":51}`, `{"query":"x","since":"yesterday"}`, `{"query":"x","decay":"sigmoid"}`, `[1,2]`} {
		m := dispatch(t, b, memParams("memory.recall", bad))
		wantResult(t, m, statusDenied, reasonArgumentInvalid)
	}
	m := dispatch(t, b, memParams("memory.remember", `{"text":"   "}`))
	wantResult(t, m, statusDenied, reasonArgumentInvalid)
}

// The broker must pass the published policy to the memory owner, not keep
// a second, narrower list. Exercise the wire response and reinforcement too.
func TestRecallUsesTheMemoryOwnersDecayPolicies(t *testing.T) {
	for _, policy := range []string{"", "carrd", "actr", "none"} {
		t.Run("decay="+policy, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				h, b := memHost(t, Config{})
				note := remember(t, b, `{"text":"harbour bell"}`)
				want, err := h.instruments.Recall(context.Background(), memory.Query{
					Text: "harbour bell", Exact: true, Stores: []string{"plugin_memories"},
					PluginID: "p", Decay: policy, Now: time.Now(),
				})
				if err != nil || len(want.Hits) != 1 {
					t.Fatalf("memory owner: %+v, %v", want, err)
				}
				got := recall(t, b, `{"query":"harbour bell","exact":true,"decay":"`+policy+`"}`)
				if got.Policy != want.Policy || got.Status != "found" || len(got.Hits) != 1 {
					t.Fatalf("broker policy/result = %+v; owner = %+v", got, want)
				}
				hit := got.Hits[0]
				if hit.ID != note.ID || hit.Strength != want.Hits[0].Strength || hit.Score != want.Hits[0].Score || hit.Accesses != 0 {
					t.Fatalf("broker hit = %+v; owner = %+v", hit, want.Hits[0])
				}
				// Unknown policies remain typed refusals and do not reinforce.
				bad := dispatch(t, b, memParams("memory.recall", `{"query":"harbour bell","decay":"sigmoid"}`))
				wantResult(t, bad, statusDenied, reasonArgumentInvalid)
				next := recall(t, b, `{"query":"harbour bell","exact":true,"decay":"none"}`)
				if len(next.Hits) != 1 || next.Hits[0].Accesses != 1 {
					t.Fatalf("successful recall must reinforce once; refusal must not: %+v", next)
				}
			})
		})
	}
}

func TestTempMemoriesDieWithTheBinding(t *testing.T) {
	st := newStore(t)
	h := newHost(t, st, Config{Grants: map[string]Grant{"t0": {Memory: true}}})
	b0 := h.Bind("t0", packagefmt.TierT0, []string{"ring4.memory"})
	got := remember(t, b0, `{"text":"an uncertified plugin's note"}`)
	if got.Scope != "temp" {
		t.Fatalf("T0 storage must be temp-scoped: %+v", got)
	}
	if err := b0.Close(); err != nil {
		t.Fatal(err)
	}
	b1 := h.Bind("t0", packagefmt.TierT0, []string{"ring4.memory"})
	if r := recall(t, b1, `{"query":"uncertified"}`); r.Status != "found_nothing" {
		t.Fatalf("the temp memory survived the activation: %+v", r)
	}
	if n, _ := st.PluginMemoryCount(store.PluginScope{PluginID: "t0"}); n != 0 {
		t.Fatalf("%d memories survived the close", n)
	}
}

func TestMemoryCeilingsAndSAFE(t *testing.T) {
	_, b := memHost(t, Config{MaxMemoryTextBytes: 32, MaxMemories: 2})
	m := dispatch(t, b, memParams("memory.remember", `{"text":"`+strings.Repeat("x", 33)+`"}`))
	wantResult(t, m, statusFailed, reasonMemoryTextTooLarge)
	remember(t, b, `{"text":"one distinct thing"}`)
	remember(t, b, `{"text":"a second, unrelated"}`)
	m = dispatch(t, b, memParams("memory.remember", `{"text":"a third, refused"}`))
	wantResult(t, m, statusFailed, reasonMemoryQuotaExceeded)

	_, safe := memHost(t, Config{InSAFE: func() bool { return true }})
	wantErrorReason(t, dispatch(t, safe, memParams("memory.remember", `{"text":"written under SAFE"}`)), reasonPolicyDeny)
	if r := recall(t, safe, `{"query":"anything"}`); r.Status != "found_nothing" {
		t.Fatalf("a plugin's own record stays readable under SAFE: %+v", r)
	}
}

// THE CEILING AND THE WINDOW ARE THE OPERATOR'S, AND A REFUSAL NAMES ONLY
// WAYS OUT THAT EXIST. The ceiling was 10,000 in the code and the window
// 90 days; a host told nothing has both. A plugin has two verbs and
// neither removes a memory, so a refusal at the ceiling says what is held,
// which of it leaves on its own and when, and that the ways out are the
// operator's: raise the ceiling, or remove what the plugin stored. A
// record full of current memories is told that nothing leaves on its own
// (it was told a replaced memory would go in 90 days, with none replaced).
// The window is asked at the refusal, so one changed while the broker
// runs is the one said. A temporary activation is told which of its rows
// end with it.
func TestThePluginMemoryCeilingAndWindowAreTheOperators(t *testing.T) {
	if got := (Config{}).maxMemories(); got != 10000 {
		t.Fatalf("a host told no ceiling holds a plugin to %d memories; it was 10000 in the code", got)
	}
	if got := (Config{}).replacedMemoryDays(); got != 90 {
		t.Fatalf("a host told no window says %d days; it was 90 in the code", got)
	}
	const operators = "a plugin cannot remove a memory: the operator can raise the ceiling (plugins.runtime.max_memories in the config file) or remove what this plugin stored (the Plugins page, STORED BY PLUGINS)"
	days := 30
	_, b := memHost(t, Config{MaxMemories: 3, ReplacedMemoryWindow: func() time.Duration { return time.Duration(days) * 24 * time.Hour }})
	refused := func(args string) string {
		t.Helper()
		m := dispatch(t, b, memParams("memory.remember", args))
		rec := wantResult(t, m, statusFailed, reasonMemoryQuotaExceeded)
		var detail string
		_ = json.Unmarshal(rec["detail"], &detail)
		return detail
	}
	one := remember(t, b, `{"text":"one distinct thing"}`)
	remember(t, b, `{"text":"a second, unrelated"}`)
	remember(t, b, `{"text":"harbour lights at dusk"}`)

	// Full of current memories: nothing leaves on its own, and a
	// correction is refused like any other row.
	for _, args := range []string{`{"text":"a fourth, refused"}`, `{"text":"one thing corrected","supersedes":"` + one.ID + `"}`} {
		if detail, want := refused(args), "plugin memory quota exceeded: 3 memories held at the 3-memory ceiling, 3 current and 0 replaced; none of them leaves on its own; "+operators; detail != want {
			t.Fatalf("a record full of current memories is refused as\n%q\nwant\n%q", detail, want)
		}
	}

	// With a replaced memory held, the window is said, as it stands now.
	_, b = memHost(t, Config{MaxMemories: 3, ReplacedMemoryWindow: func() time.Duration { return time.Duration(days) * 24 * time.Hour }})
	one = remember(t, b, `{"text":"one distinct thing"}`)
	remember(t, b, `{"text":"a second, unrelated"}`)
	remember(t, b, `{"text":"one thing corrected","supersedes":"`+one.ID+`"}`)
	if detail, want := refused(`{"text":"harbour lights at dusk"}`), "plugin memory quota exceeded: 3 memories held at the 3-memory ceiling, 2 current and 1 replaced; 1 replaced memory is removed on its own, 30 days after it was replaced (plugins.runtime.replaced_memory_days); "+operators; detail != want {
		t.Fatalf("with one replaced memory held the refusal is\n%q\nwant\n%q", detail, want)
	}
	days = 7
	if detail := refused(`{"text":"harbour lights at dusk"}`); !strings.Contains(detail, "7 days after it was replaced") {
		t.Fatalf("the window changed while the broker ran and the refusal says the old one: %q", detail)
	}

	// A temporary activation: its own rows end with it, and that is said
	// of those rows and no others.
	h, _ := memHost(t, Config{MaxMemories: 1, Grants: map[string]Grant{"t0": {Memory: true}}})
	b = h.Bind("t0", packagefmt.TierT0, []string{"ring4.memory"})
	remember(t, b, `{"text":"an uncertified plugin's note"}`)
	if detail, want := refused(`{"text":"another, with no room"}`), "plugin memory quota exceeded: 1 memories held at the 1-memory ceiling, 1 current and 0 replaced; 1 is this activation's temporary memory and ends with it; "+operators; detail != want {
		t.Fatalf("a temporary record's refusal is\n%q\nwant\n%q", detail, want)
	}
}
