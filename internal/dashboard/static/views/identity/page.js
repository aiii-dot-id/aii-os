import { S } from '../../state.js';
import { $, copyText } from '../../util.js';

export { copyText };
export const linkTo = hash => location.origin + location.pathname + hash;
export const showAddress = hash => { try { history.replaceState(null, '', hash); } catch (err) { } };
export const goTo = hash => { location.hash = hash; };

export function putInReply(text) {
  const input = $('msg-input');
  if (!input) return;
  input.value = text;
  input.dispatchEvent(new Event('input', { bubbles: true }));
  S.go?.('chat');
  input.focus();
  input.setSelectionRange(input.value.length, input.value.length);
}
