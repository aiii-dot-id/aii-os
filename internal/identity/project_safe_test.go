package identity

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestProjectMutationsRefuseAtTheSAFEVerbAndReadsContinue(t *testing.T) {
	e, _ := newVerbEngine(t)
	e.SetSafeMode("the record cannot be verified")
	for _, action := range []string{"create", "update", "close", "select", "deselect", "evidence", "waive"} {
		if _, err := e.ExecuteAction(context.Background(), "verb", "work", map[string]any{
			"action": "project." + action, "project": "existing", "name": "new",
		}); !errors.Is(err, ErrProjectSafe) {
			t.Errorf("project.%s reached a mutation port in SAFE: %v", action, err)
		}
	}
	read, err := e.ExecuteAction(context.Background(), "verb", "recall", map[string]any{"source": "projects"})
	if err != nil || !strings.Contains(read, "No projects yet") {
		t.Fatalf("read-only project recall stopped working in SAFE: %q %v", read, err)
	}
}
