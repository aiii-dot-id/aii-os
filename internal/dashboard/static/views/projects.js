
import { S } from '../state.js';
import { $, esc, hueOf } from '../util.js';
import { send, query } from '../ws.js';
import { go } from '../app.js';
import { pendingSlot } from '../pending.js';

export const createPending = pendingSlot();
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
  return !!createPending.claim(requestID);
}

export function acceptCreate(requestID, prevIDs) {
  if (!createPending.claim(requestID)) return '';
  return newlyCreatedID(prevIDs, S.projects);
}
export function acceptFocusSave(requestID) {
  return !!focusPending.claim(requestID);
}
export function acceptContractSave(requestID) {
  if (!contractPending.claim(requestID)) return false;

  S.contractDraft = null;
  return true;
}

export function rejectFocusSave(requestID) {
  return !!focusPending.claim(requestID);
}
export function rejectContractSave(requestID) {
  return !!contractPending.claim(requestID);
}

export function projectsConnectionLost() {
  const create = createPending.drop();
  const focus = focusPending.drop();
  const contract = contractPending.drop();
  return !!(create || focus || contract);
}

function viewedOf() {
  const id = (S.viewedProject === null) ? (S.activeProject ? S.activeProject.id : '') : S.viewedProject;
  if (!id) return null;
  return S.projects.find(pp => pp.id === id) || null;
}

