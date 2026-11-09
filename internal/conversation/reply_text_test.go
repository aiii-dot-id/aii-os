package conversation

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/limits"
	"github.com/aiii-dot-id/aii-os/internal/llm"
)

// A TURN TELLS ITS REPLY AS IT IS WRITTEN, CALL BY CALL. Each
// call's text is told piece by piece to a ReplyEmitter, then the call's end
// says what the turn took from it, or that none of it was kept and why. A
// call that returns is kept, whatever a later call does; only the call that
// fails, is stopped or is asked again is withdrawn. A turn whose request
// carries an output contract tells nothing, and a plain Emitter is told
// nothing.

// scriptedCall is one call of a scripted provider: the pieces it writes,
// then its response or its error; cancel stops the turn after the pieces.
type scriptedCall struct {
	pieces []string
	resp   llm.Response
	err    error
	cancel bool
}

type scriptedLLM struct {
	calls  []scriptedCall
	n      int
	told   bool // a call's options carried an observer
	cancel context.CancelFunc
}

func (s *scriptedLLM) Chat(ctx context.Context, _ []llm.Message, o llm.ChatOptions) (*llm.Response, error) {
	c := s.calls[s.n]
	s.n++
	if o.OnText != nil {
		s.told = true
		for _, p := range c.pieces {
			o.OnText(p)
		}
	}
	if c.cancel {
		s.cancel()
		return nil, ctx.Err()
	}
	if c.err != nil {
		return nil, c.err
	}
	r := c.resp
	return &r, nil
}

// replyLog is a ReplyEmitter that keeps what it was told, in order.
type replyLog struct {
	fakeEmitter
	said []string
}

func (r *replyLog) ReplyText(call int, piece string) {
	r.said = append(r.said, "text "+string(rune('0'+call))+" "+piece)
}

func (r *replyLog) ReplyEnded(call int, taken, withdrawn string) {
	if withdrawn != "" {
		r.said = append(r.said, "withdrawn "+string(rune('0'+call))+" "+withdrawn)
		return
	}
	r.said = append(r.said, "kept "+string(rune('0'+call))+" "+taken)
}

func TestATurnTellsItsReplyCallByCall(t *testing.T) {
	read := scriptedCall{pieces: []string{"Reading ", "the file."}, resp: resp("Reading the file.", toolCall("c1", "bash", `{}`))}
	for _, c := range []struct {
		name   string
		second scriptedCall
		said   []string
		spoken string
		failed bool
	}{
		{"both calls kept", scriptedCall{pieces: []string{"Done."}, resp: resp("Done.")},
			[]string{"text 1 Reading ", "text 1 the file.", "kept 1 Reading the file.", "text 2 Done.", "kept 2 Done."},
			"Reading the file.\n\nDone.", false},
		{"the second fails after text", scriptedCall{pieces: []string{"Half"}, err: errors.New("provider 500")},
			[]string{"text 1 Reading ", "text 1 the file.", "kept 1 Reading the file.", "text 2 Half", "withdrawn 2 provider"},
			"Reading the file.", true},
		{"the second is stopped after text", scriptedCall{pieces: []string{"Half"}, cancel: true},
			[]string{"text 1 Reading ", "text 1 the file.", "kept 1 Reading the file.", "text 2 Half", "withdrawn 2 stopped"},
			"Reading the file.", true},
		{"the second's final text differs from its pieces", scriptedCall{pieces: []string{"Hel", "lo"}, resp: resp("Hello there.")},
			[]string{"text 1 Reading ", "text 1 the file.", "kept 1 Reading the file.", "text 2 Hel", "text 2 lo", "kept 2 Hello there."},
			"Reading the file.\n\nHello there.", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			client := &scriptedLLM{calls: []scriptedCall{read, c.second}, cancel: cancel}
			log := &replyLog{}
			loop := New(client, &fakeTools{results: map[string]string{"bash": "ok"}}, &fakeDefs{}, nil, log, testBounds(Config{MaxIterations: limits.Of(4)}))
			res, err := loop.Run(ctx, "system", []llm.Message{{Role: "user", Content: "go"}})
			if (err != nil) != c.failed {
				t.Fatalf("the turn's error: %v", err)
			}
			if strings.Join(log.said, "|") != strings.Join(c.said, "|") {
				t.Fatalf("told:\n%s\nwant:\n%s", strings.Join(log.said, "\n"), strings.Join(c.said, "\n"))
			}
			if res.Spoken != c.spoken {
				t.Fatalf("the turn kept %q, want %q", res.Spoken, c.spoken)
			}
		})
	}
}

// A call refused for a tool's definition is sent again without it: what it
// told before the refusal is withdrawn as asked again, and the call sent
// again is told as a call of its own.
func TestACallAskedAgainIsWithdrawnAsSuch(t *testing.T) {
	client := &scriptedLLM{calls: []scriptedCall{
		{pieces: []string{"Before "}, err: &llm.ToolSchemaRefusedError{Tool: "bash", Err: errors.New("schema")}},
		{pieces: []string{"Answer."}, resp: resp("Answer.")},
	}}
	log := &replyLog{}
	loop := New(client, &fakeTools{}, &fakeDefs{defs: []llm.ToolDefinition{requestDef("bash")}}, nil, log, testBounds(Config{MaxIterations: limits.Of(4)}))
	if _, err := loop.Run(t.Context(), "system", []llm.Message{{Role: "user", Content: "go"}}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(log.said, "|"); got != "text 1 Before |withdrawn 1 asked again|text 2 Answer.|kept 2 Answer." {
		t.Fatalf("told %q", got)
	}
}

// Nothing is told for a turn whose request carries an output contract, nor
// to an emitter that is not a ReplyEmitter.
func TestNothingIsToldWithoutAReplyEmitterOrWithAnOutputContract(t *testing.T) {
	answer := scriptedCall{pieces: []string{"{\"answer\":"}, resp: resp(`{"answer":"yes"}`)}
	for _, c := range []struct {
		name string
		ctx  context.Context
		em   Emitter
	}{
		{"an output contract", requestContext(t, RequestOptions{ResponseFormat: testResponseFormat()}), &replyLog{}},
		{"a plain emitter", t.Context(), &fakeEmitter{}},
	} {
		client := &scriptedLLM{calls: []scriptedCall{answer}}
		loop := New(client, &fakeTools{}, &fakeDefs{}, nil, c.em, testBounds(Config{MaxIterations: limits.Of(2)}))
		if _, err := loop.Run(c.ctx, "system", []llm.Message{{Role: "user", Content: "go"}}); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if client.told {
			t.Errorf("%s: the provider was handed an observer", c.name)
		}
		if log, ok := c.em.(*replyLog); ok && len(log.said) != 0 {
			t.Errorf("%s: told %q", c.name, log.said)
		}
	}
}
