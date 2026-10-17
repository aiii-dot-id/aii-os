package cognitive

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/llm/wire"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/aiii-dot-id/aii-os/internal/store/cursor"
)

func TestACorrectiveRoundThatCannotBeFittedKeepsTheFirstRefusalAndSaysWhatWasSent(t *testing.T) {
	st := selfModelStore()
	model := &correctedChat{sizedChat: sizedChat{limit: 1 << 20}, prose: 2, tighten: true}
	lg := &mockLedger{st: st}
	var said []Condition
	sm := NewSelfModel(st, model, &mockSelfModelCommitter{})
	sm.SetDoor(rememberingLedger{lg})
	sm.SetTeller(&Teller{Report: func(c Condition) { said = append(said, c) }})

	err := sm.Execute(context.Background())
	if err == nil || !strings.Contains(err.Error(), "expected one commit call or exact NO_CHANGE") {
		t.Fatalf("THE FIRST REFUSAL WAS DROPPED: the pass returned %v", err)
	}
	if len(model.sent) != 1 || model.refused != 0 {
		t.Fatalf("rig: one request is sent and answered, and the corrective round is not put to the client: %d sent, %d refused at send", len(model.sent), model.refused)
	}
	last := said[len(said)-1]
	if last.Kind != ConditionNoRoom || !strings.Contains(last.Said, "first reply was refused") || !strings.Contains(last.Said, "corrective round was not sent") || strings.Contains(last.Said, "nothing sent") {
		t.Fatalf("the card is told what happened: %+v", last)
	}
	told := toldIdentity(lg)
	if len(told) != 1 {
		t.Fatalf("the identity is told once: %v", told)
	}
	for _, content := range told {
		if strings.Contains(content, "nothing was sent") || !strings.Contains(content, "your first reply was refused") || !strings.Contains(content, "was not sent") {
			t.Fatalf("THE IDENTITY WAS TOLD SOMETHING FALSE: one request was sent and answered, and they were told %q", content)
		}
	}

	model.limit = 1 << 20
	if err := sm.Execute(context.Background()); err == nil {
		t.Fatal("rig: the second pass fails as the first did")
	}
	if again := toldIdentity(lg); len(again) != 1 {
		t.Fatalf("the same refusal met the same way is told once: %v", again)
	}
}

// finishChat is SELF_MODEL's model ending every reply with one finish
// reason, each reply longer than the last.
type finishChat struct {
	reason string
	calls  int
	users  []string
}

func (m *finishChat) Chat(_ context.Context, messages []wire.Message, _ wire.ChatOptions) (*wire.Response, error) {
	m.calls++
	m.users = append(m.users, messages[len(messages)-1].Content)
	return &wire.Response{Choices: []wire.Choice{{Message: wire.Message{Content: strings.Repeat("who I am ", m.calls)}, FinishReason: m.reason}}, ModelID: "finish-chat"}, nil
}

// facilityFailures is the failure experiences a SELF_MODEL pass appended.
func facilityFailures(lg *mockLedger) int {
	n := 0
	for i := range lg.appended {
		if p, ok := lg.payloads[i].(map[string]any); ok && strings.HasPrefix(fmt.Sprint(p["id"]), "exp_facility_") {
			n++
		}
	}
	return n
}

func TestARepeatingPortraitFailureIsRecordedOnceAndAPausedReplyIsNoViolation(t *testing.T) {
	ctx := context.Background()
	for name, model := range map[string]SelfModelLLM{
		"prose":   &mockLLM{override: "who I am, in prose, with no commit call"},
		"cut off": &finishChat{reason: "length"},
	} {
		st := selfModelStore()
		lg := &mockLedger{st: st}
		sm := NewSelfModel(st, model, &mockSelfModelCommitter{})
		sm.SetDoor(rememberingLedger{lg})
		for pass := 0; pass < 6; pass++ {
			if err := sm.Execute(ctx); err == nil {
				t.Fatalf("%s: rig: every pass fails twice", name)
			}
		}
		if n := facilityFailures(lg); n != 1 {
			t.Errorf("%s: SIX FAILING PASSES LEFT %d FAILURE EXPERIENCES; the record is asked first, and holds one", name, n)
		}
	}

	for _, reason := range []string{"pause_turn", "tool_calls", "content_filter", "a_reason_no_build_knows"} {
		st := selfModelStore()
		lg := &mockLedger{st: st}
		model := &finishChat{reason: reason}
		sm := NewSelfModel(st, model, &mockSelfModelCommitter{})
		sm.SetDoor(rememberingLedger{lg})
		var unfinished *wire.IncompleteResponseError
		if err := sm.Execute(ctx); !errors.As(err, &unfinished) || unfinished.Reason != reason {
			t.Errorf("%s: the pass fails with the reply's own ending, and waits: %v", reason, err)
		}
		if model.calls != 1 || len(lg.appended) != 0 {
			t.Errorf("%s: A PROVIDER'S CONDITION WAS READ AS A CONTRACT VIOLATION: %d call(s), %d record(s); want one call, no corrective round and nothing recorded", reason, model.calls, len(lg.appended))
		}
	}
}

