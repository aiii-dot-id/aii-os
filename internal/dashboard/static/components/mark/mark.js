const NS = 'http://www.w3.org/2000/svg', CORE = 3, HALF = 50, TAU = 2 * Math.PI, WIDE = 1.16;

export const turns = (hours, size = 100) => hours <= 0 ? 0 : hours < 1 ? hours : 1 + Math.log2(hours) / (size <= 40 ? 2 : 1);

import { LOOK, lookOf } from './look.js';
export { LOOK, lookOf };

const GROW = 1.25, BRAIN = [[44, -4], [40, -20], [26, -33], [6, -38], [-16, -36], [-32, -26], [-42, -10], [-44, 4], [-40, 16], [-30, 24], [-20, 30], [-8, 28], [-2, 22], [8, 28], [24, 31], [38, 24], [36, 14], [44, 10]];
let outline = null;
function brain() {
  if (outline) return outline;
  outline = [];
  for (let i = 0, n = BRAIN.length; i < n; i++) {
    const [a, b, c, d] = [BRAIN[(i + n - 1) % n], BRAIN[i], BRAIN[(i + 1) % n], BRAIN[(i + 2) % n]];
    for (let j = 0; j < 40; j++) {
      const t = j / 40, t2 = t * t, t3 = t2 * t;
      outline.push([0, 1].map(x => GROW * 0.5 * (2 * b[x] + (c[x] - a[x]) * t + (2 * a[x] - 5 * b[x] + 4 * c[x] - d[x]) * t2 + (3 * b[x] - a[x] - 3 * c[x] + d[x]) * t3)));
    }
  }
  return outline;
}

function edgeOf({ shape, cx, cy, folds }, inset) {
  const R = HALF - inset;
  const circle = th => { const d = cx * Math.cos(th) + cy * Math.sin(th); return -d + Math.sqrt(d * d - cx * cx - cy * cy + R * R); };
  if (!shape) return circle;
  const N = 360, bins = new Array(N).fill(0);
  for (const [x, y] of brain()) {
    const i = Math.round(((Math.atan2(y - cy, x - cx) + TAU) % TAU) / TAU * N) % N;
    bins[i] = Math.max(bins[i], Math.hypot(x - cx, y - cy));
  }
  for (let i = 0; i < N; i++) if (!bins[i]) { let l = i, r = i; while (!bins[l]) l = (l + N - 1) % N; while (!bins[r]) r = (r + 1) % N; bins[i] = (bins[l] + bins[r]) / 2; }
  const far = th => { const x = ((th % TAU) + TAU) % TAU / TAU * N, i = Math.floor(x) % N, f = x - Math.floor(x); return bins[i] * (1 - f) + bins[(i + 1) % N] * f; };
  const lobed = th => far(th) * (1 + (folds ? 0.03 : 0) * Math.sin(folds * th + 1)) - inset;
  return shape >= 1 ? lobed : th => (1 - shape) * circle(th) + shape * lobed(th);
}

function spiral(born, count, edge, look, size) {
  let a = Math.floor(born / 1000) | 0;
  const rand = () => (a = (Math.imul(a, 1103515245) + 12345) & 0x7fffffff) / 0x80000000;
  const waves = [0, 1, 2].map(() => [1.5 + 4 * rand(), 0.4 + 0.6 * rand(), rand() * 7]);
  const crinkle = look.shape * look.folds / 12, at = (th, r) => [look.cx + r * Math.cos(th), look.cy + r * Math.sin(th)], pts = [];
  const wobble = (th, r, gap) => r * (1 + (1 - look.shape) * Math.min(0.012, 0.25 * gap / r) * waves.reduce((s, v) => s + v[1] * Math.sin(v[0] * th + v[2]), 0) / 3);
  if (!crinkle) {
    const n = Math.max(24, Math.ceil(count * (size >= 200 ? 48 : 24)));
    for (let i = 0; i <= n; i++) {
      const f = count * i / n, th = f * TAU, gap = (edge(th) - CORE) / count;
      pts.push(at(th, wobble(th, CORE + gap * f, gap)));
    }
    return pts;
  }
  let phase = rand() * TAU, f = 0;
  for (;;) {
    const th = f * TAU, gap = (edge(th) - CORE) / count, r = CORE + gap * f, lam = 1.7 * gap;
    const fade = Math.min(1, Math.max(0, (gap * size / 100 - 1.2) / 2), f / 2, (count - f) * 2);
    pts.push(at(th, wobble(th, r + 0.3 * gap * crinkle * fade * Math.sin(phase), gap)));
    if (f >= count) return pts;
    const ds = Math.min(lam / 8, 1.2);
    phase += ds / lam * TAU; f = Math.min(count, f + ds / (TAU * Math.max(r, 1)));
  }
}

