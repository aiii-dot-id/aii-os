package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/conversation"
	"github.com/aiii-dot-id/aii-os/internal/identity"
	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/ring"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

// m4Marker is a planted marker: M4, a name, a number, the paragraph's
// letter and, at a paragraph's end, z. Its family is the marker without the
// letter: one piece of planted content.
var (
	m4Marker = regexp.MustCompile(`M4[A-Z]+[0-9]*[a-y]?z?`)
	m4Fam    = regexp.MustCompile(`^M4[A-Z]+[0-9]*`)
)

func m4Family(marker string) string { return m4Fam.FindString(marker) }

// m4Hops bounds the reads one route may take: the call itself and the
// reads its answers print.
const m4Hops = 12

// m4Fixture is the identity and what was planted in it: by store (the
// recall source, or tool, that holds it) the families of markers there,
// every marker of each family, and the value a route's open argument
// takes for a store.
type m4Fixture struct {
	a       *App
	dir     string
	planted map[string][]string // store -> families
	markers map[string][]string // family -> every marker it planted
	fill    map[string]string   // "store/argument" -> value
	op      string              // the plugin operation the detail route names
	turns   int                 // the turns m4Render has sent
}

// plant records content as planted in a store and returns it.
func (f *m4Fixture) plant(store, text string) string {
	for _, m := range m4Marker.FindAllString(text, -1) {
		fam := m4Family(m)
		if !slices.Contains(f.markers[fam], m) {
			f.markers[fam] = append(f.markers[fam], m)
		}
		if !slices.Contains(f.planted[store], fam) {
			f.planted[store] = append(f.planted[store], fam)
		}
	}
	return text
}

// m4Words is a passage of n paragraphs, each opening and closing with a
// marker of its own, so a fold that keeps some paragraphs, or a line cut
// short, leaves the rest's markers out of view.
func m4Words(marker string, n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteString("\n\n")
		}
		letter := string(rune('a' + i))
		fmt.Fprintf(&b, "%s%s lantern and gallery, the record says what was seen on the stair and the keeper wrote it down plainly so it could be found again by its own words %s%sz.", marker, letter, marker, letter)
	}
	return b.String()
}

// m4Probe is the fixture's plugin: a primary operation that reads the
// detail its context names, and a context provider beside it.
type m4Probe struct {
	name, op string
	context  bool
	result   string
}

func (p *m4Probe) Name() string        { return p.name }
func (p *m4Probe) Description() string { return "M4PLUGINOP read the lighthouse log" }
func (p *m4Probe) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "string"}}}
}
func (p *m4Probe) ContextProvider() bool { return p.context }
func (p *m4Probe) DispatchOwner() any    { return p }
func (p *m4Probe) ReadOnly() bool        { return true }
func (p *m4Probe) Discovery() tools.Discovery {
	return tools.Discovery{Plugin: "org.example.lighthouse", Operation: p.op, Summary: "M4PLUGINOP read the lighthouse log", Effects: "read.internal", MaxResultBytes: 4096, Primary: !p.context}
}
func (p *m4Probe) Execute(_ context.Context, args map[string]any) (tools.Result, error) {
	if p.context || args["id"] == "m4-detail" {
		return tools.Result{Output: p.result}, nil
	}
	return tools.Result{Output: "no such entry in the lighthouse log"}, nil
}

