//go:build !windows

// Runs through the unix browser-engine rig (browserengine_test.go, same tag).
package dashboard

import (
	"errors"
	"sync"
	"testing"
)

// A REFUSED LOGS READ REACHES THE PANEL FROM THE DAEMON (a review, 2026-10-31
// 17:46Z): the whole page, its socket connected, entered by S.go at the Logs
// panel; the daemon's ListLogs refuses, and the panel says so in its own
// words where it was loading — the refusal carries the ask's request id, so
// the panel knows it is its own; then the handler is set right, a file is
// listed, its TailLogs refuses, the panel says that too, and a second press
// reads the lines once the tail is served.
func TestARefusedLogsReadReachesThePanelFromTheDaemon(t *testing.T) {
	if testing.Short() {
		t.Skip("browser engines are not run in -short")
	}
	s, _, _ := recordBrowserFixture(t)
	// The daemon refuses every odd list and tail call and serves the even
	// ones: each engine's page makes two of each, so its second entry and
	// second press meet success (the server is shared by the engines' runs).
	var mu sync.Mutex
	lists, tails := 0, 0
	h := s.currentHandler()
	h.ListLogs = func() ([]LogFileState, error) {
		mu.Lock()
		defer mu.Unlock()
		lists++
		if lists%2 == 1 {
			return nil, errors.New("the log directory could not be read")
		}
		return []LogFileState{{Name: "aii.log", Size: 12, ModAt: "2026-10-31T12:00:00Z"}}, nil
	}
	h.TailLogs = func(name string) (*LogTailState, error) {
		mu.Lock()
		defer mu.Unlock()
		tails++
		if tails%2 == 1 {
			return nil, errors.New("the file is gone")
		}
		return &LogTailState{Name: name, Lines: []string{"one line"}}, nil
	}
	driver := `
const report = text => fetch('/__result', { method: 'POST', body: text, keepalive: true });
const until = (cond, ms) => new Promise(done => { const end = Date.now() + ms; const tick = () => cond() || Date.now() > end ? done() : setTimeout(tick, 15); tick(); });
(async () => {
  const { S } = await import('/state.js');
  const W = (await import('/strings/settings-technical.js')).default;
  const { fill } = await import('/util.js');
  await until(() => typeof S.go === 'function' && S.connected, 15000);
  const said = () => document.querySelector('.technical-logs > .ledger-said')?.textContent || '';
  const tail = () => document.querySelector('.technical-logs__tail')?.textContent || '';
  location.hash = '#/settings/technical/logs';
  S.go('technical');
  await until(() => said() === fill(W.logsListFailed, { why: 'the log directory could not be read' }), 10000);
  const state = () => ' [view ' + S.view + ', panel ' + JSON.stringify((document.querySelector('.technical-logs') || {}).outerHTML || '').slice(0, 240) + ', list ' + JSON.stringify(S.logsList) + ']';
  if (said() !== fill(W.logsListFailed, { why: 'the log directory could not be read' })) return report('FAIL: the refused list is not said: ' + JSON.stringify(said()) + state());
  // The directory is readable now: entering the panel again by its own
  // address, as the Settings list's link does, lists the file. (S.go opens
  // Technical on its first panel, as a place opens on its first tab.)
  S.go('chat');
  await until(() => S.view === 'chat', 10000);
  location.hash = '#/settings/technical/logs';
  await until(() => document.querySelector('.technical-logs__file button'), 10000);
  const press = document.querySelector('.technical-logs__file button');
  if (!press) return report('FAIL: the file was not listed after the directory became readable: ' + JSON.stringify(said()) + state());
  press.click();
  await until(() => tail() === fill(W.logsTailFailed, { name: 'aii.log', why: 'the file is gone' }), 10000);
  if (tail() !== fill(W.logsTailFailed, { name: 'aii.log', why: 'the file is gone' })) return report('FAIL: the refused tail is not said: ' + JSON.stringify(tail()));
  document.querySelector('.technical-logs__file button').click();
  await until(() => document.querySelector('.technical-logs__pre')?.textContent === 'one line', 10000);
  if (document.querySelector('.technical-logs__pre')?.textContent !== 'one line') return report('FAIL: the lines were not read after the press: ' + JSON.stringify(tail()));
  report('OK');
})().catch(e => report('FAIL: ' + ((e && e.message) || String(e))));
`
	runRecordInEngines(t, s, "", driver, 1280, 800)
}
