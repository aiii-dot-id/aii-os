//go:build !windows

package dashboard

import (
	"strings"
	"testing"
)

// .
// .
// .
// .
// .
func TestConverseControlShowsItsState(t *testing.T) {
	css, err := staticFS.ReadFile("static/layout.css")
	if err != nil {
		t.Fatal(err)
	}
	for _, rule := range []string{".converse {", ".converse.waiting {", ".converse.live {", "@keyframes converse-pulse", ".converse.live { transform:none; animation:none; }"} {
		if !strings.Contains(string(css), rule) {
			t.Fatalf("the stylesheet gives the control its state: missing %q", rule)
		}
	}
	page := `<!doctype html>
<div id="voice-transcript" hidden></div>
<button class="converse" id="converse" disabled hidden aria-pressed="false">&#127908;</button>
<script type="module">
import { converseForTest } from './voice.js';
import { S } from './state.js';
import { assert, run } from './__harness.js';
run(() => {
  S.stats = { voice_engine: true, voice_state: 'plugin' }; S.connected = true;
  const c = document.getElementById('converse');
  converseForTest('idle');
  assert(!c.hidden && !c.disabled && !c.classList.contains('live') && !c.classList.contains('waiting') && c.dataset.glyph === 'mic' && c.querySelector('svg') && c.getAttribute('aria-pressed') === 'false', 'idle is a microphone: ' + c.outerHTML);
  converseForTest('waiting');
  assert(c.classList.contains('waiting') && !c.classList.contains('live') && c.dataset.glyph === 'mic', 'waiting is marked, still a microphone: ' + c.outerHTML);
  converseForTest('live');
  assert(c.classList.contains('live') && !c.classList.contains('waiting') && c.dataset.glyph === 'stop' && c.querySelector('svg.stop') && c.getAttribute('aria-pressed') === 'true', 'live is a stop square, pressed: ' + c.outerHTML);
  assert(document.getElementById('voice-transcript').textContent === 'Listening…', 'and the line says listening');
  converseForTest('idle');
  assert(!c.classList.contains('live') && c.dataset.glyph === 'mic' && document.getElementById('voice-transcript').hidden, 'back to idle: ' + c.outerHTML);
});
</script>`
	runPageInEngines(t, page, map[string][]byte{
		"/app.js":   []byte("export function toast(m) {}\n"),
		"/state.js": []byte("export const S = { stats: null, connected: false };\n"),
	})
}
