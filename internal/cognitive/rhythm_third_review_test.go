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

func TestADreamPassTellsOfAReadInPartOnlyOnceItHasLanded(t *testing.T) {
	unfinished := func(reason string) func(string) (string, error) {
		return func(string) (string, error) { return "", &wire.IncompleteResponseError{Reason: reason, Got: 40} }
	}
	says := func(text string) func(string) (string, error) {
		return func(string) (string, error) { return text, nil }
	}
	for _, stall := range []struct {
		name   string
		reply  func(string) (string, error)
		refuse func(ledger.EventType) error
	}{
		{name: "the call fails", reply: func(string) (string, error) { return "", errors.New("connection reset by peer") }},
		{name: "the reply is cut off", reply: unfinished("length")},
		{name: "the reply is declined", reply: unfinished("refusal")},
		{name: "the note is over its bound", reply: says(strings.Repeat("x", 700))},
		{name: "nothing comes back", reply: says("")},
		{name: "the run marker is refused", reply: says("You notice a pattern in what happened."), refuse: func(et ledger.EventType) error {
			if et == ledger.EventDreamRun {
				return errors.New("refused at the door")
			}
			return nil
		}},
	} {
		t.Run(stall.name, func(t *testing.T) {
			st := &mockStore{unprocessedCnt: 1, experiences: []store.Experience{{ID: "e1", Content: strings.Repeat("a long record of the day. ", 100), Raw: 1}}}
			model := &sizedModel{limit: 120, reply: stall.reply}
			lg := &mockLedger{st: st, refuse: stall.refuse}
			var said []Condition
			dream := NewDream(st, checkedModel{model}, lg, nil, DreamConfig{Threshold: 1, MaxChars: 600})
			dream.SetTeller(&Teller{Report: func(c Condition) { said = append(said, c) }, Door: rememberingLedger{lg}, Exists: st.EntityExists})
			if err := dream.Execute(context.Background()); err != nil {
				t.Fatal(err)
			}
			if len(model.sent) != 1 || !strings.Contains(model.sent[0], "a long record") || len(model.sent[0]) > 120 || len(lg.runPayloads()) != 0 || st.unprocessedCnt != 1 {
				t.Fatalf("rig: the experience is sent in part and the pass stalls: %d sent, %d run marker(s), %d still raw", len(model.sent), len(lg.runPayloads()), st.unprocessedCnt)
			}
			if told := toldIdentity(lg); len(told) != 0 {
				t.Fatalf("THE IDENTITY WAS TOLD OF A READING THAT DID NOT HAPPEN: the pass stalled, e1 is still raw, and the record holds %v", told)
			}
			for _, c := range said {
				if strings.Contains(c.Said, "consumed") && !strings.Contains(c.Said, "nothing consumed") {
					t.Fatalf("THE CARD WAS TOLD e1 IS CONSUMED, AND IT IS STILL RAW: %q", c.Said)
				}
			}

			// The same pass, landing.
			model.reply, lg.refuse = says("You notice a pattern in what happened."), nil
			if err := dream.Execute(context.Background()); err != nil {
				t.Fatal(err)
			}
			told := toldIdentity(lg)
			if len(told) != 1 || st.unprocessedCnt != 0 {
				t.Fatalf("a pass that lands tells of the experience it read in part, once: %d telling(s) %v, %d still raw", len(told), told, st.unprocessedCnt)
			}
			for _, content := range told {
				if !strings.Contains(content, "your experience e1 was longer than one dream pass can carry") || !strings.Contains(content, "consumed as read in part") {
					t.Fatalf("the telling is not of e1 read in part: %q", content)
				}
			}
			if last := len(lg.appended) - 1; lg.appended[last] != ledger.EventExperienceCreate || lg.appended[last-1] != ledger.EventDreamRun {
				t.Fatalf("the telling follows the run marker that consumed e1; the door admitted %v", lg.appended)
			}
			// The line begins with e1 and ENDS with its reading: nothing
			// is joined behind it where no conversation waited.
			if c := said[len(said)-1]; c.Kind != ConditionFitted || !strings.HasPrefix(c.Said, "the oldest experience that waited (e1) was longer") || !strings.HasSuffix(c.Said, "characters were read, and it is consumed as read in part") {
				t.Fatalf("the card is told of e1 read in part once it is: %+v", c)
			}
		})
	}
}

