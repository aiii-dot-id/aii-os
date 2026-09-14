package dashboard

import (
	"regexp"
	"sort"
	"strings"
	"testing"
)

// .
// .
// .
// .
// .
// .
func themeBlocks(t *testing.T) (dark, light map[string]bool) {
	t.Helper()
	css, err := staticFS.ReadFile("static/theme.css")
	if err != nil {
		t.Fatal(err)
	}
	block := func(open string) map[string]bool {
		i := strings.Index(string(css), open)
		if i < 0 {
			t.Fatalf("theme.css has no %q block", open)
		}
		body := string(css)[i+len(open):]
		body = body[:strings.Index(body, "\n}")]
		out := map[string]bool{}
		for _, m := range regexp.MustCompile(`(?m)(--[a-z0-9-]+)\s*:`).FindAllStringSubmatch(body, -1) {
			out[m[1]] = true
		}
		return out
	}
	return block(":root {"), block(`:root[data-theme="light"] {`)
}

func TestTheLightThemeOverridesEveryColourToken(t *testing.T) {
	dark, light := themeBlocks(t)
	neutral := map[string]bool{"--font": true, "--mono": true, "--r-lg": true, "--r-md": true, "--r-sm": true, "--white": true, "--grad": true, "--grad-hot": true}
	var missing, extra []string
	for name := range dark {
		if !neutral[name] && !light[name] {
			missing = append(missing, name)
		}
	}
	for name := range light {
		if !dark[name] {
			extra = append(extra, name)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing) > 0 {
		t.Errorf("the light theme leaves these tokens at their dark values: %v", missing)
	}
	if len(extra) > 0 {
		t.Errorf("the light theme defines tokens the dark root does not: %v (add them to :root so both themes carry them)", extra)
	}
	if len(dark) < 30 {
		t.Fatalf("expected the full token set in :root, found %d", len(dark))
	}
}
