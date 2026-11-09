
import { S } from '../state.js';
import { renderDelivered, gradeFocusOf } from './delivered.js';
import { $, esc, hueOf, bytes, fill } from '../util.js';
import { send, query } from '../ws.js';
import { go } from '../app.js';
import { pendingSlot } from '../pending.js';
import { holdReloadWhile } from '../overlay.js';
import { signedFetch, ticketed } from '../pagekey.js';
import { recordedHTML } from './work-recorded.js';
import { titlePlace } from '../waiting.js';
import W from '../strings/projects.js';
import C from '../strings/components.js';
export { renderProjPill } from '../shell/project-pill.js';

export const createPending = pendingSlot();
let newProjectDraft = { name: '', description: '' };
holdReloadWhile(() => !!(S.contractDraft || S.focusDraft || newProjectDraft.name || newProjectDraft.description));
export const focusPending = pendingSlot();
export const contractPending = pendingSlot();

const dockFilterInput = typeof document !== 'undefined' ? document.getElementById('dock-filter') : null;
if (dockFilterInput) dockFilterInput.addEventListener('input', () => {
  S.dockFilter = dockFilterInput.value;
  renderDock();
});

function projectAct(action, fields) { return send({ type: 'project', project: Object.assign({ action: action }, fields || {}) }); }

function captureContractDraft(id) {
  const v = (el) => (el ? el.value : '');
  return {
    id: id,
    outcome: v($('ct-outcome')),
    acceptance: v($('ct-accept')),
    constraints: v($('ct-constr')),
    parent: $('ct-parent') ? $('ct-parent').value : null,
  };
}

export function viewProject(id) {
  projectAct('select', { id: id });
  S.viewedProject = id;
  S.projTab = 'overview';
  try { if (location.hash !== '#/projects/' + id) location.hash = '#/projects/' + id; } catch (err) {}
  if (S.view !== 'projects') go('projects'); else renderProjects();
}

export function workInProject(id) { projectAct('select', { id: id }); }

export function stopWorkingInProject() { projectAct('deselect', {}); }

export function newlyCreatedID(prevIDs, projects) {
  const fresh = (projects || []).filter(p => prevIDs.indexOf(p.id) < 0);
  return fresh.length === 1 ? fresh[0].id : '';
}

export function rejectCreate(requestID) {
  if (!createPending.claim(requestID)) return false;
  if (S.view === 'projects') renderProjects();
  return true;
}

export function acceptCreate(requestID, prevIDs) {
  const pending = createPending.claim(requestID); if (!pending) return '';
  if (JSON.stringify(newProjectDraft) === JSON.stringify(pending.draft)) {
    newProjectDraft = { name: '', description: '' };
    if ($('np-name')) $('np-name').value = '';
    if ($('np-desc')) $('np-desc').value = '';
  }
  return newlyCreatedID(prevIDs, S.projects);
}
export function acceptFocusSave(requestID) {
  if (continueAnswered(requestID)) return false;
  const pending = focusPending.claim(requestID); if (!pending) return false;
  if (JSON.stringify(S.focusDraft) === JSON.stringify(pending.draft)) S.focusDraft = null;
  return true;
}
export function acceptContractSave(requestID) {
  const pending = contractPending.claim(requestID); if (!pending) return false;
  if (JSON.stringify(S.contractDraft) === JSON.stringify(pending.draft)) S.contractDraft = null;
  return true;
}

export function rejectFocusSave(requestID) {
  return continueRefused(requestID) || !!focusPending.claim(requestID);
}
export function rejectContractSave(requestID) {
  return !!contractPending.claim(requestID);
}

export function projectsConnectionLost() {
  const create = createPending.drop();
  const focus = focusPending.drop();
  const contract = contractPending.drop();
  const resumed = continuePending.drop();
  if (resumed) { continueSaidText = C.connectionLostCheck; redraw(); }
  return !!(create || focus || contract || resumed);
}

function viewedOf() {
  const id = (S.viewedProject === null) ? (S.activeProject ? S.activeProject.id : '') : S.viewedProject;
  if (!id) return null;
  return S.projects.find(pp => pp.id === id) || null;
}