// m4Identity is a realistic fixture identity, made through the doors a
// live one is: a long conversation, a charter its operator affirmed and a
// belief about them, Ring 2 beliefs promoted on their evidence, an
// intention, a self-portrait, both parts of Ring 3 and a brief, a work
// session and a standing state, an alarm that fired and one that waits, a
// project, a curiosity, a skill proposal, an arrival, a plugin whose
// context names a detail route, and two files a turn reads. Every piece
// carries markers of its own.
func m4Identity(t *testing.T) *m4Fixture {
	t.Helper()
	a, dir := dispatchApp(t)
	t.Cleanup(func() { stopApp(t, a) })
	f := &m4Fixture{a: a, dir: dir, planted: map[string][]string{}, markers: map[string][]string{}, fill: map[string]string{}}
	ctx := interaction.WithSaid(t.Context(), interaction.SaidTyped) // as the chat door marks what the operator types
	appendEvent := func(typ ledger.EventType, level int, payload map[string]any) {
		t.Helper()
		if _, err := a.door.Append(typ, level, payload, ""); err != nil {
			t.Fatalf("%s: %v", typ, err)
		}
	}
	act := func(verb string, args map[string]any) {
		t.Helper()
		if _, err := a.engine.ExecuteAction(ctx, "verb", verb, args); err != nil {
			t.Fatalf("%s %v: %v", verb, args, err)
		}
	}

	// A long conversation: thirty exchanges of eight paragraphs a turn,
	// so the window leaves the oldest out and a pressed request abridges
	// what it shows (an abridgement must save more than it declares).
	for i := 1; i <= 30; i++ {
		if err := a.recordConversation(ctx, string(interaction.Operator), f.plant("conversation", m4Words(fmt.Sprintf("M4ASK%02d", i), 8))); err != nil {
			t.Fatal(err)
		}
		if err := a.recordConversation(ctx, string(interaction.Resident), f.plant("conversation", m4Words(fmt.Sprintf("M4REPLY%02d", i), 8))); err != nil {
			t.Fatal(err)
		}
	}

	// The charter, affirmed by the operator's latest word, and a belief
	// about them that the relationship shaped.
	if err := a.recordConversation(ctx, string(interaction.Operator), "M4 yes, that is our charter"); err != nil {
		t.Fatal(err)
	}
	turn, err := a.store.GetLatestOperatorTurn()
	if err != nil || turn == nil {
		t.Fatalf("operator turn = %+v, %v", turn, err)
	}
	appendEvent(ledger.EventRelationshipUpsert, 1, map[string]any{
		"id": "rel_m4", "counterpart_name": "Quinn", "counterpart_role": "operator",
		"relationship_type": "founding_operator", "charter_text": f.plant("relationships", m4Words("M4CHARTER", 3)),
		"operator_approval_excerpt": turn.Content, "operator_approval_turn": turn.TurnSeq, "approval_basis": "conversation_turn",
	})
	appendEvent(ledger.EventExperienceCreate, 3, map[string]any{"id": "exp_m4_about", "content": f.plant("experiences", "M4ABOUT Quinn asked what I think first"), "category": "observation", "provenance": "self"})
	appendEvent(ledger.EventBeliefUpsert, 3, map[string]any{"id": "b_m4_about", "statement": f.plant("beliefs", "M4OPERATOR Quinn asks what you think first"), "ring": 3, "confidence": 0.7,
		"origin_refs": []map[string]string{{"kind": "experience", "id": "exp_m4_about"}}})
	appendEvent(ledger.EventEdgeCreate, 3, map[string]any{"id": "edge_m4_about", "from_id": "rel_m4", "to_id": "b_m4_about", "edge_type": "SHAPED_BY"})

	// Ring 2: beliefs promoted on three experiences each, every experience
	// long enough that its evidence line is shown in part.
	statements := []string{
		"reading the whole record before acting keeps the work honest",
		"a small change said plainly beats a large one hidden",
		"the operator's review catches what the tests do not",
		"a plan written down survives an interruption",
		"asking early costs less than guessing late",
		"the lighthouse log is the first place to look",
	}
	for b, statement := range statements {
		belief := fmt.Sprintf("b_m4_%d", b+1)
		var refs []map[string]string
		for e := 1; e <= 3; e++ {
			id := fmt.Sprintf("exp_m4_%d_%d", b+1, e)
			appendEvent(ledger.EventExperienceCreate, 3, map[string]any{"id": id, "content": f.plant("experiences", m4Words(fmt.Sprintf("M4EVIDENCE%d%d", b+1, e), 2)), "category": "observation", "provenance": "self"})
			refs = append(refs, map[string]string{"kind": "experience", "id": id})
		}
		appendEvent(ledger.EventBeliefUpsert, 3, map[string]any{"id": belief, "statement": f.plant("beliefs", fmt.Sprintf("M4BELIEF%d %s", b+1, statement)), "ring": 3, "confidence": 0.8, "origin_refs": refs})
		act("commit", map[string]any{"variant": "belief.promote", "id": belief, "ring": 2})
	}
	f.planted["standing"] = []string{"M4BELIEF1"}
	f.fill["standing/id"] = "b_m4_1"

	// An intention, the self-portrait drawn from all four kinds of record,
	// Ring 3's two parts and the brief.
	appendEvent(ledger.EventIntentionCreate, 3, map[string]any{"id": "int_m4", "statement": f.plant("intentions", "M4INTENTION finish the lighthouse inspection this week")})
	appendEvent(ledger.EventSelfModelSynthesize, 3, map[string]any{
		"id": "syn_m4", "synthesis_text": f.plant("syntheses", m4Words("M4PORTRAIT", 3)), "continuity_thread": "I keep reading before I act.",
		"source_entity_refs": []ledger.SelfModelSourceRef{{Class: "beliefs", ID: "b_m4_1"}, {Class: "experiences", ID: "exp_m4_1_1"},
			{Class: "intentions", ID: "int_m4"}, {Class: "relationships", ID: "rel_m4"}},
	})
	if err := a.rings.SetSection(ring.Ring3, "surfacing", f.plant("ring3", m4Words("M4SURFACING", 4))); err != nil {
		t.Fatal(err)
	}
	if err := a.rings.SetSection(ring.Ring3, "working_truth", f.plant("ring3", m4Words("M4WORKINGTRUTH", 4))); err != nil {
		t.Fatal(err)
	}
	a.rings.SetBrief(f.plant("brief", m4Words("M4BRIEF", 4)))

	// What is in front of them, and what waits.
	act("work", map[string]any{"action": "start", "description": f.plant("work:status", "M4SESSION the lighthouse inspection")})
	act("work", map[string]any{"action": "update", "state": f.plant("work:status", m4Words("M4STATE", 3)), "expected_evidence": f.plant("work:status", "M4EXPECTED the log reads clean"), "falsifier": f.plant("work:status", "M4FALSIFIER a gap in the log")})
	if err := a.store.SetStandingState(f.plant("work:status", m4Words("M4STANDING", 2))); err != nil {
		t.Fatal(err)
	}
	if err := a.store.RecordToolStart("turn_m4_work", 1, "main", "m", "", "shell", `{"command":"cat lantern.log"}`); err != nil {
		t.Fatal(err)
	}
	if err := a.store.RecordToolDoneMeasured("turn_m4_work", 1, "shell", `{"command":"cat lantern.log"}`, f.plant("work", "M4WORKCALL the lantern log reads clean to the gallery"), interaction.Succeeded, false, store.ToolMeasurement{}); err != nil {
		t.Fatal(err)
	}
	// A sub-agent at work, which this turn's facts show and work status
	// lists (its queue row already taken, so no worker starts it here).
	if _, err := a.store.DB().Exec(`INSERT INTO work_queue (id, kind, payload, dedup_key, source, state) VALUES (?, ?, '{}', ?, 'identity', 'DONE')`, "wq_m4_child", identity.SubagentWorkKind, "ws_m4_child"); err != nil {
		t.Fatal(err)
	}
	if err := a.store.StartWorkSession(ctx, "ws_m4_child", store.SubagentDescription(f.plant("work:status", "M4SUBAGENT survey the north stair"))); err != nil {
		t.Fatal(err)
	}
	act("work", map[string]any{"action": "alarm.set", "message": f.plant("alarms", "M4ALARM check the gallery rail"), "duration": "6h"})
	if err := a.store.AddOutboxMessage("timer_m4_fired", "operator", "", f.plant("alarms", "M4FIRED the lantern alarm rang"), nil); err != nil {
		t.Fatal(err)
	}
	act("work", map[string]any{"action": "project.create", "name": f.plant("projects", "M4PROJECT lighthouse"), "description": "the inspection of the lighthouse"})
	act("work", map[string]any{"action": "curiosity", "subject": f.plant("curiosity", "M4CURIOSITY why the lantern drifted"), "why": "it drifted twice", "pointer": "lantern.log"})
	act("commit", map[string]any{"variant": "skill.propose", "title": f.plant("skills", "M4SKILL read the log first"), "delta": "read the whole log before patching", "evidence": "ws_m4"})
	// An arrival long enough to read in two parts, its first paragraphs in
	// the first and its last in the second.
	arrival := strings.Split(m4Words("M4ARRIVAL", 4), "\n\n")
	tide := strings.Repeat("the tide came in over the lower rocks and went out again. ", 220)
	if _, err := a.store.RecordInbound("in_m4", "mail", "friend", f.plant("inbound", f.plant("inbound/in_m4", strings.Join([]string{arrival[0], arrival[1], tide, arrival[2], arrival[3]}, "\n\n")))); err != nil {
		t.Fatal(err)
	}

	// A plugin: a primary operation that reads a detail, and a context
	// provider whose context names it.
	f.op = "lighthouse.read"
	f.plant("tools:show", "M4PLUGINOP")
	detail := &m4Probe{name: "pl_org_example_lighthouse_read", op: f.op, result: f.plant("tools:call", "M4PLUGINDETAIL the lantern log in full")}
	if err := a.toolReg.RegisterDynamic(detail, "org.example.lighthouse"); err != nil {
		t.Fatal(err)
	}
	contextResult, err := json.Marshal(map[string]string{"text": f.plant("plugin", "M4PLUGINCONTEXT the lantern log has a gap"), "detail_route": f.op + " id=m4-detail"})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.toolReg.RegisterHostOp(&m4Probe{name: "pl_org_example_lighthouse_context", op: "lighthouse.context", context: true, result: string(contextResult)}, "org.example.lighthouse"); err != nil {
		t.Fatal(err)
	}
	f.fill["tools/name"] = f.op

	// A snapshot of them, which recall source=continuity reports: the
	// count it gives is what the snapshot plants (it carries no words of
	// the identity's).
	act("work", map[string]any{"action": "backup.take"})
	f.planted["continuity"] = []string{"M4SNAPSHOT"}
	f.markers["M4SNAPSHOT"] = []string{"snapshots: 1 kept"}

	// Two files a turn reads, large enough that the second folds the first
	// under a pressed request.
	for _, name := range []string{"m4-log-a", "m4-log-b"} {
		fam := strings.ToUpper(strings.ReplaceAll(name, "-", ""))
		if err := os.WriteFile(filepath.Join(dir, name+".md"), []byte(f.plant("read", m4Words(fam, 24))), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

// m4Request is one request a turn sent, the turn it belongs to, and how
// that turn was pressed.
type m4Request struct {
	turn  int
	label string
	body  string
}

// m4Model is the route test's fake provider: it answers each resident
// request from its script, or in words, and every answer in words carries
// reasoning, as a thinking model's does, so the record keeps it.
type m4Model struct {
	mu        sync.Mutex
	script    []string
	bodies    []string
	reasoning []string // the reasoning its answers carried, in order
	replies   []string // and their words
	n         int
}

func (m *m4Model) serve(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	m.mu.Lock()
	m.n++
	answer := ""
	if strings.Contains(string(body), conversation.TurnOpen) {
		m.bodies = append(m.bodies, string(body))
		if len(m.script) > 0 {
			answer, m.script = m.script[0], m.script[1:]
		}
	}
	if answer == "" {
		thought := fmt.Sprintf("M4THINKING%03da the keeper weighed the log first M4THINKING%03daz", m.n, m.n)
		text := m4Words(fmt.Sprintf("M4ANSWER%03d", m.n), 8)
		m.reasoning = append(m.reasoning, thought)
		m.replies = append(m.replies, text)
		answer = m4Reply(text, thought)
	}
	m.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, answer)
}

func (m *m4Model) sent() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.bodies...)
}

