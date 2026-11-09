package llm

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// THE REPLY'S TEXT IS TOLD AS IT IS READ. Each streaming
// decoder tells ChatOptions.OnText the selected answer's content, in
// order, and nothing else: not a thought, not a reasoning summary, not a
// second choice or candidate, not a tool call's arguments. A call read
// whole tells it nothing; its reply arrives whole at the end.

func told() (func(string), *[]string) {
	var got []string
	return func(s string) { got = append(got, s) }, &got
}

func TestEachStreamingDecoderTellsOnlyTheAnswersText(t *testing.T) {
	const want = "Hello"
	t.Run("openai-compatible", func(t *testing.T) {
		say, got := told()
		resp, err := readChatStream(strings.NewReader(`data: {"id":"c1","choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":"weigh it"}}]}

data: {"id":"c1","choices":[{"index":0,"delta":{"content":"Hel"}},{"index":1,"delta":{"content":"another choice"}}]}

data: {"id":"c1","choices":[{"index":0,"delta":{"reasoning":"an account"}}]}

data: {"id":"c1","choices":[{"index":0,"delta":{"content":"lo"}}]}

data: {"id":"c1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"t1","type":"function","function":{"name":"read","arguments":"{\"path\":\"notes\"}"}}]}}]}

data: {"id":"c1","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}

data: [DONE]
`), say)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Join(*got, "|") != "Hel|lo" || resp.Choices[0].Message.Content != want {
			t.Fatalf("told %q for the reply %q", *got, resp.Choices[0].Message.Content)
		}
	})
	t.Run("anthropic", func(t *testing.T) {
		say, got := told()
		a, err := readAnthropicStream(strings.NewReader(`event: message_start
data: {"type":"message_start","message":{"id":"m","content":[],"usage":{"input_tokens":1,"output_tokens":0}}}

data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}

data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"weigh it"}}

data: {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig"}}

data: {"type":"content_block_stop","index":0}

data: {"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}

data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"Hel"}}

data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"lo"}}

data: {"type":"content_block_stop","index":1}

data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":4}}

data: {"type":"message_stop"}

`), say)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Join(*got, "|") != "Hel|lo" || a.StopReason != "end_turn" {
			t.Fatalf("told %q, stop %q", *got, a.StopReason)
		}
	})
	t.Run("responses", func(t *testing.T) {
		say, got := told()
		resp, err := readResponsesStream(strings.NewReader(`data: {"type":"response.reasoning_summary_text.delta","delta":"weigh it"}
data: {"type":"response.output_text.delta","delta":"Hel"}
data: {"type":"response.function_call_arguments.delta","delta":"{\"path\""}
data: {"type":"response.output_text.delta","delta":"lo"}
data: {"type":"response.output_item.done","item":{"type":"message","content":[{"type":"output_text","text":"Hello"}]}}
`+streamTerminal), say)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Join(*got, "|") != "Hel|lo" || resp.Choices[0].Message.Content != want {
			t.Fatalf("told %q for the reply %q", *got, resp.Choices[0].Message.Content)
		}
	})
	t.Run("gemini", func(t *testing.T) {
		say, got := told()
		resp, err := readGeminiStream(strings.NewReader(`data: {"candidates":[{"index":0,"content":{"role":"model","parts":[{"text":"weigh it","thought":true}]}}]}

data: {"candidates":[{"index":0,"content":{"role":"model","parts":[{"text":"Hel","thoughtSignature":"c2ln"}]}}]}

data: {"candidates":[{"index":1,"content":{"role":"model","parts":[{"text":"another candidate"}]}}]}

data: {"candidates":[{"index":0,"content":{"role":"model","parts":[{"text":"lo"}]},"finishReason":"STOP"}]}

`), say)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Join(*got, "|") != "Hel|lo" || resp.Choices[0].Message.Content != want {
			t.Fatalf("told %q for the reply %q", *got, resp.Choices[0].Message.Content)
		}
	})
}

// A provider that fails after some of the reply has been told: the failure
// says the text was generated (so it is not asked for again), and what was
// told is what the stream carried.
func TestAFailureAfterTextSaysItWasGeneratedAndWhatWasTold(t *testing.T) {
	say, got := told()
	_, err := readChatStream(strings.NewReader(`data: {"id":"c1","choices":[{"index":0,"delta":{"content":"Half a "}}]}

data: {"error":{"message":"the upstream closed"}}

`), say)
	var failure *StreamFailure
	if !errors.As(err, &failure) || !failure.Generated {
		t.Fatalf("a failure after text was %v", err)
	}
	if strings.Join(*got, "|") != "Half a " {
		t.Fatalf("told %q", *got)
	}
}

// Through Chat, in every dialect: a streamed reply is told whole, piece by
// piece, and a reply read whole is told nothing.
func TestChatTellsAStreamedReplyAndNotAWholeOne(t *testing.T) {
	for _, tc := range []struct {
		name, provider, body string
		streamed             bool
	}{
		{"openai-compatible, streamed", "openai", shaped(openAIChunk, "m"), true},
		{"openai-compatible, whole", "openai", shaped(openAIWhole, "m"), false},
		{"anthropic, streamed", "anthropic", shaped(anthropicSSE, "m"), true},
		{"anthropic, whole", "anthropic", shaped(anthropicWhole, "m"), false},
		{"gemini, streamed", "gemini", "data: " + shaped(geminiWhole, "m") + "\r\n\r\n", true},
		{"gemini, whole", "gemini", shaped(geminiWhole, "m"), false},
		{"responses, streamed", "chatgpt", "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"output_index\":0,\"content_index\":0,\"delta\":\"ok\"}\n\n" + shaped(responsesSSE, "m"), true},
		// A stream of finished items alone tells nothing: its reply arrives
		// whole at the end, as a call read whole does.
		{"responses, finished items only", "chatgpt", shaped(responsesSSE, "m"), false},
	} {
		say, got := told()
		resp, err := answering(t, tc.provider, "m", tc.body).Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, ChatOptions{OnText: say})
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		reply := resp.Choices[0].Message.Content
		if tc.streamed && strings.Join(*got, "") != reply {
			t.Errorf("%s: told %q for the reply %q", tc.name, *got, reply)
		}
		if !tc.streamed && len(*got) != 0 {
			t.Errorf("%s: a reply read whole was told: %q", tc.name, *got)
		}
	}
}
