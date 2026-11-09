//go:build !windows

package dashboard

import "testing"

const modelSwitchJS = `
import { report, until } from '/__harness.js';
import { S } from '/state.js';
const $ = id => document.getElementById(id);
const shown = el => !!el && el.getClientRects().length > 0;
const sockets = window.__sockets, sent = window.__sent;
const say = f => sockets[0].say(f);
const problems = [];
const check = (ok, what) => { if (!ok) problems.push(what); };
const IN_FORCE = 'anthropic/claude-opus-5-5', OTHER = 'deepseek/deepseek-v4.1-lite-preview-long-identifier-2026-10-28', LOCAL = 'DeepSeek-V4.1-Lite-MLX-8bit';
const stats = { name: 'Quill', version: 'test', build: 'test', ledger_seq: 7, lifetime_ticks: 1, belief_count: 0, intention_count: 0, experience_count: 0, reflection_count: 0 };
const config = { llm: { provider: 'OpenRouter', model: IN_FORCE, resolved_provider: 'OpenRouter', resolved_model: IN_FORCE, timeout_seconds: 120 }, speech: {}, prompt: {}, witness: {}, logs: {}, agency: {}, plugins: {}, cognition: {}, dashboard: { host: '127.0.0.1', port: 1, tls: false }, restart_required: [] };
const providers = [{ name: 'OpenRouter', default: true, status: 'ok', models: [IN_FORCE, OTHER], default_model: IN_FORCE }, { name: 'A local server', models: [LOCAL], default_model: LOCAL }, { name: 'Speech only', chat: false }, { name: 'A gone service', status: 'unreachable', models: ['gone-1'], default_model: 'gone-1' }];
const choose = (el, value) => { el.value = value; el.dispatchEvent(new Event('change', { bubbles: true })); };
const said = () => $('chat-substrate-status').textContent;

async function run() {
  await until(() => sockets.length > 0);
  sockets[0].open();
  say({ type: 'status', stats }); say({ type: 'continuity', continuity: { mode: 'normal', ledger_seq: 7 } }); say({ type: 'config', config }); say({ type: 'providers', providers });
  S.go('chat');
  await until(() => S.view === 'chat' && shown($('chat-provider')));
  const wrap = $('chat-substrate-wrap'), provider = $('chat-provider'), model = $('chat-model'), use = $('chat-substrate-apply');

  // At the top of the conversation.
  await until(() => wrap.parentElement === $('view-chat'));
  check($('view-chat').firstElementChild === wrap && wrap.getBoundingClientRect().bottom <= $('thread').getBoundingClientRect().top + 1, 'in the conversation the model is not above the thread');
  const box = document.querySelector('.composer-row.c-reply__box').getBoundingClientRect(), r = wrap.getBoundingClientRect();
  check(Math.abs(r.left - box.left) <= 1 && Math.abs(r.right - box.right) <= 1, 'the model does not stand at the column: ' + Math.round(r.left) + ' to ' + Math.round(r.right) + ', the box ' + Math.round(box.left) + ' to ' + Math.round(box.right));
  check(document.documentElement.scrollWidth <= document.documentElement.clientWidth && wrap.scrollWidth <= wrap.clientWidth, 'a long model name runs past the page or the row');

  // What is in force, and only what can think.
  check(provider.value === 'OpenRouter' && model.value === IN_FORCE, 'it does not show what is in force: ' + provider.value + ' / ' + model.value);
  check(![...provider.options].some(o => o.value === 'Speech only'), 'an entry that only speaks is offered as a model');
  // One that did not answer when last asked is not offered (Settings → Model
  // lists it, and why); one that answers, or was not asked yet, reads as its name.
  const reads = () => [...provider.options].map(o => o.value + '=' + o.textContent).join(' | ');
  check(reads() === 'OpenRouter=OpenRouter | A local server=A local server', 'the providers read: ' + reads());
  // The one in force is still shown when it stops answering, and says so: the
  // switch never shows another than the one answering.
  say({ type: 'providers', providers: providers.map(p => p.name === 'OpenRouter' ? { ...p, status: 'unreachable' } : p) });
  check(reads() === 'OpenRouter=OpenRouter · not answering | A local server=A local server' && provider.value === 'OpenRouter', 'with the one in force not answering, the providers read: ' + reads() + ', showing ' + provider.value);
  say({ type: 'providers', providers });
  check(reads() === 'OpenRouter=OpenRouter | A local server=A local server', 'the one in force answering again still reads: ' + reads());
  // A choice not yet used, of a provider that then stops answering, is withdrawn:
  // not offered, not shown, and Use cannot send it.
  choose(provider, 'A local server');
  check(provider.value === 'A local server' && !use.disabled, 'choosing another provider does not make Use live');
  say({ type: 'providers', providers: providers.map(p => p.name === 'A local server' ? { ...p, status: 'unreachable' } : p) });
  check(reads() === 'OpenRouter=OpenRouter' && provider.value === 'OpenRouter' && use.disabled, 'a choice of a provider that stopped answering still stands: ' + reads() + ', showing ' + provider.value + ', Use ' + (use.disabled ? 'off' : 'live'));
  if (problems.length) return report('FAIL: ' + problems.join('\n  - '));
  say({ type: 'providers', providers });
  const local = () => providers.map(p => p.name === 'A local server' ? { ...p, status: 'unreachable' } : p);
  const sets = () => sent.filter(q => q.type === 'config_set');
  const setsOf = name => sets().filter(q => JSON.stringify(q).includes(name)).length;
  // With its model list open: both lists are drawn again, and Use sends nothing.
  choose(provider, 'A local server'); model.focus();
  const before = sets().length;
  say({ type: 'providers', providers: local() });
  check(provider.value === 'OpenRouter' && model.value === IN_FORCE && use.disabled, 'with its model list open, a withdrawn choice left ' + provider.value + ' / ' + model.value + ', Use ' + (use.disabled ? 'off' : 'live'));
  use.click();
  check(sets().length === before, 'Use sent a withdrawn pair: ' + JSON.stringify(sets().slice(before)));
  model.blur(); say({ type: 'providers', providers });
  // Use pressed while Settings' door is fetched, and the provider stops answering meanwhile: nothing is sent.
  const realIn = S.settingsIn; let release, settled = false;
  S.settingsIn = () => new Promise(r => { release = r; }).then(() => realIn()).then(v => { settled = true; return v; });
  choose(provider, 'A local server');
  check(!use.disabled, 'choosing another provider does not make Use live');
  use.click();
  say({ type: 'providers', providers: local() });
  release(); await until(() => settled, 5000);
  await until(() => provider.value === 'OpenRouter', 2000);
  check(setsOf('A local server') === 0 && sets().length === before, 'a pair withdrawn while Settings came was sent: ' + JSON.stringify(sets().slice(before)));
  check(provider.value === 'OpenRouter' && model.value === IN_FORCE, 'after it, the switch shows ' + provider.value + ' / ' + model.value);
  S.settingsIn = realIn; say({ type: 'providers', providers });
  if (problems.length) return report('FAIL: ' + problems.join('\n  - '));
  check(use.disabled && said() === '', 'with nothing changed Use is live or something is said');

  // A choice is the person's until Use, through a frame from the host.
  choose(model, OTHER);
  check(!use.disabled, 'a choice does not make Use live');
  say({ type: 'config', config });
  check(model.value === OTHER && !use.disabled, 'a frame from the host took the choice back');
  use.click();
  await until(() => sent.some(q => q.type === 'config_set'));
  const asked = sent.find(q => q.type === 'config_set');
  check(asked.config['llm.provider'] === 'OpenRouter' && asked.config['llm.model'] === OTHER && Object.keys(asked.config).length === 2, 'Use sent: ' + JSON.stringify(asked.config));
  await until(() => said().includes('Checking that ' + OTHER));
  check(provider.disabled && model.disabled && use.disabled, 'it can be pressed again while it is checked');

  // The Model page's answer, in its words, and the Model page holds the same.
  const adopted = { ...config, llm: { ...config.llm, model: OTHER, resolved_model: OTHER, answered: true } };
  say({ type: 'config', request_id: asked.request_id, config: adopted });
  await until(() => said().includes('Active. ' + OTHER));
  check(said().includes('the model Quill thinks with'), 'the answer is not the Model page\'s: ' + said());
  check(model.value === OTHER && use.disabled && !model.disabled, 'after the answer: ' + model.value + ' use disabled ' + use.disabled);
  location.hash = '#/settings/model';
  await until(() => shown($('cfg-model')));
  check($('cfg-model').value === OTHER && $('cfg-provider').value === 'OpenRouter', 'the Model page says otherwise: ' + $('cfg-provider').value + ' / ' + $('cfg-model').value);
  const page = document.querySelector('[data-said="llm"]');
  check(page && page.textContent === said(), 'the two places say different things: ' + (page && page.textContent) + ' / ' + said());

  // Another provider brings its own list; a refusal is said, and the choice stands.
  location.hash = '#/chat';
  await until(() => S.view === 'chat');
  sent.length = 0;
  choose(provider, 'A local server');
  check(model.value === LOCAL && [...model.options].length === 1, 'the list did not follow the provider: ' + model.value);
  use.click();
  await until(() => sent.some(q => q.type === 'config_set'));
  const second = sent.find(q => q.type === 'config_set');
  say({ type: 'error', message: 'The server did not answer.', request_id: second.request_id });
  await until(() => said().includes('The server did not answer.'));
  check(provider.value === 'A local server' && model.value === LOCAL && !use.disabled, 'a refusal took the choice back or left Use dead');

  // No identity, nothing to choose for.
  report(problems.length ? 'FAIL at ' + innerWidth + ':\n  - ' + problems.join('\n  - ') : 'OK');
}
run().catch(e => report('FAIL: the run stopped: ' + ((e && e.message) || e)));
`

