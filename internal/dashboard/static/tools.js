import { S } from './state.js';
import { cardHead, esc } from './util.js';
import { send } from './ws.js';
import { pendingSlot } from './pending.js';

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
  const held = pending.waiting();
  const on = t => (held && held.tool === t.name ? held.enabled : t.enabled);
  return '<div class="card">' + cardHead('TOOLS — RING 5 REACH', 'Every tool the identity may call, each with its switch: a tool switched off is not offered to them, and a call to it is refused.') +
    (S.tools.length ? S.tools.map(t =>
      '<div class="tool-row"><span class="tn">' + esc(t.name) + '</span><span class="td">' + esc(t.description) + note(t) + '</span>' +
      '<label class="switch"><input type="checkbox" data-tool="' + esc(t.name) + '"' + (on(t) ? ' checked' : '') + (held ? ' disabled' : '') + '><span class="tk"></span></label></div>').join('') :
      '<div class="empty">no tools</div>') +
    '<div data-announce>' + esc(notice) + '</div></div>';
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
