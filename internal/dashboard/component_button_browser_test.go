//go:build !windows

package dashboard

import "testing"

// THE BUTTON (spec 5a): a native <button type="button"> in each kind, an
// icon-only one named by its label, busy and disabled set and cleared in
// place, onPress called on a press and not while busy; at least 24 by 24
// px, and 44 at phone widths.
func TestComponentButton(t *testing.T) {
	body := `
import { run } from '/__harness.js';
import { everyControlNamed } from '/__checks.js';
import { render, update } from '/components/button/button.js';
run(async (assert) => {
  const root = document.getElementById('root');
  let pressed = 0;
  const kinds = ['primary', 'secondary', 'quiet', 'danger'].map(kind => render({ label: 'Save ' + kind, kind, onPress: () => pressed++ }));
  const svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
  const icon = render({ label: 'Copy', icon: svg, onPress: () => pressed++ });
  root.append(...kinds, icon);
  for (const b of kinds) {
    assert(b.tagName === 'BUTTON' && b.type === 'button', 'not a native button of type button');
    const r = b.getBoundingClientRect();
    assert(r.width >= 24 && r.height >= 24, b.textContent + ' is ' + r.width + ' by ' + r.height);
  }
  assert(icon.getAttribute('aria-label') === 'Copy' && icon.textContent === '', 'an icon-only button is not named by its label');
  kinds[0].click(); icon.click();
  assert(pressed === 2, 'onPress was not called for each press: ' + pressed);
  update(kinds[0], { busy: true });
  assert(kinds[0].disabled && kinds[0].getAttribute('aria-busy') === 'true', 'busy is not disabled and said');
  kinds[0].click();
  assert(pressed === 2, 'a busy button acted');
  update(kinds[0], { busy: false });
  assert(!kinds[0].disabled && !kinds[0].hasAttribute('aria-busy'), 'busy did not clear');
  kinds[1].focus();
  assert(document.activeElement === kinds[1], 'a button cannot take focus');
  assert(everyControlNamed(root, assert) === 5, 'not every button was checked');
});`
	runPageInEngines(t, componentPage(body, "button"), componentModules(t, "button"))
	runPageInEnginesAtSize(t, componentPage(`
import { run } from '/__harness.js';
import { render } from '/components/button/button.js';
run(async (assert) => {
  const b = render({ label: 'Go' }); document.getElementById('root').append(b);
  const r = b.getBoundingClientRect();
  assert(r.width >= 44 && r.height >= 44, 'at phone width a button is ' + r.width + ' by ' + r.height);
});`, "button"), componentModules(t, "button"), 390, 844)
}
