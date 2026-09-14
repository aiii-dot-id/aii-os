//go:build !windows

package dashboard

import "testing"

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
func TestSignInOpensThePopupInTheClickHandler(t *testing.T) {
	page := `<!doctype html><html><body>
<div id="st"><button data-prov-signin="ChatGPT (Plus/Pro)">Sign in</button></div>
<script type="module">
import { run } from '/__harness.js';
import { S } from '/state.js';
import { sent } from '/ws.js';
import { wireProviderSignIn } from '/views/settings.js';
run((assert) => {
  const opens = []; const nav = [];
  const fake = { closed: false, set location(u) { nav.push(u); } };
  window.open = (u) => { opens.push(u); return fake; };
  assert(typeof S.signInNavigate === 'function', 'settings registered S.signInNavigate');
  wireProviderSignIn(document.getElementById('st'));
  document.querySelector('[data-prov-signin]').click();
  assert(opens.length === 1 && opens[0] === '', 'one blank window opened in the click handler');
  assert(sent.length === 1 && sent[0].type === 'provider_signin' && sent[0].provider === 'ChatGPT (Plus/Pro)', 'the click then asks the server for the authorize URL');
  // The reply arrives later: the transport navigates the pre-opened window.
  S.signInNavigate('https://auth.example/authorize?state=s');
  assert(opens.length === 1, 'no second window was opened on the reply');
  assert(nav.length === 1 && nav[0].startsWith('https://auth.example/'), 'the pre-opened window was navigated to the authorize URL');
  // A failed sign-in start closes the window it opened.
  let closed = 0; const fake2 = { closed: false, close() { closed++; }, set location(u) { nav.push(u); } };
  window.open = (u) => { opens.push(u); return fake2; };
  document.querySelector('[data-prov-signin]').click();
  S.signInAbandon();
  assert(closed === 1, 'an abandoned sign-in closes the window it opened');
});
</script></body></html>`
	modules := map[string][]byte{
		"/state.js":              []byte(`export const S = {};`),
		"/util.js":               []byte(`export const $ = (id) => document.getElementById(id); export const esc = (s) => String(s);`),
		"/ws.js":                 []byte(`export const sent = []; export function send(m) { sent.push(m); } export function query() {}`),
		"/sandbox.js":            stubModule("sandboxCardHTML", "wireSandboxCard"),
		"/pending.js":            stubModule("pendingSlot"),
		"/views/model-picker.js": stubModule("providerModels"),
	}
	real, err := staticFS.ReadFile("static/views/settings.js")
	if err != nil {
		t.Fatal(err)
	}
	modules["/views/settings.js"] = real
	runPageInEngines(t, page, modules)
}
