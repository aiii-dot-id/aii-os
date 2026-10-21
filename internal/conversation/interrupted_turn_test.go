package conversation

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/limits"
	"github.com/aiii-dot-id/aii-os/internal/llm"
)

// failAfterSpeech says something aloud, asks for a tool, and then fails
// the way a cancelled or broken turn fails — after the identity has
// already spoken.
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

// AN INTERRUPTED TURN KEEPS WHAT WAS SAID. Every early return dropped
// the accumulated speech, so cancelling after nine rounds of narration
// left the durable history holding the operator's message, the tool
// events, and none of the reasoning that produced them — and the next
// turn replayed a transcript missing its own words.
//
// Cancel stops work. It does not erase history.
func TestAnInterruptedTurnKeepsWhatWasSaid(t *testing.T) {
	for _, c := range []struct {
		name      string
		err       error
		wantCause string
	}{
		// A cancel that reaches the loop with no cause named: the operator
		// is not blamed for it (TestACancelledTurnIsToldWhatCancelledIt).
		{"a cancel nobody named", context.Canceled, "what cancelled it is not known"},
		{"out of time", context.DeadlineExceeded, "ran out of time"},
		{"substrate fault", errors.New("provider 500"), "substrate failed"},
	} {
		t.Run(c.name, func(t *testing.T) {
			client := &failAfterSpeech{err: c.err}
			loop := New(client, &fakeTools{results: map[string]string{"bash": "ok"}}, &fakeDefs{}, nil, nil,
				testBounds(Config{MaxIterations: limits.Of(4)}))
			res, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}})

			// The error is STILL returned: preserving speech must not
			// make a failed turn look successful.
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

// The control: a turn that ends cleanly is not marked interrupted, and
// a failure BEFORE anything was said has nothing to preserve.
func TestACleanTurnIsNotMarkedInterrupted(t *testing.T) {
	client := &captureLLM{script: []llm.Response{textResp("done")}}
	loop := New(client, &fakeTools{}, &fakeDefs{}, nil, nil, testBounds(Config{MaxIterations: limits.Of(2)}))
	res, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Interrupted != "" {
		t.Fatalf("a clean turn was marked interrupted: %q", res.Interrupted)
	}

	silent := &failAfterSpeech{calls: 1, err: errors.New("provider 500")} // fails on its FIRST call
	loop2 := New(silent, &fakeTools{}, &fakeDefs{}, nil, nil, testBounds(Config{MaxIterations: limits.Of(2)}))
	res2, err2 := loop2.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}})
	if err2 == nil {
		t.Fatal("the failing turn must report its error")
	}
	if res2.Spoken != "" || res2.Interrupted != "" {
		t.Fatalf("a turn that said nothing has nothing to preserve: %+v", res2)
	}
}

