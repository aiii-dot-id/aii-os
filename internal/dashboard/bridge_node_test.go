package dashboard

// The bridge contract, executed (threat row S1's frame half): node
// (which ships MessageChannel) runs the REAL frame bridge core
// (bridge.js — allowlists, wired-command registry, topic projections)
// against the REAL section client (section-api.js) over a live
// MessageChannel, and asserts refusal-by-name for undeclared topics
// and undeclared/unwired commands, the exact v0 command frame with its
// server gate's argument shape, the host's own answer to an act (matched by
// its request id; a refusal in the host's words; a lost connection; a
// command that could not be sent), the current state sent on subscribe,
// token push, and the resize flow. The
// js_syntax walk proves the modules PARSE; this proves they BEHAVE —
// same skip rule when node is absent.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const bridgeDriver = `
import { createFrameBridge, topicOf, WIRED_COMMANDS } from './bridge.mjs';
import { ready, _accept } from './section_api.mjs';

function fail(msg) { console.error('FAIL: ' + msg); process.exit(1); }
function assert(cond, msg) { if (!cond) fail(msg); }
async function settle() { for (let i = 0; i < 8; i++) await new Promise(r => setTimeout(r, 0)); }

// --- topicOf: the precise ws.js-dispatch → topic mapping ---
let t = topicOf({ type: 'status', stats: { name: 'Mira' } });
assert(t && t.topic === 'status' && t.data.name === 'Mira', 'status projection');
t = topicOf({ type: 'projects', projects: [{ id: 'p1' }] });
assert(t && t.topic === 'projects' && t.data[0].id === 'p1', 'projects projection');
t = topicOf({ type: 'work', work: { queued: 2 } });
assert(t && t.topic === 'work' && t.data.queued === 2, 'work projection');
t = topicOf({ type: 'identity', identity: { name: 'Mira' } });
assert(t && t.topic === 'identity' && t.data.name === 'Mira', 'identity projection');
t = topicOf({ type: 'continuity', continuity: { mode: 'safe' } });
assert(t && t.topic === 'continuity' && t.data.mode === 'safe', 'continuity projection');
t = topicOf({ type: 'asks', asks: [{ id: 'a1' }] });
assert(t && t.topic === 'asks' && t.data[0].id === 'a1', 'asks projection');
// config is narrowed to what Status shows: nothing else of it reaches a section
t = topicOf({ type: 'config', config: { llm: { model: 'm', provider: 'p', resolved_model: 'rm', resolved_provider: 'rp', api_key: 'SECRET', endpoint: 'SECRET' }, cognition: { pulse_seconds: 60, secret: 'SECRET' }, plugins: { installed: ['a'], token: 'SECRET' }, secrets_ok: true, dashboard: { token: 'SECRET' } } });
assert(t && t.topic === 'config' && t.data.llm.resolved_model === 'rm' && t.data.cognition.pulse_seconds === 60 && t.data.plugins.installed[0] === 'a', 'config projection: ' + JSON.stringify(t));
assert(!JSON.stringify(t).includes('SECRET') && t.data.secrets_ok === undefined && t.data.dashboard === undefined, 'config carries only the fields Status shows: ' + JSON.stringify(t));
t = topicOf({ type: 'providers', providers: [{ name: 'p', context_length: 8, api_key: 'SECRET' }] });
assert(t && t.data[0].context_length === 8 && !JSON.stringify(t).includes('SECRET'), 'providers carries only what Status shows: ' + JSON.stringify(t));
assert(topicOf({ type: 'spend', spend: {} }) === null, 'spend is NOT a topic: it answers one request');

