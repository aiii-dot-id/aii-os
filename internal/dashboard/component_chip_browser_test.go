//go:build !windows

package dashboard

import "testing"

// THE CHIP (spec 5a): a <span> that says its text, or a <button> when it can
// be pressed; every tone drawn, the text always there, so the colour is
// never the only signal.
func TestComponentChip(t *testing.T) {
	body := `
import { run } from '/__harness.js';
import { everyControlNamed } from '/__checks.js';
import { render } from '/components/chip/chip.js';
run(async (assert) => {
  const root = document.getElementById('root');
  const tones = ['neutral', 'accent', 'good', 'warn', 'bad'].map(tone => render({ text: tone + ' chip', tone }));
  let pressed = 0;
  const press = render({ text: '2 updates', tone: 'accent', onPress: () => pressed++ });
  root.append(...tones, press);
  for (const c of tones) assert(c.tagName === 'SPAN' && c.textContent.endsWith(' chip'), 'a static chip is not a span with its text');
  assert(new Set(tones.map(c => getComputedStyle(c).color)).size >= 4, 'the tones are not drawn apart');
  assert(press.tagName === 'BUTTON' && press.type === 'button', 'a pressable chip is not a button');
  press.click();
  assert(pressed === 1, 'a pressable chip did not call onPress');
  assert(press.getBoundingClientRect().height >= 24, 'a pressable chip is under 24 px tall');
  assert(everyControlNamed(root, assert) === 1, 'the one control was not checked');
});`
	runPageInEngines(t, componentPage(body, "chip"), componentModules(t, "chip"))
}
