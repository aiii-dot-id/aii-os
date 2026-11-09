//go:build !windows

package dashboard

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

const settingsInForcePage = technicalFormsPrelude + `
const rowOf = key => document.querySelector('[data-force-row="' + key + '"]');
const shown = () => [...document.querySelectorAll('[data-force-row]')].map(r => r.dataset.forceRow);
const typeIn = value => { const f = document.querySelector('[data-force-search]'); f.value = value; f.dispatchEvent(new Event('input', { bubbles: true })); };
run(async () => {
  start();
  const settingsHeld = S.config.cognition.settings;
  S.config.cognition.settings = [];
  window.__copied = [];
  navigator.clipboard && (navigator.clipboard.writeText = async text => { window.__copied.push(text); });
  location.hash = '#/settings/technical/inforce';
  renderTechnical();
  await waitFor(() => rowOf('prompt.alarm_firings'), 'the rows are not drawn');
  // The table's units may come after the rows, and the rows then say them.
  assert(rowOf('agency.max_tool_rounds').querySelector('.technical-force__value').textContent === '44 · default 30', 'with no table yet: ' + rowOf('agency.max_tool_rounds').querySelector('.technical-force__value').textContent);
  S.config.cognition.settings = settingsHeld;
  renderTechnical();
  await waitFor(() => /^44 rounds/.test(rowOf('agency.max_tool_rounds').querySelector('.technical-force__value').textContent), 'the table came and the rows did not say its units: ' + rowOf('agency.max_tool_rounds').querySelector('.technical-force__value').textContent);
  assert(rowOf('logs.max_bytes').querySelector('.technical-force__value').textContent === '5 MB', 'a key that names its unit: ' + rowOf('logs.max_bytes').querySelector('.technical-force__value').textContent);
  const form = document.querySelector('.technical__pane');
  assert(/^Every setting, its current value and who set it\./.test(form.querySelector('.technical-form__heading').textContent), 'the heading');
  assert(document.querySelector('.c-side-nav [aria-current="page"]').textContent === 'Settings in force', 'the open panel is not marked');
  assert(shown().length === 14, 'every row: ' + shown().join());

  // A row: its words where it has them, its key, what is in force, its default
  // where that differs, and who set it.
  const row = rowOf('prompt.alarm_firings');
  assert(row.querySelector('.technical-force__title').textContent === 'Alarm firings listed', 'the name: ' + row.textContent);
  assert(row.querySelector('code').textContent === 'prompt.alarm_firings', 'the key');
  assert(rowOf('agency.rhythm_seconds').querySelector('.technical-force__value').textContent === '20 minutes · default 10 minutes', 'a number the table holds does not read as on its panel: ' + rowOf('agency.rhythm_seconds').querySelector('.technical-force__value').textContent);
  assert(row.querySelector('code wbr'), 'a key breaks inside a word');
  assert(row.querySelector('.technical-force__value').textContent === '50 · default 20', 'the value: ' + row.querySelector('.technical-force__value').textContent);
  assert(row.querySelector('.technical-force__by').textContent === 'config.json', 'who set it: ' + row.querySelector('.technical-force__by').textContent);
  const plain = rowOf('agency.queue_workers');
  assert(!plain.querySelector('.technical-force__title'), 'a key with no words is given some');
  assert(plain.querySelector('.technical-force__value').textContent === '3' && plain.querySelector('.technical-force__by').textContent === 'Default', 'a default row: ' + plain.textContent);
  assert(rowOf('agency.harvest_wake').querySelector('.technical-force__value').textContent === '', 'a value it cannot show is not left empty, with Default beside it');

  // A secret says it is set, and nothing of it.
  assert(rowOf('dashboard.access_token').querySelector('.technical-force__value').textContent === 'Set in config.json. The value is not shown.', 'the hidden row');
  // A secret the file does not hold is not said to be set; a value stated as its
  // default is the file's, is not changed, and has nothing to go back to.
  const absent = rowOf('dashboard.auth_token_sha256');
  assert(absent.querySelector('.technical-force__value').textContent === 'Not set in config.json.' && absent.querySelector('.technical-force__by').textContent === '', 'a hidden key the file does not hold: ' + absent.textContent);
  // An empty list is the same as none, and no default to name.
  assert(rowOf('contacts').querySelector('.technical-force__value').textContent === '[]' && rowOf('contacts').querySelector('.technical-force__by').textContent === 'config.json', 'an empty list stated: ' + rowOf('contacts').textContent);
  assert(rowOf('dashboard.approved_scripts').querySelector('.technical-force__value').textContent === '', 'an empty list named as a default: ' + rowOf('dashboard.approved_scripts').textContent);
  const asDefault = rowOf('agency.subagent_max_legs');
  assert(asDefault.querySelector('.technical-force__by').textContent === 'config.json' && !asDefault.querySelector('[data-force-back]'), 'stated at its default: ' + asDefault.textContent);

  // Search by key and by name, and the two filters; both counted.
  const chips = [...form.querySelectorAll('[data-force-filter]')].map(b => b.textContent);
  assert(chips.join() === 'All 14,Changed from default 5', 'the filters: ' + chips.join());
  assert(document.querySelector('[data-force-search]').placeholder === 'Search 14 settings', 'the search says its count');
  typeIn('ALARM_FIRINGS');
  assert(shown().join() === 'prompt.alarm_firings', 'by key: ' + shown().join());
  typeIn('firings listed');
  assert(shown().join() === 'prompt.alarm_firings', 'by name: ' + shown().join());
  const input = document.querySelector('[data-force-search]');
  typeIn('nothing like this');
  assert(!shown().length && !form.querySelector('[data-force-none]').hidden, 'no match is not said');
  assert(document.querySelector('[data-force-search]') === input, 'searching drew the field again');
  typeIn('');
  assert(form.querySelector('[data-force-none]').hidden, 'a match still says none');
  form.querySelector('[data-force-filter="changed"]').click();
  assert(shown().join() === 'agency.max_tool_rounds,agency.rhythm_seconds,llm.timeout_seconds,prompt.alarm_firings,witness_server.url', 'changed from default: ' + shown().join());
  assert(form.querySelector('[data-force-filter="changed"]').getAttribute('aria-pressed') === 'true', 'the filter on is not marked');
  form.querySelector('[data-force-filter="all"]').click();

  // More: the link, the key, where it is set, and back to the default.
  const more = rowOf('prompt.alarm_firings').querySelector('[data-force-more]');
  assert(more.querySelector('summary').getAttribute('aria-label') === 'More for Alarm firings listed', 'the press has no name');
  more.open = true;
  await new Promise(r => setTimeout(r, 30));
  const place = rowOf('prompt.alarm_firings').querySelector('a');
  assert(place.textContent === 'Open in Reflection' && place.getAttribute('href') === '#/settings/technical/reflection', 'where it is set: ' + place.textContent + ' ' + place.getAttribute('href'));
  const back = () => rowOf('prompt.alarm_firings').querySelector('[data-force-back]');
  assert(back().textContent === 'Set back to 20', 'back to the default: ' + back().textContent);
  assert(!rowOf('agency.max_tool_calls').querySelector('[data-force-back]'), 'a key not stated offers a way back');
  assert(!rowOf('dashboard.access_token').querySelector('[data-force-back]'), 'a secret offers a way back');
  assert(!rowOf('witness_server.url').querySelector('[data-force-back]'), 'a key the table does not hold offers a way back');
  assert(!rowOf('llm.timeout_seconds').querySelector('[data-force-back]'), 'a key whose own field holds a number offers a way back');
  assert(rowOf('agency.queue_workers').querySelector('a') === null, 'a key with no place names one');
  frames.length = 0;
  back().click();
  assert(JSON.stringify(saves()[0].config) === '{"prompt.alarm_firings":null}', 'set back sends the key blank: ' + JSON.stringify(saves()[0].config));
  await waitFor(() => /Checking/.test(form.textContent), 'the set back is not said beside its row');
  assert(acceptSettingsConfig('req-1'), 'the set back is not claimed');
  S.renderTechnical();
  await waitFor(() => window.__reads === 2, 'the rows are not read again after a set back: ' + window.__reads);
  await waitFor(() => /^20/.test(rowOf('prompt.alarm_firings').querySelector('.technical-force__value').textContent), 'the row does not show the default again');

  // A row's own address brings it into view and marks it.
  location.hash = '#/settings/technical/verification'; renderTechnical();
  await panelOpen('witness');
  location.hash = '#/settings/technical/inforce/agency.queue_workers'; renderTechnical();
  await waitFor(() => rowOf('agency.queue_workers') && rowOf('agency.queue_workers').hasAttribute('data-force-here'), 'a row\'s address does not mark it');
  assert(document.querySelector('.c-side-nav [aria-current="page"]').textContent === 'Settings in force', 'a row\'s address is not the panel');

  // At the board's foot, the way out of a broken page.
  assert(form.querySelector('a[href="/shipped/"]') && form.querySelector('a[href="/plain/"]') && /without Taylor's scripts/.test(form.textContent), 'the way out is not said');
});
const panelOpen = section => waitFor(() => document.querySelector('[data-technical-form="' + section + '"]'), section + ' did not open');
</script>`

