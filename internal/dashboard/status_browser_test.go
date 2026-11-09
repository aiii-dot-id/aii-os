//go:build !windows

package dashboard

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// SETTINGS → STATUS (spec step 5; the board SettingsStatus). The shipped
// views/settings/status.js on a page of its own, the frames set as the
// socket sets them, the reads it asks for recorded.
const statusPage = `<!doctype html>
<link rel="stylesheet" href="/tokens/tokens.css"><link rel="stylesheet" href="/views/settings/status.css">
<div id="status-stack" style="width:%WIDTH%"></div>
<script type="module">
import { S } from '/state.js';
import { renderStatus, readStatus } from '/views/settings/status.js';
import W from '/strings/status.js';
import { fill } from '/util.js';
import { run } from '/__harness.js';
const head = () => document.getElementById('status-heading').textContent;
const shown = () => !document.querySelector('.status__blocks').hidden;
const line = id => { const el = document.querySelector('[data-block="' + id + '"] .status__line'); return el && !el.hidden ? el.textContent : ''; };
const rows = id => [...document.querySelectorAll('[data-block="' + id + '"] .status__rows li')].map(li => li.textContent);
const normal = () => {
  S.firstConnect = false; S.connected = true; S.pageFrames = { status: true, continuity: true };
  S.stats = { name: 'Mira', version: '0.2.1', build: 'abc123', ledger_seq: 1320, lifetime_ticks: 120, uptime: '3h12m', belief_count: 48, reflection_count: 6, experience_count: 211, intention_count: 4, malformed_calls: 0, suspicious_paths: 0, duplicate_arg_keys: 0, last_turn: '18,400 tokens over 3 calls' };
  S.cont = { mode: 'normal', ledger_seq: 1320, witness_url: 'https://witness.test', witnessed_at: new Date(Date.now() - 7200e3).toISOString(), anchored_seq: 1300 };
  S.config = { llm: { provider: 'Example AI', model: 'm1', resolved_provider: 'Example AI', resolved_model: 'm1' }, cognition: { pulse_seconds: 60 }, plugins: { installed: [{ name: 'p' }] } };
  S.providers = [{ name: 'Example AI', context_length: 128000 }];
  S.tools = [{ name: 'read', enabled: true }, { name: 'web_fetch', enabled: false }];
  S.work = { live: [], queued: 0, delivered: [] }; S.activeProject = null; S.asks = []; S.overlays = []; S.thinking = false;
  S.spend = null;
};
run(async assert => {
%SCRIPT%
});
</script>`

func runStatusPage(t *testing.T, width, script string) {
	t.Helper()
	page := strings.Replace(strings.Replace(statusPage, "%WIDTH%", width, 1), "%SCRIPT%", script, 1)
	runPageInEngines(t, page, map[string][]byte{
		"/ws.js": []byte("export function query(q, extra) { (window.__asked ||= []).push(q + (extra ? JSON.stringify(extra) : '')); return 'q'; }\nexport function send(m) { (window.__sent ||= []).push(m); m.request_id = m.request_id || 'r' + window.__sent.length; return m.request_id; }\n"),
	})
}

// THE HEADING IS THE ANSWER (spec step 5): "Everything is working" only
// while every surface the page-wide note reads is normal; otherwise that
// note's words, each other note under it. Nothing is claimed before both
// frames have been read since the connection was made, and without a
// connection only that is said.
func TestStatusHeadingIsTheAnswer(t *testing.T) {
	runStatusPage(t, "800px", `
  S.connected = false; S.pageFrames = { status: false, continuity: false }; renderStatus();
  assert(head() === W.offline && !shown(), 'without a connection Status said more than that: ' + head());
  S.connected = true; renderStatus();
  assert(head() === 'Loading…' && !shown(), 'connected with neither frame read, Status claimed: ' + head());
  normal(); S.pageFrames.continuity = false; renderStatus();
  assert(head() === 'Loading…' && !shown(), 'with only the status read, Status claimed: ' + head());
  normal(); renderStatus();
  assert(head() === W.allWell && shown(), 'every surface normal, the heading is not the answer: ' + head());
  const tone = () => document.getElementById('status-heading').dataset.tone || '';
  assert(tone() === 'good', 'all well, the heading\'s tone is not good: ' + tone());
  const cases = [
    ['safe mode', () => { S.cont.mode = 'safe'; S.cont.safe_reason = 'their record did not verify'; }, fill(W.safeMode, { name: 'Mira', reason: 'their record did not verify' })],
    ['principles', () => { S.cont.mode = 'degraded_ring5'; }, fill(W.waitingPrinciples, { name: 'Mira' })],
    ['a credential', () => { S.stats.credential_warning = 'the key expires tomorrow'; }, fill(W.credentialWarning, { what: 'the key expires tomorrow' })],
  ];
  for (const [what, set, words] of cases) {
    normal(); set(); renderStatus();
    assert(head() === words, what + ': the heading is not AII OS\'s note: ' + head());
    assert(tone() === (what === 'safe mode' ? 'bad' : 'warn'), what + ': the heading\'s tone is not the note\'s: ' + tone());
    assert(!document.getElementById('status').textContent.includes(W.allWell), what + ': "' + W.allWell + '" is said beside it');
  }
  normal(); S.cont.mode = 'safe'; S.cont.safe_reason = 'r'; S.stats.credential_warning = 'k'; renderStatus();
  const under = [...document.querySelectorAll('.status__note')].map(p => p.textContent);
  assert(head() === fill(W.safeMode, { name: 'Mira', reason: 'r' }) && under.join() === fill(W.credentialWarning, { what: 'k' }), 'two notes are not the heading and the one under it: ' + head() + ' / ' + under.join(' | '));
  normal(); renderStatus();
  assert(head() === W.allWell && !document.querySelector('.status__note'), 'cleared, the heading did not come back: ' + head());`)
}

