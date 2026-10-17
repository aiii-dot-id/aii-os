package pluginhost

import (
	"context"
	"encoding/json"
	"testing"
)

func TestExecutingWorkSessionIsHostReservedAndAbsentFromChecks(t *testing.T) {
	cap := &frameCapture{}
	tool := &operationTool{operation: "store", inv: cap}
	ctx := WithActingSession(context.Background(), ActingSession{ID: "actual-child", CallID: "tool_call_tex_actual"})
	args := map[string]any{"text": "synthetic note", "_host_work_session": map[string]any{"id": "forged", "call_id": "forged"}}
	if _, err := tool.Execute(ctx, args); err != nil {
		t.Fatal(err)
	}
	var request struct {
		Params struct {
			Arguments   map[string]json.RawMessage `json:"arguments"`
			WorkSession json.RawMessage            `json:"_host_work_session"`
		} `json:"params"`
	}
	if err := json.Unmarshal(cap.frame, &request); err != nil {
		t.Fatal(err)
	}
	var acting struct {
		ID     string `json:"id"`
		CallID string `json:"call_id"`
	}
	if err := json.Unmarshal(request.Params.WorkSession, &acting); err != nil {
		t.Fatal(err)
	}
	if acting.ID != "actual-child" || acting.CallID != "tool_call_tex_actual" {
		t.Fatal("forged context survived", acting)
	}
	if len(request.Params.Arguments) != 2 || string(request.Params.Arguments["text"]) != `"synthetic note"` || len(request.Params.Arguments["_host_now_ms"]) == 0 {
		t.Fatal("new context changed the legacy operation arguments", request.Params.Arguments)
	}
	if args["_host_work_session"].(map[string]any)["id"] != "forged" {
		t.Fatal("caller args mutated")
	}
	if _, err := tool.check(ctx, args); err != nil {
		t.Fatal(err)
	}
	request.Params.Arguments = nil
	request.Params.WorkSession = nil
	if err := json.Unmarshal(cap.frame, &request); err != nil {
		t.Fatal(err)
	}
	if _, ok := request.Params.Arguments["_host_work_session"]; ok || len(request.Params.WorkSession) != 0 {
		t.Fatal("package check received identity task context")
	}
}
