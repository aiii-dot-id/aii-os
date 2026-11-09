import { S } from './state.js';

export const page = { sectionsList: [], safeReason: '', heldName: 'SAFE', layout: null, latest: new Map(), failed: new Set() };

let framesIn = null, frames = null;
const fetchFrames = () => (framesIn ||= import('./sections-frames.js').then(function (m) { frames = m; return m; }, function (err) { framesIn = null; throw err; }));
const didNotLoad = function (err) { console.warn('sections: the frames did not load', err); };

function placesAny() {
  const profiles = page.layout && page.layout.profiles;
  return !!profiles && Object.values(profiles).some(function (p) { return Object.values(p || {}).some(function (ids) { return Array.isArray(ids) && ids.length; }); });
}
function remount() {
  if (frames || placesAny()) fetchFrames().then(function (m) { m.remount(page); }, didNotLoad);
}

export function onSections(list, reason) {
  page.sectionsList = list || [];
  page.safeReason = reason || '';
  remount();
  S.sectionDraws(S.view, true);
}
export function onLayout(raw) {
  page.layout = raw || null;
  remount();
}

export function publish(msg) {
  if (msg.type === 'continuity' && msg.continuity) {
    const name = msg.continuity.mode === 'degraded_ring5' ? 'DEGRADED' : 'SAFE';
    if (name !== page.heldName) { page.heldName = name; if (page.safeReason) remount(); }
  }
  if (msg.type === 'continuity' && msg.continuity && msg.continuity.mode === 'safe') {
    if (!page.safeReason) { page.safeReason = msg.continuity.safe_reason || 'safe mode'; remount(); }
  } else if (msg.type === 'continuity' && msg.continuity && msg.continuity.mode === 'degraded_ring5') {
    if (!page.safeReason) { page.safeReason = msg.continuity.degraded_why || 'waiting for Ring 5'; remount(); }
  }
  if (page.latest.get('connection')?.on === false) told({ type: 'connection', on: true });
  told(msg);
}

function told(msg) {
  if (msg.type !== 'backups' && page.sectionsList.some(function (s) { return (s.topics || []).includes(msg.type); })) page.latest.set(msg.type, msg);
  if (frames) frames.frame(msg);
}

S.sectionsLost = function () {
  ['status', 'continuity'].forEach(function (t) { const m = page.latest.get(t); if (m) m.before = true; });
  told({ type: 'connection', on: false });
  if (frames) frames.lost();
};

S.sectionDraws = function (v, read) {
  const sec = page.sectionsList.find(function (s) { return s.built_in && s.views && s.views.includes(v); });
  if (!sec || page.failed.has(sec.id)) return false;
  const show = function (m) { m.remount(page); if (S.view === v) m.open(v, read); };
  if (frames) show(frames);
  else fetchFrames().then(show, function (err) {
    didNotLoad(err);
    page.failed.add(sec.id);
    if (S.view === v) S.go?.(v);
  });
  return true;
};

export function sectionTitle(view) { return frames ? frames.sectionTitle(view) : ''; }
export function onTokensChanged() { if (frames) frames.onTokensChanged(); }
export function initSections() {}