func TestAProvidersAnswerIsNamedToTheIdentityByItsStatus(t *testing.T) {
	const body = "slow down, account acct-7f3a: see https://provider.example/limits"
	for _, c := range []struct {
		name  string
		err   *llm.HTTPError
		cause string
	}{
		{"too many requests, with a wait", &llm.HTTPError{StatusCode: 429, Body: body, RetryAfter: 30 * time.Second, Retryable: true},
			"the model provider refused the request: HTTP 429 Too Many Requests; it asked for a wait of 30s"},
		{"too many requests, no wait given", &llm.HTTPError{StatusCode: 429, Body: body},
			"the model provider refused the request: HTTP 429 Too Many Requests"},
		{"a credential it will not take", &llm.HTTPError{StatusCode: 401, Body: body},
			"the model provider refused the request: HTTP 401 Unauthorized"},
		{"a request it will not accept", &llm.HTTPError{StatusCode: 400, Body: body},
			"the model provider refused the request: HTTP 400 Bad Request"},
		{"its own failure", &llm.HTTPError{StatusCode: 500, Body: body, Retryable: true},
			"the model provider failed: HTTP 500 Internal Server Error"},
		{"a status with no name, and a wait", &llm.HTTPError{StatusCode: 529, Body: body, RetryAfter: 90 * time.Second, Retryable: true},
			"the model provider failed: HTTP 529; it asked for a wait of 1m30s"},
	} {
		t.Run(c.name, func(t *testing.T) {
			client := &failAfterSpeech{err: c.err}
			loop := New(client, &fakeTools{results: map[string]string{"bash": "ok"}}, &fakeDefs{}, nil, nil,
				testBounds(Config{MaxIterations: limits.Of(4)}))
			res, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}})
			if err == nil {
				t.Fatal("a turn the provider refused must still report its error")
			}
			if res.Interrupted != c.cause {
				t.Fatalf("the identity is told %q, want %q", res.Interrupted, c.cause)
			}
			if strings.Contains(res.Interrupted, "acct-7f3a") || strings.Contains(res.Interrupted, "provider.example") {
				t.Fatalf("THE PROVIDER'S OWN WORDS REACHED THE IDENTITY: %q", res.Interrupted)
			}
		})
	}

	// The stop is asked first: a turn the operator stopped was stopped by
	// the operator, whatever the provider went on to answer.
	stopped, cancel := context.WithCancelCause(context.Background())
	cancel(StoppedByOperator)
	if got := interruptCause(stopped, &llm.HTTPError{StatusCode: 429}); got != "stopped by the operator" {
		t.Errorf("a stopped turn whose request was also refused is told %q", got)
	}
	// The answer is found however the caller wrapped it.
	if got := interruptCause(context.Background(), fmt.Errorf("LLM call: %w", &llm.HTTPError{StatusCode: 403})); got != "the model provider refused the request: HTTP 403 Forbidden" {
		t.Errorf("a wrapped refusal is told %q", got)
	}
	// And what is not a provider's answer is still the substrate's.
	if got := interruptCause(context.Background(), errors.New("connection reset by peer")); got != "the substrate failed mid-turn" {
		t.Errorf("a failure that is no provider's answer is told %q", got)
	}
}

func TestACancelledTurnIsToldWhatCancelledIt(t *testing.T) {
	for _, c := range []struct {
		name  string
		cause error // what the canceller named; nil for none
		want  string
	}{
		{"the operator's stop", StoppedByOperator, "stopped by the operator"},
		{"a shutdown", RuntimeShuttingDown, "the runtime was shutting down"},
		{"a restart", RuntimeRestarting, "the runtime was restarting"},
		{"a cancel that names nothing", nil, "the turn was cancelled; what cancelled it is not known"},
		{"a cause that is no stop of the turn's", errors.New("something else"), "the turn was cancelled; what cancelled it is not known"},
	} {
		t.Run(c.name, func(t *testing.T) {
			ctx, cancel := context.WithCancelCause(context.Background())
			defer cancel(nil)
			client := &captureLLM{script: []llm.Response{resp("looking", toolCall("1", "work", `{}`)), textResp("never asked for")}}
			loop := New(client, &stopper{stop: func() { cancel(c.cause) }}, &fakeDefs{}, &fakeTranscript{}, nil, testBounds(Config{MaxIterations: limits.Of(8)}))
			res, err := loop.Run(ctx, "system", []llm.Message{{Role: "user", Content: "go"}})
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("a cancelled turn ends cancelled, got %v", err)
			}
			if res.Spoken != "looking" {
				t.Fatalf("spoken = %q", res.Spoken)
			}
			if res.Interrupted != c.want {
				t.Fatalf("A TURN CUT BY %s IS TOLD %q; want %q", strings.ToUpper(c.name), res.Interrupted, c.want)
			}
			// The error says who, for a caller that holds only the error
			// (a timer's wake decides by it whether the attempt is done
			// with), and names nobody where nobody was named.
			var stop TurnStop
			if named := errors.As(c.cause, &stop); errors.As(err, &stop) != named || named && !errors.Is(err, c.cause) {
				t.Fatalf("the error of a turn cut by %s is %v", c.name, err)
			}
		})
	}
}

func TestANamedStopIsStillACancellation(t *testing.T) {
	for _, stop := range []TurnStop{StoppedByOperator, RuntimeShuttingDown, RuntimeRestarting} {
		wrapped := fmt.Errorf("LLM call: read response stream: %w", stop)
		if !errors.Is(wrapped, context.Canceled) {
			t.Fatalf("%q is not a cancellation to errors.Is", stop)
		}
		if errors.Is(wrapped, context.DeadlineExceeded) {
			t.Fatalf("%q answers to a deadline", stop)
		}
	}
	if errors.Is(StoppedByOperator, RuntimeShuttingDown) || !errors.Is(fmt.Errorf("x: %w", StoppedByOperator), StoppedByOperator) {
		t.Fatal("one stop answers to another's name, or not to its own")
	}
}