// m4Reply is an answer in words, with its reasoning.
func m4Reply(text, reasoning string) string {
	msg, _ := json.Marshal(map[string]any{"role": "assistant", "content": text, "reasoning_content": reasoning})
	return `{"id":"r","model":"fake","choices":[{"index":0,"message":` + string(msg) + `,"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`
}

// m4Render sends the fixture's turns and returns every request they made.
func m4Render(t *testing.T, f *m4Fixture) []m4Request {
	t.Helper()
	model := &m4Model{}
	srv := httptest.NewServer(http.HandlerFunc(model.serve))
	t.Cleanup(srv.Close)
	f.a.llmSwap.Swap(llm.New(&llm.ClientConfig{Endpoint: srv.URL, Model: "fake", MaxOutputTokens: 4096, Retries: -1, TimeoutSeconds: 10}))
	full := f.a.composer.MaxTokens()
	var out []m4Request
	turn := func(label string) error {
		t.Helper()
		before := len(model.sent())
		f.turns++
		ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
		defer cancel()
		ctx = interaction.WithSaid(ctx, interaction.SaidTyped)
		if err := f.a.acquireTurn(ctx); err != nil {
			t.Fatal(err)
		}
		words := f.plant("conversation", m4Words(fmt.Sprintf("M4TURN%03d", f.turns), 8))
		_, err := f.a.observeChat(ctx, words, func(kind, name, args string) {})
		waitUntil(t, func() bool { return !f.a.TurnActive() }, "the turn to end")
		for _, b := range model.sent()[before:] {
			out = append(out, m4Request{turn: f.turns, label: label, body: b})
		}
		return err
	}
	defer func() {
		model.mu.Lock()
		defer model.mu.Unlock()
		for _, thought := range model.reasoning {
			f.plant("reasoning", thought)
		}
		for _, text := range model.replies {
			f.plant("conversation", text)
		}
	}()

	// The prompt budget, from the identity's own down to nothing: every
	// section folds, then is omitted, until only what never yields is
	// left. pressed is the last.
	pressed := full
	for budget := full; budget > 0; budget -= 2000 {
		f.a.composer.SetMaxTokens(budget)
		if err := turn(fmt.Sprintf("prompt budget %d", budget)); err != nil {
			t.Fatalf("a turn at prompt budget %d: %v", budget, err)
		}
		pressed = budget
	}

	// The request budget, from the identity's own down to the last the
	// provider admits, with the prompt pressed so the history has room to
	// yield: it is abridged, then dropped.
	floor := full
	for budget := full; budget > 0; budget -= 1000 {
		f.a.conv.SetModelLimits(budget, 0)
		err := turn(fmt.Sprintf("prompt budget %d, request budget %d", pressed, budget))
		var refused *llm.ContextLimitError
		if errors.As(err, &refused) {
			break
		}
		if err != nil {
			t.Fatalf("a turn at request budget %d: %v", budget, err)
		}
		floor = budget
	}

	logA, err := os.ReadFile(filepath.Join(f.dir, "m4-log-a.md"))
	if err != nil {
		t.Fatal(err)
	}
	budget := floor + len(logA)/2
	f.a.conv.SetModelLimits(budget, 0)
	read := func(id, name string) string {
		b, _ := json.Marshal(map[string]string{"file_path": filepath.Join(f.dir, name)})
		return toolCall(id, "read", string(b))
	}
	model.mu.Lock()
	model.script = []string{callReply("", read("m4_read_a", "m4-log-a.md")), callReply("", read("m4_read_b", "m4-log-b.md")), m4Reply("M4 read both.", "")}
	model.mu.Unlock()
	if err := turn(fmt.Sprintf("prompt budget %d, request budget %d, tool rounds", pressed, budget)); err != nil {
		t.Fatalf("the turn with tool rounds: %v", err)
	}
	f.a.composer.SetMaxTokens(full)
	f.a.conv.SetModelLimits(full, 0)
	return out
}

