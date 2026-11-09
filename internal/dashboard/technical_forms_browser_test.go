//go:build !windows

package dashboard

import "testing"

// technicalFormsPrelude is Technical with a configuration of each form's own,
// through its real modules over a socket that only records.
const technicalFormsPrelude = `<!doctype html><meta name="viewport" content="width=device-width, initial-scale=1">
<link rel="stylesheet" href="/tokens/tokens.css"><link rel="stylesheet" href="/layout.css"><link rel="stylesheet" href="/components/side-nav/side-nav.css"><link rel="stylesheet" href="/components/button/button.css"><link rel="stylesheet" href="/views/settings/technical/technical.css"><link rel="stylesheet" href="/views/settings/limit-row.css">
<div id="views"></div><div id="settings-stack"></div>
<script type="module">
import { S } from '/state.js';
import { frames } from '/ws.js';
import { renderTechnical } from '/views/settings/technical/technical.js';
import { acceptSettingsConfig } from '/views/settings.js';
import { assert, run, until } from '/__harness.js';

const set = (key, more) => Object.assign({ key, min: 1, max: 86400, default: 600, unit: 'seconds', effective: { value: 600, level: 'default' } }, more);
const config = () => ({ llm: { provider: 'p', model: 'm' }, dashboard: { host: '127.0.0.1', port: 1, tls: false }, plugins: {}, restart_required: [],
  prompt: { max_tokens: 0, recent_turns: 20 }, witness: { url: 'https://witness.test', interval_events: 100 },
  logs: { dir: 'log', max_backups: 9, compress_days: 7, max_bytes_default: 5242880, max_total_bytes_default: 52428800 },
  agency: { roles: [{ name: 'critic', provider: 'cloud', model: 'm1', description: 'Reads a draft.' }], rhythm_seconds: 600 },
  database: { preferred: '', active: 'sqlite', can_export: true, dictionary_bytes: 0 },
  cognition: { morning_brief_at: '', morning_brief_default: '07:00', input_budget: 1000, input_budget_source: 'derived', conditions: [], overrides_block: {}, overrides_digest: 'd1',
    settings: [set('agency.rhythm_seconds'), set('agency.max_tool_rounds', { unit: 'rounds', per_model: 'model', unlimited: true, card: 'agency' }), set('llm.retries', { card: 'substrate' }), set('tools.webfetch_max_bytes', { card: 'tools', zero_is_default: true }), set('agency.max_tool_calls', { unit: 'tool calls', unlimited: true, default_unlimited: true, card: 'agency' }), set('agency.turn_seconds_local', { card: 'agency' }), set('agency.subagent_wall_seconds_local', { card: 'agency' })] }, tools: { webfetch_max_bytes: 0 } });
const panel = async (id, section) => {
  location.hash = '#/settings/technical/' + id;
  renderTechnical();
  await until(() => document.querySelector('[data-technical-form="' + section + '"]'), 5000);
  assert(document.querySelector('[data-technical-form="' + section + '"]'), id + ' did not draw its form');
};
const form = () => document.querySelector('.technical__pane');
// until does not fail by itself: what was waited for is asserted.
const waitFor = async (cond, what) => { await until(cond, 3000).catch(() => {}); assert(cond(), what); };
const type = (el, value) => { el.value = value; el.dispatchEvent(new Event('input', { bubbles: true })); };
const saves = () => frames.filter(f => f.type === 'config_set');
const start = () => {
  S.config = config(); S.providers = []; S.providersLoaded = true; S.connected = true; S.view = 'technical'; S.identityExists = true; S.stats = { name: 'Taylor' };
  // As the page shows a view: a view not shown is inert, and takes no focus.
  const view = document.getElementById('view-technical');
  view.classList.add('on'); view.inert = false; view.removeAttribute('aria-hidden');
};
`

func technicalFormsModules(t *testing.T) map[string][]byte {
	t.Helper()
	css, err := staticFS.ReadFile("static/layout.css")
	if err != nil {
		t.Fatal(err)
	}
	return map[string][]byte{"/layout.css": css,
		"/state.js": []byte(`export const S = { providers: [], config: null, providersLoaded: false };`),
		"/ws.js": []byte(`export const frames = [];
export function send(f) { frames.push(f); return 'req-1'; }
export function query(n, e) { return send(Object.assign({ type: 'query', query: n }, e || {})); }`),
		"/sandbox.js": []byte(`export function sandboxCardHTML() { return ''; } export function wireSandboxCard() {}`)}
}

