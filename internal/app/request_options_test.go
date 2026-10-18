package app

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/coder/websocket"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/conversation"
	"github.com/aiii-dot-id/aii-os/internal/llm"
)

func scopedRequestContext(t *testing.T, names []string) context.Context {
	t.Helper()
	ctx, err := conversation.WithRequestOptions(t.Context(), conversation.RequestOptions{AllowedTools: &names})
	if err != nil {
		t.Fatal(err)
	}
	return ctx
}

func TestConstrainedChatNeverSteersOrQueuesItsOptions(t *testing.T) {
	a, _ := dispatchApp(t)
	defer stopApp(t, a)
	ctx := scopedRequestContext(t, []string{})
	if err := a.admitConstrainedChat(ctx, "first"); err != nil {
		t.Fatal(err)
	}
	if err := a.admitConstrainedChat(ctx, "second"); err == nil {
		t.Fatal("busy request admitted")
	}
	if pending := a.DrainSteering(); len(pending) != 0 {
		t.Fatalf("contract entered a different turn: %v", pending)
	}
	a.EndTurn()
	if err := a.admitConstrainedChat(ctx, "third"); err != nil {
		t.Fatal(err)
	}
	a.EndTurn()
}

func TestConstrainedChatRejectsUnknownToolsWithoutClaim(t *testing.T) {
	a, _ := dispatchApp(t)
	defer stopApp(t, a)
	if err := a.admitConstrainedChat(scopedRequestContext(t, []string{"not-a-real-tool"}), "q"); err == nil {
		t.Fatal("unknown tool admitted")
	}
	if a.TurnActive() {
		t.Fatal("invalid request leaked a turn claim")
	}
	if err := a.admitConstrainedChat(t.Context(), "q"); err == nil {
		t.Fatal("missing options admitted")
	}
}

func TestPromptReservePricesTheToolsActuallyOffered(t *testing.T) {
	a, _ := promptCacheApp(t)
	msg := llm.Message{Role: "user", Content: "read a source file"}
	all, err := a.promptReserve(msg, 0)
	if err != nil {
		t.Fatal(err)
	}
	none, err := a.promptReserveFor(scopedRequestContext(t, []string{}), msg, 0)
	if err != nil {
		t.Fatal(err)
	}
	if none >= all {
		t.Fatalf("narrowing not reflected in reserve: empty %d full %d", none, all)
	}
}

