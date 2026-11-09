//go:build !windows

package dashboard

import "testing"

// THE AII OS NOTE (spec 4 and 5a): role status for info and warn and alert
// for bad; "AII OS" in its own element when the text begins so; what it
// holds open, its actions under it; said again in the same element, which
// stays in the page.
func TestComponentOsNote(t *testing.T) {
	body := `
import { run } from '/__harness.js';
import { everyControlNamed } from '/__checks.js';
import { render, update } from '/components/os-note/os-note.js';
run(async (assert) => {
  const root = document.getElementById('root');
  const roles = ['info', 'warn', 'bad'].map(tone => render({ tone, text: 'AII OS can\'t reach aiii.id.' }));
  root.append(...roles);
  assert(roles.map(n => n.getAttribute('role')).join() === 'status,status,alert', 'the tones do not carry their roles');
  const lead = roles[2].querySelector('strong');
  assert(lead && lead.textContent === 'AII OS' && roles[2].textContent === 'AII OS can\'t reach aiii.id.', 'AII OS is not its own element at the start');
  assert(getComputedStyle(lead).color !== getComputedStyle(roles[2].querySelector('p')).color, 'AII OS is not drawn in the tone');
  const plain = render({ text: 'This page has changed.' });
  root.append(plain);
  assert(!plain.querySelector('strong'), 'a note not beginning AII OS was given the words');
  const details = document.createElement('details'); details.innerHTML = '<summary>Where everything went</summary><ul><li>a</li></ul>';
  const button = document.createElement('button'); button.type = 'button'; button.textContent = 'Got it';
  const full = render({ text: 'This page has changed.', children: [details], actions: [button] });
  root.append(full);
  assert(full.contains(details) && full.querySelector('.c-os-note__actions').contains(button), 'children and actions are not held');
  update(full, { text: 'AII OS checked it.' });
  assert(full.isConnected && full.textContent === 'AII OS checked it.' && !full.contains(details), 'update did not say it again in place');
  assert(everyControlNamed(root, assert) === 0, 'the note left a control behind');
});`
	runPageInEngines(t, componentPage(body, "os-note"), componentModules(t, "os-note"))
}
