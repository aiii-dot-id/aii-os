import { captureEdits, clearLimitDraft, configBusy, configSaidHTML, editorFields, fieldKey, fieldValue, isEdited, saveSettings, sendConfigChanges, setConfigResult } from '../save.js';
import { limitDrafted } from '../parts.js';
import { edits } from '../state.js';
import { S } from '../../../state.js';
import { esc, fill, h } from '../../../util.js';
import W from '../../../strings/settings-technical-forms.js';

const drafted = new Map();
const pending = new Map();
const wrongOn = new Set();
const PANEL_OF = { witness: 'verification', cognition: 'reflection', logs: 'logs', storage: 'storage', limits: 'limits' };
let open = null;
let goingToWrong = false;

const isOpen = () => open && open.root.isConnected && open.root.dataset.form === open.section;
const overridesDrafted = section => limitDrafted(S.config) && (section === 'limits' || section === 'cognition');
function collect() {
  if (!isOpen()) return;
  const { root, section } = open;
  captureEdits(root);
  editorFields(root).filter(el => !el.id.startsWith('lim-add-')).forEach(el => {
    const key = fieldKey(el), held = edits.get(key);
    if (held && (isEdited(held) || held.validity?.badInput)) drafted.set(key, section); else drafted.delete(key);
  });
  if (![...drafted.values()].includes(section) && !overridesDrafted(section)) { pending.delete(section); wrongOn.delete(section); return; }
  const got = firstWrong(root) ? null : saveSettings(section, true);
  if (got) { pending.set(section, got.changes); wrongOn.delete(section); }
  else { pending.delete(section); wrongOn.add(section); }
}
const firstWrong = root => editorFields(root).find(el => el.getAttribute('aria-invalid') === 'true' && drafted.has(fieldKey(el)));
function count() {
  for (const key of drafted.keys()) if (!edits.has(key)) drafted.delete(key);
  for (const held of [pending, wrongOn]) for (const section of held.keys()) if (![...drafted.values()].includes(section) && !overridesDrafted(section)) held.delete(section);
  return drafted.size + (limitDrafted(S.config) ? 1 : 0);
}
function press() {
  if (configBusy()) return;
  collect();
  const here = isOpen() && firstWrong(open.root);
  if (here) { here.focus(); return; }
  const elsewhere = [...wrongOn].find(section => !isOpen() || section !== open.section);
  if (elsewhere) {
    goingToWrong = true;
    location.hash = '#/settings/technical/' + PANEL_OF[elsewhere];
    S.renderTechnical?.();
    return;
  }
  if (wrongOn.size) { drawBar(); return; }
  const changes = Object.assign({}, ...pending.values());
  if (!Object.keys(changes).length) { drawBar(); return; }
  sendConfigChanges('technical', changes, { fields: new Map([...drafted.keys()].map(key => [key, fieldValue(edits.get(key))])) });
  drawBar();
}
function discard() {
  for (const key of drafted.keys()) edits.delete(key);
  drafted.clear(); pending.clear(); wrongOn.clear(); clearLimitDraft(); setConfigResult(null);
  if (isOpen()) { open.root.technicalDrawn = null; open.root.replaceChildren(); }
  S.renderTechnical?.();
  drawBar();
}
function bar() {
  let el = document.querySelector('[data-technical-bar]');
  if (el) return el;
  const save = h('button', { type: 'button', class: 'c-button c-button--primary', 'data-save': 'technical' }, W.save);
  const away = h('button', { type: 'button', class: 'c-button c-button--quiet', 'data-discard': '' }, W.discard);
  save.onclick = press; away.onclick = discard;
  el = h('div', { class: 'technical-savebar', 'data-technical-bar': '' }, h('span', { class: 'technical-savebar__said', role: 'status', tabindex: '-1' }), save, away);
  (document.getElementById('view-technical') || document.body).appendChild(el);
  return el;
}
export function drawBar() {
  const el = bar(), n = count(), answer = configSaidHTML('technical');
  el.hidden = !n && !answer;
  const said = el.querySelector('.technical-savebar__said');
  const words = answer || esc(n === 1 ? W.changeOne : fill(W.changeMany, { n }));
  if (said.innerHTML !== words) said.innerHTML = words;
  const save = el.querySelector('[data-save]'), away = el.querySelector('[data-discard]');
  const held = el.contains(document.activeElement) && document.activeElement !== said;
  save.setAttribute('aria-disabled', String(configBusy()));
  save.hidden = away.hidden = !n;
  if (held && !n) said.focus({ preventScroll: true });
}
export function formDrawn(root, section) {
  if (!isOpen() || open.root !== root || open.section !== section) { if (!configBusy()) setConfigResult(null); }
  open = { root, section };
  collect();
  if (goingToWrong) { goingToWrong = false; firstWrong(root)?.focus(); }
  drawBar();
}
export function formEdited() { setConfigResult(null); collect(); drawBar(); }
