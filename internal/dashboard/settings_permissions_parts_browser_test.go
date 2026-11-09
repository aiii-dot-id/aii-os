//go:build !windows

package dashboard

import "testing"

// SETTINGS → PLUGINS AND PERMISSIONS' PARTS ARE THE BOARD (spec step 16a,
// the board SettingsPermissions), over the shipped modules. Folders their
// tools may reach: their home always, each granted folder with Remove, a
// field to add one, and the sandbox's switch at the end of its row, whose
// words turn it as the box does and open the warning before anything is
// asked. Other assistants' notes, said once. Tools: a row each, the plugin
// a tool comes from, the switch at the end of its row, pressed by its words
// too. Names said when the identity has one, "they" when not. The parts'
// stylesheet comes when they are first drawn.
func TestSettingsPermissionsPartsAreTheBoard(t *testing.T) {
	page := `<!doctype html>
<div id="settings-stack"></div>
<script type="module">
import { S } from './state.js';
import { frames } from './ws.js';
import { sandboxCardHTML, wireSandboxCard } from './sandbox.js';
import { toolsCardHTML, wireToolsCard } from './tools.js';
import { assert, run, until } from './__harness.js';
const st = document.getElementById('settings-stack');
const draw = () => { st.innerHTML = sandboxCardHTML() + toolsCardHTML(); wireSandboxCard(st); wireToolsCard(st); };
S.renderSandbox = draw; S.renderTools = draw;
const text = sel => (st.querySelector(sel) || { textContent: '' }).textContent;
run(async () => {
  const css = () => document.querySelector('link[href="./views/settings/permissions.css"]');
  assert(!css(), 'the parts\' stylesheet was asked for before they were drawn');
  S.stats = { name: 'Taylor' };
  S.sandbox = { root: '/home/rowan', extra_roots: ['/work/projects'], off: false, agent_notes: { copied: false } };
  S.tools = [{ name: 'shell', description: 'Runs a command.', enabled: true }, { name: 'calendar.list', description: 'Lists events.', enabled: true, plugin: 'google' }];
  draw();
  await until(() => css()?.sheet);
  assert(getComputedStyle(st.querySelector('[data-folders]')).maxWidth === '820px', 'the parts\' stylesheet did not load');
  const heads = [...st.querySelectorAll('h3')].map(h => h.textContent);
  assert(JSON.stringify(heads) === JSON.stringify(["Folders Taylor's tools may reach", "Other assistants' notes", 'Tools · 2']), 'the parts: ' + JSON.stringify(heads));
  const rows = [...st.querySelectorAll('[data-folders] li')];
  assert(rows.length === 2 && rows[0].textContent === 'Their home · always/home/rowan' && rows[1].textContent === '/work/projectsRemove', 'the folder rows: ' + JSON.stringify(rows.map(r => r.textContent)));
  assert(st.querySelector('label[for="sbx-add"]').textContent === 'Add a folder', 'the field to add a folder');
  // The switch's words turn it as the box does: the warning opens and nothing is asked.
  const asked = () => frames.filter(f => f.type === 'sandbox_switch');
  st.querySelector('[data-folders] .permissions__switch .permissions__words').click();
  assert(st.querySelector('[data-sandbox-warning]') && asked().length === 0, 'pressing the switch\'s words did not open the warning, or asked the host');
  document.getElementById('sbx-off-cancel').click();
  assert(!st.querySelector('[data-sandbox-warning]') && asked().length === 0, 'Cancel did not close the warning, or asked the host');
  assert((st.querySelector('[data-agent-notes]').textContent.match(/Claude Code/g) || []).length === 1, 'the notes part says what it copies more than once');
  // A tool's words press its switch.
  const toolRows = [...st.querySelectorAll('[data-tools] li')];
  assert(toolRows[1].textContent.startsWith('calendar.list · from the google plugin'), 'a plugin\'s tool does not say its plugin: ' + toolRows[1].textContent);
  toolRows[0].querySelector('.permissions__words').click();
  const toggled = frames.filter(f => f.type === 'tool_toggle');
  assert(toggled.length === 1 && toggled[0].tool === 'shell' && toggled[0].enabled === false, 'pressing a tool\'s words did not press its switch: ' + JSON.stringify(toggled));
  // Not yet named: "they".
  S.stats = {}; draw();
  S.sandbox = Object.assign({}, S.sandbox, { off: true }); draw();
  assert(!st.querySelector('#sbx-hint') && !/only here/.test(text('[data-folders]')), 'an unnamed identity\'s folders are said to be their only reach while the sandbox is off');
  S.sandbox = Object.assign({}, S.sandbox, { off: false }); draw();
  assert(text('[data-folders] h3') === 'Folders their tools may reach' && text('#tools-hint') === 'A tool switched off is not offered to them, and a call to it is refused.', 'the unnamed forms: ' + text('[data-folders] h3') + ' / ' + text('#tools-hint'));
});
</script>`
	modules := map[string][]byte{}
	for _, path := range []string{"static/sandbox.js", "static/tools.js"} {
		data, err := staticFS.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		modules["/"+path[len("static/"):]] = data
	}
	modules["/state.js"] = []byte(`export const S = { view: 'settings' };`)
	modules["/util.js"] = utilStub(`export const $ = id => document.getElementById(id); export const esc = v => String(v ?? '').replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/"/g,'&quot;');`)
	modules["/ws.js"] = []byte(`export const frames = []; export function send(f) { f.request_id=String(frames.length+1); frames.push(f); return f.request_id; }`)
	runPageInEngines(t, page, modules)
}

