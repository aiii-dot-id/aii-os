const opens = (s, at) => at < s.length && !/\s/.test(s[at]);
const closes = (s, at) => at > 0 && !/\s/.test(s[at - 1]);

export function spans(s) {
  const out = [];
  let text = '';
  const flush = () => { if (text) { out.push(['text', text]); text = ''; } };
  for (let i = 0; i < s.length;) {
    if (s[i] === '`') {
      const end = s.indexOf('`', i + 1);
      if (end > i + 1 && !s.slice(i + 1, end).includes('\n')) { flush(); out.push(['code', s.slice(i + 1, end)]); i = end + 1; continue; }
    } else if (s.startsWith('***', i) && opens(s, i + 3) && closing(s, i + 3, '***') > i + 3) {
      const end = closing(s, i + 3, '***');
      flush(); out.push(['em', [['strong', spans(s.slice(i + 3, end))]]]); i = end + 3; continue;
    } else if (s.startsWith('**', i) && opens(s, i + 2)) {
      const end = closing(s, i + 2, '**');
      if (end > i + 2) { flush(); out.push(['strong', spans(s.slice(i + 2, end))]); i = end + 2; continue; }
    } else if (s[i] === '*' && s[i + 1] !== '*' && opens(s, i + 1)) {
      const end = closing(s, i + 1, '*');
      if (end > i + 1) { flush(); out.push(['em', spans(s.slice(i + 1, end))]); i = end + 1; continue; }
    }
    text += s[i++];
  }
  flush();
  return out;
}

function closing(s, from, mark) {
  for (let i = from; i < s.length; i++) {
    if (s[i] === '\n') return -1;
    if (s[i] === '`') { const end = s.indexOf('`', i + 1); if (end > i && !s.slice(i + 1, end).includes('\n')) { i = end; continue; } }
    if (s.startsWith(mark, i) && closes(s, i) && (mark !== '*' || (s[i + 1] !== '*' && s[i - 1] !== '*'))) return i;
  }
  return -1;
}

function nodes(parts) {
  return parts.map(([kind, content]) => {
    if (kind === 'text') return document.createTextNode(content);
    const el = document.createElement(kind);
    if (kind === 'code') el.textContent = content; else el.append(...nodes(content));
    return el;
  });
}

const BULLET = /^ {0,3}[-*+] +(.*)$/, NUMBER = /^ {0,3}(\d{1,9})[.)] +(.*)$/;
const HEADING = /^ {0,3}#{1,6} +(.*?)(?: +#+)? *$/, FENCE = /^ {0,3}```/;
const fenceEnd = (lines, i) => FENCE.test(lines[i]) ? lines.findIndex((l, j) => j > i && FENCE.test(l)) : -1;
export function blocks(text) {
  const lines = String(text || '').split('\n'), out = [];
  let run = [];
  const flush = () => { if (run.length) out.push(['text', run.join('\n')]); run = []; };
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i], end = fenceEnd(lines, i), head = HEADING.exec(line);
    const kind = BULLET.test(line) ? BULLET : NUMBER.test(line) ? NUMBER : null;
    if (end > i) { flush(); out.push(['pre', lines.slice(i + 1, end).join('\n')]); i = end; }
    else if (head) { flush(); out.push(['heading', head[1]]); }
    else if (!kind) run.push(line);
    else {
      flush();
      const items = [], start = kind === NUMBER ? +NUMBER.exec(line)[1] : 1;
      for (; i < lines.length && kind.test(lines[i]); i++) items.push(kind.exec(lines[i]).at(-1));
      out.push([kind === NUMBER ? 'ol' : 'ul', items, start]); i--;
    }
  }
  flush();
  if (out.length > 1) for (const b of out) if (b[0] === 'text') b[1] = b[1].replace(/^\n+|\n+$/g, '');
  return out.filter(b => b[0] !== 'text' || b[1]);
}

const make = (tag, cls, kids) => { const e = document.createElement(tag); if (cls) e.className = cls; e.append(...kids); return e; };
function blockNodes(text) {
  return blocks(text).flatMap(([kind, content, start]) => {
    if (kind === 'text') return nodes(spans(content));
    if (kind === 'pre') return [make('pre', 'in-words__pre', [make('code', '', [content])])];
    if (kind === 'heading') return [make('p', 'in-words__heading', nodes(spans(content)))];
    const list = make(kind, '', content.map(item => make('li', '', nodes(spans(item)))));
    if (start !== 1) list.start = start;
    return [list];
  });
}

export function draw(el, text) {
  el.replaceChildren(...blockNodes(String(text || '')));
}

export function fragment(text) {
  const f = document.createDocumentFragment();
  f.append(...blockNodes(String(text || '')));
  return f;
}

export function fenced(lines) {
  const code = new Set();
  for (let i = 0; i < lines.length; i++) {
    const end = fenceEnd(lines, i);
    while (end > i) code.add(i++);
    if (end >= 0) code.add(end);
  }
  return code;
}

export function setWords(el, text) {
  if (!el || (el.dataset.words === text && (el.firstChild || !text))) return;
  el.dataset.words = text;
  draw(el, text);
}
