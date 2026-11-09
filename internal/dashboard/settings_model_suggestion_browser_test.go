//go:build !windows

package dashboard

import "testing"

// A MODEL BEING PICKED IS NOT TAKEN AWAY BY AN ANSWER. Settings is drawn again
// on every config, tools, sandbox or choices frame, and a field taken out of
// the document even for a moment loses its open native suggestion list: on
// Windows a model highlighted with the arrow keys was cancelled by a config
// reply landing before Enter (a beta user, 2026-10-30; reproduced by a review in
// installed 0.2.0 with native keys, four config replies within about 97 ms of
// opening Settings). A page cannot open the browser's own
// suggestion list, so this test holds what that list depends on: through the
// frames the focused model field and its list are the same nodes and are
// never taken out and put back, the typed text and the caret stay, the list
// follows the provider, the rest of the page follows the frame, and the model
// picked is the one saved.
const settingsModelSuggestionPage = substratePageMarkup + `<script type="module">
import { S } from './state.js';
import { frames } from './ws.js';
import { renderSettings } from './views/settings.js';
import { assert, run } from './__harness.js';
run(() => {
  const el = id => document.getElementById(id);
  S.identityExists = true;
  S.view = 'settings';
  S.providersLoaded = true;
  S.providers = [{ name: 'fixture', default: true, models: ['base', 'draft', 'deep'], chat: true }];
  S.config = { llm: { provider: 'fixture', model: 'base', resolved_provider: 'fixture', resolved_model: 'base', timeout_seconds: 120, probe_timeout_seconds: 20 },
    prompt: { max_tokens: 1000, recent_turns: 5 }, dashboard: { host: '127.0.0.1', port: 8180, tls: false } };
  renderSettings();
  const input = el('cfg-model'), list = input && input.list;
  assert(list && list.id === 'cfg-model-list', 'the model field has no suggestion list');
  input.focus();
  input.value = 'dr';
  input.dispatchEvent(new Event('input', { bubbles: true }));
  input.setSelectionRange(2, 2);

  // Anything that puts the field or its list back into the page took it out first.
  const putBack = [];
  const watch = new MutationObserver(() => {});
  watch.observe(el('settings-stack'), { childList: true, subtree: true });
  const read = () => watch.takeRecords().forEach(r => r.addedNodes.forEach(n => {
    if (n === input || n === list || (n.contains && (n.contains(input) || n.contains(list)))) putBack.push(n.nodeName);
  }));

  // What the socket does with a config frame (ws.js): the new configuration,
  // then Settings drawn again. Four arrive within about 97 ms of opening it,
  // none of them changing the models, so the list's options are not written.
  const listWatch = new MutationObserver(() => {});
  listWatch.observe(list, { childList: true, subtree: true, characterData: true });
  for (let i = 0; i < 4; i++) {
    S.config = { ...S.config, llm: { ...S.config.llm, resolved_model: 'live-status-' + i, timeout_seconds: 321 + i } };
    renderSettings();
    read();
  }
  const rewritten = listWatch.takeRecords().length;
  listWatch.disconnect();
  assert(rewritten === 0, 'the suggestion list was rewritten by frames that did not change it: ' + rewritten + ' changes');
  // A frame that changes the list itself: the provider now serves another model.
  S.providers = [{ name: 'fixture', default: true, models: ['base', 'draft', 'deep', 'wide'], chat: true }];
  renderSettings();
  read();

  assert(putBack.length === 0, 'the model field or its list was taken out and put back: ' + putBack.join(', '));
  assert(el('cfg-model') === input && input.isConnected, 'the model field was replaced');
  assert(input.list === list && list.isConnected, 'the suggestion list was replaced');
  assert(document.activeElement === input, 'focus left the model field');
  assert(input.value === 'dr' && input.selectionStart === 2 && input.selectionEnd === 2, 'the typed text or the caret moved: ' + input.value);
  assert([...list.options].map(o => o.value).join(',') === 'base,draft,deep,wide', 'the list did not follow the provider');
  assert(el('cfg-timeout').value === '324', 'the rest of the page did not follow the frames');
  assert(el('settings-stack').textContent.includes('live-status-3'), 'the status did not follow the frames');

  // Enter takes the suggestion; the model picked is the one saved.
  input.value = 'draft';
  input.dispatchEvent(new Event('input', { bubbles: true }));
  document.querySelector('[data-save="llm"]').click();
  const sent = frames.filter(f => f.type === 'config_set').at(-1);
  assert(sent && sent.config['llm.model'] === 'draft', 'the picked model was not the one saved');

  // Away from the field, a frame draws the page whole as before.
  watch.disconnect();
  input.blur();
  renderSettings();
  assert(el('cfg-model').value === 'draft', 'the picked model did not survive leaving the field');
});
</script>`

