/* views/plugins.js — plugin management: the operator's real controls.
   The autoload level and the discovered-but-not-loaded list used to live
   under Settings while this view showed a "COMING" placeholder, so an
   operator looking for plugins was told they did not exist yet while the
   controls sat one tab away (operator ruling 2026-08-24: real controls
   here). Config saving keeps its single owner in settings.js; this view
   consumes it through a narrow seam.
   R66: frame — plugin management stays in the binary. */
import { S } from '../state.js';
import { $, esc } from '../util.js';
import { saveConfigSection, configFeedbackHTML } from './settings.js';

function pluginsHTML(c) {
  if (!c) return '<div class="card"><div class="empty">loading configuration…</div></div>';
  const lvl = c.plugins.autoload || 'T1';
  const opts = [
    ['T3', 'T3 only — platform-signed'],
    ['T2', 'T2 or above — reviewed + signed'],
    ['T1', 'T1 or above — signed (default)'],
    ['T0', 'any — including unsigned (dev)'],
    ['none', 'none — nothing auto-loads'],
  ];
  return '<div class="card"><h3>PLUGINS — AUTO-LOAD LEVEL</h3>' +
    '<div style="font-size:11.5px;color:var(--faint);margin-bottom:8px;line-height:1.5">Plugins install by dropping a directory into plugins/ beside config.json. A discovered package activates only if its <b>verified</b> tier meets this level — evidence, never the package\'s claim. The sandbox never relaxes with the level, and a package whose signature fails verification is refused at every level.</div>' +
    '<label class="f">AUTO-LOAD</label><select id="cfg-plevel">' +
    opts.map(o => '<option value="' + o[0] + '"' + (o[0] === lvl ? ' selected' : '') + '>' + o[1] + '</option>').join('') + '</select>' +
    '<div class="savebar"><button class="btn" data-save="plugins">Save</button><span class="savenote">saved — applies live</span></div>' +
    skipsHTML(c.plugins.skips) + '</div>';
}
function skipsHTML(skips) {
  if (!skips || !skips.length) return '';
  return '<div style="margin-top:10px"><label class="f">PRESENT, VERIFIED — NOT LOADED</label>' +
    skips.map(s => '<div style="font-size:11.5px;color:var(--faint);padding:4px 0;border-top:1px solid var(--line)"><b style="color:var(--txt)">' +
      esc(s.id) + '</b> (' + esc(s.tier) + ') — ' + esc(s.reason) + '</div>').join('') + '</div>';
}

/* --- plugins ------------------------------------------------ */
export function renderPlugins() {
  const st = $('plugins-stack');
  const c = S.config;
  st.innerHTML = configFeedbackHTML() + pluginsHTML(c) + roadmapHTML();
  st.querySelectorAll('[data-save]').forEach(btn => { btn.onclick = () => { saveConfigSection(btn.dataset.save); renderPlugins(); }; });
}

/* The roadmap stays BELOW the controls, not instead of them. It is also
   what explains the composer's microphone: a reserved seat, kept
   deliberately (operator ruling 2026-08-24) until voice exists. */
function roadmapHTML() {
  return '<div class="card"><h3>VOICE<span class="soon">FLAGSHIP</span></h3>' +
    '<p style="color:var(--dim);font-size:13.5px;line-height:1.65">Human-level voice is a major coming feature of AII OS — fully on-device speech, audio that never crosses the network, arriving as a platform plugin. The microphone in the composer is its reserved seat.</p></div>' +
    '<div class="card"><h3>WORKING MEMORY<span class="soon">COMING</span></h3>' +
    '<p style="color:var(--dim);font-size:13.5px;line-height:1.65">RING4 working-state memory will itself be a plugin: mechanically scored recall, dream-cycle consolidation, and promotion to the ledger only as the identity\'s own explicit act. Plugins run out of process behind a capability broker, write only to scoped RING4, and the identity decides what becomes memory.</p></div>';
}