function emptyProjectsHTML() {
  return '<div class="projects__page">' +
    (S.projects.length ? '' : '<p class="projects__after">' + esc(W.noneYet) + '</p>') +
    part('pj-new', esc(W.newProject), esc(named(W.whatIsItHint, W.whatIsItHintUnnamed)),
      '<div class="projects__form"><label class="projects__label" for="np-name">' + esc(W.name) + '</label><input type="text" id="np-name">' +
      '<label class="projects__label" for="np-desc">' + esc(W.whatIsIt) + '</label><textarea id="np-desc" rows="2"></textarea>' +
      '<div class="savebar projects__buttons"><button class="c-button c-button--primary" id="np-create">' + esc(W.create) + '</button></div></div>') +
    '</div><div id="projects-delivered"></div>';
}
export function renderProjects() {
  const sp = $('proj-space');
  const p = viewedOf();

  titlePlace(p ? p.name : '');

  const gradeFocus = gradeFocusOf();
  const editing = document.activeElement && document.activeElement.id;
  const edit = editing === 'focus-edit';

  if (p && (editing === 'ct-outcome' || editing === 'ct-accept' ||
            editing === 'ct-constr' || editing === 'ct-parent')) {
    S.contractDraft = captureContractDraft(p.id);
    return;
  }
  if (edit && p) {
    S.focusDraft = { id: p.id, val: $('focus-edit').value };
    return;
  }
  if (!p) {
    if ($('np-name') && $('np-desc')) newProjectDraft = { name: $('np-name').value, description: $('np-desc').value };
    if (editing === 'np-name' || editing === 'np-desc') {
      const btn = $('np-create'); btn.disabled = !!createPending.waiting(); btn.textContent = btn.disabled ? 'Creating…' : 'Create';
      renderDock(); return;
    }
    S.focusDraft = null;

    const ghost = S.projectsPrimed && S.viewedProject !== null && S.viewedProject !== '' &&
      !S.projects.some(pr => pr.id === S.viewedProject) ? S.viewedProject : null;
    let ghostBanner = '';
    if (ghost !== null) {
      ghostBanner = '<div id="ghost-banner" class="card" style="margin:18px auto;max-width:430px;border-left:3px solid var(--warn,#c33)">' +
        'This address names no project: ' + esc(ghost) +
        '</div>';
      try { if (location.hash === '#/projects/' + ghost) history.replaceState(null, '', '#/projects'); } catch (err) {}
    }
    sp.innerHTML = ghostBanner + emptyProjectsHTML();
    renderDelivered(sp.querySelector('#projects-delivered'), true, gradeFocus);
    const nameInput = $('np-name'), description = $('np-desc');
    nameInput.value = newProjectDraft.name; description.value = newProjectDraft.description;
    nameInput.oninput = description.oninput = () => { newProjectDraft = { name: nameInput.value, description: description.value }; };
    const btn = $('np-create');
    btn.disabled = !!createPending.waiting();
    btn.textContent = btn.disabled ? 'Creating…' : 'Create';
    if (btn) btn.onclick = () => {
      const name = $('np-name').value.trim();

      if (name && !createPending.waiting()) {
        const draft = { name: nameInput.value, description: description.value };
        newProjectDraft = draft;
        if (createPending.arm({ draft }, projectAct('create', { name, description: description.value.trim() }))) { btn.disabled = true; btn.textContent = 'Creating…'; }
      }
    };
  } else {
    const hue = hueOf(p.id);
    const ws = (S.workspace && S.workspaceFor === p.id) ? S.workspace : null;

    if (!ws) query('workspace', { name: p.id });
    const outcome = (p.contract && p.contract.outcome) || p.description || '';
    sp.innerHTML = '<div class="projects__page" style="--hue:' + hue + '">' +
      '<header class="projects__head"><h2 class="projects__title">' + esc(p.name) +
      (p.state !== 'open' ? ' <span class="chip">' + esc(p.state) + '</span>' : '') + '</h2>' +
      '<div class="projects__acts">' +
      (p.active
        ? '<span class="projects__working">' + esc(named(W.inFocus, W.inFocusUnnamed)) + '</span>' +
          '<button class="c-button c-button--quiet" id="proj-leave">' + esc(W.workOutside) + '</button>' +
          '<button class="c-button c-button--primary" id="proj-work">' + esc(named(W.continueWith, W.continueUnnamed)) + '</button>'
        : (p.state === 'open'
          ? '<button class="c-button c-button--primary" id="proj-work">' + esc(named(W.continueWith, W.continueUnnamed)) + '</button>'
          : '<button class="c-button" id="proj-reopen">' + (p.state === 'archived' ? esc(W.unarchive) : 'Reopen project') + '</button>')) +
      '</div></header>' +
      '<p class="projects__said" role="status" id="proj-continue-said">' + esc(continueSaid(p)) + '</p>' +
      (outcome ? '<p class="projects__after"><span class="projects__soft">' + esc(W.after) + '</span> ' + esc(outcome) + '</p>' : '') +
      (agreedCaption(p) ? '<p class="projects__meta agreed">' + esc(agreedCaption(p)) + '</p>' : '<p class="projects__meta">' + esc(folderCaption(p)) + '</p>') +
      progressCard(p) + filesHTML(p, ws) + workHTML(ws) + agreementHTML(p, ws) +
      '</div>' +
      '<div id="projects-delivered"></div>';
    renderDelivered(sp.querySelector('#projects-delivered'), true, gradeFocus);
    const wbtn = $('proj-work');
    if (wbtn) wbtn.onclick = () => continueWith(p);

    const lbtn = $('proj-leave');
    if (lbtn) lbtn.onclick = () => stopWorkingInProject();
    const rbtn = $('proj-reopen');
    if (rbtn) rbtn.onclick = () => projectAct(p.state === 'archived' ? 'unarchive' : 'reopen', { id: p.id });
    $('focus-edit').oninput = () => { S.focusDraft = { id: p.id, val: $('focus-edit').value }; };
    for (const id of ['ct-outcome', 'ct-accept', 'ct-constr', 'ct-parent']) {
      $(id).oninput = $(id).onchange = () => { S.contractDraft = captureContractDraft(p.id); };
    }
    $('focus-save').onclick = () => {
      const val = $('focus-edit').value;

      if (focusPending.waiting()) return;
      focusPending.arm({ draft: { id: p.id, val } }, projectAct('update', { id: p.id, focus: val }));
      S.focusDraft = { id: p.id, val: val };
    };
    const csave = $('ct-save');
    if (csave) csave.onclick = () => {
      if (contractPending.waiting()) return;

      const list = (id) => ($(id).value || '').split('\n').map((x) => x.trim()).filter((x) => x !== '');
      const parentSel = $('ct-parent');
      contractPending.arm({ draft: captureContractDraft(p.id) }, projectAct('update', {
        id: p.id,
        contract: {
          outcome: ($('ct-outcome').value || '').trim(),
          acceptance: list('ct-accept'),
          constraints: list('ct-constr'),
        },

        parent: parentSel ? parentSel.value : '',
      }));

      S.contractDraft = captureContractDraft(p.id);
    };
    $('proj-state').onclick = () => projectAct(p.state === 'open' ? 'close' : (p.state === 'archived' ? 'unarchive' : 'reopen'), { id: p.id });
    const abtn = $('proj-archive');
    if (abtn) abtn.onclick = () => projectAct('archive', { id: p.id });
    const delbtn = $('proj-delete');
    if (delbtn) delbtn.onclick = () => {
      if (!window.confirm('Delete \u201c' + p.name + '\u201d permanently? Its directory moves to the projects root\u2019s .trash \u2014 nothing is destroyed, but the project is gone from here.')) return;
      S.viewedProject = '';
      projectAct('delete', { id: p.id });
    };
    const wrf = $('ws-refresh');
    if (wrf) wrf.onclick = () => query('workspace', { name: p.id });
    const frf = $('fl-refresh');
    if (frf) frf.onclick = () => query('workspace', { name: p.id });
    sp.querySelectorAll('.fl-file').forEach(row => {
      row.onclick = () => aiiOpenFile(p.id, row.dataset.name);
    });
    continueArrived(p);
  }
  renderDock();
}