const path = pts => pts.map((p, i) => (i ? 'L' : 'M') + p[0].toFixed(2) + ' ' + p[1].toFixed(2)).join(' ');
const svgEl = (tag, attrs) => {
  const el = document.createElementNS(NS, tag);
  for (const k in attrs) el.setAttribute(k, attrs[k]);
  return el;
};

export function render(props) {
  const el = svgEl('svg', { class: 'c-mark', viewBox: '-50 -50 100 100', fill: 'none', stroke: 'currentColor', 'stroke-linecap': 'round', 'stroke-linejoin': 'round', 'aria-hidden': 'true' });
  update(el, props);
  return el;
}

export function update(el, { born, hours = 0, size = 32, state = '', look = lookOf({ getPropertyValue: () => '' }) }) {
  if (state && el.getAttribute('data-state') !== state) el.setAttribute('data-state', state);
  const count = turns(hours, size), said = [born, count, size, look.shape, look.cx, look.cy, look.folds].join('/');
  if (el.dataset.said === said) return;
  el.dataset.said = said;
  const w = 1 + (WIDE - 1) * look.shape;
  el.setAttribute('viewBox', (-HALF * w).toFixed(2) + ' -50 ' + (100 * w).toFixed(2) + ' 100');
  el.setAttribute('width', (size * w).toFixed(1)); el.setAttribute('height', size);
  const u = 100 / size, bound = size <= 40 ? 1.3 : 1.1, inset = bound * u / 2 + 0.3, edge = edgeOf(look, inset);
  const at = (th, r) => [look.cx + r * Math.cos(th), look.cy + r * Math.sin(th)], thin = { 'stroke-width': (bound * u).toFixed(2), 'stroke-opacity': 0.75 };
  const line = Math.max(0.7, Math.min(1.6, count ? (edge(0) - CORE) / count * size / 100 * 0.32 : 0));
  const kids = [];
  if (!look.shape) kids.push(svgEl('circle', { class: 'c-mark__bound', r: (HALF - inset).toFixed(2), ...thin }));
  else {
    const ring = [];
    for (let i = 0; i < 180; i++) ring.push(at(i / 180 * TAU, edge(i / 180 * TAU)));
    const ts = Math.atan2(27 * GROW - look.cy, -3 * GROW - look.cx), rs = edge(ts);
    kids.push(svgEl('path', { class: 'c-mark__bound', d: path(ring) + ' Z', ...thin }),
      svgEl('path', { d: path([at(ts, rs), at(ts, rs + 7 * look.shape)]), 'stroke-width': (2 * bound * u).toFixed(2), 'stroke-opacity': (0.75 * look.shape).toFixed(2) }));
  }
  const centre = { class: 'c-mark__centre', r: (Math.max(2, size * 0.035) * u).toFixed(2), fill: 'currentColor', stroke: 'none' };
  if (look.cx || look.cy) Object.assign(centre, { cx: look.cx, cy: look.cy });
  kids.push(svgEl('circle', centre));
  if (count) {
    const pts = spiral(born, count, edge, look, size), end = count * TAU, steps = look.shape ? 180 : 72, round = [];
    for (let i = 1; i <= steps; i++) { const th = end + i / steps * TAU; round.push(at(th, edge(th))); }
    kids.push(svgEl('path', { class: 'c-mark__turns', d: path(pts), 'stroke-width': (line * u).toFixed(2), 'stroke-opacity': 0.75 }),
      svgEl('path', { class: 'c-mark__life', d: path(pts.concat(round)), pathLength: 1000, 'stroke-width': (Math.max(1.4, line * 1.8) * u).toFixed(2) }));
  }
  el.replaceChildren(...kids);
}

export function markup(props, colour) {
  const el = render(props);
  el.setAttribute('xmlns', NS);
  el.setAttribute('stroke', colour);
  el.querySelector('.c-mark__centre').setAttribute('fill', colour);
  el.querySelector('.c-mark__life')?.remove();
  return new XMLSerializer().serializeToString(el);
}