// THE FIVE BLOCKS SAY ONLY WHAT HAS A SOURCE (ruled 2026-10-31): each
// sentence from the frames, and nothing for a value no frame carries. Its
// reads are the frames it draws and the month's spend.
func TestStatusSaysOnlyWhatItHasASourceFor(t *testing.T) {
	runStatusPage(t, "800px", `
  readStatus();
  const asked = window.__asked || [];
  for (const q of ['status', 'continuity', 'config', 'providers', 'tools', 'work', 'asks']) assert(asked.includes(q), 'Status does not read ' + q + ': ' + asked.join());
  assert(asked.includes('spend{"days":32}'), 'Status does not read the last 32 days\' spend: ' + asked.join());
  normal(); renderStatus();
  assert(line('trust') === fill(W.trustLine, { name: 'Mira', model: 'm1', provider: 'Example AI' }), 'trust: ' + line('trust'));
  // At rest there is no Context: the window is in Model and cost's Details (the first-session review, change 4).
  assert(!document.querySelector('[data-block="context"]') && rows('modelCost').includes(fill(W.windowTokens, { n: (128000).toLocaleString() })), 'the model\'s window is not under Model and cost, or Context is still a block: ' + rows('modelCost').join(' | '));
  assert(rows('modelCost').includes(fill(W.lastTurn, { when: '18,400 tokens over 3 calls' })), 'the last turn\'s cost is not under Model and cost: ' + rows('modelCost').join(' | '));
  assert(line('continuity').startsWith('Their record has ' + (1320).toLocaleString() + ' entries, verified ') && !line('continuity').includes('Working on'), 'continuity: ' + line('continuity'));
  assert(line('tools') === fill(W.toolsCountOnePlugin, { tools: 2 }) + ' ' + fill(W.toolsOff, { off: 1 }), 'tools: ' + line('tools'));
  assert(line('modelCost') === '', 'a month\'s spend was said with no read of the month: ' + line('modelCost'));
  assert(line('flow') === fill(W.flowLineNow, { name: 'Mira', state: W.stateWaiting, asks: 'Nothing' }), 'flow: ' + line('flow'));
  S.stats.name = 'Unnamed'; renderStatus();
  assert(line('trust').startsWith('Your identity thinks with ') && line('flow').startsWith('Your identity is '), 'before they have a name Status calls them otherwise: ' + line('trust') + ' | ' + line('flow'));
  S.stats.name = 'Mira'; renderStatus();
  // The month's spend: Status's own read, this month in the identity's zone.
  const now = new Date(), month = new Intl.DateTimeFormat('en-CA', { timeZone: 'UTC', year: 'numeric', month: '2-digit' }).format(now);
  const lastMonth = new Date(Date.UTC(now.getUTCFullYear(), now.getUTCMonth(), 0)).toISOString().slice(0, 10);
  const spent = floor => ({ zone: 'UTC', days: [{ day: month + '-02', tokens: 300, floor }, { day: lastMonth, tokens: 900 }] });
  S.spend = spent(false); S.spendRequest = 'another'; renderStatus();
  assert(line('modelCost') === '', 'a spend read not Status\'s own was said as the month: ' + line('modelCost'));
  S.spendRequest = 'q'; renderStatus();
  assert(line('modelCost') === fill(W.modelCostTokens, { tokens: (300).toLocaleString() }), 'the month\'s spend, last month left out: ' + line('modelCost'));
  S.spend = spent(true); renderStatus();
  assert(line('modelCost') === fill(W.modelCostTokensFloor, { tokens: (300).toLocaleString() }), 'a month with a call that did not report is not "at least": ' + line('modelCost'));
  S.spend = { zone: 'UTC', days: [{ day: lastMonth, tokens: 900 }] }; renderStatus();
  assert(line('modelCost') === fill(W.modelCostTokens, { tokens: '0' }), 'a month that spent nothing is not said as nothing: ' + line('modelCost'));
  // Safe mode, and principles not yet loaded: the trust sentence never says "off; loaded".
  normal(); S.cont.mode = 'safe'; S.cont.safe_reason = 'r'; renderStatus();
  assert(line('trust') === fill(W.trustSafe, { name: 'Mira', model: 'm1', provider: 'Example AI', reason: 'r' }), 'safe trust: ' + line('trust'));
  normal(); S.cont.mode = 'degraded_ring5'; renderStatus();
  assert(line('trust') === fill(W.trustWaiting, { name: 'Mira', model: 'm1', provider: 'Example AI' }), 'waiting trust: ' + line('trust'));
  // Nothing known, nothing said: no window, no turn, no tools read.
  // Nothing switched off says nothing of it; many plugins are many.
  normal(); S.tools = [{ name: 'read', enabled: true }, { name: 'write', enabled: true }]; S.config.plugins.installed = [{ name: 'a' }, { name: 'b' }]; renderStatus();
  assert(line('tools') === fill(W.toolsCount, { tools: 2, plugins: 2 }), 'tools with nothing switched off: ' + line('tools'));
  normal(); S.providers = []; S.stats.last_turn = ''; S.tools = []; renderStatus();
  assert(line('tools') === '', 'a sentence was said with nothing to say it from: ' + line('tools'));
  // The press's own hint says what it does, not a value: it is left out of the reading.
  assert(!document.getElementById('status').textContent.replace(W.flowRunSoonerHint, '').match(/typical reply|failover|next reflection|left out/i), 'Status said a value no frame carries');`)
}