// m4Sections is, by section name, the families of planted content the
// identity's own unpressed prompt carries in each section: what a receipt
// that names the section, or folds it, stands in for.
func m4Sections(t *testing.T, f *m4Fixture) map[string][]string {
	t.Helper()
	ws, err := f.a.buildWorkState(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	facts, err := f.a.buildTurnFacts(t.Context(), true)
	if err != nil {
		t.Fatal(err)
	}
	p, err := f.a.composer.ComposeTurn(ws, facts, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]string{}
	for _, s := range p.Sections {
		for _, m := range m4Marker.FindAllString(s.Content, -1) {
			if fam := m4Family(m); !slices.Contains(out[s.Name], fam) {
				out[s.Name] = append(out[s.Name], fam)
			}
		}
	}
	return out
}

func TestEveryRouteThePromptPrintsReturnsWhatItIsPrintedFor(t *testing.T) {
	f := m4Identity(t)
	sections := m4Sections(t, f)
	requests := m4Render(t, f)

	// The receipts that name a section, by the route they print: a fold's
	// declaration carries the same route its section's omission line does.
	named := map[string]string{}
	for _, req := range requests {
		texts, _ := m4Texts(t, req.body)
		for _, tx := range texts {
			for _, line := range strings.Split(tx.text, "\n") {
				name, route, ok := strings.Cut(strings.TrimPrefix(line, "- "), " — ")
				if ok && strings.HasPrefix(line, "- ") && sections[name] != nil {
					named[strings.TrimSpace(route)] = name
				}
			}
		}
	}

	// Each distinct call once, with the places it was printed.
	type call struct {
		tool   string
		args   map[string]string
		expect []string
		where  []string
	}
	calls := map[string]*call{}
	var order, acts []string
	shapes := map[string]bool{}
	for i, req := range requests {
		var prev string
		if i > 0 && requests[i-1].turn == req.turn {
			prev = requests[i-1].body
		}
		texts, enums := m4Texts(t, req.body)
		visible := m4Visible(t, req.body)
		for _, r := range m4Find(texts, enums) {
			if !m4Reads(r) {
				if !slices.Contains(acts, r.printed) {
					acts = append(acts, r.printed)
				}
				continue
			}
			scope := r.line
			if strings.HasPrefix(r.where, "messages[") && r.where != m4CurrentWhere(t, req.body) {
				scope = m4TextAt(t, req.body, r.where)
			}
			section := ""
			args, expect := m4Fill(f, r, m4Expect(t, f, r, req.body, prev, sections, named, visible, shapes, &section), scope)
			// One call for each route and what it is for — a section, or the
			// pieces of content its answer must hold: the same route printed
			// for the same thing at another budget is the same call, held to
			// what either printing is for.
			var fams []string
			for _, m := range expect {
				if fam := m4Family(m); !slices.Contains(fams, fam) {
					fams = append(fams, fam)
				}
			}
			sort.Strings(fams)
			what := "section " + section
			if section == "" {
				what = "content " + strings.Join(fams, ",")
			}
			key := r.tool + " " + m4Canon(args) + " | " + what
			c := calls[key]
			if c == nil {
				c = &call{tool: r.tool, args: args}
				calls[key] = c
				order = append(order, key)
			}
			for _, m := range expect {
				if !slices.Contains(c.expect, m) {
					c.expect = append(c.expect, m)
				}
			}
			at := fmt.Sprintf("%s in %s at %s: %q", r.printed, r.where, req.label, strings.TrimSpace(r.line))
			if len(c.where) < 3 {
				c.where = append(c.where, at)
			}
		}
	}
	for _, shape := range []string{"omitted section", "folded section", "excerpt", "abridged turn", "turns out of view", "folded result"} {
		if !shapes[shape] {
			t.Errorf("no request printed a route for a %s: the fixture no longer presses the prompt that far, and that kind of route is not followed", shape)
		}
	}
	if len(order) == 0 {
		t.Fatal("no request printed a route that reads")
	}
	seen := map[string]bool{}
	defer func() {
		for key := range m4Undelivered {
			if !seen[key] {
				t.Errorf("%s is in the known-undelivered table and no request printed it: remove it from the table", key)
			}
		}
	}()
	for _, key := range order {
		c := calls[key]
		printed := strings.Join(c.where, "\n         ")
		if len(c.expect) == 0 {
			t.Errorf("%s %s: the fixture planted nothing this route is printed for, so what it returns cannot be held to anything; plant some.\nprinted: %s", c.tool, m4Canon(c.args), printed)
			continue
		}
		got, reads, refused, missing := m4Follow(t, f, c.tool, c.args, c.expect)
		if row, known := m4Undelivered[key]; known {
			seen[key] = true
			if got != "" && refused == "" {
				t.Errorf("%s now delivers (it returns %s): remove it from the known-undelivered table", key, got)
				continue
			}
			t.Logf("%s still does not deliver (%s; %s): refused %q, none of %s in %d read(s)\nprinted: %s", key, row, m4UndeliveredWhy, refused, m4Short(c.expect), reads, printed)
			continue
		}
		switch {
		case refused != "":
			t.Errorf("%s %s is refused: %s\nprinted: %s", c.tool, m4Canon(c.args), refused, printed)
		case len(missing) > 0:
			t.Errorf("%s %s reads whole and does not return all it is printed for: missing %s in %d read(s)\nprinted: %s", c.tool, m4Canon(c.args), m4Short(missing), reads, printed)
		case got == "":
			t.Errorf("%s %s does not return what it is printed for: none of %s in %d read(s)\nprinted: %s", c.tool, m4Canon(c.args), m4Short(c.expect), reads, printed)
		default:
			t.Logf("%s %s returns %s (%d read(s))", c.tool, m4Canon(c.args), got, reads)
		}
	}
	sort.Strings(acts)
	t.Logf("routes that act, not called: %s", strings.Join(acts, "; "))
}

func TestAWholeReadRefusesAStaleOrChangedReference(t *testing.T) {
	f := m4Identity(t)
	recall := func(args map[string]any) (string, bool) {
		t.Helper()
		raw, err := json.Marshal(args)
		if err != nil {
			t.Fatal(err)
		}
		var tc llm.ToolCall
		tc.ID, tc.Function.Name, tc.Function.Arguments = "m4_stale", "recall", string(raw)
		obs := f.a.executeToolCall(t.Context(), tc)
		return obs.Text, obs.Failed || obs.Refused
	}
	version := regexp.MustCompile(`version ([0-9a-f]{8}), part 1 of \d+:`)
	for _, c := range []struct{ name, section, fresh string }{
		{"ring3", "surfacing", "M4CHANGED the stair was repainted"},
		{"brief", "", "M4CHANGED a new day began"},
	} {
		first, refused := recall(map[string]any{"source": c.name, "query": ""})
		v := version.FindStringSubmatch(first)
		if refused || v == nil || !strings.Contains(first, f.markers[map[string]string{"ring3": "M4WORKINGTRUTH", "brief": "M4BRIEF"}[c.name]][7]) {
			t.Fatalf("%s is not read whole:\n%.400s", c.name, first)
		}
		if c.section != "" {
			if err := f.a.rings.SetSection(ring.Ring3, c.section, c.fresh); err != nil {
				t.Fatal(err)
			}
		} else {
			f.a.rings.SetBrief(c.fresh)
		}
		stale, refused := recall(map[string]any{"source": c.name, "part": "1@" + v[1]})
		if !refused || !strings.Contains(stale, "has changed since version "+v[1]) || !strings.Contains(stale, "recall source="+c.name) || m4Marker.MatchString(stale) {
			t.Fatalf("%s: a reference to a version that is gone is not refused as one, or gives a text:\n%s", c.name, stale)
		}
		if now, _ := recall(map[string]any{"source": c.name}); !strings.Contains(now, "M4CHANGED") {
			t.Fatalf("%s: the read from its start is not the text as it is now:\n%s", c.name, now)
		}
	}
	if out, refused := recall(map[string]any{"source": "inbound", "id": "in_m4_gone"}); !refused || !strings.Contains(out, `No arrival has id "in_m4_gone"`) || m4Marker.MatchString(out) {
		t.Fatalf("an arrival's id the record does not hold is not refused as one:\n%s", out)
	}
	first, _ := recall(map[string]any{"source": "inbound", "id": "in_m4"})
	v := version.FindStringSubmatch(first)
	if v == nil {
		t.Fatalf("the arrival is not read whole:\n%.400s", first)
	}
	if out, refused := recall(map[string]any{"source": "inbound", "id": "in_m4", "part": "9@" + v[1]}); !refused || !strings.Contains(out, "part 9 is not one of them") || m4Marker.MatchString(out) {
		t.Fatalf("a part the arrival does not have is not refused as one:\n%s", out)
	}
}

var m4Undelivered = map[string]string{}

const m4UndeliveredWhy = "known not to deliver on main"

// m4Visible is every planted marker a request shows the model.
func m4Visible(t *testing.T, body string) map[string]bool {
	t.Helper()
	texts, _ := m4Texts(t, body)
	seen := map[string]bool{}
	for _, tx := range texts {
		for _, m := range m4Marker.FindAllString(tx.text, -1) {
			seen[m] = true
		}
	}
	return seen
}

// m4Expect is what a route is printed for, as the markers its answer must
// hold one of: the missing markers of what it stands in for — a section
// omitted or folded, a line cut short, an abridged turn, a folded result,
// the turns out of view — or, where nothing is missing, what its store
// holds. shapes records which of those it stood in.
func m4Expect(t *testing.T, f *m4Fixture, r m4Route, body, prev string, sections map[string][]string, named map[string]string, visible map[string]bool, shapes map[string]bool, section *string) []string {
	t.Helper()
	hidden := func(fams []string) []string {
		var out []string
		for _, fam := range fams {
			for _, m := range f.markers[fam] {
				if !visible[m] {
					out = append(out, m)
				}
			}
		}
		return out
	}
	families := func(text string) []string {
		var out []string
		for _, m := range m4Marker.FindAllString(text, -1) {
			if fam := m4Family(m); !slices.Contains(out, fam) && f.markers[fam] != nil {
				out = append(out, fam)
			}
		}
		return out
	}
	text := m4TextAt(t, body, r.where)
	current := r.where == m4CurrentWhere(t, body)
	history := strings.HasPrefix(r.where, "messages[") && !current && !strings.Contains(r.where, "(tool)")
	line := strings.TrimSpace(r.line)
	decl := m4Declaration(r.line, r.printed)

	// A section omitted ("- Name — route") or folded ("[summary — … route]").
	if name, rest, ok := strings.Cut(strings.TrimPrefix(line, "- "), " — "); ok && strings.HasPrefix(line, "- ") && sections[name] != nil && strings.Contains(rest, r.printed) {
		shapes["omitted section"] = true
		*section = m4Part(name, sections[name], hidden(sections[name]))
		return hidden(sections[name])
	}
	if decl != "" && !history {
		shapes["folded section"] = true
		name := m4NamedBy(decl, named)
		if name == "" {
			name = m4Heading(text, r.line)
		}
		*section = m4Part(name, sections[name], hidden(sections[name]))
		return hidden(sections[name])
	}
	// A line cut short, a turn abridged, a result folded: what that line or
	// message no longer shows.
	if strings.Contains(r.line, " characters; whole: ") {
		shapes["excerpt"] = true
	}
	if got := hidden(families(r.line)); len(got) > 0 {
		return got
	}
	if history && decl != "" {
		shapes["abridged turn"] = true
		if got := hidden(families(text)); len(got) > 0 {
			return got
		}
	}
	if r.callID != "" && prev != "" {
		shapes["folded result"] = true
		earlier, _ := m4Texts(t, prev)
		for _, tx := range earlier {
			if tx.call != nil && tx.call.ID == r.callID {
				if got := hidden(families(tx.text)); len(got) > 0 {
					return got
				}
			}
		}
	}
	paragraph := m4Paragraph(text, r.line)
	if got := hidden(families(paragraph)); len(got) > 0 {
		return got
	}
	// The turns out of view: what the conversation holds that the request
	// does not show.
	if current && strings.Contains(paragraph, "older conversation turns are not shown") && r.args["source"] == "conversation" {
		shapes["turns out of view"] = true
		return hidden(f.planted["conversation"])
	}
	// Nothing is missing where it stands: what its store holds.
	store := r.args["source"]
	if r.tool != "recall" {
		store = r.tool + ":" + r.args["action"]
	}
	var out []string
	for _, fam := range f.planted[store] {
		out = append(out, f.markers[fam]...)
	}
	return out
}

// m4Part names the section a receipt stands in for, and says when what is
// missing is only part of it (a fold that kept some of its pieces whole).
func m4Part(name string, fams, missing []string) string {
	for _, fam := range fams {
		if !slices.ContainsFunc(missing, func(m string) bool { return m4Family(m) == fam }) {
			return name + " (part)"
		}
	}
	return name
}

// m4Declaration is the bracketed fold declaration a route stands in on its
// line ("[summary — …]"), or "".
func m4Declaration(line, printed string) string {
	at := strings.Index(line, printed)
	if at < 0 {
		return ""
	}
	open := strings.LastIndex(line[:at], "[summary —")
	if open < 0 || strings.Contains(line[open:at], "]") {
		return ""
	}
	end := strings.Index(line[at:], "]")
	if end < 0 {
		return ""
	}
	return line[open : at+end+1]
}

// m4NamedBy is the section an omission line names by the route a
// declaration carries.
func m4NamedBy(decl string, named map[string]string) string {
	best := ""
	for route := range named {
		if strings.Contains(decl, route) && len(route) > len(best) {
			best = route
		}
	}
	return named[best]
}

// m4Heading is the first-level heading the line stands under.
func m4Heading(text, line string) string {
	at := strings.Index(text, line)
	if at < 0 {
		return ""
	}
	lines := strings.Split(text[:at], "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if h, ok := strings.CutPrefix(lines[i], "# "); ok {
			return strings.TrimSpace(h)
		}
	}
	return ""
}