// TECHNICAL'S FORMS ARE ITS PANELS, in the spec's order, each at its own
// address. A form says what it holds in the annex's words, saves through the one
// door, sends only its own fields, keeps what is being typed through a redraw
// and a move to another panel, says the answer beside the Save and gives focus
// back to it, writes nothing for a frame that changes nothing, and waits for an
// IME's composition. Logs' settings stand above its reader. A number another
// page holds a draft of is not sent by this page's Save.
func TestTechnicalsFormsAreItsPanels(t *testing.T) {
	page := technicalFormsPrelude + `
run(async () => {
  start();
  await panel('verification', 'witness');
  const links = [...document.querySelectorAll('.c-side-nav a')];
  assert(links.map(a => a.textContent).join() === 'What Taylor sees,Verification,Helpers,Reflection,Logs,Storage,Turn and tool limits,Settings in force,Layout', 'the panels: ' + links.map(a => a.textContent).join());
  assert(links.map(a => a.getAttribute('href').split('/').pop()).join() === 'sent,verification,helpers,reflection,logs,storage,limits,inforce,layout', 'the addresses: ' + links.map(a => a.getAttribute('href')).join());
  assert(document.querySelector('.c-side-nav [aria-current="page"]').textContent === 'Verification', 'the open panel is not marked');

  // Verification: how far the receipts reach, from the continuity frame.
  const line = () => form().querySelector('[data-receipt-line]').textContent;
  assert(line() === '', 'no frame yet says nothing: ' + line());
  S.cont = { witnessed_at: '', anchored_seq: 0 }; renderTechnical();
  await waitFor(() => line() === 'No receipt yet. The first comes within the hour.', 'no receipt yet: ' + line());
  S.cont = { witnessed_at: new Date(Date.now() - 7200000).toISOString(), anchored_seq: 1234 }; renderTechnical();
  await waitFor(() => /^Last receipt 2 hours ago, through entry 1,234\.$/.test(line()), 'the last receipt is not said');
  S.cont = { witnessed_at: new Date().toISOString(), anchored_seq: 1234, mode: 'degraded_witness', degraded_since: '2026-10-30T08:00:00Z' }; renderTechnical();
  await waitFor(() => /^The server has not answered since /.test(line()) && /not yet verified\.$/.test(line()), 'a server gone dark is not said');

  // A form comes back whole after a panel that draws in a part of its own, and
  // after one that holds no form: nothing it drew before stands in for it.
  await panel('logs', 'logs');
  await panel('verification', 'witness');
  assert(document.getElementById('cfg-wurl'), 'the form did not come back after Logs');
  location.hash = '#/settings/technical/sent'; renderTechnical();
  await waitFor(() => !document.getElementById('cfg-wurl'), 'the form stayed on another panel');
  await panel('verification', 'witness');
  assert(document.getElementById('cfg-wurl'), 'the form did not come back after a panel with none');

  // A draft stands through a move to another panel and back; the bar at the
  // window's foot owns it, there and here, and takes the answer.
  const bar = () => document.querySelector('[data-technical-bar]');
  const barSaid = () => bar().querySelector('.technical-savebar__said').textContent;
  assert(bar().hidden, 'nothing typed, and the bar stands');
  type(document.getElementById('cfg-wurl'), 'https://other.test');
  assert(!bar().hidden && barSaid() === '1 change', 'an edit does not say it is unsaved: ' + barSaid());
  assert(getComputedStyle(bar()).position === 'fixed' && bar().getBoundingClientRect().bottom >= innerHeight - 1, 'the bar is not pinned to the window: ' + bar().getBoundingClientRect().bottom);
  await panel('helpers', 'agency');
  assert(!document.getElementById('cfg-wurl'), 'the other form is still drawn');
  assert(!bar().hidden && barSaid() === '1 change', 'the bar left with the panel: ' + barSaid());
  await panel('logs', 'logs');
  assert(!bar().hidden && barSaid() === '1 change', 'the bar left with the panel: ' + barSaid());
  await panel('verification', 'witness');
  assert(document.getElementById('cfg-wurl').value === 'https://other.test', 'the move to another panel lost the draft');
  assert(document.querySelector('[data-save="technical"]') && !document.querySelector('[data-save="technical"]').hidden, 'the draft came back with no Save');

  // Discard lays it down, here and in the bar; nothing is sent.
  frames.length = 0;
  document.querySelector('[data-discard]').click();
  await waitFor(() => document.getElementById('cfg-wurl').value === 'https://witness.test', 'Discard kept the draft: ' + document.getElementById('cfg-wurl').value);
  assert(bar().hidden && !saves().length, 'Discard left the bar, or sent something');
  type(document.getElementById('cfg-wurl'), 'https://other.test');

  // Its Save: its own two keys; the answer beside it; focus back on it.
  const save = () => document.querySelector('[data-save="technical"]');
  save().focus();
  save().click();
  assert(saves().length === 1 && JSON.stringify(saves()[0].config) === '{"witness.url":"https://other.test","witness.interval_events":100}', 'the save: ' + JSON.stringify(saves()[0] && saves()[0].config));
  await waitFor(() => save().getAttribute('aria-disabled') === 'true', 'the Save was not closed while it is checked');
  S.config.witness.url = 'https://other.test';
  assert(acceptSettingsConfig('req-1'), 'the save is not claimed');
  renderTechnical();
  await waitFor(() => document.querySelector('[data-said="technical"]'), 'the answer does not stand beside the Save');
  assert(/^Saved/.test(document.querySelector('[data-said="technical"]').textContent), 'the answer: ' + document.querySelector('[data-said="technical"]').textContent);
  assert(document.activeElement === bar().querySelector('.technical-savebar__said'), 'focus did not go to the answer: ' + document.activeElement.tagName);
  assert(save().hidden, 'a saved draft still offers its Save');
  assert(!/change/.test(barSaid()), 'a saved draft is still counted: ' + barSaid());

  // A frame that changes nothing writes nothing.
  const input = document.getElementById('cfg-wint'), first = form().firstElementChild;
  renderTechnical();
  await new Promise(r => setTimeout(r, 50));
  assert(document.getElementById('cfg-wint') === input && form().firstElementChild === first, 'a frame that changed nothing wrote the form again');

  // A field keeps focus through a frame that does change the form.
  document.getElementById('cfg-wint').focus();
  S.config.witness.url = 'https://third.test'; renderTechnical();
  await waitFor(() => document.getElementById('cfg-wurl').value === 'https://third.test', 'the frame was not drawn');
  assert(document.activeElement === document.getElementById('cfg-wint'), 'focus did not stay in the field: ' + document.activeElement.tagName);

  // Nothing is drawn under an IME's composition: one redraw follows its end.
  const wint = document.getElementById('cfg-wint');
  wint.dispatchEvent(new CompositionEvent('compositionstart', { bubbles: true }));
  S.config.witness.interval_events = 250; renderTechnical();
  await new Promise(r => setTimeout(r, 50));
  assert(document.getElementById('cfg-wint') === wint && wint.value === '100', 'the form was drawn under a composition');
  wint.dispatchEvent(new CompositionEvent('compositionend', { bubbles: true }));
  await waitFor(() => document.getElementById('cfg-wint').value === '250', 'the composition\'s end did not draw what came meanwhile');

  // Helpers: the roles, read-only, and nothing to set.
  await panel('helpers', 'agency');
  assert(form().querySelector('[data-role="critic"]').textContent === 'critic · cloud · m1Reads a draft.', 'the role: ' + form().querySelector('[data-role="critic"]').textContent);
  assert(!form().querySelector('input, [data-save]'), 'Helpers offers something to set');

  // Logs: its settings above the reader, each writing in its own part.
  location.hash = '#/settings/technical/logs'; renderTechnical();
  await until(() => document.getElementById('cfg-ldir'), 5000);
  S.logsList = [{ name: 'aii.log', size: 2048, mod_at: '2026-11-02T08:00:00Z' }]; // the answer to entering's read
  renderTechnical();
  await until(() => document.querySelector('[data-logfile="aii.log"]'), 5000);
  const parts = form().querySelectorAll(':scope > .technical__part');
  assert(parts.length === 2 && parts[0].contains(document.getElementById('cfg-ldir')) && parts[1].querySelector('[data-logfile="aii.log"]'), 'the settings do not stand above the reader');
  assert(document.getElementById('cfg-lbytes').placeholder === 'Usually 5', 'a blank roll does not show the usual: ' + document.getElementById('cfg-lbytes').placeholder);
  frames.length = 0;
  type(document.getElementById('cfg-lbackups'), '3');
  document.querySelector('[data-save="technical"]').click();
  assert(saves()[0].config['logs.max_backups'] === 3 && !('logs.max_bytes' in saves()[0].config), 'the save: ' + JSON.stringify(saves()[0].config));
  S.config.logs.max_backups = 3;
  assert(acceptSettingsConfig('req-1'), 'the logs save is not claimed');

  // Limits: only what changed, and never a draft on another page.
  document.getElementById('settings-stack').innerHTML = '<input data-cog-key="llm.retries" value="99">';
  await panel('limits', 'limits');
  // The two limits of a local model: only once one is set up.
  const localRows = () => ['agency.turn_seconds_local', 'agency.subagent_wall_seconds_local'].filter(k => form().querySelector('[data-cog-key="' + k + '"]')).length;
  assert(localRows() === 0, 'with no local entry, its limits are drawn');
  const withProviders = async (list, want, why) => { S.providers = list; S.renderTechnical(); await waitFor(() => localRows() === want, why + ': ' + localRows()); };
  await withProviders([{ name: 'Local server', local: true, status: 'unreachable' }], 0, 'an entry that does not answer is not set up');
  await withProviders([{ name: 'Local server', local: true, status: 'ok' }], 2, 'an entry that answers is set up');
  await withProviders([{ name: 'Elsewhere', status: 'ok' }], 0, 'an entry that is not local is not');
  await withProviders([{ name: 'p', local: true, status: 'unreachable' }], 2, 'the identity runs on it');
  await withProviders([{ name: 'cloud', local: true, status: 'unreachable' }], 2, 'a role runs on it');
  await withProviders([], 0, 'with no local entry, its limits are drawn');
  // A number left unset reads as blank, with its usual value, and is not sent.
  const unset = form().querySelector('[data-cog-key="tools.webfetch_max_bytes"]');
  assert(unset && unset.value === '' && /^Usually /.test(unset.placeholder), 'an unset number does not read as blank: ' + (unset && unset.value));
  assert(document.getElementById('cfg-ptokens').value === '', 'an unset request budget does not read as blank');
  frames.length = 0;
  assert(bar().hidden, 'nothing changed, and the bar offers a Save');
  document.querySelector('[data-save="technical"]').click();
  assert(!saves().length, 'nothing changed, and a save was sent: ' + JSON.stringify(saves()[0] && saves()[0].config));
  type(document.getElementById('cfg-pturns'), '30');
  type(form().querySelector('[data-cog-key="agency.max_tool_rounds"]'), '40');
  document.querySelector('[data-save="technical"]').click();
  const sent = saves()[0].config;
  assert(JSON.stringify(sent) === '{"prompt.recent_turns":30,"agency.max_tool_rounds":40}', 'the save sends what changed and nothing of another page: ' + JSON.stringify(sent));
  assert(form().querySelector('[data-setting], [data-technical-setting="llm.retries"]'), 'the provider\'s limits are not on the panel');
});
</script>`
	runPageInEngines(t, page, technicalFormsModules(t))
}

