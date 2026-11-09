//go:build !windows

package dashboard

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// THE OUTCOME ROW (spec section 5a, the board OutcomeRow; step 4). A
// stretch whose every step succeeded with a receipt is one folded line,
// "{name} · read 2 files · ran 1 command · Done"; a step that failed, was
// refused, is unknown or was cancelled keeps the row open with an AII OS
// note giving its outcome and reason; a success with no receipt, or a
// qualification the record carries, is never "Done"; and what became of a
// reply sent on through a channel is the daemon's three sentences, never
// one.
func TestComponentOutcomeRow(t *testing.T) {
	page := `<!doctype html><div id="host"></div>
<script type="module">
import * as row from '/components/outcome-row/outcome-row.js';
import { assert, run } from '/__harness.js';
run(() => {
  const ok = (tool, extra) => Object.assign({ tool, outcome: 'succeeded', receipt: true }, extra || {});
  const host = document.getElementById('host');
  const done = row.render({ actor: 'Mira', items: [ok('read'), ok('read'), ok('shell')], receipt: '#/identity/record/12' });
  host.append(done);
  const line = el => el.querySelector('summary').textContent;
  assert(line(done) === 'Mira · read 2 files · ran 1 command · Done', 'a stretch that succeeded is not one line: ' + line(done));
  assert(!done.open && !done.classList.contains('c-outcome-row--open') && !done.querySelector('.c-os-note'), 'a stretch that succeeded is open, or carries a note');
  assert(done.querySelector('a.c-outcome-row__receipt').getAttribute('href') === '#/identity/record/12' && done.querySelector('a.c-outcome-row__receipt').textContent === 'Details', 'View receipt does not go to the record');
  done.open = true;
  row.update(done, { actor: 'Mira', items: [ok('read'), ok('read'), ok('shell')] });
  assert(done.open, 'a row the reader opened was closed by an update');

  assert(done.querySelector('.c-outcome-row__actor').textContent === 'Mira' && done.querySelector('.c-outcome-row__verdict--good').textContent === 'Done', 'the line is not drawn as the board draws it');
  for (const [outcome, said, label] of [['failed', 'Running a command failed: exit 2', 'Failed'], ['refused', 'Running a command was refused: not allowed here', 'Refused'], ['unknown', 'AII OS couldn\'t confirm running a command finished.', 'Not confirmed'], ['cancelled', 'Running a command was stopped by you.', 'Stopped']]) {
    const reason = outcome === 'failed' ? 'exit 2' : outcome === 'refused' ? 'not allowed here' : '';
    const el = row.render({ actor: 'Mira', items: [ok('read'), { tool: 'shell', outcome, reason, receipt: true }] });
    host.append(el);
    assert(el.open && el.classList.contains('c-outcome-row--open'), outcome + ' folded');
    assert(!line(el).endsWith('Done'), outcome + ' says Done: ' + line(el));
    const v = el.querySelector('.c-outcome-row__verdict--bad');
    assert(v && v.textContent === label, outcome + ' does not end in "' + label + '": ' + line(el));
    const note = el.querySelector('.c-os-note');
    assert(note && note.textContent.includes(said), outcome + ' does not say what happened under it: ' + (note && note.textContent));
  }
  const unreceipted = row.render({ actor: 'Mira', items: [ok('read', { receipt: false })] });
  assert(unreceipted.open && !line(unreceipted).endsWith('Done'), 'a success with no receipt is Done');
  const qualified = row.render({ actor: 'Mira', items: [ok('read')], qualifications: ['not saved'] });
  assert(qualified.open && line(qualified) === 'Mira · read 1 file · not saved', 'a qualification was lost or called Done: ' + line(qualified));

  const notes = [
    row.deliveryNote({ effect: 'unknown' }),
    row.deliveryNote({ effect: 'unknown', via: 'org.example.telegram' }),
    row.deliveryNote({ parked: true, attempts: 8 }),
    row.deliveryNote({ attempts: 2 }),
  ].map(n => n && n.text);
  assert(notes[0] === 'AII OS cannot say whether it left. Not resent, and no other channel tried.', 'an unknown with no carrier: ' + notes[0]);
  assert(notes[1] === 'It may have left via org.example.telegram, and AII OS cannot tell whether it arrived. Not resent.', 'an unknown that may have left: ' + notes[1]);
  assert(notes[2] === 'Not delivered after 8 tries. AII OS has stopped trying.', 'parked: ' + notes[2]);
  assert(notes[3] === 'Not delivered yet. AII OS is trying again.', 'queued: ' + notes[3]);
  assert(new Set(notes).size === 4, 'the unconfirmed states are not each their own sentence');
  assert(row.deliveryNote({ delivered: true }) === null && row.deliveryNote({ effect: 'performed' }) === null, 'a delivered reply carries a note');
});
</script>`
	runPageInEngines(t, page, nil)
}

