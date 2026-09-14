package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/identity"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// .
// .
// .
func TestWorkBoundariesAndActorsReachSubscribers(t *testing.T) {
	a := liveApp(t)
	const id = "org.example.journal"
	sink := &eventSink{name: pluginhost.ToolNameFor(id, "journal.event")}
	if err := a.toolReg.RegisterHostOp(sink, id); err != nil {
		t.Fatal(err)
	}
	a.startSubscriber(&pluginhost.ActivePlugin{ID: id, Subscriptions: []pluginhost.SubscriptionDecl{
		{Topic: pluginhost.TopicWorkStarted, Operation: "journal.event"},
		{Topic: pluginhost.TopicWorkDelivered, Operation: "journal.event"},
		{Topic: pluginhost.TopicWorkHarvested, Operation: "journal.event"},
		{Topic: pluginhost.TopicToolCalled, Operation: "journal.event", Filter: map[string]string{"tool": "ls"}},
	}})
	defer a.stopSubscriber(id)

	if err := a.store.StartWorkSession("ws_main", "write the report"); err != nil {
		t.Fatal(err)
	}
	a.executeToolCall(context.Background(), toolCallFor("ls"))
	a.executeToolCall(context.WithValue(context.Background(), identity.SubagentWorkSession{}, "ws_child"), toolCallFor("ls"))
	if err := a.store.DeliverWorkSession("ws_main", "served: the report", store.EvidenceCompletedLocally, ""); err != nil {
		t.Fatal(err)
	}
	if err := a.store.MarkHarvested("ws_main", 7); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for sink.count() < 5 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if sink.count() != 5 {
		t.Fatalf("got %d events: %v", sink.count(), sink.topics())
	}
	if got := strings.Join(sink.topics(), ","); got != "work.started,tool.called,tool.called,work.delivered,work.harvested" {
		t.Fatalf("order: %s", got)
	}
	payloads := make([]map[string]interface{}, 0, 5)
	for _, g := range sink.got {
		p, _ := g["payload"].(map[string]interface{})
		payloads = append(payloads, p)
	}
	if p := payloads[0]; p["session"] != "ws_main" || p["actor"] != "main" || p["project"] != "" {
		t.Fatalf("work.started: %v", p)
	}
	if p := payloads[1]; p["actor"] != "main" || p["session"] != "ws_main" || p["tool"] != "ls" {
		t.Fatalf("the main seat's tool event names its open session: %v", p)
	}
	if p := payloads[2]; p["actor"] != "subagent" || p["session"] != "ws_child" {
		t.Fatalf("a sub-agent's tool event names its own session: %v", p)
	}
	if p := payloads[3]; p["outcome"] != "served" || p["evidence"] != store.EvidenceCompletedLocally || p["actor"] != "main" {
		t.Fatalf("work.delivered carries the classes: %v", p)
	}
	if p := payloads[4]; p["session"] != "ws_main" {
		t.Fatalf("work.harvested: %v", p)
	}
	for _, p := range payloads {
		for k := range p {
			switch k {
			case "session", "project", "actor", "outcome", "evidence", "tool", "failed", "duration_ms":
			default:
				t.Fatalf("an event carries identifiers and classes only; found %q in %v", k, p)
			}
		}
		if s, _ := p["session"].(string); strings.Contains(s, "report") {
			t.Fatalf("a description leaked: %v", p)
		}
	}
}
