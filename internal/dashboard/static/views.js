import { S } from './state.js';
import { query } from './ws.js';
import { renderHome } from './views/home.js';
import WI from './strings/identity.js';
import { placeThread } from './shell/thread-place.js';
import { offer, later } from './shell/later.js';
import { sheet } from './shell/sheet.js';
import { route } from './shell/router.js';
import WS from './strings/status.js';
import W from './strings/shell.js';

const once = load => {
  let p = null, got;
  const ask = () => (p ||= load().then(m => (got = m), err => { p = null; throw err; }));
  ask.now = () => got;
  return ask;
};
const settings = once(() => Promise.all([Promise.all([
  import('./views/settings.js'), import('./views/plugin-setting.js'), import('./sandbox.js'), import('./tools.js'),
  import('./views/backups.js'), import('./views/messages.js'), import('./views/mail-repair.js'), import('./views/expert.js'),
]), sheet('views/settings/access.css'), sheet('views/settings/people.css'), sheet('views/settings/limit-row.css'), sheet('views/settings/voice.css')]).then(([mods]) => {
  mods.forEach(offer);
  const r = route(location.hash);
  if (r && r.view === 'settings' && r.project) S.settingsSection?.(r.project);
  if (S.view === 'settings') S.requestSettingsSection?.();
  return mods[0];
}));
S.settingsHeld = () => !settings.now();
S.settingsIn = settings;
let generation = 0;
export const navigated = () => { generation++; };
export const door = () => { const mine = ++generation; return () => generation === mine; };
for (const name of ['openSettings', 'openUpdates']) {
  const stand = (...args) => {
    const holds = door();
    return settings().then(() => {
      if (!holds()) return;
      if (S[name] !== stand) S[name]?.(...args);
    }, didNotOpen('settings'));
  };
  S[name] = stand;
}
const bundle = (offering, css, redraws, ...loads) => {
  let ready = false;
  const holding = load => load().then(m => {
    for (const name of redraws) {
      const own = S[name];
      if (own && !own.holding) { S[name] = (...args) => { if (ready) own(...args); }; S[name].holding = true; }
    }
    return m;
  });
  return once(() => Promise.all([...loads.map(holding), ...css.map(sheet)])
    .then(all => { ready = true; const mods = all.slice(0, loads.length); if (offering) mods.forEach(offer); return mods; }));
};
const plugins = bundle(true, ['views/settings/permissions.css'], ['framePagesChanged'], () => settings().then(() => import('./views/plugins.js')));
const record = bundle(false, ['views/identity/record.css', 'components/filter/filter.css'], [], () => import('./views/identity/record.js'));
const about = bundle(true, ['views/identity/who.css', 'components/filter/filter.css', 'views/identity/experiences.css',
  'components/belief-row/belief-row.css', 'components/why-trail/why-trail.css', 'views/identity/beliefs.css'],
  ['renderIdentityView', 'renderBeliefs', 'renderBetweenAgain', 'renderExperiences'],
  () => import('./views/memory.js'), () => import('./views/identity.js'), () => import('./views/identity/between.js'),
  () => import('./views/identity/experiences.js'), () => import('./views/backups.js'));
const status = bundle(true, ['views/settings/status.css'], ['renderStatus'], () => import('./views/settings/status.js'));
const firstRun = bundle(true, ['views/firstrun/start.css'], [], () => import('./views/firstrun/start.js'), () => import('./firstboot-restore.js'));
const firstRunIn = once(() => firstRun().then(() => later('renderProviderOptions')()));
export const fetchFirstRun = () => firstRunIn().catch(err => import('./recovery.js').then(r => r.startFailed(err), () => {}));
const technical = bundle(false, ['views/settings/technical/technical.css', 'views/settings/limit-row.css'], ['renderTechnical'], () => import('./views/settings/technical/technical.js'));
const didNotOpen = v => err => import('./recovery.js').then(r => r.partFailed(TITLES[v] || v, err), () => {});
const projects = bundle(true, ['views/projects.css'], [], () => import('./views/projects.js'));
export const showProject = (id, goTo) => {
  if (projects.now() !== undefined) { projects.now()[0].viewProject(id); return; }
  if (S.view !== 'projects') goTo('projects');
  const mine = ++generation;
  const named = () => { let r = route(location.hash); if (r && r.redirect) r = route(r.redirect); return !!r && r.view === 'projects' && r.project === id; };
  projects().then(([m]) => { if (generation === mine && named()) m.viewProject(id); }, didNotOpen('projects'));
};
const whenIn = (load, act) => { if (load.now() !== undefined) act(load.now()); else load().then(act, () => {}); };
const draw = (v, load, render) => {
  const shown = m => {
    if (S.view !== v || S.sectionDraws?.(v)) return;
    render(m);
    const el = document.getElementById('view-' + v);
    if (el && !el.classList.contains('on')) { el.classList.add('on'); el.inert = false; el.setAttribute('aria-hidden', 'false'); }
  };
  if (load.now() !== undefined) shown(load.now());
  else load().then(shown, didNotOpen(v));
};

const TITLES = { chat:'Chat', projects:'Projects', memory:'Memory', identity:'Identity', between: WI.tabs.between, experiences: WI.tabs.experiences, record:'Record', plugins:'Plugins', status: WS.title, phone: W.phoneAndDevices, technical: W.technical, settings:'Settings' };

function requestView(v, read = query) {
  if (S.sectionDraws?.(v, true)) return;
  if (v === 'chat') { read('status'); read('continuity'); read('projects'); read('work'); read('identity'); }
  if (v === 'projects') { read('projects'); read('work'); }
  if (v === 'memory') read('identity');
  if (v === 'identity') { read('identity'); read('continuity'); read('work'); }
  if (v === 'between') { read('identity'); read('continuity'); }
  if (v === 'experiences') read('identity');
  if (v === 'status') whenIn(status, () => later('readStatus')(read));
  if (v === 'settings') { read('config'); read('tools'); read('sandbox'); S.requestSettingsSection?.(); }
  if (v === 'plugins' || v === 'phone') read('config');
  if (v === 'technical') whenIn(technical, ([m]) => m.readTechnical());
}
function renderView(v) {
  if (S.sectionDraws?.(v)) return;
  if (v === 'projects') draw(v, projects, ([m]) => m.renderProjects());
  if (v === 'memory') draw(v, about, () => later('renderMemory')());
  if (v === 'identity') draw(v, about, () => later('renderIdentity')(true));
  if (v === 'between') draw(v, about, () => later('renderBetween')());
  if (v === 'experiences') draw(v, about, () => later('renderExperiences')());
  if (v === 'status') draw(v, status, () => later('renderStatus')());
  if (v === 'settings') draw(v, settings, m => m.renderSettings());
  if (v === 'phone') draw(v, settings, m => m.renderPhone());
  if (v === 'plugins') draw(v, plugins, ([m]) => m.renderPlugins());
  if (v === 'record') draw(v, record, ([m]) => m.renderRecord());
  if (v === 'technical') draw(v, technical, ([m]) => m.renderTechnical());
  if (v === 'chat') { placeThread('chat'); renderHome(); S.interactionWake?.(); }
}

export { TITLES, requestView, renderView };
