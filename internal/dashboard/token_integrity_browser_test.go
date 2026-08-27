//go:build !windows

// Runs only through the unix browser-engine rig (browserengine_test.go,
// same tag) — this file calls its helpers, so it shares its build row.

package dashboard

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

/*
Two tests about ONE defect class: a token reference that resolves to
nothing. Nothing crashes, nothing logs, and the styling silently falls
back to whatever was inherited. This is the same shape as the inert
`tablet` profile and the witness-state collapse — a failure that is
silent because nothing is watching, not because nothing is wrong.

The browser test establishes the SEMANTICS empirically rather than from
a reading of the spec: what does an undefined custom property actually
do to an inherited property, in each engine we ship on. The static test
then uses that semantics to police the real stylesheet.

Deliberately NOT a lint on naming or ordering. The only thing asserted
is the property that has a consequence: every var() reference either
resolves, or carries a fallback, or is set at runtime.
*/

const tokenIntegrityPage = `<!doctype html>
<html><head><style>
  :root { --faint: rgb(97, 96, 135); --txt: rgb(236, 234, 248); }
  /* The exact shape of views/settings.js skipsHTML(): a faint parent
     with an emphasised child. */
  #row     { color: var(--faint); }
  #phantom { color: var(--nope); }
  #withfb  { color: var(--nope, rgb(1, 2, 3)); }
  #real    { color: var(--txt); }
</style></head>
<body>
<div id="row">
  skipped <b id="phantom">phantom</b> <b id="withfb">fallback</b> <b id="real">real</b>
</div>
<script type="module">
import { assert, run } from './__harness.js';

const colorOf = id => getComputedStyle(document.getElementById(id)).color;

run(() => {
  // 1. Baseline: the parent's own token resolves.
  assert(colorOf('row') === 'rgb(97, 96, 135)', 'parent colour wrong: ' + colorOf('row'));

  // 2. THE DEFECT SEMANTICS. An undefined custom property makes the
  //    declaration invalid at computed-value time. For an INHERITED
  //    property like color that is not "ignore the declaration" — it is
  //    "inherit". So the emphasis silently becomes its parent, and the
  //    contrast the author intended is lost with no error anywhere.
  assert(colorOf('phantom') === colorOf('row'),
    'undefined token did not fall back to inherited: ' + colorOf('phantom'));

  // 3. A fallback makes the same reference safe. This is why var(--bg2,#111)
  //    in settings.js is defensible and var(--fg) is not.
  assert(colorOf('withfb') === 'rgb(1, 2, 3)', 'fallback not used: ' + colorOf('withfb'));

  // 4. A defined token gives the emphasis the author wanted — visibly
  //    different from the parent.
  assert(colorOf('real') === 'rgb(236, 234, 248)', 'real token wrong: ' + colorOf('real'));
  assert(colorOf('real') !== colorOf('row'), 'emphasis indistinguishable from parent');
});
</script>
</body></html>`

func TestUndefinedTokenSemanticsInBrowsers(t *testing.T) {
	runPageInEngines(t, tokenIntegrityPage, nil)
}

var (
	reTokenDecl = regexp.MustCompile(`(--[A-Za-z0-9-]+)\s*:`)
	// A var() reference WITHOUT a fallback: no comma before the close.
	reVarNoFallback = regexp.MustCompile(`var\(\s*(--[A-Za-z0-9-]+)\s*\)`)
)

// TestEveryTokenReferenceResolves is the static half. Every var(--x) with
// no fallback must name a token that is actually declared somewhere the
// browser will see — theme.css, any stylesheet, or an inline style set by
// JS at runtime (the per-project --hue is set that way, and is correct).
//
// A reference with a fallback is exempt by construction: it has already
// declared what to do when the token is missing.
func TestEveryTokenReferenceResolves(t *testing.T) {
	root := filepath.Join("static")
	declared := map[string]bool{}
	type ref struct{ token, file string }
	var refs []ref

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		switch filepath.Ext(path) {
		case ".css", ".js", ".html":
		default:
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		text := string(body)
		// Declarations: in a stylesheet block, or inside an inline
		// style="--hue:220" written by JS. Both reach the browser.
		for _, m := range reTokenDecl.FindAllStringSubmatch(text, -1) {
			declared[m[1]] = true
		}
		for _, m := range reVarNoFallback.FindAllStringSubmatch(text, -1) {
			refs = append(refs, ref{token: m[1], file: path})
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(declared) == 0 || len(refs) == 0 {
		t.Fatalf("scan found nothing: %d declared, %d refs", len(declared), len(refs))
	}

	var bad []string
	for _, r := range refs {
		if !declared[r.token] {
			bad = append(bad, r.token+" referenced in "+r.file+" is declared nowhere")
		}
	}
	sort.Strings(bad)
	if len(bad) > 0 {
		t.Fatalf("token reference resolves to nothing (silently inherits):\n  %s",
			strings.Join(bad, "\n  "))
	}
}