// THE MODEL IS CHOSEN AT THE TOP OF THE CONVERSATION. Provider, model, Use and
// what came back, at the top of the conversation; it saves through the Model
// page's door and says the Model page's answer, so the two cannot disagree. A
// choice stays the person's through a frame from the host until it is used.
func TestTheModelIsChosenAtTheTopOfTheConversation(t *testing.T) {
	page, modules, headers := shippedPageRig(t, modelSwitchJS)
	for _, size := range [][2]int{{390, 844}, {853, 600}, {1024, 800}, {1280, 900}} {
		runPageInEnginesAtSizeWithHeaders(t, page, modules, headers, size[0], size[1])
	}
}

const recordSentJS = `
import { report, until } from '/__harness.js';
import { S } from '/state.js';
const sockets = window.__sockets;
async function run() {
  await until(() => sockets.length > 0);
  sockets[0].open();
  sockets[0].say({ type: 'status', stats: { name: 'Quill', version: 'test', build: 'test', ledger_seq: 7, lifetime_ticks: 1, belief_count: 0, intention_count: 0, experience_count: 0, reflection_count: 0 } });
  location.hash = '#/identity/record';
  await until(() => S.view === 'record' && document.querySelector('[data-record-sent] a')?.getClientRects().length);
  const a = document.querySelector('[data-record-sent] a');
  if (a.textContent !== 'See exactly what the model was sent') return report('FAIL: the link reads ' + a.textContent);
  if (a.getAttribute('href') !== '#/settings/technical/sent') return report('FAIL: the link goes to ' + a.getAttribute('href'));
  report('OK');
}
run().catch(e => report('FAIL: the run stopped: ' + ((e && e.message) || e)));
`

