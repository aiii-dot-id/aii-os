//go:build !windows

package dashboard

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"
)

const recordPageShell = `<!doctype html><html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<link rel="stylesheet" href="/theme.css"><link rel="stylesheet" href="/layout.css"></head>
<body><div class="views" id="views"></div><script type="module" src="/__driver.js"></script></body></html>`

// itself would set and the page key its sign-in would answer, kept where the
// page keeps it.
func runRecordInEngines(t *testing.T, s *Server, start, driver string, w, h int) {
	t.Helper()
	engines := resolveEngines()
	if len(engines) == 0 {
		skipWithoutEngines(t)
	}
	for _, found := range engines {
		t.Run(found.engine.name, func(t *testing.T) {
			results := make(chan string, 4)
			var port string
			srv := httptest.NewUnstartedServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/__result":
					body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
					select {
					case results <- string(body):
					default:
					}
					rw.WriteHeader(http.StatusNoContent)
					return
				case "/__harness.js", "/__driver.js":
					rw.Header().Set("Content-Type", "text/javascript")
					rw.Header().Set("Cache-Control", "no-store")
					if r.URL.Path == "/__harness.js" {
						io.WriteString(rw, harnessJS)
					} else {
						io.WriteString(rw, "localStorage.setItem('aii.signin', '"+pageKeyFor(s)+"');\n"+driver)
					}
					return
				case "/":
					rw.Header().Set("Set-Cookie", "aii_token_"+port+"="+s.accessHash()+"; Path=/; HttpOnly; SameSite=Lax")
					rw.Header().Set("Content-Type", "text/html; charset=utf-8")
					if start != "" {
						rw.Header().Set("Content-Security-Policy", uiCSP)
						io.WriteString(rw, start)
						return
					}
					fmt.Fprintf(rw, `<!doctype html><body style="margin:0"><script>localStorage.setItem('aii.signin', '%s')</script><iframe src="/phone" width="%d" height="%d" style="border:0;display:block"></iframe></body>`, pageKeyFor(s), w, h)
					return
				case "/phone":
					in := r.Clone(r.Context())
					in.URL.Path = "/"
					in.Header.Del("Accept-Encoding")
					rec := httptest.NewRecorder()
					s.server.Handler.ServeHTTP(rec, in)
					for k, v := range rec.Header() {
						if k != "Content-Length" && k != "Etag" {
							rw.Header()[k] = v
						}
					}
					rw.Header().Set("Content-Security-Policy", strings.Replace(rec.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'", "frame-ancestors 'self'", 1))
					rw.WriteHeader(rec.Code)
					io.WriteString(rw, strings.Replace(rec.Body.String(), "</body>", `<script type="module" src="/__driver.js"></script></body>`, 1))
					return
				}
				s.server.Handler.ServeHTTP(rw, r)
			}))
			defer srv.Close()
			host := srv.Listener.Addr().String()
			port = host[strings.LastIndex(host, ":")+1:]
			s.AllowHost(host)
			srv.Start()
			runServerInOneEngine(t, found.engine, found.path, srv.URL+"/", results, 0, 0, 0)
		})
	}
}

func recordBrowserFixture(t *testing.T) (*Server, string, string) {
	t.Helper()
	big := `{"n":30.0,"e":1e3,"face":"😀","unbroken":"` + strings.Repeat("A", 3000) + `","long":"` + strings.Repeat("Ω界😀a", 300<<10/10) + `"}`
	note := `{"id":"exp_old","content":"words a note once called sealed","category":"observation","private":true}`
	payloads := []string{`{"name":"Fixture"}`}
	for i := 2; i <= 25; i++ {
		switch i {
		case 6:
			payloads = append(payloads, big)
		case 9:
			payloads = append(payloads, note)
		case 14:
			payloads = append(payloads, `{"text":"a word only here: quillwort"}`)
		default:
			payloads = append(payloads, fmt.Sprintf(`{"i":%d}`, i))
		}
	}
	st := recordFixture(t, payloads...)
	s := recordServer(t, st, "a-token-of-honest-length-for-this-test-0123456789", RecordSettings{PageRows: 10, FirstViewBytes: 512, Dir: "/srv/an identity"})
	s.rangeCap = 4099
	cut := 0
	head := []byte(big[:512])
	for !utf8.Valid(head) {
		head = head[:len(head)-1]
	}
	for off := len(head) + 4099; off < len(big); off += 4099 {
		if !utf8.RuneStart(big[off]) {
			cut++
		}
	}
	if cut == 0 {
		t.Fatal("no range of the fixture ends inside a character: the test would not prove the join")
	}
	return s, big, note
}