// REFLECTION CARRIES "RUN THEIR NEXT PASS NOW", through the same door and
// the same answers as Status's: the press asks once, says nothing until the
// daemon answers, says "Asked at {time}", and says why when refused.
func TestReflectionRunsTheNextPassThroughTheDoor(t *testing.T) {
	page := technicalFormsPrelude + `
run(async () => {
  start();
  await panel('reflection', 'cognition');
  const press = () => form().querySelector('[data-pass-now]');
  const said = () => form().querySelector('[data-pass-said]').textContent;
  assert(press() && press().textContent === 'Run their next pass now' && !press().disabled, 'the press is not offered');
  assert(said() === '', 'said before asked: ' + said());
  press().click();
  assert(frames.filter(f => f.type === 'pass_now').length === 1, 'the press did not ask the door once');
  await waitFor(() => press().disabled, 'the press was not closed while it is asked');
  assert(said() === '', 'said before the answer: ' + said());
  S.passNowAnswered({ request_id: 'req-1', pass_now: { moved: false, reason: 'turn_holding' } });
  await waitFor(() => !press().disabled && /Taylor/.test(said()), 'a refusal is not said: ' + said());
  press().click();
  await waitFor(() => press().disabled, 'the second press was not closed');
  S.passNowAnswered({ request_id: 'req-1', pass_now: { moved: true, asked_at: '2026-11-02T12:00:00Z' } });
  await waitFor(() => /^Asked at /.test(said()) && !press().disabled, 'the answer is not said: ' + said());
  press().click();
  await waitFor(() => press().disabled, 'the third press was not closed');
  S.passNowLost();
  await waitFor(() => /^Asked at /.test(said()) && said().split('. ').length > 1 && !press().disabled, 'a lost connection is not said beside what was asked: ' + said());
  const off = document.getElementById('cog-brief-off');
  off.click();
  assert(document.getElementById('cog-brief-at').disabled, 'no brief does not close its hour');
});
</script>`
	runPageInEngines(t, page, technicalFormsModules(t))
}