// STATUS CHANGES WHILE IT IS OPEN (spec step 5), on the real page through
// its own socket: a credential warning appears and clears, a disconnection
// shows, work and spend read back, and the page is never left: the same
// Status, its Details still open.
func TestStatusChangesWhileOpen(t *testing.T) {
	runStatusMeasurement(t, statusFakeSocketJS, `import { report, until } from '/__harness.js';
import { S } from '/state.js';
const sockets = window.__sockets, sent = window.__sent;
const live = () => sockets[sockets.length - 1];
const say = m => live().say(m);
const tick = () => new Promise(r => setTimeout(r, 40));
const head = () => (document.getElementById('status-heading') || {}).textContent || '';
const line = id => (document.querySelector('[data-block="' + id + '"] .status__line') || {}).textContent || '';
const problems = [];
const check = (ok, what) => { if (!ok) problems.push(what); };
const stats = { name: 'Mira', version: 'test', build: 'test', ledger_seq: 3, lifetime_ticks: 1, belief_count: 0, intention_count: 0, experience_count: 0, reflection_count: 0 };
const frames = () => { say({ type: 'status', stats }); say({ type: 'continuity', continuity: { mode: 'normal', ledger_seq: 3 } }); };
async function run() {
  await until(() => sockets.length > 0);
  sockets[0].open(); await tick();
  frames();
  say({ type: 'config', config: { llm: { provider: 'Example AI', model: 'm1', resolved_provider: 'Example AI', resolved_model: 'm1' }, speech: {}, prompt: {}, witness: {}, logs: {}, agency: {}, plugins: {}, updates: {}, restart_required: [], dashboard: {} } });
  location.hash = '#/settings'; await until(() => S.view === 'status' && head() === 'Everything is working', 5000);
  check(head() === 'Everything is working', '#/settings did not open Status with its answer: view ' + S.view + ', heading "' + head() + '"');
  const page = document.getElementById('status');
  const details = document.querySelector('[data-block="continuity"] .status__details');
  details.querySelector('summary').click(); await tick();
  // A credential warning appears, and clears.
  say({ type: 'status', stats: Object.assign({}, stats, { credential_warning: 'the key expires tomorrow' }) }); await tick();
  check(head().includes('the key expires tomorrow'), 'the warning did not reach the heading while Status was open: ' + head());
  const note = document.getElementById('page-note'), box = note.getBoundingClientRect();
  check(note.textContent.includes('the key expires tomorrow') && box.width <= 1 && box.height <= 1, 'on Status the note is drawn twice, or is gone from the page: ' + box.width + 'x' + box.height);
  say({ type: 'status', stats }); await tick();
  check(head() === 'Everything is working', 'the warning did not clear while Status was open: ' + head());
  // Work and spend read back.
  say({ type: 'work', work: { live: [{ id: 'w1', description: 'the grant letter', status: 'live' }], queued: 0, delivered: [] } }); await tick();
  check(line('continuity').includes('the grant letter') && line('flow').includes('the grant letter'), 'work did not read back: ' + line('continuity') + ' / ' + line('flow'));
  const spendAsks = () => sent.filter(f => f.type === 'query' && f.query === 'spend');
  const asked = spendAsks().at(-1);
  check(!!asked && asked.days === 32, 'Status did not ask for its own 32 days of spend: ' + JSON.stringify(asked));
  const today = new Date().toISOString().slice(0, 10);
  const month = tokens => ({ zone: 'UTC', days: [{ day: today, tokens }] });
  if (asked) {
    say({ type: 'spend', request_id: asked.request_id, spend: month(1000) }); await tick();
    check(line('modelCost').startsWith((1000).toLocaleString() + ' tokens this month'), 'spend did not read back: ' + line('modelCost'));
    // SPEND IS READ AGAIN WHEN SPEND WAS ACCOUNTED, never on the last turn's
    // sentence. A turn ends while Status is open: Status reads its spend again.
    const reads = () => spendAsks().length;
    const turnEnds = () => say({ type: 'response', role: 'identity', message: 'Done.', done: true });
    const used = Object.assign({}, stats, { last_turn: '1,500 tokens over 2 calls', accounted: 1 });
    say({ type: 'status', stats: used }); await tick();
    let n = reads(); turnEnds(); await tick();
    const again = spendAsks().at(-1);
    check(reads() === n + 1 && again.request_id !== asked.request_id && again.days === 32, 'a turn\'s end did not bring a new read of the spend: ' + JSON.stringify(again));
    if (reads() === n + 1) {
      say({ type: 'spend', request_id: asked.request_id, spend: month(9999) }); await tick();
      check(line('modelCost').startsWith((1000).toLocaleString() + ' tokens'), 'an older read\'s answer was summed: ' + line('modelCost'));
      say({ type: 'spend', request_id: again.request_id, spend: month(2500) }); await tick();
      check(line('modelCost').startsWith((2500).toLocaleString() + ' tokens this month'), 'the new read did not update the month: ' + line('modelCost'));
    }
    // A second turn of the same usage: the sentence is the same, the spend is not.
    n = reads(); turnEnds(); say({ type: 'status', stats: Object.assign({}, used, { accounted: 2 }) }); await tick();
    const second = spendAsks().at(-1);
    check(reads() > n, 'a second turn of the same usage brought no new read of the spend');
    if (reads() > n) {
      say({ type: 'spend', request_id: second.request_id, spend: month(4000) }); await tick();
      check(line('modelCost').startsWith((4000).toLocaleString() + ' tokens this month'), 'the second turn\'s spend did not reach the month: ' + line('modelCost'));
    }
    // A facility's call (the brief, the rhythm): no turn ends; the count of accounted calls moves.
    n = reads(); say({ type: 'status', stats: Object.assign({}, used, { accounted: 3 }) }); await tick();
    const facility = spendAsks().at(-1);
    check(reads() === n + 1, 'spend accounted with no turn brought no new read of the spend');
    if (reads() === n + 1) {
      say({ type: 'spend', request_id: facility.request_id, spend: month(4300) }); await tick();
      check(line('modelCost').startsWith((4300).toLocaleString() + ' tokens this month'), 'a facility\'s spend did not reach the month: ' + line('modelCost'));
    }
    // A status frame that accounted nothing new reads nothing.
    n = reads(); say({ type: 'status', stats: Object.assign({}, used, { accounted: 3, uptime: '3h13m' }) }); await tick();
    check(reads() === n, 'a status frame with nothing new accounted read the spend again');
  }
  // The press goes out on the socket, and the door's answer comes back beside it.
  const press = [...document.querySelectorAll('[data-block="flow"] button')].find(b => b.textContent === 'Reflect now');
  const said = () => (document.querySelector('[data-block="flow"] .status__answer') || {}).textContent || '';
  check(!!press, 'Flow\'s Details carry no press');
  if (press) {
    press.click(); await tick();
    const ask = sent.filter(f => f.type === 'pass_now').at(-1);
    check(!!ask, 'the press sent nothing');
    if (ask) { say({ type: 'pass_now', request_id: ask.request_id, pass_now: { moved: true, reason: '', asked_at: new Date().toISOString() } }); await tick(); }
    check(said().startsWith('Asked at '), 'the door\'s answer did not come back beside the press: ' + said());
    // Asked again, and the daemon answers with an error frame of its own:
    // another request's error ends nothing; its own ends the attempt in the
    // daemon's words, the confirmed ask kept and the press free.
    press.click(); await tick();
    const errored = sent.filter(f => f.type === 'pass_now').at(-1);
    say({ type: 'error', request_id: 'another-request', message: 'another request failed' }); await tick();
    check(press.disabled, 'another request\'s error ended this press');
    if (errored) { say({ type: 'error', request_id: errored.request_id, message: 'pass_now: not available' }); await tick(); }
    check(!press.disabled && said().startsWith('Asked at ') && said().endsWith('pass_now: not available'), 'the press\'s own error did not end it in the daemon\'s words beside the confirmed ask: "' + said() + '", busy ' + press.disabled);
    // Asked again, and the connection drops before the answer.
    press.click(); await tick();
    check(press.disabled, 'the second press is not on its way');
  }
  // A disconnection shows; a reconnect says nothing until both frames come again.
  const before = sockets.length;
  live().drop(); await tick();
  check(head() === 'Not connected. Reconnecting…', 'a disconnection did not show: ' + head());
  await until(() => sockets.length > before, 20000);
  live().open(); await tick();
  check(head() === 'Loading…', 'reconnected, Status claimed before its frames: ' + head());
  frames(); await tick();
  check(head() === 'Everything is working', 'after both frames came again, the answer did not: ' + head());
  if (press) check(!press.disabled && said().startsWith('Asked at ') && said().endsWith('The connection dropped before AII OS answered. Ask again if you wish.'),
    'after the lost answer the press is not free, or the confirmed ask or the unconfirmed attempt is not said: "' + said() + '", busy ' + press.disabled);
  check(S.view === 'status' && location.hash === '#/settings' && document.getElementById('status') === page, 'Status was left or drawn anew: ' + S.view + ' ' + location.hash);
  check(details.isConnected && details.open, 'an open Details closed under a redraw');
  report(problems.length ? 'FAIL: ' + problems.join('\n  - ') : 'OK');
}
run().catch(e => report('FAIL: the run stopped: ' + ((e && e.message) || e)));
`)
}