// THE RECORD PAGE SHOWS THE RECORD AS IT IS, IN A REAL BROWSER. In
// every engine on this host, over the dashboard's own routes with its token
// asked for:
//   - the page says in one sentence that every entry is as it was recorded,
//     a password included; an entry opened says, as written, its envelope
//     (sequence, time, type, ring and bytes) above its payload;
//   - a payload larger than the first view says its size, and opened it is
//     the mirror's bytes, byte for byte: 30.0 and 1e3 as written, a character
//     outside the Basic Multilingual Plane, 300 KiB, read in ranges that cut
//     characters in two;
//   - a note written with the old private member is shown whole;
//   - Indented is a labelled copy that adds only spaces and line breaks;
//   - Older entries reaches seq 1; a word only in a payload finds its row; an
//     address of one row opens the page at it, opened.
func TestTheRecordPageShowsEveryRowWholeInABrowser(t *testing.T) {
	if testing.Short() {
		t.Skip("browser engines are not run in -short")
	}
	s, big, note := recordBrowserFixture(t)
	wantBig, _ := json.Marshal(base64.StdEncoding.EncodeToString([]byte(big)))
	wantNote, _ := json.Marshal(note)
	driver := `
import { run, until, assert } from '/__harness.js';
import { renderRecord, RECORD_WORDS as W } from '/views/record.js';
const WI = (await import('/strings/identity.js')).default;
const bigB64 = ` + string(wantBig) + `, note = ` + string(wantNote) + `;
const bytesOf = b64 => Uint8Array.from(atob(b64), c => c.charCodeAt(0));
const same = (a, b) => a.length === b.length && a.every((x, i) => x === b[i]);
// By role and name: each entry is an article named for its sequence; a
// control is found by its words, a field by its label.
const rows = () => [...document.querySelectorAll('article[aria-label^="Entry "]')];
const row = seq => document.querySelector('article[aria-label="' + WI.entry.replace('{seq}', seq) + '"]');
const open = async seq => { row(seq).querySelector('.record__open').click(); await until(() => row(seq).querySelector('pre'), 10000); };
const button = (el, text) => [...el.querySelectorAll('button')].find(b => b.textContent === text);
const para = (el, text) => [...el.querySelectorAll('p')].find(p => p.textContent === text);
const indent = el => [...el.querySelectorAll('label')].find(l => l.textContent.trim() === WI.indented)?.querySelector('input');
const seqs = () => rows().map(r => r.dataset.seq).join(',');
run(async () => {
  document.getElementById('view-record').classList.add('on');
  location.hash = '#/record';
  renderRecord();
  await until(() => rows().length === 10, 10000);
  assert(seqs() === '25,24,23,22,21,20,19,18,17,16', 'the newest page is ' + seqs());
  assert(para(document, WI.recordHint), 'the page does not say every entry is as it was recorded');
  assert(!document.body.textContent.includes('aii ledger show'), 'the Record page still carries Technical\'s line');
  await open('25');
  assert(row('25').querySelector('.record__raw').textContent === '#25 · 2026-10-15T00:00:25Z · fixture.1 · ring 1 · 8 bytes', 'an entry\'s envelope is ' + row('25').querySelector('.record__raw').textContent);

  // Older entries, to seq 1.
  const older = button(document, WI.olderEntries);
  while (!older.hidden) { const n = rows().length; older.click(); await until(() => rows().length > n, 10000); }
  assert(rows().length === 25 && rows().at(-1).dataset.seq === '1', 'Older stopped at ' + seqs());

  // The big payload: its size first, then all of it.
  await open('6');
  const bigRow = row('6'), want = bytesOf(bigB64);
  const more = button(bigRow, W.showAll(want.length));
  assert(more && more.textContent === W.showAll(want.length), 'the size line is ' + (more && more.textContent));
  const pre = bigRow.querySelector('pre');
  assert(new TextEncoder().encode(pre.textContent).length <= 512, 'more than the first view was shown unasked');
  more.click();
  await until(() => !button(bigRow, W.showAll(want.length)), 30000);
  const shown = new TextEncoder().encode(pre.textContent);
  assert(same(shown, want), 'the payload shown is not the record: ' + shown.length + ' bytes for ' + want.length);
  assert(pre.textContent.includes('"n":30.0,"e":1e3,"face":"😀"'), 'the numbers or the character were rewritten');
  assert(getComputedStyle(pre).whiteSpace === 'pre-wrap' && getComputedStyle(pre).overflowWrap === 'anywhere', 'the payload does not wrap');

  // The old private note, whole.
  await open('9');
  assert(row('9').querySelector('pre').textContent === note && ![...row('9').querySelectorAll('button')].some(b => b.textContent.endsWith(' bytes — show all')), 'the old note is not shown whole');

  // Indented: a labelled copy, never the default.
  const box = indent(row('9')), notePre = row('9').querySelector('pre');
  assert(box && !box.checked, 'Indented is missing or on by default');
  box.click();
  const copy = notePre.textContent;
  assert(copy !== note && copy.replace(/[ \n]/g, '') === note.replace(/[ \n]/g, ''), 'the indented copy changed more than spaces: ' + copy);
  assert(!para(row('9'), WI.indentedHint).hidden, 'the copy is not said to be one');
  box.click();
  assert(notePre.textContent === note && para(row('9'), WI.indentedHint).hidden, 'Indented off did not give the record back');
  const bigBox = indent(bigRow);
  bigBox.click();
  assert(pre.textContent.includes('"n": 30.0,') && pre.textContent.includes('"e": 1e3,'), 'the indented copy rewrote a number');
  bigBox.click();

  // Words only in a payload.
  const words = document.querySelector('input[type="search"][aria-label="' + WI.searchRecord + '"]');
  assert(words && words.placeholder === WI.searchRecord, 'the search field is not "Search the record"');
  words.value = 'QUILLWORT';
  words.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter' }));
  await until(() => seqs() === '14', 10000);
  assert(seqs() === '14', 'a word only in a payload found ' + seqs());
  words.value = 'no record holds these words';
  words.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter' }));
  const said = document.querySelector('[role="status"]');
  await until(() => said.textContent === WI.recordNoMatch, 10000);
  assert(said.textContent === WI.recordNoMatch && !rows().length, 'an empty page said ' + said.textContent);

  location.hash = '#/record/7';
  renderRecord();
  await until(() => rows().length && rows()[0].dataset.seq === '7', 10000);
  assert(rows()[0].getAttribute('aria-current') === 'true' && rows()[0].querySelector('pre') && seqs() === '7,6,5,4,3,2,1', 'the row\'s address opened ' + seqs());
});
`
	runRecordInEngines(t, s, recordPageShell, driver, 0, 0)
}

