//go:build !windows

package dashboard

import "testing"

// MORE OPTIONS KEEPS ITS LEVEL AND ITS PLACE (step 15 Model b, the board's
// More options). The Model card is drawn from markup on every frame, and
// frames land while a person is choosing, so what must outlast a redraw is
// asked of a redraw here, in the order a person meets it:
//   - the three waits and the per-model timeout overrides are under More
//     options (nothing reachable on the page today goes away);
//   - More options opened by its summary stays open through a redraw;
//   - the thinking menu shows the level in force, sends effort_set when a
//     level is chosen, and, focused, is not taken from under the keyboard by
//     frames that land before the answer (it shows the level sent);
//   - a refused level goes back to the level in force;
//   - Save never sends the thinking level (the menu is data-immediate);
//   - with an unsaved provider the menu is inert, because effort_set saves
//     the level for the model in use.
const settingsModelMorePage = substratePageMarkup + `<script type="module">
import { S } from './state.js';
import { frames } from './ws.js';
import { renderSettings, effortAnswered } from './views/settings.js';
import { assert, run } from './__harness.js';
run(async () => {
  const el = id => document.getElementById(id);
  S.identityExists = true;
  S.view = 'settings';
  S.providersLoaded = true;
  S.providers = [{ name: 'alpha', models: ['a1', 'a2'], chat: true }, { name: 'beta', models: ['b1'], chat: true }];
  S.config = { llm: { provider: 'alpha', model: 'a1', resolved_provider: 'alpha', resolved_model: 'a1', timeout_seconds: 120, probe_timeout_seconds: 20, refused_retry_max_default: 300,
      effort_choice: { levels: ['low', 'medium', 'high'], in_force: 'medium', checked: ['high'] } },
    cognition: { settings: [{ key: 'llm.timeout_seconds', per_model: 'model', card: 'substrate', min: 1, max: 3600, default: 120, unit: 'seconds', effective: { value: 120, level: 'default' } }] },
    prompt: { max_tokens: 1000, recent_turns: 5 }, dashboard: { host: '127.0.0.1', port: 8180, tls: false } };
  const frame = i => { S.config = { ...S.config, llm: { ...S.config.llm, probe_timeout_seconds: 21 + i } }; renderSettings(); };
  renderSettings();
  let more = document.querySelector('[data-more]');
  assert(more && !more.open, 'More options is not drawn, or is drawn open');
  assert(more.contains(el('cfg-timeout')) && more.contains(el('cfg-probe-timeout')) && more.contains(el('cfg-refused-retry')),
    'the three waits are not under More options');
  assert(!more.innerHTML.includes('llm.timeout_seconds') && !more.innerHTML.includes('llm-timeout-seconds'),
    'the per-provider timeout rows are drawn under More options: they live on Technical');

  // Opened as a person opens it, and the page hears it before the next frame.
  const toggled = new Promise(r => more.addEventListener('toggle', r, { once: true }));
  more.querySelector('summary').click();
  await toggled;
  frame(0);
  more = document.querySelector('[data-more]');
  assert(more && more.open, 'a redraw closed More options');

  const menu = el('model-effort');
  assert(menu && menu.value === 'medium', 'the menu does not show the level in force: ' + (menu && menu.value));
  assert(!menu.disabled, 'the menu is inert with nothing unsaved');
  menu.focus();
  menu.value = 'high';
  menu.dispatchEvent(new Event('change', { bubbles: true }));
  const sets = frames.filter(f => f.type === 'effort_set');
  assert(sets.length === 1 && sets[0].effort === 'high', 'choosing high sent ' + JSON.stringify(sets));
  // The level acts through its own frame, so choosing it is not an unsaved
  // edit: no draft notice, and nothing holds a reload for it.
  const notice = document.querySelector('[data-settings-draft]');
  assert(notice, 'the page draws no unsaved-edits notice to check');
  assert(notice.hidden, 'choosing a level counted as an unsaved edit');

  // Frames land before the answer, with the menu focused.
  for (let i = 1; i <= 3; i++) frame(i);
  assert(el('model-effort') === menu, 'a redraw replaced the focused menu');
  assert(document.querySelector('[data-settings-draft]').hidden, 'after a redraw the chosen level counted as an unsaved edit');
  assert(document.activeElement === menu, 'a redraw took focus from the menu');
  assert(menu.value === 'high', 'before its answer the menu does not show the level sent: ' + menu.value);
  assert(el('cfg-probe-timeout').value === '24', 'the rest of the card did not follow the frames');

  // Refused: the socket takes the answer for this request, then draws again.
  assert(effortAnswered(sets[0].request_id), 'the refusal was not taken for the level sent');
  assert(!effortAnswered(sets[0].request_id), 'one answer was taken twice');
  renderSettings();
  assert(el('model-effort').value === 'medium', 'a refused level stayed on the menu: ' + el('model-effort').value);

  // Save sends the waits and never the level.
  const timeout = el('cfg-timeout');
  timeout.value = '150';
  timeout.dispatchEvent(new Event('input', { bubbles: true }));
  document.querySelector('[data-save="llm"]').click();
  const saved = frames.filter(f => f.type === 'config_set').at(-1);
  assert(saved && Object.keys(saved.config).some(k => k === 'llm.timeout_seconds'), 'Save did not send the timeout: ' + JSON.stringify(saved && saved.config));
  assert(Object.keys(saved.config).every(k => !/effort/i.test(k)), 'Save sent the thinking level: ' + JSON.stringify(saved.config));
  assert(frames.filter(f => f.type === 'effort_set').length === 1, 'Save sent another effort_set');

  // An unsaved provider makes the menu inert.
  const provider = el('cfg-provider');
  provider.value = 'beta';
  provider.dispatchEvent(new Event('input', { bubbles: true }));
  provider.dispatchEvent(new Event('change', { bubbles: true }));
  assert(el('model-effort').disabled, 'the menu acts while an unsaved provider is chosen');
});
</script>`

