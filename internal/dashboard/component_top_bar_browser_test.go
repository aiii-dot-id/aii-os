//go:build !windows

package dashboard

import "testing"

// THE TOP BAR (spec 3 and 5a). On a desktop: their name, which opens Home,
// and three places, Projects, About {name} and Settings, the open one marked
// aria-current; "Your AI identity" and "About them" before a name; no Search
// button until there is a search to open; what the frame passes in kept in
// it. At phone widths: the four places, Home among them, along the bottom,
// each at least 44 px tall, the name still above. Measured as the boards
// TopBar and PhoneNav draw it: a 64 px bar 32 px in, the name in the voice
// face at 20/28, places 15/22 with 8 by 12 inside and no icon; at phone
// widths a 56 px bar, and under each place's 13/18 word a 20 px icon.
func TestComponentTopBar(t *testing.T) {
	desktop := `
import { run } from '/__harness.js';
import { everyControlNamed, nameOf } from '/__checks.js';
import { render, update } from '/components/top-bar/top-bar.js';
run(async (assert) => {
  const root = document.getElementById('root');
  const kept = document.createElement('button'); kept.type = 'button'; kept.id = 'kept'; kept.textContent = 'Theme';
  const bar = render({ end: [kept] });
  root.append(bar, bar.rail);
  const shown = () => [...bar.rail.querySelectorAll('nav a')].filter(a => a.getClientRects().length).map(a => a.innerText);
  assert(nameOf(bar.querySelector('.c-top-bar__who')) === 'Your AI identity, the conversation', 'before a name the bar does not say "Your AI identity": ' + nameOf(bar.querySelector('.c-top-bar__who')));
  assert(shown().join('|') === 'Chat|Projects|About them|Record|Plugins|Settings', 'the sidebar is not Chat, Projects, About them, Record, Plugins and Settings: ' + shown().join('|'));
  update(bar, { name: 'Mira', active: 'identity' });
  assert(bar.querySelector('#p-name').textContent === 'Mira' && nameOf(bar.querySelector('.c-top-bar__who')) === 'Mira, the conversation', 'the name was not said');
  assert(shown().join('|') === 'Chat|Projects|About Mira|Record|Plugins|Settings', 'About does not carry the name: ' + shown().join('|'));
  const current = [...bar.rail.querySelectorAll('[aria-current="page"]')].map(a => a.dataset.view);
  assert(current.join() === 'identity', 'the open place is not the one marked: ' + current);
  update(bar, { name: 'Mira', active: 'projects' });
  assert([...bar.rail.querySelectorAll('[aria-current="page"]')].map(a => a.dataset.view).join() === 'projects', 'the mark did not move');
  assert([...bar.rail.querySelectorAll('nav a')].map(a => a.getAttribute('href')).join() === '#/chat,#/projects,#/identity,#/identity/record,#/settings/permissions,#/settings', 'the links do not go to their addresses');
  assert(!bar.querySelector('.c-top-bar__search'), 'a Search button is drawn with nothing to open');
  assert(bar.contains(kept), 'what the frame passed in is not kept');
  let searched = 0;
  const withSearch = render({ onSearch: () => searched++ });
  root.append(withSearch);
  withSearch.querySelector('.c-top-bar__search').click();
  assert(searched === 1 && /Ctrl K/.test(nameOf(withSearch.querySelector('.c-top-bar__search'))), 'Search is not a named button that calls onSearch');
  everyControlNamed(root, assert);
  for (const a of [...bar.querySelectorAll('a'), ...bar.rail.querySelectorAll('a')]) { const r = a.getBoundingClientRect(); if (r.width) assert(r.height >= 24, a.textContent + ' is under 24 px'); }
  const cs = getComputedStyle(bar), name = getComputedStyle(bar.querySelector('#p-name')), place = getComputedStyle(bar.rail.querySelector('nav a[data-view="projects"]'));
  assert(bar.getBoundingClientRect().height === 64 && cs.paddingRight === '32px', 'the bar is not 64 px tall and 32 px in at its end: ' + bar.getBoundingClientRect().height + ' ' + cs.paddingRight);
  assert(name.fontSize === '20px' && name.lineHeight === '28px' && name.fontWeight === '600' && /Iowan|Charter|Georgia/.test(name.fontFamily), 'the name is not the voice face at 20/28: ' + name.fontFamily + ' ' + name.fontSize);
  assert(place.fontSize === '15px' && place.lineHeight === '22px' && parseFloat(place.minHeight) === 40 && place.paddingLeft === '8px', 'a link is not 15/22, 40 tall, 8 in: ' + place.fontSize + ' ' + place.lineHeight + ' ' + place.minHeight + ' ' + place.paddingLeft);
  assert([...bar.rail.querySelectorAll('nav a')].every(a => a.querySelector('svg').getClientRects().length), 'a link of the sidebar has no icon');
});`
	modules := componentModules(t, "top-bar")
	runPageInEnginesAtSize(t, componentPage(desktop, "top-bar"), modules, 1280, 800)
	phone := `
import { run } from '/__harness.js';
import { render } from '/components/top-bar/top-bar.js';
run(async (assert) => {
  const bar = render({ name: 'Mira', active: 'chat' });
  document.getElementById('root').append(bar, bar.rail);
  const shown = [...bar.rail.querySelectorAll('nav a')].filter(a => a.getClientRects().length);
  assert(shown.map(a => a.innerText).join('|') === 'Chat|Projects|Mira|Settings', 'the phone bar does not hold the four places: ' + shown.map(a => a.innerText).join('|'));
  const nav = bar.rail.querySelector('nav').getBoundingClientRect();
  assert(Math.abs(nav.bottom - innerHeight) < 2, 'the places are not along the bottom: ' + nav.bottom + ' of ' + innerHeight);
  for (const a of shown) { const r = a.getBoundingClientRect(); assert(r.height >= 44, a.textContent + ' is ' + r.height + ' px tall at phone width'); }
  const who = bar.querySelector('.c-top-bar__who').getBoundingClientRect();
  assert(who.top < 60 && who.height > 0, 'the name is not above');
  assert(shown[0].getAttribute('aria-current') === 'page', 'Home is not marked open');
  assert(Math.round(bar.getBoundingClientRect().height) === 56, 'the phone bar is not 56 px tall: ' + bar.getBoundingClientRect().height);
  for (const a of shown) {
    const icon = a.querySelector('svg'), r = icon && icon.getBoundingClientRect(), cs = getComputedStyle(a);
    assert(r && r.width === 20 && r.height === 20 && icon.getAttribute('aria-hidden') === 'true', a.innerText + ' has no 20 px icon hidden from the reader');
    assert(cs.fontSize === '13px' && cs.lineHeight === '18px' && r.bottom <= [...a.querySelectorAll('span')].find(s => s.getClientRects().length).getBoundingClientRect().top + 1, a.innerText + ' is not a 13/18 word under its icon');
    assert(a.getBoundingClientRect().height >= 52, a.innerText + ' is under 52 px');
  }
});`
	runPageInEnginesAtSize(t, componentPage(phone, "top-bar"), modules, 390, 844)
}
