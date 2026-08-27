/* overlay.js — the W3 applier: the client half of overlay hot reload.
   The server half (watcher + broadcast) tells the screen that overlay
   bytes moved on disk; THIS module is what makes the moved bytes reach
   the pixels without an F5.

   The split mirrors theme.js exactly: the server is the boundary and
   the validator, this module is the applier. And like theme.js it is
   safe on its own terms — it never parses HTML, never generates a
   <style> block (the no-parse-step choice that buys uiCSP's strict
   style-src-elem), and touches nothing but stylesheet tags it can
   prove belong to the frame.

   WHY HREF-SWAP FOR CSS AND RELOAD FOR JS. A stylesheet is a resolved
   byte stream the engine re-applies when the tag's href changes: swap
   the version token and the new bytes land on a live page — the same
   live-apply family as theme tokens (CSS custom properties). A script
   is an already-executed module graph; re-importing it from the same
   URL is a no-op (the module map keys on the URL), so the honest
   mechanism is a reload. Reload eats the composer draft, so the draft
   is persisted first — the W3 "painlessly" rule: an edit on disk must
   never cost the operator their half-typed words.

   The frame ships link tags for every servable stylesheet including
   the empty custom.css stub, so the applier only ever RE-POINTS an
   existing tag. A file the frame does not reference has no tag, and
   a swap with no tag is a no-op. */

let swapped = {};   // path -> version token currently applied

/* onOverlayChanged receives the LIVE invalidation: the watcher saw
   overlay bytes move on disk and the server says so with a fresh
   monotonic token plus the paths that moved. This is the ONLY live
   trigger — the `overlays` audit readback (retained events with
   decidedAt stamps) is render-only; a page that applied audit events
   would loop (connect → retained accepted event → reload → connect)
   and would miss real edits (a later edit rebroadcasts the same old
   decidedAt token). Audit is history; this is news. */
export function onOverlayChanged(token, paths) {
  if (typeof token !== 'number' || !Array.isArray(paths)) return;
  if (token <= lastToken) return;          // stale or duplicate push
  lastToken = token;
  let needsReload = false;
  for (const p of paths) {
    if (typeof p !== 'string') continue;
    if (swapped[p] === token) continue;    // already applied this change
    swapped[p] = token;
    if (p.endsWith('.css')) swapCSS(p, String(token));
    else needsReload = true;               // .js forks — module graph
  }
  if (needsReload) draftSafeReload();
}

let lastToken = 0;  // highest live token applied (per page load)

/* swapCSS re-points the frame's stylesheet tag for this path with a
   fresh version token, forcing a re-fetch that Cache-Control
   no-cache answers from current disk truth. hrefs in index.html are
   relative ("./custom.css"); server paths are absolute ("/custom.css").
   Absent tag = a file the frame does not reference: no-op. */
function swapCSS(p, token) {
  const base = p.startsWith('/') ? p.slice(1) : p;
  for (const link of document.querySelectorAll('link[rel="stylesheet"]')) {
    const href = link.getAttribute('href');
    if (!href) continue;
    const clean = stripVersion(href);
    if (clean === p || clean === './' + base) {
      link.setAttribute('href', clean + '?v=' + encodeURIComponent(token));
      return;
    }
  }
}

/* stripVersion removes any prior ?v= token so repeated pushes stay
   idempotent: compare on the clean href, swap in a fresh token. */
function stripVersion(href) {
  const i = href.indexOf('?');
  return i <  0 ? href : href.slice(0, i);
}

/* draftSafeReload persists the composer draft, reloads, and restores
   it after. sessionStorage (not localStorage): the draft belongs to
   THIS tab's live session, not to the machine's durable state — a
   second tab clobbering a first tab's draft is the two-writers bug. */
function draftSafeReload() {
  const ta = document.getElementById('msg-input');
  if (ta && typeof ta.value === 'string' && ta.value.trim() !== '') {
    try { sessionStorage.setItem('aii.draft', ta.value); } catch (err) {}
  }
  location.reload();
}

/* restoreDraft runs at boot (app.js): a draft persisted by a
   draftSafeReload lands back in the composer. One-shot on purpose —
   the value is removed on read, so a manual F5 after restore never
   resurrects a stale draft. */
export function restoreDraft() {
  let saved = '';
  try { saved = sessionStorage.getItem('aii.draft') || ''; } catch (err) {}
  if (!saved) return;
  try { sessionStorage.removeItem('aii.draft'); } catch (err) {}
  const ta = document.getElementById('msg-input');
  if (ta) ta.value = saved;
}
