package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/conversation"
	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/jsonschema"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/tools"
	"github.com/aiii-dot-id/aii-os/internal/untrusted"
)

// heardReadScript asks for one recall with the given raw arguments, then
// answers; it keeps what the model was handed back.
type heardReadScript struct {
	args   string
	calls  int
	handed []llm.Message
}

func (s *heardReadScript) Chat(_ context.Context, msgs []llm.Message, _ llm.ChatOptions) (*llm.Response, error) {
	s.calls++
	if s.calls == 1 {
		var tc llm.ToolCall
		tc.ID, tc.Type, tc.Function.Name, tc.Function.Arguments = "heard-read", "function", "recall", s.args
		return &llm.Response{Choices: []llm.Choice{{Message: llm.Message{ToolCalls: []llm.ToolCall{tc}}}}}, nil
	}
	s.handed = append([]llm.Message(nil), msgs...)
	return &llm.Response{Choices: []llm.Choice{{Message: llm.Message{Content: "read"}}}}, nil
}

func TestAHeardReadNamedWithAFoldedDuplicateKeyKeepsItsBoundary(t *testing.T) {
	a := newVoiceApp(t)
	a.toolReg = tools.NewRegistry(t.TempDir(), nil, tools.Timeouts{})
	withHeardHistory(a)
	a.retainHeard("heard-session", 1, "withheld banking sentence", dashboard.VoiceEvent{}, "withheld", "pending", nil)
	model := &heardReadScript{args: `{"source":"heard","SOURCE":"memory","query":"banking"}`}
	loop := conversation.New(model, appToolExecutor{a}, appToolDefiner{a}, appTranscript{st: a.store}, nil, testLoop(conversation.Config{}))
	if _, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "what was said?"}}); err != nil {
		t.Fatal(err)
	}
	var handed string
	for _, m := range model.handed {
		if strings.Contains(m.Content, "withheld banking sentence") {
			handed = m.Content
		}
	}
	if handed == "" {
		t.Fatal("the heard read did not run: the probe cannot see the boundary")
	}
	if open := strings.Index(handed, untrusted.Open); open < 0 || open > strings.Index(handed, "withheld banking sentence") {
		t.Fatalf("heard words reached the model outside the untrusted boundary: %q", handed)
	}
	turns, err := a.store.RecentTurnsIncludingSystem(50)
	if err != nil {
		t.Fatal(err)
	}

	recorded := false
	for _, turn := range turns {
		recorded = recorded || strings.Contains(turn.Content, "withheld banking sentence")
	}
	if !recorded {
		t.Fatal("the record of a heard read does not hold the words the identity read")
	}
}