func TestAModelBeingPickedIsNotTakenAwayByAnAnswer(t *testing.T) {
	runPageInEngines(t, settingsModelSuggestionPage, settingsDraftModules(t))
}

// THE KEEPING ITSELF (keep.js), on a page of its own: a kept field and its
// list stay the same nodes and take their twins' attributes and options but
// not the typed value; the nodes around them follow the markup and a status
// line that changed is announced; a kept node the new markup no longer has, or
// a path that changed shape, draws the whole as before.
const keepInPlacePage = `<!doctype html><div id="root"></div>
<script type="module">
import { said } from './announce.js';
import { renderKeeping } from './views/settings/keep.js';
import { assert, run } from './__harness.js';
run(() => {
  const root = document.getElementById('root'), el = id => document.getElementById(id);
  const page = (models, note) => '<div class="card"><label>MODEL</label><div class="f"><input id="m" list="m-list" value="base" placeholder="' + note + '">' +
    '<button type="button">Refresh</button><datalist id="m-list">' + models.map(m => '<option value="' + m + '"></option>').join('') + '</datalist></div>' +
    '<p data-announce>' + note + '</p></div>';
  root.innerHTML = page(['a', 'b'], 'one');
  const input = el('m'), list = input.list, button = root.querySelector('button');
  input.focus();
  input.value = 'typed';
  const watch = new MutationObserver(() => {});
  watch.observe(root, { childList: true, subtree: true });
  const twins = renderKeeping(root, page(['a', 'b', 'c'], 'two'), false, [input, list]);
  const putBack = watch.takeRecords().flatMap(r => [...r.addedNodes]).filter(n => n === input || n === list || (n.contains && (n.contains(input) || n.contains(list))));
  assert(twins && twins.get(input) && twins.get(input).id === 'm', 'no twins for the kept field');
  assert(putBack.length === 0 && el('m') === input && input.list === list, 'the kept field or its list was taken out and put back');
  assert(document.activeElement === input && input.value === 'typed', 'focus or the typed value was lost');
  assert(input.placeholder === 'two', 'the kept field did not take its twin\'s attributes');
  assert([...list.options].map(o => o.value).join(',') === 'a,b,c', 'the kept list did not take its twin\'s options');
  assert(root.querySelector('button') !== button && root.querySelector('p').textContent === 'two', 'the nodes around the kept ones did not follow the markup');
  assert(said.join('|') === 'two', 'the changed status line was not announced: ' + said.join('|'));
  assert(renderKeeping(root, '<p>gone</p>', true, [input, list]) === null && !input.isConnected && root.textContent === 'gone',
    'a kept node the markup no longer has did not draw the whole');
  root.innerHTML = page(['a'], 'three');
  const again = el('m');
  assert(renderKeeping(root, '<section>' + page(['a'], 'four') + '</section>', true, [again, again.list]) === null && !again.isConnected,
    'a path that changed shape did not draw the whole');
});
</script>`

func TestARebuildKeepsTheFieldInUseWhereItStands(t *testing.T) {
	keep, err := staticFS.ReadFile("static/views/settings/keep.js")
	if err != nil {
		t.Fatal(err)
	}
	runPageInEngines(t, keepInPlacePage, map[string][]byte{
		"/views/settings/keep.js": keep,
		"/announce.js": []byte(`export const said = [];
export function announce(text) { if (text) said.push(text); }
export function renderInto(root, html) { root.innerHTML = html; }`),
	})
}