func TestABriefIsSaidDrawnFromPartOfTheDayOnlyOnceItIsWritten(t *testing.T) {
	st := &mockStore{}
	for i := 1; i <= 3; i++ {
		st.experiences = append(st.experiences, store.Experience{ID: fmt.Sprintf("x%d", i), Content: fmt.Sprintf("NOTED-x%d, a thing that happened", i), Category: "observation"})
	}
	model := &admitsLLM{mockLLM: &mockLLM{err: errors.New("connection reset by peer")}, admits: func(m string) bool { return strings.Count(m, "NOTED-") <= 1 }}
	bw := &mockBriefWriter{}
	var said []Condition
	brief := NewMorningBrief(st, model, bw, MorningBriefConfig{})
	brief.SetTeller(&Teller{Report: func(c Condition) { said = append(said, c) }})
	if err := brief.Execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	if model.calls != 1 || strings.Count(model.lastUser, "NOTED-") != 1 || bw.brief != "" {
		t.Fatalf("rig: one record of three is sent and the call fails: %d call(s), %d record(s) sent, brief %q", model.calls, strings.Count(model.lastUser, "NOTED-"), bw.brief)
	}
	if len(said) != 0 {
		t.Fatalf("THE CARD WAS TOLD OF A BRIEF THAT WAS NOT WRITTEN: %+v", said)
	}
	model.err, model.override = nil, "A short bridge."
	if err := brief.Execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	if bw.brief != "A short bridge." || len(said) != 1 || said[0].Kind != ConditionFitted || !strings.Contains(said[0].Said, "was drawn from 1 of 3 records of the day") {
		t.Fatalf("a brief written from part of the day says so: brief %q, the card was told %+v", bw.brief, said)
	}
}

func TestTheRequestSaysAnExperienceReadInPartIsConsumedByAPassThatCloses(t *testing.T) {
	st := &mockStore{unprocessedCnt: 2, experiences: []store.Experience{
		{ID: "e2", Content: "NOTED-e2 a short thing after it", Raw: 1},
		{ID: "e1", Content: "NOTED-e1 " + strings.Repeat("ж", 9000), Raw: 1},
	}}
	model := &sizedModel{limit: 4_500, reply: func(string) (string, error) { return "no envelope here", nil }}
	lg := &mockLedger{st: st}
	c := NewConsolidate(st, checkedModel{model}, rememberingLedger{lg}, &mockRingWriter{}, ConsolidateConfig{Threshold: 1})
	if err := c.Execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(model.sent) != 1 || !strings.Contains(model.sent[0], "is not in view: it is longer than one pass can carry, and a pass that closes consumes it as read in part]") {
		t.Fatalf("THE REQUEST SAYS MORE OF THE EXPERIENCE THAN A REFUSED REPLY BEARS OUT: %d sent\n%.400s", len(model.sent), strings.Join(model.sent, "\n---\n"))
	}
	if len(lg.runPayloads()) != 0 || st.unprocessedCnt != 2 || !c.aside.has("e1") {
		t.Fatalf("a reply that does not parse consumes nothing and sets the one experience aside: %+v, %d waiting, aside=%v", lg.runPayloads(), st.unprocessedCnt, c.aside.has("e1"))
	}
	told := toldIdentity(lg)
	if len(told) != 1 {
		t.Fatalf("the identity is told once, that e1 is set aside: %v", told)
	}
	for _, content := range told {
		if !strings.Contains(content, "e1") || !strings.Contains(content, "set aside") || strings.Contains(content, "read in part") {
			t.Fatalf("what the identity is told of an experience nothing consumed: %q", content)
		}
	}
}

// droppedChat is a correctedChat whose call number drop, counted from one,
// fails by transport: nothing is sent and nothing answers.
type droppedChat struct {
	correctedChat
	drop, calls int
}

