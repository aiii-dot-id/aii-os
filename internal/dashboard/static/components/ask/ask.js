import { h, fill, composing } from '../../util.js';
import W from '../../strings/home.js';

let made = 0;
const theirs = a => a.kind !== 'word' && a.kind !== 'confirm';

export function render({ ask: a, name = '', onAnswer = () => false, into = null }) {
  const id = 'c-ask-' + (++made);
  const attrs = { class: 'c-ask', 'data-kind': a.kind, 'data-from': theirs(a) ? 'identity' : a.kind === 'word' ? 'host' : 'plugin', 'data-state': 'open', 'aria-labelledby': id + '-label ' + id + '-text' };
  let el = into;
  if (el) { el.replaceChildren(); el.className = ''; for (const [k, v] of Object.entries(attrs)) el.setAttribute(k, v); }
  else el = h('section', attrs);
  const label = theirs(a) ? fill(W.asks, { name: name || W.yourIdentity }) : a.kind === 'word' ? W.askWordsTitle : a.from || a.plugin || '';
  const text = a.kind === 'confirm' ? a.text || a.operation || '' : a.text || '';
  el.append(
    h('p', { class: 'c-ask__label', id: id + '-label' }, label),
    h('p', { class: 'c-ask__text' + (theirs(a) ? ' c-ask__text--theirs' : ''), id: id + '-text' }, a.kind === 'word' ? '“' + text + '”' : text));
  if (a.kind === 'confirm') {
    if (a.operation || a.effects) el.append(h('p', { class: 'c-ask__hint' }, [a.operation, a.effects].filter(Boolean).join(' · ')));
    const args = Object.keys(a.args || {}).sort();
    if (args.length) el.append(h('dl', { class: 'c-ask__args' }, ...args.flatMap(k => [h('dt', {}, k), h('dd', {}, typeof a.args[k] === 'string' ? a.args[k] : JSON.stringify(a.args[k]))])));
  }
  const bar = h('div', { class: 'c-ask__bar' });
  const press = (b, p) => {
    if (el.dataset.state !== 'open' && el.dataset.state !== 'refused') return;
    if (!onAnswer(p)) return;
    el.querySelectorAll('[aria-pressed]').forEach(o => o.setAttribute('aria-pressed', String(o === b)));
  };
  const button = (text, p, quiet) => {
    const b = h('button', { type: 'button', class: 'c-button c-button--' + (quiet ? 'quiet' : 'secondary') + ' c-ask__choice' + (quiet ? ' c-ask__choice--quiet' : ''), 'aria-pressed': 'false' }, text);
    b.onclick = () => press(b, p);
    return b;
  };
  const enterKeepsComposing = input => input.addEventListener('keydown', e => { if (e.key === 'Enter' && composing(e)) e.preventDefault(); });
  const field = () => {
    const input = h('input', { type: 'text', class: 'c-ask__input', maxlength: '2000', placeholder: W.askAnswerPlaceholder, 'aria-label': W.askAnswerPlaceholder });
    const say = h('button', { type: 'submit', class: 'c-button c-button--primary c-ask__send' }, W.askSend);
    const form = h('form', { class: 'c-ask__say' }, input, say);
    form.onsubmit = e => {
      e.preventDefault();
      const words = input.value.trim();
      if (!words) { input.focus(); return; }
      press(say, { answer: 'said', text: words });
    };
    enterKeepsComposing(input);
    return form;
  };
  switch (a.kind) {
    case 'choose': {
      bar.append(...(a.choices || []).map(c => button(c, { answer: 'chose', choice: c })));
      const other = button(W.askSomethingElse, null, true);
      other.removeAttribute('aria-pressed');
      other.onclick = () => { other.replaceWith(field()); el.querySelector('.c-ask__input').focus(); };
      bar.append(other, button(W.askNotNow, { answer: 'not_now' }, true));
      break;
    }
    case 'connect':
      bar.append(button(W.askConnectRead, { answer: 'connect', scope: 'read' }), button(W.askConnectChange, { answer: 'connect', scope: 'modify' }), button(W.askNotNow, { answer: 'not_now' }, true));
      el.append(bar, h('p', { class: 'c-ask__hint' }, [a.connector, W.askConnectSetUp].filter(Boolean).join(' · ')));
      break;
    case 'word':
      bar.append(button(W.askWordsYes, { answer: 'confirm' }), button(W.askWordsNo, { answer: 'no' }, true));
      break;
    case 'voice': {
      const who = h('input', { type: 'text', class: 'c-ask__input', maxlength: '200', placeholder: W.askVoiceWho, 'aria-label': W.askVoiceWho });
      const no = button(W.askVoiceNo, null, true);
      no.onclick = () => press(no, { answer: 'no', text: who.value.trim() });
      const say = h('form', { class: 'c-ask__say' }, who, no);
      say.onsubmit = e => { e.preventDefault(); if (who.value.trim()) no.click(); };
      enterKeepsComposing(who);
      bar.append(button(W.askVoiceYes, { answer: 'confirm' }), say);
      if (a.heard) el.append(h('p', { class: 'c-ask__hint' }, '“' + a.heard + '”'));
      break;
    }
    case 'confirm':
      bar.append(button(W.askAllowOnce, { act: 'confirm' }), button(W.askAllowAlways, { act: 'always' }), button(W.askDeny, { act: 'deny' }, true));
      el.append(bar, h('p', { class: 'c-ask__hint' }, W.askAlwaysNote));
      break;
    default:
      bar.append(field(), button(W.askNotNow, { answer: 'not_now' }, true), button(W.askWordsNo, { answer: 'no' }, true));
  }
  if (!bar.isConnected) el.append(bar);
  el.append(h('p', { class: 'c-ask__note' }));
  return el;
}

export function update(el, { state, note = '' }) {
  el.dataset.state = state;
  const live = state === 'open' || state === 'refused';
  el.querySelectorAll('button, input').forEach(b => { b.disabled = !live; });
  if (live) el.querySelectorAll('[aria-pressed="true"]').forEach(b => b.setAttribute('aria-pressed', 'false'));
  const said = el.querySelector('.c-ask__note');
  if (said.textContent !== note) said.textContent = note;
}

export const typed = el => el.querySelector('.c-ask__input')?.value || '';
export function type(el, words) {
  if (!words) return;
  if (!el.querySelector('.c-ask__input')) [...el.querySelectorAll('.c-ask__choice--quiet:not([aria-pressed])')].forEach(b => b.click());
  const input = el.querySelector('.c-ask__input');
  if (input) input.value = words;
}
