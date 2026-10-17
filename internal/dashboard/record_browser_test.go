//go:build !windows

package dashboard

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

const recordPageShell = `<!doctype html><html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<link rel="stylesheet" href="/theme.css"><link rel="stylesheet" href="/layout.css"></head>
<body><div class="views" id="views"></div><script type="module" src="/__driver.js"></script></body></html>`

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
						io.WriteString(rw, driver)
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
					fmt.Fprintf(rw, `<!doctype html><body style="margin:0"><iframe src="/phone" width="%d" height="%d" style="border:0;display:block"></iframe></body>`, w, h)
					return
				case "/phone":
					in := r.Clone(r.Context())
					in.URL.Path = "/"
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
const bigB64 = ` + string(wantBig) + `, note = ` + string(wantNote) + `;
const bytesOf = b64 => Uint8Array.from(atob(b64), c => c.charCodeAt(0));
const same = (a, b) => a.length === b.length && a.every((x, i) => x === b[i]);
const rows = () => [...document.querySelectorAll('.ledger-row')];
const row = seq => document.getElementById('record-' + seq);
const seqs = () => rows().map(r => r.dataset.seq).join(',');
run(async () => {
  document.getElementById('view-record').classList.add('on');
  location.hash = '#/record';
  renderRecord();
  await until(() => rows().length === 10, 10000);
  assert(seqs() === '25,24,23,22,21,20,19,18,17,16', 'the newest page is ' + seqs());
  const line = document.querySelector('.ledger-line');
  assert(line.textContent === W.heading('/srv/an identity').join(''), 'the heading line is ' + line.textContent);
  assert(line.querySelector('code').textContent === 'aii ledger show -dir /srv/an identity', 'the command is ' + line.querySelector('code').textContent);
  assert(document.querySelector('.ledger-warn').textContent === W.warning, 'the warning is not the one sentence');
  assert(row('25').querySelector('.ledger-meta').textContent === '#25 · 2026-10-15T00:00:25Z · fixture.1 · ring 1', 'a row\'s line is ' + row('25').querySelector('.ledger-meta').textContent);
  assert(row('25').querySelector('.ledger-meta a').getAttribute('href') === '#/record/25', 'a row\'s link is not its seq');

  const older = document.querySelector('.ledger-older');
  while (!older.hidden) { const n = rows().length; older.click(); await until(() => rows().length > n, 10000); }
  assert(rows().length === 25 && rows().at(-1).dataset.seq === '1', 'Older stopped at ' + seqs());

  const bigRow = row('6'), want = bytesOf(bigB64);
  const more = bigRow.querySelector('.ledger-more');
  assert(more && more.textContent === W.showAll(want.length), 'the size line is ' + (more && more.textContent));
  const pre = bigRow.querySelector('pre');
  assert(new TextEncoder().encode(pre.textContent).length <= 512, 'more than the first view was shown unasked');
  more.click();
  await until(() => !bigRow.querySelector('.ledger-more'), 30000);
  const shown = new TextEncoder().encode(pre.textContent);
  assert(same(shown, want), 'the payload shown is not the record: ' + shown.length + ' bytes for ' + want.length);
  assert(pre.textContent.includes('"n":30.0,"e":1e3,"face":"😀"'), 'the numbers or the character were rewritten');
  assert(getComputedStyle(pre).whiteSpace === 'pre-wrap' && getComputedStyle(pre).overflowWrap === 'anywhere', 'the payload does not wrap');

  assert(row('9').querySelector('pre').textContent === note && !row('9').querySelector('.ledger-more'), 'the old note is not shown whole');

  const box = row('9').querySelector('.ledger-indent input'), notePre = row('9').querySelector('pre');
  assert(box && !box.checked, 'Indented is missing or on by default');
  box.click();
  const copy = notePre.textContent;
  assert(copy !== note && copy.replace(/[ \n]/g, '') === note.replace(/[ \n]/g, ''), 'the indented copy changed more than spaces: ' + copy);
  assert(!row('9').querySelector('.ledger-note').hidden, 'the copy is not said to be one');
  box.click();
  assert(notePre.textContent === note && row('9').querySelector('.ledger-note').hidden, 'Indented off did not give the record back');
  const bigBox = bigRow.querySelector('.ledger-indent input');
  bigBox.click();
  assert(pre.textContent.includes('"n": 30.0,') && pre.textContent.includes('"e": 1e3,'), 'the indented copy rewrote a number');
  bigBox.click();

  const words = document.querySelector('[data-record-words]');
  words.value = 'QUILLWORT';
  words.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter' }));
  await until(() => seqs() === '14', 10000);
  assert(seqs() === '14', 'a word only in a payload found ' + seqs());
  words.value = '';
  words.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter' }));
  await until(() => rows().length === 10, 10000);
  const type = document.querySelector('[data-record-type]');
  assert([...type.options].map(o => o.value).join(',') === ',fixture.0,fixture.1,fixture.2,ring0.genesis', 'the type list is ' + [...type.options].map(o => o.value));
  type.value = 'fixture.2'; type.dispatchEvent(new Event('change'));
  await until(() => rows().length && rows().every(r => r.querySelector('.ledger-meta').textContent.includes(' · fixture.2 · ')), 10000);
  assert(seqs() === '23,20,17,14,11,8,5,2', 'one type read ' + seqs());
  words.value = 'no record holds these words';
  words.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter' }));
  const said = document.querySelector('.ledger-said');
  await until(() => said.textContent === W.empty, 10000);
  assert(said.textContent === W.empty && !rows().length, 'an empty page said ' + said.textContent);

  location.hash = '#/record/7';
  renderRecord();
  await until(() => rows().length && rows()[0].dataset.seq === '7', 10000);
  assert(rows()[0].classList.contains('ledger-target') && seqs() === '7,6,5,4,3,2,1', 'the row\'s address opened ' + seqs());
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
  if (!row()) return report('FAIL: the row never appeared (view ' + S.view + ', said ' + JSON.stringify(document.querySelector('.ledger-said')?.textContent) + ', hash ' + location.hash + ', trail ' + trail.join(' ') + ')');
  if (innerWidth !== 375) return report('FAIL: not at phone width: ' + innerWidth);
  const opened = () => { const r = row(), m = r && r.querySelector('.ledger-more'); if (m && !m.disabled) m.click(); return r && !m; };
  await until(opened, 30000);
  if (!opened()) return report('FAIL: the row was not opened');
  const view = document.getElementById('view-record');
  if (!view.classList.contains('on')) return report('FAIL: the Record view is not the one shown');
  const wide = [...view.querySelectorAll('.ledger-row, .ledger-head, .ledger-filters label, pre, .ledger-acts > *')]
    .filter(el => el.getBoundingClientRect().right > innerWidth + 0.5 || el.getBoundingClientRect().left < -0.5);
  if (wide.length) return report('FAIL: wider than the screen: ' + wide.map(el => el.className || el.tagName).join(', '));
  const over = [...view.querySelectorAll('.ledger-row, .ledger-head, pre, .ledger-meta, .ledger-line')].filter(el => el.scrollWidth > el.clientWidth + 1);
  if (over.length) return report('FAIL: text runs past its box: ' + over.map(el => el.className || el.tagName).join(', '));
  if (view.scrollWidth > view.clientWidth + 1) return report('FAIL: the view scrolls sideways: ' + view.scrollWidth + ' in ' + view.clientWidth);
  if (document.documentElement.scrollWidth > innerWidth) return report('FAIL: the page scrolls sideways: ' + document.documentElement.scrollWidth);
  location.hash = '#/record/sent';
  renderRecord();
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
  const sentWide = [...view.querySelectorAll('.ledger-tabs, .sent-row, .sent-row pre, .ledger-head')]
    .filter(el => el.getBoundingClientRect().right > innerWidth + 0.5 || el.scrollWidth > el.clientWidth + 1);
  if (sentWide.length) return report('FAIL: what the model is sent is wider than the screen: ' + sentWide.map(el => el.className || el.tagName).join(', '));
  if (document.documentElement.scrollWidth > innerWidth) return report('FAIL: the page scrolls sideways on the tab: ' + document.documentElement.scrollWidth);
  const nav = [...document.querySelectorAll('#nav .nav-item')].filter(el => !el.hidden);
  const off = nav.filter(el => el.getBoundingClientRect().right > innerWidth + 0.5);
  if (off.length) return report('FAIL: off the phone bar: ' + off.map(el => el.textContent.trim()).join(', '));
  report('OK');
})().catch(e => report('FAIL: ' + ((e && e.message) || String(e))));
`
	runRecordInEngines(t, s, "", driver, 375, 812)
}

func TestWhatTheModelIsSentIsShownAsSentInABrowser(t *testing.T) {
	if testing.Short() {
		t.Skip("browser engines are not run in -short")
	}
	png := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x89, 'P', 'N'}, 500))
	jpeg := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0xff, 0xd8}, 150))
	short := `{"model":"m","messages":[{"role":"system","content":"RING 0 TEXT 😀"},{"role":"user","content":"30.0 1e3"}]}`
	long := `{"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,` + png +
		`"}}]}],"x":[{"type":"image","source":{"type":"base64","media_type":"image/jpeg","data":"` + jpeg + `"}}],"pad":"` + strings.Repeat("Ω", 2000) + `"}`
	held := &heldSent{}
	s := New("127.0.0.1", 0, &WSHandler{Sent: held, RecordSettings: func() RecordSettings { return RecordSettings{FirstViewBytes: 512} }})
	s.SetAccessToken(true, "a-token-of-honest-length-for-this-test-0123456789")
	s.rangeCap = 333
	js := func(v any) string { b, _ := json.Marshal(v); return string(b) }
	prelude := `
import { run, until, assert } from '/__harness.js';
import { renderRecord, SENT_WORDS as W } from '/views/record.js';
const rows = () => [...document.querySelectorAll('.sent-row')];
const lines = () => [...document.querySelectorAll('.sent-pane-line, .ledger-pane:not([hidden]) .ledger-line, .ledger-pane:not([hidden]) .ledger-warn')].map(p => p.textContent);
const open = async row => { row.open = true; await until(() => row.querySelector('[data-part="body"]') && !row.querySelector(':scope > .ledger-said'), 10000); };
const go = async () => {
  document.getElementById('view-record').classList.add('on');
  location.hash = '#/record/sent';
  renderRecord();
  await until(() => document.querySelector('.ledger-pane:not([hidden]) .ledger-head'), 10000);
};
`
	t.Run("two calls", func(t *testing.T) {
		held.set(20, false, short, long)
		runRecordInEngines(t, s, recordPageShell, prelude+`
const short = `+js(short)+`, long = `+js(long)+`, png = `+js(png)+`;
run(async () => {
  await go();
  assert(document.querySelector('.ledger-tabs .tab.sel').textContent === 'What the model is sent', 'the tab is not the one selected');
  const said = lines();
  assert(said[0] === W.heading(20) && said[1] === W.plugins && said[2] === W.unmarked && said.length === 3, 'the page says ' + JSON.stringify(said));
  await until(() => rows().length === 2, 10000);
  assert(rows()[0].querySelector('summary').textContent === '2026-10-15T01:02:01Z · dream · a-model on an-entry · ' + new TextEncoder().encode(long).length + ' bytes', 'a line reads ' + rows()[0].querySelector('summary').textContent);
  assert(rows()[1].querySelector('summary').textContent === '2026-10-15T01:02:00Z · turn 40 · a-model on an-entry · ' + new TextEncoder().encode(short).length + ' bytes', 'a line reads ' + rows()[1].querySelector('summary').textContent);
  await open(rows()[1]);
  const parts = [...rows()[1].querySelectorAll('.sent-part')].map(h => h.textContent);
  assert(parts.join() === W.asSent + ',' + W.system, 'the parts are ' + parts);
  assert(rows()[1].querySelector('[data-part="body"] pre').textContent === short, 'As sent is not the body');
  assert(rows()[1].querySelector('[data-part="system"] pre').textContent === 'RING 0 TEXT 😀', 'the system text is ' + rows()[1].querySelector('[data-part="system"] pre').textContent);
  const big = rows()[0];
  await open(big);
  const box = big.querySelector('[data-part="body"]');
  box.querySelector('.ledger-more').click();
  await until(() => !box.querySelector('.ledger-more'), 10000);
  const pics = [...box.querySelectorAll('.sent-image')];
  assert(pics.map(b => b.textContent).join('|') === W.image('image/png', 1500) + '|' + W.image('image/jpeg', 300), 'the pictures read ' + pics.map(b => b.textContent).join('|'));
  const pre = box.querySelector('pre');
  assert(!pre.textContent.includes(png), 'a picture is drawn as its base64 before it is asked for');
  pics.forEach(b => b.click());
  const shown = new TextEncoder().encode(pre.textContent), want = new TextEncoder().encode(long);
  assert(shown.length === want.length && shown.every((x, i) => x === want[i]), 'As sent, its pictures shown, is not the body: ' + shown.length + ' bytes for ' + want.length);
  assert(big.querySelector('.ledger-note') && big.querySelector('[data-part="system"]') === null, 'a call with no system member does not say so');
});`, 0, 0)
	})
	t.Run("kept 0", func(t *testing.T) {
		held.set(0, false)
		runRecordInEngines(t, s, recordPageShell, prelude+`
run(async () => {
  await go();
  assert(document.querySelector('.sent-none')?.textContent === W.none && !rows().length, 'kept 0 is not said: ' + JSON.stringify(lines()));
  assert(document.querySelector('.ledger-pane:not([hidden]) > .ledger-said').textContent === '', 'kept 0 is called empty');
});`, 0, 0)
	})
	t.Run("over the budget", func(t *testing.T) {
		held.set(20, true, long)
		runRecordInEngines(t, s, recordPageShell, prelude+`
run(async () => {
  await go();
  assert(document.querySelector('.sent-over')?.textContent === W.over(`+strconv.Itoa(DefaultSentMaxBytes)+`), 'a call over the budget is not said: ' + JSON.stringify(lines()));
});`, 0, 0)
	})
}
