package dashboard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// .
// .
// .
// .

// .
// .
// .
// .

// .
// .
// .
// .
func TestThemePropagatesToSections(t *testing.T) {
	read := func(name string) string {
		body, err := os.ReadFile(filepath.Join("static", name))
		if err != nil {
			t.Fatal(err)
		}
		return string(body)
	}
	sections := read("sections.js")
	ws := read("ws.js")
	bridge := read("bridge.js")

	// .
	// .
	// .
	// .
	if !strings.Contains(sections, "document.documentElement.style") {
		t.Error("collectTokens no longer reads documentElement.style: " +
			"themed tokens are invisible to a stylesheet rule walk, so sections " +
			"would receive the compiled defaults with nothing logged")
	}

	// .
	// .
	// .
	// .
	ruleWalk := strings.Index(sections, "document.styleSheets")
	inline := strings.Index(sections, "document.documentElement.style")
	if ruleWalk < 0 || inline < 0 || inline < ruleWalk {
		t.Errorf("inline token overlay must follow the rule walk (rules at %d, inline at %d)", ruleWalk, inline)
	}

	// .
	if !strings.Contains(sections, "export function onTokensChanged") {
		t.Error("sections.js no longer exports onTokensChanged: already-mounted " +
			"sections would keep their palette for the life of the mount")
	}

	// .
	// .
	if !strings.Contains(sections, "pushTokens") {
		t.Error("nothing in sections.js calls bridge.pushTokens; the propagation seam is dead again")
	}
	if !strings.Contains(bridge, "function pushTokens") {
		t.Error("bridge.js no longer defines pushTokens")
	}

	// .
	// .
	// .
	// .
	if !strings.Contains(ws, "onTokensChanged") {
		t.Fatal("ws.js never calls onTokensChanged; a theme change reaches the frame only")
	}
	line := ""
	for _, l := range strings.Split(ws, "\n") {
		if strings.Contains(l, "case 'theme'") {
			line = l
			break
		}
	}
	if line == "" {
		t.Fatal("ws.js has no theme case")
	}
	applied := strings.Index(line, "onTheme(")
	propagated := strings.Index(line, "onTokensChanged")
	if applied < 0 || propagated < 0 {
		t.Fatalf("theme case must apply then propagate, got: %s", strings.TrimSpace(line))
	}
	if propagated < applied {
		t.Errorf("onTokensChanged runs BEFORE onTheme applies; sections get the old palette: %s",
			strings.TrimSpace(line))
	}
}