// windowChat is SELF_MODEL's model under an input ceiling in tokens that
// counts the system prompt and the tools with the user message, as the
// client does.
type windowChat struct {
	budget int
	sizedChat
}

func (m *windowChat) CheckWithTools(_ context.Context, system, user string, tools []wire.ToolDefinition) error {
	if need := messageTokens("system", system) + requestReserve("", user, tools) - messageTokens("system", preambleJoin); need > m.budget {
		return &wire.ContextLimitError{Required: need, Limit: m.budget}
	}
	return nil
}

// fillingRings is an authority preamble that takes all of the budget the
// reserve does not: rings that fold exactly as far as they are asked to.
type fillingRings struct{ budget int }

func (f fillingRings) AuthorityPreamble() (string, error) {
	text, _, err := f.AuthorityPrefixWithin(0)
	return text, err
}
func (f fillingRings) AuthorityPrefixWithin(reserve int) (string, int, error) {
	return strings.Repeat("r", 3*max(1, f.budget-reserve)), 0, nil
}

func TestThePortraitsReserveIsForARequestThatCanBeSent(t *testing.T) {
	st := selfModelStore() // a belief, an experience, an intention and the relationship: four kinds, the intention among them
	st.intentions[0].Statement = strings.Repeat("an intention with some length to it. ", 10)
	model := &windowChat{budget: 4_000, sizedChat: sizedChat{limit: 1 << 20}}
	committer := &mockSelfModelCommitter{}
	var said []Condition
	sm := NewSelfModel(st, model, committer)
	sm.SetAuthority(fillingRings{budget: 4_000})
	sm.SetTeller(&Teller{Report: func(c Condition) { said = append(said, c) }})
	if err := sm.Execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(model.sent) != 1 || committer.calls != 1 || !strings.Contains(model.sent[0], "[intentions id=i1]") {
		t.Fatalf("THE RESERVE LEFT NO ROOM FOR THE ONE INTENTION THE PORTRAIT NEEDS: %d sent, %d commit(s), the card was told %+v", len(model.sent), committer.calls, said)
	}
}

func TestTheRenderOnlyPassLogsACheckThatFailedAsTheErrorItIs(t *testing.T) {
	render := func(model LLMCaller) string {
		st := &mockStore{beliefs: []store.Belief{{ID: "b1", Statement: "Testing reveals truth", Ring: 3}}}
		logs := logsink.CaptureForTest(t)
		c := NewConsolidate(st, model, &mockLedger{st: st}, &mockRingWriter{}, ConsolidateConfig{Threshold: 1})
		if err := c.Execute(context.Background()); err != nil {
			t.Fatal(err)
		}
		return logs.String()
	}
	failed := render(brokenCheck{&mockLLM{}, errors.New("the client's profile does not validate")})
	if !strings.Contains(failed, "consolidate.error") || !strings.Contains(failed, "the client's profile does not validate") || strings.Contains(failed, "does not fit") {
		t.Fatalf("A FAILED CHECK WAS LOGGED AS A REQUEST THAT DOES NOT FIT:\n%s", failed)
	}
	over := render(&admitsLLM{mockLLM: &mockLLM{}, admits: func(string) bool { return false }})
	if !strings.Contains(over, "consolidate.budget") || !strings.Contains(over, "does not fit the model's input budget of 1 tokens") {
		t.Fatalf("a render the client refuses for size is said as one that does not fit, with the ceiling the client named:\n%s", over)
	}
}

