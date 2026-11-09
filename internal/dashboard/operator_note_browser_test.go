//go:build !windows

package dashboard

import "testing"

// THEIR NOTE TO YOU AT THE CONVERSATION'S HEAD (spec 1b; step 13; where Home's
// note was until the operator took Home away, 2026-11-05). On the real page, from
// IdentityState.note:
//
//   - their words whole, and only their words in their voice; after each
//     sentence a kept claim names, "Where this comes from" opening its
//     record entries (one past 2^53 as it was kept); a sentence said twice
//     and two claims on one sentence lose no source and double none;
//   - every record that did not go through, once: after the first sentence
//     whose claim names it, else after the note; how many more; how many
//     sources could not be checked; the footer with when they wrote it;
//     AII OS's lines outside their voice;
//   - a read of what did not go through that failed is said, never drawn
//     as none;
//   - with no note kept, nothing stands there (their latest message is the
//     conversation's own, under it); the brief is never shown as the note;
//     and a note taken away (the brief turned off) leaves nothing.
const operatorNoteJS = homeReaderJS + `
const W = (await import('/strings/home.js')).default;
const fillW = (s, v) => s.replace(/\{(\w+)\}/g, (m, k) => k in v ? String(v[k]) : m);
const TEXT = 'Good morning. I shipped the parser. The email to Ola did not go out. I shipped the parser.';
const written = at(7, 12);
const note = extra => Object.assign({ text: TEXT, written_at: written, unchecked: 1, not_succeeded_more: 2,
  claims: [
    { sentence: 'I shipped the parser.', entries: ['9007199254740993'], outcomes: [] },
    { sentence: 'The email to Ola did not go out.', entries: [], outcomes: ['e1'] },
    { sentence: 'I shipped the parser.', entries: ['12'], outcomes: ['e2'] },
    { sentence: 'I shipped the parser.', entries: ['13', '12'], outcomes: ['e2'] },
  ],
  not_succeeded: [
    { id: 'e1', kind: 'tool_result', outcome: 'failed', summary: 'send_email: to Ola', at: at(6, 0) },
    { id: 'e2', kind: 'tool_result', outcome: 'cancelled', summary: 'shell: make', at: at(6, 10) },
    { id: 'e3', kind: 'tool_result', outcome: 'refused', summary: 'web_fetch: the forecast', at: at(6, 20) },
  ] }, extra);
const identity = n => say({ type: 'identity', identity: Object.assign({ beliefs: [], intentions: [], experiences: [], brief: 'The brief, written to them.', charter: '', synthesis: '' }, n ? { note: n } : {}) });
const article = () => $('chat-note') && $('chat-note').querySelector('.c-note');
const voice = () => [...article().querySelectorAll('.c-note__voice')].map(e => e.textContent).join('');
const timeOf = iso => new Date(iso).toLocaleString([], { weekday: 'short', hour: 'numeric', minute: '2-digit' });
const osLines = () => [...article().querySelectorAll('.c-note__os')].map(e => e.textContent);
async function run() {
  held.recorded.push(row(1, 'operator', 'Hello'), row(2, 'resident', 'Their latest message.'));
  await open();
  // A note written over four days, after notes that were not kept.
  const days = d => new Date(Date.now() - d * 86400000).toISOString();
  const span = { window_from: days(4), window_until: days(0) };
  // A day-long note kept three days ago and read now, a later one refused.
  const retained = { window_from: days(4), window_until: days(3) };
  const unread = { not_succeeded: [], not_succeeded_more: 0, not_succeeded_unread: 'the record could not be read' };
  identity(CASE === 'none' || CASE === 'taken-away' ? null : note(CASE === 'unread' ? unread : CASE === 'span' ? span
    : CASE === 'retained' ? retained : CASE === 'retained-unread' ? Object.assign({}, retained, unread) : {}));
  if (CASE === 'taken-away') { identity(note({})); await tick(); }
  S.go('chat'); await serve(); await tick();
  if (CASE === 'taken-away') {
    check(article() && article().dataset.state === 'note', 'the note did not stand before it was taken away');
    identity(null); await serve(); await tick();
  }
  if (CASE === 'none' || CASE === 'taken-away') {
    check(!article() && !$('chat-note').textContent, 'with no note kept, something stands at the conversation\'s head: ' + JSON.stringify($('chat-note').textContent));
    check(!$('chat-head').textContent.includes('The brief, written to them.'), 'the brief is shown as their note');
    return done();
  }
  check(article() && article().dataset.state === 'note', 'their note does not stand at the conversation\'s head');
  check(voice() === TEXT, 'their words are not whole in their voice: ' + JSON.stringify(voice()));
  check(![...article().querySelectorAll('.c-note__voice')].some(v => v.querySelector('.c-note__os, .c-note__where, a')), 'the page speaks inside their voice');
  check(noteLabel() === fillW(W.noteLabel, { name: 'Mira' }), 'the label is not the page\'s: ' + noteLabel());
  const time = new Date(written).toLocaleTimeString([], { hour: 'numeric', minute: '2-digit' });
  const foot = (article().querySelector('.c-note__footer') || {}).textContent;
  check(foot === fillW(W.noteWrittenAt, { name: 'Mira', time }) + ' ' + W.noteLinksHint, 'the footer does not say when they wrote it: ' + foot);
  if (CASE === 'span' || CASE === 'retained' || CASE === 'retained-unread') {
    const since = new Date(span.window_from).toLocaleString([], { weekday: 'long', day: 'numeric', month: 'long', hour: 'numeric', minute: '2-digit' });
    const lines = [...article().querySelectorAll('.c-note__os--after')].map(e => e.textContent);
    if (CASE === 'retained-unread') {
      check(lines.includes(fillW(W.noteNotDoneUnread, { reason: 'the record could not be read', when: since })) && !lines.some(l => l.includes(W.noteSinceYesterday)), 'a kept note read days later calls its unread span yesterday\'s: ' + JSON.stringify(lines));
      return done();
    }
    check(lines.includes(fillW(W.noteNotDoneAfter, { summary: 'web_fetch: the forecast', outcome: 'refused', time: timeOf(at(6, 20)), when: since })) && !lines.some(l => l.includes(W.noteSinceYesterday)), 'a note over four days says its records are yesterday\'s: ' + JSON.stringify(lines));
    return done();
  }
  if (CASE === 'unread') {
    check(osLines().includes(fillW(W.noteNotDoneUnread, { reason: 'the record could not be read', when: W.noteSinceYesterday })), 'a failed read of what did not go through is not said: ' + JSON.stringify(osLines()));
    check(!osLines().some(l => l.includes('send_email')), 'a failed read is drawn as a list');
    return done();
  }
  // Sources: the first "I shipped the parser." carries its claim's entry
  // and the merged claim's; the second its own. The cancelled build is
  // named by both places, so it is said once, after the first.
  const where = [...article().querySelectorAll('.c-note__where')].map(d => [...d.querySelectorAll('a')].map(a => a.getAttribute('href')));
  check(JSON.stringify(where) === JSON.stringify([['#/identity/record/9007199254740993', '#/identity/record/13', '#/identity/record/12'], ['#/identity/record/12']]),
    'the sources are not where their claims fall, whole and once: ' + JSON.stringify(where));
  // Every record that did not go through, once, where its claim falls.
  const kids = [...article().querySelector('.c-note__body').children];
  const e1 = osLines().filter(l => l.includes('send_email')), e2 = osLines().filter(l => l.includes('shell: make')), e3 = osLines().filter(l => l.includes('web_fetch'));
  check(e1.length === 1 && e1[0] === fillW(W.noteNotDone, { summary: 'send_email: to Ola', outcome: 'failed', time: timeOf(at(6, 0)) }), 'the email that failed is not named once, by its summary, outcome and time, in the page\'s words: ' + JSON.stringify(e1));
  check(e2.length === 1 && e3.length === 1, 'a record that did not go through is lost or doubled: ' + JSON.stringify(osLines()));
  const emailAt = kids.findIndex(k => k.textContent.trim() === 'The email to Ola did not go out.');
  check(kids[emailAt + 1] && kids[emailAt + 1].textContent.includes('send_email'), 'the failed email is not right after the sentence that names it');
  const firstShipped = kids.findIndex(k => k.classList.contains('c-note__voice') && k.textContent.trim().endsWith('I shipped the parser.'));
  const emailVoice = kids.findIndex(k => k.textContent.trim() === 'The email to Ola did not go out.');
  check(kids.slice(firstShipped + 1, emailVoice).some(k => k.textContent.includes('shell: make')), 'the cancelled build is not after the first sentence whose claim names it');
  const afterLines = [...article().querySelectorAll('.c-note__os--after')].map(e => e.textContent);
  check(afterLines.includes(fillW(W.noteNotDoneAfter, { summary: 'web_fetch: the forecast', outcome: 'refused', time: timeOf(at(6, 20)), when: W.noteSinceYesterday })), 'a record no sentence names is not said after the note: ' + JSON.stringify(afterLines));
  check(afterLines.includes(fillW(W.noteNotDoneMore, { n: 2 })), 'how many more did not go through is not said');
  check(afterLines.includes(W.noteUncheckedOne), 'the source that could not be checked is not said');
  // Each AII OS line is the board's boxed note, its own "AII OS" in strong.
  const boxes = [...article().querySelectorAll('.c-note__os')];
  check(boxes.length && boxes.every(b => b.querySelector('strong') && /^AII OS:?$/.test(b.querySelector('strong').textContent) && getComputedStyle(b).borderTopStyle === 'solid' && getComputedStyle(b).display === 'flex'),
    'an AII OS line is not the board\'s boxed note: ' + boxes.map(b => b.outerHTML.slice(0, 80)).join(' | '));
  done();
}
run().catch(e => report('FAIL: the run stopped: ' + ((e && e.message) || e)));
`

func TestTheConversationOpensOnTheirNoteWithWhatDidNotGoThrough(t *testing.T) {
	for _, c := range []string{"note", "span", "retained", "retained-unread", "unread", "none", "taken-away"} {
		t.Run(c, func(t *testing.T) {
			runRealPage(t, statusFakeSocketJS, "const CASE = '"+c+"';\n"+operatorNoteJS, homeStyles)
		})
	}
}
