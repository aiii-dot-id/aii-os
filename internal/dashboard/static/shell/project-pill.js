import { S } from '../state.js';
import { $ } from '../util.js';

function projectLine() {
  const there = $('reply-project');
  if (there) return there;
  const box = document.querySelector('.composer-row');
  if (!box) return null;
  const line = document.createElement('a');
  line.className = 'c-reply__project';
  line.id = 'reply-project';
  line.hidden = true;
  box.before(line);
  return line;
}

export function renderProjPill() {
  const p = S.activeProject, line = projectLine();
  $('pill-proj').textContent = p ? '\u25CE ' + p.name : '';
  if (!line) return;
  line.hidden = !p;
  if (p) { line.href = '#/projects/' + encodeURIComponent(p.id); line.title = p.name; line.textContent = '\u25CE ' + p.name; }
}
