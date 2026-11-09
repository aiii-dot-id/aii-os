import { act, here } from '../../ws.js';

export const copyText = text => act('clipboard.write', { text }).then(() => true, () => false);
export const linkTo = hash => here.base + hash;
export const showAddress = hash => { here.hash = hash; act('page.go', { to: hash, replace: true }).catch(() => {}); };
export const goTo = hash => { act('page.go', { to: hash }).catch(() => {}); };
export const putInReply = text => { act('reply.quote', { text }).catch(() => {}); };