export function dockFilterOf(p) {
  const f = (S.dockFilter || '').trim().toLowerCase();
  if (!f) return true;
  return (p.id || '').toLowerCase().indexOf(f) >= 0 || (p.name || '').toLowerCase().indexOf(f) >= 0;
}

export function renderDock() {
  const dock = $('dock');
  const viewed = viewedOf();

  const viewedID = viewed ? viewed.id : '';
  const archivedN = S.projects.filter(p => p.state === 'archived').length;
  const list = S.projects.filter(p => (S.showArchived || p.state !== 'archived') && dockFilterOf(p));
  const empty = '<div class="dock-empty">' + (S.projects.length
    ? (archivedN === S.projects.length && !S.showArchived ? 'all projects are archived' : 'no projects match')
    : 'no projects yet') + '</div>';
  dock.innerHTML = '<h2 class="projects__dock-title">' + esc(W.title) + '</h2>' + (list.length ? list.map(pp => {
    const marks = (pp.active ? ' active' : '') + (pp.id === viewedID ? ' viewing' : '');
    return '<button type="button" class="dock-item' + marks + (pp.state === 'closed' || pp.state === 'archived' ? ' closed' : '') + '" data-id="' + esc(pp.id) + '"' +
      (pp.id === viewedID ? ' aria-current="page"' : '') + '><span class="b-name">' + esc(pp.name) + '</span>' +
      (pp.active ? '<span class="projects__soft">· working here</span>' : pp.state !== 'open' ? '<span class="projects__soft">· ' + esc(pp.state) + '</span>' : '') + '</button>';
  }).join('') : empty) +
  '<button type="button" class="dock-item dock-new' + (viewedID === '' && S.viewedProject === '' ? ' viewing' : '') + '" id="dock-new"><span class="b-name">' + esc(W.newProject) + '</span></button>' +
  (archivedN ? '<button type="button" class="dock-item dock-new" id="dock-archived"><span class="b-name">' + (S.showArchived ? 'Hide' : 'Show') + ' archived (' + archivedN + ')</span></button>' : '');

  dock.querySelectorAll('.dock-item[data-id]').forEach(el => { el.onclick = () => viewProject(el.dataset.id); });

  $('dock-new').onclick = () => { S.viewedProject = ''; renderProjects(); };
  const ab = $('dock-archived');
  if (ab) ab.onclick = () => { S.showArchived = !S.showArchived; renderProjects(); };
}