// AN UNSAVED PROVIDER KEEPS ITS OWN LIST. With provider A saved and B chosen
// but not saved, the page shows B's models; a frame that changes nothing must
// not write A's list under the open suggestion and then B's back (a review,
// 2026-11-04 03:36Z: the markup built the list from the saved provider). The
// list is drawn for the provider the page shows, so an unchanged frame writes
// nothing, a change to B's models is followed, and Save sends B and the model.
// Refresh models asks the provider shown from the moment it is chosen, the
// default-flagged choice included (a review, 03:45Z: it asked the previous one
// until something drew the page again).
const settingsModelDraftProviderPage = substratePageMarkup + `<script type="module">
import { S } from './state.js';
import { frames } from './ws.js';
import { renderSettings } from './views/settings.js';
import { assert, run } from './__harness.js';
run(() => {
  const el = id => document.getElementById(id);
  S.identityExists = true;
  S.view = 'settings';
  S.providersLoaded = true;
  S.providers = [{ name: 'alpha', models: ['a1', 'a2'], chat: true }, { name: 'beta', models: ['b1', 'b2'], chat: true }, { name: 'gamma', default: true, models: ['g1'], chat: true }];
  S.config = { llm: { provider: 'alpha', model: 'a1', resolved_provider: 'alpha', resolved_model: 'a1', timeout_seconds: 120, probe_timeout_seconds: 20 },
    prompt: { max_tokens: 1000, recent_turns: 5 }, dashboard: { host: '127.0.0.1', port: 8180, tls: false } };
  renderSettings();
  const select = el('cfg-provider');
  const choose = name => { el('cfg-provider').value = name; el('cfg-provider').dispatchEvent(new Event('change', { bubbles: true })); };
  const refreshAsks = () => { document.querySelector('[data-refresh-models]').click(); const q = frames.filter(f => f.type === 'query' && f.query === 'discover').at(-1); return q && q.provider; };
  // Refresh asks the provider shown at the press, before anything is drawn again.
  choose('');
  let asked = refreshAsks();
  assert(asked === 'gamma', 'Refresh on the default-flagged choice asked ' + asked);
  renderSettings(); // the discovery answer draws the page again, and Refresh can be pressed
  choose('beta');
  asked = refreshAsks();
  assert(asked === 'beta', 'Refresh right after choosing beta asked ' + asked);
  const input = el('cfg-model'), list = input.list;
  assert([...list.options].map(o => o.value).join(',') === 'b1,b2', 'choosing beta did not show its models');
  input.focus();
  input.value = 'b';
  input.dispatchEvent(new Event('input', { bubbles: true }));
  const listWatch = new MutationObserver(() => {});
  listWatch.observe(list, { childList: true, subtree: true, characterData: true });
  for (let i = 0; i < 4; i++) {
    S.config = { ...S.config, llm: { ...S.config.llm, timeout_seconds: 130 + i } };
    renderSettings();
  }
  const written = listWatch.takeRecords().length;
  assert(written === 0, 'frames that changed nothing wrote the open list: ' + written + ' changes');
  assert(el('cfg-provider').value === 'beta' && el('cfg-model') === input && input.list === list, 'the draft provider or the field was lost');
  assert([...list.options].map(o => o.value).join(',') === 'b1,b2', 'the open list is not beta\'s');
  assert(el('cfg-timeout').value === '133', 'the rest of the page did not follow the frames');
  S.providers = [S.providers[0], { name: 'beta', models: ['b1', 'b2', 'b3'], chat: true }, S.providers[2]];
  renderSettings();
  listWatch.disconnect();
  assert([...list.options].map(o => o.value).join(',') === 'b1,b2,b3', 'the list did not follow beta\'s models');
  input.value = 'b3';
  input.dispatchEvent(new Event('input', { bubbles: true }));
  document.querySelector('[data-save="llm"]').click();
  const sent = frames.filter(f => f.type === 'config_set').at(-1);
  assert(sent && sent.config['llm.provider'] === 'beta' && sent.config['llm.model'] === 'b3',
    'the saved pair is not beta and b3: ' + JSON.stringify(sent && sent.config));
});
</script>`

func TestAnUnsavedProviderKeepsItsOwnList(t *testing.T) {
	runPageInEngines(t, settingsModelDraftProviderPage, settingsDraftModules(t))
}