const settingsInForceRows = `{"settings":[
{"key":"agency.harvest_wake","stated":false},
{"key":"agency.max_tool_calls","value":"5","usual":"5"},
{"key":"agency.max_tool_rounds","value":"44","usual":"30","stated":true},
{"key":"agency.queue_workers","value":"3","usual":"3"},
{"key":"agency.rhythm_seconds","value":"1200","usual":"600","stated":true},
{"key":"dashboard.access_token","hidden":true,"stated":true},
{"key":"llm.timeout_seconds","value":"120","usual":"60","stated":true},
{"key":"logs.max_bytes","value":"5242880","usual":"5242880"},
{"key":"prompt.alarm_firings","value":"50","usual":"20","stated":true},
{"key":"witness_server.url","value":"https://w.test","usual":"https://other.test","stated":true},
{"key":"dashboard.auth_token_sha256","hidden":true},
{"key":"agency.subagent_max_legs","value":"4","usual":"4","stated":true},
{"key":"contacts","value":"[]","usual":"[]","stated":true},
{"key":"dashboard.approved_scripts","usual":"[]"}]}`

// SETTINGS IN FORCE, AS THE BOARD DRAWS IT: every key, with what is in force and
// who set it; search by name or key; All and Changed from default; under More its
// link, its key, the place that sets it and a way back to the default where the
// door takes one. A secret says only that it is set. A row has its own address.
func TestSettingsInForceAsTheBoardDrawsIt(t *testing.T) {
	modules := technicalFormsModules(t)
	modules["/settings-in-force"] = []byte(settingsInForceRows)
	page := strings.Replace(settingsInForcePage, "const rowOf =", "window.__reads = 0; const realFetch = fetch; window.fetch = (u, o) => { if (String(u).includes('settings-in-force')) { window.__reads++; if (window.__reads > 1) return Promise.resolve(new Response(JSON.stringify({ settings: [{ key: 'prompt.alarm_firings', value: '20', usual: '20' }, { key: 'agency.queue_workers', value: '3', usual: '3' }, { key: 'agency.harvest_wake' }, { key: 'dashboard.access_token', hidden: true, stated: true }, { key: 'witness_server.url', value: 'x', usual: 'x' }] }))); } return realFetch(u, o); };\nconst rowOf =", 1)
	runPageInEngines(t, page, modules)
}

