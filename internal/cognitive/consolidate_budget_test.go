package cognitive

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/llm/wire"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// sizedModel is a model whose input ceiling the test states in characters
// of the user message, and which refuses a request over it before sending
// as the production client does (*wire.ContextLimitError). attempts counts
// every request put to it, refused or sent; calls counts those sent.
type sizedModel struct {
	limit    int
	reply    func(user string) (string, error)
	attempts []string
	sent     []string
	systems  []string
}

func (m *sizedModel) over(user string) error {
	if m.limit > 0 && len(user) > m.limit {
		return &wire.ContextLimitError{Required: len(user) / 3, Limit: m.limit / 3}
	}
	return nil
}

func (m *sizedModel) ChatSimple(_ context.Context, system, user string) (string, string, error) {
	m.attempts = append(m.attempts, user)
	if err := m.over(user); err != nil {
		return "", "", err
	}
	m.sent = append(m.sent, user)
	m.systems = append(m.systems, system)
	text, err := m.reply(user)
	return text, "sized-model", err
}

func (m *sizedModel) ChatStructured(ctx context.Context, system, user string, _ wire.ToolDefinition) (string, string, bool, error) {
	text, id, err := m.ChatSimple(ctx, system, user)
	return text, id, false, err
}

func (m *sizedModel) Chat(context.Context, []wire.Message, wire.ChatOptions) (*wire.Response, error) {
	return nil, fmt.Errorf("sizedModel does not answer Chat")
}

// checkedModel is a sizedModel the facility can ask before sending.
type checkedModel struct{ *sizedModel }

func (m checkedModel) CheckSimple(_ context.Context, _, user string) error { return m.over(user) }
func (m checkedModel) CheckWithTools(_ context.Context, _, user string, _ []wire.ToolDefinition) error {
	return m.over(user)
}

// rememberingLedger is the mock door whose record then holds the
// experiences it minted, as the store's would: what the identity was told
// is found again by EntityExists. It is kept out of the mock's raw queue,
// so a test's count of what waits is the count of what it put there.
type rememberingLedger struct{ *mockLedger }

func (r rememberingLedger) Append(t ledger.EventType, ring int, payload any, modelID string) (*ledger.Event, error) {
	evt, err := r.mockLedger.Append(t, ring, payload, modelID)
	if err == nil && t == ledger.EventExperienceCreate && r.st != nil {
		if p, ok := payload.(map[string]any); ok {
			r.st.experiences = append(r.st.experiences, store.Experience{ID: fmt.Sprint(p["id"]), Content: fmt.Sprint(p["content"]), Provenance: "system"})
		}
	}
	return evt, err
}

// toldIdentity is what the facility minted to tell the identity: raw
// experiences of the substrate's own voice, by their ids.
func toldIdentity(lg *mockLedger) map[string]string {
	out := map[string]string{}
	for i, et := range lg.appended {
		if et != ledger.EventExperienceCreate {
			continue
		}
		p, ok := lg.payloads[i].(map[string]any)
		if !ok || p["provenance"] != "system" || p["raw"] != true {
			continue
		}
		out[fmt.Sprint(p["id"])] = fmt.Sprint(p["content"])
	}
	return out
}

// upsertOn answers an envelope that rests one belief on the first
// experience on the table.
func upsertOn(user string) (string, error) {
	i := strings.Index(user, "[e")
	if i < 0 {
		return "", fmt.Errorf("no experience on the table")
	}
	id := user[i+1 : i+strings.Index(user[i:], "]")]
	return fmt.Sprintf(`{"operations": [{"op": "upsert", "statement": "What %s shows, said once", "confidence": 0.6, "evidence": [%q]}], "ring3_view": "You believe what %s shows."}`, id, id, id), nil
}

