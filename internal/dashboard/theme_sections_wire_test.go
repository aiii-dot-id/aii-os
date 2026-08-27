package dashboard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

/*
The static half of the theme/section join. TestThemeTokensAreInvisibleToRuleWalk
proves in both engines WHY this wiring is needed; this proves the wiring
is actually there.

Not a browser test on purpose: sections.js imports ws.js, app.js,
bridge.js and panel.js, so loading it in a bare page exercises the
module graph rather than the fix. What can drift here is the wiring
itself, and wiring is visible in the source.

Each assertion below corresponds to a way this can silently regress —
and "silently" is the operative word: every failure mode here leaves
the frame looking perfect while sections hold a stale palette.
*/
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

	// 1. The collector must read the INLINE custom properties of the
	//    root element. Without this, a section handshaking after a
	//    theme edit is handed the compiled defaults — the rule walk
	//    cannot see what setProperty wrote.
	if !strings.Contains(sections, "document.documentElement.style") {
		t.Error("collectTokens no longer reads documentElement.style: " +
			"themed tokens are invisible to a stylesheet rule walk, so sections " +
			"would receive the compiled defaults with nothing logged")
	}

	// 2. Overlay ORDER. The inline read must come after the rule walk,
	//    because that is the order the frame's own cascade resolves.
	//    Reversed, the compiled default would win and the whole fix
	//    would be inert while still looking present.
	ruleWalk := strings.Index(sections, "document.styleSheets")
	inline := strings.Index(sections, "document.documentElement.style")
	if ruleWalk < 0 || inline < 0 || inline < ruleWalk {
		t.Errorf("inline token overlay must follow the rule walk (rules at %d, inline at %d)", ruleWalk, inline)
	}

	// 3. Sections mounted BEFORE the edit only learn by being told.
	if !strings.Contains(sections, "export function onTokensChanged") {
		t.Error("sections.js no longer exports onTokensChanged: already-mounted " +
			"sections would keep their palette for the life of the mount")
	}

	// 4. pushTokens was a seam with no caller for two tiers. If it goes
	//    callerless again, propagation is dead and nothing fails.
	if !strings.Contains(sections, "pushTokens") {
		t.Error("nothing in sections.js calls bridge.pushTokens; the propagation seam is dead again")
	}
	if !strings.Contains(bridge, "function pushTokens") {
		t.Error("bridge.js no longer defines pushTokens")
	}

	// 5. ws.js is the dispatcher that owns ordering: apply, then
	//    propagate. If onTokensChanged ran first, every section would
	//    be handed the palette it already had — a call that looks
	//    correct in a diff and does nothing at runtime.
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