// RECORD SAYS WHERE WHAT THE MODEL WAS SENT IS: Technical's panel.
func TestRecordLeadsToWhatTheModelWasSent(t *testing.T) {
	page, modules, headers := shippedPageRig(t, recordSentJS)
	runPageInEnginesAtSizeWithHeaders(t, page, modules, headers, 1280, 900)
}

const whyWhereItWasPressedJS = `
import { report, until } from '/__harness.js';
import { S } from '/state.js';
const sockets = window.__sockets, sent = window.__sent;
const say = f => sockets[0].say(f);
const problems = [];
const check = (ok, what) => { if (!ok) problems.push(what); };
const beliefs = Array.from({ length: 40 }, (_, i) => ({ id: 'b' + i, statement: 'Belief ' + i + ', held for a reason a person may want to read.', ring: 3, status: 'confirmed', evidence_count: i % 3, confidence: 0.8 }));
const stats = { name: 'Quill', version: 'test', build: 'test', ledger_seq: 40, lifetime_ticks: 1, belief_count: 40, intention_count: 0, experience_count: 0, reflection_count: 0 };
const row = id => document.querySelector('#beliefs-view .c-belief[data-belief-id="' + id + '"]');
const seen = el => { const r = el.getBoundingClientRect(); return r.top < innerHeight && r.bottom > 0 && r.width > 0; };
async function run() {
  await until(() => sockets.length > 0);
  sockets[0].open();
  say({ type: 'status', stats }); say({ type: 'continuity', continuity: { mode: 'normal', ledger_seq: 40 } });
  location.hash = '#/identity/beliefs';
  await until(() => S.view === 'memory');
  say({ type: 'identity', identity: { beliefs, intentions: [], experiences: [] } });
  await until(() => row('b30'));
  // Far down the list, as a person reaches it, by mouse and by keyboard.
  for (const [id, press] of [['b30', b => b.click()], ['b12', b => { b.focus(); b.click(); }]]) {
    row(id).scrollIntoView({ block: 'center' });
    press(row(id).querySelector('.c-belief__why'));
    await until(() => sent.some(q => q.query === 'belief_trail' && q.belief_id === id));
    const asked = sent.filter(q => q.query === 'belief_trail' && q.belief_id === id).at(-1);
    await until(() => document.querySelector('#beliefs-view .c-trail'));
    const trail = document.querySelector('#beliefs-view .c-trail');
    check(seen(trail), 'Why? on ' + id + ' opened nothing a person can see: the trail stands at ' + Math.round(trail.getBoundingClientRect().top) + ' of ' + innerHeight);
    check(trail.closest('li')?.previousElementSibling === row(id), 'the trail of ' + id + ' is not under it');
    say({ type: 'belief_trail', request_id: asked.request_id, belief_trail: { belief: { id, statement: 'Belief ' + id + ' as recorded.', last_seq: 4, ring: 3, status: 'confirmed', confidence: 0.8 }, origins: [], contradicts: [], supersedes: [], truncated: {} } });
    await until(() => document.querySelector('#beliefs-view .c-trail')?.textContent.includes('as recorded'));
    check(seen(document.querySelector('#beliefs-view .c-trail')), 'the answer to Why? on ' + id + ' is not where a person can see it');
  }
  report(problems.length ? 'FAIL at ' + innerWidth + ':\n  - ' + problems.join('\n  - ') : 'OK');
}
run().catch(e => report('FAIL: the run stopped: ' + ((e && e.message) || e)));
`

