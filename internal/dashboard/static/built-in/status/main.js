import { ready } from '/section-api.js';
import { S } from './state.js';
import { useBridge } from './ws.js';
import { hearPageKeys } from './built-in/page-keys.js';
import { keepInSection } from './built-in/keep.js';
import { renderStatus, readStatus } from './views/settings/status.js';

const api = await ready();
useBridge(api);
hearPageKeys(api);
keepInSection(api, 'status');
api.tokens(tokens => { for (const [name, value] of Object.entries(tokens)) document.documentElement.style.setProperty(name, value); });
const on = (topic, take) => api.data.subscribe(topic, (data, before) => { take(data, before); renderStatus(); });
let ended = null;
await Promise.all([
  on('connection', d => { S.connected = d; if (!d) S.pageFrames = { status: false, continuity: false }; }),
  on('status', (d, before) => { S.stats = d; S.pageFrames.status = !before; }),
  on('continuity', (d, before) => { S.cont = d; S.pageFrames.continuity = !before; }),
  on('config', d => { S.config = d; }),
  on('providers', d => { S.providers = d || []; }),
  on('tools', d => { S.tools = d || []; }),
  on('work', d => { S.work = d; }),
  on('asks', d => { S.asks = d || []; }),
  on('overlays', d => { S.overlays = d || []; }),
  on('projects', d => { S.activeProject = (d || []).find(p => p.active) || null; }),
  on('turn', d => { S.thinking = d.thinking; if (ended !== null && d.ended !== ended) S.turnEnded?.(); ended = d.ended; }),
]);
await on('route', () => readStatus());
renderStatus();
