package interaction

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAnExitStatusBelongsToAToolResultAndIsNotServed(t *testing.T) {
	three, minus := 3, -1
	result := Input{ID: "r", Kind: ToolResult, Role: System, RelatedID: "c", Outcome: Succeeded,
		Source: &Source{Kind: "tool", ID: "x"}, Details: Details{Tool: "shell", Actor: "main", ExitStatus: &three}}
	if err := Validate(result); err != nil {
		t.Fatalf("a tool result with its exit status is refused: %v", err)
	}
	negative := result
	negative.Details.ExitStatus = &minus
	if err := Validate(negative); err == nil {
		t.Error("a negative exit status was admitted")
	}
	call := Input{ID: "c", Kind: ToolCall, Role: System, Source: &Source{Kind: "tool", ID: "x"},
		Details: Details{Tool: "shell", Actor: "main", ExitStatus: &three}}
	if err := Validate(call); err == nil {
		t.Error("an exit status on a call, which has not run, was admitted")
	}
	message := Input{ID: "m", Kind: Message, Role: Resident, Content: "hello", Details: Details{ExitStatus: &three}}
	if err := Validate(message); err == nil {
		t.Error("an exit status on a message was admitted")
	}

	r := Record{ID: "r", Kind: ToolResult, Role: System, Details: Details{Tool: "shell", ExitStatus: &three}}
	if got := Served(r); got.Details.ExitStatus != nil || got.Details.Tool != "shell" {
		t.Fatalf("Served: %+v", got.Details)
	}
	if r.Details.ExitStatus == nil {
		t.Fatal("Served changed the record it was given")
	}
	raw, err := json.Marshal(Compact(r, "recorded", "inc"))
	if err != nil || strings.Contains(string(raw), "exit_status") {
		t.Fatalf("a compacted row carries the member: %s %v", raw, err)
	}
}

// A work change's promise and its wait are the session's own and stay in
// the record; the version 1 contract does not carry them, so a page and a
// detail read leave them out and keep the rest of the change.
func TestAWorksPromiseAndWaitAreNotServed(t *testing.T) {
	promise, resumes, plan := "the figures by noon", "now", "add them up"
	r := Record{ID: "w", Kind: Notice, Role: System, Details: Details{Work: &WorkChange{
		Session: "ws", Plan: &plan, Promise: &promise, ResumesOn: &resumes, Under: promise, Met: "delivery:ws_child"}}}
	got := Served(r).Details.Work
	if got.Promise != nil || got.ResumesOn != nil || got.Under != "" || got.Met != "" || got.Session != "ws" || got.Plan != &plan {
		t.Fatalf("Served: %+v", got)
	}
	if w := r.Details.Work; w.Promise == nil || w.ResumesOn == nil || w.Under == "" || w.Met == "" {
		t.Fatal("Served changed the record it was given")
	}
	raw, err := json.Marshal(Compact(r, "recorded", "inc"))
	if err != nil {
		t.Fatal(err)
	}
	for _, member := range []string{`"promise"`, `"resumes_on"`, `"under"`, `"met"`} {
		if strings.Contains(string(raw), member) {
			t.Fatalf("a compacted row carries %s: %s", member, raw)
		}
	}
}
