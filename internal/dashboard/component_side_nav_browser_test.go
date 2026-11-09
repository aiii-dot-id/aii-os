//go:build !windows

package dashboard

import "testing"

// THE SIDE NAV (spec 5a; the board SettingsNav): a <nav> with its name, a
// heading, then one link per item, each an address; the open one is
// aria-current and drawn on the second surface; a count shows beside an
// item's words; a rule above an item that asks for one, as the board
// SettingsList draws it; update marks another item open in the same element.
func TestComponentSideNav(t *testing.T) {
	body := `
import { run } from '/__harness.js';
import { everyControlNamed, nameOf } from '/__checks.js';
import { render, update } from '/components/side-nav/side-nav.js';
run(async (assert) => {
  const root = document.getElementById('root');
  const items = [{ id: 'all', label: 'All settings', href: '#/settings' }, { id: 'permissions', label: 'Plugins and permissions', href: '#/settings/permissions', count: 1 }];
  const nav = render({ title: 'Settings', items, selected: 'all' });
  root.append(nav);
  assert(nav.tagName === 'NAV' && nameOf(nav) === 'Settings', 'the list is not a navigation named Settings');
  assert(nav.querySelector('h2').textContent === 'Settings', 'the heading is not drawn');
  const links = [...nav.querySelectorAll('a')];
  assert(links.map(a => a.getAttribute('href')).join() === '#/settings,#/settings/permissions', 'the items are not links to their addresses');
  assert(links[0].getAttribute('aria-current') === 'page' && !links[1].hasAttribute('aria-current'), 'the open item is not the current one');
  const on = getComputedStyle(links[0]), off = getComputedStyle(links[1]);
  assert(on.backgroundColor !== off.backgroundColor && on.fontWeight === '600', 'the open item is not drawn apart');
  assert(on.fontSize === '15px' && on.lineHeight === '22px' && on.paddingTop === '8px' && on.paddingLeft === '12px', 'an item is not 15/22 with 8 by 12 inside: ' + on.fontSize + '/' + on.lineHeight + ' ' + on.paddingTop + ' ' + on.paddingLeft);
  assert(links[1].textContent === 'Plugins and permissions1' && links[1].querySelector('.c-chip'), 'the count is not beside the words');
  links[1].focus();
  update(nav, { title: 'Settings', items, selected: 'permissions' });
  assert(nav.querySelector('[aria-current="page"]').dataset.item === 'permissions', 'update did not move the current item');
  assert([...nav.querySelectorAll('a')].every((a, i) => a === links[i]), 'update drew the links again instead of marking them');
  assert(document.activeElement === links[1], 'the focused link lost its focus when it was marked open');
  update(nav, { title: 'Settings', items: [items[0], Object.assign({}, items[1], { count: 0 })], selected: 'permissions' });
  assert(!links[1].querySelector('.c-chip') && document.activeElement === links[1], 'a count gone took the chip and kept the focus');
  // The board's rule (rule: true): a 1 px line, 8 px clear of the item
  // above and of its own, between the items' 2 px gaps: 2 + 8 + 1 + 8 + 2.
  const between = () => Math.round(links[1].getBoundingClientRect().top - links[0].getBoundingClientRect().bottom);
  update(nav, { title: 'Settings', items: [items[0], Object.assign({}, items[1], { rule: true })], selected: 'permissions' });
  const line = getComputedStyle(links[1], '::before');
  assert(links[1].hasAttribute('data-rule') && !links[0].hasAttribute('data-rule'), 'the rule is not on the item that asks for it alone');
  assert(line.borderTopWidth === '1px' && line.borderTopStyle === 'solid' && line.top === '-11px', 'the rule is not a 1 px line 8 px clear of its item: ' + line.borderTopWidth + ' ' + line.borderTopStyle + ' at ' + line.top);
  assert(between() === 21, 'the rule does not leave 8 px clear on either side of its line: ' + between() + ' px between the items, not 21');
  update(nav, { title: 'Settings', items, selected: 'permissions' });
  assert(!links[1].hasAttribute('data-rule') && between() === 2, 'a rule taken away stayed: ' + between() + ' px between the items');
  for (const a of nav.querySelectorAll('a')) assert(a.getBoundingClientRect().height >= 24, a.textContent + ' is under 24 px tall');
  assert(everyControlNamed(root, assert) === 2, 'the two links were not checked');
});`
	runPageInEngines(t, componentPage(body, "side-nav", "chip"), componentModules(t, "side-nav", "chip"))
}