func TestAConsolidationWhoseBeliefListOutgrowsTheBudgetFitsSaysSoAndKeepsFormingBeliefs(t *testing.T) {
	st := &mockStore{unprocessedCnt: 3, experiences: []store.Experience{
		{ID: "e1", Content: "the operator shipped the release at midnight", Raw: 1},
		{ID: "e2", Content: "another midnight release from the operator", Raw: 1},
		{ID: "e3", Content: "a third release shipped at midnight", Raw: 1},
	}}
	for i := 0; i < 200; i++ {
		st.beliefs = append(st.beliefs, store.Belief{ID: fmt.Sprintf("b%03d", i), Ring: 3,
			Statement: fmt.Sprintf("Belief %03d about gardening, tides and the weather in another country entirely. ", i) + strings.Repeat("Unrelated detail. ", 12)})
	}
	st.beliefs = append(st.beliefs, store.Belief{ID: "b_midnight", Ring: 3, Statement: "The operator ships releases at midnight."})
	model := &sizedModel{limit: 16_000, reply: upsertOn}
	lg := &mockLedger{st: st}
	c := NewConsolidate(st, checkedModel{model}, rememberingLedger{lg}, &mockRingWriter{}, ConsolidateConfig{Threshold: 3})

	if err := c.Execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(model.sent) != 1 {
		t.Fatalf("THE BELIEF LIST STOPPED CONSOLIDATION: %d request(s) sent of %d asked; the pass must fit the list and send", len(model.sent), len(model.attempts))
	}
	sent := model.sent[0]
	if !strings.Contains(sent, "b_midnight") {
		t.Fatalf("the beliefs shown are not those that bear on what the pass reads: the one about midnight releases is missing:\n%.600s", sent)
	}
	if !strings.Contains(sent, "of 201 current beliefs shown") || !strings.Contains(sent, "recall (source=ledger)") {
		t.Fatalf("the pass does not declare what it left out, with the route to it:\n%.600s", sent)
	}
	if upsertsIn(lg) != 1 || st.unprocessedCnt != 0 {
		t.Fatalf("the pass forms its belief and consumes what it read: %d upsert(s), %d waiting", upsertsIn(lg), st.unprocessedCnt)
	}
	told := toldIdentity(lg)
	if len(told) != 1 {
		t.Fatalf("the identity is told once that their consolidation saw part of their beliefs: %v", told)
	}
	for _, content := range told {
		if !strings.Contains(content, "of your 201 current beliefs") {
			t.Fatalf("what the identity is told does not say how many of how many: %q", content)
		}
	}

	// A second pass over a list that grew: it fits again, forms its belief,
	// and does not tell the identity what it already told.
	st.experiences = append(st.experiences, store.Experience{ID: "e4", Content: "one more midnight release", Raw: 1},
		store.Experience{ID: "e5", Content: "midnight again", Raw: 1}, store.Experience{ID: "e6", Content: "and again at midnight", Raw: 1})
	st.unprocessedCnt = 3
	st.beliefs = append(st.beliefs, store.Belief{ID: "b_more", Ring: 3, Statement: "One belief more."})
	if err := c.Execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(model.sent) != 2 || upsertsIn(lg) != 2 || st.unprocessedCnt != 0 {
		t.Fatalf("the next pass keeps forming beliefs: %d sent, %d upsert(s), %d waiting", len(model.sent), upsertsIn(lg), st.unprocessedCnt)
	}
	if got := toldIdentity(lg); len(got) != 1 {
		t.Fatalf("the identity was told again of the same condition: %v", got)
	}
}