// "RUN THEIR NEXT PASS NOW" ASKS THE DOOR AND SAYS ITS ANSWER BESIDE ITSELF
// (F-COG-110, ruled 2026-10-31): the press sends pass_now; moved, it says
// "Asked at {time}" and never that a pass ran, and that confirmed ask stands
// through every later press, refusal, loss and redraw; refused, it says why
// in words for each typed reason, beside the confirmed ask; a lost
// connection ends the attempt unconfirmed and frees the press. An answer is
// this ask's only by its exact request id and its own pass_now member; any
// other frame is ignored and takes nothing away. The line it says it in is
// in the page before.
func TestStatusPressAsksTheDoor(t *testing.T) {
	runStatusPage(t, "800px", `
  normal(); renderStatus();
  const flow = document.querySelector('[data-block="flow"]');
  const press = [...flow.querySelectorAll('button')].find(b => b.textContent === W.flowRunSooner);
  const answer = () => flow.querySelector('.status__answer').textContent;
  const last = () => window.__sent.at(-1).request_id;
  assert(press && !flow.querySelector('.status__details').hidden, 'Flow\'s Details do not carry the press');
  assert(flow.querySelector('.status__answer').getAttribute('aria-live') === 'polite' && answer() === '', 'the answer\'s line is not there, empty, before the press');
  press.click();
  assert(window.__sent.length === 1 && window.__sent[0].type === 'pass_now', 'the press did not ask the door: ' + JSON.stringify(window.__sent));
  assert(press.disabled && press.getAttribute('aria-busy') === 'true', 'the press is not busy while its answer is on its way');
  const at = new Date(2026, 9, 31, 4, 41).toISOString(), asked = fill(W.passAsked, { time: new Date(at).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }) });
  // Not this ask's answer: no id, another id, or no pass_now member. Each is ignored and frees nothing.
  S.passNowAnswered({ type: 'pass_now', pass_now: { moved: true, asked_at: at } });
  S.passNowAnswered({ type: 'pass_now', request_id: 'another', pass_now: { moved: true, asked_at: at } });
  S.passNowAnswered({ type: 'pass_now', request_id: last(), moved: true, asked_at: at });
  assert(answer() === '' && press.disabled, 'a frame that is not this ask\'s answer was taken, or freed the press: "' + answer() + '"');
  S.passNowAnswered({ type: 'pass_now', request_id: last(), pass_now: { moved: true, reason: '', asked_at: at } });
  assert(answer() === asked, 'moved, the answer is not "Asked at": ' + answer());
  assert(!/\bran\b/i.test(answer()) && !press.disabled, 'the answer says ran for asked, or the press stayed busy: ' + answer());
  // Refused later: the reason beside the confirmed ask, which stands.
  const reasons = { turn_holding: fill(W.passTurn, { name: 'Mira' }), stall: W.passStall, budget_hold: W.passBudget,
    safe: fill(W.passSafe, { name: 'Mira' }), in_flight: W.passInFlight, paused: W.passPaused, already_due: W.passDue, changed: W.passChanged,
    not_recurring: fill(W.passNotRecurring, { name: 'Mira' }), absent: fill(W.passAbsent, { name: 'Mira' }) };
  for (const [reason, words] of Object.entries(reasons)) {
    press.click();
    S.passNowAnswered({ type: 'pass_now', request_id: last(), pass_now: { moved: false, reason } });
    assert(answer() === asked + ' ' + words, reason + ' is not said in its words beside the confirmed ask: ' + answer());
  }
  // A connection lost before the answer: unconfirmed, the press free again, the confirmed ask kept.
  press.click();
  S.passNowLost();
  assert(answer() === asked + ' ' + W.passUnconfirmed && !press.disabled, 'a lost answer did not end the attempt unconfirmed: "' + answer() + '", busy ' + press.disabled);
  S.stats = Object.assign({}, S.stats, { belief_count: 50 }); renderStatus();
  assert(answer() === asked + ' ' + W.passUnconfirmed, 'a redraw lost the answer: ' + answer());`)
}