const named = (withName, unnamed) => { const n = identityName(); return n ? fill(withName, { name: n }) : unnamed; };
const folderCaption = p => (p && p.dir ? fill(W.folderAlone, { path: p.dir }) : '');

const continuePending = pendingSlot();
let continueSaidText = '', continueFor = '';
function continueWith(p) {
  if (continuePending.waiting()) return;
  if (p.active) { go('chat'); return; }
  continueFor = p.id;
  continueSaidText = continuePending.arm({ id: p.id }, projectAct('select', { id: p.id })) ? C.waiting : C.notConnected;
  renderProjects();
}
function continueSaid(p) { return continueFor === p.id ? continueSaidText : ''; }
function continueAnswered(requestID) {
  const asked = continuePending.claim(requestID);
  if (!asked) return false;
  continueSaidText = '';
  const here = S.view === 'projects' && viewedOf()?.id === asked.id;
  if (here && S.activeProject && S.activeProject.id === asked.id) go('chat');
  return true;
}
const redraw = () => { if (S.view === 'projects') renderProjects(); };
function continueRefused(requestID) {
  if (!continuePending.claim(requestID)) return false;
  continueSaidText = '';
  redraw();
  return true;
}
function continueArrived(p) { if (!continuePending.waiting() && continueFor === p.id && continueSaidText === C.waiting) continueSaidText = ''; }

