package dashboard

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestTheComponentSetsViewNamesNoPlugin(t *testing.T) {
	raw, err := os.ReadFile("static/views/plugins.js")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	for _, fn := range []string{"function componentSetsHTML(", "function servingSelection(", "function runtimeLimitsHTML(",
		"function needsOf(", "function readinessHTML(", "function pendingHTML(", "function checksHTML(", "function probesHTML(",
		"function machineHTML(", "function wireMachine("} {
		start := strings.Index(src, fn)
		if start < 0 {
			t.Fatalf("%s is gone", fn)
		}
		end := strings.Index(src[start:], "\n}\n")
		if end < 0 {
			t.Fatalf("%s has no end", fn)
		}
		if m := regexp.MustCompile(`(?i)voice|speech|\bmeaning\b|github|hugging ?face|'full'|'small'`).FindString(src[start : start+end]); m != "" {
			t.Errorf("%s names %q", fn, m)
		}
	}
}
