//go:build !windows

package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/conversation"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

func TestAShellCallCutShortIsRecordedAsTheRuleSays(t *testing.T) {
	for _, c := range []struct {
		name, command string
		ceiling       int // the shell's, in seconds; 0 is the default
		stop          bool
		outcome, code string
	}{
		{"the operator's stop", "echo begun > started; sleep 30", 0, true, "cancelled", ""},
		{"the shell's ceiling", "echo begun; sleep 30", 1, false, "failed", tools.ReasonShellTimeout},
		{"the shell's ceiling, after more output than the record keeps", "seq 1 2000; sleep 30", 1, false, "failed", tools.ReasonShellTimeout},
		{"a non-zero exit, which is an answer", "echo begun; exit 3", 0, false, "succeeded", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			a := newVoiceApp(t)
			home := t.TempDir()
			a.toolReg = tools.NewRegistry(home, nil, tools.Timeouts{ShellSeconds: c.ceiling})
			args, err := json.Marshal(map[string]string{"command": c.command})
			if err != nil {
				t.Fatal(err)
			}
			loop := conversation.New(&argsScript{tool: "shell", args: string(args)}, appToolExecutor{a}, appToolDefiner{a}, appTranscript{st: a.store}, nil, testLoop(conversation.Config{}))
			ctx, done := a.beginCancellableTurn(context.Background())
			defer done()
			ended := make(chan error, 1)
			go func() {
				_, err := loop.Run(ctx, "system", []llm.Message{{Role: "user", Content: "go"}})
				ended <- err
			}()
			if c.stop {
				deadline := time.Now().Add(10 * time.Second)
				for {
					if _, err := os.Stat(filepath.Join(home, "started")); err == nil {
						break
					}
					if time.Now().After(deadline) {
						t.Fatal("the command never began")
					}
					time.Sleep(10 * time.Millisecond)
				}
				if !a.CancelTurn() {
					t.Fatal("no turn to stop")
				}
			}
			select {
			case <-ended:
			case <-time.After(20 * time.Second):
				t.Fatal("the turn did not end")
			}
			var outcome string
			var code sql.NullString
			if err := a.store.DB().QueryRow(`SELECT outcome, json_extract(details, '$.reason_code') FROM conversations WHERE kind='tool_result' AND json_extract(details, '$.tool')='shell'`).Scan(&outcome, &code); err != nil {
				t.Fatalf("the shell's result: %v", err)
			}
			if outcome != c.outcome || code.String != c.code {
				t.Fatalf("the call's record: outcome %q code %q, want %q %q", outcome, code.String, c.outcome, c.code)
			}
		})
	}
}

// The event says what the record says: a plugin that subscribed to
// tool.called learns that the call failed. It learned "failed": false of
// a command the ceiling ended.
func TestASubscriberLearnsThatAShellCallRanIntoItsCeiling(t *testing.T) {
	a := liveApp(t)
	a.toolReg = tools.NewRegistry(t.TempDir(), nil, tools.Timeouts{ShellSeconds: 1})
	const id = "org.example.hooks"
	sink := &eventSink{name: pluginhost.ToolNameFor(id, "log.event")}
	if err := a.toolReg.RegisterHostOp(sink, id); err != nil {
		t.Fatal(err)
	}
	ap := &pluginhost.ActivePlugin{ID: id, Subscriptions: []pluginhost.SubscriptionDecl{{Topic: pluginhost.TopicToolCalled, Operation: "log.event", Filter: map[string]string{"tool": "shell"}}}}
	a.startSubscriber(ap)
	defer a.stopSubscriberOf(ap)

	call := toolCallFor("shell")
	call.Function.Arguments = `{"command":"echo begun; sleep 30"}`
	obs := a.executeToolCall(context.Background(), call)
	if !obs.Failed || obs.Refused || obs.ReasonCode != tools.ReasonShellTimeout {
		t.Fatalf("the observation of a command the ceiling ended: %+v", obs)
	}
	awaitEvents(t, "the tool event", func() bool { return sink.count() == 1 })
	payload, _ := sink.got[0]["payload"].(map[string]any)
	if payload["tool"] != "shell" || payload["failed"] != true {
		t.Fatalf("the tool event carries the name and the outcome: %v", payload)
	}
}