// silentAfterSpeech speaks and asks for a tool, then returns no response
// and no error.
type silentAfterSpeech struct{ calls int }

func (s *silentAfterSpeech) Chat(_ context.Context, _ []llm.Message, _ llm.ChatOptions) (*llm.Response, error) {
	s.calls++
	if s.calls == 1 {
		r := resp("I have read the file.", toolCall("c", "bash", `{}`))
		return &r, nil
	}
	return nil, nil
}

// emptyAfterSpeech speaks and asks for a tool, then answers with nothing:
// no text and no tool call, or (choiceless) a response with no choices at
// all.
type emptyAfterSpeech struct {
	calls      int
	choiceless bool
}

func (e *emptyAfterSpeech) Chat(_ context.Context, _ []llm.Message, _ llm.ChatOptions) (*llm.Response, error) {
	e.calls++
	r := textResp("")
	if e.choiceless {
		r = llm.Response{}
	}
	if e.calls == 1 {
		r = resp("I have read the file.", toolCall("c", "bash", `{}`))
	}
	return &r, nil
}

// A MODEL THAT RETURNED NOTHING IS NOT THE SUBSTRATE FAILING (h-turn-06;
// row 3.6). The request was sent and answered; the answer had no text and
// no tool call. It was told as "the substrate failed mid-turn", which is a
// different fact: nothing here failed.
func TestAnEmptyReplyIsToldAsTheModelsAndNotTheSubstrates(t *testing.T) {
	loop := New(&emptyAfterSpeech{}, &fakeTools{results: map[string]string{"bash": "ok"}}, &fakeDefs{}, nil, nil, testBounds(Config{MaxIterations: limits.Of(4)}))
	res, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}})
	if err == nil || err.Error() != "LLM response has no text or tool calls" {
		t.Fatalf("err = %v, want the empty reply's own error, its text unchanged", err)
	}
	if res.Spoken != "I have read the file." {
		t.Fatalf("spoken = %q", res.Spoken)
	}
	if res.Interrupted != "the model returned an empty reply" {
		t.Fatalf("AN EMPTY REPLY IS TOLD AS %q", res.Interrupted)
	}

	silent := New(&silentAfterSpeech{}, &fakeTools{results: map[string]string{"bash": "ok"}}, &fakeDefs{}, nil, nil, testBounds(Config{MaxIterations: limits.Of(4)}))
	if res, err := silent.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}}); err == nil || res.Interrupted != "the substrate failed mid-turn" {
		t.Fatalf("a call with no response is told as %q (%v)", res.Interrupted, err)
	}
	// A response with no choices at all is the same fact, at the site that
	// reads it: the model answered, with nothing.
	choiceless := New(&emptyAfterSpeech{choiceless: true}, &fakeTools{results: map[string]string{"bash": "ok"}}, &fakeDefs{}, nil, nil, testBounds(Config{MaxIterations: limits.Of(4)}))
	res, err = choiceless.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}})
	if err == nil || err.Error() != "LLM response has no choices" || res.Interrupted != "the model returned an empty reply" {
		t.Fatalf("A RESPONSE WITH NO CHOICES IS TOLD AS %q (%v)", res.Interrupted, err)
	}
	// A closing call answered with nothing, and a response with no choices
	// at all, are the same fact.
	for _, e := range []error{emptyReply("LLM final response at the round limit has no text"), emptyReply("LLM response has no choices")} {
		if got := interruptCause(context.Background(), e); got != "the model returned an empty reply" {
			t.Fatalf("%v is told as %q", e, got)
		}
	}
	// And where a closing call is read with no response at all, that is
	// not the model's either.
	var empty emptyReply
	if _, _, err := finalResponse(nil, "at the round limit"); err == nil || errors.As(err, &empty) {
		t.Fatalf("A CLOSING CALL WITH NO RESPONSE IS TYPED AS THE MODEL'S EMPTY REPLY: %v", err)
	}
}