// AT A PHONE'S WIDTH each form fits, the list of panels is one line across and
// its open entry in view, the form begins on the first screen under it, and a
// press is a thumb's size: an input, a select, a check, a limit's disclosure and
// what stands under it.
func TestTechnicalsFormsFitAPhone(t *testing.T) {
	page := technicalFormsPrelude + `
run(async () => {
  start();
  for (const [id, section] of [['verification', 'witness'], ['helpers', 'agency'], ['reflection', 'cognition'], ['storage', 'storage'], ['limits', 'limits']]) {
    await panel(id, section);
    const sheets = [...document.querySelectorAll('link[rel="stylesheet"]')];
    await Promise.all(sheets.map(l => l.sheet ? 0 : new Promise(r => { l.addEventListener('load', r, { once: true }); l.addEventListener('error', r, { once: true }); })));
    form().querySelectorAll('details').forEach(d => { d.open = true; });
    const root = document.documentElement;
    assert(root.scrollWidth <= root.clientWidth + 1, id + ' moves the page sideways: ' + root.scrollWidth + ' in ' + root.clientWidth);
    const nav = document.querySelector('.technical__nav .c-side-nav');
    assert(getComputedStyle(nav).flexDirection === 'row' && nav.getBoundingClientRect().height < 60, id + ': the list of panels is not one line across');
    const open = nav.querySelector('[aria-current="page"]').getBoundingClientRect(), box = nav.getBoundingClientRect();
    assert(open.left >= box.left - 1 && open.right <= box.right + 1, id + ': the open panel is scrolled out of the list');
    const small = [...form().querySelectorAll('input.technical-form__input, select.technical-form__input, .technical-form__check, input.limit-row__input, .limit-row__fields select, .limit-row__unlimited, .technical-form__more > summary')]
      .filter(el => Math.round(el.getBoundingClientRect().height) < 44);
    assert(!small.length, id + ': too small to press: ' + small.map(e => Math.round(e.getBoundingClientRect().height) + ' ' + (e.id || e.className)).join());
  }
});
</script>`
	runPageInEnginesAtSize(t, page, technicalFormsModules(t), 390, 844)
}