func TestATellingConsolidationReadsInPartIsNotToldOf(t *testing.T) {
	telling := tellingPrefix + "0123456789abcdef"
	st := &mockStore{unprocessedCnt: 1, experiences: []store.Experience{{ID: telling, Content: "consolidation: " + strings.Repeat("ж", 9000), Provenance: "system", Raw: 1}}}
	model := &sizedModel{limit: 4_500, reply: upsertOn}
	lg := &mockLedger{st: st}
	var said []Condition
	c := NewConsolidate(st, checkedModel{model}, rememberingLedger{lg}, &mockRingWriter{}, ConsolidateConfig{Threshold: 1})
	c.SetTeller(&Teller{Report: func(c Condition) { said = append(said, c) }})
	if err := c.Execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(model.sent) != 1 || !strings.Contains(model.sent[0], "is not in view") || st.unprocessedCnt != 0 {
		t.Fatalf("rig: the telling is read in part and consumed: %d sent, %d waiting", len(model.sent), st.unprocessedCnt)
	}
	if kinds := saidOf(said, "consolidate"); len(kinds) != 1 || kinds[0] != ConditionFitted {
		t.Fatalf("the card is told the telling was read in part: %v", kinds)
	}
	if told := toldIdentity(lg); len(told) != 0 {
		t.Fatalf("A TELLING WAS TOLD OF: %v", told)
	}
}

// longTurn is a transcript holding one unread turn of some length. What
// is left of it after a pass that read it in part is not served.
type longTurn struct{ oneTurn }

func (o *longTurn) NextConversation(int, string) (cursor.ConversationBatch, error) {
	if o.read {
		return readTo(o.at), nil
	}
	text := strings.Repeat("a long thing the operator said. ", 40)
	return cursor.ConversationBatch{
		Through: cursor.TurnCursor{Turn: "turn_a", Position: cursor.TurnLength(text)},
		Parts:   []cursor.TurnPart{{ID: "turn_a", Role: "operator", Text: text, Whole: true}},
	}, nil
}

