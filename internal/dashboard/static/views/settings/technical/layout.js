import { page } from '../../../sections.js';
import { esc, fill } from '../../../util.js';
import { nameOf } from './forms.js';
import W from '../../../strings/settings-technical-forms.js';

const L = W.layout;
export function drawLayout(root) {
  const profiles = (page.layout && page.layout.profiles) || null;
  const slots = profiles ? Object.entries(profiles).flatMap(([profile, places]) => Object.entries(places || {}).map(([slot, ids]) => [fill(L.profile, { profile, slot }), ids || []])) : [];
  const name = nameOf();
  const html = '<div class="technical-form" data-technical-form="layout"><p class="technical-form__heading">' + esc(name ? fill(L.heading, { name }) : L.headingUnnamed) + '</p>' +
    (slots.length ? '<section class="technical-form__group"><h3 class="technical-form__group-title">' + esc(L.readback) + '</h3><h4 class="technical-form__label">' + esc(L.slots) + '</h4>' +
      '<ul class="technical-form__list" data-layout-slots>' + slots.map(([where, ids]) => '<li class="technical-form__row"><span class="technical-form__row-title">' + esc(where) + '</span><span class="technical-form__hint">' + esc(ids.length ? ids.join(', ') : L.nothingPlaced) + '</span></li>').join('') + '</ul>' +
      '<h4 class="technical-form__label">' + esc(L.sections) + '</h4><ul class="technical-form__list" data-layout-sections>' +
      (page.sectionsList || []).map(s => '<li class="technical-form__row" data-layout-section="' + esc(s.id) + '"><span class="technical-form__row-title">' + esc(fill(L.sectionRow, { id: s.id, slot: s.slot })) + '</span>' + (s.built_in ? '<span class="technical-form__hint">' + esc(L.builtIn) + '</span>' : '') + '</li>').join('') + '</ul></section>'
      : '<p class="technical-form__fact" data-layout-slots>' + esc(L.noneSet) + '</p>') + '</div>';
  if (root.technicalDrawn !== html) { root.technicalDrawn = html; root.innerHTML = html; }
}