func TestTheOperatorReadsTheHeardSpeechTheIdentityCan(t *testing.T) {
	a := newVoiceApp(t)
	withHeardHistory(a)
	a.retainHeard("room", 1, "delivered words about the harbour", dashboard.VoiceEvent{}, "delivered", "", nil)
	a.retainHeard("room", 2, "withheld banking sentence", dashboard.VoiceEvent{}, "withheld", "speaker not on the list", nil)
	var h dashboard.WSHandler
	a.wireInteractionHooks(&h)
	if h.HeardSpeech == nil {
		t.Fatal("the dashboard is given no read of heard speech")
	}
	page, err := h.HeardSpeech(0)
	if err != nil {
		t.Fatal(err)
	}
	identitys, err := a.recallHeard("", 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	if page != identitys {
		t.Fatalf("the operator's page is not the identity's read:\n%s\n%s", page, identitys)
	}
	var got struct {
		Rows []heardHistoryRow `json:"rows"`
	}
	if err := json.Unmarshal([]byte(page), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Rows) != 2 || got.Rows[0].Text != "withheld banking sentence" || got.Rows[0].Delivery != "withheld" || got.Rows[0].Reason != "speaker not on the list" || got.Rows[1].Text != "delivered words about the harbour" {
		t.Fatalf("the operator's page: %+v", got.Rows)
	}
	if older, err := h.HeardSpeech(got.Rows[0].Seq); err != nil || strings.Contains(older, "banking") || !strings.Contains(older, "harbour") {
		t.Fatalf("the rows before the newest: %q, %v", older, err)
	}
	// The sentence the identity is told with every read says who else reads.
	if !strings.Contains(page, "reading delivers nothing to you as a turn. What you read here is recorded whole in your conversation, and your operator can read this history") {
		t.Fatalf("a read does not tell the identity that it is recorded and that its operator reads the same: %s", page)
	}
}

// Exercise the emitted provider contract, response parser and production host
// dispatcher together. The fake provider models a caller requiring every
// property; it is not evidence about a remote provider's normalization policy.
func TestHeardCallableProviderRoundTrip(t *testing.T) {
	a := newVoiceApp(t)
	a.toolReg = tools.NewRegistry(t.TempDir(), nil, tools.Timeouts{})
	withHeardHistory(a)
	a.cfg.Speech.Speakers = SpeakerPolicyConfig{Mode: "only", UIDs: []string{uuidA}}
	h, _ := newTrackedSession(a, "callable-history", true)
	page := &pageLog{}
	var wakes atomic.Int32
	stubWake(t, func() (string, error) { wakes.Add(1); return "reply", nil })
	a.voiceEngineEvent(nil, uuidFinal(h.id, 7, "track-a", "retained cobalt context"), page.enqueue)
	a.voiceEngineEvent(nil, pendingHeard(h.id, 20, 7, "track-a"), page.enqueue)
	const args = `{"after_seq":0,"after_edge":"","id":"","decay":"none","exact":false,"limit":20,"query":"","source":"heard"}`
	checked := make(chan error, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Tools []struct {
				Name       string         `json:"name"`
				Parameters map[string]any `json:"parameters"`
			} `json:"tools"`
		}
		err := json.NewDecoder(r.Body).Decode(&req)
		var parameters map[string]any
		for _, tool := range req.Tools {
			if tool.Name == "recall" {
				parameters = tool.Parameters
			}
		}
		if err == nil && parameters == nil {
			err = fmt.Errorf("recall missing from emitted wire")
		}
		if err == nil {
			var value any
			err = json.Unmarshal([]byte(args), &value)
			for _, allRequired := range []bool{false, true} {
				if err != nil {
					break
				}
				if allRequired {
					var required []any
					for key := range parameters["properties"].(map[string]any) {
						required = append(required, key)
					}
					parameters["required"] = required
					parameters["additionalProperties"] = false
				}
				var schema *jsonschema.Schema
				schema, err = jsonschema.Compile(parameters)
				if err == nil {
					err = schema.Validate(value)
				}
			}
		}
		checked <- err
		w.Header().Set("Content-Type", "text/event-stream")
		payload, _ := json.Marshal(map[string]any{"type": "response.completed", "response": map[string]any{
			"output": []any{map[string]any{"type": "function_call", "name": "recall", "call_id": "heard-call", "arguments": args}},
		}})
		fmt.Fprintf(w, "event: response.completed\ndata: %s\n\n", payload)
	}))
	defer srv.Close()
	c := llm.New(&llm.ClientConfig{Endpoint: srv.URL, APIKey: "fixture", Model: "fixture", Provider: "chatgpt"})
	response, err := c.Chat(context.Background(), []llm.Message{{Role: "user", Content: "Inspect heard context"}}, llm.ChatOptions{Tools: a.buildToolDefinitions()})
	if err != nil {
		t.Fatal(err)
	}
	if err = <-checked; err != nil {
		t.Fatal(err)
	}
	if len(response.Choices) != 1 || len(response.Choices[0].Message.ToolCalls) != 1 {
		t.Fatalf("call not parsed: %+v", response)
	}
	observation := a.executeToolCall(context.Background(), response.Choices[0].Message.ToolCalls[0])
	if observation.Failed || !strings.Contains(observation.Text, "retained cobalt context") || !strings.Contains(observation.Text, "Untrusted heard context") {
		t.Fatalf("consumer contract not executable: %+v", observation)
	}
	rows := historyRows(t, a, "")
	if len(rows) != 1 || rows[0].Delivery != "withheld" || rows[0].SpeakerUUID != "" || wakes.Load() != 0 || page.find("transcript_final", 7) != nil {
		t.Fatal("explicit inspection admitted or replayed withheld speech")
	}
	if _, present, err := a.store.TurnSeqByAnnotation(annotationVoice, voiceRefKey(h.id, 7)); err != nil || present {
		t.Fatalf("inspection created a turn: %v", err)
	}
	a.voiceInputFinished(h.id)
	awaitDrained(t, h)
}

