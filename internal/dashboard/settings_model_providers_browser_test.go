//go:build !windows

// Runs only through the unix browser-engine rig (browserengine_test.go,
// same tag) — this file calls the board's helpers, so it shares its build row.

package dashboard

import "testing"

// THE MODEL PAGE'S PROVIDERS, AS THE BOARD DRAWS THEM (step 15 Model b; host
// 0632 rulings; host 1146: "plan Max" crossed the row's line at 390). On
// #/settings/model at a phone's width:
//   - Providers is a section with its own heading, and the page still says
//     its own name once;
//   - each row's state line says how the provider is reached and whether it
//     answers, in the board's words, and says "No key" only when the status
//     backs it (an entry whose key comes from the environment has none stored
//     and is not keyless);
//   - Manage is a real button: it opens and closes that provider's editor,
//     says which with aria-expanded, and keeps focus across its own redraw
//     and across an answer's redraw;
//   - a row's note stays inside its row and inside the screen.
//
// The providers come from the server's answer, as on a real page: written
// into the page by hand, the next answer to "providers" (the local-model
// part asks once when it is first drawn) replaced them.
func TestModelProvidersAreRowsWithStateAndManage(t *testing.T) {
	const refusedWhy = "the provider refused the credential it was shown: key rejected (401)"
	dir := &ProviderDirectory{Providers: []ProviderInfo{
		{Name: "ollama", Chat: true, Local: true, APIType: "openai", Endpoint: "http://127.0.0.1:11434/v1", Models: []string{"a", "b", "c"}, Status: "ok"},
		{Name: "kept", Chat: true, APIType: "openai", Endpoint: "https://kept.test/v1", Models: []string{"m"}, HasKey: true, Status: "ok", Default: true},
		{Name: "fromenv", Chat: true, APIType: "openai", Endpoint: "https://env.test/v1", APIKeyEnv: "GATEWAY_KEY", Models: []string{"m"}, Status: "ok"},
		{Name: "keyless", Chat: true, APIType: "openai", Endpoint: "https://keyless.test/v1", Status: "auth_required"},
		{Name: "anthropic", Chat: true, APIType: "anthropic", Endpoint: "https://api.anthropic.test", Models: []string{"m"}, Credential: "claude-code", CredentialInfo: &CredentialInfo{Kind: "claude-code", Plan: "Max"}, Status: "ok", StatusReason: "plan Max — weekly limit resets Monday at 09:00 in the account owner's own time zone"},
		{Name: "refused", Chat: true, APIType: "openai", Endpoint: "https://refused.test/v1", HasKey: true, Status: "key_refused", StatusReason: refusedWhy},
		{Name: "local-refused", Chat: true, Local: true, APIType: "openai", Endpoint: "http://127.0.0.1:8081/v1", Models: []string{"m", "n"}, HasKey: true, Status: "key_refused", StatusReason: refusedWhy},
	}}
	runBoardProviders(t, 390, 844, dir, `
  await go('#/settings/model', () => shown(document.getElementById('view-settings')));
  if (!shown(document.getElementById('view-settings'))) return fail('#/settings/model at 390 drew no page');
  const { renderSettings } = await import('/views/settings.js');
  const row = i => document.querySelector('[data-prov-row="' + i + '"]');
  await until(() => shown(row(4)));
  if (!shown(row(4))) return fail('the provider rows were not drawn on #/settings/model');

  const sec = document.querySelector('[data-providers]');
  const h = sec && document.getElementById(sec.getAttribute('aria-labelledby'));
  if (!h || h.textContent.trim() !== 'Providers') return fail('Providers has no heading of its own: ' + (h && h.textContent));
  const names = [...document.querySelectorAll('#settings-stack h1, #settings-stack h2')].filter(shown).map(e => e.textContent.trim());
  if (names.filter(n => n === 'Model').length !== 1) return fail('the page does not say its name once: ' + JSON.stringify(names));

  const state = i => (row(i).querySelector('.settings-access__row-words > .settings-access__hint') || {}).textContent || '';
  // A key the provider refused says so, and what to do: not "kept", not "not answering".
  const want = { 0: 'Local · 3 models · Answering', 1: 'Key kept · Answering', 2: 'Answering', 4: 'Signed in · Answering', 5: 'Key refused: paste a new one under Manage', 6: 'Local · 2 models · Key refused: paste a new one under Manage' };
  for (const [i, w] of Object.entries(want)) if (state(i) !== w) return fail('row ' + i + ' says ' + JSON.stringify(state(i)) + ', not ' + JSON.stringify(w));
  if (!state(3).startsWith('No key · Not answering: ') || state(3).length <= 'No key · Not answering: '.length) return fail('a keyless row says ' + JSON.stringify(state(3)));
  if (/no key/i.test(row(2).textContent)) return fail('a key from the environment was called no key: ' + JSON.stringify(row(2).textContent));

  const manage = i => document.querySelector('[data-prov-manage="' + i + '"]');
  const editorOpen = i => !!document.querySelector('[data-prov-commit="' + i + '"]');
  if (!manage(1) || manage(1).tagName !== 'BUTTON') return fail('a row has no Manage button');
  if (manage(1).getAttribute('aria-expanded') !== 'false' || editorOpen(1)) return fail('a closed row says it is open');
  manage(1).focus(); manage(1).click();
  await until(() => editorOpen(1));
  if (!editorOpen(1)) return fail('Manage did not open the provider');
  if (manage(1).getAttribute('aria-expanded') !== 'true') return fail('an open row says aria-expanded=' + manage(1).getAttribute('aria-expanded'));
  if (document.activeElement !== manage(1)) return fail('Manage lost focus when it opened its editor: ' + (document.activeElement && document.activeElement.outerHTML.slice(0, 80)));
  renderSettings(); renderSettings();
  if (document.activeElement !== manage(1)) return fail('Manage lost focus to an answer\'s redraw: ' + (document.activeElement && document.activeElement.outerHTML.slice(0, 80)));
  if (!editorOpen(1)) return fail('an answer\'s redraw closed the open provider');
  manage(1).click();
  await until(() => !editorOpen(1));
  if (editorOpen(1) || manage(1).getAttribute('aria-expanded') !== 'false') return fail('Manage did not close the provider');

  const note = [...row(4).querySelectorAll('.settings-access__hint')].find(e => e.textContent.includes('plan Max'));
  if (!note) return fail('the row\'s note is not inside its row');
  const edge = window.innerWidth, r = row(4).getBoundingClientRect(), n = note.getBoundingClientRect();
  if (n.bottom > r.bottom + 0.5 || n.top < r.top - 0.5) return fail('the note crosses its row: note ' + n.top + '–' + n.bottom + ', row ' + r.top + '–' + r.bottom);
  if (r.right > edge + 0.5 || n.right > edge + 0.5) return fail('the row or its note reaches past the screen at ' + edge);
  const page = document.scrollingElement;
  if (page.scrollWidth > page.clientWidth) return fail('the page scrolls sideways: ' + page.scrollWidth + ' wide in ' + page.clientWidth);
`)
}
