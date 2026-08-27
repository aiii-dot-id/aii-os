//go:build !windows

// Runs only through the unix browser-engine rig (browserengine_test.go,
// same tag) — this file calls its helpers, so it shares its build row.

package dashboard

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// TestFrameCSPIsServedOnTheFrame proves the policy reaches the wire on
// a STARTED server through the real mux, for every frame asset type.
//
// A CSP that exists only as a constant is the failure mode this repo
// keeps finding: text that nothing executes. The constant is asserted
// here against what the handler actually emits, so the two cannot drift.
func TestUICSPReachesTheWire(t *testing.T) {
	s := New("127.0.0.1", 0, &WSHandler{IdentityName: "X"})
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())

	for _, p := range []string{"/", "/app.js", "/theme.css", "/layout.css"} {
		resp, err := testClient.Get("https://" + addr + p)
		if err != nil {
			t.Fatalf("GET %s: %v", p, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s: status %d", p, resp.StatusCode)
		}
		if got := resp.Header.Get("Content-Security-Policy"); got != uiCSP {
			t.Errorf("GET %s: CSP header\n got: %q\nwant: %q", p, got, uiCSP)
		}
		if got := resp.Header.Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("GET %s: nosniff header = %q", p, got)
		}
	}
}

// TestFrameCSPDirectives pins the properties that make the policy worth
// having. These are asserted by NAME rather than by comparing the whole
// string, so that adding a directive does not fail the test but WEAKENING
// one does.
func TestUICSPDirectives(t *testing.T) {
	// The two that carry the security value. script-src without
	// 'unsafe-inline' is what makes an injected event handler inert;
	// style-src-elem without it is what makes an injected <style> inert.
	if !strings.Contains(uiCSP, "script-src 'self';") {
		t.Error("script-src must be exactly 'self' — no inline, no eval")
	}
	if !strings.Contains(uiCSP, "style-src-elem 'self';") {
		t.Error("style-src-elem must be 'self' — an injected <style> must not apply")
	}
	// 'unsafe-inline' is permitted for style ATTRIBUTES only. If it ever
	// appears in script-src or style-src-elem the policy is decorative.
	for _, directive := range strings.Split(uiCSP, "; ") {
		name, value, _ := strings.Cut(directive, " ")
		if name == "style-src" || name == "style-src-attr" {
			continue // the measured concession: 54 inline style attributes
		}
		if strings.Contains(value, "unsafe") {
			t.Errorf("directive %q carries an unsafe- keyword outside the style-attribute concession", directive)
		}
	}
	if !strings.Contains(uiCSP, "default-src 'none'") {
		t.Error("default-src must be 'none' so an unlisted fetch type fails closed")
	}
}

// cspProbePage exercises the policy's actual behaviour in a real engine.
// Structural review cannot tell me whether style-src-elem is honoured;
// only an engine can.
const cspProbePage = `<!DOCTYPE html><html><head><title>csp</title></head>
<body><div id="host"></div>
<script type="module" src="/probe.js"></script>
</body></html>`

const cspProbeJS = `
import { run, assert } from '/__harness.js';

run(() => {
  const host = document.getElementById('host');
  const colorOf = id => getComputedStyle(document.getElementById(id)).color;

  // PERMITTED: an inline style ATTRIBUTE arriving via innerHTML. This is
  // how all 54 of the shipped ones arrive; if the policy blocks it the
  // frame renders wrong, so this assertion is the breakage canary.
  host.innerHTML = '<div id="attr" style="color:rgb(1,2,3)">x</div>';
  assert(colorOf('attr') === 'rgb(1, 2, 3)',
         'inline style attribute was blocked: ' + colorOf('attr'));

  // PERMITTED: CSSOM setProperty — the exact mechanism theme.js uses to
  // apply theme.json. If CSP blocked this the whole theme tier would die
  // silently under the new header.
  document.documentElement.style.setProperty('--probe', 'rgb(4,5,6)');
  host.insertAdjacentHTML('beforeend',
    '<div id="tok" style="color:var(--probe)">y</div>');
  assert(colorOf('tok') === 'rgb(4, 5, 6)',
         'CSSOM setProperty was blocked: ' + colorOf('tok'));

  // REFUSED: an injected <style> ELEMENT. This is the security win that
  // style-src-elem 'self' buys, and it is only affordable because nothing
  // in the frame legitimately creates one.
  const st = document.createElement('style');
  st.textContent = '#victim { color: rgb(9,9,9) !important; }';
  document.head.appendChild(st);
  host.insertAdjacentHTML('beforeend', '<div id="victim">z</div>');
  assert(colorOf('victim') !== 'rgb(9, 9, 9)',
         'an injected <style> element APPLIED — style-src-elem is not holding');

  // REFUSED: an inline event handler. script-src has no 'unsafe-inline',
  // so a handler smuggled through innerHTML must never run.
  window.__cspHandlerFired = false;
  host.insertAdjacentHTML('beforeend',
    '<div id="ev" onclick="window.__cspHandlerFired = true">c</div>');
  document.getElementById('ev').click();
  assert(window.__cspHandlerFired === false,
         'an inline event handler RAN — script-src is not holding');
});
`

// TestFrameCSPSemanticsInBrowsers runs the probe under the REAL uiCSP
// constant, delivered as a real header, in every installed engine.
func TestUICSPSemanticsInBrowsers(t *testing.T) {
	runPageInEnginesWithHeaders(t, cspProbePage,
		map[string][]byte{"/probe.js": []byte(cspProbeJS)},
		map[string]string{"Content-Security-Policy": uiCSP})
}
