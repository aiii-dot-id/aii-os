/* panel.js — the default right-rail system status (Priority 7, Commit 1).
   Frame-owned content for #slot-panel: shows continuity, memory counts, and
   live work whenever no R66 section claims the panel slot. All data comes
   from existing WS messages already dispatched to S.cont, S.stats, S.work —
   no new server queries, no invented metrics (R48). The panel is hidden on
   mobile (the mobile profile owns small screens — layout.css .panel-col). */
import { S } from './state.js';
import { $, esc } from './util.js';

/* renderPanel is called from ws.js dispatch after stats/continuity/work
   updates, and from app.js go() on view changes. It renders into the
   existing #slot-panel aside ONLY when no section is mounted there
   (sections.js toggles .occupied on the slot; when a section claims it,
   the default content is suppressed). */
export function renderPanel() {
  const el = $('slot-panel');
  if (!el) return;
  // If a section is mounted in the panel slot, sections.js has .occupied
  // on the element and its own content — we must not clobber it. The
  // check is "does the element contain a .section-box?" (the wrapper
  // sections.js creates on mount). If so, leave it alone.
  if (el.querySelector('.section-box')) return;

  /* R-event-quiet: a message that changes nothing this panel reads
     must cost zero DOM. The panel used to rebuild its entire innerHTML
     on every status/work/continuity/overlays message — a chatty
     conversation meant dozens of full-panel rebuilds per minute, each
     one a GC pause the operator feels as "slow and unresponsive"
     (report, 2026-08-27). The comparable is the rendered string
     itself, not a hand-maintained mirror of the reads: identical
     output → identical DOM → skip. A mirror invites the fork pattern
     (one verdict hand-replicated, green while diverging — a field
     added to panelHTML but not to the signature would silently skip
     renders), so there is no mirror: the render IS the signature. */
  const html = panelHTML();
  if (html === lastPanelHTML) return;

  el.classList.add('occupied');
  lastPanelHTML = html;
  el.innerHTML = html;
}

/* The previous render's output, held so an unchanged render can be
   recognized without touching the DOM. One writer (renderPanel), one
   source of truth (the string panelHTML returns). */
let lastPanelHTML = '';