function agreedCaption(p) {
  const c = (p && p.contract) || {};
  if (!c.agreed_by || !c.agreed_at) return '';
  const when = new Date(c.agreed_at);
  if (isNaN(when.getTime())) return '';
  const months = ['January','February','March','April','May','June',
    'July','August','September','October','November','December'];
  const date = when.getDate() + ' ' + months[when.getMonth()];
  const who = c.agreed_by === 'operator'
    ? 'you'
    : (S.identityExists && S.stats && S.stats.name && S.stats.name !== 'Unnamed' ? S.stats.name : 'your identity');
  const folder = p && p.dir ? ' \u00b7 the project\u2019s own folder: ' + p.dir : '';
  return 'Agreed by ' + who + ' on ' + date + folder;
}

const PROG_STATES = ['verified', 'supported', 'open', 'unsupported', 'waived'];
const PROG_HINT = {
  verified: W.meansVerified,
  supported: W.meansSupported,
  open: W.meansOpen,
  unsupported: W.meansUnsupported,
  waived: W.meansWaived,
};

function identityName() {
  return S.identityExists && S.stats && S.stats.name && S.stats.name !== 'Unnamed' ? S.stats.name : '';
}

function progLabel(it) {
  switch (it.state) {
    case 'verified': return W.stateVerified;
    case 'supported': {
      const n = identityName();
      return n ? fill(W.stateSupported, { name: n }) : W.stateSupportedNoName;
    }
    case 'waived': return W.stateWaived;
    case 'unsupported': return W.stateUnsupported;
    case 'open':
    default: return W.stateOpen;
  }
}

function progCountWord(s) {
  const w = progLabel({ state: s });
  return s === 'supported' ? w : w.toLowerCase();
}

const PROG_WHY_UNSUPPORTED = {
  partial_or_mixed: W.partialOrMixed,
  external_effect_unknown: W.effectUnknown,
  rejected_before_effect: W.refusedBefore,
  not_run: W.neverRun,
};

function evidenceWords(ref) {
  if (!/^ws_/.test(ref)) return fill(W.evidence, { ref });
  const w = ((S.workspace && S.workspace.work) || []).find(x => x.id === ref);
  return w && w.description ? fill(W.evidenceSession, { session: w.description }) : W.evidenceASession;
}

function progWhy(it) {
  const ref = it.ref ? evidenceWords(it.ref) : '';
  let says = '';
  switch (it.state) {
    case 'verified': {
      const how = it.class === 'host_receipted' ? W.receipt
        : it.class === 'locally_verified' ? W.checkedLocally : '';
      if (ref) return how ? ref + ', ' + how : ref;
      return how ? how.charAt(0).toUpperCase() + how.slice(1) : '';
    }
    case 'supported':
      says = it.class === 'completed_locally'
        ? W.seenDone
        : it.class === 'worker_report_only' ? W.helperReported : '';
      break;
    case 'unsupported':
      says = Object.prototype.hasOwnProperty.call(PROG_WHY_UNSUPPORTED, it.class || '')
        ? PROG_WHY_UNSUPPORTED[it.class]
        : (it.class ? fill(W.unknownClass, { class: it.class }) : '');
      break;
    case 'waived':
      says = W.setAside;
      break;
    case 'open':
    default:
      says = it.stale
        ? W.recheck
        : W.waitsOnEvidence;
  }
  if (ref && says) return ref + '. ' + says;
  return ref || says;
}

function progNext(pr, items, next) {
  const counts = pr.counts || {};
  const blocking = ['open', 'supported', 'unsupported']
    .reduce((n, s) => n + (parseInt(counts[s], 10) || 0), 0);
  const cannot = blocking > 0
    ? fill(W.cannotClose, { reason: blocking === 1 ? W.blockingOne : fill(W.blocking, { n: blocking }) })
    : W.cannotCloseBare;
  const open = '<p class="prog-next projects__next">';
  const label = '<b>' + esc(W.next) + '</b> ';
  if (next >= 0) {
    const it = items.find((x) => x.index === next) || items[next];
    const text = String((it && it.text) || '').trim();
    const stop = /[.?!]$/.test(text) ? '' : '.';
    return open + label + esc(text + stop) +
      (pr.closure_allowed ? '' : ' <span class="projects__soft">' + esc(cannot) + '</span>') + '</p>';
  }
  if (pr.closure_allowed) {
    return open + label + esc(W.nothing) + ' <span class="projects__soft">' + esc(W.canClose) + '</span></p>';
  }
  return open + '<span class="projects__soft">' + esc(cannot) + '</span></p>';
}