// IT FITS A PHONE: nothing moves the page sideways, each row is a block, and its
// More is a thumb's size.
func TestSettingsInForceFitsAPhone(t *testing.T) {
	modules := technicalFormsModules(t)
	modules["/settings-in-force"] = []byte(settingsInForceRows)
	runPageInEnginesAtSize(t, technicalFormsPrelude+`
run(async () => {
  start();
  location.hash = '#/settings/technical/inforce';
  renderTechnical();
  await waitFor(() => document.querySelector('[data-force-row]'), 'the rows are not drawn');
  const sheets = [...document.querySelectorAll('link[rel="stylesheet"]')];
  await Promise.all(sheets.map(l => l.sheet ? 0 : new Promise(r => { l.addEventListener('load', r, { once: true }); l.addEventListener('error', r, { once: true }); })));
  const root = document.documentElement;
  assert(root.scrollWidth <= root.clientWidth + 1, 'it moves the page sideways: ' + root.scrollWidth + ' in ' + root.clientWidth);
  const row = document.querySelector('[data-force-row]');
  assert(getComputedStyle(row).display === 'block', 'a row is not a block on a phone');
  const small = [...document.querySelectorAll('.technical-force__more summary, [data-force-filter], [data-force-search]')].filter(el => el.getBoundingClientRect().height < 43.5);
  assert(!small.length, 'too small to press: ' + small.map(e => e.getBoundingClientRect().height.toFixed(3) + ' ' + e.tagName + ' ' + e.className));
  const name = row.querySelector('.technical-force__names');
  assert(getComputedStyle(name).flexDirection === 'column', 'a name and its key run together');
});
</script>`, modules, 390, 844)
}

