package cognitive

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/llm/wire"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// admitsLLM is a model whose input limit the test states as a rule over
// the request, asked as the production client is asked: before anything
// is sent, and refusing as it refuses, by type.
type admitsLLM struct {
	*mockLLM
	admits func(userMessage string) bool
}

func (a *admitsLLM) CheckSimple(_ context.Context, _, userMessage string) error {
	if !a.admits(userMessage) {
		return &wire.ContextLimitError{Required: 2, Limit: 1}
	}
	return nil
}

// A BATCH TOO LARGE FOR THE MODEL IS READ AS FAR AS IT FITS, AND THE REST
// WAIT THEIR TURN. A pass read a fixed number of experiences, oldest
// first, and when those did not fit the model's input limit with no
// conversation at all it sent nothing and advanced nothing. The next pass
// read the same ones. A batch the model could not take stopped the
// facility for good.
//
// Here six experiences wait and the model admits a request that holds at
// most three. The first pass sends three, and consumes exactly the three
// it sent; the second sends and consumes the other three.
func TestADreamBatchTooLargeForTheModelIsReadAsFarAsItFits(t *testing.T) {
	st := &mockStore{unprocessedCnt: 6}
	for i := 1; i <= 6; i++ {
		st.experiences = append(st.experiences, store.Experience{ID: fmt.Sprintf("e%d", i), Content: fmt.Sprintf("NOTED-e%d, a thing that happened", i), Raw: 1})
	}
	model := &admitsLLM{mockLLM: &mockLLM{override: "You notice a pattern in what happened."}, admits: func(m string) bool { return strings.Count(m, "NOTED-") <= 3 }}
	lg := &mockLedger{st: st}
	dream := NewDream(st, model, lg, &mockRingWriter{}, DreamConfig{Threshold: 1})
	waiting, _ := st.ListRawExperiencesExcept(6, "none") // oldest first, as the pass reads them

	if err := dream.Execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	if model.calls != 1 || strings.Count(model.lastUser, "NOTED-") != 3 {
		t.Fatalf("A BATCH THE MODEL CANNOT TAKE STOPPED THE PASS: %d call(s), %d of six experiences in what was sent; want one call carrying three", model.calls, strings.Count(model.lastUser, "NOTED-"))
	}
	runs := lg.runPayloads()
	if len(runs) != 1 || len(runs[0].Inputs) != 3 || st.unprocessedCnt != 3 {
		t.Fatalf("the pass consumes what it sent and no more: %+v, %d still waiting; want three consumed and three waiting", runs, st.unprocessedCnt)
	}
	for i, id := range runs[0].Inputs {
		if !strings.Contains(model.lastUser, "NOTED-"+id+",") {
			t.Fatalf("experience %s was consumed and was not in what the model read:\n%s", id, model.lastUser)
		}
		if id != waiting[i].ID {
			t.Fatalf("the pass read %v; the three that waited longest are %s, %s and %s", runs[0].Inputs, waiting[0].ID, waiting[1].ID, waiting[2].ID)
		}
	}
	first := model.lastUser
	if err := dream.Execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	if model.calls != 2 || st.unprocessedCnt != 0 || strings.Count(model.lastUser, "NOTED-") != 3 {
		t.Fatalf("the next pass reads the rest: %d call(s), %d still waiting, %d in what was sent", model.calls, st.unprocessedCnt, strings.Count(model.lastUser, "NOTED-"))
	}
	for _, id := range runs[0].Inputs {
		if strings.Contains(model.lastUser, "NOTED-"+id+",") {
			t.Fatalf("experience %s was read twice:\n%s\n---\n%s", id, first, model.lastUser)
		}
	}
}

// AN EXPERIENCE LONGER THAN THE MODEL TAKES IS READ AS FAR AS IT FITS. One
// experience that does not fit by itself can never be read whole, and
// waiting for it stops everything behind it. The pass reads the most of
// its content that fits, to the code point, with what marks another's
// words still whole around it, and consumes it.
func TestAnExperienceTooLongForTheModelIsReadAsFarAsItFits(t *testing.T) {
	long := store.Experience{ID: "e1", Content: strings.Repeat("ж", 5000), Provenance: "external", Raw: 1}
	st := &mockStore{unprocessedCnt: 2, experiences: []store.Experience{
		{ID: "e2", Content: "NOTED-e2, a short thing after it", Raw: 1},
		long,
	}}
	if got, _ := st.ListRawExperiencesExcept(1, "none"); len(got) != 1 || got[0].ID != "e1" {
		t.Fatalf("rig: the long experience is the oldest waiting: %+v", got)
	}
	model := &admitsLLM{mockLLM: &mockLLM{override: "You notice something long."}, admits: func(m string) bool { return strings.Count(m, "ж") < 1000 }}
	lg := &mockLedger{st: st}
	dream := NewDream(st, model, lg, &mockRingWriter{}, DreamConfig{Threshold: 1})
	if err := dream.Execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	if model.calls != 1 || strings.Count(model.lastUser, "ж") != 999 {
		t.Fatalf("AN EXPERIENCE TOO LONG FOR THE MODEL STOPPED THE PASS: %d call(s), %d of its characters sent; want one call with the 999 that fit", model.calls, strings.Count(model.lastUser, "ж"))
	}
	cut := long
	cut.Content = strings.Repeat("ж", 999)
	if !strings.Contains(model.lastUser, evidenceText(cut)) {
		t.Fatalf("what was sent is not the experience as it is rendered, shortened inside what marks it as another's words:\n%.300s", model.lastUser)
	}
	runs := lg.runPayloads()
	if len(runs) != 1 || len(runs[0].Inputs) != 1 || runs[0].Inputs[0] != "e1" || st.unprocessedCnt != 1 {
		t.Fatalf("the long experience is consumed and the one behind it waits its turn: %+v, %d waiting", runs, st.unprocessedCnt)
	}
}

// WHERE NOTHING FITS, NOTHING IS SENT. A model that admits no request at
// all, not even one with no experience in it, is a configuration that
// leaves no room: the pass sends nothing and consumes nothing, as before.
func TestADreamRequestThatDoesNotFitEmptySendsNothing(t *testing.T) {
	st := &mockStore{unprocessedCnt: 2, experiences: []store.Experience{
		{ID: "e1", Content: "NOTED-e1", Raw: 1}, {ID: "e2", Content: "NOTED-e2", Raw: 1},
	}}
	model := &admitsLLM{mockLLM: &mockLLM{override: "unreachable"}, admits: func(string) bool { return false }}
	lg := &mockLedger{st: st}
	dream := NewDream(st, model, lg, &mockRingWriter{}, DreamConfig{Threshold: 1})
	if err := dream.Execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	if model.calls != 0 || len(lg.appended) != 0 || st.unprocessedCnt != 2 {
		t.Fatalf("a request that fits in no form was sent or consumed: %d call(s), %v appended, %d waiting", model.calls, lg.appended, st.unprocessedCnt)
	}
}
