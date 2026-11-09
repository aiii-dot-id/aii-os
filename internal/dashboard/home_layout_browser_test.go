//go:build !windows

package dashboard

import "testing"

// WHAT THE CONVERSATION'S HEAD SAYS STANDS WHERE IT CAN BE READ, at a desktop's
// width and a phone's (where Home's note was until the operator took Home away,
// 2026-11-05). Their note stands at the conversation's column, on the first
// screen; their alarms are not said there, since they already appear on a
// settings page. A long note, new since the last visit, is open, and scrolls in its
// own box, so the thread keeps its room; seen before, it stands closed under
// its line. What is under way is said there on a phone, where there is no
// sidebar to say it, and not twice on a desktop. When saving is slow, its line
// starts where the reply box starts.
const homeLayoutJS = homeReaderJS + `
async function run() {
  const long = Array.from({ length: 40 }, (_, i) => 'Line ' + (i + 1) + ' of a long note, set out so it fills more than a screen.').join(' ');
  held.recorded.push(row(1, 'operator', 'What can you do?'), row(2, 'resident', 'A great deal.'));
  await open();
  S.go('chat'); await serve(); await tick();
  say({ type: 'identity', identity: { beliefs: [], intentions: [], experiences: [], charter: '', synthesis: '', note: { text: long, written_at: new Date().toISOString(), claims: [], not_succeeded: [] } } });
  const later = Date.now() + 3600000;
  say({ type: 'work', work: { live: [{ description: 'Reading the grant letter' }], queued: 0, alarms: [{ id: 'a1', message: 'Call your sister', due_at: later }] } }); await tick();
  await until(() => ($('chat-note').textContent || '').includes('Line 1 of a long note'), 5000);
  const box = document.querySelector('#chat-note > details.chat-head__note'), thread = $('thread');
  const reply = document.querySelector('.composer-row.c-reply__box').getBoundingClientRect();
  check(!/Call your sister/.test(document.body.innerText), 'their alarm is said on the conversation\'s page: ' + JSON.stringify(($('chat-head') || {}).innerText));
  check(box && box.open, 'a note new since the last visit is not open');
  const n = box.getBoundingClientRect(), th = thread.getBoundingClientRect();
  check(n.bottom <= th.top + 0.5, 'the note does not stand above the thread: ' + [n.bottom, th.top].map(Math.round));
  check(Math.abs(n.left - reply.left) <= 1 && Math.abs(n.right - reply.right) <= 1, 'the note does not stand at the conversation\'s column: ' + Math.round(n.left) + ' to ' + Math.round(n.right) + ', the reply box ' + Math.round(reply.left) + ' to ' + Math.round(reply.right));
  check(n.top < reply.top, 'the note is not on the first screen');
  check(n.height <= innerHeight * (innerWidth <= 767 ? 0.22 : 0.4) + 1 && box.scrollHeight > box.clientHeight, 'a long note does not scroll in its own box: ' + Math.round(n.height) + ' of ' + innerHeight);
  check(th.height >= innerHeight * 0.2, 'the thread has no room under the note at ' + innerWidth + ': ' + Math.round(th.height) + ' of ' + innerHeight);
  check(!document.body.innerText.includes('Reading the grant letter') && !$('chat-work'), 'what is under way is listed beside the conversation');
  // Saving is slow: its line starts where the reply box starts, on the screen.
  $('msg-input').value = 'And what should I call you?'; $('msg-input').dispatchEvent(new Event('input', { bubbles: true }));
  $('send-btn').click(); await tick();
  say({ type: 'chat_ack', request_id: sent.filter(f => f.type === 'chat').at(-1).request_id, message: 'accepted' });
  await until(() => /Still saving/.test((document.querySelector('#chat-pending summary') || {}).textContent || ''), 5000).catch(() => {});
  const line = document.querySelector('#chat-pending summary'), boxC = document.querySelector('.composer-row.c-reply__box');
  check(line && /Still saving/.test(line.textContent), 'the slow line is not there: ' + (line && line.textContent));
  check(line && Math.abs(line.getBoundingClientRect().left - boxC.getBoundingClientRect().left) <= 1, 'the slow line starts at ' + (line && Math.round(line.getBoundingClientRect().left)) + ', the reply box at ' + Math.round(boxC.getBoundingClientRect().left));
  done();
}
run().catch(e => report('FAIL: the run stopped: ' + ((e && e.message) || e)));
`

