/* views/projects.js — the projects surface (R62 workrooms): the
   focused-project page, the focus line editor, create/close/reopen,
   and the dock of bubbles + stagebar pill. R66: the PM-board default
   section (UI_FRAME.md §6/§7) — first co-build target; its truth
   already lives in the project directory, never in this module. */
import { S } from '../state.js';
import { $, esc, hueOf } from '../util.js';
import { send, query } from '../ws.js';
import { go } from '../app.js';
import { pendingSlot } from '../pending.js';

/* The two correlated waits this surface owns. They are slots rather than
   fields on S because a slot cannot be left out of the disconnect sweep —
   which is exactly how a create survived its own socket and left the
   button inert until a page reload. */
export const createPending = pendingSlot();
export const focusPending = pendingSlot();

/* Wire the dock filter once at module load. The input is static HTML;
   this is the only code that ever listens to it. Each keystroke
   re-renders ONLY the dock row (renderDock), never the page — the
   operator keeps their scroll and their page while the row narrows
   under them. */
const dockFilterInput = typeof document !== 'undefined' ? document.getElementById('dock-filter') : null;
if (dockFilterInput) dockFilterInput.addEventListener('input', () => {
  S.dockFilter = dockFilterInput.value;
  renderDock();
});

/* --- projects ----------------------------------------------- */
function projectAct(action, fields) { return send({ type: 'project', project: Object.assign({ action: action }, fields || {}) }); }
/* viewProject — the operator's gesture IS the commitment. Click a
   bubble, land on the project, the identity works there. Three
   reports (2026-08-27) read click-as-choose; the browse-only design
   shipped a page that sent nothing, felt dead, and left WORK empty
   because nothing had moved. One gesture, one meaning. */
export function viewProject(id) {
  projectAct('select', { id: id });
  S.viewedProject = id;
  S.projTab = 'overview';
  try { if (location.hash !== '#/projects/' + id) location.hash = '#/projects/' + id; } catch (err) {}
  if (S.view !== 'projects') go('projects'); else renderProjects();
}
/* workInProject — kept for callers that want the intent explicit;
   the click path no longer needs it, but tests and home cards may. */
export function workInProject(id) { projectAct('select', { id: id }); }
/* newlyCreatedID — which project did the operator just make? The only
   honest answer available on this side of the wire: the id that was not
   in the previous list. Name matching would pick the wrong twin, and
   recomputing the server's slug rule would duplicate a decision that
   isn't ours. Ambiguity returns '' — zero new ids (the create failed)
   and two-or-more (something else landed in the same payload) are both
   cases where moving the operator's page would be a guess. This is the
   rule that decides where the operator ends up standing, so it is a
   named function with a test rather than a clause inside a dispatch. */
export function newlyCreatedID(prevIDs, projects) {
  const fresh = (projects || []).filter(p => prevIDs.indexOf(p.id) < 0);
  return fresh.length === 1 ? fresh[0].id : '';
}
/* rejectCreate — the error wearing OUR create's request id disarms it
   (D72). Without this, a refused create left pendingCreate armed and
   the next unrelated projects payload moved the operator to whichever
   id it happened to carry. Returns true when it consumed the error. */
export function rejectCreate(requestID) {
  return !!createPending.claim(requestID);
}
/* acceptCreate / acceptFocusSave — ws.js asks THESE rather than reaching
   into the slots, the same shape rejectCreate already had. The frame
   dispatcher should not know how this surface remembers what it is
   waiting for, and a stubbed projects.js in a rig stays a few plain
   functions instead of having to imitate a slot. */
export function acceptCreate(requestID, prevIDs) {
  if (!createPending.claim(requestID)) return '';
  return newlyCreatedID(prevIDs, S.projects);
}
export function acceptFocusSave(requestID) {
  return !!focusPending.claim(requestID);
}
/* viewedOf — the project whose page is on screen. With nothing chosen
   this follows the active project, so the first paint is unchanged;
   once the operator picks, their pick holds. A stale id (renamed or
   gone) resolves to null and renders the no-project page rather than
   inventing content. */
