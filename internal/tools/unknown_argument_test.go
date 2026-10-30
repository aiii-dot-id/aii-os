package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/untrusted"
)

func TestAnUnknownArgumentToABuiltinIsRefusedNamingIt(t *testing.T) {
	home := t.TempDir()
	marker := filepath.Join(home, "ran")
	r := NewRegistry(home, nil, Timeouts{})
	before := r.MalformedCallCount()
	res, err := r.ExecuteModel(t.Context(), "shell", map[string]any{"command": "touch " + marker, "timeout": 20})
	if err != nil || !res.Refused || res.ReasonCode != ReasonUnknownArgument {
		t.Fatalf("an unknown argument was not refused, typed: %+v %v", res, err)
	}
	for _, want := range []string{"shell does not take timeout", "nothing ran", "It takes: command, cwd."} {
		if !strings.Contains(res.Error, want) {
			t.Errorf("the refusal does not say %q: %s", want, res.Error)
		}
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("the refused command ran: %v", err)
	}
	if r.MalformedCallCount() != before+1 {
		t.Errorf("the refused call is not counted malformed")
	}

	// Two at once are named together, sorted.
	res, _ = r.ExecuteModel(t.Context(), "grep", map[string]any{"pattern": "x", "n": true, "-A": 2})
	if res.ReasonCode != ReasonUnknownArgument || !strings.Contains(res.Error, "grep does not take -A, n;") {
		t.Fatalf("grep's unknown arguments are not named: %+v", res)
	}

	// Every declared argument passes.
	if res, _ := r.ExecuteModel(t.Context(), "ls", map[string]any{"path": home, "recursive": false, "sort": "name", "limit": 5, "offset": 0}); res.Refused {
		t.Fatalf("a call of declared arguments was refused: %+v", res)
	}
	if res, _ := r.Execute(t.Context(), "ls", map[string]any{"path": home, "host_note": "x"}); res.Refused {
		t.Fatalf("the host's call was refused: %+v", res)
	}
	// A plugin's operation keeps its own schema's check.
	if err := r.RegisterDynamic(&namedTool{"pl_x_op"}, "org.example.x"); err != nil {
		t.Fatal(err)
	}
	res, err = r.ExecuteModel(t.Context(), "pl_x_op", map[string]any{"anything": 1})
	if err != nil || res.Refused || res.Output != untrusted.Wrap("plugin org.example.x", "ran pl_x_op") {
		t.Fatalf("a plugin's operation was held to the builtins' check: %+v %v", res, err)
	}
}

// A POSITIVE DEPTH IS A RECURSIVE LISTING. ls refused depth without
// recursive=true, and that refusal was the commonest ls failure: depth now
// lists that many levels whatever recursive says.
func TestADepthListsRecursively(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "a", "b"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "a", "b", "deep.txt"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	r := NewRegistry(home, nil, Timeouts{})
	// A listing names its entries with the system's separator.
	sep := string(filepath.Separator)
	res, err := r.ExecuteModel(t.Context(), "ls", map[string]any{"path": home, "depth": 2})
	if err != nil || res.Error != "" || !strings.Contains(res.Output, "  a"+sep+"b"+sep+"\n") || strings.Contains(res.Output, "deep.txt") {
		t.Fatalf("depth=2 did not list two levels: %+v %v", res, err)
	}
	res, _ = r.ExecuteModel(t.Context(), "ls", map[string]any{"path": home, "depth": 3, "recursive": false})
	if res.Error != "" || !strings.Contains(res.Output, "  a"+sep+"b"+sep+"deep.txt\n") {
		t.Fatalf("depth=3 did not list three levels: %+v", res)
	}
	res, _ = r.ExecuteModel(t.Context(), "ls", map[string]any{"path": home})
	if res.Error != "" || !strings.Contains(res.Output, "  a"+sep+"\n") || strings.Contains(res.Output, "a"+sep+"b") {
		t.Fatalf("a plain listing went below its directory: %+v", res)
	}
}
