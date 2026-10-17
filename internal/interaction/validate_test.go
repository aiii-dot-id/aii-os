package interaction

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// THE WRITE GATE DECIDES WHAT ENTERS THE PERMANENT RECORD. Every store
// append passes through Validate, and a rule that stops refusing lets a
// malformed occurrence into an identity's history for good. Each row is
// a valid base with one thing broken, and must be refused naming that
// thing; each base must pass, so a refusal is the mutation's and not the
// base's.
func TestValidateRefusesEachBrokenRule(t *testing.T) {
	call := func() Input {
		return Input{ID: "c1", Kind: ToolCall, Role: System, Source: &Source{Kind: "tool", ID: "t1"}, Details: Details{Tool: "read", Actor: "resident"}}
	}
	bases := map[string]func() Input{
		"message": func() Input { return Input{ID: "m1", Kind: Message, Role: Operator, Content: "hello"} },
		"outbound": func() Input {
			return Input{ID: "o1", Kind: Outbound, Role: Resident, Source: &Source{Kind: "outbox", ID: "x1"}}
		},
		"call": call,
		"result": func() Input {
			in := call()
			in.ID, in.Kind, in.RelatedID, in.Outcome = "r1", ToolResult, "c1", Succeeded
			return in
		},
		"notice": func() Input { return Input{ID: "n1", Kind: Notice, Role: System, Content: "a notice"} },
		"annotation": func() Input {
			return Input{ID: "a1", Kind: Annotation, Role: System, RelatedID: "m1",
				Details: Details{Annotation: &AnnotationData{Kind: "speaker", Key: "k", Payload: json.RawMessage(`{"speaker":"ContactB"}`)}}}
		},
	}

	for _, outcome := range []Outcome{Succeeded, Failed, Unknown, Cancelled, Refused} {
		in := bases["result"]()
		in.Outcome = outcome
		if err := Validate(in); err != nil {
			t.Fatalf("a %s result is refused: %v", outcome, err)
		}
	}
	for name, base := range bases {
		if err := Validate(base()); err != nil {
			t.Fatalf("the %s base is refused: %v", name, err)
		}
	}
	neg := int64(-1)
	far := time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		name, base, says string
		mutate           func(*Input)
	}{
		{"an empty ID", "message", "occurrence ID", func(in *Input) { in.ID = "" }},
		{"an ID over 256 bytes", "message", "occurrence ID", func(in *Input) { in.ID = strings.Repeat("i", 257) }},
		{"a NUL in the ID", "message", "occurrence ID", func(in *Input) { in.ID = "m\x001" }},
		{"the legacy kind", "message", "kind or role", func(in *Input) { in.Kind = Legacy }},
		{"an unknown role", "message", "kind or role", func(in *Input) { in.Role = "stranger" }},
		{"content that is not UTF-8", "message", "UTF-8", func(in *Input) { in.Content = string([]byte{0xff}) }},
		{"a NUL in a reference", "message", "context reference", func(in *Input) { in.SessionID = "s\x00" }},
		{"a reference over 512 bytes", "message", "context reference", func(in *Input) { in.TurnID = strings.Repeat("t", 513) }},
		{"a relation to itself", "result", "self relation", func(in *Input) { in.RelatedID = in.ID }},
		{"a source of an unknown kind", "call", "invalid source", func(in *Input) { in.Source.Kind = "web" }},
		{"a source with no ID", "call", "invalid source", func(in *Input) { in.Source.ID = "" }},
		{"a negative duration", "result", "negative duration", func(in *Input) { in.Details.DurationMS = &neg }},
		{"a migration origin field", "message", "origin fields", func(in *Input) { in.Details.Origin = "import" }},
		{"an unknown outcome", "result", "invalid outcome", func(in *Input) { in.Outcome = "maybe" }},
		{"system speech", "message", "message role", func(in *Input) { in.Role = System }},
		{"a message with a source", "message", "message role", func(in *Input) { in.Source = &Source{Kind: "tool", ID: "t"} }},
		{"a message with an outcome", "message", "message role", func(in *Input) { in.Outcome = Failed }},
		{"outbound with its own content", "outbound", "outbound", func(in *Input) { in.Content = "copied body" }},
		{"outbound not from the resident", "outbound", "outbound", func(in *Input) { in.Role = Operator }},
		{"outbound from a tool source", "outbound", "outbound", func(in *Input) { in.Source.Kind = "tool" }},
		{"a call with no tool", "call", "tool provenance", func(in *Input) { in.Details.Tool = "" }},
		{"a call with no actor", "call", "tool provenance", func(in *Input) { in.Details.Actor = "" }},
		{"a call with an outcome", "call", "attempt is not an outcome", func(in *Input) { in.Outcome = Succeeded }},
		{"a result without its call", "result", "requires its call", func(in *Input) { in.RelatedID = "" }},
		{"a result with no known outcome", "result", "requires its call", func(in *Input) { in.Outcome = "" }},
		{"a notice not by the system", "notice", "system-authored", func(in *Input) { in.Role = Resident }},
		{"an annotation of nothing", "annotation", "invalid annotation", func(in *Input) { in.RelatedID = "" }},
		{"an annotation with content", "annotation", "invalid annotation", func(in *Input) { in.Content = "words" }},
		{"an annotation with no kind", "annotation", "invalid annotation", func(in *Input) { in.Details.Annotation.Kind = "" }},
		{"an annotation payload that is not JSON", "annotation", "invalid annotation", func(in *Input) { in.Details.Annotation.Payload = json.RawMessage(`{"a":`) }},
		{"an annotation on a message", "message", "annotation in another kind", func(in *Input) {
			in.Details.Annotation = &AnnotationData{Kind: "speaker", Payload: json.RawMessage(`{}`)}
		}},
		{"a work change outside a notice", "message", "work change", func(in *Input) { in.Details.Work = &WorkChange{Session: "w"} }},
		{"a grade on the resident's words", "message", "grade", func(in *Input) {
			in.Role = Resident
			in.Details.Grade = &Grade{Session: "s", Grade: "good"}
		}},
		{"a time past year 9999", "message", "", func(in *Input) { in.OccurredAt = &far }},
	} {
		t.Run(c.name, func(t *testing.T) {
			in := bases[c.base]()
			c.mutate(&in)
			err := Validate(in)
			if err == nil {
				t.Fatal("accepted")
			}
			var e *Error
			if !errors.As(err, &e) || e.Code != "INTERACTION_ARGUMENT_INVALID" {
				t.Fatalf("refused untyped: %v", err)
			}
			if !strings.Contains(e.Detail, c.says) {
				t.Fatalf("refused for another reason (want %q): %v", c.says, err)
			}
		})
	}
}
