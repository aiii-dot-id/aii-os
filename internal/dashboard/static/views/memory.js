
import { S } from '../state.js';
import { $, esc } from '../util.js';
import { send } from '../ws.js';
import { renderBeliefs } from './identity/beliefs.js';
import WI from '../strings/identity.js';

const CHANGE_WORDS = {
  drops: n => n + (n === 1 ? ' vector held is dropped.' : ' vectors held are dropped.'),
  embeds: n => n + (n === 1 ? ' text is embedded by the new source.' : ' texts are embedded by the new source.'),
  sends: entry => 'Every memory\'s text and every recall\'s cue is sent to ' + entry + '.',
  off: 'Nothing is dropped and nothing is sent; recall answers by words until a source is chosen again.',
  uncounted: 'What this changes is not counted: that source cannot be asked now.',
  change: 'Change',
  keep: 'Keep',
};
const PROVIDER_WORDS = {
  off: 'Each memory\'s text is then sent to that provider once to be embedded, and every recall\'s cue each time a recall is asked.',
  on: 'Each memory\'s text is sent to that provider once to be embedded, and again if the model changes; every recall\'s cue is sent to it each time a recall is asked.',
};
const PLUGIN_WORDS = {
  on: 'Each memory\'s text is handed to that plugin once to be embedded, and again if its model changes; every recall\'s cue is handed to it each time a recall is asked.',
};
let asked = null;

function changeHTML(m, choice) {
  const c = (m.changes || []).find(x => x.choice === choice);
  let words;
  if (choice === 'none') words = CHANGE_WORDS.off;
  else if (!c) words = CHANGE_WORDS.uncounted;
  else words = CHANGE_WORDS.drops(Number(c.dropped)) + ' ' + CHANGE_WORDS.embeds(Number(c.to_embed)) + (c.sent_to ? ' ' + CHANGE_WORDS.sends(esc(c.sent_to)) : '');
  return '<div class="item" id="mem-source-change">' + words +
    ' <button class="btn sm" data-mem-change>' + CHANGE_WORDS.change + '</button> <button class="btn sm ghost" data-mem-keep>' + CHANGE_WORDS.keep + '</button></div>';
}

function sourceChoice(m) {
  const stored = m.choice || '';
  if (asked === stored) asked = null;
  const chosen = asked === null ? stored : asked;
  const options = (stored ? [] : [['', 'not chosen yet']]).concat([['none', 'nothing: the layer is off'],
    ['provider', 'the embeddings model of the provider the identity thinks with' + (m.provider_model ? '' : ' (none is named)')]]);
  const plugins = (m.plugins || []).slice();
  if (stored.startsWith('plugin:') && !plugins.includes(stored.slice(7))) plugins.push(stored.slice(7));
  for (const id of plugins) {
    options.push(['plugin:' + id, 'the plugin ' + id + ', on this machine' + ((m.plugins || []).includes(id) ? '' : ' (not running)')]);
  }
  return '<div class="item"><label for="mem-source">The vectors are made by </label><select id="mem-source">' +
    options.map(([value, label]) => '<option value="' + esc(value) + '"' + (value === chosen ? ' selected' : '') + '>' + esc(label) + '</option>').join('') +
    '</select></div>' + (asked === null ? '' : changeHTML(m, asked));
}

