export const SINCE_BIRTH = 'birth';

export const dateChoices = W => [['30', W.last30], ['7', W.last7], [SINCE_BIRTH, W.sinceBirth], ['dates', W.chooseDates]];

export function within(t, f) {
  if (f.when === SINCE_BIRTH) return true;
  if (isNaN(t)) return false;
  if (f.when === 'dates') {
    const day = (text, after) => { const d = new Date(text + 'T00:00'); d.setDate(d.getDate() + after); return d.getTime(); };
    return (!f.from || t >= day(f.from, 0)) && (!f.to || t < day(f.to, 1));
  }
  const from = new Date();
  from.setHours(0, 0, 0, 0);
  from.setDate(from.getDate() - (Number(f.when) - 1));
  return t >= from.getTime();
}

export function shortWhen(t) {
  if (isNaN(t)) return '';
  const d = new Date(t), year = d.getFullYear() !== new Date().getFullYear() ? { year: 'numeric' } : {};
  return d.toLocaleString([], { day: 'numeric', month: 'short', ...year, hour: '2-digit', minute: '2-digit' });
}

function el(tag, attrs, ...kids) {
  const e = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) if (v !== false) e.setAttribute(k, v === true ? '' : v);
  e.append(...kids);
  return e;
}

export function choice(label, options, value, changed) {
  const sel = el('select', { class: 'c-filter__select' }, ...options.map(([v, text]) => el('option', { value: v, selected: v === value }, text)));
  sel.onchange = () => changed(sel.value);
  return el('label', { class: 'c-filter' }, el('span', { class: 'c-filter__label' }, label), sel);
}

export function dateFilter(W, f, changed) {
  const day = (label, key) => {
    const input = el('input', { type: 'date', class: 'c-filter__select', value: f[key] || '' });
    input.onchange = () => { f[key] = input.value; changed(); };
    return el('label', { class: 'c-filter' }, el('span', { class: 'c-filter__label' }, label), input);
  };
  const dates = el('span', { class: 'c-filter__dates' }, day(W.dateFrom, 'from'), day(W.dateTo, 'to'));
  dates.hidden = f.when !== 'dates';
  const when = choice(W.filterDate, dateChoices(W), f.when, v => { f.when = v; dates.hidden = v !== 'dates'; changed(); });
  return [when, dates];
}