function viewedOf() {
  const id = (S.viewedProject === null) ? (S.activeProject ? S.activeProject.id : '') : S.viewedProject;
  if (!id) return null;
  return S.projects.find(pp => pp.id === id) || null;
}
/* emptyProjectsHTML — the one source of the no-project page. Every
   render of the empty state derives from this function; a hand-copied
   variant anywhere else is a fork waiting to diverge (the panel
   signature lesson, applied by construction). */
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
  /* The tab title: go() sets the view's title on every navigation
     (the crumb line); a focused project refines it, name first
     because tabs truncate from the right. Ghosts resolve null, so an
     address naming nothing never titles the tab. Same fact viewedOf
     already resolved; no second source to fork. */
  if (p) document.title = p.name + ' — AII OS';
  /* The operator is typing a focus note. Any incoming message used to
     rebuild the whole page and replace the textarea with the SAVED
     text — destroying the draft mid-word. The render is skipped while
     an edit is live; the draft lives in S.focusDraft so tab switches
     and later renders restore it, and the save sends it. */
  const edit = document.activeElement && document.activeElement.id === 'focus-edit';
  if (edit && p) {
    S.focusDraft = { id: p.id, val: $('focus-edit').value };
    return; // the page on screen is newer than the data
  }
  if (!p) {
    S.focusDraft = null;
    /* A focused id that names no project is a ghost — the address
       bar claims a page that does not exist. The honest render is
       the empty state (derived from emptyProjectsHTML, never a
       hand copy) plus a banner that says so, and the address is
       retracted via replaceState (no history entry) so the URL
       stops lying once the truth is on screen. The retraction is
       gated: re-renders after the first must not fight a real
       navigation that changed the hash in the meantime. */
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
      /* Creating a project shows you that project. It does NOT move the
         identity into it — that stays one deliberate click. pendingCreate
         holds this create's request id so ws.js adopts the new project
         only from the answer wearing that id; the operator is never left
         editing the page they were on before, believing it is the one
         they just made. While armed, the button does not fire again — a
         second click is the same create twice (D72). */
      if (name && !createPending.waiting()) {
        createPending.arm({}, projectAct('create', { name: name, description: $('np-desc').value.trim() }));
      }
    };
  } else {
    const hue = hueOf(p.id);
    const ws = (S.workspace && S.workspaceFor === p.id) ? S.workspace : null;
    /* Re-query on entry and switch: a workspace older than the current
       focus is not rendered as truth, it is replaced. The reply
       re-renders via the ws.js dispatch. */
    if (!ws) query('workspace', { name: p.id });
    const tab = S.projTab || 'overview';
    /* Tabs: the three surfaces of one truth — the manifest (Overview),
       the directory (Files), and the attributed work (Work). One query
       feeds all three; the server assembles one typed snapshot. */
    const tabs = [
      ['overview', 'Overview'],
      ['files', 'Files'],
      ['work', 'Work']
    ];
    sp.innerHTML = '<div style="--hue:' + hue + '">' +
      '<div class="proj-head"><h2><span class="dot"></span>' + esc(p.name) + '</h2>' +
      '<span class="chip ' + (p.state === 'open' ? 'active' : '') + '">' + esc(p.state) + '</span>' +
      /* Where the identity is, stated on the page you are looking at —
         not inferred from the fact that you can see it. Viewing and
         working are now different things, so the page has to say which
         one this is. */
      /* Only an OPEN project offers the move — the backend refuses a
         closed one anyway (selectProject), so offering it was a button
         that could only ever error. Closed offers the honest next act:
         reopen (D73). */
      (p.active
        ? '<span class="chip working">&#9678; working here</span>'
        : (p.state === 'open'
          ? '<button class="btn ghost sm" id="proj-work">Work in this project</button>'
          : '<button class="btn ghost sm" id="proj-reopen">Reopen project</button>')) +
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
    const rbtn = $('proj-reopen');
    if (rbtn) rbtn.onclick = () => projectAct('reopen', { id: p.id });
    if (tab === 'overview') {
      $('focus-save').onclick = () => {
        const val = $('focus-edit').value;
        /* "saved" only appears when the server has acknowledged — the
           request-id handshake exists upstream (b037d80); a painted
           "saved" with no ack is a lie the operator caught once. The
           draft stays armed until the ack lands; the projects payload
           wearing this request id confirms the bytes and clears it. */
        focusPending.arm({}, projectAct('update', { id: p.id, focus: val }));
        S.focusDraft = { id: p.id, val: val };
      };
      $('proj-state').onclick = () => projectAct(p.state === 'open' ? 'close' : 'reopen', { id: p.id });
    }
    if (tab === 'work') {
      const rf = $('ws-refresh');
      if (rf) rf.onclick = () => query('workspace', { name: p.id });
    }
    if (tab === 'files') {
      const rf = $('fl-refresh');
      if (rf) rf.onclick = () => query('workspace', { name: p.id });
    }
  }
  renderDock();
}
/* dockFilterOf — does this project belong under the current dock
   filter? §8.9: the dock listed everything, unsorted and unfiltered,
   which stops being a navigation surface past a handful of projects.
   The match is a case-insensitive substring over BOTH id and name —
   id because that is what a deep link or a log line hands you, name
   because that is what a human remembers. A named function, not a
   clause inline: what matches is the decision the operator's eye
   depends on, and it is tested by name. */