// --- a real channel: frame bridge on port1, section api on port2 ---
const ch = new MessageChannel();
const wsFrames = [];
let resized = 0, offline = false, readForTheUndeclared = false;
const bridge = createFrameBridge({
  topics: ['status', 'future.topic'],           // declared by the section
  commands: ['project.select', 'future.cmd'],   // declared; future.cmd is NOT wired
  wired: WIRED_COMMANDS,                        // the frame registry (v0: exactly one command)
  latest: topic => topic === 'status' ? { type: 'status', stats: { name: 'Now' } } : undefined,
  reads: { record: () => { readForTheUndeclared = true; return Promise.resolve({ rows: [{ seq: '1', head: 'private' }] }); } }, // as the frame always offers
  post: m => ch.port1.postMessage(m),
  sendToServer: f => { if (offline) return ''; wsFrames.push(f); return 'r' + wsFrames.length; },
  onResize: h => { resized = h; },
});
ch.port1.onmessage = e => bridge.onFrame(e.data);
// v1 is the bridge contract; the mismatch case must throw BY NAME.
let threw = false;
try { _accept({ tokens: {} }, new MessageChannel().port2); } catch (e) { threw = /is not v1/.test(String(e)); }
if (!threw) { console.error('FAIL: version-less connect must be refused'); process.exit(1); }
_accept({ v: 1, tokens: { '--acc': '#8b6cff' } }, ch.port2);
const api = await ready();

// tokens: initial set arrives with the connect
let toks = null;
api.tokens(x => { toks = x; });
assert(toks && toks['--acc'] === '#8b6cff', 'initial tokens push');

// subscribe (declared) → relay flows
let seen = null;
await api.data.subscribe('status', d => { seen = d; });
assert(seen && seen.name === 'Now', 'the current state is held when the subscription is answered: ' + JSON.stringify(seen));
let future = 'none';
await api.data.subscribe('future.topic', d => { future = d; });
assert(future === 'none', 'a topic with no state yet sends nothing on subscribe');
bridge.publish('status', { name: 'Mira' });
await settle();
assert(seen && seen.name === 'Mira', 'declared+subscribed topic relays');

// undeclared subscribe → refused reply NAMING the topic; cb never fires
let leaked = false;
let refusedMsg = '';
await api.data.subscribe('identity', () => { leaked = true; }).then(
  () => fail('undeclared subscribe must refuse'),
  e => { refusedMsg = e.message; });
assert(refusedMsg.includes('identity') && refusedMsg.includes('not declared'), 'refusal names the topic: ' + refusedMsg);
bridge.publish('identity', { private: true });
await settle();
assert(!leaked, 'an undeclared topic must never reach the section');

// undeclared-by-section publish is dropped even if frame tried
bridge.publish('work', { queued: 1 });
await settle(); // 'work' was never declared by THIS section

// act: the ONE wired v0 command → the EXACT existing WS frame
const acted = api.act('project.select', { id: 'p-42' });
let state = 'waiting';
acted.then(() => { state = 'answered'; }, () => { state = 'refused'; });
await settle();
assert(wsFrames.length === 1, 'exactly one WS frame sent');
assert(state === 'waiting', 'an act is answered only by the host');
bridge.answer({ type: 'projects', request_id: 'r9', projects: [] });
await settle();
assert(state === 'waiting', 'another request\'s answer answered the act');
bridge.answer({ type: 'projects', request_id: 'r1', projects: [{ id: 'p-42' }] });
const r = await acted;
assert(Array.isArray(r) && r[0].id === 'p-42', 'the act resolves with what the host answered: ' + JSON.stringify(r));
const f = wsFrames[0];
assert(f.type === 'project' && f.project && f.project.action === 'select' && f.project.id === 'p-42',
  'v0 command maps to the existing project-select case, got ' + JSON.stringify(f));

// act with bad args → validate refuses before any send
await api.act('project.select', {}).then(
  () => fail('argument validation must refuse'),
  e => assert(e.message.includes('args.id'), 'refusal names the requirement: ' + e.message));
assert(wsFrames.length === 1, 'refused act must not touch the socket');

// act undeclared → refused naming the command
await api.act('config.set', { x: 1 }).then(
  () => fail('undeclared command must refuse'),
  e => assert(e.message.includes('config.set') && e.message.includes('not declared'), 'names the command: ' + e.message));