// REFRESH IS OFFERED ONLY FOR A PROVIDER THAT RESOLVES. The shipped
// providers.json flags no entry as default, so the blank choice resolves no
// provider (a review, 2026-11-04 03:54Z; host 03:54Z, carried in 15 Model).
// Refresh was drawn only when a provider resolved and was never retargeted or
// hidden after: choosing the blank entry left it asking the previous provider,
// and on a page drawn with none, choosing one left it missing until the next
// draw. With no provider resolved Refresh is not offered and sends nothing;
// choosing one offers it at once, for that provider, the same button, no
// redraw between. Each direction has its own page: a provider chosen on a
// page is a draft the next draw keeps (substrateHTML), so a page drawn after
// a choice is not one drawn with no provider. Each "sends nothing" press is
// on a button never pressed before: a pressed button is disabled, and a
// disabled button's click sends nothing whatever the code does.
const settingsModelNoDefaultHead = substratePageMarkup + `<script type="module">
import { S } from './state.js';
import { frames } from './ws.js';
import { renderSettings } from './views/settings.js';
import { assert, run } from './__harness.js';
run(() => {
  const el = id => document.getElementById(id);
  S.identityExists = true;
  S.view = 'settings';
  S.providersLoaded = true;
  S.providers = [{ name: 'alpha', models: ['a1'], chat: true }, { name: 'beta', models: ['b1'], chat: true }];
  const configWith = provider => ({ llm: { provider, model: '', resolved_provider: provider, resolved_model: '', timeout_seconds: 120, probe_timeout_seconds: 20 },
    prompt: { max_tokens: 1000, recent_turns: 5 }, dashboard: { host: '127.0.0.1', port: 8180, tls: false } });
  const choose = name => { el('cfg-provider').value = name; el('cfg-provider').dispatchEvent(new Event('change', { bubbles: true })); };
  const refresh = () => document.querySelector('[data-refresh-models]');
  const asks = () => frames.filter(f => f.type === 'query' && f.query === 'discover').map(f => f.provider);
  const press = () => { const b = refresh(); assert(b, 'no Refresh button in the page'); b.click(); };
`

const settingsModelNoDefaultTail = `});
</script>`

// Drawn with alpha, then the blank choice, then alpha again.
const settingsModelNamedToBlankPage = settingsModelNoDefaultHead + `
  S.config = configWith('alpha');
  renderSettings();
  const shown = refresh();
  assert(shown && !shown.hidden, 'Refresh is not offered for alpha');
  choose('');
  assert(refresh() === shown, 'choosing the blank entry drew the page again');
  assert(shown.hidden, 'Refresh is still offered with no provider resolved');
  press();
  assert(asks().length === 0, 'Refresh with no provider resolved asked ' + asks().join(','));
  choose('alpha');
  assert(refresh() === shown, 'choosing alpha drew the page again');
  assert(!shown.hidden, 'Refresh is not offered after choosing alpha again');
  press();
  assert(asks().join(',') === 'alpha', 'Refresh after choosing alpha again asked ' + asks().join(','));
` + settingsModelNoDefaultTail

// Drawn with no provider resolved, then beta.
const settingsModelBlankToNamedPage = settingsModelNoDefaultHead + `
  S.config = configWith('');
  renderSettings();
  const shown = refresh();
  assert(shown, 'a page drawn with no provider resolved has no Refresh button to offer later');
  assert(shown.hidden, 'Refresh is offered on a page drawn with no provider resolved');
  press();
  assert(asks().length === 0, 'Refresh on a page drawn with no provider asked ' + asks().join(','));
  choose('beta');
  assert(refresh() === shown, 'choosing beta drew the page again');
  assert(!shown.hidden, 'Refresh is missing after choosing beta');
  press();
  assert(asks().join(',') === 'beta', 'Refresh after choosing beta asked ' + asks().join(','));
` + settingsModelNoDefaultTail

func TestRefreshIsOfferedOnlyForAProviderThatResolves(t *testing.T) {
	runPageInEngines(t, settingsModelNamedToBlankPage, settingsDraftModules(t))
	runPageInEngines(t, settingsModelBlankToNamedPage, settingsDraftModules(t))
}