func (m *droppedChat) Chat(ctx context.Context, messages []wire.Message, opts wire.ChatOptions) (*wire.Response, error) {
	if m.calls++; m.calls == m.drop {
		return nil, errors.New("connection reset by peer")
	}
	return m.correctedChat.Chat(ctx, messages, opts)
}

func TestThePortraitsCardLineIsSaidOfARequestThatWasSentAndAnswered(t *testing.T) {
	shown := func(request string) int { return strings.Count(request, "- [beliefs id=") }
	rig := func(prose, drop int) (*droppedChat, *mockLedger, map[string]Condition, *[]Condition, *SelfModelFacility) {
		st := selfModelStore()
		for i := 0; i < 300; i++ {
			st.beliefs = append(st.beliefs, store.Belief{ID: fmt.Sprintf("b%03d", i), Ring: 3,
				Statement: fmt.Sprintf("Belief %03d, at length: ", i) + strings.Repeat("a careful clause of what I hold. ", 10)})
		}
		model := &droppedChat{correctedChat: correctedChat{sizedChat: sizedChat{limit: 12_000}, prose: prose}, drop: drop}
		lg := &mockLedger{st: st}
		// The card as the app keeps it: one line a facility, the last said.
		card := map[string]Condition{"self_model": {Facility: "self_model", Kind: ConditionNoRoom, Said: "what the card said before"}}
		var said []Condition
		sm := NewSelfModel(st, model, &mockSelfModelCommitter{})
		sm.SetDoor(rememberingLedger{lg})
		sm.SetTeller(&Teller{Report: func(c Condition) { card[c.Facility] = c; said = append(said, c) }})
		return model, lg, card, &said, sm
	}

	// The first request, failing by transport and then answered.
	model, lg, card, said, sm := rig(0, 1)
	if err := sm.Execute(context.Background()); err == nil || len(model.sent) != 0 {
		t.Fatalf("rig: the first call fails by transport: %v, %d sent", err, len(model.sent))
	}
	if len(*said) != 0 || card["self_model"].Said != "what the card said before" {
		t.Fatalf("THE CARD WAS TOLD WHAT A MODEL WAS SHOWN THAT SAW NOTHING: %+v", *said)
	}
	if told := toldIdentity(lg); len(told) != 0 {
		t.Fatalf("THE IDENTITY WAS TOLD WHAT THEIR PORTRAIT IS DRAWN FROM, AND NOTHING WAS DRAWN: %v", told)
	}
	if err := sm.Execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(model.sent) != 1 || len(*said) != 1 || (*said)[0].Kind != ConditionFitted || !strings.Contains((*said)[0].Said, fmt.Sprintf("%d of 301 beliefs and 1 of 1 active intentions were shown", shown(model.sent[0]))) {
		t.Fatalf("a request sent and answered says what it was shown: %d sent, the card was told %+v", len(model.sent), *said)
	}
	if told := toldIdentity(lg); len(told) != 1 {
		t.Fatalf("the identity is told once what their portrait was not drawn from: %v", told)
	}

	// The corrective round, answered: each request says what it showed.
	model, _, _, said, sm = rig(1, 0)
	if err := sm.Execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(model.sent) != 2 || shown(model.sent[1]) >= shown(model.sent[0]) || len(*said) != 2 {
		t.Fatalf("rig: a first reply in prose and a corrective round of fewer beliefs: %d sent, the card was told %+v", len(model.sent), *said)
	}
	for i, c := range *said {
		if want := fmt.Sprintf("%d of 301 beliefs", shown(model.sent[i])); c.Kind != ConditionFitted || !strings.Contains(c.Said, want) {
			t.Fatalf("request %d showed %s; the card was told %+v", i+1, want, c)
		}
	}

	// The corrective round, failing by transport: what the first said stands.
	model, _, card, said, sm = rig(1, 2)
	if err := sm.Execute(context.Background()); err == nil || len(model.sent) != 1 {
		t.Fatalf("rig: the first request is answered in prose and the corrective call fails by transport: %v, %d sent", err, len(model.sent))
	}
	if want := fmt.Sprintf("%d of 301 beliefs", shown(model.sent[0])); len(*said) != 1 || !strings.Contains(card["self_model"].Said, want) {
		t.Fatalf("THE CARD WAS TOLD OF A CORRECTIVE ROUND NO MODEL SAW: the first request showed %s, and the card was told %+v", want, *said)
	}
}

