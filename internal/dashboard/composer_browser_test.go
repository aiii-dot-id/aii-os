//go:build !windows

// .
// .

package dashboard

import (
	"strings"
	"testing"
)

// .
// .
func chatModuleRig(t *testing.T) map[string][]byte {
	t.Helper()
	chat, err := staticFS.ReadFile("static/views/chat.js")
	if err != nil {
		t.Fatal(err)
	}
	util, err := staticFS.ReadFile("static/util.js")
	if err != nil {
		t.Fatal(err)
	}
	return map[string][]byte{
		"/views/chat.js":         chat,
		"/state.js":              []byte(`export const S = { stats: null, identityExists: false, providers: [], config: null, providersLoaded: false, asks: [] };`),
		"/util.js":               util,
		"/ws.js":                 []byte(`export const frames = []; export function send(f) { frames.push(f); return 'req-' + frames.length; } export function wsReady() { return true; }`),
		"/presence.js":           []byte(`export function setThinking() {} export function toolPulse() {} export function renderPresence() {}`),
		"/app.js":                []byte(`export const toasts = []; export function toast(m) { toasts.push(m); }`),
		"/views/model-picker.js": []byte(`export function fillModelPicker() {}`),
		"/pending.js":            []byte(`export function pendingSlot() { return { arm(){ return true; }, claim(){ return false; }, drop(){}, waiting(){ return false; } }; }`),
	}
}

// .
// .
// .
// .
// .
// .
// .
// .
const composerKeepsPage = substratePageMarkup + `<script type="module">
import { S } from './state.js';
import { frames, setConnected } from './ws.js';
import { sendChat } from './views/chat.js';
import { assert, run } from './__harness.js';
run(() => {
  const input = document.getElementById('msg-input');
  const chats = () => frames.filter(f => f.type === 'chat');
  S.identityExists = true;
  setConnected(false);
  input.value = 'a paragraph typed while the socket was down';
  input.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }));
  assert(chats().length === 0, 'a message was sent on a dead socket');
  assert(input.value === 'a paragraph typed while the socket was down', 'the words were thrown away with the toast: ' + JSON.stringify(input.value));
  setConnected(true);
  input.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, isComposing: true }));
  assert(chats().length === 0 && input.value !== '', 'an IME confirming a candidate sent the message');
  input.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }));
  assert(chats().length === 1 && input.value === '', 'a plain Enter on a live socket did not send and clear: ' + chats().length + ' ' + JSON.stringify(input.value));
});
</script>`

func TestTheComposerKeepsWhatItCouldNotSend(t *testing.T) {
	runPageInEngines(t, composerKeepsPage, substrateModules(t))
}