const progHead = (summary) => '<section class="card projects__part projects__done" aria-labelledby="pj-done"><div class="projects__top"><h3 id="pj-done" class="projects__h3">' + esc(W.done) + '</h3>' +
  (summary ? '<span class="projects__counts" role="status">' + summary + '</span>' : '') + '</div>';

function progressCard(p) {
  const pr = p && p.progress;
  if (!pr) return progHead('') + '<p class="projects__hint">' + esc(W.notRead) + '</p></section>';
  if (!pr.has_criteria) return progHead('') + '<p class="projects__hint">' + esc(W.noCriteria) + '</p></section>';
  const items = pr.items || [];
  const counts = pr.counts || {};
  const parts = PROG_STATES.filter((s) => counts[s])
    .map((s) => '<span class="projects__count">' + esc(counts[s] + ' ' + progCountWord(s)) + '</span>');
  const summary = parts.length ? parts.join(' \u00b7 ') : esc(items.length === 1 ? W.itemOne : fill(W.items, { n: items.length }));
  const nextRaw = parseInt(pr.next_index, 10);
  const next = (!isNaN(nextRaw) && nextRaw >= 0 && nextRaw < items.length) ? nextRaw : -1;

  const rows = items.map((it) => {
    const st = it.state || 'open';
    const hint = PROG_HINT[st] || st;
    const why = progWhy(it);
    return '<li class="projects__claim is-' + esc(st) + '">' +
      '<span class="chip projects__state" title="' + esc(hint) + '">' + esc(progLabel(it)) + '</span> ' +
      '<span class="projects__claim-words"><span>' + esc(it.text) + '</span>' +
      (why ? '<span class="prog-why projects__hint">' + esc(why) + '</span>' : '') + '</span></li>';
  }).join('');

  return progHead(summary) +
    '<ul class="projects__rows" aria-label="' + esc(W.claimsLabel) + '">' + rows + '</ul>' +
    progNext(pr, items, next) + '</section>';
}

export function renderProjTab(p, ws, tab) {
  if (tab === 'overview') return agreementHTML(p, ws) + progressCard(p);
  if (tab === 'files') return filesHTML(p, ws);
  if (tab === 'work') return workHTML(ws);
  return '';
}

const part = (id, title, hint, body) => '<section class="projects__part" aria-labelledby="' + id + '"' + (hint ? ' aria-describedby="' + id + '-hint"' : '') + '>' +
  '<h3 id="' + id + '" class="projects__h3">' + title + '</h3>' + (hint ? '<p id="' + id + '-hint" class="projects__hint">' + hint + '</p>' : '') + body + '</section>';

function filesHTML(p, ws) {
  if (!ws) return part('pj-files', esc(W.files), '', '<p class="projects__hint">Loading directory…</p>');
  const files = ws.files || [];
  if (!files.length) return part('pj-files', esc(W.files), '', '<p class="projects__hint">The directory is empty.</p>');
  return part('pj-files', esc(W.files), ws.files_capped && ws.files_total
    ? esc('Showing ' + files.length + ' of ' + ws.files_total + ' entries — capped, the full list lives in the directory.') : '',
    '<table class="ws-table fl-open projects__files"><thead><tr><th>Name</th><th>Size</th></tr></thead><tbody>' +
    files.map(f => '<tr class="' + (f.dir ? 'fl-dir' : 'fl-file') + '"' + (f.dir ? '' : ' data-name="' + esc(f.name) + '"') + '><td>' + (f.dir ? esc(f.name) + '/' : '<button type="button" class="fl-name">' + esc(f.name) + '</button>') + '</td>' +
      '<td>' + (f.dir ? '—' : bytes(f.size)) + '</td></tr>').join('') +
    '</tbody></table><div class="projects__buttons"><button class="c-button c-button--quiet" id="fl-refresh">' + esc(W.refresh) + '</button></div>');
}

