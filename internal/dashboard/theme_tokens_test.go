package dashboard

import (
	"sort"
	"strings"
	"testing"
)

// THE DARK THEME OVERRIDES EVERY COLOUR THE LIGHT ROOT DEFINES. The
// browser test can only show that a token RESOLVES, and a token missing
// from the dark block still resolves — to the light value, which is
// exactly the defect: one light surface in a dark page. So the token sets
// are compared statically, in both files. In tokens.css every colour role
// of :root is redefined by the dark theme. In theme.css, whose names are
// aliases of the roles, what does not follow a role by construction (a
// value with no var() in it: the per-theme rgb of a colour for the
// translucent tints, a shadow) is redefined by its dark block, and the
// dark block defines nothing :root does not. Type, space and shape, and
// the few values that are the same in both themes (the decoration's names,
// which are nothing in both), stay in :root alone.
func TestTheDarkThemeOverridesEveryColourToken(t *testing.T) {
	read := func(name string) string {
		raw, err := staticFS.ReadFile("static/" + name)
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}

	tokens := read("tokens/tokens.css")
	roots, dark := tokenBlock(t, tokens, ":root {"), tokenBlock(t, tokens, `:root[data-theme="dark"] {`)
	var missing []string
	colours := 0
	for name, value := range roots {
		if strings.HasPrefix(value, "#") {
			colours++
			if _, ok := dark[name]; !ok {
				missing = append(missing, name)
			}
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("tokens.css: the dark theme leaves these roles at their light values: %v", missing)
	}
	if colours < 14 {
		t.Fatalf("tokens.css: expected the full set of colour roles in :root, found %d", colours)
	}

	theme := read("theme.css")
	roots, dark = tokenBlock(t, theme, ":root {"), tokenBlock(t, theme, `:root[data-theme="dark"] {`)
	same := map[string]bool{"--white-rgb": true, "--glow": true, "--sky": true, "--stars": true}
	missing = nil
	var extra []string
	for name, value := range roots {
		if !strings.Contains(value, "var(") && !same[name] {
			if _, ok := dark[name]; !ok {
				missing = append(missing, name)
			}
		}
	}
	for name := range dark {
		if _, ok := roots[name]; !ok {
			extra = append(extra, name)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing) > 0 {
		t.Errorf("theme.css: the dark theme leaves these tokens at their light values: %v", missing)
	}
	if len(extra) > 0 {
		t.Errorf("theme.css: the dark theme defines tokens :root does not: %v (add them to :root so both themes carry them)", extra)
	}
	if len(roots) < 30 {
		t.Fatalf("theme.css: expected every old name in :root, found %d", len(roots))
	}
}
