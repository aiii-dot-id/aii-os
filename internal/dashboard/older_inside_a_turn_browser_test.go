//go:build !windows

package dashboard

import "testing"

// SCROLLING BACK READS OLDER HISTORY WHEN A PAGE BEGINS INSIDE A TURN (the
// operator, 2026-11-07: "Scrolling conversation backward is not loading
// older turns and tool calls"). A turn's work is drawn under its reply, so a
// page whose first row is one of a turn's calls has that row below the
// reply; the reader at the top of what is drawn must still be given the
// older page.
const olderInsideATurnJS = homeReaderJS + `
const item = id => document.querySelector('[data-turn-id="' + id + '"]');
let n = 1;
for (let t = 0; t < 30; t++) {
  const turn = 'tt' + t;
  held.recorded.push(row(n++, 'operator', 'Question ' + t, { turn_id: turn }));
  for (let c = 0; c < 5; c++) {
    const call = 'm' + n;
    held.recorded.push(row(n++, 'system', '{}', { kind: 'tool_call', turn_id: turn, details: { tool: 'shell' } }));
    held.recorded.push(row(n++, 'system', 'ok', { kind: 'tool_result', related_id: call, outcome: 'succeeded', turn_id: turn, details: { tool: 'shell' } }));
  }
  held.recorded.push(row(n++, 'resident', ('Answer ' + t + ' says a good deal about the work. ').repeat(30), { turn_id: turn }));
}
async function run() {
  await open(); S.go('chat'); await serve();
  const thread = $('thread'), older = () => queries().filter(q => q.interaction_window.mode === 'older').length;
  const firstRow = held.recorded[0].id;
  check(held.recorded.length % 50 !== 0, 'the history must not end on a page boundary');
  for (let i = 0; i < 40 && !item(firstRow); i++) {
    thread.scrollTop = 0;
    thread.dispatchEvent(new WheelEvent('wheel', { deltaY: -120, bubbles: true }));
    thread.dispatchEvent(new Event('scroll'));
    await serve();
  }
  check(older() > 0, 'scrolling to the top asked for no older page');
  check(item(firstRow), 'the conversation did not read back to its first message (' + older() + ' older pages asked)');
  done();
}
run().catch(e => report('FAIL: the run stopped: ' + ((e && e.message) || e)));
`

func TestScrollingBackReadsOlderHistoryWhenAPageBeginsInsideATurn(t *testing.T) {
	runRealPage(t, `try { sessionStorage.clear(); localStorage.removeItem('aii.view'); } catch (e) {}
`+statusFakeSocketJS, olderInsideATurnJS, homeStyles)
}