// act declared-but-unwired → refused naming the command (double allowlist)
await api.act('future.cmd', {}).then(
  () => fail('unwired command must refuse'),
  e => assert(e.message.includes('future.cmd') && e.message.includes('not wired'), 'names the unwired command: ' + e.message));
assert(wsFrames.length === 1, 'no refused act reaches the socket');

// tokens: on change
bridge.pushTokens({ '--acc': '#ffffff' });
await settle();
assert(toks['--acc'] === '#ffffff', 'token change pushes through');

// resize: section reports; frame hears the clamped px
api.resize(320);
await settle();
assert(resized === 320, 'resize flows to the frame, got ' + resized);
api.resize(999999);
await settle();
assert(resized === 4000, 'resize clamps at the ceiling, got ' + resized);

// the host refuses, in its own words
const refused = api.act('project.select', { id: 'p-gone' });
await settle();
bridge.answer({ type: 'error', request_id: 'r' + wsFrames.length, message: 'no project is called p-gone' });
await refused.then(() => fail('the host refused, and the act resolved'),
  e => assert(e.message === 'no project is called p-gone', 'the refusal is the host\'s words: ' + e.message));

// the connection is lost while an act waits
const waiting = api.act('project.select', { id: 'p-43' });
await settle();
bridge.lost();
await waiting.then(() => fail('an act whose connection was lost resolved'),
  e => assert(/connection was lost/.test(e.message), 'a lost connection is said: ' + e.message));
bridge.answer({ type: 'projects', request_id: 'r' + wsFrames.length, projects: [] });
await settle();

// a command that could not be sent is refused, and waits for nothing
offline = true;
const before = wsFrames.length;
await api.act('project.select', { id: 'p-44' }).then(() => fail('an unsent act resolved'),
  e => assert(/not connected/.test(e.message), 'an unsent act says so: ' + e.message));
assert(wsFrames.length === before, 'nothing was sent while not connected');

// record.read: answered by the frame's own read, only to a section that names it
await api.act('record.read', {}).then(() => fail('a section that did not name record.read was given the record'),
  e => assert(e.message.includes('record.read') && e.message.includes('not declared'), 'record.read undeclared is refused by name: ' + e.message));
assert(!readForTheUndeclared, 'the frame read the record for a section that did not name record.read');
const rch = new MessageChannel();
const reads = [];
const reader = createFrameBridge({
  topics: [], commands: ['record.read'], wired: WIRED_COMMANDS,
  post: m => rch.port1.postMessage(m), sendToServer: () => fail('record.read reached the socket'),
  reads: { record: params => { reads.push(params); return params.before === '9' ? Promise.reject(new Error('the record could not be read')) : Promise.resolve({ rows: [{ seq: '3', head: 'hello' }], older: '2' }); } },
});
rch.port1.onmessage = e => reader.onFrame(e.data);
let rid = 1;
const ask = (args) => new Promise((ok, no) => {
  const id = 'q' + rid++;
  const prev = rch.port2.onmessage;
  rch.port2.onmessage = e => { if (e.data.id === id) { rch.port2.onmessage = prev; e.data.ok ? ok(e.data.data) : no(new Error(e.data.error)); } };
  rch.port2.postMessage({ kind: 'act', id, command: 'record.read', args });
});
const page = await ask({});
assert(page && page.rows[0].seq === '3' && page.older === '2' && JSON.stringify(reads[0]) === '{}', 'record.read hands the page the frame read: ' + JSON.stringify(page) + ' ' + JSON.stringify(reads));
await ask({ before: '2' });
assert(reads[1] && reads[1].before === '2', 'record.read pages back by before: ' + JSON.stringify(reads[1]));
await ask({ before: 'x;drop' }).then(() => fail('a bad before was read'), e => assert(e.message.includes('args.before'), 'a bad before is refused by name: ' + e.message));
assert(reads.length === 2, 'a refused record.read reads nothing');
await ask({ before: '9' }).then(() => fail('a failed read resolved'), e => assert(e.message === 'the record could not be read', 'a failed read says the reader\'s words: ' + e.message));