// A COUNT OF ONE IS ONE, AND A DURATION IS SAID IN WORDS: the Details rows
// say "1 belief", not "1 beliefs", and how long AII OS has run in this
// browser's words for its largest whole unit, not the daemon's "3h12m5s".
func TestStatusSaysCountsAndTimeAsWords(t *testing.T) {
	runStatusPage(t, "800px", `
  normal(); Object.assign(S.stats, { belief_count: 1, experience_count: 1, intention_count: 1, reflection_count: 1, malformed_calls: 1, foreground_holds: 1, lifetime_ticks: 60, uptime: '3h12m5s' }); renderStatus();
  const said = rows('continuity').concat(rows('tools'), rows('flow'));
  for (const one of [fill(W.beliefOne, { n: 1 }), fill(W.experienceOne, { n: 1 }), fill(W.intentionOne, { n: 1 }), fill(W.reflectionOne, { n: 1 }),
    fill(W.oddCallOne, { n: 1 }), fill(W.foregroundHoldOne, { n: 1 }), fill(W.lifetimeOne, { hours: 1 })]) {
    assert(said.includes(one), 'a count of one is not said as one: wanted "' + one + '" in ' + said.join(' | '));
  }
  const hours = new Intl.NumberFormat([], { style: 'unit', unit: 'hour', unitDisplay: 'long' }).format(3);
  assert(rows('flow').includes(fill(W.uptime, { duration: hours })), 'the time running is not said in words: ' + rows('flow').join(' | '));
  S.stats.uptime = '50h0m0s'; renderStatus();
  const days = new Intl.NumberFormat([], { style: 'unit', unit: 'day', unitDisplay: 'long' }).format(2);
  assert(rows('flow').includes(fill(W.uptime, { duration: days })), 'two days running is not said in days: ' + rows('flow').join(' | '));
  assert(!document.getElementById('status').textContent.match(/\d+h\d+m|call\(s\)/), 'a machine\'s form reached the page');`)
}