func TestAPassRefusedForSizeHalvesDownToOneAndSetsItAsideNeverRetryingTheSameRefusal(t *testing.T) {
	st := &mockStore{unprocessedCnt: 8}
	for i := 8; i >= 2; i-- { // newest first, as the store lists them; the mock's queue is oldest first
		st.experiences = append(st.experiences, store.Experience{ID: fmt.Sprintf("e%d", i), Content: fmt.Sprintf("a small thing that happened, number %d", i), Raw: 1})
	}
	st.experiences = append(st.experiences, store.Experience{ID: "e1", Content: "POISON " + strings.Repeat("far too long to carry. ", 400), Raw: 1})
	model := &sizedModel{limit: 4_000, reply: upsertOn}
	lg := &mockLedger{st: st}
	c := NewConsolidate(st, model, rememberingLedger{lg}, &mockRingWriter{}, ConsolidateConfig{Threshold: 1})

	for pass := 1; pass <= 12 && st.unprocessedCnt > 1; pass++ {
		if err := c.Execute(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	poisoned := 0
	var sizes []int
	for _, a := range model.attempts {
		if strings.Contains(a, "POISON") {
			poisoned++
			sizes = append(sizes, strings.Count(a, "\n- ["))
		}
	}
	if poisoned != 4 {
		t.Fatalf("THE SAME REFUSAL WAS ASKED %d TIMES (batches %v); want 8, 4, 2, then the one alone, and never again", poisoned, sizes)
	}
	if fmt.Sprint(sizes) != "[8 4 2 1]" {
		t.Fatalf("the refused batches halve down to one: %v", sizes)
	}
	if st.unprocessedCnt != 1 {
		t.Fatalf("every experience but the one set aside is consumed: %d still wait", st.unprocessedCnt)
	}
	told := toldIdentity(lg)
	if len(told) != 1 {
		t.Fatalf("the identity is told once of what was set aside: %v", told)
	}
	for _, content := range told {
		if !strings.Contains(content, "e1") {
			t.Fatalf("what the identity is told does not name what was set aside: %q", content)
		}
	}
	before := len(model.attempts)
	for i := 0; i < 3; i++ {
		if err := c.Execute(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	for _, a := range model.attempts[before:] {
		if strings.Contains(a, "POISON") {
			t.Fatal("what was set aside was asked again in a later pass")
		}
	}
}

func TestATruncatedReplyIsRetriedSmaller(t *testing.T) {
	st := &mockStore{unprocessedCnt: 4}
	for i := 4; i >= 1; i-- {
		st.experiences = append(st.experiences, store.Experience{ID: fmt.Sprintf("e%d", i), Content: fmt.Sprintf("thing %d", i), Raw: 1})
	}
	model := &sizedModel{reply: func(user string) (string, error) {
		if strings.Count(user, "\n- [") > 2 {
			return "", &wire.IncompleteResponseError{Reason: "length", Got: 8000}
		}
		return upsertOn(user)
	}}
	lg := &mockLedger{st: st}
	c := NewConsolidate(st, model, lg, &mockRingWriter{}, ConsolidateConfig{Threshold: 1})
	for i := 0; i < 2; i++ {
		if err := c.Execute(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if len(model.sent) != 2 || strings.Count(model.sent[1], "\n- [") != 2 {
		t.Fatalf("A TRUNCATED REPLY WAS ASKED FOR AGAIN AT THE SAME SIZE: %d request(s); the second carried %d experiences, want 2", len(model.sent), strings.Count(model.sent[len(model.sent)-1], "\n- ["))
	}
	runs := lg.runPayloads()
	if len(runs) != 1 || len(runs[0].Inputs) != 2 || st.unprocessedCnt != 2 {
		t.Fatalf("the smaller pass is consumed: %+v, %d waiting", runs, st.unprocessedCnt)
	}
}

func TestAConsolidationBatchTooLargeForTheModelIsReadAsFarAsItFits(t *testing.T) {
	st := &mockStore{unprocessedCnt: 6}
	for i := 6; i >= 1; i-- {
		st.experiences = append(st.experiences, store.Experience{ID: fmt.Sprintf("e%d", i), Content: fmt.Sprintf("NOTED-e%d ", i) + strings.Repeat("a long record of the day. ", 50), Raw: 1})
	}
	model := &sizedModel{limit: 4_500, reply: upsertOn}
	lg := &mockLedger{st: st}
	c := NewConsolidate(st, checkedModel{model}, rememberingLedger{lg}, &mockRingWriter{}, ConsolidateConfig{Threshold: 1})
	if err := c.Execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(model.sent) != 1 {
		t.Fatalf("A BATCH THE MODEL CANNOT TAKE STOPPED THE PASS: %d sent of %d asked", len(model.sent), len(model.attempts))
	}
	n := strings.Count(model.sent[0], "NOTED-")
	runs := lg.runPayloads()
	if strings.Contains(model.sent[0], "is not in view") {
		t.Fatalf("an experience that fits whole was read in part:\n%.300s", model.sent[0])
	}
	if n < 2 || n >= 6 || len(runs) != 1 || len(runs[0].Inputs) != n || st.unprocessedCnt != 6-n {
		t.Fatalf("the pass reads as many as fit, oldest first, and consumes those: %d sent, %+v, %d waiting", n, runs, st.unprocessedCnt)
	}
	for i, id := range runs[0].Inputs {
		if id != fmt.Sprintf("e%d", i+1) {
			t.Fatalf("the oldest are read first: %v", runs[0].Inputs)
		}
	}

	long := &mockStore{unprocessedCnt: 2, experiences: []store.Experience{
		{ID: "e2", Content: "NOTED-e2 a short thing after it", Raw: 1},
		{ID: "e1", Content: "NOTED-e1 " + strings.Repeat("ж", 9000), Raw: 1},
	}}
	model = &sizedModel{limit: 4_500, reply: upsertOn}
	lg = &mockLedger{st: long}
	c = NewConsolidate(long, checkedModel{model}, rememberingLedger{lg}, &mockRingWriter{}, ConsolidateConfig{Threshold: 1})
	if err := c.Execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(model.sent) != 1 || !strings.Contains(model.sent[0], "is not in view: it is longer than one pass can carry") || strings.Contains(model.sent[0], "NOTED-e2") {
		t.Fatalf("AN EXPERIENCE TOO LONG ALONE STOPPED THE PASS, or was not declared read in part: %d sent\n%.300s", len(model.sent), strings.Join(model.sent, "\n---\n"))
	}
	runs = lg.runPayloads()
	if len(runs) != 1 || len(runs[0].Inputs) != 1 || runs[0].Inputs[0] != "e1" || long.unprocessedCnt != 1 {
		t.Fatalf("the long experience is consumed as read in part and the one behind it waits: %+v, %d waiting", runs, long.unprocessedCnt)
	}
	told := toldIdentity(lg)
	if len(told) != 1 {
		t.Fatalf("the identity is told once that an experience was read in part: %v", told)
	}
	for _, content := range told {
		if !strings.Contains(content, "e1") || !strings.Contains(content, "read in part") {
			t.Fatalf("what the identity is told: %q", content)
		}
	}
}
