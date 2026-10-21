package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/llm"
)

// THE LESSON MEETS THE DECISION. A work session that names a kind starts
// with the
// kind's record, the identity's playbook and the matching lessons in the
// start's own reply; the card says when the session runs past the
// shortest served attempt; a failed call brings its lessons once.
func TestAWorkSessionStartsWithItsPlaybookRecordAndLessons(t *testing.T) {
	a, _ := promptCacheApp(t)
	p := addContextProbe(t, a, `{"text":"lesson: the preview URL is in the build log","detail_route":"search id=lesson-1"}`)
	ctx := context.Background()
	work := func(args map[string]any) string {
		t.Helper()
		out, err := a.engine.ExecuteAction(ctx, "verb", "work", args)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}

	// A served attempt of two calls is the kind's record.
	work(map[string]any{"action": "start", "description": "deploy the preview", "family": "deploy-preview"})
	for i, tool := range []string{"read", "shell"} {
		if err := a.store.RecordToolStart("turn_first", i+1, "main", "m", "", tool, "{}"); err != nil {
			t.Fatal(err)
		}
	}
	work(map[string]any{"action": "deliver", "result": "served: preview up"})

	book := filepath.Join(a.configSnapshot().Tools.CWD, playbookRel("deploy-preview"))
	if err := os.MkdirAll(filepath.Dir(book), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(book, []byte("---\nname: deploy-preview\n---\n1. read the build log for the URL\n2. open it\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := work(map[string]any{"action": "start", "description": "deploy the preview again", "family": "deploy-preview"})
	for _, want := range []string{
		"Kind deploy-preview: 1 delivered attempt(s), 1 served. Shortest served: 2 calls (read, shell).",
		"Your playbook for deploy-preview (playbooks/deploy-preview/SKILL.md, your own file",
		"1. read the build log for the URL",
		"lesson: the preview URL is in the build log",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("the start lacks %q:\n%s", want, out)
		}
	}
	if _, args := p.snapshot(); len(args) == 0 || !strings.Contains(args[len(args)-1]["query"].(string), "deploy-preview deploy the preview again") {
		t.Fatalf("the lessons were not asked with the kind and the description: %v", args)
	}

	// Past the shortest served attempt, the card says so.
	ws, _ := a.store.ActiveWorkSession()
	for i := 1; i <= 3; i++ {
		if err := a.store.RecordToolStart("turn_second", i, "main", "m", "", "shell", "{}"); err != nil {
			t.Fatal(err)
		}
	}
	if line := a.familyCardLine(ctx, ws); !strings.Contains(line, "Your playbook for deploy-preview (playbooks/deploy-preview/SKILL.md, your own file") || !strings.Contains(line, "1. read the build log for the URL") || !strings.Contains(line, "3 calls so far in this session; the shortest served attempt took 2.") {
		t.Fatalf("the card line: %q", line)
	}

	// A failed call brings its lessons once per tool and reason.
	call := func() string {
		var tc llm.ToolCall
		tc.ID, tc.Type, tc.Function.Name, tc.Function.Arguments = "c1", "function", "no_such_tool", "{}"
		return appToolExecutor{a}.Execute(ctx, tc).Text
	}
	if first := call(); !strings.Contains(first, "Your notes about failures like this one:") || !strings.Contains(first, "lesson: the preview URL is in the build log") {
		t.Fatalf("a failed call's result lacks its lessons:\n%s", first)
	}
	if again := call(); strings.Contains(again, "Your notes about failures like this one:") {
		t.Fatalf("the same failure brought its lessons twice:\n%s", again)
	}
}