// AN OPEN MORE STANDS OVER THE ROWS below it, at both widths: its words are what is
// there to press, its press stays where it was, and it stays inside the window.
func TestAnOpenMoreStandsOverTheRows(t *testing.T) {
	modules := technicalFormsModules(t)
	modules["/settings-in-force"] = []byte(settingsInForceRows)
	for _, width := range []int{1280, 390} {
		runPageInEnginesAtSize(t, technicalFormsPrelude+`
run(async () => {
  start();
  location.hash = '#/settings/technical/inforce';
  renderTechnical();
  await waitFor(() => document.querySelector('[data-force-row]'), 'the rows are not drawn');
  const sheets = [...document.querySelectorAll('link[rel="stylesheet"]')];
  await Promise.all(sheets.map(l => l.sheet ? 0 : new Promise(r => { l.addEventListener('load', r, { once: true }); l.addEventListener('error', r, { once: true }); })));
  const row = document.querySelectorAll('[data-force-row]')[1];
  row.scrollIntoView({ block: 'start' });
  const more = row.querySelector('[data-force-more]'), press = more.querySelector('summary');
  // Until the window has finished scrolling there.
  let before = press.getBoundingClientRect(), settled = 0;
  while (settled < 3) {
    await new Promise(r => setTimeout(r, 50));
    const now = press.getBoundingClientRect();
    settled = now.y === before.y ? settled + 1 : 0;
    before = now;
  }
  const nextHeight = row.nextElementSibling.getBoundingClientRect().height;
  more.open = true;
  await new Promise(r => setTimeout(r, 30));
  const after = press.getBoundingClientRect();
  assert(Math.abs(after.x - before.x) < 1 && Math.abs(after.y - before.y) < 1, 'the press moved: ' + before.x + ',' + before.y + ' to ' + after.x + ',' + after.y);
  assert(row.nextElementSibling.getBoundingClientRect().height === nextHeight, 'the row below changed');
  const menu = more.querySelector('.technical-force__menu');
  const box = menu.getBoundingClientRect();
  assert(box.left >= 0 && box.right <= innerWidth, 'it leaves the window: ' + box.left + ' to ' + box.right + ' in ' + innerWidth);
  for (const item of menu.querySelectorAll('.c-button')) {
    const r = item.getBoundingClientRect();
    const hit = document.elementFromPoint(r.x + r.width / 2, r.y + r.height / 2);
    assert(hit && item.contains(hit), 'under ' + item.textContent + ' is ' + (hit && (hit.tagName + ' ' + hit.className + ' ' + hit.textContent.slice(0, 40))));
    assert(innerWidth > 500 || r.height >= 43.5, 'too small to press: ' + item.textContent + ' ' + r.height);
  }
});
</script>`, modules, width, 844)
	}
}

// A READ FROM BEFORE A SET BACK IS NOT ITS ANSWER: a read on its way when the save
// is answered is not let stand for the read owed after it, whichever comes back last.
func TestAnOlderReadIsNotTheAnswerToASetBack(t *testing.T) {
	modules := technicalFormsModules(t)
	runPageInEngines(t, technicalFormsPrelude+`
import { readInForce } from '/views/settings/technical/in-force.js';
const rowOf = key => document.querySelector('[data-force-row="' + key + '"]');
const before = { settings: [{ key: 'prompt.alarm_firings', value: '50', usual: '20', stated: true }] };
const after = { settings: [{ key: 'prompt.alarm_firings', value: '20', usual: '20' }] };
const reads = [];
const realFetch = fetch;
window.fetch = (u, o) => (String(u).includes('settings-in-force')
  ? new Promise(resolve => reads.push(rows => resolve(new Response(JSON.stringify(rows), { status: 200, headers: { 'Content-Type': 'application/json' } }))))
  : realFetch(u, o));
run(async () => {
  start();
  location.hash = '#/settings/technical/inforce';
  renderTechnical();
  await waitFor(() => reads.length === 1, 'the panel did not read');
  reads[0](before);
  await waitFor(() => rowOf('prompt.alarm_firings'), 'the rows are not drawn');
  // A new visit's read is on its way, and a row's Set back is pressed and answered.
  readInForce();
  assert(reads.length === 2, 'the second read: ' + reads.length);
  const row = rowOf('prompt.alarm_firings');
  row.querySelector('[data-force-more]').open = true;
  await new Promise(r => setTimeout(r, 30));
  row.querySelector('[data-force-back]').click();
  assert(saves().length === 1, 'the press sent no save');
  assert(acceptSettingsConfig('req-1'), 'the save is claimed');
  renderTechnical();
  await waitFor(() => reads.length === 3, 'the read owed after the save was not asked for');
  reads[2](after);
  await waitFor(() => rowOf('prompt.alarm_firings').querySelector('.technical-force__by').textContent === 'Default', 'the owed read did not draw');
  reads[1](before);
  // waits by design: nothing should change, so there is no condition to wait for
  await new Promise(r => setTimeout(r, 80));
  renderTechnical();
  await new Promise(r => setTimeout(r, 30));
  assert(rowOf('prompt.alarm_firings').querySelector('.technical-force__by').textContent === 'Default', 'the older read replaced it: ' + rowOf('prompt.alarm_firings').textContent);
});
</script>`, modules)
}