export function dockFilterOf(p) {
  const f = (S.dockFilter || '').trim().toLowerCase();
  if (!f) return true;
  return (p.id || '').toLowerCase().indexOf(f) >= 0 || (p.name || '').toLowerCase().indexOf(f) >= 0;
}
/* renderDock — the bubble row, its own render so filtering the dock
   does not rebuild the whole page (each keystroke costs one #dock
   innerHTML write, nothing else — the filter input itself is static
   HTML in .dock-wrap and cannot be clobbered by any re-render). */
export function renderDock() {
  const dock = $('dock');
  const viewed = viewedOf();
  /* Two marks, because there are two states: 'active' is the identity's
     working project (server truth), 'viewing' is the page you have open.
     They are usually the same bubble and were previously indistinguishable
     by construction — which is precisely how an edit reached the wrong
     project without anything on screen looking wrong. */
  const viewedID = viewed ? viewed.id : '';
  const list = S.projects.filter(dockFilterOf);
  const empty = '<div class="dock-empty">' + (S.projects.length ? 'no projects match' : 'no projects yet') + '</div>';
  dock.innerHTML = (list.length ? list.map(pp => {
    const initials = pp.name.split(/\s+/).map(w => w[0] || '').join('').slice(0, 2).toUpperCase();
    const marks = (pp.active ? ' active' : '') + (pp.id === viewedID ? ' viewing' : '');
    return '<div class="dock-item' + marks + '" data-id="' + esc(pp.id) + '" style="--hue:' + hueOf(pp.id) + '">' +
    '<div class="bubble' + marks + (pp.state === 'closed' ? ' closed' : '') + '" title="' + esc(pp.name) +
    (pp.active ? ' — the identity is working here' : '') + '">' + esc(initials) + '</div>' +
    '<div class="b-name">' + esc(pp.name) + '</div></div>';
  }).join('') : empty) +
  '<div class="dock-item' + (viewedID === '' && S.viewedProject === '' ? ' viewing' : '') +
  '" id="dock-new"><div class="bubble new" title="New project">+</div><div class="b-name">new</div></div>';
  /* Clicking a bubble is the commitment — one gesture, one meaning
     (operator verdict 2026-08-27, three reports). The stale comment
     here once said the opposite; a fork of the same verdict in two
     wordings stayed green while one of them was wrong. */
  dock.querySelectorAll('.dock-item[data-id]').forEach(el => { el.onclick = () => viewProject(el.dataset.id); });
  /* The new-project page is a view, not a lie about the active project.
     This used to null out activeProject client-side — claiming locally
     that the identity had left a project it was still in, until the next
     payload silently contradicted it. */
  $('dock-new').onclick = () => { S.viewedProject = ''; renderProjects(); };
}