func TestTheRecordPageFitsAPhone(t *testing.T) {
	if testing.Short() {
		t.Skip("browser engines are not run in -short")
	}
	s, big, _ := recordBrowserFixture(t)
	held := &heldSent{}
	held.set(20, false, `{"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,`+strings.Repeat("QUJD", 3000)+`"}}]}],"x":`+big+`}`)
	s.currentHandler().Sent = held
	driver := `
const report = text => fetch('/__result', { method: 'POST', body: text, keepalive: true });
const until = (cond, ms) => new Promise(done => { const end = Date.now() + ms; const tick = () => cond() || Date.now() > end ? done() : setTimeout(tick, 15); tick(); });
(async () => {
  const { S } = await import('/state.js');
  const { renderRecord } = await import('/views/record.js');
  await until(() => typeof S.go === 'function', 10000);
  const trail = [location.hash]; addEventListener('hashchange', e => trail.push(e.newURL.replace(/^[^#]*/, '') + '@' + S.view + (S.connected ? '+c' : '')));
  S.go('record');
  location.hash = '#/record/6';
  renderRecord();
  const row = () => document.getElementById('record-6');
  await until(() => row(), 10000);
  if (!row()) return report('FAIL: the row never appeared (view ' + S.view + ', said ' + JSON.stringify(document.querySelector('.record__said')?.textContent) + ', hash ' + location.hash + ', trail ' + trail.join(' ') + ')');
  if (innerWidth !== 375) return report('FAIL: not at phone width: ' + innerWidth);
  const opened = () => { const r = row(), m = r && r.querySelector('.ledger-more'); if (m && !m.disabled) m.click(); return r && !m; };
  await until(opened, 30000);
  if (!opened()) return report('FAIL: the row was not opened');
  const view = document.getElementById('view-record');
  if (!view.classList.contains('on')) return report('FAIL: the Record view is not the one shown');
  const wide = [...view.querySelectorAll('.record__item, .record__filters > *, .record__head > *, pre, .ledger-acts > *')]
    .filter(el => el.getBoundingClientRect().right > innerWidth + 0.5 || el.getBoundingClientRect().left < -0.5);
  if (wide.length) return report('FAIL: wider than the screen: ' + wide.map(el => el.className || el.tagName).join(', '));
  // Nor does any text run past its box, where the box would hide it or
  // scroll it sideways.
  const over = [...view.querySelectorAll('.record__item, pre, .record__line, .record__raw, .record__hint')].filter(el => el.scrollWidth > el.clientWidth + 1);
  if (over.length) return report('FAIL: text runs past its box: ' + over.map(el => el.className || el.tagName).join(', '));
  if (view.scrollWidth > view.clientWidth + 1) return report('FAIL: the view scrolls sideways: ' + view.scrollWidth + ' in ' + view.clientWidth);
  if (document.documentElement.scrollWidth > innerWidth) return report('FAIL: the page scrolls sideways: ' + document.documentElement.scrollWidth);
  // The old address of "What the model is sent" opens Technical's panel
  // (step 19); one request opened whole, its picture shown.
  location.hash = '#/identity/record/sent';
  renderRecord();
  await until(() => location.hash === '#/settings/technical/sent' && S.view === 'technical', 10000);
  if (S.view !== 'technical') return report('FAIL: the old sent address did not open Technical: ' + location.hash + ' view ' + S.view);
  const call = () => document.querySelector('.sent-row');
  await until(() => call(), 10000);
  if (!call()) return report('FAIL: no call is listed');
  call().open = true;
  const body = () => call().querySelector('[data-part="body"]');
  await until(() => body() && body().querySelector('.ledger-more'), 10000);
  body().querySelector('.ledger-more').click();
  await until(() => body().querySelector('.sent-image'), 10000);
  if (!body().querySelector('.sent-image')) return report('FAIL: the call was not opened');
  body().querySelector('.sent-image').click();
  const sentWide = [...document.querySelectorAll('.technical__nav, .technical__pane .ledger-head, .sent-row, .sent-row pre')]
    .filter(el => el.getBoundingClientRect().right > innerWidth + 0.5 || el.scrollWidth > el.clientWidth + 1);
  if (sentWide.length) return report('FAIL: what the model is sent is wider than the screen: ' + sentWide.map(el => el.className || el.tagName).join(', '));
  if (document.documentElement.scrollWidth > innerWidth) return report('FAIL: the page scrolls sideways on the panel: ' + document.documentElement.scrollWidth);
  const nav = [...document.querySelectorAll('#nav .nav-item')].filter(el => el.getClientRects().length);
  const off = nav.filter(el => el.getBoundingClientRect().right > innerWidth + 0.5);
  if (off.length) return report('FAIL: off the phone bar: ' + off.map(el => el.textContent.trim()).join(', '));
  report('OK');
})().catch(e => report('FAIL: ' + ((e && e.message) || String(e))));
`
	runRecordInEngines(t, s, "", driver, 375, 812)
}