// HOW {NAME} HAS CHANGED THIS PAGE, the UI Adapters' part (step 16a), over
// the shipped module and a stand-in for the host's script doors. A script
// waiting is listed as that; before it is shown nothing offers to approve
// it; once shown, its bytes are text and what a script can do is said
// before the button that approves it. The way to see the page without
// their scripts is said.
func TestTheAdaptersPartSaysWhatAScriptCanDoBeforeItIsApproved(t *testing.T) {
	page := `<!doctype html>
<div id="settings-stack"></div>
<script type="module">
import { S } from './state.js';
import { scriptsCardHTML, wireScriptsCard } from './scripts.js';
import { assert, run, until } from './__harness.js';
const st = document.getElementById('settings-stack');
const draw = () => { st.innerHTML = scriptsCardHTML(); wireScriptsCard(st, draw); };
run(async () => {
  S.stats = { name: 'Taylor' };
  draw();
  await until(() => st.querySelector('[data-script="custom.js"]'));
  assert(st.querySelector('h3').textContent === 'How Taylor has changed this page', 'the heading: ' + st.querySelector('h3').textContent);
  assert(st.querySelector('[data-script="custom.js"]').textContent.startsWith('awaiting your approval · custom.js'), 'the waiting script: ' + st.querySelector('[data-script="custom.js"]').textContent);
  assert(!st.querySelector('[data-script-approve]') && !st.querySelector('.c-os-note'), 'approval or the warning was drawn before the file was shown');
  assert(/open \/shipped\/; without anything of theirs, \/plain\//.test(st.querySelector('[data-scripts-shipped]').textContent), 'the way to the page without their scripts is not said');
  st.querySelector('[data-script-show="custom.js"]').click();
  await until(() => st.querySelector('[data-script-text]') && st.querySelector('[data-script-text]').textContent === 'document.title = "<b>x</b>";\n');
  const note = st.querySelector('.c-os-note'), go = st.querySelector('[data-script-approve="custom.js"]');
  assert(note && /^AII OS: this is a script\. It can do anything you can do on this page/.test(note.textContent) && go && (note.compareDocumentPosition(go) & Node.DOCUMENT_POSITION_FOLLOWING), 'WHAT A SCRIPT CAN DO IS NOT SAID BEFORE ITS APPROVAL: ' + (note && note.textContent));
});
</script>`
	data, err := staticFS.ReadFile("static/scripts.js")
	if err != nil {
		t.Fatal(err)
	}
	digest := "ab0000000000000000000000000000000000000000000000000000000000cd"
	modules := map[string][]byte{
		"/scripts.js": data,
		"/state.js":   []byte(`export const S = {};`),
		"/util.js":    utilStub(`export const $ = id => document.getElementById(id); export const esc = v => String(v ?? '').replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/"/g,'&quot;');`),
		"/pagekey.js": []byte(`export async function signedFetch(url) {
  if (url === '/overlay/scripts') return new Response(JSON.stringify({ scripts: [{ path: 'custom.js', sha256: '` + digest + `', size: 30, approved: false }], can_approve: true }), { status: 200 });
  if (url.startsWith('/overlay/scripts/file')) return new Response('document.title = "<b>x</b>";\n', { status: 200, headers: { 'X-Script-SHA256': '` + digest + `' } });
  return new Response('no', { status: 404 });
}`),
	}
	runPageInEngines(t, page, modules)
}
