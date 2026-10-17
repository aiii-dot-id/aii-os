package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/conversation"
	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/coder/websocket"
)

// This live test is opt-in. The runner owns the idle-host check and a guarded
// loopback provider proxy. It passes prompts/source bytes, never grader keys.
// A fresh ordinary app fixture keeps experimental history away from residents.
type requestLiveCase struct {
	ID          string                       `json:"id"`
	Prompt      string                       `json:"prompt"`
	Sources     map[string]string            `json:"sources"`
	Options     *conversation.RequestOptions `json:"request_options"`
	NoStream    bool                         `json:"no_stream"`
	ThinkingOff bool                         `json:"thinking_off"`
}

type requestLiveExecutor struct {
	app    *App
	root   string
	files  map[string]bool
	mu     sync.Mutex
	events []map[string]any
}

func (x *requestLiveExecutor) Execute(ctx context.Context, call llm.ToolCall) conversation.Observation {
	var args map[string]any
	var obs conversation.Observation
	err := json.Unmarshal([]byte(call.Function.Arguments), &args)
	pathKey := "file_path"
	if call.Function.Name == "grep" {
		pathKey = "path"
	}
	path, _ := args[pathKey].(string)
	clean := filepath.Clean(path)
	allowed := err == nil && (call.Function.Name == "read" || call.Function.Name == "grep") && x.files[clean]
	if !allowed {
		obs = conversation.Observation{Failed: true, Refused: true, ReasonCode: "EVAL_READ_ONLY_SCOPE", Text: "Only the task's specified source files may be inspected with read or grep; no action ran."}
	} else {
		obs = (appToolExecutor{x.app}).Execute(ctx, call)
	}
	x.mu.Lock()
	x.events = append(x.events, map[string]any{"tool": call.Function.Name, "arguments": args, "failed": obs.Failed, "refused": obs.Refused, "truncated": obs.Truncated, "reason_code": obs.ReasonCode, "text": obs.Text})
	x.mu.Unlock()
	return obs
}

// readRecordedTurn resolves the exact recorded operator row named by the
// dashboard receipt, and so the host turn the request ran as. It deliberately treats missing/ambiguous evidence as
// unavailable instead of silently falling back to prompt-text matching.
func readRecordedTurn(ctx context.Context, query func(context.Context, interaction.Query) (*interaction.Page, error), requestID string, frames []dashboard.ServerMessage) (interaction.Record, error) {
	var zero interaction.Record
	var receipt *interaction.Location
	for _, frame := range frames {
		if frame.Type != "chat_receipt" || frame.RequestID != requestID || frame.ChatRecording == nil || frame.ChatRecording.Ref == nil {
			continue
		}
		if receipt != nil {
			return zero, fmt.Errorf("recorded turn unavailable: duplicate chat receipt")
		}
		r := *frame.ChatRecording.Ref
		if r.Source != "recorded" || r.Incarnation == "" || r.ID == "" {
			return zero, fmt.Errorf("recorded turn unavailable: invalid chat receipt reference")
		}
		receipt = &r
	}
	if receipt == nil {
		return zero, fmt.Errorf("recorded turn unavailable: missing chat receipt")
	}
	q := interaction.Query{Version: 1, Source: receipt.Source, Filter: interaction.Filter{Role: interaction.Operator}, Limit: 50}
	const maxPages = 64
	for pageNo := 0; pageNo < maxPages; pageNo++ {
		page, err := query(ctx, q)
		if err != nil {
			return zero, fmt.Errorf("recorded turn unavailable: history query: %w", err)
		}
		if page == nil {
			return zero, fmt.Errorf("recorded turn unavailable: nil history page")
		}
		if page.Source != receipt.Source || page.Incarnation != receipt.Incarnation {
			return zero, fmt.Errorf("recorded turn unavailable: receipt source/incarnation mismatch")
		}
		for _, row := range page.Rows {
			if row.ID != receipt.ID {
				continue
			}
			if row.Role != interaction.Operator {
				return zero, fmt.Errorf("recorded turn unavailable: receipt names non-operator row")
			}
			if row.TurnID == "" {
				return zero, fmt.Errorf("recorded turn unavailable: the receipt's row names no host turn")
			}
			return row, nil
		}
		if page.NextCursor == "" {
			return zero, fmt.Errorf("recorded turn unavailable: receipt row not found")
		}
		q.Cursor = page.NextCursor
	}
	return zero, fmt.Errorf("recorded turn unavailable: history page bound exceeded")
}