function emptyProjectsHTML() {
  return '<div class="empty" style="padding-top:60px">No project focused.<br>Pick a bubble below, or create one — a project is a durable workroom you and ' +
    esc(S.identityExists ? S.stats.name : 'your identity') + ' share.</div>' +
    '<div class="card" style="max-width:430px;margin:18px auto"><h3>NEW PROJECT</h3>' +
    '<input type="text" id="np-name" placeholder="Name">' +
    '<label class="f">WHAT IS IT?</label><textarea id="np-desc" rows="3"></textarea>' +
    '<div class="savebar"><button class="btn" id="np-create">Create</button></div></div>';
}
export function renderProjPill() {
  const el = $('pill-proj');
  if (S.activeProject) {
    el.style.display = '';
    el.innerHTML = '&#9678; <b>' + esc(S.activeProject.name) + '</b>';
    el.style.borderColor = 'hsl(' + hueOf(S.activeProject.id) + ' 70% 60% / .55)';
  } else el.style.display = 'none';
}
export function renderProjects() {
  const sp = $('proj-space');
  const p = viewedOf();

  if (p) document.title = p.name + ' — AII OS';

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
    const btn = $('np-create');
    if (btn) btn.onclick = () => {
      const name = $('np-name').value.trim();

      if (name && !createPending.waiting()) {
        createPending.arm({}, projectAct('create', { name: name, description: $('np-desc').value.trim() }));
      }
    };
  } else {
    const hue = hueOf(p.id);
    const ws = (S.workspace && S.workspaceFor === p.id) ? S.workspace : null;

    if (!ws) query('workspace', { name: p.id });
    const tab = S.projTab || 'overview';

    const tabs = [
      ['overview', 'Overview'],
      ['files', 'Files'],
      ['work', 'Work']
    ];
    sp.innerHTML = '<div style="--hue:' + hue + '">' +
      '<div class="proj-head"><h2><span class="dot"></span>' + esc(p.name) + '</h2>' +
      '<span class="chip ' + (p.state === 'open' ? 'active' : '') + '">' + esc(p.state) + '</span>' +

      (p.active
        ? '<span class="chip working">&#9678; working here</span>' +
          '<button class="btn ghost sm" id="proj-leave">Work outside projects</button>'
        : (p.state === 'open'
          ? '<button class="btn ghost sm" id="proj-work">Work in this project</button>'
          : '<button class="btn ghost sm" id="proj-reopen">' + (p.state === 'archived' ? 'Unarchive' : 'Reopen project') + '</button>')) +
      '</div>' +
      '<div class="proj-dir">' + esc(p.dir) + '</div>' +
      '<div class="proj-desc">' + esc(p.description || '') + '</div>' +
      '<div class="tabbar">' + tabs.map(t =>
        '<div class="tab' + (tab === t[0] ? ' sel' : '') + '" data-tab="' + t[0] + '">' + t[1] + '</div>').join('') + '</div>' +
      renderProjTab(p, ws, tab) +
      '</div>';
    sp.querySelectorAll('.tab').forEach(el => { el.onclick = () => { S.projTab = el.dataset.tab; renderProjects(); }; });
    const wbtn = $('proj-work');
    if (wbtn) wbtn.onclick = () => workInProject(p.id);

    const lbtn = $('proj-leave');
    if (lbtn) lbtn.onclick = () => projectAct('deselect');
    const rbtn = $('proj-reopen');
    if (rbtn) rbtn.onclick = () => projectAct(p.state === 'archived' ? 'unarchive' : 'reopen', { id: p.id });
    if (tab === 'overview') {
      $('focus-save').onclick = () => {
        const val = $('focus-edit').value;

        focusPending.arm({}, projectAct('update', { id: p.id, focus: val }));
        S.focusDraft = { id: p.id, val: val };
      };
      const csave = $('ct-save');
      if (csave) csave.onclick = () => {
        if (contractPending.waiting()) return;

        const list = (id) => ($(id).value || '').split('\n').map((x) => x.trim()).filter((x) => x !== '');
        const parentSel = $('ct-parent');
        contractPending.arm({}, projectAct('update', {
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
      const dbtn = $('proj-deselect');
      if (dbtn) dbtn.onclick = () => stopWorkingInProject();
    }
    if (tab === 'work') {
      const rf = $('ws-refresh');
      if (rf) rf.onclick = () => query('workspace', { name: p.id });
    }
    if (tab === 'files') {
      const rf = $('fl-refresh');
      if (rf) rf.onclick = () => query('workspace', { name: p.id });

      sp.querySelectorAll('.fl-file').forEach(row => {
        row.onclick = () => aiiOpenFile(p.id, row.dataset.name);
      });
    }
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
  // Archived projects are kept out of the way, behind one toggle.
  const archivedN = S.projects.filter(p => p.state === 'archived').length;
  const list = S.projects.filter(p => (S.showArchived || p.state !== 'archived') && dockFilterOf(p));
  const empty = '<div class="dock-empty">' + (S.projects.length
    ? (archivedN === S.projects.length && !S.showArchived ? 'all projects are archived' : 'no projects match')
    : 'no projects yet') + '</div>';
  dock.innerHTML = (list.length ? list.map(pp => {
    const initials = pp.name.split(/\s+/).map(w => w[0] || '').join('').slice(0, 2).toUpperCase();
    const marks = (pp.active ? ' active' : '') + (pp.id === viewedID ? ' viewing' : '');
    return '<div class="dock-item' + marks + '" data-id="' + esc(pp.id) + '" style="--hue:' + hueOf(pp.id) + '">' +
    '<div class="bubble' + marks + (pp.state === 'closed' || pp.state === 'archived' ? ' closed' : '') + '" title="' + esc(pp.name) + (pp.state === 'archived' ? ' (archived)' : '') +
    (pp.active ? ' — the identity is working here' : '') + '">' + esc(initials) + '</div>' +
    '<div class="b-name">' + esc(pp.name) + '</div></div>';
  }).join('') : empty) +
  '<div class="dock-item' + (viewedID === '' && S.viewedProject === '' ? ' viewing' : '') +
  '" id="dock-new"><div class="bubble new" title="New project">+</div><div class="b-name">new</div></div>' +
  (archivedN ? '<div class="dock-item" id="dock-archived" title="' + (S.showArchived ? 'hide' : 'show') + ' archived projects"><div class="bubble new">' + (S.showArchived ? '&#8722;' : '&#8801;') + '</div><div class="b-name">archived ' + archivedN + '</div></div>' : '');

  dock.querySelectorAll('.dock-item[data-id]').forEach(el => { el.onclick = () => viewProject(el.dataset.id); });

  $('dock-new').onclick = () => { S.viewedProject = ''; renderProjects(); };
  const ab = $('dock-archived');
  if (ab) ab.onclick = () => { S.showArchived = !S.showArchived; renderProjects(); };
}

export function renderProjTab(p, ws, tab) {
  if (tab === 'overview') {
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

    const parentOpts = ['<option value="">— none —</option>'].concat(
      (S.projects || []).filter((o) => o.id !== p.id).map((o) =>
        '<option value="' + esc(o.id) + '"' + (o.id === draftParent ? ' selected' : '') + '>' + esc(o.name) + '</option>')).join('');
    return '<div class="card"><h3>CONTRACT — what is sought, and what would confirm it</h3>' +
      '<label class="f">OUTCOME</label>' +
      '<textarea id="ct-outcome" rows="2" placeholder="What is this project pursuing?">' + esc(cd ? cd.outcome : (c.outcome || '')) + '</textarea>' +
      '<label class="f">ACCEPTANCE — one per line, each something evidence could settle</label>' +
      '<textarea id="ct-accept" rows="3" placeholder="What would confirm the outcome?">' + esc(cd ? cd.acceptance : lines(c.acceptance)) + '</textarea>' +
      '<label class="f">CONSTRAINTS — one per line</label>' +
      '<textarea id="ct-constr" rows="2" placeholder="What bounds how it may be reached?">' + esc(cd ? cd.constraints : lines(c.constraints)) + '</textarea>' +
      '<label class="f">PART OF</label><select id="ct-parent">' + parentOpts + '</select>' +
      '<div class="savebar"><button class="btn" id="ct-save">Save contract</button>' +
      '<span class="savenote" id="ct-note">saved</span></div>' +
      '<div style="color:var(--dim);font-size:13px;line-height:1.6">Saving replaces the whole contract with what is written here. Status is never stored — it is derived from evidence.</div></div>' +
      '<div class="card"><h3>FOCUS — what is happening here right now</h3>' +
      '<textarea id="focus-edit" rows="3" placeholder="Re-seeds the working state when this project is selected again…">' + esc(draft !== null ? draft : (p.focus || '')) + '</textarea>' +
      '<div class="savebar"><button class="btn" id="focus-save">Save focus</button>' +

      ((S.activeProject && S.activeProject.id === p.id)
        ? '<button class="btn ghost" id="proj-deselect">Work outside this project</button>'
        : '') +
      '<button class="btn ghost" id="proj-state">' + (p.state === 'open' ? 'Close project' : (p.state === 'archived' ? 'Unarchive' : 'Reopen')) + '</button>' +
      (p.state === 'archived'
        ? '<button class="btn ghost" id="proj-delete">Delete permanently</button>'
        : '<button class="btn ghost" id="proj-archive">Archive</button>') +
      '<span class="savenote" id="focus-note">saved</span></div></div>' +
      '<div class="card"><h3>THIS WORKROOM</h3>' +
      '<div class="ws-facts"><div>' + workLine + '</div>' +
      '<div>' + fileLine + '</div></div>' +
      '<div style="color:var(--dim);font-size:13px;line-height:1.6">Chat carries this project while it is focused — turns and work sessions are stamped with it. Files live in the directory above, reachable by the identity\'s own tools.</div></div>';
  }
  if (tab === 'files') {
    if (!ws) return '<div class="card"><h3>FILES</h3><div class="dim-note">Loading directory…</div></div>';
    const files = ws.files || [];
    if (!files.length) return '<div class="card"><h3>FILES</h3><div class="dim-note">The directory is empty.</div></div>';

    return '<div class="card"><h3>FILES — click a file to open it</h3>' +
      '<div class="savebar"><button class="btn ghost" id="fl-refresh">Refresh</button></div>' +
      '<table class="ws-table fl-open"><thead><tr><th>Name</th><th>Size</th></tr></thead><tbody>' +
      files.map(f => '<tr class="' + (f.dir ? 'fl-dir' : 'fl-file') + '"' + (f.dir ? '' : ' data-name="' + esc(f.name) + '"') + '><td>' + (f.dir ? '&#128193; ' : '&#128196; ') + esc(f.name) + '</td><td>' +
        (f.dir ? '—' : fmtSize(f.size)) + '</td></tr>').join('') +
      '</tbody></table></div>';
  }
  if (tab === 'work') {
    if (!ws) return '<div class="card"><h3>WORK</h3><div class="dim-note">Loading attributed work…</div></div>';
    const work = ws.work || [];
    if (!work.length) return '<div class="card"><h3>WORK</h3><div class="dim-note">No work sessions attributed to this project yet.</div></div>';
    return '<div class="card"><h3>WORK — sessions stamped with this project</h3>' +
      '<div class="savebar"><button class="btn ghost" id="ws-refresh">Refresh</button></div>' +
      work.map(w => '<div class="ws-item"><div class="ws-desc">' + esc(w.description || w.id) + '</div>' +
        '<div class="ws-meta"><span class="chip">' + esc(w.status) + '</span></div>' +

        '<div class="ws-verdict' + (w.result ? '' : ' none') + '">' +
        esc(w.result || 'no outcome recorded yet') + '</div></div>').join('') +
      '</div>';
  }
  return '';
}

function fmtSize(n) {
  if (n < 1024) return n + ' B';
  if (n < 1024 * 1024) return (n / 1024).toFixed(1) + ' KB';
  return (n / (1024 * 1024)).toFixed(1) + ' MB';
}

export function aiiOpenFile(projectId, relPath) {
  const seg = String(relPath || '').split('/').map(encodeURIComponent).join('/');
  const url = '/p/' + encodeURIComponent(projectId) + '/' + seg;
  closeFileViewer();
  const ov = document.createElement('div');
  ov.id = 'file-viewer';
  ov.innerHTML = '<div class="fv-card" role="dialog" aria-modal="true">' +
    '<div class="fv-head"><span id="fv-title">' + esc(relPath) + '</span>' +
    '<button class="btn ghost sm" id="file-viewer-close">Close</button></div>' +
    '<div class="fv-body" id="fv-body"><div class="dim-note">Loading…</div></div></div>';
  document.body.appendChild(ov);
  $('file-viewer-close').onclick = closeFileViewer;
  ov.onclick = (ev) => { if (ev.target === ov) closeFileViewer(); };

  const body = ov.querySelector('#fv-body');
  const mine = () => document.getElementById('file-viewer') === ov;
  fetch(url).then(r => {
    if (!mine()) return null;
    if (!r.ok) throw new Error('HTTP ' + r.status);
    const ct = (r.headers.get('content-type') || '').split(';')[0].trim().toLowerCase();
    if (ct.indexOf('image/') === 0) {
      body.innerHTML = '<img src="' + esc(url) + '" alt="' + esc(relPath) + '">';
      return null;
    }
    return r.text().then(text => { if (mine()) body.textContent = text; });
  }).catch(err => {
    if (!mine()) return;
    body.textContent = 'Could not open: ' + (err && err.message ? err.message : 'unknown error');
  });
}

export function closeFileViewer() {
  const ov = document.getElementById('file-viewer');
  if (ov) ov.remove();
}
if (typeof document !== 'undefined' && !window.__aiiFvKey) {
  window.__aiiFvKey = true;
  document.addEventListener('keydown', (ev) => {
    if (ev.key === 'Escape') closeFileViewer();
  });
}
