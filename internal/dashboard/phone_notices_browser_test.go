//go:build !windows

package dashboard

import "testing"

// THE PAGE'S TWO NOTICES READ RIGHT ON A PHONE: the update notice, drawn as the
// page draws it (a changed module while work is unsaved), spans the screen less
// its margins, where it was a narrow column over the reply box; and a toast that
// is a note wears a plain edge, where every toast wore an error's red one. An
// error's toast keeps its edge. Both stand above the places' bar along the
// bottom, never over it (the lead's strips, 2026-11-03: they covered 6 px).
const phoneNoticesJS = `import { report, until } from '/__harness.js';
const sockets = window.__sockets;
const problems = [];
const check = (ok, what) => { if (!ok) problems.push(what); };
async function run() {
  await until(() => sockets.length > 0);
  sockets[0].open();
  sockets[0].say({ type: 'status', stats: { name: 'Mira', version: 'test', build: 'test', ledger_seq: 9, lifetime_ticks: 1, belief_count: 0, intention_count: 0, experience_count: 0, reflection_count: 0 } });
  const overlay = await import('/overlay.js');
  overlay.holdReloadWhile(() => true);
  overlay.onOverlayChanged(Date.now(), ['views/home.js']);
  await until(() => document.getElementById('overlay-update'));
  const notice = document.getElementById('overlay-update');
  const box = notice && notice.getBoundingClientRect();
  check(box && box.width >= innerWidth - 40, 'at ' + innerWidth + ' px the update notice is ' + (box ? Math.round(box.width) : 'not') + ' px wide, not the screen less its margins');
  const bar = document.querySelector('.c-top-bar__nav').getBoundingClientRect();
  check(bar.top > innerHeight / 2 && box.bottom <= bar.top, 'the update notice ends at ' + Math.round(box.bottom) + ', over the places\' bar from ' + Math.round(bar.top));
  const { toast } = await import('/app.js');
  const t = document.getElementById('toast');
  const probe = document.createElement('span');
  probe.style.color = 'var(--bad)';
  document.body.append(probe);
  const bad = getComputedStyle(probe).color;
  probe.remove();
  toast('An error.');
  check(getComputedStyle(t).borderLeftColor === bad, 'an error toast lost its error edge: ' + getComputedStyle(t).borderLeftColor + ', the error colour is ' + bad);
  toast('A note.', 'note');
  check(getComputedStyle(t).borderLeftColor !== bad, 'a note wears the error edge: ' + getComputedStyle(t).borderLeftColor);
  await until(() => getComputedStyle(t).transform === 'none');
  check(t.getBoundingClientRect().bottom <= bar.top, 'the toast ends at ' + Math.round(t.getBoundingClientRect().bottom) + ', over the places\' bar from ' + Math.round(bar.top));
  report(problems.length ? 'FAIL: ' + problems.join('\n  - ') : 'OK\nthe update notice ' + Math.round(box.width) + ' px wide at ' + innerWidth + '; it ends at ' + Math.round(box.bottom) + ', the toast at ' + Math.round(t.getBoundingClientRect().bottom) + ', the places\' bar from ' + Math.round(bar.top));
}
run().catch(e => report('FAIL: the run stopped: ' + ((e && e.message) || e)));
`

// A TOAST STANDS CLEAR OF THE UPDATE NOTICE. The update notice stays until the page
// reloads, so a toast that came while it showed lay over it, and neither could be
// read. It was so at a phone's width, where both span the screen, and on any screen
// narrower than the two side by side.
const toastAboveNoticeJS = `import { report, until } from '/__harness.js';
const sockets = window.__sockets;
const problems = [];
const check = (ok, what) => { if (!ok) problems.push(what); };
const apart = (a, b) => a.bottom <= b.top || b.bottom <= a.top || a.right <= b.left || b.right <= a.left;
const round = r => [r.left, r.top, r.right, r.bottom].map(Math.round).join(',');
async function run() {
  await until(() => sockets.length > 0);
  sockets[0].open();
  sockets[0].say({ type: 'status', stats: { name: 'Mira', version: 'test', build: 'test', ledger_seq: 9, lifetime_ticks: 1, belief_count: 0, intention_count: 0, experience_count: 0, reflection_count: 0 } });
  const overlay = await import('/overlay.js');
  overlay.holdReloadWhile(() => true);
  const { toast } = await import('/app.js');
  const t = document.getElementById('toast');
  overlay.onOverlayChanged(Date.now(), ['views/home.js']);
  await until(() => document.getElementById('overlay-update'));
  const notice = document.getElementById('overlay-update');
  toast('A note that came while the update notice showed.', 'note');
  await until(() => getComputedStyle(t).transform === 'none' && getComputedStyle(t).opacity === '1', 3000).catch(() => {});
  await until(() => apart(t.getBoundingClientRect(), notice.getBoundingClientRect()), 1000).catch(() => {});
  const a = t.getBoundingClientRect(), b = notice.getBoundingClientRect();
  check(apart(a, b), 'at ' + innerWidth + ' px the toast at ' + round(a) + ' lies over the update notice at ' + round(b));
  check(a.top >= 0 && a.bottom <= innerHeight, 'at ' + innerWidth + ' px the toast at ' + round(a) + ' leaves the screen');
  report(problems.length ? 'FAIL: ' + problems.join('\n  - ') : 'OK\nat ' + innerWidth + ' px the toast ' + round(a) + ', the notice ' + round(b));
}
run().catch(e => report('FAIL: the run stopped: ' + ((e && e.message) || e)));
`

