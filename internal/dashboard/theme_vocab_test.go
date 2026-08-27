package dashboard

import "testing"

// TestDeclaredThemeTokensReadsShippedStylesheet proves the vocabulary
// comes from the bytes that actually ship, not from a mirrored list.
// If theme.css loses its :root block or the embed pattern stops
// covering it, this fails here rather than degrading into a silent
// "every token is inert" (or, worse, "nothing is ever inert").
func TestDeclaredThemeTokensReadsShippedStylesheet(t *testing.T) {
	got := DeclaredThemeTokens()
	if len(got) == 0 {
		t.Fatal("the shipped theme.css must yield a vocabulary; empty disables inert detection entirely")
	}
	// Load-bearing names spread across the block, including one whose
	// value contains var() and one whose value contains commas and
	// quotes — the parser must survive both.
	for _, name := range []string{"--acc", "--bg0", "--txt", "--line", "--grad", "--font"} {
		if !got[name] {
			t.Errorf("theme.css declares %s but the vocabulary does not contain it", name)
		}
	}
	if got["--acent"] {
		t.Error("a token theme.css does not declare must not appear declared — inert detection would never fire")
	}
}

// TestDeclaredTokensIgnoresCommentsAndValues pins the three ways a
// naive scan gets this wrong: commentary that mentions a token, a
// var() reference inside a value, and declarations outside :root.
func TestDeclaredTokensIgnoresCommentsAndValues(t *testing.T) {
	css := `
/* --commented:#fff; a note about --alsocommented */
:root {
  --real:#fff;
  --grad:linear-gradient(94deg,var(--nested) 0%,var(--real) 100%);
  --font:-apple-system,"Segoe UI",sans-serif;
}
.panel { --outside:1; }
`
	got := declaredTokensIn(css)
	for _, want := range []string{"--real", "--grad", "--font"} {
		if !got[want] {
			t.Errorf("%s is declared in :root and must be found", want)
		}
	}
	for _, reject := range []string{"--commented", "--alsocommented", "--nested", "--outside"} {
		if got[reject] {
			t.Errorf("%s is not a :root declaration and must not be in the vocabulary", reject)
		}
	}
}