// A ROW'S ADDRESS BRINGS THAT ROW, on a panel already drawn with the same rows, and
// past a search or filter that had hidden it.
func TestARowsAddressBringsItOnAPanelAlreadyDrawn(t *testing.T) {
	modules := technicalFormsModules(t)
	modules["/settings-in-force"] = []byte(settingsInForceRows)
	runPageInEngines(t, technicalFormsPrelude+`
const marked = () => [...document.querySelectorAll('[data-force-here]')].map(r => r.dataset.forceRow).join();
const scrolled = [];
const realScroll = Element.prototype.scrollIntoView;
Element.prototype.scrollIntoView = function (o) { if (this.dataset && this.dataset.forceRow) scrolled.push(this.dataset.forceRow); return realScroll.call(this, o); };
const go = key => { location.hash = '#/settings/technical/inforce/' + key; renderTechnical(); };
run(async () => {
  start();
  go('prompt.alarm_firings');
  await waitFor(() => marked() === 'prompt.alarm_firings', 'the first address: ' + marked());
  go('agency.rhythm_seconds');
  await waitFor(() => marked() === 'agency.rhythm_seconds', 'a second address on the same rows: ' + marked());
  assert(scrolled[scrolled.length - 1] === 'agency.rhythm_seconds', 'it was not brought into view: ' + scrolled.join());
  const search = document.querySelector('[data-force-search]');
  search.value = 'firings';
  search.dispatchEvent(new Event('input', { bubbles: true }));
  assert(!document.querySelector('[data-force-row="agency.queue_workers"]'), 'the search hides the row');
  go('agency.queue_workers');
  await waitFor(() => marked() === 'agency.queue_workers', 'past a search: ' + marked());
  assert(search.value === '' && document.querySelector('[data-force-search]') === search, 'the search still stands: ' + search.value);
  document.querySelector('[data-force-filter="changed"]').click();
  assert(!document.querySelector('[data-force-row="logs.max_bytes"]'), 'the filter hides the row');
  go('logs.max_bytes');
  await waitFor(() => marked() === 'logs.max_bytes', 'past a filter: ' + marked());
  assert(document.querySelector('[data-force-filter="all"]').getAttribute('aria-pressed') === 'true', 'the filter still stands');
  assert(scrolled[scrolled.length - 1] === 'logs.max_bytes', 'past a filter it was not brought into view: ' + scrolled.join());
});
</script>`, modules)
}

// A FILE THAT COULD NOT BE READ IS SAID: the values in force still stand, nothing is
// said of who set them, a hidden value is only not shown, and nothing offers to go back.
func TestAnUnreadFileIsSaid(t *testing.T) {
	modules := technicalFormsModules(t)
	modules["/settings-in-force"] = []byte(`{"file_unread":"it broke","settings":[
{"key":"prompt.alarm_firings","value":"50","usual":"20"},
{"key":"agency.queue_workers","value":"3","usual":"3"},
{"key":"agency.harvest_wake"},
{"key":"contacts","value":"[]","usual":"[]"},
{"key":"dashboard.approved_scripts","usual":"[]"},
{"key":"dashboard.access_token","hidden":true}]}`)
	page := technicalFormsPrelude + `
const rowOf = key => document.querySelector('[data-force-row="' + key + '"]');
run(async () => {
  start();
  location.hash = '#/settings/technical/inforce';
  renderTechnical();
  await waitFor(() => rowOf('prompt.alarm_firings'), 'the rows are not drawn');
  const said = document.querySelector('[data-force-unread]');
  assert(!said.hidden && said.textContent === 'config.json could not be read, so who set each value is not shown: it broke', 'the file: ' + said.textContent);
  assert(rowOf('prompt.alarm_firings').querySelector('.technical-force__by').textContent === '', 'it says who set it');
  assert(rowOf('prompt.alarm_firings').querySelector('.technical-force__value').textContent === '50 · default 20', 'the value: ' + rowOf('prompt.alarm_firings').textContent);
  assert(!rowOf('prompt.alarm_firings').querySelector('[data-force-back]'), 'it offers to go back');
  assert(rowOf('dashboard.access_token').querySelector('.technical-force__value').textContent === 'The value is not shown.', 'the hidden row: ' + rowOf('dashboard.access_token').textContent);
  assert(document.querySelector('[data-force-filter="changed"]').textContent === 'Changed from default 1', 'what differs is still counted: ' + document.querySelector('[data-force-filter="changed"]').textContent);
  // A row with nothing to say still holds its press, on a phone too.
  await Promise.all([...document.querySelectorAll('link[rel="stylesheet"]')].map(l => l.sheet ? 0 : new Promise(r => { l.addEventListener('load', r, { once: true }); l.addEventListener('error', r, { once: true }); })));
  const bare = rowOf('agency.harvest_wake');
  assert(bare.querySelector('summary').getBoundingClientRect().bottom <= bare.getBoundingClientRect().bottom + 0.5, 'its press stands out of its row: ' + bare.querySelector('summary').getBoundingClientRect().bottom + ' past ' + bare.getBoundingClientRect().bottom);
});
</script>`
	runPageInEngines(t, page, modules)
	runPageInEnginesAtSize(t, page, modules, 390, 844)
}

// IF SETTINGS DOES NOT OPEN THE PANEL SAYS SO, as every form does, and sends nothing.
func TestSettingsInForceSaysWhenSettingsDidNotOpen(t *testing.T) {
	modules := technicalFormsModules(t)
	modules["/settings-in-force"] = []byte(settingsInForceRows)
	modules["/recovery.js"] = []byte(`export function partFailed(title, err) { window.__failed = title + ': ' + err.message; }`)
	runPageInEngines(t, technicalFormsPrelude+`
run(async () => {
  start();
  S.settingsIn = () => Promise.reject(new Error('it did not come'));
  location.hash = '#/settings/technical/inforce';
  renderTechnical();
  await waitFor(() => window.__failed, 'nothing said it did not open');
  assert(/it did not come/.test(window.__failed), 'what was said: ' + window.__failed);
  assert(!document.querySelector('[data-force-row]') && !saves().length, 'it drew or sent without Settings');
});
</script>`, modules)
}