// A FAILURE IS SAID ON ITS LINE, AND NOTHING OPENS BY ITSELF: a
// stretch the person folded stays folded, and the page does not jump. Each
// stretch whose step did not succeed is one folded line, marked open, that
// never says Done, with its AII OS note and its link a press away; the
// stretch that succeeded is one line that says Done.
func TestAFailureIsSaidOnItsLineAndNothingOpensByItself(t *testing.T) {
	page := `<!doctype html><div id="thread"><div id="thread-inner"></div></div>
<div id="composer-wrap"><textarea id="msg-input"></textarea><button id="send-btn">Send</button></div><button id="jump-latest" hidden>Latest</button>
<script type="module">
import { S } from '/state.js';
import { renderInteractionPages } from '/views/chat.js';
import { run } from '/__harness.js';
run(async assert => {
  S.stats = { name: 'Mira' }; S.identityExists = true;
  const outcomes = ['succeeded', 'failed', 'refused', 'unknown', 'cancelled'];
  const rows = [];
  let seq = 0;
  const at = () => '2026-10-30T12:' + String(10 + seq).padStart(2, '0') + ':00Z';
  outcomes.forEach((outcome, i) => {
    const turn = 't' + i;
    rows.push({ id: 'q' + i, sequence: String(++seq), kind: 'message', role: 'operator', content: 'Do it', turn_id: turn, recorded_at: at(), details: {} });
    rows.push({ id: 'c' + i, sequence: String(++seq), kind: 'tool_call', role: 'system', turn_id: turn, recorded_at: at(), content: '{}', details: { tool: 'shell' } });
    rows.push({ id: 'r' + i, related_id: 'c' + i, sequence: String(++seq), kind: 'tool_result', role: 'system', turn_id: turn, recorded_at: at(), outcome, content: outcome === 'succeeded' ? 'ok' : 'it went wrong\nmore', details: { tool: 'shell' } });
    rows.push({ id: 'a' + i, sequence: String(++seq), kind: 'message', role: 'resident', content: 'Reply ' + i, turn_id: turn, recorded_at: at(), details: {} });
  });
  const p = { identity: 'fixture', incarnation: 'disk', rows, has_older: false, has_newer: false };
  const pages = new Map([['recorded', p], ['transient', { identity: 'fixture', incarnation: 'memory', rows: [] }]]);
  renderInteractionPages(pages, () => {}, false);
  const receipts = [...document.querySelectorAll('#thread-inner .turn-receipt')];
  assert(receipts.length === outcomes.length, 'stretches: ' + receipts.length);
  receipts.forEach((el, i) => {
    const outcome = outcomes[i], box = el.querySelector('.receipt'), line = el.querySelector('.receipt-label').textContent;
    if (outcome === 'succeeded') {
      assert(!box.open && el.dataset.outcome === 'done' && line === 'Mira · ran 1 command · Done', 'the stretch that succeeded is not one folded line: ' + line + ' open=' + box.open);
      assert(!el.querySelector('.receipt-notes .c-os-note'), 'the stretch that succeeded carries a note');
      assert(el.querySelector('a.receipt-view')?.getAttribute('href') === '#/identity/record/' + rows.find(r => r.id === 'r' + i).sequence, 'View receipt does not open the step\'s entry');
      return;
    }
    assert(!box.open && el.dataset.outcome === 'open', outcome + ' opened by itself, or is not marked as not done');
    assert(!line.endsWith('Done'), outcome + ' says Done: ' + line);
    const note = el.querySelector('.receipt-notes .c-os-note');
    assert(note && /running a command/i.test(note.textContent), outcome + ' has no note under it giving its outcome: ' + (note && note.textContent));
    // View receipt opens the entry that records the step that did not succeed.
    const view = el.querySelector('a.receipt-view');
    assert(view && view.textContent === 'Details' && view.getAttribute('href') === '#/identity/record/' + rows.find(r => r.id === 'r' + i).sequence, outcome + ': View receipt does not open the step\'s entry: ' + (view && view.getAttribute('href')));
  });
  // A step with no recorded entry has no link.
  rows.push({ id: 'qx', sequence: String(++seq), kind: 'message', role: 'operator', content: 'One more', turn_id: 'tx', recorded_at: at(), details: {} });
  rows.push({ id: 'cx', sequence: String(++seq), kind: 'tool_call', role: 'system', turn_id: 'tx', recorded_at: at(), content: '{}', details: { tool: 'shell' } });
  renderInteractionPages(pages, () => {}, false);
  const last = [...document.querySelectorAll('#thread-inner .turn-receipt')].at(-1);
  assert(last.dataset.outcome === 'open' && !last.querySelector('a.receipt-view'), 'a step with no recorded entry has a link');
  // A reader who opens one keeps it open, and one folded again stays folded, across renders.
  receipts[1].querySelector('.receipt').open = true;
  renderInteractionPages(pages, () => {}, false);
  assert(receipts[1].querySelector('.receipt').open, 'a render folded a stretch the reader opened');
  receipts[1].querySelector('.receipt').open = false;
  renderInteractionPages(pages, () => {}, false);
  assert(!receipts[1].querySelector('.receipt').open, 'a render opened again a stretch the reader folded');
});
</script>`
	runPageInEngines(t, page, chatModuleRig(t))
}

