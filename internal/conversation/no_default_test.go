package conversation

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/limits"
	"github.com/aiii-dot-id/aii-os/internal/llm"
)

// THE LOOP HAS NO DEFAULT OF ITS OWN. It
// carried a second copy of the turn's limits — ten rounds
// where the configuration's default is thirty, a 600,000 fence beside the
// table's — and filled any limit a caller left unset with it, so a caller
// that forgot one was fenced at a number nobody chose. Now:
//   - no file of this package declares a Default* constant or variable, or
//     a withDefaults;
//   - a turn whose configuration leaves a limit unset is refused, naming
//     that limit, before any request is made; and each limit is named when
//     it alone is missing.
func TestTheLoopHasNoDefaultOfItsOwn(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			switch d := n.(type) {
			case *ast.ValueSpec:
				for _, id := range d.Names {
					if strings.HasPrefix(id.Name, "Default") || strings.HasPrefix(id.Name, "default") && strings.Contains(strings.ToLower(id.Name), "budget") {
						t.Errorf("%s: %s declares a default of the loop's own; the caller names it (internal/limits owns the code defaults)", fset.Position(id.Pos()), id.Name)
					}
				}
			case *ast.FuncDecl:
				if d.Name.Name == "withDefaults" || d.Name.Name == "InForce" {
					t.Errorf("%s: %s fills a limit the caller did not name", fset.Position(d.Pos()), d.Name.Name)
				}
			}
			return true
		})
	}

	run := func(cfg Config) (*scriptLLM, error) {
		client := &scriptLLM{script: []llm.Response{textResp("answered")}}
		_, err := New(client, &fakeTools{}, oneDefs{}, nil, nil, cfg).Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}})
		return client, err
	}
	var unset UnsetBound
	client, err := run(Config{})
	if !errors.As(err, &unset) || unset != "MaxIterations" || client.calls != 0 {
		t.Fatalf("a turn with no limits named ran, or was refused for another reason: %v (%d requests)", err, client.calls)
	}
	full := testBounds(Config{})
	for name, clear := range map[UnsetBound]func(*Config){
		"MaxIterations":          func(c *Config) { c.MaxIterations = limits.Ceiling{} },
		"TurnTokenBudget":        func(c *Config) { c.TurnTokenBudget = limits.Ceiling{} },
		"MaxToolCalls":           func(c *Config) { c.MaxToolCalls = limits.Ceiling{} },
		"Checkpoint.Calls":       func(c *Config) { c.Checkpoint.Calls = limits.Ceiling{} },
		"Checkpoint.GraceRounds": func(c *Config) { c.Checkpoint.GraceRounds = 0 },
		"Checkpoint.Slack":       func(c *Config) { c.Checkpoint.Slack = 0 },
		"ToolSchemaRefusals":     func(c *Config) { c.ToolSchemaRefusals = 0 },
		"MaxToolResultChars":     func(c *Config) { c.MaxToolResultChars = 0 },
	} {
		cfg := full
		clear(&cfg)
		if _, err := run(cfg); !errors.As(err, &unset) || unset != name || !strings.Contains(err.Error(), string(name)+" is not set") {
			t.Errorf("with %s alone unset the turn was not refused naming it: %v", name, err)
		}
	}
	if _, err := run(full); err != nil {
		t.Fatalf("control: with every limit named the turn did not run: %v", err)
	}
}