func TestRequestOptionsLiveCase(t *testing.T) {
	if os.Getenv("AII_REQUEST_CONTRACT_LIVE") != "1" {
		t.Skip("requires explicitly admitted local workload window")
	}
	endpoint := os.Getenv("AII_REQUEST_CONTRACT_ENDPOINT")
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" {
		t.Fatal("requires guarded loopback provider proxy")
	}
	input, err := os.ReadFile(os.Getenv("AII_REQUEST_CONTRACT_CASE"))
	if err != nil {
		t.Fatal(err)
	}
	var task requestLiveCase
	if err := json.Unmarshal(input, &task); err != nil {
		t.Fatal(err)
	}
	if task.ID == "" || len(task.Sources) == 0 {
		t.Fatal("case identity and frozen sources are required")
	}
	a, root := dispatchApp(t)
	defer stopApp(t, a)
	ex := &requestLiveExecutor{app: a, root: root, files: map[string]bool{}}
	for name, text := range task.Sources {
		if filepath.IsAbs(name) || filepath.Clean(name) != name || strings.HasPrefix(name, "..") {
			t.Fatal("unsafe fixture path")
		}
		target := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			t.Fatal(err)
		}
		if text != "__INTENTIONALLY_MISSING__" {
			if err := os.WriteFile(target, []byte(text), 0600); err != nil {
				t.Fatal(err)
			}
		}
		ex.files[target] = true
	}
	extra := map[string]any{}
	effort := "high"
	if task.ThinkingOff {
		extra["chat_template_kwargs"] = map[string]any{"enable_thinking": false}
		effort = ""
	}
	zero := 0.0
	client := llm.New(&llm.ClientConfig{Endpoint: endpoint, APIKey: os.Getenv("AII_REQUEST_CONTRACT_PROXY_KEY"), Model: os.Getenv("AII_REQUEST_CONTRACT_MODEL"), ReasoningEffort: effort, MaxOutputTokens: 2048, MaxInputTokens: 60000, Temperature: &zero, TimeoutSeconds: 240, NoStream: task.NoStream, Retries: -1, Extra: extra})
	a.llmSwap.Swap(client)
	a.conv = conversation.New(a.llmSwap, ex, appToolDefiner{a}, appTranscript{st: a.store, safe: a.safeTools}, appEmitter{a: a}, conversation.Config{MaxIterations: 8, MaxToolCalls: 8, ContextBudgetTokens: 60000, MaxToolResultChars: 32000, TurnTokenBudget: 400000, ReplaySafe: func(c llm.ToolCall) bool { return c.Function.Name == "read" || c.Function.Name == "grep" }})
	a.composer.SetMaxTokens(60000)
	a.promptGate.SetMaxTokens(60000)
	h := &dashboard.WSHandler{Speaker: "identity"}
	a.wireTurnHooks(h)
	s := dashboard.New("127.0.0.1", 0, h)
	if _, err := s.Start(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	conn := dialWSS(t, s.Origin())
	defer conn.Close(websocket.StatusNormalClosure, "")
	ctx, cancel := context.WithTimeout(t.Context(), 9*time.Minute)
	defer cancel()
	prompt := strings.ReplaceAll(task.Prompt, "{{ROOT}}", root)
	started := time.Now()
	body, _ := json.Marshal(dashboard.ClientMessage{Type: "chat", RequestID: task.ID, Message: prompt, RequestOptions: task.Options})
	if err := conn.Write(ctx, websocket.MessageText, body); err != nil {
		t.Fatal(err)
	}
	var terminal dashboard.ServerMessage
	var frames []dashboard.ServerMessage
	for {
		_, raw, err := conn.Read(ctx)
		if err != nil {
			terminal = dashboard.ServerMessage{Type: "error", Message: err.Error()}
			break
		}
		var msg dashboard.ServerMessage
		if err := json.Unmarshal(raw, &msg); err != nil {
			t.Fatal(err)
		}
		frames = append(frames, msg)
		if msg.Type == "error" || msg.Type == "response" && msg.Done && msg.Role == "identity" && msg.RequestErrorCode == "" {
			terminal = msg
			break
		}
	}
	elapsed := time.Since(started).Seconds()
	// Wait for the turn cleanup before observing the events and stopping the app.
	for a.TurnActive() && ctx.Err() == nil {
		time.Sleep(10 * time.Millisecond)
	}
	ex.mu.Lock()
	events := append([]map[string]any(nil), ex.events...)
	ex.mu.Unlock()
	output := map[string]any{"id": task.ID, "fixture_root": root, "elapsed_seconds": elapsed, "response": terminal, "events": events, "frames": frames, "completed": terminal.Type == "response" && terminal.Done, "turn_released": !a.TurnActive()}
	// Bind history to the durable chat_receipt, never to prompt text. The
	// receipt names an interaction record (not necessarily the host turn ID).
	var turnErr error
	if row, readErr := readRecordedTurn(t.Context(), a.store.QueryInteractions, task.ID, frames); readErr != nil {
		turnErr = readErr
		output["turn_unavailable"] = readErr.Error()
	} else {
		output["turn_id"] = row.TurnID
		// What the turn cost, by the exact id: the existing usage row, of
		// which a turn that reached a provider has exactly one.
		var n int
		var usage struct {
			ProviderCalls                     int
			Prompt, Completion, Total, Cached *int
			UnknownAttempts, Silent           *int
			Provider, Model                   *string
		}
		err := a.store.DB().QueryRow(`SELECT COUNT(*) FROM turn_metrics WHERE turn_id = ?`, row.TurnID).Scan(&n)
		if err == nil && n == 1 {
			err = a.store.DB().QueryRow(`SELECT rounds, prompt_tokens, completion_tokens, total_tokens, cached_prompt_tokens, unknown_attempts, silent_calls, provider, model
				FROM turn_metrics WHERE turn_id = ?`, row.TurnID).Scan(&usage.ProviderCalls, &usage.Prompt, &usage.Completion, &usage.Total, &usage.Cached, &usage.UnknownAttempts, &usage.Silent, &usage.Provider, &usage.Model)
		}
		switch {
		case err != nil:
			turnErr = fmt.Errorf("the turn's usage row could not be read: %w", err)
		case n == 0:
			turnErr = fmt.Errorf("the turn has no usage row: no provider call was recorded for it")
		case n > 1:
			turnErr = fmt.Errorf("the turn has %d usage rows where one is expected", n)
		}
		if turnErr != nil {
			output["usage_unavailable"] = turnErr.Error()
		} else {
			output["usage"] = map[string]any{"provider_calls": usage.ProviderCalls, "prompt_tokens": usage.Prompt, "completion_tokens": usage.Completion, "total_tokens": usage.Total,
				"cached_prompt_tokens": usage.Cached, "unknown_attempts": usage.UnknownAttempts, "silent_calls": usage.Silent, "provider": usage.Provider, "model": usage.Model}
		}
	}
	encoded, err := json.MarshalIndent(output, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(os.Getenv("AII_REQUEST_CONTRACT_RESULT"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(encoded); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if terminal.Type == "error" {
		t.Fatalf("live request did not complete: %s", terminal.RequestErrorCode)
	}
	if turnErr != nil {
		t.Fatalf("live request's recorded turn unavailable: %v", turnErr)
	}
	fmt.Println("bounded local case completed; result written")
}