// pageNotePage runs the page-wide note's module over a page that holds its
// region before any words.
const pageNotePage = `<!doctype html><div id="page-note" role="status"></div>
<script type="module">
import { S } from '/state.js';
import { renderPageNote } from '/shell/page-note.js';
import { assert, run } from '/__harness.js';
const notes = () => [...document.querySelectorAll('#page-note .c-os-note')].map(n => n.textContent);
run(() => {
%SCRIPT%
});
</script>`

// NOTHING AT REST (spec section 0 and step 4). Connected, in mode normal,
// with no credential warning, AII OS's note on every page says nothing:
// the region is there, empty.
func TestNothingAtRest(t *testing.T) {
	script := `
  S.connected = true; S.pageFrames = { status: true, continuity: true }; S.stats = { name: 'Mira' }; S.cont = { mode: 'normal' };
  renderPageNote();
  assert(document.getElementById('page-note').children.length === 0 && notes().length === 0, 'a page at rest says something: ' + notes().join(' | '));
  S.cont = { mode: 'safe', safe_reason: 'their record did not verify' };
  renderPageNote();
  assert(notes().length === 1, 'safe mode is not said');
  S.cont = { mode: 'normal' };
  renderPageNote();
  assert(notes().length === 0, 'the note stayed after the mode came back to normal: ' + notes().join(' | '));`
	t.Run("the-note-module", func(t *testing.T) {
		runPageInEngines(t, strings.Replace(pageNotePage, "%SCRIPT%", script, 1), map[string][]byte{
			"/state.js": []byte(`export const S = { connected: false, stats: null, cont: null };`),
		})
	})
	// A cold arrival and a reconnect, executed on the shipped frame through
	// its own socket (thread_real_page_browser_test.go).
	t.Run("cold-arrival-and-reconnect-on-the-real-page", func(t *testing.T) { runOnTheRealPage(t, "note") })
}