// m4Paragraph is the paragraph of text a line stands in.
func m4Paragraph(text, line string) string {
	for _, p := range strings.Split(text, "\n\n") {
		if strings.Contains(p, line) {
			return p
		}
	}
	return line
}

// m4TextAt is the text of one part of a request, by m4Texts' name for it.
func m4TextAt(t *testing.T, body, where string) string {
	t.Helper()
	texts, _ := m4Texts(t, body)
	for _, tx := range texts {
		if tx.where == where {
			return tx.text
		}
	}
	return ""
}

// m4CurrentWhere names the message a request answers: the one its turn
// block rides.
func m4CurrentWhere(t *testing.T, body string) string {
	t.Helper()
	texts, _ := m4Texts(t, body)
	for _, tx := range texts {
		if strings.HasPrefix(tx.where, "messages[") && strings.Contains(tx.text, conversation.TurnOpen) {
			return tx.where
		}
	}
	return ""
}

func m4Reads(r m4Route) bool {
	switch {
	case r.tool == "recall":
		return true
	case r.tool == "work":
		return r.args["action"] == "status"
	case r.tool == "tools" && r.args["action"] == "call":
		return strings.Contains(r.line, "Detail route:")
	case r.tool == "tools":
		return slices.Contains([]string{"brief", "search", "show"}, r.args["action"])
	}
	return strings.Contains(r.line, "it was the read-only call")
}

