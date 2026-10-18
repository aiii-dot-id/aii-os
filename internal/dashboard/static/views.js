import { S } from './state.js';
import { query } from './ws.js';
import { renderHome } from './views/home.js';
import { renderProjects } from './views/projects.js';
import { renderMemory } from './views/memory.js';
import { renderIdentity } from './views/identity.js';
import { renderPlugins } from './views/plugins.js';
import { renderSettings } from './views/settings.js';
import { renderRecord } from './views/record.js';

const TITLES = { home:'Home', chat:'Chat', projects:'Projects', memory:'Memory', identity:'Identity', record:'Record', plugins:'Plugins', settings:'Settings' };

function requestView(v, read = query) {
  if (v === 'home') { read('status'); read('continuity'); read('projects'); read('work'); }
  if (v === 'projects') read('projects');
  if (v === 'memory') read('identity');
  if (v === 'identity') { read('identity'); read('continuity'); }
  if (v === 'settings') { read('config'); read('tools'); read('sandbox'); S.requestSettingsSection?.(); }
  if (v === 'plugins') read('config');
}
function renderView(v) {
  if (v === 'home') renderHome();
  if (v === 'projects') renderProjects();
  if (v === 'memory') renderMemory();
  if (v === 'identity') renderIdentity();
  if (v === 'settings') renderSettings();
  if (v === 'plugins') renderPlugins();
  if (v === 'record') renderRecord();
  if (v === 'chat') S.interactionWake?.();
}

export { TITLES, requestView, renderView };
