//go:build !windows

package dashboard

import "testing"

// A LOST CONNECTION SAYS A START WAS NOT CONFIRMED ONLY WHEN ONE WAS ASKED
// (a review's read of stack B). On the shipped page and its socket: with no model
// chosen the socket drops and comes back, and Begin stays held for want of a
// model with no word of a start; with a model on this computer still being
// asked for, the same. A save of the notes choice that is lost says nothing
// has begun, and a birth that was asked and lost says it was not confirmed, as
// before; each leaves Begin pressable for the model chosen.
const firstRunLossJS = `import { report, until } from '/__harness.js';
const sockets = window.__sockets, sent = window.__sent;
const $ = id => document.getElementById(id);
const problems = [];
const check = (ok, what) => { if (!ok) problems.push(what); };
const last = () => sockets[sockets.length - 1];
const pause = ms => new Promise(r => setTimeout(r, ms));
const hosted = { name: 'OpenAI', endpoint: 'https://api.openai.test/v1', models: ['o-small'], default_model: 'o-small', chat: true };
const local = { name: 'Local server', endpoint: 'http://127.0.0.1:8080/v1', local: true, models: [], chat: true, preselect: true, preselect_why: 'found on this computer' };
const said = () => $('fb-result').textContent;
const started = () => /start was confirmed|Reload to see/.test(said());
// unborn opens a socket, says there is no identity yet and lists the providers.
async function unborn(providers) {
  last().open(); last().say({ type: 'status', stats: { name: '(not born)' } });
  await until(() => $('fb-birth') && $('fb-choices'), 5000);
  last().say({ type: 'providers', providers });
  await pause(200);
}
// drop loses the socket and waits for the page to make the next.
async function drop() {
  const count = sockets.length;
  last().drop(); await pause(200);
  const held = $('fb-birth').disabled, line = said();
  await until(() => sockets.length > count, 15000);
  return { held, line };
}
async function run() {
  await until(() => sockets.length > 0);
  await unborn([hosted]);
  check($('fb-birth').disabled && said() === 'Choose a model first.', 'before the loss Begin is not held with its line: "' + said() + '"');
  // No choice made: lost and back.
  let lost = await drop();
  check(lost.held && !/start was confirmed|Reload to see/.test(lost.line), 'with no model chosen a lost connection made Begin pressable or said a start: ' + lost.held + ' "' + lost.line + '"');
  await unborn([hosted]);
  check($('fb-birth').disabled && !started(), 'with no model chosen the connection came back to a Begin pressable or a start said: "' + said() + '"');
  // A model on this computer still being asked for: lost and back.
  await unborn([local, hosted]);
  check($('fb-birth').disabled, 'Begin can be pressed while the model on this computer is still asked for');
  lost = await drop();
  check(lost.held && !/start was confirmed|Reload to see/.test(lost.line), 'with the model on this computer still asked for a lost connection made Begin pressable or said a start: ' + lost.held + ' "' + lost.line + '"');
  await unborn([hosted]);
  // A save of the notes choice, lost: nothing has begun, and Begin is pressable for the model chosen.
  const pick = () => { const r = document.querySelector('[data-row="OpenAI"] input'); r.checked = true; r.dispatchEvent(new Event('change')); };
  pick(); await pause(100);
  check(!$('fb-birth').disabled, 'with a model chosen Begin is still held');
  $('fb-birth').click(); await pause(50);
  check(sent.some(f => f.type === 'config_set'), 'Begin did not save the notes choice first');
  lost = await drop();
  check(!lost.held && lost.line === 'Not connected. Nothing has begun.', 'a lost save is not said as nothing begun, or left Begin held: ' + lost.held + ' "' + lost.line + '"');
  // A birth asked, then lost: said as not confirmed, and Begin is pressable again.
  await unborn([hosted]);
  last().say({ type: 'config', config: { genesis: { agent_memory_off: false, agent_memory_found: false } } }); await pause(100);
  pick(); await pause(100);
  const before = sent.length;
  $('fb-birth').click(); await pause(50);
  check(sent.slice(before).some(f => f.type === 'genesis'), 'Begin did not ask for the birth: ' + JSON.stringify(sent.slice(before).map(f => f.type)));
  lost = await drop();
  check(!lost.held && /start was confirmed/.test(lost.line), 'a lost birth is not said as a start not confirmed: ' + lost.held + ' "' + lost.line + '"');
  report(problems.length ? 'FAIL: ' + problems.join('\n  - ') : 'OK');
}
run().catch(e => report('FAIL: the run stopped: ' + ((e && e.message) || e)));
`

func TestALostConnectionSaysAStartOnlyWhenOneWasAsked(t *testing.T) {
	runRealPage(t, statusFakeSocketJS, firstRunLossJS, homeStyles)
}
