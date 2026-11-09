package dashboard

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// tokenBlock returns the declarations of the block opened by open in css,
// as name to value. A block that is not there fails the test.
func tokenBlock(t *testing.T, css, open string) map[string]string {
	t.Helper()
	i := strings.Index(css, open)
	if i < 0 {
		t.Fatalf("no %q block", open)
	}
	body := css[i+len(open):]
	end := strings.Index(body, "}")
	if end < 0 {
		t.Fatalf("the %q block is not closed", open)
	}
	out := map[string]string{}
	for _, m := range regexp.MustCompile(`(--[a-z0-9-]+)\s*:\s*([^;]+);`).FindAllStringSubmatch(body[:end], -1) {
		out[m[1]] = strings.TrimSpace(m[2])
	}
	return out
}

// resolveTokens follows var(--x) through the merged map until every value
// is a literal, failing on a reference to nothing or a cycle.
func resolveTokens(t *testing.T, tokens map[string]string) map[string]string {
	t.Helper()
	ref := regexp.MustCompile(`^var\(\s*(--[a-z0-9-]+)\s*\)$`)
	out := map[string]string{}
	for name, value := range tokens {
		seen := map[string]bool{name: true}
		for {
			m := ref.FindStringSubmatch(value)
			if m == nil {
				break
			}
			next, ok := tokens[m[1]]
			if !ok || seen[m[1]] {
				t.Fatalf("%s refers to %s, which is undefined or a cycle", name, m[1])
			}
			seen[m[1]] = true
			value = next
		}
		out[name] = value
	}
	return out
}

func merged(layers ...map[string]string) map[string]string {
	out := map[string]string{}
	for _, l := range layers {
		for k, v := range l {
			out[k] = v
		}
	}
	return out
}

// luminance is WCAG 2's relative luminance of a #rrggbb colour.
func luminance(t *testing.T, hex string) float64 {
	t.Helper()
	if len(hex) != 7 || hex[0] != '#' {
		t.Fatalf("%q is not a #rrggbb colour", hex)
	}
	var c [3]float64
	for i := range c {
		v, err := strconv.ParseUint(hex[1+2*i:3+2*i], 16, 8)
		if err != nil {
			t.Fatalf("%q: %v", hex, err)
		}
		s := float64(v) / 255
		if s <= 0.03928 {
			c[i] = s / 12.92
		} else {
			c[i] = math.Pow((s+0.055)/1.055, 2.4)
		}
	}
	return 0.2126*c[0] + 0.7152*c[1] + 0.0722*c[2]
}

func contrast(t *testing.T, a, b string) float64 {
	la, lb := luminance(t, a), luminance(t, b)
	return (math.Max(la, lb) + 0.05) / (math.Min(la, lb) + 0.05)
}

// EVERY TEXT ROLE MEETS 4.5:1 ON EVERY SURFACE, AND A CONTROL'S EDGE 3:1
// (WCAG 1.4.3, 1.4.11; spec sections 4, 5c and 6a), in the light theme,
// the dark one, and high contrast over each. Read from the shipped
// tokens.css itself, so a colour cannot change without being checked. The
// dark theme is written twice, for the system's choice and the page's
// own switch, and the two must agree.
//
// guard:tree
func TestTokensMeetContrast(t *testing.T) {
	raw, err := staticFS.ReadFile("static/tokens/tokens.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(raw)
	light := tokenBlock(t, css, ":root {")
	darkSystem := tokenBlock(t, css, `:root:not([data-theme="light"]) {`)
	darkSwitch := tokenBlock(t, css, `:root[data-theme="dark"] {`)
	high := tokenBlock(t, css, `:root[data-contrast="high"] {`)
	if fmt.Sprint(darkSystem) != fmt.Sprint(darkSwitch) {
		t.Errorf("the dark theme differs between the system's choice and the page's switch:\n%v\n%v", darkSystem, darkSwitch)
	}

	texts := []string{"--text", "--text-2", "--text-3", "--accent", "--good", "--warn", "--bad"}
	surfaces := []string{"--bg", "--surface", "--surface-2"}
	themes := []struct {
		name   string
		tokens map[string]string
	}{
		{"light", light},
		{"dark", merged(light, darkSwitch)},
		{"light, high contrast", merged(light, high)},
		{"dark, high contrast", merged(light, darkSwitch, high)},
	}
	for _, theme := range themes {
		c := resolveTokens(t, theme.tokens)
		for _, s := range surfaces {
			for _, f := range texts {
				if r := contrast(t, c[f], c[s]); r < 4.5 {
					t.Errorf("%s: %s (%s) on %s (%s) is %.2f:1, under 4.5:1", theme.name, f, c[f], s, c[s], r)
				}
			}
			if r := contrast(t, c["--control"], c[s]); r < 3 {
				t.Errorf("%s: --control (%s) on %s (%s) is %.2f:1, under 3:1", theme.name, c["--control"], s, c[s], r)
			}
		}
		if r := contrast(t, c["--on-accent"], c["--accent"]); r < 4.5 {
			t.Errorf("%s: --on-accent (%s) on --accent (%s) is %.2f:1, under 4.5:1", theme.name, c["--on-accent"], c["--accent"], r)
		}
	}
}