// WARNINGS ARE INDEPENDENT (spec step 4). A mode that is not normal and a
// credential that needs attention are each their own note: an expiring
// adopted credential can stop thinking whatever the mode says, so mode
// normal with a credential warning is never a page at rest, and clearing
// one never clears the other. Not connected says so, alone.
func TestWarningsAreIndependent(t *testing.T) {
	script := `
  S.connected = true; S.pageFrames = { status: true, continuity: true }; S.stats = { name: 'Mira', credential_warning: 'the Claude Code sign-in expires in 2 days' }; S.cont = { mode: 'normal' };
  renderPageNote();
  assert(notes().length === 1 && notes()[0].includes('A credential needs your attention: the Claude Code sign-in expires in 2 days'), 'mode normal with a credential warning is at rest: ' + notes().join(' | '));
  S.cont = { mode: 'safe', safe_reason: 'their record did not verify' };
  renderPageNote();
  assert(notes().length === 2 && notes()[0].includes('AII OS has put Mira in safe mode: their record did not verify') && notes()[1].includes('A credential needs your attention'), 'the two are not each their own note: ' + notes().join(' | '));
  S.cont = { mode: 'normal' };
  renderPageNote();
  assert(notes().length === 1 && notes()[0].includes('A credential needs your attention'), 'clearing the mode cleared the credential: ' + notes().join(' | '));
  S.cont = { mode: 'degraded_witness', degraded_since: '2026-10-30T12:00:00Z' };
  S.stats = { name: 'Mira' };
  renderPageNote();
  assert(notes().length === 1 && notes()[0].includes('can\'t reach aiii.id to check Mira\'s record'), 'clearing the credential cleared the mode: ' + notes().join(' | '));
  assert(document.querySelector('#page-note a[href="#/settings"]'), 'a note does not link to Status');
  S.connected = false;
  renderPageNote();
  assert(notes().length === 1 && notes()[0].includes('Not connected. Reconnecting…') && !document.querySelector('#page-note a'), 'a page not connected does not say so, alone: ' + notes().join(' | '));`
	runPageInEngines(t, strings.Replace(pageNotePage, "%SCRIPT%", script, 1), map[string][]byte{
		"/state.js": []byte(`export const S = { connected: false, stats: null, cont: null };`),
	})
}

// A PROVIDER'S FAILURE IS SAID ON ITS TURN, IN AII OS'S WORDS (spec step
// 4). The real page, a born identity whose provider cannot be reached: the
// operator sends, the turn fails, and what reaches the turn says which
// provider AII OS could not get an answer from, why, and that they are
// unchanged; the composer draws nothing of it.
func TestAProvidersFailureIsSaidOnItsTurn(t *testing.T) {
	// Two turns' failures, each under its own turn, on the shipped frame
	// through its own socket (thread_real_page_browser_test.go).
	t.Run("two-turns-on-the-real-page", func(t *testing.T) { runOnTheRealPage(t, "turn") })
	t.Run("the-daemons-frame-to-the-turn", providersFailureReachesItsTurn)
}

// providersFailureReachesItsTurn: the real Go server and a real record
// (the in-memory store as the interaction reader). The handler records the
// operator's message, confirms it, and fails as an unreachable provider
// does; the page's own hook, unmodified, puts the daemon's frame under that
// message, by the whole reference the frame carries. The reader names the
// identity the recording does, as the app's reader does for its pages.
func providersFailureReachesItsTurn(t *testing.T) {
	st, err := store.NewMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	s := New("127.0.0.1", 0, &WSHandler{
		Speaker:      "identity",
		Interactions: identifiedReader{st, "fixture-identity"},
		GetStats:     func() (*StatsResponse, error) { return &StatsResponse{Name: "Mira", Version: "0.2.1-test"}, nil },
		GetConfig: func() *ConfigState {
			return &ConfigState{LLM: LLMConfigState{Provider: "Example AI", Model: "m1", ResolvedProvider: "Example AI", ResolvedModel: "m1"}}
		},
		HandleMessage: func(ctx context.Context, text string) (string, error) {
			ref, err := st.RecordRelatedConversation(ctx, "operator", text, interaction.TurnID(ctx), "", interaction.Details{})
			if err != nil {
				return "", err
			}
			ChatRecorded(ctx, &interaction.Location{Identity: "fixture-identity", Source: "recorded", Incarnation: st.InteractionIncarnation(), ID: ref.ID}, nil)
			return "", &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}
		},
	})
	st.SetInteractionObserver(s.PokeInteractions)
	runDashboardInEngines(t, s, "/", shellDriver+`
(async () => {
  const { ready } = await import('/recovery.js');
  await ready;
  const { S } = await import('/state.js');
  location.hash = '#/chat';
  await until(() => S.config && S.config.llm && S.identityExists && viewOn() === 'view-chat' && document.getElementById('msg-input'));
  if (!(S.config && S.config.llm && S.identityExists)) return fail('the page never read a born identity and its model: config ' + JSON.stringify(S.config && S.config.llm));
  const input = document.getElementById('msg-input');
  input.value = 'Are you there?';
  input.dispatchEvent(new Event('input', { bubbles: true }));
  document.getElementById('send-btn').click();
  // The newest such message: the three engines share this server and its
  // record, so an earlier engine's message of the same words is already
  // there, and its failure was never this page's to draw.
  const note = () => { const row = [...document.querySelectorAll('#thread-inner > [data-turn-id]')].filter(r => (r.querySelector(':scope > .msg.operator .body') || {}).textContent === 'Are you there?').at(-1); return row && row.querySelector(':scope > .msg.operator > .turn-failure'); };
  await until(() => note(), 10000);
  const said = note() ? note().textContent : '';
  if (said !== 'AII OS couldn\'t get an answer from Example AI: connection refused. Mira is unchanged; try again, or change the model in Settings.') return fail('the daemon\'s failure is not under its message in AII OS\'s words: "' + said + '"');
  // (This rig's daemon answers some reads "not available"; those are no turn's, and stay where they were.)
  const composerSaid = (document.getElementById('chat-error') || {}).textContent || '';
  if (composerSaid.includes('get an answer')) return fail('the composer drew the turn\'s failure: ' + composerSaid);
  report('OK');
})().catch(e => fail((e && e.message) || String(e)));
`)
}