// WHY? OPENS WHERE IT WAS PRESSED. The trail stands under the belief it explains,
// in view, at every width: at the widths between a phone's and the two columns it
// once stood under the whole list, where the press showed nothing at all.
func TestWhyOpensTheTrailWhereItWasPressed(t *testing.T) {
	page, modules, headers := shippedPageRig(t, whyWhereItWasPressedJS)
	for _, size := range [][2]int{{390, 844}, {853, 600}, {1024, 800}, {1280, 900}} {
		runPageInEnginesAtSizeWithHeaders(t, page, modules, headers, size[0], size[1])
	}
}

const nothingOnlyALivePageSawJS = `
import { report, until } from '/__harness.js';
import { S } from '/state.js';
const sockets = window.__sockets;
const say = f => sockets[0].say(f);
const stats = extra => ({ name: 'Quill', version: 'test', build: 'test', ledger_seq: 7, lifetime_ticks: 1, belief_count: 0, intention_count: 0, experience_count: 0, reflection_count: 0, ...extra });
const notices = () => [...document.querySelectorAll('#thread-inner .chat-notice, #chat-notices .chat-notice, #chat-notice')].map(e => e.textContent);
async function run() {
  await until(() => sockets.length > 0);
  sockets[0].open();
  say({ type: 'status', stats: stats() }); say({ type: 'continuity', continuity: { mode: 'normal', ledger_seq: 7 } });
  location.hash = '#/chat';
  await until(() => S.view === 'chat');
  say({ type: 'projects', projects: [{ id: 'a', name: 'Alpha', state: 'open', active: false }] });
  say({ type: 'projects', projects: [{ id: 'a', name: 'Alpha', state: 'open', active: true }] });
  await until(() => document.getElementById('reply-project')?.textContent.includes('Alpha'));
  say({ type: 'status', stats: stats({ update: { enabled: true, current_version: '1.0.0', available_version: '1.1.0', automatic: false } }) });
  await until(() => !document.getElementById('nav-update').hidden);
  say({ type: 'system_line', message: 'One question was withdrawn.' });
  say({ type: 'restart' });
  say({ type: 'response', role: 'system', done: true, message: 'An internal pass holds the turn.' });
  await until(() => document.getElementById('toast')?.textContent.length > 0);
  const lines = notices();
  if (lines.length) return report('FAIL: lines only this page saw stand in the conversation: ' + JSON.stringify(lines));
  report('OK');
}
run().catch(e => report('FAIL: the run stopped: ' + ((e && e.message) || e)));
`

// WHAT ONLY A LIVE PAGE SAW IS NOT IN THE CONVERSATION. The project in focus, an
// update, a restart and a question withdrawn are each said where the page draws
// that state, or for a moment; a line in the thread would be gone after a reload
// and so would make the second drawing differ from the first.
func TestWhatOnlyALivePageSawIsNotInTheConversation(t *testing.T) {
	page, modules, headers := shippedPageRig(t, nothingOnlyALivePageSawJS)
	runPageInEnginesAtSizeWithHeaders(t, page, modules, headers, 1280, 900)
}