func TestTheComposerFollowsTheIdentityAndTheActiveModel(t *testing.T) {
	page := `<!doctype html><html><body>
<div id="thread"><div id="thread-inner"></div></div>
<div id="effort" hidden><button id="effort-btn" aria-expanded="false">Effort · <b id="effort-val"></b></button><div id="effort-menu" hidden></div></div>
<textarea id="msg-input"></textarea><button id="send-btn"></button>
<button id="elsewhere">elsewhere</button>
<script type="module">
import { run } from '/__harness.js';
import { S } from '/state.js';
import { frames } from '/ws.js';
import { renderComposer } from '/views/chat.js';
import { toasts } from '/app.js';
run(async (assert) => {
  const wrap = document.getElementById('effort'), val = document.getElementById('effort-val');
  const menu = document.getElementById('effort-menu'), btn = document.getElementById('effort-btn');
  const input = document.getElementById('msg-input');
  const levels = () => [...menu.querySelectorAll('button')].map(b => b.dataset.level).join(',');
  const checked = () => { const c = menu.querySelector('[aria-checked="true"]'); return c ? c.dataset.level : null; };
  const own = (levels, in_force, checked) => ({ llm: { effort_choice: { levels, in_force, checked } } });

  S.identityExists = true;
  S.stats = { name: 'Willow' };
  S.config = own(['low', 'medium', 'high'], 'high');
  renderComposer();
  assert(!wrap.hidden, 'a model with its own levels is offered the control');
  assert(val.textContent === 'High', 'the label is the level in force, in words: ' + val.textContent);
  assert(levels() === ',low,medium,high', 'the menu is Default, then the model\'s own levels: ' + levels());
  assert(checked() === 'high', 'the level in force is the one checked');

  btn.click();
  assert(!menu.hidden && btn.getAttribute('aria-expanded') === 'true', 'the button opens the menu');
  menu.querySelector('[data-level="low"]').click();
  assert(menu.hidden, 'choosing closes the menu');
  const sent = frames.filter(f => f.type === 'effort_set');
  assert(sent.length === 1 && sent[0].effort === 'low', 'one choice sends one effort_set carrying the level: ' + JSON.stringify(frames));

  btn.click();
  menu.querySelector('[data-level=""]').click();
  assert(frames[frames.length - 1].effort === '', 'Default sends no level');

  S.config = own(['low', 'medium', 'high'], '');
  renderComposer();
  assert(val.textContent === 'Default' && checked() === '', 'nothing in force reads Default, with Default checked');

  btn.click();
  const focused = menu.querySelector('[data-level="medium"]');
  focused.focus();
  renderComposer();
  assert(!menu.hidden && document.activeElement === focused, 'a status push leaves an open menu and its focused item alone');
  document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }));
  assert(menu.hidden, 'Escape closes the menu');
  btn.click();
  document.getElementById('elsewhere').click();
  assert(menu.hidden, 'a click elsewhere closes the menu');

  S.config = own(['none', 'minimal', 'low', 'medium', 'high', 'xhigh', 'max'], 'medium');
  renderComposer();
  assert(levels() === ',none,minimal,low,medium,high,xhigh,max' && val.textContent === 'Medium', 'another model brings its own vocabulary: ' + levels());
  assert(menu.querySelector('[data-level="xhigh"]').textContent.indexOf('Extra High') === 0, 'xhigh reads Extra High, and still sends xhigh');
  assert(![...menu.querySelectorAll('button')].some(b => b.hasAttribute('data-checked')), 'a model\'s own levels are not marked as checked');

  S.config = own(['low', 'medium', 'high', 'xhigh'], 'high', ['low', 'medium', 'high', 'xhigh']);
  renderComposer();
  const borrowed = menu.querySelector('[data-level="xhigh"]');
  assert(!wrap.hidden && borrowed.hasAttribute('data-checked') && borrowed.title === 'Checked with the provider before it applies', 'a borrowed level is offered, and says it is checked first: ' + borrowed.outerHTML);
  assert(!menu.querySelector('[data-level=""]').hasAttribute('data-checked'), 'Default is never checked');
  btn.click();
  borrowed.click();
  assert(frames[frames.length - 1].type === 'effort_set' && frames[frames.length - 1].effort === 'xhigh', 'a borrowed level sends the same one narrow message');
  assert(toasts.some(m => m.indexOf('Checking Extra High') === 0), 'choosing a borrowed level does not say it is being checked: ' + toasts.join(' | '));

  btn.click();
  S.config = { llm: {} };
  renderComposer();
  assert(wrap.hidden && menu.hidden, 'a model with no effort levels has no control, and an open menu goes with it');
  S.config = own([], '');
  renderComposer();
  assert(wrap.hidden, 'an empty vocabulary draws nothing');
  S.config = null;
  renderComposer();
  assert(wrap.hidden, 'no config, no control');

  assert(input.placeholder === 'Message Willow', 'the placeholder names the identity: ' + input.placeholder);
  S.stats = { name: 'Unnamed' };
  renderComposer();
  assert(input.placeholder === 'Message your identity', 'an unnamed identity is not called Unnamed: ' + input.placeholder);
  S.identityExists = false;
  S.stats = { name: '(not born)' };
  S.config = own(['low'], 'low');
  renderComposer();
  assert(input.placeholder === 'Message your identity' && wrap.hidden, 'before birth: no name and no control');
});
</script></body></html>`
	runPageInEngines(t, page, chatModuleRig(t))
}

// .
// .
// .
// .
// .
func TestAMessageCanBeCopiedExactlyAsShown(t *testing.T) {
	page := `<!doctype html><html><body>
<div id="thread"><div id="thread-inner"></div></div>
<textarea id="msg-input"></textarea><button id="send-btn"></button>
<script type="module">
import { run } from '/__harness.js';
import { toasts } from '/app.js';
import { addMsg, sysLine } from '/views/chat.js';
run(async (assert) => {
  const settle = () => new Promise(r => setTimeout(r, 20));
  assert(window.isSecureContext, 'the rig must serve a secure context for the Clipboard API path to be the one under test');
  const copied = [];
  Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText: async t => { copied.push(t); } } });

  const shown = 'Three things.\n  an indented line & <angle brackets>';
  const reply = addMsg('identity', shown);
  const b = reply.querySelector('.copyb');
  assert(b, 'a reply has a copy button');
  b.click();
  await settle();
  assert(copied.length === 1 && copied[0] === shown, 'the clipboard received exactly the text shown: ' + JSON.stringify(copied));
  assert(!b.innerHTML.includes('rect'), 'the icon becomes a check once copied');
  assert(toasts.length === 0, 'a successful copy shows no toast');

  assert(addMsg('operator', 'what changed?').querySelector('.copyb'), 'the operator\'s own message can be copied');
  sysLine('The operator stopped this turn.');
  assert(!document.querySelector('.msg.system .copyb'), 'a system line has no copy button');

  Object.defineProperty(navigator, 'clipboard', { configurable: true, value: undefined });
  let taken = null;
  document.execCommand = cmd => { const ta = document.body.querySelector('textarea[readonly]'); if (cmd === 'copy' && ta) taken = ta.value; return true; };
  addMsg('identity', 'the older path').querySelector('.copyb').click();
  await settle();
  assert(taken === 'the older path', 'without the Clipboard API the copy command takes the same text: ' + taken);
  assert(!document.body.querySelector('textarea[readonly]'), 'the temporary field is removed');

  document.execCommand = () => false;
  const refused = addMsg('identity', 'refused').querySelector('.copyb');
  refused.click();
  await settle();
  assert(toasts.length === 1 && /Could not copy/.test(toasts[0]), 'a refused copy says so: ' + JSON.stringify(toasts));
  assert(refused.innerHTML.includes('rect'), 'a refused copy does not show the check');
});
</script></body></html>`
	runPageInEngines(t, page, chatModuleRig(t))
}