// identifiedReader serves a record's pages under the identity that names
// them, sealed again, as the app's reader does (interaction_view.go): the
// bare store leaves a page's identity empty, and a reference names its
// occurrence under an identity.
type identifiedReader struct {
	interaction.Reader
	identity string
}

func (r identifiedReader) QueryInteractions(ctx context.Context, q interaction.Query) (*interaction.Page, error) {
	p, err := r.Reader.QueryInteractions(ctx, q)
	if err != nil {
		return nil, err
	}
	p.Identity = r.identity
	interaction.Seal(p)
	return p, nil
}

// THE PAGE-WIDE NOTE WAITS FOR BOTH FRAMES (spec step 4, amended). Connected,
// it says nothing until the status and the continuity have each been read
// since the connection was made: silence is not a claim that all is well,
// and a frame read on an earlier connection is not this one's. Without a
// connection it says only that; a reconnect redraws nothing stale.
func TestThePageNoteWaitsForBothFrames(t *testing.T) {
	script := `
  const safe = { mode: 'safe', safe_reason: 'their record did not verify' };
  S.connected = true; S.pageFrames = { status: false, continuity: false }; S.stats = { name: 'Mira', credential_warning: 'the key expires' }; S.cont = safe;
  renderPageNote();
  assert(notes().length === 0, 'a page that has read neither frame said something: ' + notes().join(' | '));
  S.pageFrames.status = true; renderPageNote();
  assert(notes().length === 0, 'a page that has read one frame said something: ' + notes().join(' | '));
  S.pageFrames.continuity = true; renderPageNote();
  assert(notes().length === 2, 'both frames read, the mode and the credential are not each said: ' + notes().join(' | '));
  S.connected = false; S.pageFrames = { status: false, continuity: false }; renderPageNote();
  assert(notes().length === 1 && notes()[0].includes('Not connected'), 'without a connection the page said more than that: ' + notes().join(' | '));
  S.connected = true; renderPageNote();
  assert(notes().length === 0, 'a reconnect redrew the old connection\'s notes before its frames: ' + notes().join(' | '));`
	runPageInEngines(t, strings.Replace(pageNotePage, "%SCRIPT%", script, 1), map[string][]byte{
		"/state.js": []byte(`export const S = { connected: false, stats: null, cont: null };`),
	})
}