function workHTML(ws) {
  const head = esc(W.work), hint = esc(W.workHint);
  if (!ws) return part('pj-work', head, hint, '<p class="projects__hint">Loading attributed work…</p>');
  const work = ws.work || [];
  if (!work.length) return part('pj-work', head, hint, '<p class="projects__hint">No work sessions attributed to this project yet.</p>');
  return part('pj-work', head, hint,
    '<ul class="projects__rows">' + work.map(w => '<li class="ws-item projects__row"><span class="ws-desc">' + esc(w.description || w.id) + '</span>' +
      '<span class="ws-meta"><span class="chip">' + esc(w.status) + '</span></span>' +
      '<span class="ws-verdict' + (w.result ? '' : ' none') + '">' + esc(w.result || 'no outcome recorded yet') + '</span>' + recordedHTML(w) + '</li>').join('') + '</ul>' +
    '<div class="projects__buttons"><button class="c-button c-button--quiet" id="ws-refresh">' + esc(W.refresh) + '</button></div>');
}

function agreementHTML(p, ws) {
  const workCount = ws ? (ws.work || []).length : 0;
  const fileCount = ws ? (ws.files || []).length : 0;
  const fileLine = ws && ws.files_capped && ws.files_total
    ? 'showing ' + fileCount + ' of ' + ws.files_total + ' entries — capped, the full list lives in the directory'
    : fileCount + ' entr' + (fileCount === 1 ? 'y' : 'ies') + ' in the directory';
  const workLine = ws && ws.work_capped
    ? workCount + ' work sessions — capped, older sessions live in the ledger'
    : workCount + ' work session' + (workCount === 1 ? '' : 's');
  const draft = (S.focusDraft && S.focusDraft.id === p.id) ? S.focusDraft.val : null;
  const c = p.contract || {};
  const cd = (S.contractDraft && S.contractDraft.id === p.id) ? S.contractDraft : null;
  const draftParent = (cd && cd.parent !== null && cd.parent !== undefined) ? cd.parent : (p.parent || '');
  const lines = (a) => (a && a.length) ? a.join('\n') : '';
  const parentOpts = ['<option value="">' + esc(W.partOfNone) + '</option>'].concat(
    (S.projects || []).filter((o) => o.id !== p.id).map((o) =>
      '<option value="' + esc(o.id) + '"' + (o.id === draftParent ? ' selected' : '') + '>' + esc(o.name) + '</option>')).join('');
  const field = (id, label, hint, rows, value) => '<label class="projects__label" for="' + id + '">' + esc(label) + '</label>' +
    (hint ? '<p class="projects__hint">' + esc(hint) + '</p>' : '') + '<textarea id="' + id + '" rows="' + rows + '">' + esc(value) + '</textarea>';
  return part('pj-agreement', esc(W.editAgreement), esc(named(W.bothHands, W.bothHandsUnnamed)),
      '<div class="projects__form">' +
      field('ct-outcome', W.outcome, W.outcomeHint, 2, cd ? cd.outcome : (c.outcome || '')) +
      field('ct-accept', W.acceptance, W.acceptanceHint, 3, cd ? cd.acceptance : lines(c.acceptance)) +
      field('ct-constr', W.constraints, W.constraintsHint, 2, cd ? cd.constraints : lines(c.constraints)) +
      '<label class="projects__label" for="ct-parent">' + esc(W.partOf) + '</label><select id="ct-parent">' + parentOpts + '</select>' +
      '<div class="savebar projects__buttons"><button class="c-button c-button--primary" id="ct-save">' + esc(W.saveAgreement) + '</button>' +
      '<span class="savenote" id="ct-note">saved</span></div>' +
      '<p class="projects__hint">Saving replaces the whole contract with what is written here. Status is never stored — it is derived from evidence.</p></div>') +
    part('pj-focus', esc(W.focus), esc(W.focusHint),
      '<div class="projects__form"><textarea id="focus-edit" rows="3" aria-labelledby="pj-focus">' + esc(draft !== null ? draft : (p.focus || '')) + '</textarea>' +
      '<div class="savebar projects__buttons"><button class="c-button c-button--primary" id="focus-save">' + esc(W.saveFocus) + '</button>' +
      '<button class="c-button" id="proj-state">' + esc(p.state === 'open' ? W.close : (p.state === 'archived' ? W.unarchive : 'Reopen project')) + '</button>' +
      (p.state === 'archived'
        ? '<button class="c-button c-button--danger" id="proj-delete">' + esc(W.deleteForever) + '</button>'
        : '<button class="c-button" id="proj-archive">' + esc(W.archive) + '</button>') +
      '<span class="savenote" id="focus-note">saved</span></div></div>' +
      '<p class="projects__hint"><span>' + workLine + '</span> · <span>' + fileLine + '</span>. ' +
      'Chat carries this project while it is focused — turns and work sessions are stamped with it. Files live in the directory above, reachable by the identity\'s own tools.</p>');
}

