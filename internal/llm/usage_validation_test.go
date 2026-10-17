package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/llm/wire"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

type pass4Transport func(*http.Request) (*http.Response, error)

func (f pass4Transport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// Counts that contradict each other are no measurement, and the reply they
// came with is still the reply. This test accepted either a refused call
// or usage not reported; the refusal threw a finished reply away, so it
// holds one outcome now: the reply, no count, and what was sent kept as
// words.
func TestCacheInconsistentUsageIsNotTrusted(t *testing.T) {
	for _, tc := range []struct{ name, provider, usage string }{
		{"negative OpenAI cache reads", "openai", `{"prompt_tokens":1000,"completion_tokens":10,"total_tokens":1010,"prompt_tokens_details":{"cached_tokens":-500}}`},
		{"OpenAI cache reads exceed input", "openai", `{"prompt_tokens":1000,"completion_tokens":10,"total_tokens":1010,"prompt_tokens_details":{"cached_tokens":2000}}`},
		{"OpenAI total contradicts components", "openai", `{"prompt_tokens":1000,"completion_tokens":10,"total_tokens":0,"prompt_tokens_details":{"cached_tokens":900}}`},
		{"negative Anthropic cache reads", "anthropic", `{"input_tokens":100,"output_tokens":10,"cache_read_input_tokens":-200}`},
		{"negative Anthropic cache writes", "anthropic", `{"input_tokens":100,"output_tokens":10,"cache_creation_input_tokens":-200}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := New(&ClientConfig{Endpoint: "https://review.invalid", Provider: tc.provider, Model: "fixture", Retries: -1})
			c.httpClient = &http.Client{Transport: pass4Transport(func(r *http.Request) (*http.Response, error) {
				body := `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":` + tc.usage + `}`
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			})}
			response, err := c.Chat(t.Context(), []Message{{Role: "user", Content: "hello"}}, ChatOptions{})
			if err != nil {
				t.Fatalf("a finished reply was refused for its usage: %v", err)
			}
			if len(response.Choices) != 1 || response.Choices[0].Message.Content != "ok" {
				t.Fatalf("the reply was not kept: %+v", response.Choices)
			}
			if u := response.Usage; u.Refused == "" || u != (Usage{Problem: wire.UsageInvalid, Refused: u.Refused}) {
				t.Fatalf("inconsistent provider usage kept a count, or lost what was sent: %+v", u)
			}
		})
	}
}

func TestAnInconsistentCountIsWarnedOncePerModel(t *testing.T) {
	inconsistentUsageSaid.Clear()
	logs := logsink.CaptureForTest(t)
	const bad = `,"usage":{"prompt_tokens":100,"completion_tokens":20,"total_tokens":420}}`
	ask := func(provider, model string, entry ...string) {
		t.Helper()
		cc := &ClientConfig{Endpoint: "https://review.invalid", Provider: provider, Model: model, Retries: -1}
		if len(entry) > 0 {
			cc.Entry, cc.Endpoint = entry[0], "https://"+entry[0]+".invalid"
		}
		c := New(cc)
		c.httpClient = &http.Client{Transport: pass4Transport(func(r *http.Request) (*http.Response, error) {
			body := `{"id":"r","model":"` + model + `","choices":[{"index":0,"message":{"role":"assistant","content":"kept"},"finish_reason":"stop"}]` + bad
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
		})}
		resp, err := c.Chat(t.Context(), []Message{{Role: "user", Content: "hello"}}, ChatOptions{})
		if err != nil || len(resp.Choices) != 1 || resp.Choices[0].Message.Content != "kept" || resp.Usage.Reported {
			t.Fatalf("%s %s: the reply was not kept with its usage not reported: %+v, %v", provider, model, resp, err)
		}
	}
	for range 3 {
		ask("openai", "model-a")
	}
	ask("openai", "model-b")
	ask("openai", "model-b")
	said := func(label, model string) int {
		return strings.Count(logs.String(), label+" "+model+" sent token counts that are not consistent")
	}
	if a, b := said("openai-compatible", "model-a"), said("openai-compatible", "model-b"); a != 1 || b != 1 {
		t.Fatalf("model-a said %d time(s) over three replies and model-b %d over two, want once each\n%s", a, b, logs.String())
	}

	ask("openai", "model-a", "second-entry")
	ask("openai", "model-a", "second-entry")
	if a := said("openai-compatible", "model-a"); a != 2 {
		t.Fatalf("model-a on a second provider entry: said %d time(s) in all, want 2 (once per entry)\n%s", a, logs.String())
	}
	if !logs.Contains("Said once for this provider and model while the host runs") {
		t.Fatalf("the line does not say it is said once\n%s", logs.String())
	}
}