// AT A PHONE'S WIDTH THE PAGE-WIDE NOTE IS PHONECHAT'S BAND (spec step 4,
// amended): the full width, square, edged below in its tone, the triangle
// beside the words; at a desktop's width it is the OsNote card, with no
// triangle.
func TestThePageNoteIsABandOnAPhone(t *testing.T) {
	css, err := staticFS.ReadFile("static/layout.css")
	if err != nil {
		t.Fatal(err)
	}
	for _, size := range []struct {
		w, h  int
		phone string
	}{{390, 844, "true"}, {1280, 900, "false"}} {
		page := `<!doctype html><link rel="stylesheet" href="/tokens/tokens.css"><link rel="stylesheet" href="/components/os-note/os-note.css"><link rel="stylesheet" href="/layout.css">
<body style="margin:0"><div id="page-note" role="status"></div>
<script type="module">
import { S } from '/state.js';
import { renderPageNote } from '/shell/page-note.js';
import { assert, run } from '/__harness.js';
run(() => {
  const phone = ` + size.phone + `;
  S.connected = true; S.pageFrames = { status: true, continuity: true }; S.stats = { name: 'Mira' }; S.cont = { mode: 'degraded_witness', degraded_since: '2026-10-30T12:00:00Z' };
  renderPageNote();
  const note = document.querySelector('#page-note .c-os-note'), icon = note && note.querySelector('.c-os-note__icon');
  assert(note, 'no note');
  const r = note.getBoundingClientRect(), cs = getComputedStyle(note);
  if (phone) {
    assert(innerWidth <= 767, 'the window is ' + innerWidth + ' px: no phone rule is in force');
    assert(Math.abs(r.left) < 1 && Math.abs(r.width - document.documentElement.clientWidth) < 1, 'the band is not the full width: ' + r.left + ' ' + r.width);
    assert(cs.borderTopLeftRadius === '0px' && cs.borderBottomStyle === 'solid' && cs.borderTopStyle === 'none', 'the band is not square and edged below: ' + cs.borderTopLeftRadius + ' ' + cs.borderTopStyle + ' ' + cs.borderBottomStyle);
    assert(icon && getComputedStyle(icon).display !== 'none' && icon.getBoundingClientRect().width > 0, 'the band has no triangle');
  } else {
    assert(cs.borderTopLeftRadius !== '0px' && (!icon || getComputedStyle(icon).display === 'none'), 'at a desktop width the note is not the card');
  }
});
</script>`
		runPageInEnginesAtSize(t, page, map[string][]byte{
			"/state.js":   []byte(`export const S = { connected: false, stats: null, cont: null };`),
			"/layout.css": css,
		}, size.w, size.h)
	}
}

// A TURN'S FAILURE STAYS ON ITS TURN (spec step 4, amended). The failure is
// an AII OS note under the operator's message the frame names (turn_ref), in
// the thread, where it stays: a redraw keeps it, a later turn neither clears
// nor inherits it, a message not drawn yet takes its note when it is, a
// failure that names no whole occurrence (a part of its reference missing,
// or none) is drawn on no turn, and the composer draws nothing of any.
func TestATurnsFailureStaysOnItsTurn(t *testing.T) {
	page := `<!doctype html><div id="announce" aria-live="polite"></div><div id="thread"><div id="thread-inner"></div></div>
<div id="composer-wrap"><textarea id="msg-input"></textarea><button id="send-btn">Send</button></div><button id="jump-latest" hidden>Latest</button>
<script type="module">
import { S } from '/state.js';
import { renderInteractionPages } from '/views/chat.js';
import { run } from '/__harness.js';
run(async assert => {
  S.stats = { name: 'Mira' }; S.identityExists = true;
  const row = (id, seq, role, content) => ({ id, sequence: String(seq), kind: 'message', role, content, turn_id: 't' + id, recorded_at: '2026-10-30T12:0' + seq + ':00Z', details: {} });
  const rows = [row('q1', 1, 'operator', 'Hello'), row('a1', 2, 'resident', 'Hello to you.'), row('q2', 3, 'operator', 'Are you there?')];
  const p = { identity: 'fixture', incarnation: 'disk', rows, has_older: false, has_newer: false };
  const pages = new Map([['recorded', p], ['transient', { identity: 'fixture', incarnation: 'memory', rows: [] }]]);
  const draw = () => renderInteractionPages(pages, () => {}, false);
  const noteOn = id => document.querySelector('[data-turn-id="' + id + '"] > .msg.operator > .turn-failure');
  draw();
  const first = 'AII OS couldn\'t get an answer from Example AI: connection refused. Mira is unchanged; try again, or change the model in Settings.';
  const ref = id => ({ identity: 'fixture', source: 'recorded', incarnation: 'disk', id });
  S.turnFailed(first, ref('q2'));
  assert(noteOn('q2') && noteOn('q2').textContent === first && !noteOn('q1'), 'the failure is not under the message it names, alone');
  assert(noteOn('q2').getAttribute('role') === 'alert' && noteOn('q2').classList.contains('c-os-note--bad'), 'the failure is not AII OS\'s note, said as it arrives');
  draw();
  assert(noteOn('q2') && noteOn('q2').textContent === first, 'a redraw lost the failure');
  rows.push(row('q3', 4, 'operator', 'Try again?')); draw();
  assert(!noteOn('q3') && noteOn('q2'), 'a later turn inherited the failure, or cleared it');
  const second = 'Example AI did not answer in time. Mira is unchanged.';
  S.turnFailed(second, ref('q3'));
  rows.push(row('a3', 5, 'resident', 'I am here.')); draw();
  assert(noteOn('q2').textContent === first && noteOn('q3').textContent === second, 'each failure did not stay on its own turn');
  // A message not drawn yet takes its note when it is.
  const third = 'identity error: the record could not be written';
  S.turnFailed(third, ref('q6'));
  assert(!document.querySelector('.turn-failure[data-text="' + third + '"]'), 'a failure was drawn before its message was');
  rows.push(row('q6', 6, 'operator', 'And now?')); draw();
  assert(noteOn('q6') && noteOn('q6').textContent === third, 'the held failure did not take its place when its message was drawn');
  // A failure that names no whole occurrence is drawn on no turn.
  const before = document.querySelectorAll('.turn-failure').length;
  for (const part of ['identity', 'source', 'incarnation', 'id']) {
    const half = ref('q1'); delete half[part];
    S.turnFailed('identity error: no ' + part, half);
  }
  const said = document.getElementById('announce').textContent;
  draw();
  assert(!noteOn('q1') && document.querySelectorAll('.turn-failure').length === before, 'a reference missing a part was placed on a turn');
  assert(['identity', 'source', 'incarnation', 'id'].every(part => said.includes('identity error: no ' + part)), 'a reference missing a part was not said as one that names no message: "' + said + '"');
  S.turnFailed('identity error: unrecorded', null); draw();
  assert(document.querySelectorAll('.turn-failure').length === before, 'a failure that names no message was drawn on a turn');
  assert(!document.getElementById('chat-error') && !document.querySelector('#composer-wrap .c-os-note'), 'the composer drew something of a failure');
});
</script>`
	runPageInEngines(t, page, chatModuleRig(t))
}