// ONE SIDEBAR: the panels stand under Technical in Settings' own list, which marks
// Technical as where this is; the panel fills what is left, under its own name.
// When Settings says its list again, the panels stay. Helpers is there when there
// are roles to list.
func TestTechnicalsPanelsStandInTheOneSidebar(t *testing.T) {
	runPageInEngines(t, technicalFormsPrelude+`
import * as sideNav from '/components/side-nav/side-nav.js';
run(async () => {
  const stage = document.createElement('div'); stage.className = 'stage'; stage.dataset.view = 'technical';
  const tabs = document.createElement('div'); tabs.id = 'place-tabs';
  const items = [{ id: 'status', label: 'Status', href: '#/settings' }, { id: 'technical', label: 'Technical', href: '#/settings/technical' }, { id: 'after', label: 'After', href: '#/settings/after' }];
  const rail = sideNav.render({ title: 'Settings', items, selected: 'technical' });
  tabs.append(rail); stage.append(tabs); document.body.prepend(stage);
  start();
  await panel('verification', 'witness');
  const nav = rail.querySelector(':scope > .technical__nav');
  assert(nav && nav.querySelector('a'), 'the panels do not stand in the one list');
  assert(rail.querySelector(':scope > [data-item="technical"]').nextElementSibling === nav, 'the panels do not stand under Technical');
  assert(!document.querySelector('.technical > .technical__nav'), 'a second list stands beside the panel');
  assert(!/In Technical/.test(rail.textContent + document.querySelector('.technical').textContent), 'the second list is still named');
  assert(document.querySelector('.technical__title').textContent === 'Verification', 'the panel does not open with its name: ' + document.querySelector('.technical__title').textContent);
  assert(rail.querySelector(':scope > [data-item="technical"]').getAttribute('aria-current') === 'true', 'Technical is not marked as where this is');
  assert(nav.querySelector('[aria-current="page"]').textContent === 'Verification', 'the open panel is not marked');
  sideNav.update(rail, { title: 'Settings', items, selected: 'technical' });
  assert(rail.querySelector(':scope > .technical__nav a[href$="/verification"]'), 'Settings saying its list again took the panels away');
  const labels = () => [...nav.querySelectorAll('a')].map(a => a.textContent);
  assert(labels().includes('Helpers'), 'roles, and no Helpers: ' + labels());
  S.config.agency.roles = [];
  renderTechnical();
  assert(!labels().includes('Helpers'), 'Helpers stands with no roles to list: ' + labels());
  location.hash = '#/settings/technical/helpers'; renderTechnical();
  assert(document.querySelector('.technical__title').textContent === labels()[0], 'an address with no panel opens the first: ' + document.querySelector('.technical__title').textContent);
});
</script>`, technicalFormsModules(t))
}

