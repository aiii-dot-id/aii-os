import { S } from '../state.js';
import { $ } from '../util.js';
import { ownsThread } from '../registry.js';

export function placeThread(v) {
  if (!ownsThread(v)) return;
  const thread = $('thread'), reply = $('composer-wrap');
  const body = document.querySelector('#view-chat > .chat-body'), chat = $('view-chat');
  if (!thread || !reply) return;
  if (body && thread.parentElement !== body) body.prepend(thread);
  if (chat && reply.parentElement !== chat) chat.appendChild(reply);
  const model = $('chat-substrate-wrap');
  if (model && chat && model.parentElement !== chat) chat.prepend(model);
  S.placeAsks?.();
}