// m4Fill puts in what a route leaves open: the query, from the content it
// is for — a word of it that the route's scope shows (its line, or the turn
// it abridges), as the identity searches by what it can see, else a word of
// what is missing, and then the answer must hold the rest of that piece,
// not the word searched for; a store's own id; the plugin operation.
func m4Fill(f *m4Fixture, r m4Route, expect []string, scope string) (map[string]string, []string) {
	args := map[string]string{}
	for k, v := range r.args {
		args[k] = v
	}
	open := func(v string) bool {
		return strings.HasPrefix(v, "<") || strings.Contains(v, "…") || strings.HasPrefix(v, "%")
	}
	if v, ok := args["query"]; ok && open(v) {
		var fams []string
		for _, m := range expect {
			fams = append(fams, m4Family(m))
		}
		seen := slices.DeleteFunc(m4Marker.FindAllString(scope, -1), func(m string) bool { return !slices.Contains(fams, m4Family(m)) })
		if len(seen) > 0 {
			args["query"] = seen[0]
		} else if len(expect) > 0 {
			word := expect[0]
			args["query"] = word
			var rest []string
			for _, m := range expect {
				if m != word && m4Family(m) == m4Family(word) {
					rest = append(rest, m)
				}
			}
			expect = rest
		}
	}
	// An id the route leaves open is the one its line names last ("… [id]"),
	// else the one the fixture planted for that store.
	if v, ok := args["id"]; ok && open(v) {
		if ids := m4LineID.FindAllStringSubmatch(r.line, -1); len(ids) > 0 {
			args["id"] = ids[len(ids)-1][1]
		}
	}
	for k, v := range args {
		if fill, ok := f.fill[r.args["source"]+"/"+k]; ok && open(v) {
			args[k] = fill
		}
	}
	if r.tool == "tools" {
		if v, ok := args["name"]; !ok || open(v) {
			args["name"] = f.fill["tools/name"]
		}
	}
	return args, expect
}

// m4Canon is a call's arguments on one line, in one order.
func m4Canon(args map[string]string) string {
	if raw, ok := args["{json}"]; ok {
		return raw
	}
	keys := make([]string, 0, len(args))
	for k := range args {
		if k != "{json}" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		parts = append(parts, k+"="+strconv.Quote(args[k]))
	}
	return strings.Join(parts, " ")
}

func m4Short(markers []string) string {
	if len(markers) > 6 {
		return strings.Join(markers[:6], ", ") + fmt.Sprintf(" and %d more", len(markers)-6)
	}
	return strings.Join(markers, ", ")
}

func m4Follow(t *testing.T, f *m4Fixture, tool string, args map[string]string, expect []string) (got string, reads int, refused string, missing []string) {
	t.Helper()
	type step struct {
		tool        string
		args        map[string]any
		whole       bool
		label       string
		part, total int
	}
	start := map[string]any{}
	if raw, ok := args["{json}"]; ok {
		if err := json.Unmarshal([]byte(raw), &start); err != nil {
			t.Fatal(err)
		}
	} else {
		for k, v := range args {
			start[k] = v
			if n, err := strconv.Atoi(v); err == nil && (k == "after_seq" || k == "limit") {
				start[k] = n
			}
		}
		if tool == "tools" && start["action"] == "call" {
			inner := map[string]any{}
			for k, v := range start {
				if k != "action" && k != "name" {
					inner[k] = v
					delete(start, k)
				}
			}
			start["args"] = inner
		}
	}
	declared := map[string]any{}
	for k, v := range args {
		declared[k] = v
	}
	queue := []step{{tool: tool, args: start, whole: m4RequestsWhole(tool, declared)}}
	seen := map[string]bool{}
	found := map[string]bool{}
	returned := map[string]bool{} // every planted marker any answer held
	whole := queue[0].whole
	finished := map[string]bool{}
	for len(queue) > 0 && reads < m4Hops {
		s := queue[0]
		queue = queue[1:]
		raw, err := json.Marshal(s.args)
		if err != nil {
			t.Fatal(err)
		}
		if seen[s.tool+string(raw)] {
			continue
		}
		seen[s.tool+string(raw)] = true
		reads++
		var tc llm.ToolCall
		tc.ID, tc.Function.Name, tc.Function.Arguments = fmt.Sprintf("m4_follow_%d", reads), s.tool, string(raw)
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
		obs := f.a.executeToolCall(ctx, tc)
		cancel()
		if obs.Failed || obs.Refused {
			if reads == 1 || s.whole {
				return "", reads, strings.TrimSpace(obs.Text), nil
			}
			continue
		}
		for _, m := range expect {
			if strings.Contains(obs.Text, m) {
				found[m] = true
				if got == "" {
					got = m
				}
			}
		}
		for _, m := range m4Marker.FindAllString(obs.Text, -1) {
			returned[m] = true
		}
		label := m4WholePart.FindStringSubmatch(obs.Text)
		if s.whole && label == nil {
			return "", reads, "", []string{"whole-read part label"}
		}
		whole = whole || label != nil
		if got != "" && !whole {
			return got, reads, "", nil
		}
		// The next part of a whole read, before anything else: by its
		// part (Ring 3, the brief, an arrival) or its query (a turn, a
		// kept result).
		var nextPart *step
		if next := m4ContinuesPart.FindStringSubmatch(obs.Text); next != nil {
			part := map[string]any{}
			for k, v := range s.args {
				part[k] = v
			}
			part["part"] = next[1]
			nextPart = &step{tool: s.tool, args: part, whole: true}
		} else if next := m4Continues.FindStringSubmatch(obs.Text); next != nil {
			part := map[string]any{}
			for k, v := range s.args {
				part[k] = v
			}
			part["query"] = next[1]
			nextPart = &step{tool: s.tool, args: part, whole: true}
		}
		if label != nil {
			part, _ := strconv.Atoi(label[2])
			total, _ := strconv.Atoi(label[3])
			if part < 1 || total < part || (s.label == "" && part != 1) ||
				(s.label != "" && (label[1] != s.label || part != s.part || total != s.total)) ||
				(part < total) != (nextPart != nil) {
				return "", reads, "", []string{"consistent whole-read parts and terminal page"}
			}
			finished[label[1]] = part == total
			if nextPart != nil {
				nextPart.label, nextPart.part, nextPart.total = label[1], part+1, total
			}
		}
		if nextPart != nil {
			queue = append([]step{*nextPart}, queue...)
		}
		// The next page, as a listing names it.
		if next := m4NextPage.FindStringSubmatch(obs.Text); next != nil {
			page := map[string]any{}
			for k, v := range s.args {
				page[k] = v
			}
			n, _ := strconv.Atoi(next[1])
			page["after_seq"] = n
			queue = append(queue, step{tool: s.tool, args: page})
		}
		// A read the answer names.
		for _, r := range m4Find([]m4Text{{where: "answer", text: obs.Text}}, m4AnswerTools) {
			next := map[string]any{}
			for k, v := range r.args {
				next[k] = v
			}
			queue = append(queue, step{tool: r.tool, args: next, whole: m4RequestsWhole(r.tool, next)})
		}
	}
	if !whole {
		return got, reads, "", nil
	}
	for label, done := range finished {
		if !done {
			missing = append(missing, "terminal part of "+label)
		}
	}
	families := map[string]bool{}
	for m := range found {
		families[m4Family(m)] = true
	}
	for _, m := range expect {
		if families[m4Family(m)] && !found[m] {
			missing = append(missing, m)
		}
	}
	// A read of a whole store returns all it holds: every family planted
	// there, each whole.
	for _, fam := range f.planted[m4WholeOf(tool, args)] {
		for _, m := range f.markers[fam] {
			if !returned[m] && !slices.Contains(missing, m) {
				missing = append(missing, m)
			}
		}
	}
	if len(missing) > 0 || got == "" {
		return "", reads, "", missing
	}
	return fmt.Sprintf("all %d of what it is held to, whole", len(found)), reads, "", nil
}

