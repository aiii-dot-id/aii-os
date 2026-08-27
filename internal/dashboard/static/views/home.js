/* views/home.js — the landing surface: greeting, the intent line,
   live work rows (delegated to views/work.js), and the project card
   grid. R66: dissolves into the frame's default layout — its pieces
   are the first slot arrangement ui-layout.json describes (§5). */
import { S } from '../state.js';
import { $, esc, hueOf } from '../util.js';
import { go } from '../app.js';
import { sendChat } from './chat.js';
import { viewProject } from './projects.js';
import { workSectionHTML } from './work.js';

/* --- home --------------------------------------------------- */
function greeting() {
  const h = new Date().getHours();
  return h < 5 ? 'Working late' : h < 12 ? 'Good morning' : h < 18 ? 'Good afternoon' : 'Good evening';
}
export function renderHome() {
  const name = S.identityExists && S.stats ? S.stats.name : '';
  let html = '<div class="greet">' + greeting() + '.</div>';
  html += '<div class="greet-sub">' + (name ? esc(name) + ' is here. ' : '') + 'What shall we work on together?</div>';
  html += '<div class="intent"><input id="intent-input" type="text" placeholder="Speak your intent&hellip;">' +
          '<button class="sendb" id="intent-send" title="Send">&#10148;</button></div>';
  html += workSectionHTML();
  html += '<div class="home-h">PROJECTS</div>';
  if (!S.projects.length) {
    html += '<div class="empty">No projects yet — open Projects to create the first workroom you\'ll share.</div>';
  } else {
    html += '<div class="cards-grid">' + S.projects.map(projCardHTML).join('') + '</div>';
  }
  $('home-inner').innerHTML = html;
  const ii = $('intent-input');
  const fire = () => { const v = ii.value; go('chat'); sendChat(v); };
  ii.addEventListener('keydown', e => { if (e.key === 'Enter') fire(); });
  $('intent-send').onclick = fire;
  document.querySelectorAll('#home-inner .proj-card').forEach(el => { el.onclick = () => viewProject(el.dataset.id); });
}
function projCardHTML(p) {
  return '<div class="proj-card' + (p.active ? ' focused' : '') + '" data-id="' + esc(p.id) + '" style="--hue:' + hueOf(p.id) + '">' +
    '<h4><span class="dot' + (p.state === 'closed' ? ' closed' : '') + '"></span>' + esc(p.name) + '</h4>' +
    '<div class="desc">' + esc(p.description || '') + '</div>' +
    '<div class="meta"><span>' + esc(p.state) + '</span><span>' + (p.active ? 'focused' : '') + '</span></div></div>';
}