// A SET BACK ON A COLD VISIT IS ANSWERED, in the real page on a real socket, opened
// straight at the panel with Settings never opened: its refusal, its success and a
// lost connection each settle it; the press keeps the focus in its row; nothing is
// sent again. A row's address follows on the page's own route.
const forceColdDriverJS = `
const report = text => fetch('/__result', { method: 'POST', body: text, keepalive: true });
const until = async (ok, ms) => { for (let i = 0; i * 50 < (ms || 10000) && !ok(); i++) await new Promise(r => setTimeout(r, 50)); return ok(); };
const realSend = WebSocket.prototype.send;
let sets = 0, cut = false;
WebSocket.prototype.send = function (d) {
  const isSet = String(d).includes('"config_set"');
  if (isSet) sets++;
  const r = realSend.call(this, d);
  if (isSet && cut) this.close();
  return r;
};
const row = key => document.querySelector('[data-force-row="' + key + '"]');
const said = () => document.querySelector('[data-said="inforce"]');
const by = key => row(key).querySelector('.technical-force__by').textContent;
const press = async key => {
  const r = row(key);
  r.querySelector('[data-force-more]').open = true;
  await new Promise(done => setTimeout(done, 30));
  const b = row(key).querySelector('[data-force-back]');
  b.focus();
  b.click();
};
(async () => {
  await until(() => document.getElementById('send-btn'));
  location.hash = '#/settings/technical/inforce';
  await until(() => row('prompt.alarm_firings'));
  if (!row('prompt.alarm_firings')) return report('FAIL: the rows are not drawn');
  const { configBusy } = await import('/views/settings/save.js');
  await press('prompt.alarm_firings');
  await until(() => sets === 1 && !configBusy() && said());
  if (configBusy() || !said() || !said().classList.contains('bad')) return report('FAIL: its refusal was not taken: busy ' + configBusy() + ', said ' + (said() && said().outerHTML));
  if (!said().closest('[role="status"]')) return report('FAIL: the answer is said where a screen reader does not hear it');
  if (!row('prompt.alarm_firings').contains(document.activeElement)) return report('FAIL: the refusal took the focus from the row: ' + document.activeElement.outerHTML.slice(0, 80));
  await press('prompt.alarm_firings');
  await until(() => sets === 2 && !configBusy() && said() && said().classList.contains('good') && by('prompt.alarm_firings') === 'Default');
  if (configBusy() || by('prompt.alarm_firings') !== 'Default') return report('FAIL: its success was not taken: busy ' + configBusy() + ', set by ' + by('prompt.alarm_firings'));
  const focus = document.activeElement;
  if (!row('prompt.alarm_firings').contains(focus) || focus.tagName !== 'SUMMARY') return report('FAIL: the answer took the focus from the row: ' + focus.outerHTML.slice(0, 80));
  cut = true;
  await press('agency.subagent_max_legs');
  await until(() => sets === 3 && !configBusy());
  cut = false;
  if (configBusy()) return report('FAIL: a lost connection left it waiting');
  // waits by design: nothing should be sent, so there is no condition to wait for
  await new Promise(r => setTimeout(r, 1500));
  if (sets !== 3) return report('FAIL: it was sent again: ' + sets);
  location.hash = '#/settings/technical/inforce/prompt.alarm_firings';
  await until(() => row('prompt.alarm_firings').hasAttribute('data-force-here'));
  if (!row('prompt.alarm_firings').hasAttribute('data-force-here')) return report('FAIL: a row address on the page\'s own route');
  location.hash = '#/settings/technical/inforce/agency.queue_workers';
  await until(() => row('agency.queue_workers').hasAttribute('data-force-here'));
  if (!row('agency.queue_workers').hasAttribute('data-force-here') || row('prompt.alarm_firings').hasAttribute('data-force-here')) return report('FAIL: a second row address on the page\'s own route');
  // A press in the rail opens its panel at once, with no frame to draw it.
  const title = () => (document.querySelector('.technical__title') || {}).textContent;
  document.querySelector('.technical__nav a[href$="/logs"]').click();
  await until(() => title() === 'Logs', 500);
  if (title() !== 'Logs') return report('FAIL: a press in the rail did not open its panel: ' + title());
  report('OK');
})().catch(e => report('FAIL: the driver stopped: ' + ((e && e.message) || e)));
`

