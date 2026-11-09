export const LOOK = { shape: ['--mark-shape', 0, 1, 0], cx: ['--mark-centre-x', -16, 16, 0], cy: ['--mark-centre-y', -16, 16, 0], folds: ['--mark-folds', 0, 24, 12] };
export function lookOf(style) {
  const out = {};
  for (const k in LOOK) {
    const [name, lo, hi, d] = LOOK[k], v = parseFloat(style.getPropertyValue(name));
    out[k] = Number.isFinite(v) ? Math.min(hi, Math.max(lo, v)) : d;
  }
  return out;
}