// A REPLY IS IN THEIR VOICE, AND CAN BE QUOTED TO THEM (spec step 4,
// amended): the serif at 19/30; Copy is a chip,
// 32 px chips named by their words; and Quote puts their first sentence in
// the reply box in the page's words around it.
func TestAReplyIsInTheirVoiceAndOffersCopy(t *testing.T) {
	css, err := staticFS.ReadFile("static/layout.css")
	if err != nil {
		t.Fatal(err)
	}
	page := `<!doctype html><link rel="stylesheet" href="/tokens/tokens.css"><link rel="stylesheet" href="/layout.css">
<div id="thread"><div id="thread-inner"></div></div>
<div id="composer-wrap"><textarea id="msg-input"></textarea><button id="send-btn">Send</button></div><button id="jump-latest" hidden>Latest</button>
<script type="module">
import { S } from '/state.js';
import { renderInteractionPages } from '/views/chat.js';
import { run } from '/__harness.js';
run(async assert => {
  S.stats = { name: 'Mira' }; S.identityExists = true;
  const rows = [{ id: 'a1', sequence: '1', kind: 'message', role: 'resident', content: 'The build passed. I sent Alex the summary.', turn_id: 't1', recorded_at: '2026-10-30T12:01:00Z', details: {} }];
  renderInteractionPages(new Map([['recorded', { identity: 'fixture', incarnation: 'disk', rows }], ['transient', { identity: 'fixture', incarnation: 'memory', rows: [] }]]), () => {}, false);
  const reply = document.querySelector('.msg.identity'), body = reply.querySelector('.body'), cs = getComputedStyle(body);
  assert(/Iowan|Charter|Georgia|serif/.test(cs.fontFamily) && cs.fontSize === '19px' && cs.lineHeight === '30px', 'the reply is not in their voice, the serif at 19/30: ' + cs.fontFamily + ' ' + cs.fontSize + '/' + cs.lineHeight);
  // Copy is a chip named by its word, at least 32 px tall (section 6); Quote is not on a reply.
  const chips = [...reply.querySelectorAll('.msg-acts button')].map(b => b.textContent.trim());
  assert(chips.join() === 'Copy', 'a reply offers ' + chips.join(', ') + ', not Copy alone');
  const copy = reply.querySelector('.msg-acts button'), box = copy.getBoundingClientRect();
  assert(box.height >= 32 && getComputedStyle(copy).backgroundColor !== 'rgba(0, 0, 0, 0)', 'Copy is not a 32 px chip on the surface: ' + box.height + ' px, ' + getComputedStyle(copy).backgroundColor);
});
</script>`
	modules := chatModuleRig(t)
	modules["/layout.css"] = css
	runPageInEngines(t, page, modules)
}