func TestASetBackOnAColdVisitIsAnswered(t *testing.T) {
	runDashboardInEachEngine(t, func(t *testing.T) *Server {
		var mu sync.Mutex
		calls := 0
		stated := map[string]string{"prompt.alarm_firings": "50", "agency.subagent_max_legs": "9"}
		pressed := 0
		h := statusSectionHandler(&pressed)
		h.GetConfig = func() *ConfigState { return &ConfigState{} }
		h.SettingsInForce = func() SettingsInForce {
			mu.Lock()
			defer mu.Unlock()
			rows := []SettingInForce{{Key: "agency.queue_workers", Value: "3", Usual: "3"}}
			for _, r := range []struct{ key, usual string }{{"agency.subagent_max_legs", "4"}, {"prompt.alarm_firings", "20"}} {
				value, says := stated[r.key]
				if !says {
					value = r.usual
				}
				rows = append(rows, SettingInForce{Key: r.key, Value: value, Usual: r.usual, Stated: says})
			}
			return SettingsInForce{Settings: rows}
		}
		h.SetConfig = func(changes map[string]any) (*ConfigState, error) {
			mu.Lock()
			defer mu.Unlock()
			calls++
			if calls == 1 {
				return nil, errors.New("refused by this test")
			}
			for key := range changes {
				delete(stated, key)
			}
			return &ConfigState{}, nil
		}
		return New("127.0.0.1", 0, h)
	}, "/", forceColdDriverJS)
}

