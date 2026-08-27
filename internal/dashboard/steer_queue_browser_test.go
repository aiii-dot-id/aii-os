//go:build !windows

// Runs only through the unix browser-engine rig (browserengine_test.go,
// same tag) — this file calls its helpers, so it shares its build row.

package dashboard

import (
	"strings"
	"testing"
)

// Accepted is not heard.
//
// A message spoken into a running turn is taken immediately but does not
// reach the model until the next tool-call boundary — up to one tool call
// away, and if the turn ends first it waits for the next one. Until now
// the operator got a toast that faded and no way to tell those states
// apart, which is the confusion the queue exists to remove: it must fill
// when words are taken and EMPTY when they are heard.
const steerQueuePage = `<!doctype html>
<style>__LAYOUT_CSS__</style>
<div class="app">
  <div class="thread" id="thread"><div class="thread-inner" id="thread-inner"></div></div>
  <div class="composer-wrap" id="composer-wrap">
    <div class="steerq" id="steerq"></div>
    <div class="composer"><textarea id="msg-input" rows="1"></textarea>
      <button class="sendb" id="send-btn">&#10148;</button></div>
  </div>
</div>
<script type="module">
import { assert, run } from './__harness.js';
import { renderSteering } from './views/chat.js';

run(() => {
  const q = document.getElementById('steerq');
  const composer = document.querySelector('.composer');

  // 1. Empty is INVISIBLE, not an empty box. A permanent widget saying
  //    "nothing waiting" is furniture the operator learns to ignore.
  renderSteering([]);
  assert(q.getBoundingClientRect().height === 0,
    'the empty queue still occupies ' + q.getBoundingClientRect().height + 'px');

  // 2. Taken words are shown, verbatim, and counted.
  renderSteering(['stop, that file is already fixed', 'and check the other one']);
  const r = q.getBoundingClientRect();
  assert(r.height > 0, 'words are waiting and the operator cannot see them');
  assert(q.textContent.includes('stop, that file is already fixed'),
    'the operator cannot see WHICH words are waiting: ' + q.textContent);
  assert(q.textContent.includes('and check the other one'), 'only part of the queue is shown');
  assert(/2 messages waiting/.test(q.textContent), 'the count is wrong or missing: ' + q.textContent);
  assert(/next tool call/.test(q.textContent),
    'the queue does not say what it is waiting FOR — "waiting" alone reads as stuck');

  // 3. It sits above the composer and does not cover it: the operator
  //    must still be able to speak while words are pending.
  assert(r.bottom <= composer.getBoundingClientRect().top + 0.5,
    'the queue overlaps the composer: queue ends ' + r.bottom +
    ', composer starts ' + composer.getBoundingClientRect().top);

  // 4. Singular reads as singular — a count that says "1 messages" is
  //    the kind of seam that teaches an operator the UI is careless.
  renderSteering(['only one']);
  assert(/1 message waiting/.test(q.textContent) && !/1 messages/.test(q.textContent),
    'singular is mis-worded: ' + q.textContent);

  // 5. AND IT MUST EMPTY. This is the moment the identity actually heard
  //    them, and it is the whole reason the queue is shown at all.
  renderSteering([]);
  assert(q.getBoundingClientRect().height === 0,
    'the queue did not clear when the words were delivered — it would read as never heard');
  assert(q.textContent.trim() === '', 'stale text survived the clear: ' + q.textContent);
});
</script>`

func TestSteeringQueueFillsAndEmptiesInBrowser(t *testing.T) {
	read := func(name string) []byte {
		t.Helper()
		b, err := staticFS.ReadFile("static/" + name)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	page := strings.Replace(steerQueuePage, "__LAYOUT_CSS__", string(read("layout.css")), 1)
	if page == steerQueuePage {
		t.Fatal("stylesheet placeholder not substituted — the page would assert against no CSS at all")
	}
	runPageInEngines(t, page, map[string][]byte{
		"/views/chat.js": read("views/chat.js"),
		"/state.js":      read("state.js"),
		"/util.js":       read("util.js"),
		"/ws.js": []byte(`export function send() { return true; }
export function wsReady() { return true; }
export function query() {}`),
		"/presence.js": []byte(`export function setThinking() {}
export function toolPulse() {}`),
		"/app.js":                []byte(`export function toast() {}`),
		"/views/model-picker.js": []byte(`export function fillModelPicker() {}`),
	})
}
