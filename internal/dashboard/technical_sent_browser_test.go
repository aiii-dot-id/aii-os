//go:build !windows

// Runs through the unix browser-engine rig (browserengine_test.go, same tag).
package dashboard

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

// Technical's first panel, "What {name} sees" (spec step 19), is today's
// "What the model is sent" tab moved under Settings: every request AII OS
// sent to the model, as the provider received it, newest first, read from
// GET /sent and GET /sent/body exactly as before. The heading is the
// specification's; a row says when, what the request was for in words, the
// model and its size; the bounds are named under the heading; a picture in
// a body is drawn as one line and shown when pressed; the old address under
// the Record page opens the panel here.
func TestWhatTheModelSeesIsTechnicalsFirstPanel(t *testing.T) {
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
import { S } from '/state.js';
import { renderTechnical, readTechnical } from '/views/settings/technical/technical.js';
import { renderRecord, SENT_MOVED_TO } from '/views/record.js';
import { fill } from '/util.js';
import W from '/strings/settings-technical.js';
const rows = () => [...document.querySelectorAll('.sent-row')];
const lines = () => [...document.querySelectorAll('.technical__pane .ledger-line, .technical__pane .ledger-warn')].map(p => p.textContent);
const open = async row => { row.open = true; await until(() => row.querySelector('[data-part="body"]') && !row.querySelector(':scope > .ledger-said'), 10000); };
const go = async () => {
  document.getElementById('view-technical').classList.add('on');
  location.hash = '#/settings/technical/sent';
  renderTechnical();
  await until(() => document.querySelector('.technical__pane .ledger-head'), 10000);
};
`
	t.Run("two requests", func(t *testing.T) {
		held.set(20, false, short, long)
		runRecordInEngines(t, s, recordPageShell, prelude+`
const short = `+js(short)+`, long = `+js(long)+`, png = `+js(png)+`;
run(async () => {
  await go();
  const current = document.querySelector('.c-side-nav [aria-current="page"]');
  assert(current && current.textContent === W.seesNoName && current.getAttribute('href') === '#/settings/technical/sent', 'the open panel is not What the model is sent: ' + (current && current.textContent));
  // An identity with no name yet is not called Unnamed.
  S.identityExists = true; S.stats = { name: 'Unnamed' };
  renderTechnical();
  assert(document.querySelector('.c-side-nav [aria-current="page"]').textContent === W.seesNoName, 'the rail calls them: ' + document.querySelector('.c-side-nav [aria-current="page"]').textContent);
  S.identityExists = false; S.stats = null;
  renderTechnical();
  const said = lines();
  const want = [W.seesHeading, W.seesKey, W.seesPlugins, W.seesUnmarked, fill(W.seesBounds, { kept: 20, maxBytes: '32 MB' })];
  assert(JSON.stringify(said) === JSON.stringify(want), 'the panel says ' + JSON.stringify(said));
  await until(() => rows().length === 2, 10000);
  assert(rows()[0].querySelector('summary').textContent === '2026-10-15T01:02:01Z · ' + W.forDream + ' · a-model on an-entry · ' + new TextEncoder().encode(long).length + ' bytes', 'a dream pass is not said in words: ' + rows()[0].querySelector('summary').textContent);
  assert(rows()[1].querySelector('summary').textContent === '2026-10-15T01:02:00Z · ' + fill(W.forTurn, { turn: 40 }) + ' · a-model on an-entry · ' + new TextEncoder().encode(short).length + ' bytes', 'a turn is not said in neutral words: ' + rows()[1].querySelector('summary').textContent);
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
  assert(pics.map(b => b.textContent).join('|') === fill(W.image, { type: 'image/png', bytes: 1500 }) + '|' + fill(W.image, { type: 'image/jpeg', bytes: 300 }), 'the pictures read ' + pics.map(b => b.textContent).join('|'));
  const pre = box.querySelector('pre');
  assert(!pre.textContent.includes(png), 'a picture is drawn as its base64 before it is asked for');
  pics.forEach(b => b.click());
  const shown = new TextEncoder().encode(pre.textContent), want2 = new TextEncoder().encode(long);
  assert(shown.length === want2.length && shown.every((x, i) => x === want2[i]), 'As sent, its pictures shown, is not the body: ' + shown.length + ' bytes for ' + want2.length);
  assert(big.querySelector('.ledger-note') && big.querySelector('[data-part="system"]') === null, 'a request with no system member does not say so');
});`, 0, 0)
	})
	t.Run("kept 0", func(t *testing.T) {
		held.set(0, false)
		runRecordInEngines(t, s, recordPageShell, prelude+`
run(async () => {
  await go();
  assert(document.querySelector('.sent-none')?.textContent === W.seesNone && !rows().length, 'kept 0 is not said: ' + JSON.stringify(lines()));
  assert(document.querySelector('.technical__pane > .ledger-said').textContent === '', 'kept 0 is called empty');
});`, 0, 0)
	})
	t.Run("over the budget", func(t *testing.T) {
		held.set(20, true, long)
		runRecordInEngines(t, s, recordPageShell, prelude+`
run(async () => {
  await go();
  assert(document.querySelector('.sent-over')?.textContent === fill(W.seesOver, { maxBytes: '32 MB' }), 'a request over the budget is not said: ' + JSON.stringify(lines()));
});`, 0, 0)
	})
	t.Run("read again on entry and after a failure, not on every render", func(t *testing.T) {
		held.set(20, false, short)
		runRecordInEngines(t, s, recordPageShell, prelude+`
run(async () => {
  // The page's own fetch, watched: how many times /sent is asked, and the
  // first time refused by the network.
  const asked = [];
  const real = window.fetch;
  let refuse = true;
  window.fetch = (url, init) => {
    const u = String(url);
    if (u === '/sent' || u.startsWith('/sent?')) { asked.push(u); if (refuse) { refuse = false; return Promise.reject(new Error('the network is down')); } }
    return real(url, init);
  };
  document.getElementById('view-technical').classList.add('on');
  location.hash = '#/settings/technical/sent';
  readTechnical(); // what entering the view asks
  renderTechnical();
  await until(() => (document.querySelector('.technical__pane .ledger-said')?.textContent || '').startsWith('What the model is sent could not be read'), 10000);
  assert(asked.length === 1, 'the first visit asked ' + asked.length + ' time(s)');
  // A render within the visit, after a failure, reads again.
  renderTechnical();
  await until(() => rows().length === 1, 10000);
  assert(asked.length === 2 && rows().length === 1, 'after a failure the next render did not read again: ' + asked.length + ' ask(s), ' + rows().length + ' row(s)');
  // A render within the visit, after a read, draws what is held: a read
  // would empty the pane at once, so the rows' node still standing says
  // none began, and the count of asks agrees.
  const held = document.querySelector('.technical__pane .ledger-rows');
  renderTechnical();
  assert(document.contains(held) && asked.length === 2, 'a render within the visit read again: ' + asked.length + ' asks, rows kept ' + document.contains(held));
  // Entering the view again reads again.
  readTechnical();
  renderTechnical();
  await until(() => asked.length === 3, 10000);
  assert(asked.length === 3, 'entering the view again did not read: ' + asked.length + ' asks');
  await until(() => rows().length === 1, 10000);
});`, 0, 0)
	})
	t.Run("the old address opens it here", func(t *testing.T) {
		held.set(20, false, short)
		runRecordInEngines(t, s, recordPageShell, prelude+`
run(async () => {
  location.hash = '#/identity/record/sent';
  renderRecord();
  await until(() => location.hash === SENT_MOVED_TO, 10000);
  assert(location.hash === '#/settings/technical/sent', 'the old address did not move: ' + location.hash);
  assert(!document.querySelector('.ledger-tabs .tab[href$="/sent"]'), 'the Record page still offers the tab');
});`, 0, 0)
	})
}
