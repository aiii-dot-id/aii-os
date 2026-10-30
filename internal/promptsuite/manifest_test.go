package promptsuite

import (
	"encoding/json"
	"strings"
	"testing"
)

// minimal is a manifest that holds: one class, one task, one check.
func minimal() map[string]any {
	return map[string]any{
		"format": 1, "revision": 1, "trials_per_task": 3, "trial_timeout_seconds": 600,
		"classes": []any{map[string]any{"id": "claims", "title": "No claim without a call", "traced_to": []any{"B1"}, "threshold": nil}},
		"plugins": []any{map[string]any{"role": "memory", "operations": []any{"store", "search"}}},
		"tasks": []any{map[string]any{
			"id": "claims-count", "version": 1, "class": "claims", "traced_to": "B1", "title": "A count rests on a read",
			"setup": map[string]any{"files": map[string]any{"records/visits.log": "one\ntwo\n"}},
			"turns": []any{"How many lines? Answer with a line COUNT: <n>."},
			"checks": []any{map[string]any{"check": "reply", "why": "the file has two lines", "turn": 1,
				"line": map[string]any{"key": "COUNT", "values": []any{"2"}}}},
		}},
	}
}

func encode(t *testing.T, m map[string]any) []byte {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func task0(m map[string]any) map[string]any { return m["tasks"].([]any)[0].(map[string]any) }

func TestAManifestIsHashedByItsBytesAndAnyEditIsAnotherManifest(t *testing.T) {
	a, err := ParseManifest(encode(t, minimal()))
	if err != nil {
		t.Fatal(err)
	}
	b, err := ParseManifest(append(encode(t, minimal()), ' '))
	if err != nil {
		t.Fatal(err)
	}
	if a.SHA256 == "" || a.SHA256 == b.SHA256 {
		t.Fatalf("a space after the object is a different manifest: %s and %s", a.SHA256, b.SHA256)
	}
}

func TestAManifestThatCannotMeanWhatItSaysIsRefused(t *testing.T) {
	for name, c := range map[string]struct {
		edit func(m map[string]any)
		want string
	}{
		"an unknown field": {func(m map[string]any) { m["judge"] = "a model" }, "unknown field"},
		"another format":   {func(m map[string]any) { m["format"] = 2 }, "format 2"},
		"no trials":        {func(m map[string]any) { m["trials_per_task"] = 0 }, "trials_per_task"},
		"no bound":         {func(m map[string]any) { m["trial_timeout_seconds"] = 0 }, "trial_timeout_seconds"},
		"a class with no task": {func(m map[string]any) {
			m["classes"] = append(m["classes"].([]any), map[string]any{"id": "voice", "title": "t", "traced_to": []any{"C2"}, "threshold": nil})
		}, "has no task"},
		"a task of no class":         {func(m map[string]any) { task0(m)["class"] = "voice" }, "not declared"},
		"a task with no checks":      {func(m map[string]any) { task0(m)["checks"] = []any{} }, "no checks"},
		"a task with no words":       {func(m map[string]any) { task0(m)["turns"] = []any{} }, "no turns"},
		"a check with no reason":     {func(m map[string]any) { task0(m)["checks"].([]any)[0].(map[string]any)["why"] = "" }, "no why"},
		"a check past the last turn": {func(m map[string]any) { task0(m)["checks"].([]any)[0].(map[string]any)["turn"] = 2 }, "turn 2"},
		"a setup file outside home":  {func(m map[string]any) { task0(m)["setup"] = map[string]any{"files": map[string]any{"../escape": "x"}} }, "not a plain path"},
		"a setup file in the record": {func(m map[string]any) {
			task0(m)["setup"] = map[string]any{"files": map[string]any{"data/ledger.jsonl": "x"}}
		}, "not a plain path"},
		"an unknown plugin role":      {func(m map[string]any) { task0(m)["setup"] = map[string]any{"plugins": []any{"planning"}} }, "does not declare"},
		"a package named, not a role": {func(m map[string]any) { m["fixtures"] = []any{} }, "unknown field"},
		"a role with no operations": {func(m map[string]any) {
			m["plugins"] = []any{map[string]any{"role": "memory", "operations": []any{}}}
		}, "declares no operations"},
		"an operation of a role the task does not install": {func(m map[string]any) {
			task0(m)["checks"] = []any{map[string]any{"check": "calls", "why": "w", "call": map[string]any{"tool": "{memory.store}"}, "min": 1}}
		}, "does not install"},
		"an operation the role does not declare": {func(m map[string]any) {
			task0(m)["setup"] = map[string]any{"plugins": []any{"memory"}}
			task0(m)["checks"] = []any{map[string]any{"check": "calls", "why": "w", "call": map[string]any{"tool": "{memory.forget}"}, "min": 1}}
		}, "does not declare"},
		"a grant withheld from a role not installed": {func(m map[string]any) {
			task0(m)["setup"] = map[string]any{"withhold": map[string]any{"memory": []any{"memory"}}}
		}, "does not install"},
		"a grant no install gives": {func(m map[string]any) {
			task0(m)["setup"] = map[string]any{"plugins": []any{"memory"}, "withhold": map[string]any{"memory": []any{"shell"}}}
		}, "not a grant an install gives"},
		"a claim with one value for both truths": {func(m map[string]any) {
			task0(m)["setup"] = map[string]any{"plugins": []any{"memory"}}
			task0(m)["checks"] = []any{map[string]any{"check": "claim", "why": "w", "turn": 1, "call": map[string]any{"tool": "{memory.store}"},
				"claim": map[string]any{"key": "STORED", "refused_when": []any{"refused"}, "refused": "yes", "kept": "yes"}}}
		}, "two different values"},
		"a threshold that is no rate": {func(m map[string]any) {
			m["classes"].([]any)[0].(map[string]any)["threshold"] = map[string]any{"noise": 1.5, "runs": 3}
		}, "threshold"},
		"a threshold of one run": {func(m map[string]any) {
			m["classes"].([]any)[0].(map[string]any)["threshold"] = map[string]any{"noise": 0.1, "runs": 1}
		}, "threshold"},
		"a task id used twice": {func(m map[string]any) { m["tasks"] = append(m["tasks"].([]any), m["tasks"].([]any)[0]) }, "used twice"},
		"a calls check unbounded": {func(m map[string]any) {
			task0(m)["checks"] = []any{map[string]any{"check": "calls", "why": "w", "call": map[string]any{"tool": "read"}}}
		}, "bounds the count"},
		"an order check with both": {func(m map[string]any) {
			task0(m)["checks"] = []any{map[string]any{"check": "order", "why": "w", "call": map[string]any{"tool": "read"},
				"before": map[string]any{"tool": "write"}, "then": map[string]any{"tool": "edit"}}}
		}, "one of before and then"},
		"an outcome the host never records": {func(m map[string]any) {
			task0(m)["checks"] = []any{map[string]any{"check": "calls", "why": "w", "call": map[string]any{"tool": "read", "outcome": "ok"}, "min": 1}}
		}, "not one the host records"},
	} {
		t.Run(name, func(t *testing.T) {
			m := minimal()
			c.edit(m)
			_, err := ParseManifest(encode(t, m))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("want a refusal naming %q, got %v", c.want, err)
			}
		})
	}
}

func TestAManifestIsFrozenOnlyWhenEveryClassHasAThreshold(t *testing.T) {
	m, err := ParseManifest(encode(t, minimal()))
	if err != nil {
		t.Fatal(err)
	}
	if ok, open := m.Frozen(); ok || len(open) != 1 || open[0] != "claims" {
		t.Fatalf("a class with a null threshold leaves the manifest unfrozen, naming it: %v %v", ok, open)
	}
	raw := minimal()
	raw["classes"].([]any)[0].(map[string]any)["threshold"] = map[string]any{"noise": 0.2, "runs": 3}
	m, err = ParseManifest(encode(t, raw))
	if err != nil {
		t.Fatal(err)
	}
	if ok, open := m.Frozen(); !ok || len(open) != 0 {
		t.Fatalf("every class with its threshold is frozen: %v %v", ok, open)
	}
}