// THE ROUTE: behind the dashboard's own access; no hook, unavailable.
func TestSettingsInForceRouteIsTheOperators(t *testing.T) {
	s := New("127.0.0.1", 0, &WSHandler{SettingsInForce: func() SettingsInForce {
		return SettingsInForce{Settings: []SettingInForce{{Key: "a.b", Value: "1", Usual: "2", Stated: true}, {Key: "dashboard.access_token", Hidden: true}}}
	}})
	s.SetAccessToken(true, "a-token-of-honest-length-for-this-test-0123456789")
	w := httptest.NewRecorder()
	s.serveSettingsInForce(w, httptest.NewRequest(http.MethodGet, "/settings-in-force", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("with no token: %d", w.Code)
	}
	r := httptest.NewRequest(http.MethodGet, "/settings-in-force", nil)
	r.AddCookie(&http.Cookie{Name: dashboardCookieName(r), Value: s.accessHash()})
	r.Header.Set(pageKeyHeader, pageKeyFor(s))
	w = httptest.NewRecorder()
	s.serveSettingsInForce(w, r)
	var got struct {
		Settings []SettingInForce `json:"settings"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || w.Code != http.StatusOK || len(got.Settings) != 2 || got.Settings[0].Usual != "2" {
		t.Fatalf("%d %v %s", w.Code, err, w.Body.String())
	}
	none := New("127.0.0.1", 0, &WSHandler{})
	w = httptest.NewRecorder()
	none.serveSettingsInForce(w, httptest.NewRequest(http.MethodGet, "/settings-in-force", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("no hook: %d", w.Code)
	}
}

// LAYOUT READS BACK what the page holds: where each section sits under each
// profile and the sections the host knows, with its words when no file is there.
// Nothing here writes: the file is edited by either hand.
func TestLayoutReadsBackWhatThePageHolds(t *testing.T) {
	runPageInEngines(t, technicalFormsPrelude+`
import { page } from '/sections.js';
run(async () => {
  start();
  location.hash = '#/settings/technical/layout';
  renderTechnical();
  await waitFor(() => document.querySelector('[data-technical-form="layout"]'), 'Layout did not open');
  const form = document.querySelector('.technical__pane');
  assert(document.querySelector('.c-side-nav [aria-current="page"]').textContent === 'Layout', 'the open panel is not marked');
  assert(form.querySelector('.technical-form__heading').textContent === 'To change where things are, or to add something new, ask Taylor.', 'the panel does not say to ask: ' + form.querySelector('.technical-form__heading').textContent);
  assert(form.querySelector('[data-layout-slots]').textContent === 'Nothing has been moved. The page is as it shipped.', 'nothing placed is not said: ' + form.querySelector('[data-layout-slots]').textContent);
  assert(!form.querySelector('[data-layout-sections]') && !/ui-layout|config\.json/.test(form.textContent), 'more than one line, or a file name, with nothing placed: ' + form.textContent);
  S.stats = { name: 'Unnamed' };
  renderTechnical();
  await waitFor(() => /ask them\.$/.test(form.querySelector('.technical-form__heading').textContent), 'an identity with no name is not asked as them: ' + form.querySelector('.technical-form__heading').textContent);
  S.stats = { name: 'Taylor' };
  page.layout = { profiles: { desktop: { rail: ['a.board'], panel: [] }, mobile: { dock: ['a.board', 'b.list'] } } };
  page.sectionsList = [{ id: 'a.board', slot: 'rail' }, { id: 'status', slot: 'view', built_in: true }];
  renderTechnical();
  await waitFor(() => form.querySelector('[data-layout-section="a.board"]'), 'what the page holds is not drawn');
  const slots = [...form.querySelectorAll('[data-layout-slots] li')].map(l => l.textContent);
  assert(slots.join('|') === 'desktop: raila.board|desktop: panelNothing placed.|mobile: docka.board, b.list', 'the slots: ' + slots.join('|'));
  assert(form.querySelector('[data-layout-section="a.board"]').textContent === 'a.board · rail', 'a section: ' + form.querySelector('[data-layout-section="a.board"]').textContent);
  assert(form.querySelector('[data-layout-section="status"]').textContent === 'status · viewbuilt in', 'a built-in section: ' + form.querySelector('[data-layout-section="status"]').textContent);
  assert(!form.querySelector('input, textarea, select, [data-save]'), 'the panel offers to write');
  const first = form.firstElementChild;
  renderTechnical();
  await new Promise(r => setTimeout(r, 50));
  assert(form.firstElementChild === first, 'a frame that changed nothing wrote the panel again');
});
</script>`, technicalFormsModules(t))
}

// A row's More names a place and opens it, so the name is the one the page uses
// and the address is one the page has.
func TestAPlaceInForceIsThePlaceItNames(t *testing.T) {
	page := `<!doctype html><html><body><div id="settings-stack"></div>
<script type="module">
import { S } from '/state.js';
import { renderSettings } from '/views/settings.js';
import { placeOf } from '/views/settings/technical/in-force.js';
import { run } from '/__harness.js';
run(async (assert) => {
  S.view = 'settings'; S.providersLoaded = true; S.providers = [];
  S.config = { llm: {}, speech: {}, dashboard: { host: '192.0.2.10', port: 8181, tls: true, require_token: false },
    prompt: {}, witness: {}, logs: {}, agency: {}, plugins: {}, updates: {}, restart_required: [] };
  renderSettings();
  for (const key of ['llm.provider', 'speech.heard_history', 'dashboard.port', 'certificate.domain', 'contacts', 'updates.channel']) {
    const place = placeOf(key);
    S.settingsSection('backups');
    assert(S.settingsSection(place.address) === true, key + ' opens no section: ' + place.address);
    renderSettings();
    // The place is named by a heading the page it opens shows (2b: the
    // section buttons that named it are gone).
    const heads = [...document.querySelectorAll('#settings-stack h2, #settings-stack h3')].map(h => h.textContent.trim());
    assert(heads.some(t => t.toLowerCase().includes(place.label.toLowerCase())), key + ' names ' + place.label + ', and the page it opens heads with ' + heads.join(' | '));
  }
  // Model's settings are placed under the name its page says, not one its
  // heading only contains (15 Model a: the list said "Substrate", the
  // page "Model", and the card's "SUBSTRATE — LLM" passed the check above).
  S.settingsSection('substrate');
  renderSettings();
  const said = document.querySelector('#settings-stack [data-settings-page-name]');
  assert(said && placeOf('llm.provider').label === said.textContent.trim(), 'llm.provider names ' + placeOf('llm.provider').label + ', and its page says its name is ' + (said && said.textContent.trim()));
  S.identityExists = true; S.stats = { name: 'Taylor' };
  assert(placeOf('dashboard.sent_requests.kept').label === 'What Taylor sees', 'the first panel: ' + placeOf('dashboard.sent_requests.kept').label);
  S.stats = { name: 'Unnamed' };
  assert(placeOf('dashboard.sent_requests.kept').label === 'What the model is sent', 'the first panel with no name: ' + placeOf('dashboard.sent_requests.kept').label);
  assert(placeOf('agency.roles') === null, 'Helpers is offered with no roles to list');
  S.config.agency.roles = [{ name: 'a-role' }];
  assert(placeOf('agency.roles').label === 'Helpers' && placeOf('agency.roles').address === 'technical/helpers', 'Helpers with roles: ' + JSON.stringify(placeOf('agency.roles')));
});
</script></body></html>`
	modules := map[string][]byte{}
	for _, path := range []string{"static/views/settings.js", "static/views/model-picker.js", "static/util.js"} {
		data, err := staticFS.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		modules["/"+strings.TrimPrefix(path, "static/")] = data
	}
	modules["/state.js"] = []byte(`export const S = { providers: [], config: null, providersLoaded: false };`)
	modules["/ws.js"] = []byte(`export function send() { return 'req-1'; } export function query() { return 'req-1'; }`)
	modules["/pending.js"] = []byte(`export function pendingSlot() { return { arm(){}, claim(){ return false; }, drop(){}, waiting(){ return false; } }; } export function anyPending() { return false; }`)
	modules["/sandbox.js"] = []byte(`export function sandboxCardHTML() { return ''; } export function wireSandboxCard() {}`)
	runPageInEngines(t, page, modules)
}
