import { S } from './state.js';
import { esc, fill } from './util.js';
import { send } from './ws.js';
import { pendingSlot } from './pending.js';
import { sheet } from './shell/sheet.js';
import W from './strings/settings-permissions.js';

const pending = pendingSlot();
let notice = '';
const render = () => { if (S.renderTools) S.renderTools(); };

export function acceptTools(requestID) {
  if (pending.waiting() && !pending.claim(requestID)) return;
  notice = '';
}
export function rejectTools(requestID, text) {
  if (!pending.claim(requestID)) return false;
  notice = text; render(); return true;
}
export function toolsConnectionLost() {
  if (!pending.drop()) return;
  notice = 'Connection lost — completion is unknown. The switches show what the runtime last reported; nothing is automatically retried.';
  render();
}

export function toolsCardHTML(note = () => '') {
  sheet('views/settings/permissions.css').catch(() => {});
  const held = pending.waiting();
  const on = t => (held && held.tool === t.name ? held.enabled : t.enabled);
  const n = (S.stats && S.stats.name) || '', name = n && n !== 'Unnamed' ? n : '';
  return '<section class="permissions__part" data-tools aria-labelledby="tools-h" aria-describedby="tools-hint">' +
    '<h3 id="tools-h" class="permissions__h3">' + esc(W.tools) + '<span class="permissions__soft"> · ' + S.tools.length + '</span></h3>' +
    '<p id="tools-hint" class="permissions__hint">' + esc(name ? fill(W.toolsHint, { name }) : W.toolsHintUnnamed) + '</p>' +
    '<ul class="permissions__rows">' + S.tools.map(t =>
      '<li class="permissions__row"><label class="permissions__switch permissions__switch--row"><span class="permissions__words"><span>' + esc(t.name) +
      (t.plugin ? '<span class="permissions__soft"> · ' + esc(fill(W.toolFrom, { plugin: t.plugin })) + '</span>' : '') + '</span>' +
      '<span class="permissions__hint">' + esc(t.description) + note(t) + '</span></span>' +
      '<input type="checkbox" role="switch" class="permissions__sw" data-tool="' + esc(t.name) + '"' + (on(t) ? ' checked' : '') + (held ? ' disabled' : '') + '></label></li>').join('') + '</ul>' +
    '<p class="permissions__said" data-announce>' + esc(notice) + '</p></section>';
}

export function wireToolsCard(st) {
  st.querySelectorAll('[data-tool]').forEach(sw => {
    sw.onchange = () => {
      const tool = sw.dataset.tool, enabled = sw.checked;
      const requestID = send({ type: 'tool_toggle', tool, enabled });
      notice = pending.arm({ tool, enabled }, requestID) ? 'Waiting for the runtime to confirm…' : 'Not connected — ' + tool + ' was not changed.';
      render();
    };
  });
}