func TestWhatDreamAndTheBriefComeToIsOnTheCard(t *testing.T) {
	ctx := context.Background()
	var said []Condition
	teller := &Teller{Report: func(c Condition) { said = append(said, c) }}
	cut := &wire.IncompleteResponseError{Reason: "length", Got: 40}
	raw := func() *mockStore {
		return &mockStore{unprocessedCnt: 1, experiences: []store.Experience{{ID: "e1", Content: "a thing that happened", Raw: 1}}}
	}

	// DREAM: a reply cut off, then a note over its bound, then a whole pass.
	st := raw()
	model := &mockLLM{err: cut}
	dream := NewDream(st, model, &mockLedger{st: st}, &mockRingWriter{}, DreamConfig{Threshold: 1, MaxChars: 100})
	dream.SetTeller(teller)
	step := func() {
		t.Helper()
		if err := dream.Execute(ctx); err != nil {
			t.Fatal(err)
		}
	}
	step()
	if len(said) != 1 || said[0].Kind != ConditionRefused || !strings.Contains(said[0].Said, "cut off at the output limit") || !strings.Contains(said[0].Said, "asked for again") {
		t.Fatalf("A DREAM REPLY CUT OFF REACHED ONLY THE LOG: %+v", said)
	}
	model.err, model.override = nil, strings.Repeat("x", 500)
	step()
	if last := said[len(said)-1]; last.Kind != ConditionRefused || !strings.Contains(last.Said, "500 characters, over the 100-character bound") {
		t.Fatalf("A NOTE REFUSED OVER ITS BOUND REACHED ONLY THE LOG: %+v", last)
	}
	model.override = "You notice a pattern."
	step()
	if got := fmt.Sprint(saidOf(said, "dream")); got != "[refused refused whole]" || st.unprocessedCnt != 0 {
		t.Fatalf("a whole pass clears the line: the card was told %s, %d still wait", got, st.unprocessedCnt)
	}
	// A transport failure says nothing of what was asked, and nothing on the card.
	st, said = raw(), nil
	dream = NewDream(st, &mockLLM{err: errors.New("connection reset")}, &mockLedger{st: st}, nil, DreamConfig{Threshold: 1})
	dream.SetTeller(teller)
	step()
	if len(said) != 0 {
		t.Fatalf("a transport failure was said as the request's own: %+v", said)
	}

	// DREAM: one part of conversation, of which only the beginning fits.
	said = nil
	talk := &longTurn{}
	fits := &admitsLLM{mockLLM: &mockLLM{override: "You notice what the operator said."}, admits: func(m string) bool { return len(m) <= 400 }}
	dream = NewDream(noRaw{}, fits, &captureDoor{}, nil, DreamConfig{})
	dream.SetConversation(talk)
	dream.SetTeller(teller)
	step()

	if !talk.read || len(said) != 1 || said[0].Kind != ConditionFitted || said[0].Said != "1 of 1 selected conversation part(s) fit the model's input budget beside the rest of the request, the last of them in part; what was left out stays unread for the next pass" {
		t.Fatalf("A CONVERSATION OF ONE PART READ IN PART SAID NOTHING: read=%v, the card was told %+v\n%s", talk.read, said, fits.lastUser)
	}

	// DREAM: a transcript read to its end, and six experiences of which
	// three fit. The store answers such a transcript with no part and the
	// cursor where it stands, which no conversation fitted to nothing
	// equals: "0 of 0 selected conversation part(s) fit" was said over the
	// experiences' own line.
	said = nil
	ended := &oneTurn{}
	turn, _ := ended.NextConversation(1, "")
	_ = ended.PublishConversationCursor(turn)
	six := &mockStore{unprocessedCnt: 6}
	for i := 1; i <= 6; i++ {
		six.experiences = append(six.experiences, store.Experience{ID: fmt.Sprintf("e%d", i), Content: fmt.Sprintf("NOTED-e%d, a thing that happened", i), Raw: 1})
	}
	three := &admitsLLM{mockLLM: &mockLLM{override: "You notice a pattern in what happened."}, admits: func(m string) bool { return strings.Count(m, "NOTED-") <= 3 }}
	dream = NewDream(six, three, &mockLedger{st: six}, nil, DreamConfig{Threshold: 1})
	dream.SetConversation(ended)
	dream.SetTeller(teller)
	step()
	if batch, _ := ended.NextConversation(1, ""); len(batch.Parts) != 0 || batch.Through != turn.Through || batch.Moved() {
		t.Fatalf("rig: a read transcript answers as the store does, no part and the cursor where it stands: %+v", batch)
	}
	if len(said) != 1 || said[0].Kind != ConditionFitted || said[0].Said != "3 of 6 experiences fit the model's input budget; the rest wait for the next pass, and no conversation is read beside them" || six.unprocessedCnt != 3 {
		t.Fatalf("EXPERIENCES FITTED WITH NO CONVERSATION WAITING WERE SAID AS A CONVERSATION THAT DID NOT FIT: the card was told %+v, %d still wait", said, six.unprocessedCnt)
	}

	// The brief: a reply cut off; then one over its bound whose second ask fails.
	said = nil
	brief := NewMorningBrief(&mockStore{}, &mockLLM{err: cut}, &mockBriefWriter{}, MorningBriefConfig{MaxChars: 900})
	brief.SetTeller(teller)
	if err := brief.Execute(ctx); err != nil {
		t.Fatal(err)
	}
	if len(said) != 1 || said[0].Kind != ConditionRefused || !strings.Contains(said[0].Said, "cut off at the output limit") || !strings.Contains(said[0].Said, "yesterday's stands") {
		t.Fatalf("A BRIEF CUT OFF REACHED ONLY THE LOG: %+v", said)
	}
	said = nil
	bw := &mockBriefWriter{}
	brief = NewMorningBrief(&mockStore{}, &scriptLLM{replies: []string{strings.Repeat("word ", 300)}}, bw, MorningBriefConfig{MaxChars: 900})
	brief.SetTeller(teller)
	if err := brief.Execute(ctx); err != nil {
		t.Fatal(err)
	}
	if len(said) != 1 || said[0].Kind != ConditionRefused || !strings.Contains(said[0].Said, "1500 characters, over the 900-character bound") || !strings.Contains(said[0].Said, "the one ask for a shorter one failed") || bw.brief != "" {
		t.Fatalf("A BRIEF WRITTEN AND LOST TO A FAILED SECOND ASK REACHED ONLY THE LOG: %+v (kept %q)", said, bw.brief)
	}
}

func TestAConditionNamesTheRoomTheHostSays(t *testing.T) {
	for _, room := range []string{"", "a context_length declared for the model"} {
		var said []Condition
		teller := &Teller{Report: func(c Condition) { said = append(said, c) }}
		want := WiderWindow
		if room != "" {
			teller.Room, want = func() string { return room }, room
		}
		oc := NewConsolidate(nil, &admitsLLM{mockLLM: &mockLLM{}, admits: func(string) bool { return false }}, &captureDoor{}, nil, ConsolidateConfig{})
		oc.SetOutcomes(&fakeOutcomes{self: strings.Repeat("cd", 32)})
		oc.SetTeller(teller)
		_ = oc.ObserveOutcomes(context.Background()) // no outcome fits: said, and an error
		if len(said) != 1 || said[0].Kind != ConditionNoRoom || !strings.Contains(said[0].Said, want+" makes room") || (room != "" && strings.Contains(said[0].Said, WiderWindow)) {
			t.Errorf("with the host saying %q the card was told %+v", room, said)
		}
	}
}
