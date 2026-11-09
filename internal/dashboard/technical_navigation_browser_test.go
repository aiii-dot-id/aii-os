//go:build !windows

// Runs through the unix browser-engine rig (browserengine_test.go, same tag).
package dashboard

import (
	"testing"
)

// THE PANEL IS READ AGAIN ON EVERY REAL ENTRY (spec step 19; the carry of
// 2026-10-31 16:46Z). The whole page, its socket connected, driven by S.go
// as the top bar drives it: Technical shows the one request; the operator
// goes Home and comes back, and the panel is read again — the page's own
// fetch answers the second read with a changed list (kept 0), which the
// panel shows. A first entry refused by the network recovers on the next
// real entry. Entering Technical asks the list exactly once per entry.
func TestTechnicalReadsAgainOnEveryEntry(t *testing.T) {
	if testing.Short() {
		t.Skip("browser engines are not run in -short")
	}
	s, _, _ := recordBrowserFixture(t)
	held := &heldSent{}
	held.set(20, false, `{"model":"m","messages":[{"role":"user","content":"hello"}]}`)
	s.currentHandler().Sent = held
	driver := `
const report = text => fetch('/__result', { method: 'POST', body: text, keepalive: true });
const until = (cond, ms) => new Promise(done => { const end = Date.now() + ms; const tick = () => cond() || Date.now() > end ? done() : setTimeout(tick, 15); tick(); });
(async () => {
  const { S } = await import('/state.js');
  await until(() => typeof S.go === 'function' && S.connected, 15000);
  if (!S.connected) return report('FAIL: the page never connected');
  // The page's own fetch, watched: the first /sent refused, the third
  // answered with the real list changed to kept 0.
  const asked = [];
  const real = window.fetch;
  window.fetch = async (url, init) => {
    const u = String(url);
    if (u !== '/sent' && !u.startsWith('/sent?')) return real(url, init);
    asked.push(u);
    if (asked.length === 1) throw new Error('the network is down');
    const res = await real(url, init);
    if (asked.length < 3) return res;
    const list = await res.json();
    list.kept = 0; list.requests = [];
    return new Response(JSON.stringify(list), { status: 200, headers: { 'Content-Type': 'application/json' } });
  };
  const rows = () => document.querySelectorAll('.sent-row').length;
  const failed = () => (document.querySelector('.technical__pane .ledger-said')?.textContent || '').startsWith('What the model is sent could not be read');
  // First entry: refused by the network.
  S.go('technical');
  await until(() => failed(), 10000);
  if (!failed()) return report('FAIL: a refused first read was not said (view ' + S.view + ', asks ' + asked.length + ')');
  if (asked.length !== 1) return report('FAIL: the first entry asked ' + asked.length + ' time(s)');
  // Home and back: the next real entry reads again and recovers.
  S.go('home');
  await until(() => S.view === 'home', 10000);
  S.go('technical');
  await until(() => rows() === 1, 10000);
  if (rows() !== 1) return report('FAIL: the second entry did not recover the list: ' + rows() + ' row(s), asks ' + asked.length);
  if (asked.length !== 2) return report('FAIL: the second entry asked ' + asked.length + ' time(s) in all');
  // Home and back again: the changed list is shown.
  S.go('home');
  await until(() => S.view === 'home', 10000);
  S.go('technical');
  await until(() => document.querySelector('.sent-none'), 10000);
  if (!document.querySelector('.sent-none') || rows() !== 0) return report('FAIL: the third entry did not show the changed list: ' + rows() + ' row(s), none said ' + !!document.querySelector('.sent-none'));
  if (asked.length !== 3) return report('FAIL: three entries asked ' + asked.length + ' time(s)');
  report('OK');
})().catch(e => report('FAIL: ' + ((e && e.message) || String(e))));
`
	runRecordInEngines(t, s, "", driver, 1280, 800)
}