// a command for built-in sections alone fails closed for any other, named or not
const ich = new MessageChannel(), bch = new MessageChannel();
const sentBy = { installed: [], builtIn: [] };
const asked = (bridgeOf, port, command, args) => new Promise(ok => {
  const id = 'q' + Math.random();
  port.onmessage = e => { if (e.data.id === id) ok(e.data); };
  port.postMessage({ kind: 'act', id, command, args: args || {} });
});
const installed = createFrameBridge({ topics: [], commands: ['pass.now', 'spend.read'], wired: WIRED_COMMANDS, post: m => ich.port1.postMessage(m), sendToServer: f => { sentBy.installed.push(f); return 'i1'; } });
ich.port1.onmessage = e => installed.onFrame(e.data);
for (const [command, args] of [['pass.now', {}], ['spend.read', { days: 32 }]]) {
  const r = await Promise.race([asked(installed, ich.port2, command, args), new Promise(ok => setTimeout(() => ok({ waiting: true }), 2000))]);
  assert(!r.ok && !r.waiting && r.error.includes('not wired'), 'an installed section that names ' + command + ' is refused: ' + JSON.stringify(r));
}
assert(sentBy.installed.length === 0, 'a built-in command reached the socket for an installed section');
const builtIn = createFrameBridge({ builtIn: true, topics: ['status', 'route'], commands: ['pass.now', 'spend.read'], wired: WIRED_COMMANDS, post: m => bch.port1.postMessage(m), sendToServer: f => { sentBy.builtIn.push(f); return 'b' + sentBy.builtIn.length; } });
bch.port1.onmessage = e => builtIn.onFrame(e.data);
const pressed = asked(builtIn, bch.port2, 'pass.now');
await settle();
assert(sentBy.builtIn.at(-1).type === 'pass_now', 'pass.now is the existing frame: ' + JSON.stringify(sentBy.builtIn));
builtIn.answer({ type: 'pass_now', request_id: 'b' + sentBy.builtIn.length, pass_now: { moved: true, asked_at: 'then' } });
const answered = await pressed;
assert(answered.ok && answered.data.asked_at === 'then', 'pass.now hears the answer the host sent under its own member: ' + JSON.stringify(answered));
const badDays = await asked(builtIn, bch.port2, 'spend.read', { days: 'all' });
assert(!badDays.ok && badDays.error.includes('args.days'), 'spend.read refuses a bad days: ' + JSON.stringify(badDays));
// what an identity has installed and overlaid is for built-in sections alone, named or not
const tch = new MessageChannel();
const namesThem = createFrameBridge({ topics: ['tools', 'overlays', 'connection', 'providers', 'config', 'status'], commands: [], wired: WIRED_COMMANDS, post: m => tch.port1.postMessage(m), sendToServer: () => 'x', latest: () => undefined });
tch.port1.onmessage = e => namesThem.onFrame(e.data);
const subscribes = topic => new Promise(ok => { const id = 'sub-' + topic; tch.port2.onmessage = e => { if (e.data.id === id) ok(e.data); }; tch.port2.postMessage({ kind: 'subscribe', id, topic }); });
for (const topic of ['tools', 'overlays', 'connection', 'providers', 'config']) {
  const r = await subscribes(topic);
  assert(!r.ok && r.error.includes('not given') && r.error.includes(topic), 'an installed section that names ' + topic + ' is refused it: ' + JSON.stringify(r));
}
const namedStatus = await subscribes('status');
assert(namedStatus.ok, 'an installed section may still read status: ' + JSON.stringify(namedStatus));
// a topic with no state yet is asked of the host, and asked again when the section is shown again
const askedFrom = sentBy.builtIn.length;
bch.port2.onmessage = () => {};
bch.port2.postMessage({ kind: 'subscribe', id: 's1', topic: 'status' });
bch.port2.postMessage({ kind: 'subscribe', id: 's2', topic: 'route' });
await settle();
const asks = sentBy.builtIn.slice(askedFrom);
assert(asks.length === 1 && asks[0].type === 'query' && asks[0].query === 'status', 'a topic with no state is asked for, and only a topic the host answers: ' + JSON.stringify(asks));
builtIn.refresh();
assert(sentBy.builtIn.slice(askedFrom).filter(f => f.query === 'status').length === 2, 'a section shown again asks again: ' + JSON.stringify(sentBy.builtIn.slice(askedFrom)));

