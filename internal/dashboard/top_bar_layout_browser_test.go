//go:build !windows

package dashboard

import (
	"fmt"
	"strings"
	"testing"
)

// THE BAR AND THE SIDEBAR HOLD AT EVERY WIDTH. On the shipped page, with a long project in focus
// and a reply under way: on a computer the sidebar's head is their mark, large, their name under it
// and the state under that, and the bar beside it is the theme switch and Search; on a phone the bar
// is their mark, their name, the theme switch and Search; nothing else; the sidebar holds its links, each on one line with its word inside it, and at a phone's width
// four of them are the bar along the bottom. Nothing runs past the bar, the sidebar or the page. The
// state is under their name, and not drawn on a phone; the pills are nowhere. The project is one line
// above the reply box, cut at the box's width, a link to its page; with none in focus nothing is
// drawn. A long name is cut at its end and pushes nothing out. Talk and its menu read as one control.
// The widths are the ones the operator looked at: Chrome at 150% on a 1280 window is 853 CSS pixels.
const topBarLayoutJS = `
import { report, until } from '/__harness.js';
import { S } from '/state.js';
const LONG = 'Verified-failure lessons: an experimentally validated learning mechanism';
const norm = s => (s || '').replace(/\s+/g, ' ').trim();
const shown = el => { if (!el || !el.getClientRects().length) return false; const c = getComputedStyle(el); return c.visibility !== 'hidden' && c.display !== 'none'; };
const tick = () => new Promise(r => setTimeout(r, 15));
const sockets = window.__sockets, sent = window.__sent;
const say = f => sockets[0].say(f);
const problems = [];
const check = (ok, what) => { if (!ok) problems.push(what); };
const stats = { name: 'Quill', version: 'test', build: 'test', ledger_seq: 7, lifetime_ticks: 1, belief_count: 0, intention_count: 0, experience_count: 0, reflection_count: 0 };
const config = { llm: { provider: 'P', model: 'm', resolved_provider: 'P', resolved_model: 'm', timeout_seconds: 120 }, speech: {}, prompt: {}, witness: {}, logs: {}, agency: {}, plugins: {}, updates: {}, restart_required: [], dashboard: {} };

function barHolds(when) {
  const w = document.documentElement.clientWidth, bar = document.getElementById('topbar'), rail = document.querySelector('.c-rail'), phone = w <= 767;
  check(document.documentElement.scrollWidth <= w, when + ': the page runs ' + (document.documentElement.scrollWidth - w) + ' px past its width');
  check(bar.scrollWidth <= bar.clientWidth, when + ': the bar runs ' + (bar.scrollWidth - bar.clientWidth) + ' px past itself');
  const inBar = [...bar.querySelectorAll('a, button')].filter(shown).map(el => el.className);
  check(inBar.length === (phone ? 3 : 2) && inBar.some(c => c.includes('c-top-bar__who')) === phone && inBar.some(c => c.includes('theme-toggle')) && inBar.some(c => c.includes('c-top-bar__search')), when + ': the bar holds ' + JSON.stringify(inBar));
  const links = [...document.querySelectorAll('#nav a.nav-item')].filter(shown);
  check(links.length === (phone ? 4 : 6), when + ': ' + links.length + ' links are drawn');
  for (const a of links) {
    const r = a.getBoundingClientRect(), word = [...a.querySelectorAll('span')].find(shown);
    check(r.left >= 0 && r.right <= w + 0.5, when + ': ' + norm(a.textContent) + ' runs off the page, ' + Math.round(r.left) + ' to ' + Math.round(r.right));
    check(word && word.getClientRects().length === 1 && word.getBoundingClientRect().height < parseFloat(getComputedStyle(word).lineHeight) * 1.6, when + ': "' + norm(word && word.textContent) + '" wraps');
    if (word) { const q = word.getBoundingClientRect(); check(q.left >= r.left - 1 && q.right <= r.right + 1, when + ': "' + norm(word.textContent) + '" runs out of its link, ' + Math.round(q.left) + ' to ' + Math.round(q.right) + ' in ' + Math.round(r.left) + ' to ' + Math.round(r.right)); }
  }
  const search = bar.querySelector('.c-top-bar__search');
  check(shown(search) && (search.getAttribute('aria-label') || '').length > 0, when + ': Search has lost its place or its name');
  const near = (a, b) => Math.abs(a - b) <= 1;
  const state = document.getElementById('p-state');
  if (phone) {
    const r = links.map(a => a.getBoundingClientRect());
    check(r.every(q => near(q.top, r[0].top)) && r[0].bottom > innerHeight - 100, when + ': the links are not the bar along the bottom');
    check(!shown(state) || state.getBoundingClientRect().width <= 1, when + ': the state is drawn on a phone: ' + norm(state.textContent));
    // What a section adds is a row of its own under the top bar, the page's whole width.
    const added = document.createElement('a'); added.className = 'nav-item'; added.textContent = 'A section';
    document.querySelector('.c-rail__pages').appendChild(added);
    const row = document.querySelector('.c-rail__below').getBoundingClientRect();
    check(near(row.left, 0) && near(row.right, w) && row.top >= bar.getBoundingClientRect().bottom - 1, when + ': a section\'s row stands from ' + Math.round(row.left) + ' to ' + Math.round(row.right) + ' at ' + Math.round(row.top));
    added.remove();
  } else {
    const r = rail.getBoundingClientRect(), stage = document.querySelector('.stage').getBoundingClientRect();
    check(near(r.left, 0) && near(r.right, stage.left), when + ': the sidebar stands from ' + Math.round(r.left) + ' to ' + Math.round(r.right) + ' and the page from ' + Math.round(stage.left));
    check(rail.scrollWidth <= rail.clientWidth, when + ': the sidebar runs ' + (rail.scrollWidth - rail.clientWidth) + ' px past itself');
    check(links.every(a => near(a.getBoundingClientRect().left, links[0].getBoundingClientRect().left)), when + ': the links are not one column');
    const markEl = document.getElementById('rail-mark'), nameEl = rail.querySelector('.c-rail__name'), s = state.getBoundingClientRect();
    check(markEl && nameEl && shown(markEl) && shown(nameEl), when + ': the sidebar\'s head holds no mark or no name of theirs');
    if (markEl && nameEl) {
      const mark = markEl.getBoundingClientRect(), name = nameEl.getBoundingClientRect();
      const middle = q => q.left + q.width / 2, column = r.left + rail.clientWidth / 2; // the sidebar's room, less any scrollbar
      check(r.top <= bar.getBoundingClientRect().top + 0.5, when + ': the sidebar does not start at the top of the window: ' + Math.round(r.top));
      check(mark.width >= 80 && mark.height >= 80 && Math.abs(middle(mark) - column) <= 1, when + ': their mark at the sidebar\'s head is ' + Math.round(mark.width) + ' by ' + Math.round(mark.height) + ' at ' + Math.round(middle(mark)) + ', the sidebar\'s middle ' + Math.round(column));
      check(name.top >= mark.bottom - 1 && Math.abs(middle(name) - column) <= 1, when + ': their name is not under their mark: ' + Math.round(name.top) + ' under ' + Math.round(mark.bottom));
      check(shown(state) && rail.contains(state) && !bar.contains(state), when + ': the state is not at the head of the sidebar');
      check(s.top >= name.bottom - 1 && s.top - name.bottom < 12 && Math.abs(middle(s) - column) <= 1, when + ': the state is not under their name: ' + Math.round(s.top) + ' under ' + Math.round(name.bottom));
    }
    check(state.getClientRects().length === 1 && s.height < 30, when + ': the state takes more than a line: ' + Math.round(s.height));
  }
  for (const id of ['pill-proj', 'pill-work']) {
    const el = document.getElementById(id);
    check(el, when + ': #' + id + ' is gone: an overlay may name it');
    if (el) check(!shown(el), when + ': #' + id + ' is drawn: ' + norm(el.textContent));
  }
}

async function run() {
  await until(() => sockets.length > 0);
  sockets[0].open();
  say({ type: 'status', stats }); say({ type: 'continuity', continuity: { mode: 'normal', ledger_seq: 7 } }); say({ type: 'config', config });
  await until(() => S.stats && S.stats.name === 'Quill');
  S.go('chat');
  await until(() => S.view === 'chat' && shown(document.getElementById('msg-input')));
  const line = () => document.getElementById('reply-project');
  check(!shown(line()), 'with no project in focus a project line is drawn: ' + norm(line() && line().textContent));
  barHolds('at rest');

  say({ type: 'projects', projects: [{ id: 'p1', name: LONG, state: 'open', active: true }] });
  say({ type: 'work', work: { live: [{ description: 'Reading the grant letter against the funder\u2019s rules, with the figures from last year', project: LONG }], queued: 2 } });
  say({ type: 'response', stream: true });
  await until(() => shown(line()) && S.thinking);
  barHolds('a long project in focus and a reply under way');
  check(!document.getElementById('rail-work') && !document.body.innerText.includes('Working now') && !document.body.innerText.includes('Reading the grant letter'), 'what is under way is listed under the sidebar\'s links');
  const box = document.querySelector('.composer-row.c-reply__box');
  const l = line();
  check(shown(l), 'the project in focus is not above the reply box');
  if (shown(l)) {
    const lr = l.getBoundingClientRect(), br = box.getBoundingClientRect();
    check(norm(l.textContent) === '◎ ' + LONG, 'the project line reads: ' + norm(l.textContent));
    check(lr.bottom <= br.top + 0.5, 'the project line is not above the box: ' + Math.round(lr.bottom) + ' against ' + Math.round(br.top));
    check(Math.abs(lr.left - br.left) <= 1 && Math.abs(lr.right - br.right) <= 1, 'the project line does not stand at the box\'s width: ' + Math.round(lr.left) + ' to ' + Math.round(lr.right) + ' against ' + Math.round(br.left) + ' to ' + Math.round(br.right));
    check(l.getClientRects().length === 1 && lr.height < 40, 'the project line takes more than one line: ' + Math.round(lr.height));
    check(getComputedStyle(l).textOverflow === 'ellipsis', 'the project line is not cut with an ellipsis');
    check(l.getAttribute('href') === '#/projects/p1', 'the project line does not lead to its project: ' + l.getAttribute('href'));
  }
  // Talk and its menu are one control.
  const mic = document.getElementById('mic'), more = document.getElementById('mic-more');
  mic.hidden = false; more.hidden = false;
  await until(() => shown(mic) && shown(more));
  const a = mic.getBoundingClientRect(), b = more.getBoundingClientRect();
  check(Math.abs(a.right - b.left) <= 1, 'Talk and its menu stand ' + Math.round(b.left - a.right) + ' px apart');
  check(Math.abs(a.top - b.top) <= 1 && Math.abs(a.height - b.height) <= 1, 'Talk and its menu are not one height: ' + Math.round(a.height) + ' and ' + Math.round(b.height));
  const radius = (el, corner) => parseFloat(getComputedStyle(el)[corner]);
  check(radius(mic, 'borderTopRightRadius') === 0 && radius(more, 'borderTopLeftRadius') === 0, 'Talk and its menu are rounded where they meet');
  check(getComputedStyle(mic).opacity === getComputedStyle(more).opacity, 'Talk and its menu differ in strength');
  mic.classList.add('live');
  check(getComputedStyle(mic).boxShadow === 'none' && getComputedStyle(mic).transform === 'none', 'recording draws a ring or a scale round one half of Talk');
  mic.classList.remove('live');
  // A long name is cut at its end; it pushes nothing out of the bar.
  say({ type: 'status', stats: { ...stats, name: 'Quill of the Long Unbroken Record and the Second Reading' } });
  await until(() => S.stats.name !== 'Quill' && document.querySelector('.c-top-bar__name').textContent !== 'Quill');
  barHolds('a long name');
  const named = document.querySelector('.c-top-bar__name');
  check(getComputedStyle(named).textOverflow === 'ellipsis', 'a long name is not cut with an ellipsis');
  const input = document.getElementById('msg-input');
  check(getComputedStyle(input, '::placeholder').whiteSpace === 'nowrap', 'a long name wraps the reply box\'s placeholder, which then shows half a line');
  report(problems.length ? 'FAIL at ' + innerWidth + ':\n  - ' + problems.join('\n  - ') : 'OK');
}
run().catch(e => report('FAIL: the run stopped: ' + ((e && e.message) || e)));
`