// A REDRAW WITH NOTHING NEW WRITES NOTHING (the panel's old law, now
// Status's): the same facts again do not touch the page, a changed fact
// does, and an open Details stays open through it.
func TestStatusIsQuietWhenNothingChanged(t *testing.T) {
	runStatusPage(t, "800px", `
  normal(); renderStatus();
  const stack = document.getElementById('status-stack');
  let writes = 0;
  new MutationObserver(m => { writes += m.length; }).observe(stack, { subtree: true, childList: true, characterData: true, attributes: true });
  const details = document.querySelector('[data-block="continuity"] .status__details');
  details.open = true;
  await new Promise(r => setTimeout(r, 0)); writes = 0;
  renderStatus(); renderStatus();
  await new Promise(r => setTimeout(r, 0));
  assert(writes === 0, 'unchanged facts wrote to the page ' + writes + ' times');
  S.stats = Object.assign({}, S.stats, { belief_count: 49 }); renderStatus();
  await new Promise(r => setTimeout(r, 0));
  assert(writes > 0 && rows('continuity').includes(fill(W.beliefs, { n: 49 })), 'a changed fact was not drawn: ' + rows('continuity').join(' | '));
  assert(details.isConnected && details.open, 'an open Details closed when a fact changed');`)
}

// THE BUILD REACHES A SCREEN (the panel's old row, now Status's Trust):
// the version, the commit and a dirty flag, as the daemon says them.
func TestTheBuildReachesStatus(t *testing.T) {
	runStatusPage(t, "800px", `
  normal(); S.stats.version = '0.1.0'; S.stats.build = 'ee418ce4ff77 (dirty)'; renderStatus();
  const row = rows('trust').find(r => r.includes('0.1.0')) || '';
  assert(row.includes('ee418ce4ff77') && row.includes('dirty'), 'the build is not in Status\'s Trust as the daemon says it: ' + rows('trust').join(' | '));`)
}

