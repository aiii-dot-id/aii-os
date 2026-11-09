//go:build !windows

package dashboard

import "testing"

// THEIR WORDS IN WORDS (spec section 1; step 11, ruled 2026-10-31; headings,
// lists and fenced code ruled 2026-11-03): **bold**, *italic* and `code`
// inline; a heading as a strong line (never a page heading), a list as a
// list, fenced code literal; nothing else admitted. In the browser: those
// drawn; a link and raw <b> left as typed; a mark that does not close, or
// that a space follows, left as typed; text with no such line exactly as
// typed. On the real page: their reply in the thread drawn so, a reply that
// shows a file too; your message as typed; Copy carrying the recorded bytes.
const inWordsUnitJS = `import { report } from '/__harness.js';
import { spans, draw } from '/components/in-words/in-words.js';
const problems = [];
const check = (ok, what) => { if (!ok) problems.push(what); };
const html = s => { const el = document.createElement('div'); draw(el, s); return el.innerHTML; };
const cases = [
  ['**bold**, *italic* and ` + "`code`" + `', '<strong>bold</strong>, <em>italic</em> and <code>code</code>'],
  ['a [link](https://example.com) and https://example.com', 'a [link](https://example.com) and https://example.com'],
  ['<b>raw</b> <script>window.__ran = 1</script>', '&lt;b&gt;raw&lt;/b&gt; &lt;script&gt;window.__ran = 1&lt;/script&gt;'],
  ['# A heading\n- a list\n1. a number', '<p class="in-words__heading">A heading</p><ul><li>a list</li></ul><ol><li>a number</li></ol>'],
  ['I set it:\n\n* **What:** Call your sister\n* **When:** Tomorrow\n\nI will tell you.', 'I set it:<ul><li><strong>What:</strong> Call your sister</li><li><strong>When:</strong> Tomorrow</li></ul>I will tell you.'],
  ['### 1. Hands-on work\n3. three\n4. four', '<p class="in-words__heading">1. Hands-on work</p><ol start="3"><li>three</li><li>four</li></ol>'],
  ['\x60\x60\x60\n<b>x</b> **y**\n\x60\x60\x60', '<pre class="in-words__pre"><code>&lt;b&gt;x&lt;/b&gt; **y**</code></pre>'],
  ['#hashtag and a\n\nb', '#hashtag and a\n\nb'],
  ['` + "`**not bold** <b>`" + `', '<code>**not bold** &lt;b&gt;</code>'],
  ['2 * 3 * 4 and **open', '2 * 3 * 4 and **open'],
  ['x * not italic* and ** not bold**', 'x * not italic* and ** not bold**'],
  ['***both*** and *a **b** c*', '<em><strong>both</strong></em> and <em>a <strong>b</strong> c</em>'],
  ['😀 *é* \\*', '😀 <em>é</em> \\*'],
];
for (const [text, want] of cases) check(html(text) === want, JSON.stringify(text) + ' is drawn ' + JSON.stringify(html(text)) + '; want ' + JSON.stringify(want));
check(!window.__ran, 'a script in their words ran');
check(spans('**a**').length === 1 && spans('**a**')[0][0] === 'strong', 'spans does not read bold');
report(problems.length ? 'FAIL: ' + problems.join('\n  - ') : 'OK');
`

func TestTheirWordsAreDrawnInWordsAndNothingElseIsAdmitted(t *testing.T) {
	renderer, err := staticFS.ReadFile("static/components/in-words/in-words.js")
	if err != nil {
		t.Fatal(err)
	}
	// A module of its own: its cases hold "</script>", which would end an
	// inline script.
	runPageInEngines(t, `<!doctype html><meta charset="utf-8"><script type="module" src="/__page.js"></script>`,
		map[string][]byte{"/__page.js": []byte(inWordsUnitJS), "/components/in-words/in-words.js": renderer})
}