// .
// .
// .
// .
func TestJumpToLatestAppearsOnlyAwayFromTheBottom(t *testing.T) {
	page := `<!doctype html><html><body>
<div id="thread" style="height:160px;overflow-y:auto"><div id="thread-inner"></div></div>
<button id="jump-latest" hidden>down</button>
<textarea id="msg-input"></textarea><button id="send-btn"></button>
<script type="module">
import { run } from '/__harness.js';
import { addMsg } from '/views/chat.js';
run(async (assert) => {
  const t = document.getElementById('thread'), j = document.getElementById('jump-latest');
  const gap = () => t.scrollHeight - t.scrollTop - t.clientHeight;
  for (let i = 0; i < 40; i++) addMsg(i % 2 ? 'identity' : 'operator', 'message ' + i);
  assert(t.scrollHeight > t.clientHeight * 3, 'the thread overflows');
  assert(gap() < 1, 'new messages were followed to the bottom');
  assert(j.hidden, 'at the bottom there is no arrow');

  t.scrollTop = 0;
  t.dispatchEvent(new Event('scroll'));
  assert(!j.hidden, 'scrolled up, the arrow appears');

  addMsg('identity', 'a reply arriving while you read back');
  assert(t.scrollTop === 0, 'a new message does not pull you down while you read back');
  assert(!j.hidden, 'and the arrow stays');

  j.click();
  const deadline = Date.now() + 3000;
  while (gap() >= 140 && Date.now() < deadline) await new Promise(r => requestAnimationFrame(r));
  t.dispatchEvent(new Event('scroll'));
  assert(gap() < 140, 'the arrow returns to the latest message: ' + gap() + 'px short');
  assert(j.hidden, 'and leaves once you are there');
});
</script></body></html>`
	runPageInEngines(t, page, chatModuleRig(t))
}

// .
// .
// .
func TestHomeTalkFollowsTheMicrophonesState(t *testing.T) {
	page := `<!doctype html><html><body>
<nav><div class="nav-item" data-view="home"></div><div class="nav-item" data-view="chat"></div><div class="nav-item" data-view="memory"></div><div class="nav-item" data-view="identity"></div><div class="nav-item" data-view="projects"></div></nav>
<div id="home-inner"></div>
<textarea id="msg-input"></textarea><button id="converse">talk</button><button id="mic">mic</button><input id="mem-search">
<script type="module">
import { run } from '/__harness.js';
import { S } from '/state.js';
import { renderHome } from '/views/home.js';
run(async (assert) => {
  const clicked = [], opened = [];
  document.querySelectorAll('.nav-item').forEach(el => { el.onclick = () => clicked.push(el.dataset.view); });
  S.openSettings = (section, focus) => opened.push(section + '/' + focus);
  S.connected = true; S.identityExists = true; S.identity = null; S.work = null; S.cont = null; S.projects = [];

  S.stats = { name: 'Willow', voice_state: 'setup' };
  renderHome();
  let talk = document.getElementById('home-talk');
  assert(talk && talk.classList.contains('faint') && talk.title === 'Voice isn\'t set up — opens Speech settings', 'with nothing set up Home offers the way to set it up: ' + (talk && talk.outerHTML));
  talk.click();
  assert(opened.join() === 'speech/sp-provider-stt' && !clicked.includes('chat'), 'and it opens Speech settings, not Chat');

  S.stats = { name: 'Willow', voice_state: 'cloud' };
  renderHome();
  document.getElementById('home-talk').click();
  assert(clicked.includes('chat') && document.activeElement === document.getElementById('mic'), 'with cloud speech it lands on Chat\'s push-to-talk microphone');

  clicked.length = 0;
  S.stats = { name: 'Willow', voice_state: 'plugin' };
  renderHome();
  document.getElementById('home-talk').click();
  assert(clicked.includes('chat') && document.activeElement === document.getElementById('converse'), 'with a voice plugin it lands on the conversation microphone');

  S.stats = { name: 'Willow', voice_state: 'safe' };
  renderHome();
  talk = document.getElementById('home-talk');
  assert(talk.disabled && talk.title === 'Voice is paused while this identity is in SAFE', 'under SAFE it is paused, and says why');
});
</script></body></html>`
	modules := map[string][]byte{}
	for _, path := range []string{"static/views/home.js", "static/state.js", "static/util.js"} {
		data, err := staticFS.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		modules["/"+strings.TrimPrefix(path, "static/")] = data
	}
	modules["/views/projects.js"] = []byte(`export function viewProject() {}`)
	modules["/views/work.js"] = []byte(`export function workSectionHTML() { return ''; } export function wireWork() {}`)
	runPageInEngines(t, page, modules)
}