// THE UPDATE NOTICE LEAVES THE REPLY BOX FREE. It stays until the page reloads, and it
// sat at the foot of the page, over the reply box in the conversation, at every width:
// "+", the words and Send could not be reached until the page was reloaded. It takes a
// row under the bar, on every page, and every control of the reply box can be pressed.
const noticeLeavesReplyFreeJS = `import { report, until } from '/__harness.js';
import { S } from '/state.js';
const sockets = window.__sockets;
const problems = [];
const check = (ok, what) => { if (!ok) problems.push(what); };
const $ = id => document.getElementById(id);
const r = el => el.getBoundingClientRect();
const round = b => [b.left, b.top, b.right, b.bottom].map(Math.round).join(',');
const apart = (a, b) => a.bottom <= b.top || b.bottom <= a.top || a.right <= b.left || b.right <= a.left;
const shown = el => !!el && el.getClientRects().length > 0;
async function run() {
  await until(() => sockets.length > 0);
  sockets[0].open();
  sockets[0].say({ type: 'status', stats: { name: 'Mira', version: 'test', build: 'test', ledger_seq: 9, lifetime_ticks: 1, belief_count: 0, intention_count: 0, experience_count: 0, reflection_count: 0 } });
  const overlay = await import('/overlay.js');
  overlay.holdReloadWhile(() => true);
  overlay.onOverlayChanged(Date.now(), ['views/home.js']);
  await until(() => $('overlay-update'));
  for (const view of ['chat']) {
    S.go(view);
    await until(() => S.view === view && shown($('send-btn')) && shown($('attach-btn')), 5000).catch(() => {});
    const notice = r($('overlay-update')), box = r(document.querySelector('.c-reply__box')), bar = r(document.querySelector('.c-top-bar'));
    check(apart(notice, box), 'in ' + view + ' at ' + innerWidth + ' px the notice at ' + round(notice) + ' lies over the reply box at ' + round(box));
    check(notice.top >= bar.bottom, 'in ' + view + ' the notice at ' + round(notice) + ' lies over the bar ending at ' + Math.round(bar.bottom));
    check(notice.bottom <= innerHeight, 'in ' + view + ' the notice at ' + round(notice) + ' leaves the screen');
    for (const id of ['attach-btn', 'msg-input', 'send-btn']) {
      const b = r($(id)), at = document.elementFromPoint((b.left + b.right) / 2, (b.top + b.bottom) / 2);
      check(at && ($(id) === at || $(id).contains(at)), 'in ' + view + ' at ' + innerWidth + ' px #' + id + ' cannot be pressed: ' + (at ? at.id || at.className : 'nothing') + ' is over it');
    }
    const now = $('overlay-update').querySelector('button'), b = r(now), at = document.elementFromPoint((b.left + b.right) / 2, (b.top + b.bottom) / 2);
    check(at === now, 'in ' + view + ' the notice\'s own button cannot be pressed');
  }
  report(problems.length ? 'FAIL: ' + problems.join('\n  - ') : 'OK');
}
run().catch(e => report('FAIL: the run stopped: ' + ((e && e.message) || e)));
`

