package conversation

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/llm"
)

// .
// .
// .
type failAfterSpeech struct {
	calls int
	err   error
}

func (f *failAfterSpeech) Chat(_ context.Context, _ []llm.Message, _ llm.ChatOptions) (*llm.Response, error) {
	f.calls++
	if f.calls == 1 {
		r := resp("I have read the file and I think the bug is in the parser.", toolCall("c", "bash", `{}`))
		return &r, nil
	}
	return nil, f.err
}

// .
// .
// .
// .
// .
// .
// .
func TestAnInterruptedTurnKeepsWhatWasSaid(t *testing.T) {
	for _, c := range []struct {
		name      string
		err       error
		wantCause string
	}{
		{"operator stop", context.Canceled, "stopped by the operator"},
		{"out of time", context.DeadlineExceeded, "ran out of time"},
		{"substrate fault", errors.New("provider 500"), "substrate failed"},
	} {
		t.Run(c.name, func(t *testing.T) {
			client := &failAfterSpeech{err: c.err}
			loop := New(client, &fakeTools{results: map[string]string{"bash": "ok"}}, &fakeDefs{}, nil, nil,
				Config{MaxIterations: 4})
			res, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}})

			// .
			// .
			if err == nil {
				t.Fatal("an interrupted turn must still report its error")
			}
			if res.Spoken == "" {
				t.Fatal("THE IDENTITY'S WORDS WERE ERASED BY THE INTERRUPTION — the transcript keeps the tool events and loses the reasoning that drove them")
			}
			if !strings.Contains(res.Spoken, "the bug is in the parser") {
				t.Fatalf("the preserved reply is not verbatim: %q", res.Spoken)
			}
			if res.Interrupted == "" {
				t.Fatal("an incomplete reply must be marked incomplete, or it reads as a finished answer")
			}
			if !strings.Contains(res.Interrupted, c.wantCause) {
				t.Fatalf("the cause must name WHAT ended the turn: got %q, want something naming %q", res.Interrupted, c.wantCause)
			}
		})
	}
}

// .
// .
func TestACleanTurnIsNotMarkedInterrupted(t *testing.T) {
	client := &captureLLM{script: []llm.Response{textResp("done")}}
	loop := New(client, &fakeTools{}, &fakeDefs{}, nil, nil, Config{MaxIterations: 2})
	res, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Interrupted != "" {
		t.Fatalf("a clean turn was marked interrupted: %q", res.Interrupted)
	}

	silent := &failAfterSpeech{calls: 1, err: errors.New("provider 500")}
	loop2 := New(silent, &fakeTools{}, &fakeDefs{}, nil, nil, Config{MaxIterations: 2})
	res2, err2 := loop2.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}})
	if err2 == nil {
		t.Fatal("the failing turn must report its error")
	}
	if res2.Spoken != "" || res2.Interrupted != "" {
		t.Fatalf("a turn that said nothing has nothing to preserve: %+v", res2)
	}
}