func TestScopedRequestDoesNotChangeTheNextOrdinaryTurn(t *testing.T) {
	a, obs := promptCacheApp(t)
	ctx := scopedRequestContext(t, []string{})
	if err := a.admitConstrainedChat(ctx, "bounded"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.observeChat(ctx, "bounded", func(string, string, string) {}); err != nil {
		t.Fatal(err)
	}
	operatorTurn(t, a, "ordinary")
	bodies := obs.snapshot()
	if len(bodies) != 2 {
		t.Fatalf("calls=%d", len(bodies))
	}
	for i, body := range bodies {
		var req map[string]json.RawMessage
		if err := json.Unmarshal([]byte(body), &req); err != nil {
			t.Fatal(err)
		}
		if i == 0 && string(req["tools"]) != "" && string(req["tools"]) != "[]" {
			t.Fatalf("scoped tools leaked: %s", req["tools"])
		}
		if i == 1 && (len(req["tools"]) == 0 || string(req["tools"]) == "[]") {
			t.Fatal("scope persisted beyond request")
		}
		if strings.Contains(body, `"response_format"`) {
			t.Fatal("format introduced without request")
		}
	}
}

func TestRequestContractCrossesDashboardAppProviderAndReadTool(t *testing.T) {
	a, root := dispatchApp(t)
	defer stopApp(t, a)
	file := filepath.Join(root, "answer.txt")
	if err := writeFileForTest(file, "answer is 42\n"); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Tools    []llm.ToolDefinition `json:"tools"`
			Format   *llm.ResponseFormat  `json:"response_format"`
			Messages []llm.Message        `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		if len(request.Tools) != 1 || request.Tools[0].Function.Name != "read" || request.Format != nil {
			t.Error("contract failed to cross app/provider boundary")
		}
		w.Header().Set("Content-Type", "application/json")
		if calls.Add(1) == 1 {
			args, _ := json.Marshal(map[string]any{"file_path": file})
			io.WriteString(w, callReply("I will read the supplied source.", toolCall("read-1", "read", string(args))))
		} else {
			if !strings.Contains(request.Messages[len(request.Messages)-1].Content, "42") {
				t.Error("read result did not reach model")
			}
			io.WriteString(w, textReply(`{"answer":42}`))
		}
	}))
	defer provider.Close()
	a.llmSwap.Swap(llm.New(&llm.ClientConfig{Endpoint: provider.URL, Model: "fake", NoStream: true, Retries: -1}))
	var options conversation.RequestOptions
	if err := json.Unmarshal([]byte(`{"allowed_tools":["read"],"response_format":{"type":"json_schema","json_schema":{"name":"answer","strict":true,"schema":{"type":"object","properties":{"answer":{"type":"integer"}},"required":["answer"],"additionalProperties":false}}}}`), &options); err != nil {
		t.Fatal(err)
	}
	h := &dashboard.WSHandler{Speaker: "identity"}
	a.wireTurnHooks(h)
	server := dashboard.New("127.0.0.1", 0, h)
	if _, err := server.Start(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	defer server.Shutdown(context.Background())
	conn := dialWSS(t, server.Origin())
	defer conn.Close(websocket.StatusNormalClosure, "")
	request, _ := json.Marshal(dashboard.ClientMessage{Type: "chat", RequestID: "contract-fits", Message: "read the source", RequestOptions: &options})
	if err := conn.Write(t.Context(), websocket.MessageText, request); err != nil {
		t.Fatal(err)
	}
	got := readUntil(t, conn, func(m *dashboard.ServerMessage) bool {
		return m.Type == "error" || m.Type == "response" && m.Done && m.Role == "identity"
	})
	if got.Type == "error" {
		t.Fatalf("contract failed: %s", got.Message)
	}
	if got.RequestID != "contract-fits" || string(got.StructuredOutput) != `{"answer":42}` || !strings.Contains(got.Message, "I will read the supplied source.") {
		t.Fatalf("lost output or speech: %+v", got)
	}
	if calls.Load() != 2 {
		t.Fatalf("provider calls=%d", calls.Load())
	}
}

// An answer that does not fit reaches the sender as the identity's words
// and "not completed": nothing is published as the structured result, and no
// call is made to repair it.
func TestAnAnswerThatDoesNotFitReachesTheSenderAsWordsAndNotCompleted(t *testing.T) {
	a, _ := dispatchApp(t)
	defer stopApp(t, a)
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, textReply("The answer is forty-two."))
	}))
	defer provider.Close()
	a.llmSwap.Swap(llm.New(&llm.ClientConfig{Endpoint: provider.URL, Model: "fake", NoStream: true, Retries: -1}))
	var options conversation.RequestOptions
	if err := json.Unmarshal([]byte(`{"allowed_tools":["read"],"response_format":{"type":"json_schema","json_schema":{"name":"answer","strict":true,"schema":{"type":"object","properties":{"answer":{"type":"integer"}},"required":["answer"],"additionalProperties":false}}}}`), &options); err != nil {
		t.Fatal(err)
	}
	h := &dashboard.WSHandler{Speaker: "identity"}
	a.wireTurnHooks(h)
	server := dashboard.New("127.0.0.1", 0, h)
	if _, err := server.Start(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	defer server.Shutdown(context.Background())
	conn := dialWSS(t, server.Origin())
	defer conn.Close(websocket.StatusNormalClosure, "")
	request, _ := json.Marshal(dashboard.ClientMessage{Type: "chat", RequestID: "does-not-fit", Message: "what is the answer", RequestOptions: &options})
	if err := conn.Write(t.Context(), websocket.MessageText, request); err != nil {
		t.Fatal(err)
	}
	words := readUntil(t, conn, func(m *dashboard.ServerMessage) bool {
		return m.Type == "error" || m.Type == "response" && m.Done && m.Role == "identity"
	})
	if words.Type != "response" || words.RequestID != "does-not-fit" || words.RequestErrorCode != "RESPONSE_FORMAT_VIOLATION" || !strings.Contains(words.Message, "The answer is forty-two.") || len(words.StructuredOutput) != 0 {
		t.Fatalf("the identity's words did not reach the sender as words, not completed: %+v", words)
	}
	failed := readUntil(t, conn, func(m *dashboard.ServerMessage) bool { return m.Type == "error" })
	if failed.RequestID != "does-not-fit" || failed.RequestErrorCode != "RESPONSE_FORMAT_VIOLATION" {
		t.Fatalf("the request was not reported as not completed, with its typed reason on the frame: %+v", failed)
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("%d provider calls: a call was made to repair the answer", n)
	}
	// The record: the answer is the identity's, kept as any reply is.
	var kept int
	if err := a.store.DB().QueryRow(`SELECT COUNT(*) FROM conversations WHERE role = 'resident' AND kind = 'message' AND content LIKE '%The answer is forty-two.%'`).Scan(&kept); err != nil || kept != 1 {
		t.Fatalf("the identity's answer is not in its record once: %d, %v", kept, err)
	}
}

// The whole request: a constrained turn's request to the provider carries
// the limits lines, written from the offer it sends, and the next ordinary
// turn's request carries none.
func TestAConstrainedTurnsRequestCarriesItsLimitsAndTheNextDoesNot(t *testing.T) {
	a, obs := promptCacheApp(t)
	ctx := scopedRequestContext(t, []string{})
	if err := a.admitConstrainedChat(ctx, "bounded"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.observeChat(ctx, "bounded", func(string, string, string) {}); err != nil {
		t.Fatal(err)
	}
	operatorTurn(t, a, "ordinary")
	bodies := obs.snapshot()
	if len(bodies) != 2 {
		t.Fatalf("calls=%d", len(bodies))
	}
	if want := `### Limits on this request (this turn only)\nTools offered: none.`; !strings.Contains(bodies[0], want) {
		t.Fatalf("the constrained turn's request does not say its limits:\n%s", bodies[0])
	}
	if strings.Contains(bodies[1], "Limits on this request") {
		t.Fatalf("the next ordinary turn's request still speaks of limits:\n%s", bodies[1])
	}
}

