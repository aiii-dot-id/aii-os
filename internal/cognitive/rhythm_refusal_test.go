package cognitive

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/llm/wire"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// queueingLedger is the mock door whose record holds what it minted as the
// store's does: a telling is raw, so it joins the raw queue, newest, and
// consolidation reads it in its turn.
type queueingLedger struct{ *mockLedger }

func (q queueingLedger) Append(t ledger.EventType, ring int, payload any, modelID string) (*ledger.Event, error) {
	evt, err := q.mockLedger.Append(t, ring, payload, modelID)
	if p, ok := payload.(map[string]any); err == nil && ok && t == ledger.EventExperienceCreate && p["raw"] == true {
		q.st.experiences = append([]store.Experience{{ID: fmt.Sprint(p["id"]), Content: fmt.Sprint(p["content"]), Provenance: "system", Raw: 1}}, q.st.experiences...)
		q.st.unprocessedCnt++
	}
	return evt, err
}

// scriptLLM answers each call from a script, in order, and keeps what it
// was asked; a call past the script fails the test's count, not the pass.
type scriptLLM struct {
	replies []string
	users   []string
}

func (s *scriptLLM) ChatSimple(_ context.Context, _, user string) (string, string, error) {
	s.users = append(s.users, user)
	if len(s.users) > len(s.replies) {
		return "", "", errors.New("the script has no reply for this call")
	}
	return s.replies[len(s.users)-1], "script-model", nil
}

func (s *scriptLLM) ChatStructured(ctx context.Context, system, user string, _ wire.ToolDefinition) (string, string, bool, error) {
	text, id, err := s.ChatSimple(ctx, system, user)
	return text, id, false, err
}

// saidOf is what a facility reported to the operator's card, by kind, in
// the order it was said.
func saidOf(said []Condition, facility string) []ConditionKind {
	var out []ConditionKind
	for _, c := range said {
		if c.Facility == facility {
			out = append(out, c.Kind)
		}
	}
	return out
}

func TestAModelThatFinishesNoReplyLeavesABoundedRecord(t *testing.T) {
	for _, reason := range []string{"length", "refusal"} {
		st := &mockStore{unprocessedCnt: 3}
		for i := 3; i >= 1; i-- {
			st.experiences = append(st.experiences, store.Experience{ID: fmt.Sprintf("e%d", i), Content: fmt.Sprintf("thing %d", i), Raw: 1})
		}
		model := &sizedModel{reply: func(string) (string, error) {
			return "", &wire.IncompleteResponseError{Reason: reason, Got: 4000}
		}}
		lg := &mockLedger{st: st}
		var said []Condition
		c := NewConsolidate(st, model, queueingLedger{lg}, &mockRingWriter{}, ConsolidateConfig{Threshold: 1})
		c.SetTeller(&Teller{Report: func(cond Condition) { said = append(said, cond) }})
		for pass := 0; pass < 40; pass++ {
			if err := c.Execute(context.Background()); err != nil {
				t.Fatal(err)
			}
		}
		told := toldIdentity(lg)
		if len(lg.appended) != 3 || len(told) != 3 {
			t.Fatalf("%s: A TELLING WAS TOLD OF: forty passes over three experiences left %d record(s), %d of them tellings; want three, one for each experience set aside", reason, len(lg.appended), len(told))
		}
		if c.aside.count() != 6 {
			t.Fatalf("%s: the three experiences and the three tellings are each set aside: %d", reason, c.aside.count())
		}
		asked := 0
		for _, user := range model.sent {
			if strings.Contains(user, "Experiences to consolidate") {
				asked++
			}
		}
		if asked != 7 {
			t.Fatalf("%s: %d consolidation requests were sent; want 7 — the three together, then each of the six alone, and none again", reason, asked)
		}
		setAside := 0
		for _, kind := range saidOf(said, "consolidate") {
			if kind == ConditionSetAside {
				setAside++
			}
		}
		if setAside != 6 {
			t.Fatalf("%s: the operator's card is told of each of the six set aside: %d", reason, setAside)
		}
	}
}