func TestMoreOptionsKeepsItsLevelAndItsPlace(t *testing.T) {
	runPageInEngines(t, settingsModelMorePage, settingsDraftModules(t))
}

// MORE OPTIONS NAMES ONLY WHAT IT HOLDS (host 1952). The thinking menu is
// drawn only for a model with thinking levels; without one, a summary that
// says "thinking" names a control that is not there.
const settingsModelMoreHintPage = substratePageMarkup + `<script type="module">
import { S } from './state.js';
import { renderSettings } from './views/settings.js';
import { assert, run } from './__harness.js';
run(() => {
  S.identityExists = true;
  S.view = 'settings';
  S.providersLoaded = true;
  S.providers = [{ name: 'alpha', models: ['a1'], chat: true }];
  const draw = choice => {
    const llm = { provider: 'alpha', model: 'a1', resolved_provider: 'alpha', resolved_model: 'a1', timeout_seconds: 120, probe_timeout_seconds: 20, refused_retry_max_default: 300 };
    if (choice) llm.effort_choice = choice;
    S.config = { llm, cognition: { settings: [] }, prompt: { max_tokens: 1000, recent_turns: 5 }, dashboard: { host: '127.0.0.1', port: 8180, tls: false } };
    renderSettings();
    const summary = document.querySelector('[data-more] summary');
    assert(summary, 'More options is not drawn');
    return { hint: summary.textContent.replace(/\s+/g, ' ').trim(), menu: document.getElementById('model-effort') };
  };
  for (const [what, choice] of [['no effort choice', null], ['no levels', { levels: [], in_force: '' }]]) {
    const { hint, menu } = draw(choice);
    assert(!menu, 'with ' + what + ' a thinking menu was drawn');
    assert(hint.endsWith('· longest wait') && !hint.includes('thinking'), 'with ' + what + ' the summary names a control that is not there: ' + hint);
  }
  const { hint, menu } = draw({ levels: ['low', 'high'], in_force: 'low' });
  assert(menu, 'with levels no thinking menu was drawn');
  assert(hint.endsWith('· thinking, longest wait'), 'with levels the summary does not name the thinking menu: ' + hint);
});
</script>`

func TestMoreOptionsNamesOnlyWhatItHolds(t *testing.T) {
	runPageInEngines(t, settingsModelMoreHintPage, settingsDraftModules(t))
}

// A FOCUSED THINKING MENU FOLLOWS THE LEVELS ANSWERED. The menu is kept through a redraw while it
// has focus. A read-back for a model with other levels gives it the new list, and it stays the
// same focused menu. One where only the level in force moved leaves its options as they are, so a
// menu held open is not closed under the keyboard.
const settingsModelMenuPage = substratePageMarkup + `<script type="module">
import { S } from './state.js';
import { renderSettings } from './views/settings.js';
import { assert, run } from './__harness.js';
run(async () => {
  S.identityExists = true;
  S.view = 'settings';
  S.providersLoaded = true;
  S.providers = [{ name: 'alpha', models: ['a1'], chat: true }];
  const read = choice => { S.config = { llm: { provider: 'alpha', model: 'a1', resolved_provider: 'alpha', resolved_model: 'a1', timeout_seconds: 120, probe_timeout_seconds: 20, effort_choice: choice },
    cognition: { settings: [] }, prompt: { max_tokens: 1000, recent_turns: 5 }, dashboard: { host: '127.0.0.1', port: 8180, tls: false } }; renderSettings(); };
  read({ levels: ['low', 'high'], in_force: 'low' });
  const more = document.querySelector('[data-more]');
  const toggled = new Promise(r => more.addEventListener('toggle', r, { once: true }));
  more.querySelector('summary').click();
  await toggled;
  const menu = document.getElementById('model-effort');
  menu.focus();
  assert(document.activeElement === menu, 'the menu did not take focus');

  const values = () => [...document.getElementById('model-effort').options].map(o => o.value).join(',');
  const kept = () => document.getElementById('model-effort') === menu && document.activeElement === menu;
  read({ levels: ['low', 'max'], in_force: 'max' });
  assert(kept(), 'an answer with other levels took the menu from under the keyboard');
  assert(values() === ',low,max', 'the focused menu offers: ' + values());
  assert(menu.value === 'max', 'the focused menu shows: ' + menu.value);

  const option = menu.options[1];
  read({ levels: ['low', 'max'], in_force: 'low' });
  assert(kept() && menu.options[1] === option, 'an answer with the same levels redrew the options of a menu in use');
  assert(menu.value === 'low', 'the focused menu shows: ' + menu.value);
});
</script>`

func TestAFocusedThinkingMenuFollowsTheLevelsAnswered(t *testing.T) {
	runPageInEngines(t, settingsModelMenuPage, settingsDraftModules(t))
}