// A REFUSED COUNT IS NOT THE FLOOR OF ITS REPLY'S ESTIMATE. The Anthropic
// dialect put the block's output_tokens on the message whatever became of
// the usage, and that count is the least the message is priced at in every
// later request. A reply kept although its counts were refused then carried
// one of them on: 900,000 here, which fills any window on the next round
// and is charged to the turn's fence as measured. The Responses dialect
// takes the count only from usage it reported; this one does now. Counts
// that add up still set the floor.
func TestARefusedCountIsNotTheFloorOfItsReplysEstimate(t *testing.T) {
	for name, tc := range map[string]struct {
		usage string
		floor int
	}{
		"refused": {`{"input_tokens":100,"cache_read_input_tokens":-200,"output_tokens":900000}`, 0},
		"kept":    {`{"input_tokens":100,"output_tokens":900000}`, 900_000},
	} {
		c := New(&ClientConfig{Endpoint: "https://review.invalid", Provider: "anthropic", Model: "fixture", Retries: -1})
		c.httpClient = &http.Client{Transport: pass4Transport(func(r *http.Request) (*http.Response, error) {
			body := `{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":` + tc.usage + `}`
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
		})}
		resp, err := c.Chat(t.Context(), []Message{{Role: "user", Content: "hello"}}, ChatOptions{})
		if err != nil || len(resp.Choices) != 1 || resp.Choices[0].Message.Content != "ok" {
			t.Fatalf("%s: the reply was not kept: %+v, %v", name, resp, err)
		}
		msg := resp.Choices[0].Message
		if resp.Usage.Reported != (tc.floor > 0) || msg.OutputTokens != tc.floor {
			t.Errorf("%s: usage reported=%t and the message carries an output count of %d, want %d", name, resp.Usage.Reported, msg.OutputTokens, tc.floor)
		}
		priced, err := EstimateMessageTokens(msg)
		if err != nil {
			t.Fatal(err)
		}
		if refused := tc.floor == 0; refused && priced >= 900_000 || !refused && priced != tc.floor {
			t.Errorf("%s: the next request prices the reply at %d tokens (the count sent was 900000; a refused one prices nothing)", name, priced)
		}
	}
}