// m4WholePart is how a whole read labels each part of what it reads, and
// m4ContinuesPart and m4Continues how a part names the next: Ring 3's, the
// brief's and an arrival's by part="P@version" on the same call, a turn's
// and a kept result's by its query.
var (
	m4WholePart     = regexp.MustCompile(`(?:^|\n)\[end of ([^\]\n]+), part (\d+) of (\d+)[^\]\n]*\]$`)
	m4ContinuesPart = regexp.MustCompile(`continues: recall [^\]\n]*part="([^"]+)"\]`)
	m4Continues     = regexp.MustCompile(`continues: query="([^"]+)"`)
)

// Completeness comes from the requested source/reference, even when a
// regression removes the answer's whole-read label. The outer metadata
// remains authoritative in the raw-JSON falsifier seam.
func m4RequestsWhole(tool string, args map[string]any) bool {
	if tool != "recall" {
		return false
	}
	source, _ := args["source"].(string)
	id, _ := args["id"].(string)
	query, _ := args["query"].(string)
	exact := args["exact"] == true || args["exact"] == "true"
	switch source {
	case "ring3", "brief":
		return true
	case "inbound":
		return id != ""
	case "conversation", "reasoning":
		return !exact && m4TurnReference.MatchString(query)
	case "work":
		return !exact && m4ResultReference.MatchString(query)
	}
	return false
}

var (
	m4TurnReference   = regexp.MustCompile(`(?i)^turn\s*[:#]?\s*\d+(?:\s+part\s*[:#]?\s*\d+)?$`)
	m4ResultReference = regexp.MustCompile(`^((?i:tex_[0-9a-f]{64})|check_[a-zA-Z0-9_-]+)(?:\s+(?i:part)\s*[:#]?\s*\d+)?$`)
)

// m4WholeOf is the store a route reads whole when it names one: Ring 3,
// the brief, or one arrival by its id. Its read must return every marker
// of every family planted there, not only of the families it happened to
// return.
func m4WholeOf(tool string, args map[string]string) string {
	if tool != "recall" {
		return ""
	}
	switch source := args["source"]; {
	case source == "ring3" || source == "brief":
		return source
	case source == "inbound" && args["id"] != "":
		return "inbound/" + args["id"]
	}
	return ""
}

// m4LineID is an id a line names in brackets.
var m4LineID = regexp.MustCompile(`\[([a-z][a-z0-9_]*_[a-z0-9_]+)\]`)

// m4NextPage is how a paged listing names its next page.
var m4NextPage = regexp.MustCompile(`its next page: after_seq=(\d+)`)

// m4AnswerTools are the reads an answer may name for the next read.
var m4AnswerTools = map[string]map[string]string{"recall": {}, "work": {"status": "action"}}

// m4Route is one call the prompt prints for the identity to make: the
// tool, its arguments as printed (a value that is a placeholder — <…>, …,
// {…}, %… — is filled by the caller), where it was printed and the line.
type m4Route struct {
	tool    string
	args    map[string]string
	printed string // the route's text
	where   string // the part of the request: system, messages[i] (role), or tools[name]
	line    string // the line it stands on
	after   string // the words that follow it on the line
	callID  string // for a folded result's notice, the call it folded
}

// m4Text is one piece of model-facing text in a request and where it is,
// and for a tool's result the call it answers, as the request carries it.
type m4Text struct {
	where, text string
	call        *llm.ToolCall
}

// m4Texts is every piece of model-facing text in a chat request: each
// message, and each tool's description and the descriptions in its schema.
func m4Texts(t *testing.T, body string) ([]m4Text, map[string]map[string]string) {
	t.Helper()
	var req struct {
		Messages []struct {
			Role       string         `json:"role"`
			Content    string         `json:"content"`
			ToolCalls  []llm.ToolCall `json:"tool_calls"`
			ToolCallID string         `json:"tool_call_id"`
		} `json:"messages"`
		Tools []struct {
			Function struct {
				Name        string         `json:"name"`
				Description string         `json:"description"`
				Parameters  map[string]any `json:"parameters"`
			} `json:"function"`
		} `json:"tools"`
	}
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatalf("request body: %v", err)
	}
	var texts []m4Text
	calls := map[string]llm.ToolCall{}
	for i, m := range req.Messages {
		where := fmt.Sprintf("messages[%d] (%s)", i, m.Role)
		if i == 0 {
			where = "system"
		}
		for _, c := range m.ToolCalls {
			calls[c.ID] = c
		}
		tx := m4Text{where: where, text: m.Content}
		if c, ok := calls[m.ToolCallID]; ok && m.ToolCallID != "" {
			tx.call = &c
		}
		texts = append(texts, tx)
	}
	// enums: by tool, each value a top-level argument may take, and that
	// argument's name — so "work status" reads as action=status.
	enums := map[string]map[string]string{}
	for _, tool := range req.Tools {
		f := tool.Function
		enums[f.Name] = map[string]string{}
		var b strings.Builder
		b.WriteString(f.Description)
		props, _ := f.Parameters["properties"].(map[string]any)
		names := make([]string, 0, len(props))
		for k := range props {
			names = append(names, k)
		}
		sort.Strings(names)
		for _, k := range names {
			p, _ := props[k].(map[string]any)
			if d, ok := p["description"].(string); ok {
				b.WriteString("\n" + d)
			}
			if vals, ok := p["enum"].([]any); ok {
				for _, v := range vals {
					if s, ok := v.(string); ok {
						enums[f.Name][s] = k
					}
				}
			}
		}
		texts = append(texts, m4Text{where: "tools[" + f.Name + "]", text: b.String()})
	}
	return texts, enums
}

