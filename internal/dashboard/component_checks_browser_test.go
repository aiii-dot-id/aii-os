//go:build !windows

// Runs only through the unix browser-engine rig (browserengine_test.go,
// same tag) — this file calls its helpers, so it shares its build row.

package dashboard

import "testing"

// componentChecks is what every component test asks of what a component
// drew (spec section 6a): every control a person can reach has a role and
// a name. It reads the element's own markup, the role a tag has by itself
// or the one it is given, and the name from aria-label, aria-labelledby or
// its text, as an accessibility tree would.
const componentChecks = `
export const nameOf = el => ((el.getAttribute('aria-label')
  || (el.getAttribute('aria-labelledby') || '').split(' ').filter(Boolean).map(id => (document.getElementById(id) || {}).textContent || '').join(' ')
  || el.textContent || '')).trim();
export const roleOf = el => el.getAttribute('role')
  || ({ A: el.hasAttribute('href') ? 'link' : '', BUTTON: 'button', SELECT: 'combobox', TEXTAREA: 'textbox', SUMMARY: 'button', INPUT: 'textbox' })[el.tagName] || '';
export function everyControlNamed(root, assert) {
  const controls = [...root.querySelectorAll('a[href],button,input,select,textarea,summary,[role="tab"],[tabindex]')].filter(el => el.getClientRects().length);
  for (const el of controls) {
    assert(roleOf(el), 'a control has no role: ' + el.outerHTML.slice(0, 90));
    assert(nameOf(el), 'a control has no name: ' + el.outerHTML.slice(0, 90));
  }
  return controls.length;
}
`

// componentModules serves the checks, and each component's stylesheet with
// the tokens, so what a test measures is what the page draws.
func componentModules(t *testing.T, names ...string) map[string][]byte {
	t.Helper()
	m := map[string][]byte{"/__checks.js": []byte(componentChecks)}
	for _, n := range names {
		b, err := staticFS.ReadFile("static/components/" + n + "/" + n + ".css")
		if err != nil {
			t.Fatal(err)
		}
		m["/components/"+n+"/"+n+".css"] = b
	}
	return m
}

// componentPage is a page that loads the tokens and the named components'
// stylesheets, then runs body as a module.
func componentPage(body string, names ...string) string {
	links := `<link rel="stylesheet" href="/tokens/tokens.css">`
	for _, n := range names {
		links += `<link rel="stylesheet" href="/components/` + n + `/` + n + `.css">`
	}
	return `<!doctype html><html><head><meta name="viewport" content="width=device-width,initial-scale=1">` + links +
		`</head><body><div id="root"></div><script type="module">` + body + `</script></body></html>`
}
