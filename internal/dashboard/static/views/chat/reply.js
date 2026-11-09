import { S } from '../../state.js';
import { $ } from '../../util.js';
import { send, wsReady } from '../../ws.js';
import { toast } from '../../app.js';
import * as replyBox from '../../components/reply-box/reply-box.js';
import { wireAttach } from '../attach.js';
import { sendChat } from './thread.js';

wireAttach();
replyBox.mount({
  onSend: text => sendChat(text),
  onStop: () => { send({ type: 'cancel' }); },
  onEffort: (level, checked) => {
    if (!wsReady()) { renderComposer(); return; }
    if (checked) toast('Checking ' + replyBox.effortName(level) + ' with the provider before it applies…');
    send({ type: 'effort_set', effort: level });
  },
  onFiles: () => { $('attach-input')?.click(); },
  onModel: () => { S.openSettings?.('substrate', 'cfg-model'); },
});

export function renderComposer() {
  const called = S.identityExists && S.stats ? S.stats.name : '';
  const named = called && called !== 'Unnamed' ? called : '', name = named || 'them';
  const l = (S.config && S.config.llm) || {};
  const d = S.identityExists ? l.effort_choice : null;
  replyBox.update({
    name, named,
    state: S.wsEverOpened && !S.connected && !$('dashboard-signin') ? 'offline' : S.thinking ? 'answering' : 'idle',
    answering: !!S.thinking,
    nothingScheduled: !!S.work?.nothing_scheduled,
    waitsOn: S.work?.resumes?.on || '',
    levels: d && d.levels && d.levels.length ? d.levels : null,
    inForce: (d && d.in_force) || '',
    checked: (d && d.checked) || [],
    model: S.identityExists ? l.resolved_model || l.model || '' : '',
  });
}
S.renderComposer = renderComposer;
