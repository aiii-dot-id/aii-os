package dashboard

import "testing"

// .
// .
// .
// .
// .
func TestDeclaredThemeTokensReadsShippedStylesheet(t *testing.T) {
	got := DeclaredThemeTokens()
	if len(got) == 0 {
		t.Fatal("the shipped theme.css must yield a vocabulary; empty disables inert detection entirely")
	}
	// .
	// .
	// .
	for _, name := range []string{"--acc", "--bg0", "--txt", "--line", "--grad", "--font"} {
		if !got[name] {
			t.Errorf("theme.css declares %s but the vocabulary does not contain it", name)
		}
	}
	if got["--acent"] {
		t.Error("a token theme.css does not declare must not appear declared — inert detection would never fire")
	}
}

// .
// .
// .
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