// .
// .
func TestTheChatChooserOffersOnlyEntriesThatChat(t *testing.T) {
	page := `<!doctype html><html><body>
<div id="chat-substrate-wrap"><select id="chat-provider"></select><select id="chat-model"></select><button id="chat-substrate-apply"></button><div id="chat-substrate-status"></div></div>
<div id="thread"><div id="thread-inner"></div></div><textarea id="msg-input"></textarea><button id="send-btn"></button>
<script type="module">
import { run } from '/__harness.js';
import { S } from '/state.js';
import { renderChatSubstrate } from '/views/chat.js';
run(async (assert) => {
  S.identityExists = true;
  S.config = { llm: { provider: 'OpenAI', model: 'gpt-5.6-sol', resolved_provider: 'OpenAI', resolved_model: 'gpt-5.6-sol' } };
  S.providers = [
    { name: 'OpenAI', chat: true, models: ['gpt-5.6-sol'], speech: { stt: { models: ['whisper-1'] } } },
    { name: 'ElevenLabs', chat: false, speech: { tts: { models: ['eleven_v3'] } } },
    { name: 'Older payload' }
  ];
  renderChatSubstrate();
  const names = [...document.getElementById('chat-provider').options].map(o => o.value);
  assert(names.indexOf('ElevenLabs') < 0, 'a speech-only entry was offered for chat: ' + names.join(','));
  assert(names.indexOf('OpenAI') >= 0 && names.indexOf('Older payload') >= 0, 'chat entries, and a payload with no chat flag, stay offered: ' + names.join(','));
});
</script></body></html>`
	runPageInEngines(t, page, chatModuleRig(t))
}

// .
// .
// .
func TestFirstbootOffersOnlyEntriesThatChat(t *testing.T) {
	page := `<!doctype html><html><body>
<select id="fb-provider"></select><div id="fb-cred-why"></div>
<script type="module">
import { run } from '/__harness.js';
import { S } from '/state.js';
import { renderProviderOptions } from '/firstboot.js';
run(async (assert) => {
  S.identityExists = false;
  S.providers = [{ name: 'OpenAI', chat: true }, { name: 'ElevenLabs', chat: false }, { name: 'Anthropic', chat: true }];
  renderProviderOptions();
  const opts = [...document.getElementById('fb-provider').options].filter(o => o.value !== '');
  assert(opts.map(o => o.textContent).join(',') === 'OpenAI,Anthropic', 'firstboot offered: ' + opts.map(o => o.textContent).join(','));
  assert(opts[1].value === '2' && S.providers[parseInt(opts[1].value, 10)].name === 'Anthropic', 'an option lost its entry: ' + opts[1].value);
});
</script></body></html>`
	fb, err := staticFS.ReadFile("static/firstboot.js")
	if err != nil {
		t.Fatal(err)
	}
	runPageInEngines(t, page, map[string][]byte{
		"/firstboot.js": fb,
		"/state.js":     []byte(`export const S = { providers: [], identityExists: false };`),
		// .
		// .
		"/util.js":   []byte(`export const $ = id => document.getElementById(id) || document.createElement('div'); export const esc = v => String(v ?? '').replace(/&/g,'&amp;').replace(/</g,'&lt;');`),
		"/ws.js":     []byte(`export function send() { return ''; } export function query() { return ''; }`),
		"/signin.js": []byte(`export function startSignIn() {} export function signInProgress() { return null; } export function signInWanted() { return true; } export function wireSignInCompletion() {}`),
	})
}
