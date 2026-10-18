package llm

import (
	"strings"
	"testing"
)

// The terminal event (response.completed / response.incomplete) is what
// says the model finished, what it cost, and whether it was cut short. A
// stream that ends without one is truncated — and a truncated stream is
// indistinguishable from a backend that never sends the event, which is
// why neither may pass as complete.

const streamOneItem = `data: {"type":"response.output_item.done","item":{"type":"message","content":[{"type":"output_text","text":"I will start by reading the ledger"}]}}
`

const streamTerminal = `data: {"type":"response.completed","response":{"usage":{"input_tokens":100,"output_tokens":20,"total_tokens":120}}}

data: [DONE]
`

// The exact reported failure: one item collected, then the connection
// drops. It used to return actionable text with unknown final status.
func TestATruncatedStreamIsNotAResponse(t *testing.T) {
	_, err := readResponsesStream(strings.NewReader(streamOneItem))
	if err == nil {
		t.Fatal("a stream that ended after one item with no terminal event was accepted as complete — " +
			"the identity would act on text it does not know is all of it")
	}
	if !strings.Contains(err.Error(), "truncated") {
		t.Fatalf("the failure does not say what happened: %v", err)
	}
}

func TestAnEmptyStreamIsStillRefused(t *testing.T) {
	if _, err := readResponsesStream(strings.NewReader("")); err == nil {
		t.Fatal("an empty stream was accepted")
	}
}

// The whole stream still works, and usage still arrives — the terminal
// event is where cost comes from, so requiring it also stops a truncated
// turn from reporting a spend of zero.
func TestACompleteStreamIsAccepted(t *testing.T) {
	resp, err := readResponsesStream(strings.NewReader(streamOneItem + "\n" + streamTerminal))
	if err != nil {
		t.Fatalf("a complete stream was refused: %v", err)
	}
	if len(resp.Choices) == 0 || !strings.Contains(resp.Choices[0].Message.Content, "reading the ledger") {
		t.Fatalf("the content did not survive: %+v", resp.Choices)
	}
	if !resp.Usage.Reported || resp.Usage.TotalTokens != 120 {
		t.Fatalf("usage did not arrive: %+v", resp.Usage)
	}
}

// A model that was cut short by the PROVIDER says so through the
// terminal event — that is a complete stream reporting an incomplete
// response, and must not be confused with a broken connection.
func TestAnIncompleteResponseIsNotATruncatedStream(t *testing.T) {
	const incomplete = `data: {"type":"response.incomplete","response":{"incomplete_details":{"reason":"max_output_tokens"},"usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15}}}
`
	resp, err := readResponsesStream(strings.NewReader(streamOneItem + "\n" + incomplete))
	if err != nil {
		t.Fatalf("a provider-reported incomplete response was treated as a broken stream: %v", err)
	}
	if resp == nil {
		t.Fatal("no response")
	}
}