// each topic is asked by the query that answers it
const dch = new MessageChannel();
const doors = [];
const doorBridge = createFrameBridge({ builtIn: true, topics: ['overlays'], commands: [], wired: WIRED_COMMANDS, post: m => dch.port1.postMessage(m), sendToServer: f => { doors.push(f); return 'd'; } });
dch.port1.onmessage = e => doorBridge.onFrame(e.data);
dch.port2.postMessage({ kind: 'subscribe', id: 'd1', topic: 'overlays' });
await settle();
assert(doors.length === 1 && doors[0].query === 'ui.overlay', 'overlays is asked by its own query: ' + JSON.stringify(doors));

// a frame kept from before a lost connection is given as that, and asked for again
const bfr = new MessageChannel();
const beforeAsks = [], beforeGiven = [];
const beforeBridge = createFrameBridge({ builtIn: true, topics: ['status'], commands: [], wired: WIRED_COMMANDS, latest: () => ({ type: 'status', stats: { name: 'Then' }, before: true }), post: m => bfr.port1.postMessage(m), sendToServer: f => { beforeAsks.push(f); return 'b'; } });
bfr.port1.onmessage = e => beforeBridge.onFrame(e.data);
bfr.port2.onmessage = e => { if (e.data.kind === 'topic') beforeGiven.push(e.data); };
bfr.port2.postMessage({ kind: 'subscribe', id: 'b1', topic: 'status' });
await settle();
assert(beforeGiven[0] && beforeGiven[0].before === true && beforeGiven[0].data.name === 'Then', 'a frame from before the loss is given as that: ' + JSON.stringify(beforeGiven));
assert(beforeAsks.length === 1 && beforeAsks[0].query === 'status', 'a frame from before the loss is asked for again: ' + JSON.stringify(beforeAsks));

