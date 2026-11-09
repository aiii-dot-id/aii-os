//go:build !windows

package dashboard

import "testing"

// A FRESH LOAD AT ABOUT → RECORD DRAWS ABOUT'S HEAD AS ITS OTHER TABS DO: the
// mark, their name and the line under it, in the column. Record is drawn by the
// page, not by About's section, and its sheets did not bring the head's rules
// (views/identity/who.css), so the head sat unstyled at the window's edge.
func TestAFreshLoadAtTheRecordDrawsAboutsHead(t *testing.T) {
	s, _, _ := recordBrowserFixture(t)
	// A born identity, so the page draws About's head; the record is the fixture's.
	s.currentHandler().GetStats = func() (*StatsResponse, error) {
		return &StatsResponse{Name: "Mira", LedgerSeq: 11, LifetimeTicks: 24}, nil
	}
	// Its mark counts the hours lived together in the pulse a live host answers with.
	s.currentHandler().GetConfig = func() *ConfigState { return &ConfigState{Cognition: CognitionConfigState{PulseSeconds: 300}} }
	driver := `
const report = text => fetch('/__result', { method: 'POST', body: text, keepalive: true });
const until = async (ok, ms) => { for (let i = 0; i * 50 < (ms || 10000) && !ok(); i++) await new Promise(r => setTimeout(r, 50)); return ok(); };
(async () => {
  // The rig opens at "/": the page is loaded again at the address, as a saved link is.
  if (location.hash !== '#/identity/record') { location.hash = '#/identity/record'; location.reload(); return; }
  const head = () => document.querySelector('#place-head > .place-head');
  const styled = () => head() && getComputedStyle(head()).display === 'flex';
  await until(() => styled() && document.querySelector('[id^="record-"]'), 15000);
  if (!head()) return report('FAIL: About\'s head was not drawn at Record');
  const box = head().getBoundingClientRect();
  if (!styled()) return report('FAIL: About\'s head at Record is unstyled: display ' + getComputedStyle(head()).display + ', at x ' + Math.round(box.left));
  if (box.left < 1) return report('FAIL: About\'s head at Record is at the window\'s edge: x ' + box.left);
  report('OK');
})().catch(e => report('FAIL: the driver stopped: ' + ((e && e.message) || e)));
`
	runRecordInEngines(t, s, "", driver, 1280, 800)
}