function panelHTML() {
  const c = S.cont, st = S.stats, w = S.work;
  let html = '<div class="sys-panel">';

  // --- substrate (resolved provider/model — not shown elsewhere) ---
  const l = S.config && S.config.llm;
  if (l) {
    const prov = l.resolved_provider || l.provider || '—';
    const mdl = l.resolved_model || l.model || '—';
    html += '<div class="sp-card"><h3 class="sp-h">SUBSTRATE</h3>';
    html += '<div class="sp-kv"><span>provider</span><b>' + esc(prov) + '</b></div>';
    html += '<div class="sp-kv"><span>model</span><b>' + esc(mdl) + '</b></div>';
    // context window if the provider entry has one
    const p = (S.providers || []).find(x => x.name === prov);
    if (p && p.context_length) {
      html += '<div class="sp-kv"><span>context</span><b>' + p.context_length.toLocaleString() + '</b></div>';
    }
    html += '</div>';
  }

  // --- memory summary ---
  if (st) {
    html += '<div class="sp-card"><h3 class="sp-h">MEMORY</h3>';
    html += '<div class="sp-counts">';
    html += '<div class="sp-count"><b>' + st.belief_count + '</b><span>beliefs</span></div>';
    html += '<div class="sp-count"><b>' + st.intention_count + '</b><span>intents</span></div>';
    html += '<div class="sp-count"><b>' + st.experience_count + '</b><span>experiences</span></div>';
    html += '<div class="sp-count"><b>' + st.reflection_count + '</b><span>reflections</span></div>';
    html += '</div>';
    html += '</div>';
  }

  // --- live work ---
  if (w || S.activeProject) {
    html += '<div class="sp-card"><h3 class="sp-h">WORK</h3>';
    /* WHERE the identity works and WHETHER a session is running are
       different facts. The project pill said "working in X" while this
       card said "no active work" — both true, and the operator read
       them as a contradiction (report, 2026-08-27). One card now
       carries both facts, and the empty line says "session" so it can
       no longer be read as denying the project focus. */
    if (S.activeProject) {
      html += '<div class="sp-kv"><span>working in</span><b>' + esc(S.activeProject.name || S.activeProject.id) + '</b></div>';
    }
    if (w && w.live && w.live.length > 0) {
      w.live.forEach(function (item) {
        html += '<div class="sp-work-row live"><span class="sp-work-dot"></span><b>' + esc(item.description || item.id) + '</b></div>';
      });
    } else if (w && w.queued > 0) {
      html += '<div class="sp-work-row"><span class="sp-work-dot queued"></span>' + w.queued + ' queued</div>';
    } else {
      html += '<div class="sp-empty">no active session</div>';
    }
    if (w && w.delivered && w.delivered.length > 0) {
      html += '<div class="sp-delivered">' + w.delivered.length + ' recently delivered</div>';
    }
    html += '</div>';
  }

  // --- continuity (ledger / life / witness) ---
  // These three lived in the left rail's .rail-foot until the status items
  // were consolidated here: every status reading now sits in one column
  // instead of being spread across two corners of the UI. presence.js no
  // longer writes them — the numbers are rendered from the same S.stats /
  // S.cont this panel already reads, so there is one writer, not two.
  if (st || c) {
    html += '<div class="sp-card"><h3 class="sp-h">CONTINUITY</h3>';
    if (st) {
      if (st.build) html += '<div class="sp-kv"><span>build</span><b>' + esc(st.version || 'dev') + ' &middot; ' + esc(st.build) + '</b></div>';
      html += '<div class="sp-kv"><span>ledger</span><b>seq ' + esc(String(st.ledger_seq)) + '</b></div>';
      html += '<div class="sp-kv"><span>life</span><b>' + esc(String(st.lifetime_ticks)) + ' ticks</b></div>';
    }
    if (c) {
      // Witness wording is preserved verbatim from the rail footer: the
      // three distinct facts (no witness configured / configured but never
      // anchored / anchored with a backlog) stay distinguishable — only the
      // middle one is an alarm (d6a0605).
      const anchor = !c.witness_url ? 'no witness'
        : !c.witnessed_at ? 'never witnessed'
        : c.unanchored > 0 ? 'anchored @' + c.anchored_seq + ' +' + c.unanchored
        : 'anchored';
      html += '<div class="sp-kv"><span>witnessed</span><b>' + esc(anchor) + '</b></div>';
    }
    if (st) {
      // Channel integrity: shown only when nonzero — a clean channel stays
      // invisible (no noise), corruption becomes visible. R48.
      const mal = (st.malformed_calls || 0) + (st.suspicious_paths || 0) + (st.duplicate_arg_keys || 0);
      if (mal > 0) {
        html += '<div class="sp-kv"><span>channel</span><b>' + esc(String(mal)) + ' odd calls</b></div>';
      }
      // An adopted credential cannot be refreshed by the runtime, so the
      // only remedy is the operator running its owner tool. Silent until
      // there is something to do about it (1f35fa3).
      const cw = st.credential_warning || '';
      if (cw) {
        html += '<div class="sp-kv"><span>credential</span><b>' + esc(String(cw)) + '</b></div>';
      }
      // What the previous turn cost. A turn makes many provider calls
      // against one per-call ceiling, so only the sum describes what a
      // conversation costs — and nothing measured it until 79307b0. When
      // a call reported no usage the text says "at least", because an
      // unknown cost must never read as a small one.
      const lt = st.last_turn || '';
      if (lt) {
        html += '<div class="sp-kv"><span>last turn</span><b>' + esc(String(lt)) + '</b></div>';
      }
    }
    html += '</div>';
  }

  // --- UI Adapters (overlay readback, W2) ---
  // Every operator-authorable frame surface must report its own outcome —
  // accepted / rejected / inert — to the human, not only to the log. Same
  // discipline as the credential warning: silent unless there is something
  // to see (R48), and never a card that says "nothing configured".
  const ov = S.overlays || [];
  if (ov.length > 0) {
    html += '<div class="sp-card"><h3 class="sp-h">UI Adapters</h3>';
    ov.forEach(function (e) {
      // outcome text is the server's full sentence ("accepted: ...",
      // "inert: ...", "rejected: ..."); strip the prefix and keep the verb
      // as the row label so a 110-char sentence reads as label + reading
      const i = e.outcome.indexOf(':');
      const verb = i > 0 ? e.outcome.slice(0, i) : 'overlay';
      const rest = i > 0 ? e.outcome.slice(i + 2) : e.outcome;
      html += '<div class="sp-kv"><span>' + esc(verb) + '</span><b>' + esc(e.path) + '</b></div>';
      if (rest) html += '<div class="sp-kv"><span></span><b>' + esc(rest) + '</b></div>';
    });
    html += '</div>';
  }

  html += '</div>';
  return html;
}