// With a schema: the provider is sent the Answer line in the turn's block
// and is never sent the schema as a request member.
func TestAHeldTurnsRequestShowsItsSchemaAndSendsNoGrammar(t *testing.T) {
	a, obs := promptCacheApp(t)
	format := &llm.ResponseFormat{Type: "json_schema", JSONSchema: &llm.JSONSchemaFormat{Name: "source_result", Strict: true, Schema: map[string]any{
		"type": "object", "properties": map[string]any{"answer": map[string]any{"type": "string"}}, "required": []any{"answer"}, "additionalProperties": false}}}
	ctx, err := conversation.WithRequestOptions(t.Context(), conversation.RequestOptions{ResponseFormat: format})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.admitConstrainedChat(ctx, "held"); err != nil {
		t.Fatal(err)
	}
	// The fixture's provider answers in words, so the request is reported
	// as not completed; what it was sent is what is under test.
	_, err = a.observeChat(ctx, "held", func(string, string, string) {})
	var verdict *llm.ResponseFormatError
	if !errors.As(err, &verdict) || verdict.Code != "RESPONSE_FORMAT_VIOLATION" {
		t.Fatalf("an answer in words was not reported by the app with the loop's typed verdict: %v", err)
	}
	bodies := obs.snapshot()
	if len(bodies) != 1 {
		t.Fatalf("%d calls: one answer, and no call to repair it", len(bodies))
	}
	if want := `### Limits on this request (this turn only)\nAnswer: one JSON value fitting schema \"source_result\": {\"additionalProperties\":false`; !strings.Contains(bodies[0], want) {
		t.Fatalf("the held turn's request does not show its schema:\n%s", bodies[0])
	}
	if strings.Contains(bodies[0], `"response_format"`) {
		t.Fatalf("the schema was sent to the provider as a request member:\n%s", bodies[0])
	}
}
