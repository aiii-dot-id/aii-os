//go:build !windows

package dashboard

import "testing"

// THE TABS (spec 5a): a tablist of tab buttons, the selected one the only tab
// stop; Right and Left move to the next tab and select it, wrapping, Home and
// End to the ends; a click selects; onSelect hears every choice; the panel is
// labelled by the selected tab.
func TestComponentTabs(t *testing.T) {
	body := `
import { run } from '/__harness.js';
import { everyControlNamed } from '/__checks.js';
import { render, update, panelFor } from '/components/tabs/tabs.js';
run(async (assert) => {
  const root = document.getElementById('root');
  const chose = [];
  const list = render({ tabs: [{ id: 'who', label: 'Who they are' }, { id: 'beliefs', label: 'Beliefs' }, { id: 'record', label: 'Record' }],
    selected: 'beliefs', label: 'About Mira', idPrefix: 't', onSelect: id => chose.push(id) });
  const panel = document.createElement('section');
  root.append(list, panel);
  panelFor(list, panel);
  const tabs = [...list.querySelectorAll('[role="tab"]')];
  const on = () => tabs.filter(b => b.getAttribute('aria-selected') === 'true').map(b => b.dataset.tab);
  assert(list.getAttribute('role') === 'tablist' && list.getAttribute('aria-label') === 'About Mira', 'not a named tablist');
  assert(tabs.length === 3 && on().join() === 'beliefs', 'the selected tab is not the one given: ' + on());
  assert(tabs.map(b => b.tabIndex).join() === '-1,0,-1', 'the selected tab is not the only tab stop');
  assert(panel.getAttribute('role') === 'tabpanel' && panel.getAttribute('aria-labelledby') === 't-beliefs', 'the panel is not labelled by its tab');
  const key = k => document.activeElement.dispatchEvent(new KeyboardEvent('keydown', { key: k, bubbles: true }));
  tabs[1].focus(); key('ArrowRight');
  assert(on().join() === 'record' && document.activeElement === tabs[2], 'Right did not move to and select the next tab');
  key('ArrowRight');
  assert(on().join() === 'who' && document.activeElement === tabs[0], 'Right did not wrap to the first tab');
  key('ArrowLeft');
  assert(on().join() === 'record', 'Left did not wrap to the last tab');
  key('Home');
  assert(on().join() === 'who', 'Home did not select the first tab');
  key('End');
  assert(on().join() === 'record', 'End did not select the last tab');
  tabs[1].click();
  assert(on().join() === 'beliefs', 'a click did not select');
  assert(chose.join() === 'record,who,record,who,record,beliefs', 'onSelect did not hear every choice: ' + chose);
  update(list, { selected: 'who' });
  panelFor(list, panel);
  assert(on().join() === 'who' && panel.getAttribute('aria-labelledby') === 't-who', 'update did not move the selection and the panel');
  assert(everyControlNamed(root, assert) === 3, 'not every tab was checked');
});`
	runPageInEngines(t, componentPage(body, "tabs"), componentModules(t, "tabs"))
}