func TestDreamsCardLineCarriesAllTheFitLeftOut(t *testing.T) {
	const (
		experiences = "3 of 6 experiences fit the model's input budget; the rest wait for the next pass, and no conversation is read beside them"
		talkNone    = "0 of 1 selected conversation part(s) fit the model's input budget beside the rest of the request; what was left out stays unread for the next pass"
		talkInPart  = "1 of 1 selected conversation part(s) fit the model's input budget beside the rest of the request, the last of them in part; what was left out stays unread for the next pass"
	)
	six := func() *mockStore {
		st := &mockStore{unprocessedCnt: 6}
		for i := 1; i <= 6; i++ {
			st.experiences = append(st.experiences, store.Experience{ID: fmt.Sprintf("e%d", i), Content: fmt.Sprintf("NOTED-e%d, a thing that happened", i), Raw: 1})
		}
		return st
	}
	threeFit := func(m string) bool { return strings.Count(m, "NOTED-") <= 3 }
	for _, c := range []struct {
		name   string
		st     DreamStore
		talk   ConversationSource
		admits func(string) bool
		want   string
	}{
		{"experiences fitted, alone", six(), nil, threeFit, experiences},
		{"conversation read in part, alone", noRaw{}, &longTurn{}, func(m string) bool { return len(m) <= 400 }, talkInPart},
		{"experiences fitted and conversation waiting", six(), &oneTurn{}, threeFit, experiences + "; " + talkNone},
	} {
		t.Run(c.name, func(t *testing.T) {
			var said []Condition
			st, _ := c.st.(*mockStore)
			dream := NewDream(c.st, &admitsLLM{mockLLM: &mockLLM{override: "You notice a pattern in what happened."}, admits: c.admits}, &mockLedger{st: st}, nil, DreamConfig{Threshold: 1})
			if c.talk != nil {
				dream.SetConversation(c.talk)
			}
			dream.SetTeller(&Teller{Report: func(c Condition) { said = append(said, c) }})
			if err := dream.Execute(context.Background()); err != nil {
				t.Fatal(err)
			}
			if len(said) != 1 || said[0].Kind != ConditionFitted || said[0].Said != c.want {
				t.Fatalf("DREAM'S LINE ON THE CARD:\n got %+v\nwant one fitted line: %s", said, c.want)
			}
		})
	}

	// The oldest experience read in part, and conversation waiting: the
	// line at the landing says both.
	st := &mockStore{unprocessedCnt: 1, experiences: []store.Experience{{ID: "e1", Content: strings.Repeat("a long record of the day. ", 100), Raw: 1}}}
	var said []Condition
	talk := &oneTurn{}
	dream := NewDream(st, &admitsLLM{mockLLM: &mockLLM{override: "You notice a pattern in what happened."}, admits: func(m string) bool { return len(m) <= 120 }}, &mockLedger{st: st}, nil, DreamConfig{Threshold: 1})
	dream.SetConversation(talk)
	dream.SetTeller(&Teller{Report: func(c Condition) { said = append(said, c) }})
	if err := dream.Execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	if st.unprocessedCnt != 0 || talk.read || len(said) != 2 || said[0].Said != talkNone {
		t.Fatalf("rig: the experience is read in part and consumed, the conversation stays unread and is said at the fit: %d raw, read=%v, %+v", st.unprocessedCnt, talk.read, said)
	}
	if last := said[1]; last.Kind != ConditionFitted || !strings.HasPrefix(last.Said, "the oldest experience that waited (e1) was longer") || !strings.HasSuffix(last.Said, "it is consumed as read in part; "+talkNone) {
		t.Fatalf("THE LANDING'S LINE REPLACED THE CONVERSATION'S: %+v", last)
	}
}