// A QUESTION STILL OUT FOR ONE PROVIDER DOES NOT HOLD ANOTHER'S REFRESH
// (a review 06:29Z on 3632c0f8b; host 06:29Z, carried into 15 Model b). The
// server answers discovery off its read loop, by request id and provider
// (ws_query.go), so the provider shown can change before an answer comes.
// Pressed on alpha, then blank, then beta with alpha's answer still out, the
// same button was still disabled and "Asking…" under beta, and nothing could
// ask beta until something drew the page again. Now the pressed state is the
// shown provider's own: beta is free and asks at once, alpha stays held until
// its own answer (not another question's) comes, the same provider is never
// asked twice, and a press that sent nothing (no connection) holds nothing.
const settingsModelRefreshAcrossSwitchPage = substratePageMarkup + `<script type="module">
import { S } from './state.js';
import { frames, setConnected } from './ws.js';
import { renderSettings, refreshAnswered, settingsConnectionLost } from './views/settings.js';
import { assert, run } from './__harness.js';
run(() => {
  const el = id => document.getElementById(id);
  S.identityExists = true;
  S.view = 'settings';
  S.providersLoaded = true;
  S.providers = [{ name: 'alpha', models: ['a1'], chat: true }, { name: 'beta', models: ['b1'], chat: true }];
  S.config = { llm: { provider: 'alpha', model: '', resolved_provider: 'alpha', resolved_model: '', timeout_seconds: 120, probe_timeout_seconds: 20 },
    prompt: { max_tokens: 1000, recent_turns: 5 }, dashboard: { host: '127.0.0.1', port: 8180, tls: false } };
  const choose = name => { el('cfg-provider').value = name; el('cfg-provider').dispatchEvent(new Event('change', { bubbles: true })); };
  const refresh = () => document.querySelector('[data-refresh-models]');
  const discover = () => frames.filter(f => f.type === 'query' && f.query === 'discover');
  const asks = () => discover().map(f => f.provider).join(',');
  const state = b => 'hidden=' + b.hidden + ' disabled=' + b.disabled + ' words="' + b.textContent + '"';
  const free = (b, who) => assert(b && !b.hidden && !b.disabled && b.textContent === 'Refresh', 'Refresh is not free to press for ' + who + ': ' + (b ? state(b) : 'no button'));
  const held = (b, who) => assert(b && !b.hidden && b.disabled && b.textContent === 'Asking…', 'Refresh is not held for ' + who + ': ' + (b ? state(b) : 'no button'));
  renderSettings();
  const shown = refresh();
  free(shown, 'alpha, never pressed');
  shown.click();
  assert(asks() === 'alpha', 'the first press asked ' + asks());
  const alphaID = discover()[0].request_id;
  held(shown, 'alpha, its question out');
  choose('');
  assert(refresh() === shown, 'choosing the blank entry drew the page again');
  assert(shown.hidden, 'Refresh is offered with no provider resolved');
  shown.click();
  assert(asks() === 'alpha', 'Refresh with no provider resolved asked ' + asks());
  choose('beta');
  assert(refresh() === shown, 'choosing beta drew the page again');
  free(shown, 'beta while alpha\'s question is out');
  shown.click();
  assert(asks() === 'alpha,beta', 'Refresh on beta with alpha\'s question out asked ' + asks());
  held(shown, 'beta, its question out');
  choose('alpha');
  assert(refresh() === shown, 'choosing alpha again drew the page again');
  held(shown, 'alpha, its question still out');
  shown.disabled = false; // the guard is the code's, not only the disabled button's
  shown.click();
  assert(asks() === 'alpha,beta', 'alpha was asked twice before it answered: ' + asks());
  assert(!refreshAnswered('not-' + alphaID, 'alpha'), 'an answer to another question freed alpha');
  assert(!refreshAnswered(alphaID, 'beta'), 'alpha\'s answer freed beta');
  assert(refreshAnswered(alphaID, 'alpha'), 'alpha\'s own answer was not taken');
  assert(!refreshAnswered(alphaID, 'alpha'), 'alpha\'s answer was taken twice');
  renderSettings(); // the answer draws the page again, as ws.js does
  free(refresh(), 'alpha after its answer');
  refresh().click();
  assert(asks() === 'alpha,beta,alpha', 'Refresh on alpha after its answer asked ' + asks());
  settingsConnectionLost();
  setConnected(false);
  renderSettings();
  choose('beta');
  free(refresh(), 'beta after the connection was lost');
  refresh().click();
  assert(asks() === 'alpha,beta,alpha', 'a press with no connection was recorded as asked: ' + asks());
  free(refresh(), 'beta after a press that sent nothing');
});
</script>`

func TestARefreshStillOutForOneProviderDoesNotHoldAnother(t *testing.T) {
	runPageInEngines(t, settingsModelRefreshAcrossSwitchPage, settingsDraftModules(t))
}