// A NUMBER READS AS A PERSON READS IT: on its label's line, sized to its value, in
// the unit a person uses, with no bounds until one is crossed. The field holds the
// reading; what is sent is what the host holds. No magic values: a box says it.
func TestTechnicalsNumbersReadAsAPersonReadsThem(t *testing.T) {
	runPageInEngines(t, technicalFormsPrelude+`
run(async () => {
  start();
  await panel('reflection', 'cognition');
  const f = () => form().querySelector('[data-cog-key="agency.rhythm_seconds"]');
  const row = f().closest('.technical-form__field');
  assert(f().value === '10' && row.querySelector('.technical-form__unit')?.textContent === 'minutes', 'a value minutes long: ' + f().value + ' ' + row.textContent);
  assert(f().getBoundingClientRect().width < 200, 'the box is not sized to its value: ' + f().getBoundingClientRect().width);
  assert(Math.abs(f().getBoundingClientRect().top - row.querySelector('label').getBoundingClientRect().top) < 24, 'the box is not on its label\'s line');
  const said = () => row.querySelector('[data-range-said]');
  assert(said().hidden && !/1,440/.test(row.textContent), 'its bounds are said before they are needed');

  // Outside its bounds: said at the field, and the Save stops there.
  frames.length = 0;
  type(f(), '99999');
  assert(!said().hidden && said().textContent === 'Enter a number from 0.017 to 1,440.', 'the bounds are not said at the field: ' + said().textContent);
  assert(f().getAttribute('aria-invalid') === 'true', 'the field is not marked');
  document.querySelector('[data-save="technical"]').click();
  assert(!saves().length && document.activeElement === f(), 'a number outside its bounds was sent, or the field was not gone to');
  type(f(), '20');
  assert(said().hidden && !f().hasAttribute('aria-invalid'), 'the bounds are still said');
  document.querySelector('[data-save="technical"]').click();
  assert(saves()[0].config['agency.rhythm_seconds'] === 1200, 'what is sent is not what the host holds: ' + JSON.stringify(saves()[0] && saves()[0].config));
  S.config.agency.rhythm_seconds = 1200;
  assert(acceptSettingsConfig('req-1'), 'the save is not claimed');
  renderTechnical();

  // Edits on two panels are one Save, and only what changed travels.
  await panel('limits', 'limits');
  type(form().querySelector('[data-cog-key="agency.max_tool_rounds"]'), '44');
  await panel('verification', 'witness');
  type(document.getElementById('cfg-wint'), '55');
  assert(document.querySelector('.technical-savebar__said').textContent === '2 changes', 'edits on two panels are not counted: ' + document.querySelector('.technical-savebar__said').textContent);
  frames.length = 0;
  document.querySelector('[data-save="technical"]').click();
  const both = saves()[0].config;
  assert(both['agency.max_tool_rounds'] === 44 && both['witness.interval_events'] === 55, 'one Save does not carry both panels: ' + JSON.stringify(both));
  assert(!('prompt.recent_turns' in both), 'what did not change travelled: ' + JSON.stringify(both));
  assert(!('agency.max_tool_calls' in both), 'a box ticked as drawn travelled: ' + JSON.stringify(both));
  S.config.agency.max_tool_rounds = 44; S.config.witness.interval_events = 55;
  assert(acceptSettingsConfig('req-1'), 'the save is not claimed');
  renderTechnical();

  // Unlimited by default: the box starts ticked and the number box empty, and
  // nothing is sent until that changes.
  await panel('limits', 'limits');
  const calls = () => form().querySelector('[data-cog-key="agency.max_tool_calls"]'), lift = () => form().querySelector('[data-cog-unl="agency.max_tool_calls"]');
  assert(lift().checked && calls().disabled && calls().value === '' && calls().placeholder === '', 'unlimited does not read as unlimited: ' + calls().value + ' ' + calls().placeholder);
  assert(document.querySelector('[data-technical-bar]').hidden, 'a ticked box as drawn counts as a change');
  lift().click();
  assert(!calls().disabled && calls().placeholder === 'Usually unlimited', 'unticking does not give the number box back: ' + calls().placeholder);
  type(calls(), '500');
  frames.length = 0;
  document.querySelector('[data-save="technical"]').click();
  assert(saves()[0].config['agency.max_tool_calls'] === 500, 'a number in place of unlimited is not sent: ' + JSON.stringify(saves()[0] && saves()[0].config));
  S.config.agency.max_tool_calls = 500;
  assert(acceptSettingsConfig('req-1'), 'the save is not claimed');
  renderTechnical();
  assert(calls().value === '500' && !lift().checked, 'a number reads back as unlimited');
  S.config.agency.max_tool_calls = null;
  renderTechnical();

  // A draft that cannot be sent: nothing older goes in its place, and the newer
  // draft is not taken for saved.
  await panel('limits', 'limits');
  const rounds = () => form().querySelector('[data-cog-key="agency.max_tool_rounds"]');
  const barSays = () => document.querySelector('.technical-savebar__said').textContent;
  frames.length = 0;
  type(rounds(), '45');
  type(rounds(), '45.5');
  assert(rounds().getAttribute('aria-invalid') === 'true', 'a fraction is not marked');
  assert(rounds().closest('.technical-form__field').querySelector('[data-range-said]').textContent === 'Rounds in a turn is a whole number, or blank for the default.', 'a fraction is not said at the field: ' + rounds().closest('.technical-form__field').querySelector('[data-range-said]').textContent);
  document.querySelector('[data-save="technical"]').click();
  assert(!saves().length, 'the older value was sent for a fraction: ' + JSON.stringify(saves()[0] && saves()[0].config));
  assert(document.activeElement === rounds() && rounds().value === '45.5', 'the Save did not stop at the fraction, or lost it');
  type(rounds(), '46');
  document.querySelector('[data-save="technical"]').click();
  assert(saves()[0]?.config['agency.max_tool_rounds'] === 46, 'the corrected value was not sent: ' + JSON.stringify(saves()[0] && saves()[0].config));
  S.config.agency.max_tool_rounds = 46;
  assert(acceptSettingsConfig('req-1'), 'the save is not claimed');
  renderTechnical();

  // One on another panel: the Save goes there, says why, and sends nothing until
  // it is right; then both go.
  frames.length = 0;
  type(rounds(), '99999');
  await panel('verification', 'witness');
  type(document.getElementById('cfg-wint'), '77');
  assert(barSays() === '2 changes', 'the two are not counted: ' + barSays());
  document.querySelector('[data-save="technical"]').click();
  assert(!saves().length, 'a draft out of bounds on another panel went with the Save: ' + JSON.stringify(saves()[0] && saves()[0].config));
  await waitFor(() => rounds() && rounds().value === '99999', 'the Save did not go to the panel of the draft');
  assert(document.activeElement === rounds(), 'the field was not gone to: ' + document.activeElement.tagName);
  assert(!rounds().closest('.technical-form__field').querySelector('[data-range-said]').hidden, 'its bounds are not said there');
  type(rounds(), '47');
  document.querySelector('[data-save="technical"]').click();
  const two = saves()[0]?.config || {};
  assert(two['agency.max_tool_rounds'] === 47 && two['witness.interval_events'] === 77, 'the two did not go together: ' + JSON.stringify(two));
  S.config.agency.max_tool_rounds = 47; S.config.witness.interval_events = 77;
  assert(acceptSettingsConfig('req-1'), 'the save is not claimed');
  renderTechnical();

  // Typed while a save is on its way: the answer retires only what was sent.
  await panel('limits', 'limits');
  frames.length = 0;
  type(rounds(), '48');
  document.querySelector('[data-save="technical"]').click();
  assert(saves()[0]?.config['agency.max_tool_rounds'] === 48, 'the first value was not sent');
  type(rounds(), '49');
  S.config.agency.max_tool_rounds = 48;
  assert(acceptSettingsConfig('req-1'), 'the save is not claimed');
  renderTechnical();
  assert(rounds().value === '49' && barSays().includes('Newer edits remain unsaved'), 'a newer draft was taken for saved: ' + rounds().value + ' ' + barSays());
  assert(!document.querySelector('[data-save="technical"]').hidden, 'a newer draft has no Save');
  frames.length = 0;
  document.querySelector('[data-save="technical"]').click();
  assert(saves()[0]?.config['agency.max_tool_rounds'] === 49, 'the newer draft cannot be saved: ' + JSON.stringify(saves()[0] && saves()[0].config));
  S.config.agency.max_tool_rounds = 49;
  assert(acceptSettingsConfig('req-1'), 'the save is not claimed');
  renderTechnical();

  // None: a box, not -1.
  await panel('limits', 'limits');
  const retries = () => form().querySelector('[data-cog-key="llm.retries"]'), none = () => form().querySelector('[data-cog-none="llm.retries"]');
  assert(none() && !none().checked, 'retries has no None');
  frames.length = 0;
  none().click();
  assert(retries().disabled, 'None leaves a number to type');
  document.querySelector('[data-save="technical"]').click();
  assert(saves()[0]?.config['llm.retries'] === -1, 'None is not sent as none: ' + JSON.stringify(saves()[0] && saves()[0].config));
  S.config.llm.retries = -1;
  assert(acceptSettingsConfig('req-1'), 'the save is not claimed');
  renderTechnical();
  assert(retries().value === '' && none().checked && retries().disabled, 'none reads back as -1: ' + retries().value);

  // A press that draws the form again keeps the keyboard where it was.
  const adding = () => form().querySelector('[data-lim-add="agency.max_tool_rounds"]');
  assert(adding(), 'the per-model rows do not stand under More');
  adding().closest('[data-lim-more]').open = true;
  await new Promise(r => setTimeout(r, 30));
  document.getElementById('lim-add-p-agency-max-tool-rounds').value = 'p';
  document.getElementById('lim-add-v-agency-max-tool-rounds').value = '7';
  S.providers = [{ name: 'p', chat: true }];
  renderTechnical();
  document.getElementById('lim-add-p-agency-max-tool-rounds').value = 'p';
  document.getElementById('lim-add-v-agency-max-tool-rounds').value = '7';
  adding().focus();
  adding().click();
  await waitFor(() => form().querySelector('[data-lim-override="agency.max_tool_rounds"]'), 'the override was not added');
  assert(document.activeElement === adding(), 'the press did not keep focus: ' + document.activeElement.tagName);
  document.querySelector('[data-discard]').click();
  S.providers = [];
  await waitFor(() => !form().querySelector('[data-lim-override="agency.max_tool_rounds"]'), 'Discard kept the override');

  // Per provider or model: once, under More at the end of the group; in force is
  // said only when it differs from what the box shows.
  assert(!form().querySelector('[data-lim-effective]'), 'in force is said with nothing overriding it');
  const add = form().querySelector('[data-lim-add="agency.max_tool_rounds"]');
  assert(add && add.closest('.technical-form__group > [data-lim-more]') && !add.closest('[data-technical-setting]'), 'the per-model rows do not stand under More');
  S.config.cognition.settings.find(s => s.key === 'agency.max_tool_rounds').effective = { value: 200, level: 'model', provider: 'Local server', model: 'big' };
  renderTechnical();
  assert(form().querySelector('[data-lim-effective="agency.max_tool_rounds"]')?.textContent === 'In force: 200 for Local server · big.', 'an override is not said');

  // Logs: Keep all and Never, not -1; sizes in MB.
  await panel('logs', 'logs');
  const lf = id => document.getElementById(id);
  assert(lf('cfg-lbackups').value === '9' && !lf('cfg-lbackups-all').checked, 'old logs kept');
  frames.length = 0;
  lf('cfg-lbackups-all').click(); lf('cfg-lcomp-never').click();
  assert(lf('cfg-lbackups').disabled && lf('cfg-lcomp').disabled, 'a box leaves a number to type');
  document.querySelector('[data-save="technical"]').click();
  const sent = saves()[0].config;
  assert(sent['logs.max_backups'] === -1 && sent['logs.compress_days'] === -1, 'the boxes are not sent as -1: ' + JSON.stringify(sent));
  S.config.logs.max_backups = -1; S.config.logs.compress_days = -1;
  assert(acceptSettingsConfig('req-1'), 'the save is not claimed');
  renderTechnical();
  assert(lf('cfg-lbackups').value === '' && lf('cfg-lbackups-all').checked && lf('cfg-lcomp').value === '' && lf('cfg-lcomp-never').checked, '-1 reads back as a number');

  // Storage: the format and the copy are the panel; the rest is under Advanced.
  await panel('storage', 'storage');
  const advanced = form().querySelector('[data-lim-more="storage"]');
  assert(advanced && !advanced.open && advanced.querySelector('summary').textContent === 'Advanced', 'no Advanced');
  assert(advanced.querySelector('#cfg-db-levels') && advanced.querySelector('#cfg-db-dictionary') && advanced.querySelector('#db-dictionary-bytes'), 'compression stands outside Advanced');
  assert(!advanced.querySelector('#cfg-db-format') && !advanced.querySelector('[data-export-db]'), 'the panel\'s own stand under Advanced');

  // With no server set, no receipt is promised.
  S.config.witness.url = '';
  S.cont = { mode: 'live' };
  await panel('verification', 'witness');
  assert(form().querySelector('[data-receipt-line]').textContent === 'No verification server is set, so no receipts are made.', 'a receipt is promised with no server: ' + form().querySelector('[data-receipt-line]').textContent);
});
</script>`, technicalFormsModules(t))
}
