//go:build !windows

// Runs through the unix browser-engine rig (browserengine_test.go, same tag).
package dashboard

import (
	"strings"
	"testing"
)

// The Logs panel's page: the shell the view makes its section in, the
// socket stubbed so what the panel asks is recorded, the frames set by hand.
const technicalLogsPage = `<!doctype html><html lang="en"><head><meta charset="utf-8">
<link rel="stylesheet" href="/tokens/tokens.css"><link rel="stylesheet" href="/views/settings/technical/technical.css"></head>
<body><div class="views" id="views"></div>
<script type="module">
import { S } from '/state.js';
import { renderTechnical, readTechnical } from '/views/settings/technical/technical.js';
import W from '/strings/settings-technical.js';
import { fill } from '/util.js';
import { run, until } from '/__harness.js';
const asked = () => window.__asked || [];
const said = () => document.querySelector('.technical-logs > .ledger-said')?.textContent || '';
const files = () => [...document.querySelectorAll('.technical-logs__file')];
const tail = () => document.querySelector('.technical-logs__tail');
const enter = () => { document.getElementById('view-technical').classList.add('on'); location.hash = '#/settings/technical/logs'; readTechnical(); renderTechnical(); };
run(async assert => {
%SCRIPT%
});
</script></body></html>`

func runTechnicalLogsPage(t *testing.T, script string) {
	t.Helper()
	page := strings.Replace(technicalLogsPage, "%SCRIPT%", script, 1)
	runPageInEngines(t, page, map[string][]byte{
		"/ws.js": []byte("export function query(q, extra) { if (window.__offline) return ''; (window.__asked ||= []).push(q + (extra ? JSON.stringify(extra) : '')); return 'q' + window.__asked.length; }\nexport function send(m) { (window.__sent ||= []).push(m); return 's'; }\n"),
	})
}

// TECHNICAL → LOGS (spec step 19): entering the panel asks for the list and
// says it is loading; the list is one row per file in words (name, size,
// when); pressing a file asks for its last lines and shows them under the
// list, or says the file is empty; no files is said as what it is, and
// logging switched off as what it is; a render with nothing changed
// writes nothing.
func TestTechnicalLogsListsTheFilesAndReadsOne(t *testing.T) {
	if testing.Short() {
		t.Skip("browser engines are not run in -short")
	}
	runTechnicalLogsPage(t, `
  S.connected = true; S.stats = { name: 'Mira' }; S.identityExists = true;
  enter();
  await until(() => asked().length === 1, 10000);
  assert(asked().join('|') === 'logs', 'entering the panel must ask for the list once, not ' + JSON.stringify(asked()));
  assert(said() === W.logsLoading, 'before the list arrives the panel says ' + JSON.stringify(said()));
  const current = document.querySelector('.c-side-nav [aria-current="page"]');
  assert(current && current.textContent === W.logs, 'the open panel is not Logs: ' + (current && current.textContent));
  // The list lands (as ws.js would set it, then draw the view again).
  S.logsList = [{ name: 'aii.log', size: 2048, mod_at: '2026-10-31T12:00:00Z' }, { name: 'aii.log.1', size: 3 << 20, mod_at: '2026-10-30T12:00:00Z' }];
  renderTechnical();
  assert(said() === '', 'with files listed nothing is said beside them: ' + JSON.stringify(said()));
  assert(files().length === 2, files().length + ' file rows');
  const first = files()[0].querySelector('button').textContent;
  assert(first.startsWith('Current log · 2 KB · '), 'the current log is not said in words: ' + first);
  const second = files()[1].querySelector('button').textContent;
  assert(second.endsWith(' · 3 MB') && !second.includes('aii.log'), 'an older log is said by when and its size: ' + second);
  const older = files()[1].closest('details');
  assert(older && !older.open && older.querySelector('summary').textContent === 'Older logs (1)', 'the older logs are not folded under their count');
  // Pressing a file asks for its lines and says it is reading them.
  files()[0].querySelector('button').click();
  await until(() => asked().length === 2, 10000);
  assert(asked().join('|') === 'logs|logs{"name":"aii.log"}', 'the press must ask for that file: ' + JSON.stringify(asked()));
  assert(files()[0].hasAttribute('aria-current'), 'the chosen file is not marked');
  assert(tail().textContent === fill(W.logsTailLoading, { name: 'aii.log' }), 'while reading the tail says ' + JSON.stringify(tail().textContent));
  // The lines land.
  S.logTail = { name: 'aii.log', lines: ['one line', 'two lines'] };
  renderTechnical();
  assert(tail().querySelector('h4').textContent === fill(W.logsTail, { name: 'aii.log' }), 'the tail is not headed by its name');
  assert(tail().querySelector('pre').textContent === 'one line\ntwo lines', 'the lines are not shown as they are: ' + JSON.stringify(tail().querySelector('pre').textContent));
  // A render with nothing changed writes nothing: the same nodes stand.
  const pre = tail().querySelector('pre'), row = files()[0];
  renderTechnical();
  assert(document.contains(pre) && document.contains(row), 'a render with nothing changed replaced the nodes');
  // An empty file says so.
  S.logTail = { name: 'aii.log', lines: [] };
  renderTechnical();
  assert(tail().querySelector('.ledger-note')?.textContent === fill(W.logsTailEmpty, { name: 'aii.log' }), 'an empty file is not said: ' + tail().textContent);
  // No files, and logging switched off, each said as what it is.
  S.logsList = []; S.logFile = ''; S.logTail = null;
  renderTechnical();
  assert(said() === W.logsNone && !files().length, 'no files is not said: ' + JSON.stringify(said()));
  S.config = { logs: { dir: '' } };
  renderTechnical();
  assert(said() === W.logsOff, 'logging off is not said: ' + JSON.stringify(said()));
  assert(window.__sent === undefined, 'the panel sent something: ' + JSON.stringify(window.__sent));
`)
}