// A NOTE SEEN BEFORE STANDS CLOSED under its own line, a press away, and opens
// when pressed: the page decides from this browser's last visit, once it has
// read when their record began, and their own press holds.
const homeNoteSeenJS = homeReaderJS + `
import { firstEntryTime } from '/views/identity/record-read.js';
async function run() {
  const written = new Date(Date.now() - 3600000).toISOString();
  held.recorded.push(row(1, 'operator', 'Hello'), row(2, 'resident', 'Hi.'));
  await open();
  S.go('chat'); await serve(); await tick();
  say({ type: 'identity', identity: { beliefs: [], intentions: [], experiences: [], charter: '', synthesis: '', note: { text: 'Good morning.', written_at: written, claims: [], not_succeeded: [] } } });
  await until(() => document.querySelector('#chat-note > details.chat-head__note'), 5000);
  // Until the page has read when their record began, the note opens (the test
  // of a note drawn before the visit is known holds that); the page's own read
  // is waited for, and the drawing it brings.
  await firstEntryTime().catch(() => {}); await tick();
  const box = document.querySelector('#chat-note > details.chat-head__note');
  check(box && !box.open, 'a note written before the last visit is open');
  check(box && box.querySelector(':scope > summary').textContent === 'A note from Mira', 'the closed note does not say whose it is: ' + (box && box.querySelector(':scope > summary').textContent));
  box.querySelector(':scope > summary').click(); await tick();
  check(box.open, 'pressing its line does not open the note');
  say({ type: 'identity', identity: { beliefs: [], intentions: [], experiences: [], charter: '', synthesis: '', note: { text: 'Good morning.', written_at: written, claims: [], not_succeeded: [] } } }); await tick();
  check(document.querySelector('#chat-note > details.chat-head__note') === box && box.open, 'a redraw undid their press');
  done();
}
run().catch(e => report('FAIL: the run stopped: ' + ((e && e.message) || e)));
`

func TestWhatTheConversationsHeadSaysStandsWhereItCanBeRead(t *testing.T) {
	for _, size := range [][2]int{{1280, 800}, {390, 844}} {
		runRealPageAtSize(t, statusFakeSocketJS, homeLayoutJS, homeStyles, size[0], size[1])
	}
}

// aMinuteAgoJS is a last visit a minute ago, kept before the page loads.
const aMinuteAgoJS = `try { localStorage.setItem('aii.lastSeenAt', String(Date.now() - 60000)); localStorage.removeItem('aii.view'); } catch (e) {}
`

func TestANoteSeenBeforeStandsClosedAPressAway(t *testing.T) {
	runRealPage(t, aMinuteAgoJS+statusFakeSocketJS, homeNoteSeenJS, homeStyles)
}

// recordReadHeldJS stands in for views/identity/record-read.js: the real
// module, with the read of when their record began held until the test lets
// it go, and then answered within the same step (a promise, no fetch), so the
// page settles before the browser reports a press it has queued.
const recordReadHeldJS = `export * from './record-read-real.js';
let release;
const held = new Promise(r => { release = r; });
window.__releaseRecord = () => release(Date.parse('2026-09-02T10:00:00Z'));
export const firstEntryTime = () => held;
`

func servedRecordReadHeld(t *testing.T) map[string][]byte {
	t.Helper()
	real, err := staticFS.ReadFile("static/views/identity/record-read.js")
	if err != nil {
		t.Fatal(err)
	}
	return map[string][]byte{"/views/identity/record-read.js": []byte(recordReadHeldJS), "/views/identity/record-read-real.js": real}
}

// A NOTE DRAWN BEFORE THE VISIT IS KNOWN SETTLES, UNLESS THEIR HAND MOVED IT
// (a review's read of the host stack, 2026-11-05). Their note can come before the
// page has read when their record began, so before it is known whether this
// browser's last visit was to them: it opens. Once that is known, a note seen
// before closes. A new note they closed with their own press stays closed.
const homeNoteOrderJS = homeReaderJS + `
async function run() {
  held.recorded.push(row(1, 'operator', 'Hello'), row(2, 'resident', 'Hi.'));
  await open();
  S.go('chat'); await serve(); await tick();
  const written = CASE === 'seen' ? new Date(Date.now() - 3600000).toISOString() : new Date().toISOString();
  say({ type: 'identity', identity: { beliefs: [], intentions: [], experiences: [], charter: '', synthesis: '', note: { text: 'Good morning.', written_at: written, claims: [], not_succeeded: [] } } });
  await until(() => document.querySelector('#chat-note > details.chat-head__note'), 5000);
  const box = document.querySelector('#chat-note > details.chat-head__note');
  check(box && box.open, 'a note drawn before the visit is known is not open');
  // Their press and the record's read land in the same step, before the browser
  // reports the toggle: the press still holds.
  if (CASE === 'theirs') { box.querySelector(':scope > summary').click(); check(!box.open, 'their press did not close the note'); }
  window.__releaseRecord();
  // waits by design: the page has drawn again on the read, and any toggle the browser queued has been reported.
  await new Promise(r => setTimeout(r, 300));
  check(document.querySelector('#chat-note > details.chat-head__note') === box, 'the note was drawn again from nothing');
  if (CASE === 'seen') check(!box.open, 'a note seen before stayed open once the visit was known');
  if (CASE === 'theirs') check(!box.open, 'the page reopened a note their own press had closed');
  done();
}
run().catch(e => report('FAIL: the run stopped: ' + ((e && e.message) || e)));
`

func TestANoteDrawnBeforeTheVisitIsKnownSettlesUnlessTheyMovedIt(t *testing.T) {
	for _, c := range []string{"seen", "theirs"} {
		t.Run(c, func(t *testing.T) {
			runRealPageServing(t, aMinuteAgoJS+statusFakeSocketJS, "const CASE = '"+c+"';\n"+homeNoteOrderJS, homeStyles, servedRecordReadHeld(t))
		})
	}
}