// THREE STATES OF VERIFICATION, NEVER ONE (the panel's old law): no witness
// set says nothing of it; a witness not yet reached says so; a verified
// record says through which entry, so entries since read as not yet.
func TestStatusSaysTheRecordsState(t *testing.T) {
	runStatusPage(t, "800px", `
  normal(); S.cont.witness_url = ''; S.cont.witnessed_at = ''; renderStatus();
  assert(!rows('continuity').some(r => /verif/i.test(r)) && line('continuity') === fill(W.continuityEntries, { entries: (1320).toLocaleString() }), 'no witness: ' + line('continuity') + ' / ' + rows('continuity').join(' | '));
  normal(); S.cont.witnessed_at = ''; renderStatus();
  assert(rows('continuity').includes(W.notYetVerified), 'a witness not yet reached is not said: ' + rows('continuity').join(' | '));
  normal(); renderStatus();
  assert(rows('continuity').some(r => r.startsWith('Verified through entry ' + (1300).toLocaleString()) && r.endsWith('witness.test')), 'a verified record does not say through which entry: ' + rows('continuity').join(' | '));
  assert(line('continuity').includes('verified'), 'a verified record does not say so at rest: ' + line('continuity'));
  normal(); S.stats.malformed_calls = 2; S.stats.duplicate_arg_keys = 1; renderStatus();
  assert(rows('tools').includes(fill(W.oddCalls, { n: 3 })), 'tool calls that did not go as expected are not counted: ' + rows('tools').join(' | '));`)
}

// WHERE THEY WORK AND WHAT THEY ARE DOING are two facts, and Status says
// both (the operator's report of 2026-08-27, kept from the panel):
// Continuity names the project they work in, Flow the session running.
func TestStatusSaysWhereAndWhatTheyWorkOn(t *testing.T) {
	runStatusPage(t, "800px", `
  normal(); S.activeProject = { id: 'p1', name: 'project-menu' }; S.work = { live: [{ id: 'w1', description: 'the grant letter' }], queued: 0, delivered: [] }; renderStatus();
  assert(line('continuity').startsWith(fill(W.continuityWork, { work: 'project-menu' })), 'the project is not said: ' + line('continuity'));
  assert(line('flow').includes(fill(W.stateWorking, { work: 'the grant letter' })), 'the session is not said: ' + line('flow'));
  normal(); S.activeProject = { id: 'p1', name: 'project-menu' }; renderStatus();
  assert(line('continuity').startsWith(fill(W.continuityWork, { work: 'project-menu' })) && line('flow').includes(W.stateWaiting), 'a project with no session: ' + line('continuity') + ' / ' + line('flow'));
  normal(); S.thinking = true; renderStatus();
  assert(line('flow').includes(W.stateAnswering), 'a turn running is not "answering": ' + line('flow'));
  // The questions waiting, listed under Details, are not said again: each
  // ask's card said it once, as it was made.
  normal(); S.asks = [{ id: 'k1', kind: 'clarify', text: 'Which week do you mean?' }]; renderStatus();
  const ask = [...document.querySelectorAll('[data-block="flow"] .status__rows li')].find(li => li.textContent === 'Which week do you mean?');
  assert(ask && !ask.closest('[aria-live], [role="status"], [role="alert"], [role="log"]'), 'a waiting question is not listed, or sits in a live region');`)
}

// WHAT THEIR PAGE CHANGES CAME TO is said in Tools' Details, each change's
// file and AII OS's sentence whole (the worst case is the fork warning),
// wrapped inside its block; with none, nothing.
func TestStatusSaysWhatTheirPageChangesCameTo(t *testing.T) {
	runStatusPage(t, "360px", `
  normal(); renderStatus();
  assert(rows('tools').length === 0 && document.querySelector('[data-block="tools"] .status__details').hidden, 'with no change asked for, Tools\' Details say something');
  const fork = 'forked: views/operator-panel.js replaces the shipped module of that name; it no longer receives upgrades, and what it does is the identity\'s own';
  S.overlays = [{ path: '/custom.js', outcome: 'held: a script awaiting your approval' }, { path: '/views/operator-panel.js', outcome: fork }]; renderStatus();
  const details = document.querySelector('[data-block="tools"] .status__details'); details.open = true;
  const said = rows('tools');
  assert(said.includes('/custom.js held: a script awaiting your approval') && said.includes('/views/operator-panel.js ' + fork), 'a change is not said with its file and AII OS\'s words: ' + said.join(' | '));
  const block = document.querySelector('[data-block="tools"]').getBoundingClientRect();
  for (const li of document.querySelectorAll('[data-block="tools"] .status__rows li')) {
    const r = li.getBoundingClientRect();
    assert(r.width > 0 && r.height > 0, 'a row drew with no geometry');
    assert(r.right <= block.right + 0.5, 'a row runs out of its block: ' + r.right + ' > ' + block.right);
  }
  S.overlays = []; renderStatus();
  assert(rows('tools').length === 0, 'a change no longer there is still said');`)
}