func TestWhatTheIdentityReadOutlastsAnIgnoreAndIsInTheGrantedHistory(t *testing.T) {
	a := newVoiceApp(t)
	a.toolReg = tools.NewRegistry(t.TempDir(), nil, tools.Timeouts{})
	withHeardHistory(a)
	start, end := int64(0), int64(16000)
	ve := dashboard.VoiceEvent{TrackID: "track-a", StartSample: &start, EndSample: &end}
	a.retainHeard("room", 2, "withheld banking sentence", ve, "withheld", "pending", nil)
	model := &heardReadScript{args: `{"source":"heard","query":"banking"}`}
	loop := conversation.New(model, appToolExecutor{a}, appToolDefiner{a}, appTranscript{st: a.store}, nil, testLoop(conversation.Config{}))
	if _, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "what was said?"}}); err != nil {
		t.Fatal(err)
	}
	// The speaker turns out to be one the operator ignores.
	a.cfg.Speech.Speakers = SpeakerPolicyConfig{Mode: "ignore", UIDs: []string{uuidA}}
	a.amendHeard(decodeVoiceFrame(uuidObservation("room", 20, 2, "track-a", uuidA)))
	a.cfg.Speech.Speakers = SpeakerPolicyConfig{Mode: "all"}
	a.heardHistory.mu.Lock()
	held := len(a.heardHistory.rows)
	a.heardHistory.mu.Unlock()
	if held != 0 {
		t.Fatalf("the buffer kept the row of a speaker found to be ignored: %d rows", held)
	}
	turns, err := a.store.RecentTurnsIncludingSystem(50)
	if err != nil {
		t.Fatal(err)
	}
	recorded := false
	for _, turn := range turns {
		recorded = recorded || strings.Contains(turn.Content, "withheld banking sentence")
	}
	if !recorded {
		t.Fatal("the Ignore reached back into the record: the read's words are gone from the conversation")
	}
	page, err := a.QueryInteractions(context.Background(), interaction.Query{Version: 1, Source: "recorded"})
	if err != nil {
		t.Fatal(err)
	}
	granted := false
	for _, r := range page.Rows {
		granted = granted || strings.Contains(r.Content, "withheld banking sentence")
	}
	if !granted {
		t.Fatalf("the interactions reader does not hold the recorded heard read: %d rows", len(page.Rows))
	}
	// SAFE refuses the buffer to both readers.
	a.retainHeard("room", 3, "words heard later", ve, "delivered", "", nil)
	a.mode.mu.Lock()
	a.mode.mode, a.mode.reason = ModeSafe, "test"
	a.mode.mu.Unlock()
	defer a.resetModeForTest()
	var h dashboard.WSHandler
	a.wireInteractionHooks(&h)
	if _, err := h.HeardSpeech(0); err == nil || !strings.Contains(err.Error(), "heard history withheld under SAFE: test") {
		t.Fatalf("the page read the buffer under SAFE: %v", err)
	}
	if _, err := a.recallHeard("", 0, 0); err == nil || !strings.Contains(err.Error(), "heard history withheld under SAFE: test") {
		t.Fatalf("the identity read the buffer under SAFE: %v", err)
	}
}