// page.key: for built-in sections alone, and only what the page hears
const kch = new MessageChannel(), och = new MessageChannel();
const keys = [];
const keyBridge = createFrameBridge({ builtIn: true, topics: [], commands: ['page.key'], wired: WIRED_COMMANDS, post: m => kch.port1.postMessage(m), sendToServer: () => fail('page.key reached the socket'), reads: { key: a => { keys.push(a); return true; } } });
kch.port1.onmessage = e => keyBridge.onFrame(e.data);
for (const [key, chord, heard] of [['k', { ctrl: true }, true], ['k', { meta: true }, true], ['k', {}, false], ['?', {}, true], ['g', {}, true], ['c', {}, true], ['h', {}, false], ['p', {}, true], ['a', {}, true], ['s', {}, true], ['x', {}, false], ['Enter', {}, false], ['g', { ctrl: true }, false], ['?', { meta: true }, false]]) {
  const before = keys.length;
  const r = await asked(keyBridge, kch.port2, 'page.key', { key, ...chord });
  assert(r.ok === heard && keys.length === before + (heard ? 1 : 0), 'page.key ' + key + JSON.stringify(chord) + ': ' + JSON.stringify(r));
}
const namesKeys = createFrameBridge({ topics: [], commands: ['page.key'], wired: WIRED_COMMANDS, post: m => och.port1.postMessage(m), sendToServer: () => fail('page.key reached the socket'), reads: { key: () => fail('another section pressed a key of the page') } });
och.port1.onmessage = e => namesKeys.onFrame(e.data);
const outsider = await asked(namesKeys, och.port2, 'page.key', { key: 'g' });
assert(!outsider.ok && outsider.error.includes('not wired'), 'another section that names page.key is refused: ' + JSON.stringify(outsider));
// where the page is and the turn are for built-in sections alone
const rt = new MessageChannel();
const namesRoute = createFrameBridge({ topics: ['route', 'turn'], commands: [], wired: WIRED_COMMANDS, post: m => rt.port1.postMessage(m), sendToServer: () => 'x' });
rt.port1.onmessage = e => namesRoute.onFrame(e.data);
for (const topic of ['route', 'turn']) {
  const r = await new Promise(ok => { const id = 'rt-' + topic; rt.port2.onmessage = e => { if (e.data.id === id) ok(e.data); }; rt.port2.postMessage({ kind: 'subscribe', id, topic }); });
  assert(!r.ok && r.error.includes('not given'), 'another section that names ' + topic + ' is refused it: ' + JSON.stringify(r));
}
// About's commands: for built-in sections alone, each checked before it goes
const ABOUT = [['recall.search', { q: 'x' }], ['belief.trail', { id: 'b' }], ['meaning.source', { choice: 'none' }], ['record.payload', { seq: 1, offset: 0, length: 1 }], ['page.go', { to: '#/home' }], ['clipboard.write', { text: 'x' }], ['visit', {}], ['reply.quote', { text: 'x' }]];
const och2 = new MessageChannel();
const outsiderAll = createFrameBridge({ topics: [], commands: ABOUT.map(a => a[0]), wired: WIRED_COMMANDS, post: m => och2.port1.postMessage(m), sendToServer: () => fail('a built-in command reached the socket for another section'), reads: new Proxy({}, { get: () => () => fail('the frame acted for another section') }) });
och2.port1.onmessage = e => outsiderAll.onFrame(e.data);
for (const [command, args] of ABOUT) {
  const r = await asked(outsiderAll, och2.port2, command, args);
  assert(!r.ok && r.error.includes('not wired'), 'another section that names ' + command + ' is refused: ' + JSON.stringify(r));
}
const ach = new MessageChannel();
const did = {}, toSocket = [];
const meaning = { type: 'identity', identity: { meaning: { choice: 'plugin:gone', plugins: ['one'] } } };
const about = createFrameBridge({ builtIn: true, topics: ['backups'], commands: ABOUT.map(a => a[0]).concat('record.read'), wired: WIRED_COMMANDS, latest: t => t === 'identity' ? meaning : undefined, post: m => ach.port1.postMessage(m), sendToServer: f => { toSocket.push(f); return 'a' + toSocket.length; },
  reads: { record: p => { did.record = p; return Promise.resolve({ rows: [] }); }, payload: p => { did.payload = p; return Promise.resolve(new ArrayBuffer(1)); }, go: (to, replace) => { did.go = [to, replace]; return true; }, copy: t => { did.copy = t; return true; }, visit: () => ({ known: true }), quote: t => { did.quote = t; return true; } } });