// m4Find reads every route out of a request's text: a tool of the
// request's own list named with arguments after it, in any of the forms the
// prompt prints them — recall (source=ledger), recall(query="…",
// source=conversation), recall source=inbound, work action=status id=…,
// work status (an argument's value alone), read({"file_path":…}) — and the
// prose that says what goes in an argument the form leaves open: "by its
// words" (the query), "with the belief's id" and "by its id" (the id).
func m4Find(texts []m4Text, enums map[string]map[string]string) []m4Route {
	tools := make([]string, 0, len(enums))
	for name := range enums {
		tools = append(tools, name)
	}
	sort.Slice(tools, func(i, j int) bool { return len(tools[i]) > len(tools[j]) })
	var out []m4Route
	for _, tx := range texts {
		for _, line := range strings.Split(tx.text, "\n") {
			// A folded result's notice names the call it folded, cut short
			// (argExcerpt); the call itself is in the request, in the
			// identity's own message, and is what "repeated exactly" means.
			if tx.call != nil && strings.Contains(line, "it was the read-only call "+tx.call.Function.Name+"(") {
				out = append(out, m4Route{tool: tx.call.Function.Name, args: map[string]string{"{json}": tx.call.Function.Arguments},
					printed: "the read-only call " + tx.call.Function.Name + "(…)", where: tx.where, line: line, callID: tx.call.ID})
				continue
			}
			if _, detail, ok := strings.Cut(line, "Detail route: "); ok {
				op, rest, _ := strings.Cut(strings.TrimSpace(detail), " ")
				if args, _ := m4Args(" "+rest, nil); op != "" {
					if args == nil {
						args = map[string]string{}
					}
					args["action"], args["name"] = "call", op
					out = append(out, m4Route{tool: "tools", args: args, printed: strings.TrimSpace(detail), where: tx.where, line: line})
				}
			}
			for _, tool := range tools {
				for from := 0; ; {
					i := strings.Index(line[from:], tool)
					if i < 0 {
						break
					}
					at := from + i
					from = at + len(tool)
					if at > 0 && m4NameByte(line[at-1]) || from < len(line) && m4NameByte(line[from]) && line[from] != '(' {
						continue
					}
					args, end := m4Args(line[from:], enums[tool])
					if len(args) == 0 {
						continue
					}
					after := line[from+end:]
					if _, ok := args["query"]; !ok && strings.HasPrefix(strings.TrimSpace(after), "by its words") {
						args["query"] = "<its words>"
					}
					if _, ok := args["id"]; !ok && (strings.HasPrefix(strings.TrimSpace(after), "with the belief's id") || strings.HasPrefix(strings.TrimSpace(after), "by its id")) {
						args["id"] = "<its id>"
					}
					out = append(out, m4Route{tool: tool, args: args, printed: strings.TrimRight(strings.TrimSpace(line[at:from+end]), ".,;:"), where: tx.where, line: line, after: after})
				}
			}
		}
	}
	return out
}

func m4NameByte(c byte) bool {
	return c == '_' || c == '.' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

// m4Args reads the arguments printed after a tool's name and how many
// bytes they take: a parenthesised list or JSON object, or words of
// key=value (an enum's value alone names its argument).
func m4Args(s string, enum map[string]string) (map[string]string, int) {
	args := map[string]string{}
	rest := strings.TrimPrefix(s, " ")
	skipped := len(s) - len(rest)
	if strings.HasPrefix(rest, "(") {
		close := m4Close(rest)
		if close < 0 {
			return nil, 0
		}
		inner := strings.TrimSpace(rest[1:close])
		if strings.HasPrefix(inner, "{") {
			var obj map[string]any
			if json.Unmarshal([]byte(inner), &obj) != nil {
				return nil, 0
			}
			for k, v := range obj {
				b, _ := json.Marshal(v)
				args[k] = string(b)
			}
			args["{json}"] = inner
			return args, skipped + close + 1
		}
		for _, pair := range m4Split(inner) {
			k, v, ok := strings.Cut(strings.TrimSpace(pair), "=")
			if !ok || !m4Key(k) {
				return nil, 0
			}
			args[k] = m4Value(v)
		}
		return args, skipped + close + 1
	}
	if skipped == 0 {
		return nil, 0
	}
	n := skipped
	for first := true; ; first = false {
		word, tail := m4Word(rest)
		if word == "" {
			break
		}
		k, v, ok := strings.Cut(word, "=")
		switch {
		case ok && m4Key(k):
			args[k] = m4Value(v)
		case first && enum[strings.TrimRight(word, ".,;:`")] != "":
			w := strings.TrimRight(word, ".,;:`")
			args[enum[w]] = w
		default:
			return args, n
		}
		n += len(rest) - len(tail)
		rest = tail
	}
	return args, n
}

// m4Word is the next word of s (a quoted value is one word) and what
// follows it, its leading space included.
func m4Word(s string) (string, string) {
	t := strings.TrimLeft(s, " `")
	if t == "" {
		return "", s
	}
	quoted := false
	for i := 0; i < len(t); i++ {
		switch {
		case t[i] == '"':
			quoted = !quoted
		case !quoted && (t[i] == ' ' || t[i] == '`'):
			return t[:i], t[i:]
		}
	}
	return t, ""
}

// m4Close is the index of the parenthesis that closes s's first, outside
// quotes and braces.
func m4Close(s string) int {
	depth, quoted := 0, false
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '"' && (i == 0 || s[i-1] != '\\'):
			quoted = !quoted
		case quoted:
		case c == '(' || c == '{':
			depth++
		case c == ')' || c == '}':
			depth--
			if depth == 0 && c == ')' {
				return i
			}
		}
	}
	return -1
}

// m4Split splits a parenthesised list at its commas, outside quotes.
func m4Split(s string) []string {
	var out []string
	quoted, start := false, 0
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == '"':
			quoted = !quoted
		case s[i] == ',' && !quoted:
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}

func m4Key(k string) bool {
	if k == "" {
		return false
	}
	for i := 0; i < len(k); i++ {
		if c := k[i]; !(c == '_' || c >= 'a' && c <= 'z') {
			return false
		}
	}
	return true
}

// m4Value is a printed value: unquoted, and without the punctuation that
// closes the sentence it stands in.
func m4Value(v string) string {
	v = strings.TrimRight(v, ".,;:`)]")
	if unq, err := strconv.Unquote(v); err == nil {
		return unq
	}
	return v
}

func TestM4RejectsUnlabelledPreviewForWholeSource(t *testing.T) {
	a, dir := dispatchApp(t)
	t.Cleanup(func() { stopApp(t, a) })
	f := &m4Fixture{a: a, dir: dir, planted: map[string][]string{}, markers: map[string][]string{}, fill: map[string]string{}}
	full := f.plant("ring3", m4Words("M4CHECK", 8))
	if err := a.rings.SetSection(ring.Ring3, "surfacing", full); err != nil {
		t.Fatal(err)
	}
	// Control: the fake answer really is the production word-search preview,
	// not the complete authored section and not a labelled whole-read part.
	preview, err := a.engine.ExecuteAction(t.Context(), "verb", "recall", map[string]any{"query": "M4CHECK"})
	if err != nil {
		t.Fatal(err)
	}
	expect := f.markers["M4CHECK"]
	if len(expect) < 2 || !strings.Contains(preview, expect[0]) || strings.Contains(preview, expect[len(expect)-1]) || m4WholePart.MatchString(preview) {
		t.Fatalf("fixture is not an incomplete unlabelled preview: %q", preview)
	}
	args := map[string]string{"source": "ring3", "{json}": `{"query":"M4CHECK"}`}
	if m4WholeOf("recall", args) != "ring3" {
		t.Fatal("checker metadata does not declare the whole Ring 3 obligation")
	}
	got, reads, refused, missing := m4Follow(t, f, "recall", args, expect)
	if got != "" && refused == "" && len(missing) == 0 {
		t.Fatalf("M4 accepted incomplete preview for a declared whole source: got=%q reads=%d missing=%v; wanted all %d planted markers", got, reads, missing, len(expect))
	}
}