const inWordsThreadJS = homeReaderJS + `
const REPLY = 'Here is **the plan**, *roughly*: run ` + "`make test`" + `.\n# A heading\n- a list item\nSee [this](https://example.com) <b>as typed</b>.';
const YOURS = 'Please **do** it';
const SHOWS = '**Done**: the proof is below.\n![the proof](proof.png)';
async function run() {
  held.recorded.push(row(1, 'operator', YOURS), row(2, 'resident', REPLY), row(3, 'operator', 'And the proof?'), row(4, 'resident', SHOWS));
  await open();
  S.go('chat'); await serve(); await tick();
  const items = [...$('thread-inner').querySelectorAll('.interaction-item')];
  const theirs = items.find(el => (el.querySelector('.body') || {}).textContent?.includes('the plan'));
  const yours = items.find(el => (el.querySelector('.body') || {}).textContent === YOURS);
  check(theirs, 'their reply is not in the thread');
  if (theirs) {
    const body = theirs.querySelector('.body');
    check(body.querySelector('strong')?.textContent === 'the plan' && body.querySelector('em')?.textContent === 'roughly' && body.querySelector('code')?.textContent === 'make test', 'their reply is not drawn in words: ' + body.innerHTML);
    check(body.querySelector('.in-words__heading')?.textContent === 'A heading' && body.querySelector('ul li')?.textContent === 'a list item', 'their heading and list are not drawn as lines of their own: ' + body.innerHTML);
    check(!body.querySelector('a, b, h1, h2, h3, h4, script'), 'their reply admitted a link, raw HTML or a page heading: ' + body.innerHTML);
    check(body.textContent.includes('See [this](https://example.com) <b>as typed</b>.'), 'the rest of their reply is not as typed: ' + JSON.stringify(body.textContent));
    const copy = theirs.querySelector('.copyb');
    check(copy && copy.dataset.copyText === REPLY, 'Copy does not carry their reply as recorded: ' + JSON.stringify(copy && copy.dataset.copyText));
  }
  check(yours && !yours.querySelector('.body strong'), 'your message is drawn in words; it is shown as typed');
  // A reply that shows a file: its words in words, the file drawn as before.
  const shows = items.find(el => (el.querySelector('.body') || {}).textContent?.includes('the proof is below'));
  check(shows && shows.querySelector('.body strong')?.textContent === 'Done' && shows.querySelector('.body figure.shown-image, .body a.file-chip'), 'a reply that shows a file is not drawn in words beside its file: ' + (shows && shows.querySelector('.body').innerHTML));
  check(shows && shows.querySelector('.copyb')?.dataset.copyText === SHOWS, 'Copy does not carry the reply that shows a file as recorded');
  done();
}
run().catch(e => report('FAIL: the run stopped: ' + ((e && e.message) || e)));
`

func TestTheirRepliesAreDrawnInWordsAndCopiedAsRecorded(t *testing.T) {
	runRealPage(t, statusFakeSocketJS, inWordsThreadJS, homeStyles)
}