ach.port1.onmessage = e => about.onFrame(e.data);
const tryAct = (command, args) => asked(about, ach.port2, command, args);
const refusedOrWaits = (command, args) => Promise.race([tryAct(command, args), new Promise(ok => setTimeout(() => ok({ waiting: true }), 2000))]);
for (const [command, args, says] of [
  ['recall.search', { q: '  ' }, 'args.q'], ['recall.search', {}, 'args.q'], ['belief.trail', { id: 7 }, 'args.id'],
  ['meaning.source', { choice: 'plugin:other' }, 'a choice the host offers'], ['meaning.source', { choice: '' }, 'a choice the host offers'], ['meaning.source', { choice: 'memory.anything' }, 'a choice the host offers'],
  ['record.read', { type: 'a;b' }, 'args.type'], ['record.payload', { seq: 'x', offset: 0, length: 1 }, 'whole numbers'], ['record.payload', { seq: 1 }, 'whole numbers'],
  ['page.go', { to: 'https://elsewhere.example/' }, 'an address of the page'], ['page.go', { to: 'javascript:alert(1)' }, 'an address of the page'], ['page.go', {}, 'an address of the page'],
  ['clipboard.write', { text: 7 }, 'args.text'], ['reply.quote', { text: '' }, 'args.text'],
]) {
  const before = toSocket.length;
  const r = await refusedOrWaits(command, args);
  assert(!r.ok && !r.waiting && r.error.includes(says), command + ' ' + JSON.stringify(args) + ' is refused by name: ' + JSON.stringify(r));
  assert(toSocket.length === before, command + ' ' + JSON.stringify(args) + ' reached the socket');
}
assert(!Object.keys(did).length, 'a refused command was done: ' + JSON.stringify(did));
// the host's answers, in the shape each asks
const recalled = tryAct('recall.search', { q: 'cue' });
await settle();
assert(toSocket.at(-1).type === 'query' && toSocket.at(-1).query === 'recall' && toSocket.at(-1).q === 'cue', 'recall.search is the existing query: ' + JSON.stringify(toSocket.at(-1)));
about.frame({ type: 'recall', request_id: 'a' + toSocket.length, query: 'cue', message: 'found' });
const recalledAnswer = await recalled;
assert(recalledAnswer.ok && recalledAnswer.data.query === 'cue' && recalledAnswer.data.message === 'found', 'recall.search hears its words: ' + JSON.stringify(recalledAnswer));
const trailAsked = tryAct('belief.trail', { id: 'b1' });
await settle();
assert(toSocket.at(-1).query === 'belief_trail' && toSocket.at(-1).belief_id === 'b1', 'belief.trail is the existing query: ' + JSON.stringify(toSocket.at(-1)));
const refusal = { type: 'error', request_id: 'a' + toSocket.length, message: 'not held', request_error_code: 'belief.not_found' };
about.frame(refusal);
const trailAnswer = await trailAsked;
assert(!trailAnswer.ok && trailAnswer.error === 'not held' && trailAnswer.code === 'belief.not_found', 'a trail\'s refusal keeps its code: ' + JSON.stringify(trailAnswer));
assert(refusal.said === true, 'a trail\'s refusal is the section\'s to say, and the socket told so');
const sourceAsked = tryAct('meaning.source', { choice: 'plugin:one' });
await settle();
assert(toSocket.at(-1).type === 'config_set' && JSON.stringify(toSocket.at(-1).config) === '{"memory.meaning_source":"plugin:one"}', 'meaning.source sends that one key: ' + JSON.stringify(toSocket.at(-1)));
const sourceRefusal = { type: 'error', request_id: 'a' + toSocket.length, message: 'refused' };
about.frame(sourceRefusal);
assert(!(await sourceAsked).ok && !sourceRefusal.said, 'a refused setting is still said by the page');
tryAct('meaning.source', { choice: 'plugin:gone' });
await settle();
assert(toSocket.at(-1).config['memory.meaning_source'] === 'plugin:gone', 'the choice stored stays a choice');
const passAsked = asked(builtIn, bch.port2, 'pass.now');
await settle();
const passRefusal = { type: 'error', request_id: 'b' + sentBy.builtIn.length, message: 'not now' };
builtIn.frame(passRefusal);
assert(!(await passAsked).ok && passRefusal.said === true, 'a refused pass is said beside its control, and not by the page too');
// what the frame does for it
assert((await tryAct('record.read', { type: 'self_model.synthesize', before: 9 })).ok && did.record.type === 'self_model.synthesize' && did.record.before === '9', 'record.read reads by kind: ' + JSON.stringify(did.record));
assert((await tryAct('record.payload', { seq: 3, offset: 4, length: 5 })).ok && JSON.stringify(did.payload) === '{"seq":"3","offset":"4","length":"5"}', 'record.payload names the range: ' + JSON.stringify(did.payload));
assert((await tryAct('page.go', { to: '#/home', replace: true })).ok && did.go[0] === '#/home' && did.go[1] === true, 'page.go hands the address on: ' + JSON.stringify(did.go));
assert((await tryAct('clipboard.write', { text: 'words' })).ok && did.copy === 'words', 'clipboard.write hands the words on');
assert((await tryAct('reply.quote', { text: 'words' })).ok && did.quote === 'words', 'reply.quote hands the words on');
assert((await tryAct('visit', {})).data.known === true, 'visit answers');
// Backups' topic: what About shows, and nothing else; asked by its own frame
const secret = { type: 'backups', backups: { action: 'status', escrow: 'SECRET', view: { enabled: true, escrow_covers: true, escrow_checked_at: 'c', secrets_ok: true, snapshots: [{ at: 'a', record: 'r', encrypted: true, path: 'SECRET' }, { at: 'older', record: 'SECRET' }], last_pass: { outcome: 'ok', error: 'SECRET' }, identity_choice: { files: ['SECRET'] } } } };
t = topicOf(secret);
assert(t && t.topic === 'backups' && t.data.snapshots.length === 1 && t.data.snapshots[0].record === 'r' && t.data.last_pass.outcome === 'ok' && t.data.escrow_covers === true, 'backups projection: ' + JSON.stringify(t));
assert(!JSON.stringify(t).includes('SECRET'), 'backups carries only what About shows: ' + JSON.stringify(t));
assert(topicOf({ type: 'backups', backups: { action: 'restore_plan' } }) === null, 'a backups frame with no view says nothing');
const before2 = toSocket.length;
ach.port2.postMessage({ kind: 'subscribe', id: 'bk', topic: 'backups' });
await settle();
assert(toSocket.length === before2 + 1 && toSocket.at(-1).type === 'backups' && toSocket.at(-1).backups.action === 'status', 'backups is asked by its own frame: ' + JSON.stringify(toSocket.at(-1)));
const nb = new MessageChannel();
const namesBackups = createFrameBridge({ topics: ['backups'], commands: [], wired: WIRED_COMMANDS, post: m => nb.port1.postMessage(m), sendToServer: () => fail('backups was asked for another section') });
nb.port1.onmessage = e => namesBackups.onFrame(e.data);
const nbR = await new Promise(ok => { nb.port2.onmessage = e => { if (e.data.id === 'x') ok(e.data); }; nb.port2.postMessage({ kind: 'subscribe', id: 'x', topic: 'backups' }); });
assert(!nbR.ok && nbR.error.includes('not given'), 'another section that names backups is refused it: ' + JSON.stringify(nbR));

