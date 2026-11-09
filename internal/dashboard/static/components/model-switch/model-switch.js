import { S } from '../../state.js';
import { $, h, fill } from '../../util.js';
import { later } from '../../shell/later.js';
import { fillModelPicker, providerModels } from '../../views/model-picker.js';
import W from '../../strings/shell.js';

let draft = null;
const inForce = () => {
  const l = (S.config && S.config.llm) || {};
  return { provider: l.resolved_provider || l.provider || '', model: l.resolved_model || l.model || '' };
};
const answering = p => !p.status || p.status === 'ok';
const candidates = () => (S.providers || []).filter(p => p.chat !== false && (answering(p) || p.name === inForce().provider));
const named = p => answering(p) ? p.name : fill(W.providerNotAnswering, { name: p.name });
const offered = (provider, model) => {
  const p = candidates().find(x => x.name === provider), now = inForce();
  return !!p && !!model && (providerModels(p).includes(model) || model === p.default_model || (provider === now.provider && model === now.model));
};

function mount() {
  const there = $('chat-substrate-wrap');
  if (there) return there;
  if (!$('view-chat')) return null;
  const wrap = h('div', { class: 'c-model-switch', id: 'chat-substrate-wrap', hidden: true },
    h('div', { class: 'c-model-switch__row' },
      h('select', { id: 'chat-provider', class: 'c-model-switch__provider', 'aria-label': W.provider }),
      h('select', { id: 'chat-model', class: 'c-model-switch__model', 'aria-label': W.model }),
      h('button', { type: 'button', class: 'btn ghost c-model-switch__use', id: 'chat-substrate-apply' }, W.modelUse)),
    h('div', { class: 'c-model-switch__said', id: 'chat-substrate-status', role: 'status' }));
  wrap.querySelector('#chat-provider').addEventListener('change', e => {
    draft = { provider: e.target.value, model: null };
    fillModels(draft.provider, null);
    draft.model = $('chat-model').value;
    render();
  });
  wrap.querySelector('#chat-model').addEventListener('change', e => {
    draft = { provider: $('chat-provider').value, model: e.target.value };
    render();
  });
  wrap.querySelector('#chat-substrate-apply').addEventListener('click', use);
  $('view-chat').prepend(wrap);
  return wrap;
}

function fillModels(providerName, preferred) {
  fillModelPicker($('chat-model'), candidates().find(p => p.name === providerName), preferred);
}

function use() {
  const provider = $('chat-provider').value, model = $('chat-model').value;
  if (!offered(provider, model)) { draft = null; render(true); return; }
  const asked = { provider, model };
  S.settingsIn().then(() => {
    if (!offered(asked.provider, asked.model)) { if (draft && draft.provider === asked.provider) draft = null; render(true); return; }
    draft = asked;
    later('sendConfigChanges')('llm', { 'llm.provider': provider, 'llm.model': model });
    render();
  });
}

export function render(force) {
  const wrap = mount();
  if (!wrap) return;
  const list = candidates();
  wrap.hidden = !S.identityExists || !S.config || !list.length;
  if (wrap.hidden) return;
  const now = inForce();
  if (draft && draft.provider === now.provider && draft.model === now.model) draft = null;
  if (draft && !list.some(p => p.name === draft.provider)) { draft = null; force = true; }
  const shown = draft || now;
  const provider = $('chat-provider'), model = $('chat-model'), apply = $('chat-substrate-apply');
  if (force || document.activeElement !== provider) {
    provider.replaceChildren(...(shown.provider && !list.some(p => p.name === shown.provider) ? [h('option', { value: shown.provider }, shown.provider)] : []),
      ...list.map(p => h('option', { value: p.name }, named(p))));
    provider.value = shown.provider;
  }
  if (force || document.activeElement !== model) fillModels(shown.provider, shown.model);
  const busy = !!later('configBusy')();
  provider.disabled = model.disabled = busy;
  apply.disabled = busy || !model.value || (provider.value === now.provider && model.value === now.model);
  $('chat-substrate-status').innerHTML = later('modelSaidHTML')() || '';
}