// ABOUT → EXPERIENCES (step 11): their newest 50, each with its kind, who
// wrote it and when; "You, quoted" only where the operator's words stand
// verbatim; the mark, "Kept out of their reflection" with what it means in
// the annex's words, never hidden for it, and its filter; filters by kind,
// date (the board's choices, From and To among them) and who wrote it over
// the 50, said; focus kept where it was through each redraw; "Copy link" to
// the record entry that made it, one past 2^53 exactly, and none where the
// entry is not sent; one opened in words and as written, Copy carrying the
// words as recorded.
const experiencesJS = `import { report, until } from '/__harness.js';
import { S } from '/state.js';
const W = (await import('/strings/identity.js')).default;
const sockets = window.__sockets;
const say = m => sockets[sockets.length - 1].say(m);
const $ = id => document.getElementById(id);
const problems = [];
const check = (ok, what) => { if (!ok) problems.push(what); };
const tick = () => new Promise(r => setTimeout(r, 40));
const norm = s => (s || '').replace(/\s+/g, ' ').trim();
const now = new Date(), ago = d => new Date(now.getTime() - d * 86400000).toISOString();
const day = d => { const t = new Date(now.getTime() - d * 86400000); return t.getFullYear() + '-' + String(t.getMonth() + 1).padStart(2, '0') + '-' + String(t.getDate()).padStart(2, '0'); };
const copied = [];
navigator.clipboard && (navigator.clipboard.writeText = async t => { copied.push(t); });
document.execCommand = () => { copied.push(document.activeElement && document.activeElement.value); return true; };
const exps = [
  { id: 'x1', content: 'Ola **prefers** staging first.', category: 'observation', created_at: ago(0), provenance: 'self', created_seq: '9007199254740993' },
  { id: 'x2', content: 'Deploy to staging first.', category: 'testimony', created_at: ago(2), provenance: 'operator', operator_verbatim: true, created_seq: '41' },
  { id: 'x3', content: 'They said something like staging matters.', category: 'testimony', created_at: ago(3), provenance: 'operator', created_seq: '40' },
  { id: 'x4', content: 'A private aside.', category: 'observation', created_at: ago(10), provenance: 'self', unmetabolized: true, created_seq: '30' },
  { id: 'x5', content: 'The backup ran.', category: 'event', created_at: ago(40), provenance: 'system' },
];
const items = () => [...$('experiences-stack').querySelectorAll('.experience')].map(el => el.dataset.experienceId);
const item = id => $('experiences-stack').querySelector('[data-experience-id="' + id + '"]');
const field = label => ([...$('experiences-stack').querySelectorAll('label.c-filter')].find(l => (l.querySelector('.c-filter__label') || {}).textContent === label) || { querySelector: () => null }).querySelector('select, input');
const choose = (label, value) => { const s = field(label); s.focus(); s.value = value; s.dispatchEvent(new Event('change')); };
const button = (el, text) => [...el.querySelectorAll('button')].find(b => b.textContent === text);
async function run() {
  await until(() => sockets.length > 0);
  sockets[0].open(); await tick();
  say({ type: 'status', stats: { name: 'Mira', version: 'test', build: 'test', ledger_seq: 50, lifetime_ticks: 1, belief_count: 0, intention_count: 0, experience_count: 5, reflection_count: 0 } });
  say({ type: 'identity', identity: { beliefs: [], intentions: [], experiences: exps } });
  location.hash = '#/identity/experiences'; await until(() => item('x1'), 4000);
  check(items().join() === 'x1,x2,x3,x4,x5', 'not every experience is listed, in order, the kept one among them: ' + items().join());
  check(norm(($('experiences-stack').querySelector('.experiences__scope') || {}).textContent).startsWith(W.experiencesScope), 'the filters do not say they filter the newest 50');
  const line = id => norm(item(id).querySelector('.experience__line').textContent);
  check(line('x2').includes(W.whoYou) && !line('x3').includes(W.whoYou) && line('x3').includes('Mira') && line('x5').includes(W.whoOs), 'who wrote it is not said honestly: ' + [line('x2'), line('x3'), line('x5')].join(' | '));
  check(item('x2').querySelector('.experience__words--yours') && !item('x1').querySelector('.experience__words--yours'), 'your quoted words are not in the page\'s face, or theirs are');
  check(item('x1').querySelector('.experience__words strong')?.textContent === 'prefers', 'their words are not drawn in words');
  const mark = norm((item('x4') && item('x4').querySelector('.experience__kept') || {}).textContent);
  check(mark.startsWith(W.keptOutChip) && mark.includes(W.keptOut.replace('{name}', 'Mira')), 'the mark is not said in the annex\'s words: ' + mark);
  // Filters, each over the 50, and together.
  choose(W.filterWho, 'you'); await tick();
  check(items().join() === 'x2', '"You, quoted" shows ' + items().join());
  choose(W.filterWho, ''); choose(W.filterKind, 'testimony'); await tick();
  check(items().join() === 'x2,x3', 'the kind filter shows ' + items().join());
  check(document.activeElement === field(W.filterKind), 'a filter\'s change took focus away from it: ' + (document.activeElement && document.activeElement.tagName));
  choose(W.filterKind, ''); choose(W.filterDate, '7'); await tick();
  check(items().join() === 'x1,x2,x3', 'the date filter shows ' + items().join());
  check([...field(W.filterDate).options].map(o => o.textContent).join('|') === [W.last30, W.last7, W.sinceBirth, W.chooseDates].join('|'), 'the date choices are not the board\'s: ' + [...field(W.filterDate).options].map(o => o.textContent));
  choose(W.filterDate, 'dates'); await tick();
  choose(W.dateFrom, day(10)); await tick(); choose(W.dateTo, day(10)); await tick();
  check(items().join() === 'x4', 'From and To show ' + items().join());
  choose(W.filterDate, 'birth'); await tick();
  check(items().join() === 'x1,x2,x3,x4,x5', 'Since birth does not show every experience: ' + items().join());
  const kept = [...$('experiences-stack').querySelectorAll('input[type=checkbox]')][0];
  kept.checked = true; kept.dispatchEvent(new Event('change')); await tick();
  check(items().join() === 'x4', 'the mark\'s filter shows ' + items().join());
  choose(W.filterWho, 'os'); await tick();
  check(!items().length && norm($('experiences-stack').textContent).includes(W.experiencesNoMatch), 'nothing matching is not said');
  kept.checked = false; kept.dispatchEvent(new Event('change')); choose(W.filterWho, ''); await tick();
  // Copy link: the entry that made it, exactly; none where none is sent.
  button(item('x1'), 'Copy link').click(); await tick();
  check(copied.at(-1) && copied.at(-1).endsWith('#/identity/record/9007199254740993'), 'Copy link is not the record entry exactly: ' + copied.at(-1));
  check(!button(item('x5'), 'Copy link'), 'an experience whose entry is not sent offers a link');
  // Opened: in words and as written; Copy carries the words as recorded.
  button(item('x1'), 'Open').focus(); button(item('x1'), 'Open').click(); await tick();
  check(document.activeElement && document.activeElement.getAttribute('aria-expanded') === 'true' && item('x1').contains(document.activeElement), 'opening an experience took focus away from it');
  const open = item('x1').querySelector('.experience__open');
  check(open && open.querySelector('.experience__in-words strong') && open.querySelector('.experience__as-written').textContent === exps[0].content, 'one opened is not in words and as written');
  button(open, W.copyEntry).click(); await tick();
  check(copied.at(-1) === exps[0].content, 'Copy does not carry the words as recorded: ' + copied.at(-1));
  report(problems.length ? 'FAIL: ' + problems.join('\n  - ') : 'OK');
}
run().catch(e => report('FAIL: the run stopped: ' + ((e && e.message) || e) + (problems.length ? '\n  - ' + problems.join('\n  - ') : '')));
`

func TestExperiencesAreTheirsFilteredOverTheNewestFiftyAndOpenInWordsAndAsWritten(t *testing.T) {
	runRealPage(t, statusFakeSocketJS, experiencesJS, append([]string{"components/filter/filter.css", "views/identity/experiences.css", "components/tabs/tabs.css"}, homeStyles...))
}