// shippedPageRig is the page as shipped, a scripted host in place of the socket,
// and js run in it.
func shippedPageRig(t *testing.T, js string) (string, map[string][]byte, map[string]string) {
	t.Helper()
	read := func(p string) []byte {
		b, err := staticFS.ReadFile("static/" + p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		return b
	}
	frame := string(read("index.html"))
	const first = `<script src="./theme-boot.js"></script>`
	if !strings.Contains(frame, first) || !strings.Contains(frame, "</body>") {
		t.Fatal("index.html no longer has the shape this test loads it by")
	}
	page := strings.Replace(frame, first, `<script src="/__socket.js"></script>`+first, 1)
	page = strings.Replace(page, "</body>", `<script type="module" src="/__measure.js"></script></body>`, 1)
	modules := map[string][]byte{"/__socket.js": []byte(statusFakeSocketJS), "/__measure.js": []byte(js), "/theme.css": read("theme.css"), "/layout.css": read("layout.css"), "/custom.css": read("custom.css")}
	return page, modules, map[string]string{"Content-Security-Policy": uiCSP}
}

func TestTheBarAndTheSidebarHoldAtEveryWidth(t *testing.T) {
	page, modules, headers := shippedPageRig(t, topBarLayoutJS)
	for _, size := range [][2]int{{390, 844}, {768, 900}, {853, 600}, {1024, 800}, {1280, 900}, {1600, 900}} {
		t.Run(fmt.Sprintf("%d", size[0]), func(t *testing.T) { runPageInEnginesAtSizeWithHeaders(t, page, modules, headers, size[0], size[1]) })
	}
}
