package dashboard

// The Model page's local-model part (views/settings/local.js), executed in
// node with the four modules that touch the page or the socket stubbed.
// a review 2026-11-06 13:31Z findings 1 and 2: a lost connection left Look or
// Add disabled until a reload, and a second Look could leave the menu
// showing one model while Add sent another. Each case loads a fresh copy of
// the part, so no case leans on another's state.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const localPartStubs = `
-- views/settings/save.js
export let configResult = null;
export const configWaitingFor = () => false;
export function sendConfigChanges() {}
-- views/settings/parts.js
export function renderSettings() { globalThis.__renders++; }
-- state.js
export const S = { view: 'settings' };
-- ws.js
export function send(m) { globalThis.__sent.push(m); return 'r' + globalThis.__sent.length; }
`

const localPartDriver = `
let failures = 0;
function check(cond, msg) { if (!cond) { console.error('FAIL: ' + msg); failures++; } }

let H = {};
globalThis.document = { addEventListener(t, f) { (H[t] ||= []).push(f); } };
let seq = 0;
// state.js is one module for every case; only local.js loads fresh.
const { S } = await import('./state.js');
async function fresh() {
  H = {}; globalThis.__sent = []; globalThis.__renders = 0; S.providers = undefined;
  return import('./views/settings/local.js?case=' + (++seq));
}
const fire = (t, target) => (H[t] || []).forEach(f => f({ target }));
const button = (attr, data) => ({ disabled: false, dataset: data || {}, hasAttribute: a => a === attr, closest() { return this; } });
const look = () => fire('click', button('data-local-look'));
const add = port => fire('click', button('data-local-add', { localAdd: String(port) }));
const pick = (port, value) => fire('change', { id: '', value, dataset: { localPick: String(port) }, closest: () => true });
const lastSent = type => [...__sent].reverse().find(m => m.type === type);
const shown = (P, port) => { const m = P.localModelHTML({}).match(new RegExp('data-local-pick="' + port + '"[^>]*>(.*?)</select>')); if (!m) return null; const s = m[1].match(/<option value="([^"]*)" selected>/) || m[1].match(/<option value="([^"]*)"/); return s ? s[1] : null; }; // no selected option: a browser shows the first
const addButton = (P, port) => (P.localModelHTML({}).match(new RegExp('data-local-found="' + port + '"[^]*?(<button[^>]*data-local-add[^>]*>[^<]*</button>)')) || [])[1] || '';
const lookButton = P => (P.localModelHTML({}).match(/<button[^>]*data-local-look[^>]*>[^<]*<\/button>/) || [''])[0];
const OLLAMA = 'http://127.0.0.1:11434/v1';

// The stub's send numbers each case's requests r1, r2, ... in order; a look's id is its position.
const idOf = m => 'r' + (__sent.indexOf(m) + 1);
const answer = (P, models) => P.localPart({ type: 'local_found', request_id: idOf(lastSent('local_look')), local_found: [{ port: 11434, address: OLLAMA, usual: 'Ollama', models }] });

// F2. A second look whose list no longer has the picked model: the menu and
// Add name the same model, and it is one the newest list offers.
{
  const P = await fresh();
  P.localModelHTML({});
  look(); answer(P, ['A', 'B']);
  pick(11434, 'B');
  check(shown(P, 11434) === 'B', 'F2 setup: picked B not shown: ' + shown(P, 11434));
  look(); answer(P, ['A', 'C']);
  const s = shown(P, 11434);
  add(11434);
  const sent = lastSent('local_add');
  check(sent && sent.local_server.model === s, 'F2: menu shows ' + s + ' but Add sent ' + (sent && sent.local_server.model));
  check(['A', 'C'].includes(sent && sent.local_server.model), 'F2: Add sent a model the newest list does not offer: ' + (sent && sent.local_server.model));
}
// F2, one model left: the only model is shown and sent.
{
  const P = await fresh();
  P.localModelHTML({});
  look(); answer(P, ['A', 'B']);
  pick(11434, 'B');
  look(); answer(P, ['C']);
  add(11434);
  check(shown(P, 11434) === 'C' && lastSent('local_add').local_server.model === 'C', 'F2 one left: shown ' + shown(P, 11434) + ', sent ' + lastSent('local_add').local_server.model);
}
// F2 kept: a second look that still lists the pick keeps it.
{
  const P = await fresh();
  P.localModelHTML({});
  look(); answer(P, ['A', 'B']);
  pick(11434, 'B');
  look(); answer(P, ['B', 'C']);
  add(11434);
  check(shown(P, 11434) === 'B' && lastSent('local_add').local_server.model === 'B', 'F2 kept: a pick still offered was dropped');
}

