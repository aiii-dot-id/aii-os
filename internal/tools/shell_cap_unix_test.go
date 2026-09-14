//go:build !windows

package tools

import (
	"context"
	"strings"
	"testing"
)

// .
// .
// .
// .
func TestShellOutputIsCappedWithTypedTruncation(t *testing.T) {
	r := NewRegistry(t.TempDir(), nil, Timeouts{})
	res, err := r.Execute(context.Background(), "shell", map[string]interface{}{
		"command": `i=0; while [ $i -lt 8000 ]; do echo xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx; i=$((i+1)); done`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Truncated {
		t.Fatal("a ~480KB output was not reported truncated")
	}
	if len(res.Output) > shellOutputCap+256 {
		t.Fatalf("output length %d exceeds the cap — retention is not bounded", len(res.Output))
	}
	if !strings.Contains(res.Output, "capped") || !strings.Contains(res.Output, "discarded") {
		t.Fatal("the cap must say what it did and how much it dropped")
	}
	if !strings.HasPrefix(res.Output, "xxxxxxxxxx") {
		t.Fatal("the HEAD of the output must survive — that is where the diagnostic lives")
	}
}