// A LONG READING WRAPS INSIDE ITS BLOCK (the panel's credential row law):
// a whole sentence in a Details row and a line stays within the block's
// width, grows downward, and overlaps nothing, at a phone's width too.
func TestStatusHoldsALongReading(t *testing.T) {
	runStatusPage(t, "320px", `
  normal(); S.stats.credential_warning = 'the adopted credential for Example AI expires in 2 hours and cannot be renewed here: sign in again from Settings → Model, or the identity stops thinking when it lapses'; renderStatus();
  const details = document.querySelector('[data-block="trust"] .status__details'); details.open = true;
  const block = document.querySelector('[data-block="trust"]');
  const b = block.getBoundingClientRect();
  assert(b.width > 0 && b.height > 0, 'the block drew with no geometry');
  assert(block.scrollWidth <= block.clientWidth + 1, 'the block scrolls sideways: ' + block.scrollWidth + ' > ' + block.clientWidth);
  const lis = [...block.querySelectorAll('.status__rows li')];
  const long = lis.find(li => li.textContent.includes('cannot be renewed'));
  assert(long, 'the warning is not in Trust\'s Details');
  const r = long.getBoundingClientRect();
  assert(r.right <= b.right + 0.5 && r.height > parseFloat(getComputedStyle(long).lineHeight) * 1.5, 'the reading did not wrap inside its block: ' + r.width + 'x' + r.height);
  for (let i = 1; i < lis.length; i++) assert(lis[i].getBoundingClientRect().top >= lis[i - 1].getBoundingClientRect().bottom - 0.5, 'two rows overlap');`)
}

// EVERY FIELD HAS A PLACE (spec 1f, step 5): each field of the status and
// continuity frames, and the identity's (step 10), as wire.go sends them,
// names in registry.js the place that shows it, and each frame names its
// row of 1f's table; a place for a field the wire does not send is stale
// and fails too.
func TestEveryFieldHasAPlace(t *testing.T) {
	frames := map[string][]string{}
	rows := map[string]string{}
	for _, f := range []struct {
		v   any
		row string
	}{{StatsResponse{}, "Status and continuity"}, {ContinuityState{}, "Status and continuity"},
		{IdentityState{}, "The identity"}, {IntentionItem{}, "The identity"}, {RelationshipItem{}, "The identity"}, {CharterApproval{}, "The identity"}} {
		ty := reflect.TypeOf(f.v)
		frames[ty.Name()] = jsonFieldNames(ty)
		rows[ty.Name()] = f.row
	}
	rowsJSON, err := json.Marshal(rows)
	if err != nil {
		t.Fatal(err)
	}
	want, err := json.Marshal(frames)
	if err != nil {
		t.Fatal(err)
	}
	page := `<!doctype html><script type="module">
import { FIELDS } from '/registry.js';
import { run } from '/__harness.js';
const want = ` + string(want) + `, rows = ` + string(rowsJSON) + `;
run(assert => {
  for (const [frame, fields] of Object.entries(want)) {
    const placed = FIELDS[frame];
    assert(placed && placed.row === rows[frame], frame + ' names no row of 1f\'s table, or not its own: ' + (placed && placed.row));
    const missing = fields.filter(f => !String((placed.fields || {})[f] || '').trim());
    assert(!missing.length, frame + ': no place for ' + missing.join(', '));
    const stale = Object.keys(placed.fields).filter(f => !fields.includes(f));
    assert(!stale.length, frame + ': a place for a field the wire does not send: ' + stale.join(', '));
  }
});
</script>`
	runPageInEngines(t, page, nil)
}

// jsonFieldNames are a struct's fields by their wire names, embedded
// structs' among them.
func jsonFieldNames(ty reflect.Type) []string {
	var names []string
	for i := 0; i < ty.NumField(); i++ {
		f := ty.Field(i)
		if f.Anonymous && f.Type.Kind() == reflect.Struct {
			names = append(names, jsonFieldNames(f.Type)...)
			continue
		}
		name := strings.Split(f.Tag.Get("json"), ",")[0]
		if name == "" || name == "-" || !f.IsExported() {
			continue
		}
		names = append(names, name)
	}
	return names
}