// F3. Drawn with the box on before any report: the part asks once and
// says it is checking, never that the local model isn't answering.
{
  const P = await fresh();
  const L = (await import('./strings/settings-local.js')).default;
  const c = { agency: { prefer_local_for_roles: true } };
  const h = P.localModelHTML(c);
  check(!h.includes('answering') && h.includes(L.checking), 'F3: before any report the part claims an outage, or says nothing of checking');
  P.localModelHTML(c);
  check(__sent.filter(m => m.type === 'query' && m.query === 'providers').length === 1, 'F3: the part asked for the report more than once');
  P.localPart({ type: 'providers', request_id: 'p', local_models: { marked: 1 } });
  check(P.localModelHTML(c).includes('answering'), 'F3: a report of a silent server is not said once it arrives');
}

// F1. Connection lost during a look: Look is pressable again and asks again.
{
  const P = await fresh();
  P.localModelHTML({});
  look();
  check(lookButton(P).includes('disabled'), 'F1 setup: Look not disabled while asking');
  check(typeof P.localConnectionLost === 'function', 'F1: local.js has no localConnectionLost');
  if (typeof P.localConnectionLost === 'function') {
    check(P.localConnectionLost() === true, 'F1: a lost look did not report a change');
    check(!lookButton(P).includes('disabled'), 'F1: Look still disabled after the connection was lost: ' + lookButton(P));
    const before = __sent.filter(m => m.type === 'local_look').length;
    look();
    check(__sent.filter(m => m.type === 'local_look').length === before + 1, 'F1: pressing Look after the loss asked nothing');
    check(P.localConnectionLost() === true && P.localConnectionLost() === false, 'F1: a loss with nothing out reported a change');
  }
}
// F1. Connection lost during an add, and the server had saved it: the next
// providers answer (every reconnect asks) shows it as added.
{
  const P = await fresh();
  P.localModelHTML({});
  look(); answer(P, ['A']);
  add(11434);
  check(addButton(P, 11434).includes('disabled'), 'F1 setup: Add not disabled while adding');
  if (typeof P.localConnectionLost === 'function') {
    P.localConnectionLost();
    check(!addButton(P, 11434).includes('disabled'), 'F1: Add still disabled after the connection was lost: ' + addButton(P, 11434));
    P.localPart({ type: 'providers', request_id: 'reconnect-1', providers: [{ name: 'Local server, port 11434', endpoint: OLLAMA, local: true }] });
    check(P.localModelHTML({}).includes('Ollama on port 11434 is added.'), 'F1: an add the server saved is not shown as added after reconnect');
  }
}
// F1. Connection lost during an add the server never saved: Add is offered again, nothing claims it was added.
{
  const P = await fresh();
  P.localModelHTML({});
  look(); answer(P, ['A']);
  add(11434);
  if (typeof P.localConnectionLost === 'function') {
    P.localConnectionLost();
    P.localPart({ type: 'providers', request_id: 'reconnect-1', providers: [{ name: 'Elsewhere', endpoint: 'http://127.0.0.1:9999/v1', local: true }] });
    const h = P.localModelHTML({});
    check(!h.includes('Ollama on port 11434 is added.') && addButton(P, 11434) && !addButton(P, 11434).includes('disabled'), 'F1: an unsaved add is claimed, or Add is not offered again: ' + h);
  }
}
// F1 late. The add saved AFTER the reconnect's providers answer: the save's
// broadcast (no request id) shows it as added, and no Add is offered.
{
  const P = await fresh();
  P.localModelHTML({});
  look(); answer(P, ['A']);
  add(11434);
  P.localConnectionLost();
  P.localPart({ type: 'providers', request_id: 'reconnect-1', providers: [] });
  P.localPart({ type: 'providers', request_id: '', providers: [{ name: 'Local server, port 11434', endpoint: OLLAMA, local: true }] });
  check(P.localModelHTML({}).includes('Ollama on port 11434 is added.') && !addButton(P, 11434), 'F1 late: a save after the reconnect snapshot is not shown as added, or Add is still offered');
}
// Another page: it only looked; an entry saved elsewhere at the found address
// reaches it by broadcast and the row says added, with no Add.
{
  const P = await fresh();
  P.localModelHTML({});
  look(); answer(P, ['A']);
  P.localPart({ type: 'providers', request_id: '', providers: [{ name: 'Local server, port 11434', endpoint: OLLAMA, local: true }] });
  check(P.localModelHTML({}).includes('Ollama on port 11434 is added.') && !addButton(P, 11434), 'F1 other page: an entry saved elsewhere still offers Add');
}
// A Look result older than the providers already received: ws.js stores
// S.providers, then hands the message to the part, as prov does here. The
// Look's snapshot was taken before the save, so its row carries no entry.
const prov = (P, providers) => { S.providers = providers; P.localPart({ type: 'providers', request_id: '', providers }); };
const SAVED = [{ name: 'Local server, port 11434', endpoint: OLLAMA, local: true }];
// Providers before the first Look's result: the row the Look creates is added.
{
  const P = await fresh();
  P.localModelHTML({});
  look();
  prov(P, SAVED);
  answer(P, ['A']);
  check(P.localModelHTML({}).includes('Ollama on port 11434 is added.') && !addButton(P, 11434), 'F1 order first look: a saved server is offered Add again');
}
// Providers before a replacement Look's result: the added row stays added.
{
  const P = await fresh();
  P.localModelHTML({});
  look(); answer(P, ['A']);
  look();
  prov(P, SAVED);
  check(P.localModelHTML({}).includes('Ollama on port 11434 is added.'), 'F1 order setup: the broadcast did not mark the row');
  answer(P, ['A']);
  check(P.localModelHTML({}).includes('Ollama on port 11434 is added.') && !addButton(P, 11434), 'F1 order replacement look: a saved server is offered Add again');
}
// Only an entry at the exact address counts: one elsewhere leaves Add offered.
{
  const P = await fresh();
  P.localModelHTML({});
  look();
  prov(P, [{ name: 'Local server, port 8080', endpoint: 'http://127.0.0.1:8080/v1', local: true }]);
  answer(P, ['A']);
  check(addButton(P, 11434) && !P.localModelHTML({}).includes('is added.'), 'F1 order other address: a row was marked added for an entry at another address');
}

if (failures) process.exit(1);
console.log('LOCAL-PART-OK');
`

func TestLocalModelPartNode(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not available — local-model part check skipped")
	}
	tmp := t.TempDir()
	put := func(name string, raw []byte) {
		dst := filepath.Join(tmp, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dst, raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{"views/settings/local.js", "views/settings/local-rule.js", "util.js", "strings/settings-local.js"} {
		raw, rerr := staticFS.ReadFile("static/" + f)
		if rerr != nil {
			t.Fatalf("read embedded %s: %v", f, rerr)
		}
		put(f, raw)
	}
	for _, part := range strings.Split(localPartStubs, "\n-- ")[1:] {
		name, body, _ := strings.Cut(part, "\n")
		put(strings.TrimSpace(name), []byte(body))
	}
	put("package.json", []byte(`{"type":"module"}`))
	put("driver.js", []byte(localPartDriver))
	out, err := exec.Command(node, filepath.Join(tmp, "driver.js")).CombinedOutput()
	if err != nil || !strings.Contains(string(out), "LOCAL-PART-OK") {
		t.Fatalf("local-model part failed (%v):\n%s", err, out)
	}
}