export function meaningCard(m) {
  if (!m) return '';
  let html = '<div class="card" id="mem-meaning"><h3>MEANING LAYER</h3>';
  if (m.question) html += '<div class="item" id="mem-source-question">' + esc(m.question) + '</div>';
  html += sourceChoice(m);
  if (!m.source) {
    html += '<div class="item">Off: ' + esc(m.unavailable || 'no source of vectors is set up') + '.</div>' +
      '<div class="item">Recall answers by exact words and fuzzy matches. Two things can make the vectors. A plugin that computes them on this machine: install one in Plugins. ' +
      'Or the provider the identity thinks with: name an embeddings model on it in Settings → Providers → Advanced. ' + PROVIDER_WORDS.off + '</div>';
    return html + '</div>';
  }
  const scale = '<span class="id">basis ' + esc(m.basis) + ' · floor ' + Number(m.floor).toFixed(3) + '</span>';
  if (m.source === 'plugin') {
    html += '<div class="item">Source: the plugin ' + esc(m.plugin) + ', on this machine ' + scale + '</div>' +
      '<div class="item">' + PLUGIN_WORDS.on + ' ' +
      'It declares no permission of any kind, and the host asks it only while the operator has granted it none. The floor is the plugin\'s own, signed with it.</div>';
  } else {
    html += '<div class="item">Source: the embeddings model of the provider the identity thinks with ' + scale + '</div>' +
      '<div class="item">' + PROVIDER_WORDS.on + ' ' +
      'The floor is the host\'s own for a provider\'s model; it was measured for none.</div>';
  }
  let partial = 0, untold = 0;
  for (const c of m.coverage || []) { partial += c.partial || 0; untold += c.untold || 0; }
  if (m.tells) {
    html += '<div class="item">The plugin says, of every text, how much of it its vector was made from.' +
      (partial > 0 ? ' ' + partial + (partial === 1 ? ' vector held was' : ' vectors held were') + ' made from the beginning of ' + (partial === 1 ? 'its' : 'their') +
        ' text alone: a recall says how much on each such hit, and what lies past it is found by words.' : '') +
      (untold > 0 ? ' ' + untold + (untold === 1 ? ' was' : ' were') + ' made before the plugin said how much of a text it read; the backfill makes ' + (untold === 1 ? 'it' : 'them') + ' again.' : '') + '</div>';
  } else {
    html += '<div class="item">The source does not say how much of a text it read.' + (m.handed_chars > 0 ? ' It is handed at most the first ' + Number(m.handed_chars) +
      ' characters of each, and a recalled memory that is longer says so.' : '') + '</div>';
  }
  let rows = 0, embedded = 0;
  for (const c of m.coverage || []) {
    rows += c.rows; embedded += c.embedded;
    const kept = c.beyond_embedded || 0;
    html += '<div class="item">' + esc(c.store) + ' <span class="id">' + c.embedded + ' of ' + c.rows + ' hold a vector</span>' +
      (c.beyond > 0 ? ' <span class="id">' + c.beyond + ' older are outside the provider\'s window (memory.conversation_vector_window) and get no new vector: ' +
        (kept > 0 ? kept + ' keep the one they have and are read by meaning, ' : '') + (c.beyond - kept) + ' are read by words alone</span>' : '') + '</div>';
  }
  const waiting = rows - embedded;
  const stale = '. A row whose text has changed since the last pass is read by its words until the next pass makes its vector again';
  html += '<div class="item">' + (waiting <= 0 ? 'Every row in scope holds a vector' + stale
    : m.waiting ? waiting + ' waiting for voice: the backfill holds off while a session is listening or a reply is being spoken'
    : waiting + ' waiting for the backfill\'s next pass' + stale) + '.</div>';
  if (m.set_aside > 0) {
    html += '<div class="item">' + m.set_aside + ' set aside: the source could not embed ' + (m.set_aside === 1 ? 'it' : 'them') +
      (m.set_aside_why ? ' (' + esc(m.set_aside_why) + ')' : '') + '. Asked again when the source next starts.</div>';
  }
  const p = m.last_pass;
  if (p) {
    html += '<div class="item">Last pass ' + esc(new Date(p.at).toLocaleString()) + ': ' + p.embedded + ' embedded' +
      (p.stopped ? ', then stopped: ' + esc(p.stopped) : '') + '.</div>';
  }
  return html + '</div>';
}

const recordHit = /^(\s*\[seq )([1-9][0-9]*)(?=, (?!turn [0-9]))/gm;
export function recallHTML(text) {
  return esc(text).replace(recordHit, (_, head, seq) =>
    head + '<a href="#/identity/record/' + seq + '" data-mem-record="' + seq + '" title="Open this row of the record, whole">' + seq + '</a>');
}

export function renderMemory() {
  const st = $('memory-stack');
  let view = $('beliefs-view'), rest = $('memory-rest');
  if (!view || !rest) {
    view = document.createElement('section'); view.id = 'beliefs-view'; view.className = 'beliefs';
    view.setAttribute('aria-label', WI.tabs.beliefs);
    rest = document.createElement('div'); rest.id = 'memory-rest';
    st.replaceChildren(view, rest);
  }
  renderBeliefs();
  const id = S.identity;
  if (!id) { rest.innerHTML = ''; return; }
  const html = meaningCard(id.meaning);
  if (rest.dataset.html !== html) { rest.dataset.html = html; rest.innerHTML = html; }
  const source = $('mem-source');
  if (source) source.onchange = () => { asked = source.value; renderMemory(); };
  const change = rest.querySelector('[data-mem-change]');
  if (change) change.onclick = () => { const choice = asked; asked = null; send({ type: 'config_set', config: { 'memory.meaning_source': choice } }); renderMemory(); };
  const keep = rest.querySelector('[data-mem-keep]');
  if (keep) keep.onclick = () => { asked = null; renderMemory(); };
}
