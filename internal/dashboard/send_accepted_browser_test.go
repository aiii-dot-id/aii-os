//go:build !windows

package dashboard

import "testing"

// A MESSAGE IS SAID TO BE WAITING ONLY WHEN IT WAITS (the walk, 2026-11-03:
// "Accepted — waiting to be recorded" was said after every message). Accepted
// and recorded at once, nothing says it is waiting; accepted and not recorded
// for more than a moment, "Still saving your message…" is said once, and its
// line goes when the message is recorded.
const sendAcceptedJS = homeReaderJS + `
async function run() {
  held.recorded.push(row(1, 'operator', 'Hello'), row(2, 'resident', 'Hello to you.'));
  await open();
  S.go('home'); await serve(); await tick();
  const said = [], lines = [];
  new MutationObserver(() => said.push($('announce').textContent)).observe($('announce'), { childList: true, characterData: true, subtree: true });
  new MutationObserver(() => document.querySelectorAll('.chat-pending > summary').forEach(s => lines.push(s.textContent))).observe(document.body, { childList: true, characterData: true, subtree: true });
  const waiting = t => /Still saving your message…/.test(t);
  const idOf = () => sent.filter(f => f.type === 'chat').at(-1).request_id;
  const send = text => { $('msg-input').value = text; $('msg-input').dispatchEvent(new Event('input', { bubbles: true })); $('send-btn').click(); };
  const recorded = (n, text) => { held.recorded.push(row(n, 'operator', text)); say({ type: 'chat_receipt', request_id: idOf(), chat_recording: { ref: { identity: 'i1', source: 'recorded', incarnation: 'recorded', id: 'm' + n } } }); };
  // Accepted, and recorded at once.
  send('Thanks!'); await tick();
  check(sent.some(f => f.type === 'chat' && f.message === 'Thanks!'), 'the message was not sent');
  say({ type: 'chat_ack', request_id: idOf(), message: 'accepted' }); await tick();
  recorded(3, 'Thanks!'); await serve();
  say({ type: 'response', role: 'identity', message: 'You are welcome.' }); await serve();
  // waits by design: past the second the line is held for, to show it does not come.
  await new Promise(r => setTimeout(r, 1500));
  check(!said.some(waiting) && !lines.some(waiting), 'a message recorded at once was said to be waiting: ' + JSON.stringify([...said, ...lines].filter(waiting)));
  // Accepted, and not recorded for more than a moment.
  send('One more thing.'); await tick();
  say({ type: 'chat_ack', request_id: idOf(), message: 'accepted' });
  await until(() => said.some(waiting), 5000).catch(() => {});
  check(said.filter(waiting).length === 1, 'a message waiting to be recorded was not said so once: ' + JSON.stringify(said.filter(waiting)));
  check([...document.querySelectorAll('.chat-pending > summary')].some(s => waiting(s.textContent)), 'its line does not say it is waiting');
  recorded(5, 'One more thing.'); await serve();
  check(![...document.querySelectorAll('.chat-pending > summary')].some(s => waiting(s.textContent)), 'its line stayed after it was recorded');
  check(said.filter(waiting).length === 1, 'it was said again: ' + JSON.stringify(said.filter(waiting)));
  // What a reply could not confirm stands: the moment's line does not cover it,
  // whichever comes first, a receipt that does not fit or a reply that makes no sense.
  const line = () => ([...document.querySelectorAll('.chat-pending > summary')].at(-1) || {}).textContent;
  const replied = async () => { say({ type: 'response', role: 'identity', message: 'Noted.' }); await serve(); };
  await replied();
  const unfit = () => say({ type: 'chat_receipt', request_id: idOf(), chat_recording: { ref: { identity: 'someone-else', source: 'recorded', incarnation: 'recorded', id: 'm9' } } });
  const accepted = () => say({ type: 'chat_ack', request_id: idOf(), message: 'accepted' });
  for (const [name, first, then, want] of [
    ['a receipt that does not fit, after it was accepted', accepted, unfit, 'Confirmation unavailable — invalid recording reply'],
    ['a receipt that does not fit, before it was accepted', unfit, accepted, 'Confirmation unavailable — invalid recording reply'],
    ['a reply that makes no sense, after it was accepted', accepted, () => say({ type: 'chat_ack', request_id: idOf(), message: 'perhaps' }), 'Confirmation unavailable — invalid admission reply'],
  ]) {
    send(name); await tick();
    first(); then(); await tick();
    check(line() === want, name + ': the line says: ' + line());
    // waits by design: past the second the line is held for, to show it does not cover this.
    await new Promise(r => setTimeout(r, 1300));
    check(line() === want, name + ': the moment\'s line covered what was said: ' + line());
    await replied();
  }
  done();
}
run().catch(e => report('FAIL: the run stopped: ' + ((e && e.message) || e)));
`

func TestAMessageIsSaidToBeWaitingOnlyWhenItWaits(t *testing.T) {
	runRealPage(t, statusFakeSocketJS, sendAcceptedJS, homeStyles)
}