t = topicOf({ type: 'turn', thinking: true, ended: 2 });
assert(t && t.topic === 'turn' && t.data.thinking === true && t.data.ended === 2, 'turn projection');
t = topicOf({ type: 'route', view: 'identity', hash: '#/identity/who', base: 'b' });
assert(t && t.topic === 'route' && t.data.view === 'identity' && t.data.hash === '#/identity/who', 'route projection');

console.log('BRIDGE-OK');
process.exit(0);
`

func TestSectionBridgeContract(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not available — bridge contract check skipped")
	}
	tmp := t.TempDir()
	for src, dst := range map[string]string{
		"static/bridge.js":      "bridge.mjs",
		"static/section-api.js": "section_api.mjs",
	} {
		raw, rerr := staticFS.ReadFile(src)
		if rerr != nil {
			t.Fatalf("read embedded %s: %v", src, rerr)
		}
		if werr := os.WriteFile(filepath.Join(tmp, dst), raw, 0o600); werr != nil {
			t.Fatal(werr)
		}
	}
	driver := filepath.Join(tmp, "driver.mjs")
	if err := os.WriteFile(driver, []byte(bridgeDriver), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(node, driver).CombinedOutput()
	if err != nil || !strings.Contains(string(out), "BRIDGE-OK") {
		t.Fatalf("bridge contract failed (%v):\n%s", err, out)
	}
}