func TestATellingReadInPartIsNotToldOf(t *testing.T) {
	st := &mockStore{unprocessedCnt: 1, experiences: []store.Experience{{ID: "e1", Content: strings.Repeat("a long record of the day. ", 100), Raw: 1}}}
	model := &admitsLLM{mockLLM: &mockLLM{override: "You notice a pattern in what happened."}, admits: func(m string) bool { return len(m) <= 120 }}
	lg := &mockLedger{st: st}
	var said []Condition
	dream := NewDream(st, model, lg, nil, DreamConfig{Threshold: 1})
	dream.SetTeller(&Teller{Report: func(c Condition) { said = append(said, c) }, Door: queueingLedger{lg}, Exists: st.EntityExists})
	for pass := 0; pass < 10; pass++ {
		if err := dream.Execute(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if told := toldIdentity(lg); len(told) != 1 {
		t.Fatalf("A TELLING WAS TOLD OF: ten passes over one long experience left %d tellings; want the one, of the experience read in part: %v", len(told), told)
	}
	if kinds := saidOf(said, "dream"); len(kinds) != 2 || st.unprocessedCnt != 0 {
		t.Fatalf("the experience and its telling are each read in part, said on the card and consumed: the card was told %v, %d still wait", kinds, st.unprocessedCnt)
	}
}

func TestAReplyLeftUnfinishedForAnotherReasonSetsNothingAsideAndPassesOverNothing(t *testing.T) {
	for _, reason := range []string{"pause_turn", "tool_calls", "content_filter", "a_reason_no_build_knows"} {
		unfinished := &wire.IncompleteResponseError{Reason: reason}

		st := &mockStore{unprocessedCnt: 4}
		for i := 4; i >= 1; i-- {
			st.experiences = append(st.experiences, store.Experience{ID: fmt.Sprintf("e%d", i), Content: fmt.Sprintf("thing %d", i), Raw: 1})
		}
		model := &sizedModel{reply: func(string) (string, error) { return "", unfinished }}
		lg := &mockLedger{st: st}
		c := NewConsolidate(st, model, queueingLedger{lg}, &mockRingWriter{}, ConsolidateConfig{Threshold: 1})
		for pass := 0; pass < 5; pass++ {
			if err := c.Execute(context.Background()); err != nil {
				t.Fatal(err)
			}
		}
		if len(model.sent) != 5 {
			t.Fatalf("%s: rig: %d request(s) sent in five passes", reason, len(model.sent))
		}
		for i, user := range model.sent {
			if n := strings.Count(user, "\n- ["); n != 4 {
				t.Fatalf("%s: A PROVIDER'S CONDITION HALVED THE BATCH: pass %d asked for %d of the four experiences", reason, i+1, n)
			}
		}
		if c.aside.count() != 0 || c.nextBatch(c.cfg()) != DefaultConsolidateBatch || len(lg.appended) != 0 || st.unprocessedCnt != 4 {
			t.Fatalf("%s: the material waits as it is: %d set aside, next batch %d, %d record(s) minted, %d waiting", reason, c.aside.count(), c.nextBatch(c.cfg()), len(lg.appended), st.unprocessedCnt)
		}

		src := &steppingOutcomes{}
		for seq := uint64(9); seq <= 10; seq++ {
			src.all = append(src.all, store.Outcome{Seq: seq, EntryHash: strings.Repeat("ab", 32), Kind: "intention", ID: fmt.Sprintf("i%d", seq), State: "completed", Was: "a thing", Said: "done"})
		}
		intake, door := &mockLLM{err: unfinished}, &captureDoor{}
		oc := NewConsolidate(nil, intake, door, nil, ConsolidateConfig{})
		oc.SetOutcomes(src)
		for i := 0; i < 5; i++ {
			if err := oc.ObserveOutcomes(context.Background()); !errors.Is(err, error(unfinished)) {
				t.Fatalf("%s: an intake whose reply did not finish reports that reply: %v", reason, err)
			}
			if n := strings.Count(intake.lastUser, "- [record "); n != 2 {
				t.Fatalf("%s: A PROVIDER'S CONDITION HALVED THE INTAKE: intake %d showed %d of the two outcomes", reason, i+1, n)
			}
		}
		if src.cursor != 0 || len(door.payloads) != 0 || oc.outcomeShow.Load() != 0 {
			t.Fatalf("%s: AN OUTCOME WAS PASSED OVER FOR A PROVIDER'S CONDITION: cursor %d, %d record(s) minted, the next intake shows %d", reason, src.cursor, len(door.payloads), oc.outcomeShow.Load())
		}
	}
}

// correctedChat is SELF_MODEL's model under an input ceiling (sizedChat)
// that answers in prose — a contract violation — while prose lasts, and
// then with the valid commit. tighten makes the ceiling, after the first
// reply, the size of the request that drew it: a client whose price of the
// corrective round the facility could not have asked for.
type correctedChat struct {
	sizedChat
	prose   int
	tighten bool
	refused int
}

func (m *correctedChat) Chat(ctx context.Context, messages []wire.Message, opts wire.ChatOptions) (*wire.Response, error) {
	user := messages[len(messages)-1].Content
	if err := m.over(user); err != nil {
		m.refused++
		return nil, err
	}
	if m.prose == 0 {
		return m.sizedChat.Chat(ctx, messages, opts)
	}
	m.prose--
	m.sent = append(m.sent, user)
	if m.tighten {
		m.limit = len(user)
	}
	return &wire.Response{Choices: []wire.Choice{{Message: wire.Message{Content: "who I am, in prose, with no commit call"}}}, ModelID: "sized-chat"}, nil
}

// uncheckedChat is a model that cannot be asked before sending.
type uncheckedChat struct{ m *correctedChat }

func (u uncheckedChat) Chat(ctx context.Context, messages []wire.Message, opts wire.ChatOptions) (*wire.Response, error) {
	return u.m.Chat(ctx, messages, opts)
}

func TestTheCorrectiveRoundIsFittedAndOneThatCannotBeSentIsSaid(t *testing.T) {
	st := selfModelStore()
	for i := 0; i < 300; i++ {
		st.beliefs = append(st.beliefs, store.Belief{ID: fmt.Sprintf("b%03d", i), Ring: 3,
			Statement: fmt.Sprintf("Belief %03d, at length: ", i) + strings.Repeat("a careful clause of what I hold. ", 10)})
	}
	model := &correctedChat{sizedChat: sizedChat{limit: 12_000}, prose: 1}
	committer := &mockSelfModelCommitter{}
	sm := NewSelfModel(st, model, committer)
	if err := sm.Execute(context.Background()); err != nil {
		t.Fatalf("THE CORRECTIVE ROUND WAS NOT SENT: %v", err)
	}
	if model.refused != 0 || len(model.sent) != 2 || committer.calls != 1 {
		t.Fatalf("the corrective round is fitted, sent and commits: %d refused before sending, %d sent, %d commit(s)", model.refused, len(model.sent), committer.calls)
	}
	first, second := model.sent[0], model.sent[1]
	if !strings.Contains(second, "violated the output contract") || !strings.Contains(second, "of 301 beliefs shown") {
		t.Fatalf("the corrective round carries the refusal and declares what it left out:\n%.300s\n…\n%.400s", second, second[max(0, len(second)-400):])
	}
	if shown := func(s string) int { return strings.Count(s, "- [beliefs id=") }; shown(second) >= shown(first) || shown(second) == 0 {
		t.Fatalf("the evidence yields to the refusal's text: %d beliefs in the first request, %d in the corrective round", shown(first), shown(second))
	}
	// The host's instruction is set off from the evidence: a labelled line
	// of its own after the last evidence line, and nothing of the record
	// after it.
	label := "\n\nCorrection (from the runtime; the evidence ended above):\nYour previous reply violated the output contract"
	if at := strings.Index(second, label); at < 0 || strings.Count(second, label) != 1 || at < strings.LastIndex(second, "\n- [") {
		t.Fatalf("THE CORRECTIVE INSTRUCTION READS AS PART OF THE EVIDENCE: no labelled line of its own after the last evidence line:\n%.500s", second[max(0, len(second)-700):])
	}

	st = selfModelStore()
	model = &correctedChat{sizedChat: sizedChat{limit: 1 << 20}, prose: 1, tighten: true}
	lg := &mockLedger{st: st}
	var said []Condition
	sm = NewSelfModel(st, uncheckedChat{model}, &mockSelfModelCommitter{})
	sm.SetDoor(rememberingLedger{lg})
	sm.SetTeller(&Teller{Report: func(c Condition) { said = append(said, c) }})
	var limit *wire.ContextLimitError
	if err := sm.Execute(context.Background()); !errors.As(err, &limit) || !strings.Contains(err.Error(), "expected one commit call or exact NO_CHANGE") {
		t.Fatalf("rig: the corrective round is refused before it is sent, and the pass fails with both refusals: %v", err)
	}
	if kinds := saidOf(said, "self_model"); len(kinds) == 0 || kinds[len(kinds)-1] != ConditionNoRoom {
		t.Fatalf("A CORRECTIVE ROUND THAT COULD NOT BE SENT REACHED ONLY THE LOG: the card was told %v", kinds)
	}
	told := toldIdentity(lg)
	if len(told) != 1 {
		t.Fatalf("the identity is told once that their portrait could not be redrawn: %v", told)
	}
	for _, content := range told {
		if !strings.Contains(content, "could not be redrawn") || !strings.Contains(content, "your first reply was refused") || strings.Contains(content, "nothing was sent") {
			t.Fatalf("what the identity is told is true of a request that was sent and answered: %q", content)
		}
	}
}

func TestAPortraitThatCannotCiteFourKindsForWantOfRoomSaysSo(t *testing.T) {
	st := selfModelStore()
	st.intentions[0].Statement = strings.Repeat("an intention far longer than the window. ", 100)
	model := &sizedChat{limit: 2_000}
	committer := &mockSelfModelCommitter{}
	lg := &mockLedger{st: st}
	var said []Condition
	sm := NewSelfModel(st, model, committer)
	sm.SetDoor(rememberingLedger{lg})
	sm.SetTeller(&Teller{Report: func(c Condition) { said = append(said, c) }})
	if err := sm.Execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(model.sent) != 0 || committer.calls != 0 {
		t.Fatalf("rig: a request that shows three kinds of evidence is not sent: %d sent, %d commit(s)", len(model.sent), committer.calls)
	}
	if len(said) != 1 || said[0].Kind != ConditionNoRoom || !strings.Contains(said[0].Said, "3 kinds of evidence") || !strings.Contains(said[0].Said, "intentions were left out for room") {
		t.Fatalf("THE PORTRAIT STOPPED AND THE CARD WAS NOT TOLD WHY: %+v", said)
	}
	told := toldIdentity(lg)
	if len(told) != 1 {
		t.Fatalf("the identity is told once why their portrait was not redrawn: %v", told)
	}
	for _, content := range told {
		if !strings.Contains(content, "3 of the 4 kinds of evidence") || !strings.Contains(content, "left out for room") {
			t.Fatalf("what the identity is told does not say why: %q", content)
		}
	}
}

func TestAWholePassClearsWhatTheCardSaidOfDreamTheBriefAndTheOutcomeIntake(t *testing.T) {
	ctx := context.Background()
	var said []Condition
	teller := &Teller{Report: func(c Condition) { said = append(said, c) }}

	// DREAM: six wait and three fit; the pass after carries the other three.
	dst := &mockStore{unprocessedCnt: 6}
	for i := 1; i <= 6; i++ {
		dst.experiences = append(dst.experiences, store.Experience{ID: fmt.Sprintf("e%d", i), Content: fmt.Sprintf("NOTED-e%d, a thing that happened", i), Raw: 1})
	}
	dmodel := &admitsLLM{mockLLM: &mockLLM{override: "You notice a pattern in what happened."}, admits: func(m string) bool { return strings.Count(m, "NOTED-") <= 3 }}
	dream := NewDream(dst, dmodel, &mockLedger{st: dst}, &mockRingWriter{}, DreamConfig{Threshold: 1})
	dream.SetTeller(teller)
	for i := 0; i < 2; i++ {
		if err := dream.Execute(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if got := fmt.Sprint(saidOf(said, "dream")); got != "[fitted whole]" {
		t.Fatalf("DREAM's line stands after a whole pass: the card was told %s; want fitted, then whole", got)
	}

	// The brief: one over its bound twice, then one that is kept.
	long := strings.Repeat("word ", 300)
	bmodel := &scriptLLM{replies: []string{long, long, "A short bridge."}}
	brief := NewMorningBrief(&mockStore{}, bmodel, &mockBriefWriter{}, MorningBriefConfig{MaxChars: 900})
	brief.SetTeller(teller)
	for i := 0; i < 2; i++ {
		if err := brief.Execute(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if got := fmt.Sprint(saidOf(said, "brief")); got != "[refused whole]" {
		t.Fatalf("the brief's line stands after a whole pass: the card was told %s; want refused, then whole", got)
	}

	// The outcome intake: two refused together, the first passed over, and
	// then the second observed with nothing left out.
	src := &steppingOutcomes{}
	for seq := uint64(9); seq <= 10; seq++ {
		src.all = append(src.all, store.Outcome{Seq: seq, EntryHash: strings.Repeat("ab", 32), Kind: "intention", ID: fmt.Sprintf("i%d", seq), State: "completed", Was: "a thing", Said: "done"})
	}
	over := strings.Repeat("too long. ", 20)
	oc := NewConsolidate(nil, &scriptLLM{replies: []string{over, over, "I finish what I start."}}, &captureDoor{}, nil, ConsolidateConfig{ObservationMaxChars: 50})
	oc.SetOutcomes(src)
	oc.SetTeller(teller)
	for i := 0; i < 3; i++ {
		_ = oc.ObserveOutcomes(ctx) // a halved intake reports its refusal
	}
	if got := fmt.Sprint(saidOf(said, "outcomes")); got != "[halved set_aside whole]" || src.cursor != 10 {
		t.Fatalf("the intake's line stands after a whole pass: the card was told %s, cursor %d; want halved, set_aside, then whole at 10", got, src.cursor)
	}
}

func TestABriefOverItsBoundIsAskedForOnceMoreShorterThenRefused(t *testing.T) {
	ctx := context.Background()
	long := strings.Repeat("word ", 300) // 1,500 characters
	model := &scriptLLM{replies: []string{long, "A short bridge."}}
	bw := &mockBriefWriter{}
	var said []Condition
	brief := NewMorningBrief(&mockStore{}, model, bw, MorningBriefConfig{MaxChars: 900})
	brief.SetTeller(&Teller{Report: func(c Condition) { said = append(said, c) }})
	if err := brief.Execute(ctx); err != nil {
		t.Fatal(err)
	}
	if len(model.users) != 2 || bw.brief != "A short bridge." {
		t.Fatalf("A BRIEF OVER ITS BOUND WAS REFUSED WITHOUT BEING ASKED FOR AGAIN: %d call(s), the brief is %q", len(model.users), bw.brief)
	}
	again := model.users[1]
	if !strings.HasPrefix(again, model.users[0]) || !strings.Contains(again, "was 1500 characters, over its bound of 900") || !strings.Contains(again, "at most 900 characters") {
		t.Fatalf("the second ask is the same day with the bound and the length of what came back stated:\n%s", again)
	}

	model.replies, model.users = []string{long, long, "never asked for"}, nil
	if err := brief.Execute(ctx); err != nil {
		t.Fatal(err)
	}
	if len(model.users) != 2 {
		t.Fatalf("one more ask, never a loop: %d call(s) for a brief twice over its bound", len(model.users))
	}
	if bw.brief != "A short bridge." {
		t.Fatalf("a brief twice over its bound is refused whole and yesterday's stands: %q", bw.brief)
	}
	last := said[len(said)-1]
	if last.Kind != ConditionRefused || !strings.Contains(last.Said, "1500 characters and, asked once more for a shorter one, 1500") {
		t.Fatalf("the card says what came back both times: %+v", last)
	}
}

// brokenCheck is a client whose admission check fails for a reason that is
// not the request's size.
type brokenCheck struct {
	*mockLLM
	err error
}

func (b brokenCheck) CheckSimple(context.Context, string, string) error { return b.err }
func (b brokenCheck) CheckWithTools(context.Context, string, string, []wire.ToolDefinition) error {
	return b.err
}

func TestACheckThatFailsForAnotherReasonIsNotRecordedAsNoRoom(t *testing.T) {
	ctx := context.Background()
	failed := errors.New("the client's profile does not validate")
	var said []Condition
	teller := &Teller{Report: func(c Condition) { said = append(said, c) }}
	raw := func() *mockStore {
		return &mockStore{unprocessedCnt: 3, experiences: []store.Experience{
			{ID: "e3", Content: "three", Raw: 1}, {ID: "e2", Content: "two", Raw: 1}, {ID: "e1", Content: "one", Raw: 1}}}
	}

	passes := map[string]func(model brokenCheck, lg *mockLedger) error{
		"consolidate": func(model brokenCheck, lg *mockLedger) error {
			lg.st = raw()
			c := NewConsolidate(lg.st, model, rememberingLedger{lg}, &mockRingWriter{}, ConsolidateConfig{Threshold: 1})
			c.SetTeller(teller)
			return c.Execute(ctx)
		},
		"outcomes": func(model brokenCheck, lg *mockLedger) error {
			c := NewConsolidate(nil, model, lg, nil, ConsolidateConfig{})
			c.SetOutcomes(&fakeOutcomes{self: strings.Repeat("cd", 32)})
			c.SetTeller(teller)
			return c.ObserveOutcomes(ctx)
		},
		"dream": func(model brokenCheck, lg *mockLedger) error {
			lg.st = raw()
			d := NewDream(lg.st, model, lg, &mockRingWriter{}, DreamConfig{Threshold: 1})
			d.SetTeller(&Teller{Report: teller.Report, Door: lg})
			return d.Execute(ctx)
		},
		"self_model": func(model brokenCheck, lg *mockLedger) error {
			sm := NewSelfModel(selfModelStore(), model, &mockSelfModelCommitter{})
			sm.SetDoor(lg)
			sm.SetTeller(teller)
			return sm.Execute(ctx)
		},
		"brief": func(model brokenCheck, lg *mockLedger) error {
			b := NewMorningBrief(&mockStore{}, model, &mockBriefWriter{}, MorningBriefConfig{})
			b.SetTeller(teller)
			return b.Execute(ctx)
		},
	}
	for name, pass := range passes {
		model, lg := brokenCheck{&mockLLM{}, failed}, &mockLedger{}
		said = nil
		if err := pass(model, lg); !errors.Is(err, failed) {
			t.Errorf("%s: a check that failed is reported as the error it is: %v", name, err)
		}
		if model.calls != 0 || len(lg.appended) != 0 {
			t.Errorf("%s: nothing is sent and nothing minted: %d call(s), %d record(s)", name, model.calls, len(lg.appended))
		}
		for _, c := range said {
			if c.Kind == ConditionNoRoom {
				t.Errorf("%s: A FAILED CHECK WAS SAID AS NO ROOM: %q", name, c.Said)
			}
		}
	}
}