let viewerOpener = null;
export function aiiOpenFile(projectId, relPath) {
  const seg = String(relPath || '').split('/').map(encodeURIComponent).join('/');
  const url = '/p/' + encodeURIComponent(projectId) + '/' + seg;
  const opener = $('file-viewer') ? viewerOpener : { el: document.activeElement, name: String(relPath || '') };
  closeFileViewer(false);
  viewerOpener = opener;
  const ov = document.createElement('dialog');
  ov.id = 'file-viewer';
  ov.setAttribute('aria-labelledby', 'fv-title');
  ov.innerHTML = '<div class="fv-card">' +
    '<div class="fv-head"><span id="fv-title">' + esc(relPath) + '</span>' +
    '<button type="button" class="btn ghost sm" id="file-viewer-close">Close</button></div>' +
    '<div class="fv-body" id="fv-body"><div class="dim-note">Loading…</div></div></div>';
  document.body.appendChild(ov);
  $('file-viewer-close').onclick = () => closeFileViewer();
  ov.onclick = (ev) => { if (ev.target === ov) closeFileViewer(); };
  ov.addEventListener('cancel', (ev) => { ev.preventDefault(); closeFileViewer(); });
  if (typeof ov.showModal === 'function') ov.showModal(); else ov.setAttribute('open', '');

  const body = ov.querySelector('#fv-body');
  const mine = () => document.getElementById('file-viewer') === ov;
  const trap = (ev) => {
    if (!mine() || ov.contains(ev.target)) return;
    const back = ov.querySelector('#file-viewer-close');
    if (back && document.activeElement !== back) back.focus();
  };
  document.addEventListener('focusin', trap, true);
  ov.__aiiTrap = trap;
  signedFetch(url).then(r => {
    if (!mine()) return null;
    if (!r.ok) throw new Error('HTTP ' + r.status);
    const ct = (r.headers.get('content-type') || '').split(';')[0].trim().toLowerCase();
    if (ct.indexOf('image/') === 0) {
      return ticketed('GET', url).then(address => { if (mine()) body.innerHTML = '<img src="' + esc(address) + '" alt="' + esc(relPath) + '">'; });
    }
    return r.text().then(text => { if (mine()) body.textContent = text; });
  }).catch(err => {
    if (!mine()) return;
    body.textContent = 'Could not open: ' + (err && err.message ? err.message : 'unknown error');
  });
}

export function closeFileViewer(restore = true) {
  const ov = document.getElementById('file-viewer');
  if (!ov) return;
  if (ov.__aiiTrap) {
    document.removeEventListener('focusin', ov.__aiiTrap, true);
    ov.__aiiTrap = null;
  }
  if (ov.open && typeof ov.close === 'function') ov.close();
  ov.remove();
  const opener = viewerOpener;
  viewerOpener = null;
  if (!restore || !opener) return;
  const row = [...document.querySelectorAll('#proj-space .fl-file')].find(r => r.dataset.name === opener.name);
  const back = (opener.el && opener.el.isConnected && opener.el !== document.body) ? opener.el
    : (row && row.querySelector('.fl-name')) || $('fl-refresh');
  if (back) back.focus();
}
if (typeof document !== 'undefined' && !window.__aiiFvKey) {
  window.__aiiFvKey = true;
  document.addEventListener('keydown', (ev) => {
    if (ev.key === 'Escape') closeFileViewer();
  });
}