// A FINISHED REPLY IS KEPT WHEN ITS TOKEN COUNTS DO NOT ADD UP. A provider
// that counts reasoning in its total and not in its completion sends such
// counts with every reasoning reply; the client refused each one, after
// the failed-call line had already been passed, so the identity could not
// finish a turn there and the log showed no failure. The reply is returned
// now, its usage is usage that was not reported (no count survives), and
// what the provider sent is said once for the call, with the provider and
// the model. A reply with no usage and one whose counts add up are as they
// were, and say nothing.
func TestAReplyWhoseCountsDoNotAddUpIsKeptAndItsUsageIsNotReported(t *testing.T) {
	const reply = `{"id":"r","model":"fake","choices":[{"index":0,"message":{"role":"assistant","content":"the answer"},"finish_reason":"stop"}]`
	const sent = "input=100 output=20 total=420 cache_read=0 cache_write=0 cache_write_5m=0 cache_write_1h=0"
	for name, tc := range map[string]struct {
		usage string
		want  Usage
		said  int
	}{
		"a total that counts reasoning the completion does not": {`,"usage":{"prompt_tokens":100,"completion_tokens":20,"total_tokens":420,"completion_tokens_details":{"reasoning_tokens":300}}`,
			Usage{Problem: wire.UsageInvalid, Refused: sent}, 1},
		"no usage at all": {``, Usage{}, 0},
		"counts that add up": {`,"usage":{"prompt_tokens":100,"completion_tokens":20,"total_tokens":120}`,
			Usage{PromptTokens: 100, CompletionTokens: 20, TotalTokens: 120, Reported: true}, 0},
	} {
		logs := logsink.CaptureForTest(t)
		inconsistentUsageSaid.Clear() // said once per process: each case is a fresh one
		resp, err := replying(t, reply+tc.usage+`}`).Chat(context.Background(), []Message{{Role: "user", Content: "hello"}}, ChatOptions{})
		if err != nil {
			t.Errorf("%s: a finished reply was refused: %v", name, err)
			continue
		}
		if len(resp.Choices) != 1 || resp.Choices[0].Message.Content != "the answer" {
			t.Errorf("%s: the reply was not kept: %+v", name, resp.Choices)
		}
		if resp.Usage != tc.want {
			t.Errorf("%s: usage %+v, want %+v", name, resp.Usage, tc.want)
		}
		if got := strings.Count(logs.String(), "sent token counts that are not consistent"); got != tc.said {
			t.Errorf("%s: the counts were said %d time(s), want %d\n%s", name, got, tc.said, logs.String())
		}
		if tc.said > 0 && !(logs.Contains("llm.budget") && logs.Contains("openai-compatible fake sent token counts that are not consistent ("+sent+")")) {
			t.Errorf("%s: the line does not name the provider, the model and what was sent\n%s", name, logs.String())
		}
		if logs.Contains("call FAILED") {
			t.Errorf("%s: a kept reply was logged as a failed call\n%s", name, logs.String())
		}
	}

	// The two dialects that build their own usage keep what was sent too:
	// Anthropic's sum in the vendor's names, the Responses shape through
	// Validate.
	if got, want := anthUsageOf(&anthUsage{InputTokens: 100, OutputTokens: 10, CacheReadInputTokens: -200}),
		(Usage{Problem: wire.UsageInvalid, Refused: "input_tokens=100 cache_read_input_tokens=-200 cache_creation_input_tokens=0 output_tokens=10"}); got != want {
		t.Errorf("Anthropic usage with a negative count is %+v, want %+v", got, want)
	}
	var responses respUsage
	if err := json.Unmarshal([]byte(`{"input_tokens":100,"output_tokens":20,"total_tokens":420}`), &responses); err != nil {
		t.Fatal(err)
	}
	if got, want := responses.normalized(), (Usage{Problem: wire.UsageInvalid, Refused: sent}); got != want {
		t.Errorf("Responses usage whose total is not input plus output is %+v, want %+v", got, want)
	}
}
func TestCacheValidZeroAndCacheTotalsSurvive(t *testing.T) {
	var zero Usage
	if err := json.Unmarshal([]byte(`{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0,"prompt_tokens_details":{"cached_tokens":0}}`), &zero); err != nil {
		t.Fatal(err)
	}
	if !zero.Reported || zero.TotalTokens != 0 {
		t.Fatalf("explicit zero lost: %+v", zero)
	}
	anth := anthUsageOf(&anthUsage{InputTokens: 100, CacheReadInputTokens: 900, CacheCreationInputTokens: 100, OutputTokens: 10})
	if anth.PromptTokens != 1100 || anth.CachedPromptTokens != 900 || anth.TotalTokens != 1110 {
		t.Fatalf("valid Anthropic cache accounting changed: %+v", anth)
	}
	t.Log("explicit zero is reported; valid Anthropic read/write counts still occupy the input window")
}