/* renderProjTab — one tab of the three-surface workspace. The data
   arrives as one workspace snapshot; null data renders an explicit
   loading state, never invented content. */
export function renderProjTab(p, ws, tab) {
  if (tab === 'overview') {
    const workCount = ws ? (ws.work || []).length : 0;
    const fileCount = ws ? (ws.files || []).length : 0;
    /* R18 §9.2: when the server capped the listing, the count line
       DECLARES the whole truth — "showing N of M" — never a silent
       shortening. fileCount alone would read as the directory's size,
       which would be a lie of omission the operator cannot see. */
    const fileLine = ws && ws.files_capped && ws.files_total
      ? 'showing ' + fileCount + ' of ' + ws.files_total + ' entries — capped, the full list lives in the directory'
      : fileCount + ' entr' + (fileCount === 1 ? 'y' : 'ies') + ' in the directory';
    /* Seed from the draft when it belongs to THIS project — an
       unsaved edit survives a re-render the guard skipped past, a tab
       switch, or a broadcast that landed between keystrokes. */
    const draft = (S.focusDraft && S.focusDraft.id === p.id) ? S.focusDraft.val : null;
    return '<div class="card"><h3>FOCUS — what is happening here right now</h3>' +
      '<textarea id="focus-edit" rows="3" placeholder="Re-seeds the working state when this project is selected again…">' + esc(draft !== null ? draft : (p.focus || '')) + '</textarea>' +
      '<div class="savebar"><button class="btn" id="focus-save">Save focus</button>' +
      '<button class="btn ghost" id="proj-state">' + (p.state === 'open' ? 'Close project' : 'Reopen') + '</button>' +
      '<span class="savenote" id="focus-note">saved</span></div></div>' +
      '<div class="card"><h3>THIS WORKROOM</h3>' +
      '<div class="ws-facts"><div>' + workCount + ' work session' + (workCount === 1 ? '' : 's') + '</div>' +
      '<div>' + fileLine + '</div></div>' +
      '<div style="color:var(--dim);font-size:13px;line-height:1.6">Chat carries this project while it is focused — turns and work sessions are stamped with it. Files live in the directory above, reachable by the identity\'s own tools.</div></div>';
  }
  if (tab === 'files') {
    if (!ws) return '<div class="card"><h3>FILES</h3><div class="dim-note">Loading directory…</div></div>';
    const files = ws.files || [];
    if (!files.length) return '<div class="card"><h3>FILES</h3><div class="dim-note">The directory is empty.</div></div>';
    return '<div class="card"><h3>FILES — one level, the directory is the truth</h3>' +
      '<div class="savebar"><button class="btn ghost" id="fl-refresh">Refresh</button></div>' +
      '<table class="ws-table"><thead><tr><th>Name</th><th>Size</th></tr></thead><tbody>' +
      files.map(f => '<tr><td>' + (f.dir ? '&#128193; ' : '&#128196; ') + esc(f.name) + '</td><td>' +
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
        /* The verdict is the reason the Work tab exists: the owner's
           own served/partial/unserved line, carried by the projection
           and asserted across the wire by the seam test. Rendering the
           status chip alone dropped it. A session with no verdict yet
           says so rather than showing an empty slot. */
        '<div class="ws-verdict' + (w.result ? '' : ' none') + '">' +
        esc(w.result || 'no outcome recorded yet') + '</div></div>').join('') +
      '</div>';
  }
  return '';
}

/* fmtSize — bytes to human form, the same convention the logs use. */
function fmtSize(n) {
  if (n < 1024) return n + ' B';
  if (n < 1024 * 1024) return (n / 1024).toFixed(1) + ' KB';
  return (n / (1024 * 1024)).toFixed(1) + ' MB';
}
