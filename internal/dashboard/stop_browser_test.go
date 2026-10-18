//go:build !windows

package dashboard

import (
	"strings"
	"testing"
)

const stopControlPage = `<!doctype html>
<style>__LAYOUT_CSS__</style>
<div class="app">
  <div class="presence"><div class="orb-wrap"><div class="orb" id="orb"></div></div>
    <span id="p-name"></span><span id="p-state"></span>
    <span class="pill" id="pill-mode"></span></div>
  <div class="thread" id="thread"><div class="thread-inner" id="thread-inner"></div></div>
  <div class="composer-wrap" id="composer-wrap"><div class="composer">
    <button class="mic" id="mic" disabled>m</button>
    <textarea id="msg-input" rows="1"></textarea>
    <button class="sendb" id="send-btn" title="Send">&#10148;</button>
  </div></div>
</div>
<script type="module">
import { assert, run } from './__harness.js';
import { setThinking } from './presence.js';
import { sent, setConnected } from './ws.js';
import { S } from './state.js';
import './views/chat.js';

run(() => {
  const btn = document.getElementById('send-btn');
  const input = document.getElementById('msg-input');

  assert(typeof btn.onclick === 'function', 'no handler is bound to the send control');

  input.value = 'hello there';
  btn.click();
  assert(sent.length === 1, 'an idle click sent ' + sent.length + ' messages, want 1');
  assert(sent[0].type === 'chat', 'an idle click sent ' + sent[0].type + ', want chat');
  assert(sent[0].message === 'hello there', 'the typed text did not travel: ' + JSON.stringify(sent[0]));

  assert(input.value === '', 'sending did not clear the submitted draft');
  assert(S.thinking && btn.classList.contains('stopping') && /stop/i.test(btn.getAttribute('aria-label')),
    'the same button did not become Stop immediately while awaiting a reply');
  assert(btn.textContent.trim() === '■', 'Stop does not show the square motif');

  input.value = 'keep this draft'; btn.click();
  assert(sent.length === 2 && sent[1].type === 'cancel' && input.value === 'keep this draft',
    'Stop sent the draft or discarded it');
  assert(S.thinking && btn.classList.contains('stopping'), 'a click falsely confirmed cancellation');
  S.chatAcknowledged('request-1', 'accepted');
  S.chatReceipt('request-1', {ref:{identity:'fixture',source:'recorded',incarnation:'disk',id:'recorded-1'}});
  assert(S.thinking && btn.classList.contains('stopping'), 'a recording receipt ended the pending response');
  assert(!document.querySelector('#chat-notice'), 'normal recording asks for attention');
  setThinking(false); // host cancellation/final-response path
  assert(!btn.classList.contains('stopping') && btn.title === 'Send' && btn.querySelector('svg'),
    'the button did not return to Send');

  let reconciliations = 0;
  S.reconcileTurn = () => { reconciliations++; };
  btn.click();
  assert(sent.length === 3 && sent[2].type === 'chat' && input.value === '', 'could not speak after stopping');
  S.chatAcknowledged('request-3', 'refused');
  assert(S.thinking && btn.classList.contains('stopping') && reconciliations === 1,
    'rejection ended the turn instead of requesting its state');
  assert(document.querySelector('[data-request-id="request-3"] .body').textContent === 'keep this draft',
    'refusal discarded the submitted text');
  setThinking(false); // the host probe says no turn is active
  assert(btn.title === 'Send', 'idle probe left Stop stuck');
  setThinking(true); // another turn remains active when a steer is refused
  input.value = 'rejected steer';
  input.dispatchEvent(new KeyboardEvent('keydown',{key:'Enter',bubbles:true}));
  S.chatReceipt('request-4', {error:{code:'CHAT_NOT_RECORDED',message:'steer queue full'}});
  S.chatAcknowledged('request-4', 'refused');
  assert(S.thinking && btn.classList.contains('stopping') && reconciliations === 2,
    'pre-record rejection hid Stop for the running turn');
  assert(document.querySelector('[data-request-id="request-4"] .body').textContent === 'rejected steer',
    'rejected steer is not recoverable');

  setThinking(true);
  input.value = 'steer by keyboard';
  input.dispatchEvent(new KeyboardEvent('keydown',{key:'Enter',bubbles:true,isComposing:true}));
  assert(sent.length === 4 && input.value === 'steer by keyboard', 'IME confirmation sent a draft');
  input.dispatchEvent(new KeyboardEvent('keydown',{key:'Enter',shiftKey:true,bubbles:true}));
  assert(sent.length === 4, 'Shift+Enter sent a draft');
  input.dispatchEvent(new KeyboardEvent('keydown',{key:'Enter',bubbles:true}));
  assert(sent.length === 5 && sent[4].type === 'chat' && input.value === '', 'Enter stopped steering');
  input.value = 'still a draft'; btn.click();
  assert(sent.length === 6 && sent[5].type === 'cancel' && input.value === 'still a draft', 'busy click did not stop');
  setThinking(false);
  setConnected(false); btn.click();
  assert(sent.length === 6 && input.value === 'still a draft' && !S.thinking && btn.title === 'Send',
    'failed send cleared the draft or invented a pending response');

});
</script>`

func TestStopControlPutsCancelOnTheWireInBrowser(t *testing.T) {
	read := func(name string) []byte {
		t.Helper()
		b, err := staticFS.ReadFile("static/" + name)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	layoutCSS := read("layout.css")
	page := strings.Replace(stopControlPage, "__LAYOUT_CSS__", string(layoutCSS), 1)
	if page == stopControlPage {
		t.Fatal("stylesheet placeholder not substituted — the page would assert against no CSS at all")
	}

	runPageInEngines(t, page, map[string][]byte{
		"/presence.js":           read("presence.js"),
		"/views/chat.js":         read("views/chat.js"),
		"/state.js":              read("state.js"),
		"/util.js":               read("util.js"),
		"/views/model-picker.js": []byte(`export function fillModelPicker() {}`),
		"/ws.js": []byte(`export const sent = [];
let connected = true;
export function setConnected(on) { connected = on; }
export function send(obj) { if(!connected)return ''; sent.push(obj); return 'request-'+sent.length; }
export function wsReady() { return connected; }
export function query() {}`),
		"/app.js": []byte(`export function toast() {}
export function go() {}
export function renderFirstbootVisibility() {}`),
	})
}