// THE UPDATE NOTICE COVERS NOTHING. It stays until the page reloads, so it takes a row
// of the page, under the bar, and everything starts below it: a warning band and its
// Status link, a Settings heading, the reply box. A notice that hides a warning is the
// thing an honest friend does not do.
const noticeCoversNothingJS = `import { report, until } from '/__harness.js';
import { S } from '/state.js';
const sockets = window.__sockets;
const problems = [];
const check = (ok, what) => { if (!ok) problems.push(what); };
const $ = id => document.getElementById(id);
const r = el => el.getBoundingClientRect();
const round = b => [b.left, b.top, b.right, b.bottom].map(Math.round).join(',');
const shown = el => !!el && el.getClientRects().length > 0;
const say = m => sockets[sockets.length - 1].say(m);
// A control takes the press itself. Words only have to be seen, so a box around them will do.
const reachable = (el, what, where, words = false) => {
  const b = r(el), x = (b.left + b.right) / 2, y = (b.top + b.bottom) / 2;
  check(b.top >= 0 && b.bottom <= innerHeight, what + ' in ' + where + ' at ' + innerWidth + ' px is off the screen: ' + round(b));
  const at = document.elementFromPoint(x, y);
  check(at && (at === el || el.contains(at) || words && at.contains(el) && !at.closest('#overlay-update')), what + ' in ' + where + ' at ' + innerWidth + ' px is covered by ' + (at ? at.id || at.className || at.tagName : 'nothing'));
};
async function run() {
  if (sessionStorage.getItem('notice-asked')) return report('FAIL: the page reloaded over an unsaved Settings edit');
  await until(() => sockets.length > 0);
  sockets[0].open();
  say({ type: 'status', stats: { name: 'Mira', version: 'test', build: 'test', ledger_seq: 9, lifetime_ticks: 1, belief_count: 0, intention_count: 0, experience_count: 0, reflection_count: 0, credential_warning: 'your key expires soon' } });
  say({ type: 'continuity', continuity: { mode: 'normal', ledger_seq: 9 } });
  say({ type: 'config', config: { llm: { provider: 'OpenAI', model: 'm', resolved_provider: 'OpenAI', resolved_model: 'm', timeout_seconds: 120 }, speech: { stt: {}, tts: {}, services: [] }, prompt: {}, witness: {}, logs: {}, agency: {}, plugins: {}, updates: {}, restart_required: [], dashboard: {} } });
  await until(() => $('page-note').querySelector('.c-os-note__link'), 5000);
  // An ordinary unsaved edit in Settings is what keeps the notice on the page.
  S.go('settings');
  await until(() => S.view === 'settings' && shown($('cfg-model')), 8000);
  const typed = 'a-model-not-saved';
  $('cfg-model').value = typed;
  $('cfg-model').dispatchEvent(new Event('input', { bubbles: true }));
  const overlay = await import('/overlay.js');
  sessionStorage.setItem('notice-asked', '1');
  overlay.onOverlayChanged(Date.now(), ['views/home.js']);
  await until(() => $('overlay-update'));
  for (const [view, composer] of [['chat', true], ['settings', false]]) {
    S.go(view);
    await until(() => S.view === view && (composer ? shown($('send-btn')) : shown(document.querySelector('#settings-stack h2'))), 8000).catch(() => {});
    const notice = $('overlay-update'), bar = document.querySelector('.c-top-bar'), band = $('page-note');
    check(r(notice).top >= r(bar).bottom, 'in ' + view + ' the notice at ' + round(r(notice)) + ' is over the bar ending at ' + Math.round(r(bar).bottom));
    check(r(band).top >= r(notice).bottom, 'in ' + view + ' at ' + innerWidth + ' px the warning band at ' + round(r(band)) + ' is not below the notice at ' + round(r(notice)));
    reachable(notice.querySelector('button'), 'the reload button', view);
    reachable(band.querySelector('.c-os-note__link'), 'the warning\'s Status link', view);
    reachable(band.querySelector('.c-os-note__text') || band.firstElementChild, 'the warning', view, true);
    if (composer) for (const id of ['attach-btn', 'msg-input', 'send-btn']) reachable($(id), '#' + id, view);
    else {
      reachable(document.querySelector('#settings-stack h2'), 'the section\'s heading', view, true);
      reachable($('cfg-model'), 'the field being edited', view);
      check($('cfg-model').value === typed, 'the unsaved edit reads "' + $('cfg-model').value + '" after the walk');
    }
    check(document.documentElement.scrollWidth <= innerWidth, 'in ' + view + ' the page scrolls sideways');
  }
  report(problems.length ? 'FAIL: ' + problems.join('\n  - ') : 'OK');
}
run().catch(e => report('FAIL: the run stopped: ' + ((e && e.message) || e)));
`

func TestTheUpdateNoticeCoversNothing(t *testing.T) {
	for _, size := range []struct {
		name          string
		width, height int
	}{{"phone", 390, 844}, {"window", 1280, 800}} {
		t.Run(size.name, func(t *testing.T) {
			runRealPageAtSize(t, statusFakeSocketJS, noticeCoversNothingJS, homeStyles, size.width, size.height)
		})
	}
}

func TestTheUpdateNoticeLeavesTheReplyBoxFree(t *testing.T) {
	for _, size := range []struct {
		name          string
		width, height int
	}{{"phone", 390, 844}, {"narrow window", 1100, 800}, {"wide window", 1280, 800}} {
		t.Run(size.name, func(t *testing.T) {
			runRealPageAtSize(t, statusFakeSocketJS, noticeLeavesReplyFreeJS, homeStyles, size.width, size.height)
		})
	}
}

func TestAToastStandsClearOfTheUpdateNotice(t *testing.T) {
	for _, size := range []struct {
		name          string
		width, height int
	}{{"phone", 390, 844}, {"narrow window", 1100, 800}, {"wide window", 1280, 800}} {
		t.Run(size.name, func(t *testing.T) {
			runRealPageAtSize(t, statusFakeSocketJS, toastAboveNoticeJS, homeStyles, size.width, size.height)
		})
	}
}

func TestThePagesNoticesReadRightOnAPhone(t *testing.T) {
	runRealPageAtSize(t, statusFakeSocketJS, phoneNoticesJS, homeStyles, 390, 844)
}
