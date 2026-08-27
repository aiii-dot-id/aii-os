//go:build !windows

// Runs only through the unix browser-engine rig (browserengine_test.go,
// same tag) — this file calls its helpers, so it shares its build row.

package dashboard

import (
	"testing"
)

// The R74 token-required bit must reach RUNNING page code under the
// shipping policy, and the prompt flow must recover from a stale
// cookie. Both halves have already failed silently in text form: the
// first flag carrier was <script>window.AII_TOKEN_REQUIRED=true</script>,
// which uiCSP (script-src 'self') refused to execute in every enforcing
// engine — the flag existed in the bytes and never in the page, and the
// byte-grep test could not tell (D75). And a browser holding a REFUSED
// token was never re-asked, a silent forever reconnect loop (D76).
//
// This probe runs the REAL ws.js against a server that has no /ws at
// all (every handshake fails — the refused-token shape), under the real
// uiCSP header, with a stale cookie pre-stored and the old inline
// carrier planted in the page so its corpse stays a corpse. Real bytes,
// real engine, real policy: the regression cannot return wearing its
// old clothes.
const tokenPromptProbePage = `<!DOCTYPE html><html><head data-aii-token-required="1"><title>token</title>
<script>window.AII_TOKEN_REQUIRED = true</script>
</head><body>
<button id="send-btn">send</button>
<script type="module" src="/probe.js"></script>
</body></html>`

const tokenPromptProbeJS = `
import { report } from '/__harness.js';

const fail = m => report('FAIL: ' + m);
try {
  // A stale token is already stored, and the server refuses every
  // connect (the rig serves no /ws — the socket never once opens).
  document.cookie = 'aii_token=stale-and-refused; path=/';
  let asks = 0;
  window.prompt = () => { asks++; return 'fresh-from-the-console'; };

  const { connect } = await import('/ws.js');
  connect();

  // First refused connect: ask once, store the entry over the corpse.
  await new Promise(r => setTimeout(r, 1200));
  if (window.AII_TOKEN_REQUIRED !== undefined)
    fail('the inline <script> carrier RAN under uiCSP — the policy weakened (D75)');
  else if (asks !== 1)
    fail('after the first refused connect asks=' + asks + ' — a stale cookie must re-prompt (D76)');
  else if (!document.cookie.includes('aii_token=fresh-from-the-console'))
    fail('the entered token did not replace the stale cookie: ' + document.cookie);
  else {
    // Ride through the following reconnects (backoff, 1s floor, x2):
    // still refused, but the ask is once per page load — a prompt
    // loop is worse than the lockout.
    await new Promise(r => setTimeout(r, 2600));
    if (asks !== 1) fail('the reconnect asked again (asks=' + asks + ') — once per page load broke');
    else report('OK');
  }
} catch (e) { fail((e && e.message) || String(e)); }
`

// stubModule builds a module exporting inert functions under the given
// names — enough for ws.js's import graph to resolve without hauling
// the whole frame into the probe. state.js and util.js ship real.
func TestTokenPromptFlowUnderTheShippingCSP(t *testing.T) {
	real := func(p string) []byte {
		b, err := staticFS.ReadFile("static/" + p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		return b
	}
	modules := map[string][]byte{
		"/probe.js": []byte(tokenPromptProbeJS),
		"/ws.js":    real("ws.js"),
		"/state.js": real("state.js"),
		"/util.js":  real("util.js"),

		"/presence.js":       stubModule("renderPresence", "setThinking"),
		"/app.js":            stubModule("go", "renderFirstbootVisibility", "toast"),
		"/views/chat.js":     stubModule("addMsg", "sysLine", "toolEventLive", "thinkingEvent", "renderHistory", "renderChatSubstrate", "acceptSubstrateConfig", "rejectSubstrateConfig", "substrateConnectionLost", "renderSteering"),
		"/views/home.js":     stubModule("renderHome"),
		"/views/work.js":     stubModule("renderWorkPill"),
		"/views/projects.js": stubModule("renderProjPill", "renderProjects", "newlyCreatedID", "rejectCreate", "acceptCreate", "acceptFocusSave"),
		"/views/memory.js":   stubModule("renderMemory"),
		"/views/identity.js": stubModule("renderIdentity"),
		"/views/plugins.js":  stubModule("renderPlugins"),
		"/views/settings.js": stubModule("renderSettings", "acceptSettingsConfig", "rejectSettingsConfig", "acceptProviderSave", "rejectProviderSave", "settingsConnectionLost"),
		"/firstboot.js":      stubModule("renderProviderOptions", "setModelOptions", "fbHint", "fbResult", "acceptDiscoveryResponse", "firstbootConnectionLost"),
		"/sections.js":       stubModule("publish", "onSections", "onLayout", "onTokensChanged"),
		"/panel.js":          stubModule("renderPanel"),
		"/theme.js":          stubModule("onTheme"),
		"/overlay.js":        stubModule("onOverlayChanged"),
		"/voice.js":          stubModule("bindTransport", "render", "speak", "connectionLost"),
	}
	runPageInEnginesWithHeaders(t, tokenPromptProbePage, modules,
		map[string]string{"Content-Security-Policy": uiCSP})
}