// A TAIL IS THE FILE'S IT NAMES, AND A REFUSED READ IS SAID (a review's two
// returns of 2026-10-31 17:37Z): the operator presses A, then B before A
// answers; A's late answer is not shown as B's lines, B's own is; a refused
// list or tail says so where it was loading, the next press asks again, and
// not connected is said as such.
func TestTechnicalLogsOwnsItsTailsAndSaysARefusedRead(t *testing.T) {
	if testing.Short() {
		t.Skip("browser engines are not run in -short")
	}
	runTechnicalLogsPage(t, `
  S.connected = true; S.stats = { name: 'Mira' }; S.identityExists = true;
  enter();
  await until(() => asked().length === 1, 10000);
  S.logsList = [{ name: 'a.log', size: 10, mod_at: '' }, { name: 'b.log', size: 20, mod_at: '' }];
  renderTechnical();
  // A pressed, then B before A answers.
  files()[0].querySelector('button').click();
  await until(() => asked().length === 2, 10000);
  files()[1].querySelector('button').click();
  await until(() => asked().length === 3, 10000);
  assert(tail().textContent === fill(W.logsTailLoading, { name: 'b.log' }), 'B selected must be reading B: ' + tail().textContent);
  // A's answer arrives late: not B's lines.
  S.logTail = { name: 'a.log', lines: ['from a'] };
  renderTechnical();
  assert(tail().textContent === fill(W.logsTailLoading, { name: 'b.log' }), 'A late answer was shown as B: ' + tail().textContent);
  // B's own answer.
  S.logTail = { name: 'b.log', lines: ['from b'] };
  renderTechnical();
  assert(tail().querySelector('pre')?.textContent === 'from b', 'B own lines not shown: ' + tail().textContent);
  // A refused tail is said, and the next press asks again.
  files()[0].querySelector('button').click();
  await until(() => asked().length === 4, 10000);
  assert(S.logsAskFailed('q4', 'the file is gone'), 'the refusal of our own ask must be ours');
  assert(tail().textContent === fill(W.logsTailFailed, { name: 'a.log', why: 'the file is gone' }), 'a refused tail is not said: ' + tail().textContent);
  assert(!S.logsAskFailed('q99', 'someone else'), 'a refusal of another ask must not be ours');
  files()[0].querySelector('button').click();
  await until(() => asked().length === 5, 10000);
  assert(tail().textContent === fill(W.logsTailLoading, { name: 'a.log' }), 'a press after a refusal must read again: ' + tail().textContent);
  // A refused list is said where the list was loading.
  enter();
  await until(() => asked().length === 6, 10000);
  assert(S.logsAskFailed('q6', 'no directory'), 'the list refusal must be ours');
  assert(said() === fill(W.logsListFailed, { why: 'no directory' }), 'a refused list is not said: ' + JSON.stringify(said()));
  // Not connected is said as such.
  S.connected = false;
  enter();
  await until(() => asked().length === 7, 10000);
  assert(said() === W.logsOffline, 'offline is not said: ' + JSON.stringify(said()));
  // A tail being read when the socket is lost says so, in the same words,
  // and the next press reads it again (the carry of 17:54Z).
  S.connected = true;
  S.logsList = [{ name: 'a.log', size: 10, mod_at: '' }];
  renderTechnical();
  files()[0].querySelector('button').click();
  await until(() => asked().length === 8, 10000);
  assert(tail().textContent === fill(W.logsTailLoading, { name: 'a.log' }), 'before the loss the tail is reading: ' + tail().textContent);
  S.connected = false;
  S.logsConnectionLost();
  assert(tail().textContent === W.logsOffline, 'a tail pending at the loss must say the connection is gone: ' + tail().textContent);
  // A press while still offline sends nothing (the socket carries no ask)
  // and the panel keeps saying so, not that it is reading.
  window.__offline = true;
  files()[0].querySelector('button').click();
  await until(() => false, 300); // waits by design: the ask resolves through a module load, and nothing observable marks a press that sent nothing
  assert(asked().length === 8 && tail().textContent === W.logsOffline, 'a press while offline must send nothing and keep the offline sentence: ' + asked().length + ' asks, ' + JSON.stringify(tail().textContent));
  window.__offline = false;
  S.connected = true;
  files()[0].querySelector('button').click();
  await until(() => asked().length === 9, 10000);
  assert(tail().textContent === fill(W.logsTailLoading, { name: 'a.log' }), 'a press after the loss must read again: ' + tail().textContent);
`)
}
