//go:build !windows

package tools

import (
	"context"
	"strings"
	"testing"
	"time"
)

// .
// .
// .
// .
// .
// .
func TestShellReportsACleanExitWhenAChildStillHoldsTheOutput(t *testing.T) {
	st := &ShellTool{timeout: 20 * time.Second, sandbox: t.TempDir()}
	start := time.Now()
	res, err := st.Execute(context.Background(), map[string]interface{}{
		"command": `sleep 4 & echo started; exit 0`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Error != "" {
		t.Fatalf("a clean exit is not an error: %q", res.Error)
	}
	for _, want := range []string{"started", "[exit status 0; ", "still held"} {
		if !strings.Contains(res.Output, want) {
			t.Fatalf("want %q in the report, got %q", want, res.Output)
		}
	}
	if elapsed := time.Since(start); elapsed > 8*time.Second {
		t.Fatalf("the call waited for the orphan: %s", elapsed)
	}
	// .
	res, err = st.Execute(context.Background(), map[string]interface{}{
		"command": `sleep 4 & echo failing; exit 3`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Error != "" || !strings.Contains(res.Output, "[exit status 3]") {
		t.Fatalf("a non-zero exit reports its status beside the output: err=%q out=%q", res.Error, res.Output)
	}
}
