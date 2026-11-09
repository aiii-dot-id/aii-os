//go:build !windows

package dashboard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// firstRunPage is first run as the shell holds it, drawn by the screen's own
// module, with the substrate rig's recording ws.js (frames) under it.
func firstRunPage(script string) string {
	return `<!doctype html><div class="fb on" id="firstboot"></div><script type="module">
import { S } from './state.js';
import { frames, setConnected } from './ws.js';
import { renderProviderOptions, discoveryAnswered, acceptDiscoveryResponse, acceptFirstRunConfig, refuseFirstRunConfig, firstRunConfigArrived, firstbootConnectionLost } from './views/firstrun/start.js';
import { assert, run } from './__harness.js';
const $ = id => document.getElementById(id);
const sent = since => frames.slice(since);
const hosted = { name: 'OpenAI', endpoint: 'https://api.openai.test/v1', models: ['o-small'], default_model: 'o-small', chat: true };
const pickRow = id => { const r = document.querySelector('[data-row="' + id + '"] input'); r.checked = true; r.dispatchEvent(new Event('change')); };
run(() => {
` + script + `
});
</script>`
}

// ONE PRESS STARTS THEM (spec section 1 and step 7; "Wrong if a birth needs
// more than one press with a local model found"). A model that answered on
// this computer is the first row, chosen for you, saying what it is and why
// (Recommended, found running here); with the notes choice as saved, one
// press of Begin asks for the birth, at the address that answered.
func TestOnePressStart(t *testing.T) {
	scenarios := map[string]string{
		"local-model-one-press": `
  S.config = { genesis: { agent_memory_off: false, agent_memory_found: true } };
  S.providers = [
    { name: 'Local server', endpoint: 'http://127.0.0.1:8080/v1', local: true, models: ['llama-3.3-70b'], default_model: 'llama-3.3-70b', chat: true, preselect: true, preselect_why: 'found on this computer' },
    hosted,
    { name: 'Anthropic', endpoint: 'https://api.anthropic.test/v1', models: ['a-small'], default_model: 'a-small', chat: true },
  ];
  renderProviderOptions();
  const rec = document.querySelector('[data-row="rec"]');
  assert(rec && rec.querySelector('input').checked, 'the model found on this computer is not chosen for you');
  const said = rec.textContent;
  // "{model}, local": a local entry may
  // be on the person's network, so its label does not say "on this computer".
  // The row's own looking line ("Looking for a model on this computer…") is
  // about where the page looks, which is this computer, so only the label is
  // checked.
  assert(said.includes('llama-3.3-70b, local') && !said.includes('llama-3.3-70b, on this computer') && said.includes('Recommended') && said.includes('Found running here. Private, and no key needed.'), 'the first row does not say what already works and why: ' + said);
  assert([...document.querySelectorAll('[data-row]')].map(r => r.dataset.row).join() === 'rec,OpenAI,Anthropic,other', 'the rows are not the one that works, OpenAI, Anthropic and Another service');
  assert($('fb-notes').checked, 'the box does not read back that the notes are copied');
  const asked = frames.filter(f => f.type === 'query' && f.query === 'discover').at(-1);
  assert(asked && asked.base_url === 'http://127.0.0.1:8080/v1', 'the local model was not asked for its models at its address');
  assert(acceptDiscoveryResponse(asked.request_id, 'Local server'), 'the answer to the ask was refused');
  discoveryAnswered(['llama-3.3-70b']);
  const before = frames.length;
  $('fb-birth').click();
  const out = sent(before);
  assert(out.length === 1 && out[0].type === 'genesis', 'one press did not ask for the birth, and only that: ' + JSON.stringify(out));
  assert(out[0].genesis.provider === 'Local server' && out[0].genesis.endpoint === 'http://127.0.0.1:8080/v1' && out[0].genesis.model === 'llama-3.3-70b', 'the birth was not asked at the model that answered: ' + JSON.stringify(out[0]));`,

		// A NAME ONLY THE CONFIGURATION HOLDS IS NOT RECOMMENDED: the server
		// names the model its server lists, and the discovery keeps it chosen.
		"a-configured-name-not-listed": `
  S.config = { genesis: { agent_memory_off: false, agent_memory_found: true } };
  S.providers = [
    { name: 'Ollama', endpoint: 'http://127.0.0.1:11434/v1', local: true, models: ['gone', 'live'], default_model: 'gone', preselect_model: 'live', chat: true, preselect: true },
    hosted,
  ];
  renderProviderOptions();
  const said = document.querySelector('[data-row="rec"]').textContent;
  assert(said.includes('live, local') && !said.includes('gone'), 'the recommendation names a model its server does not list: ' + said);
  const asked = frames.filter(f => f.type === 'query' && f.query === 'discover').at(-1);
  assert(acceptDiscoveryResponse(asked.request_id, 'Ollama'), 'the answer to the ask was refused');
  discoveryAnswered(['live', 'also']);
  assert($('fb-model').value === 'live', 'the discovery left no model chosen, or another: ' + $('fb-model').value);
  const before = frames.length;
  $('fb-birth').click();
  const out = sent(before);
  assert(out.length === 1 && out[0].type === 'genesis' && out[0].genesis.model === 'live', 'the birth was not asked at the model the server lists: ' + JSON.stringify(out));`,

		// THE BOX UNCHECKED IS SAVED FIRST, THEN THE BIRTH: Begin sends the
		// save and nothing else; the save's own reply, read back as saved,
		// asks for the birth.
		"notes-left-out-saved-then-born": `
  S.config = { genesis: { agent_memory_off: false, agent_memory_found: true } };
  S.providers = [hosted];
  renderProviderOptions();
  pickRow('OpenAI');
  $('fb-notes').checked = false; $('fb-notes').dispatchEvent(new Event('change'));
  const before = frames.length;
  $('fb-birth').click();
  let out = sent(before);
  assert(out.length === 1 && out[0].type === 'config_set' && out[0].config['genesis.agent_memory.off'] === true, 'Begin did not save the box first, and only that: ' + JSON.stringify(out));
  assert($('fb-birth').disabled, 'Begin can be pressed again while the save is out');
  assert(!acceptFirstRunConfig('another-request'), 'a reply to another request was taken as the save');
  S.config = { genesis: { agent_memory_off: true, agent_memory_found: true } };
  assert(acceptFirstRunConfig(out[0].request_id), 'the save\'s own reply was not taken');
  out = sent(before);
  assert(out.length === 2 && out[1].type === 'genesis' && out[1].genesis.provider === 'OpenAI', 'the birth was not asked once the save was confirmed: ' + JSON.stringify(out));`,

		// A SAVED "LEAVE THEM OUT" READS BACK UNCHECKED, AND CHECKING IT AGAIN
		// IS SAVED BEFORE THE BIRTH.
		"saved-off-reads-back-and-is-changed": `
  S.config = { genesis: { agent_memory_off: true, agent_memory_found: true } };
  S.providers = [hosted];
  renderProviderOptions();
  assert(!$('fb-notes').checked, 'a saved "leave them out" does not read back as an unchecked box');
  pickRow('OpenAI');
  $('fb-notes').checked = true; $('fb-notes').dispatchEvent(new Event('change'));
  const before = frames.length;
  $('fb-birth').click();
  const out = sent(before);
  assert(out.length === 1 && out[0].type === 'config_set' && out[0].config['genesis.agent_memory.off'] === false, 'checking the box again was not saved first: ' + JSON.stringify(out));`,

		// THE ROWS ARE KEPT THROUGH A CHOICE AND A REPLY (a review's read of
		// step 7). Arrow keys move the choice as they move the focus, so a
		// row redrawn on choosing, or on a reply that changes nothing, would
		// take the keyboard with it: the radio chosen is the same node,
		// focused, checked and marked, its detail under it; a field typed in
		// keeps its focus through a reply; and a reply that changes the rows
		// draws them again with the focus on the same row.
		"rows-kept-through-a-choice-and-a-reply": `
  S.config = { genesis: { agent_memory_off: false, agent_memory_found: true } };
  S.providers = [hosted, { name: 'Anthropic', endpoint: 'https://api.anthropic.test/v1', models: ['a-small'], default_model: 'a-small', chat: true }];
  renderProviderOptions();
  const radio = id => document.querySelector('[data-row="' + id + '"] input');
  const first = radio('OpenAI'), second = radio('Anthropic');
  first.focus();
  pickRow('OpenAI');
  assert(radio('OpenAI') === first && document.activeElement === first, 'choosing a row drew the rows again: the focus is on ' + (document.activeElement && document.activeElement.outerHTML));
  pickRow('Anthropic');
  second.focus();
  assert(radio('OpenAI') === first && radio('Anthropic') === second && document.activeElement === second, 'moving the choice drew the rows again');
  assert(second.checked && !first.checked && second.closest('[data-row]').classList.contains('is-on') && !first.closest('[data-row]').classList.contains('is-on'), 'the mark did not move with the choice');
  assert($('fb-detail').parentElement === second.closest('[data-row]') && !$('fb-detail').hidden, 'the detail did not move under the chosen row');
  S.providers = S.providers.map(p => Object.assign({}, p));
  renderProviderOptions();
  firstRunConfigArrived(); acceptFirstRunConfig('a-push-of-the-config');
  assert(radio('Anthropic') === second && document.activeElement === second && second.checked, 'a reply that changes nothing drew the rows again, or moved the choice');
  $('fb-apikey').focus();
  renderProviderOptions();
  assert(document.activeElement === $('fb-apikey'), 'a reply took the focus from the field being typed in');
  second.focus();
  S.providers = [hosted, { name: 'Anthropic', endpoint: 'https://api.anthropic.test/v1', models: ['a-small'], default_model: 'a-small', chat: true }, { name: 'Local server', endpoint: 'http://127.0.0.1:8080/v1', local: true, models: ['m'], default_model: 'm', chat: true, preselect: true }];
  renderProviderOptions();
  const again = radio('Anthropic');
  assert(again !== second && document.activeElement === again && again.checked, 'rows drawn again did not keep the focus and the choice on the same row');`,

		// A REFUSED SAVE STOPS THERE AND SAYS SO: no birth, Begin again.
		"refused-save-stops": `
  S.config = { genesis: { agent_memory_off: false, agent_memory_found: true } };
  S.providers = [hosted];
  renderProviderOptions();
  pickRow('OpenAI');
  $('fb-notes').checked = false; $('fb-notes').dispatchEvent(new Event('change'));
  const before = frames.length;
  $('fb-birth').click();
  const save = sent(before)[0];
  assert(refuseFirstRunConfig(save.request_id, 'the disk is full'), 'the refusal of the save was not taken');
  assert(!sent(before).some(f => f.type === 'genesis'), 'a refused save went on to a birth');
  assert($('fb-result').textContent.includes('could not save your choice about the notes, so nothing has begun') && $('fb-result').textContent.includes('the disk is full'), 'the refusal was not said: ' + $('fb-result').textContent);
  assert(!$('fb-birth').disabled, 'Begin stayed disabled after the refusal');`,

		// A CONNECTION LOST BETWEEN THE SAVE AND ITS REPLY STOPS THERE: no
		// birth, Begin again, and the save's late reply begins nothing.
		"lost-between-save-and-birth": `
  S.config = { genesis: { agent_memory_off: false, agent_memory_found: true } };
  S.providers = [hosted];
  renderProviderOptions();
  pickRow('OpenAI');
  $('fb-notes').checked = false; $('fb-notes').dispatchEvent(new Event('change'));
  const before = frames.length;
  $('fb-birth').click();
  const save = sent(before)[0];
  assert(firstbootConnectionLost(), 'the lost connection was not taken by first run');
  assert($('fb-result').textContent === 'Not connected. Nothing has begun.', 'the lost connection was not said: ' + $('fb-result').textContent);
  assert(!$('fb-birth').disabled, 'Begin stayed disabled after the connection went');
  assert(!acceptFirstRunConfig(save.request_id), 'the save\'s reply after the connection went was taken');
  assert(!sent(before).some(f => f.type === 'genesis'), 'a save not confirmed went on to a birth');`,
		// THE NOTES STEP IS THERE ONLY WHERE SUCH NOTES ARE (the first-session
		// review, change 2): with none on this computer, Begin's list is the
		// founding principles, checked, then their first words, and no box; with
		// some, the step and its box are shown.
		// BEGIN WAITS FOR A MODEL (the walk's first finding): with nothing
		// chosen it cannot be pressed and says so in one line, and a press asks
		// for nothing; once a service and its model are chosen it can.
		"begin-waits-for-a-model": `
  S.config = { genesis: { agent_memory_off: false, agent_memory_found: true } };
  S.providers = [hosted, { name: 'Anthropic', endpoint: 'https://api.anthropic.test/v1', models: ['a-small'], default_model: 'a-small', chat: true }];
  renderProviderOptions();
  assert($('fb-birth').disabled && $('fb-result').textContent === 'Choose a model first.', 'with nothing chosen Begin can be pressed, or does not say why: ' + $('fb-birth').disabled + ' "' + $('fb-result').textContent + '"');
  const before = frames.length;
  $('fb-birth').click();
  assert(!sent(before).some(f => f.type === 'genesis' || f.type === 'config_set'), 'a press with nothing chosen asked for something: ' + JSON.stringify(sent(before)));
  pickRow('Anthropic');
  assert(!$('fb-birth').disabled && $('fb-result').textContent === '', 'with a model chosen Begin still waits: "' + $('fb-result').textContent + '"');`,
		"the-notes-step-only-where-notes-are": `
  S.config = { genesis: { agent_memory_off: false, agent_memory_found: false } };
  S.providers = [hosted];
  renderProviderOptions(); firstRunConfigArrived();
  const steps = () => [...document.querySelectorAll('.first-run__begin li')].filter(li => !li.hidden).map(li => li.textContent).join(' | ');
  assert(steps() === 'The founding principles are fetched from aiii.id and checked. | Your identity says their first words.', 'with no notes here, Begin says: ' + steps());
  assert($('fb-notes-row').hidden, 'the notes box shows with no notes here');
  S.config = { genesis: { agent_memory_off: false, agent_memory_found: true } };
  firstRunConfigArrived();
  assert(!$('fb-notes-row').hidden && steps().split(' | ').length === 3 && steps().includes('Claude Code'), 'with notes here, the step and its box are not shown: ' + steps());`,
	}
	// AN IDENTITY'S OWN index.html OF 0.2.0 KEEPS ITS FORM, AND IT STILL
	// BEGINS A BIRTH: the screen's module draws nothing over a form already
	// there, has no box to save, and sends genesis on the old Birth.
	old, err := os.ReadFile(filepath.Join("testdata", "overlay-0.2.0", "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	html := string(old)
	from, to := strings.Index(html, `<div class="fb" id="firstboot">`), strings.Index(html, `<div id="fb-restore-body" hidden></div>`)
	if from < 0 || to < from {
		t.Fatal("0.2.0's index.html fixture has no first-run form")
	}
	oldForm := html[from:to] + `<div id="fb-restore-body" hidden></div></div></div></div>`
	oldPage := strings.Replace(firstRunPage(`
  S.config = { genesis: { agent_memory_off: false, agent_memory_found: true } };
  S.providers = [hosted];
  renderProviderOptions();
  assert(!$('fb-choices') && !$('fb-notes') && $('fb-birth').textContent === 'Birth', 'the screen drew over the old form');
  $('fb-provider').value = '0'; $('fb-provider').onchange();
  $('fb-apikey').value = 'k';
  const before = frames.length;
  $('fb-birth').click();
  const out = sent(before);
  assert(out.length === 1 && out[0].type === 'genesis' && out[0].genesis.provider === 'OpenAI' && out[0].genesis.api_key === 'k', 'the old form did not begin a birth: ' + JSON.stringify(out));`),
		`<div class="fb on" id="firstboot"></div>`, oldForm, 1)
	t.Run("an-old-form-still-begins", func(t *testing.T) {
		runPageInEngines(t, oldPage, substrateModules(t))
	})
	for name, script := range scenarios {
		name, script := name, script
		t.Run(name, func(t *testing.T) {
			if !strings.Contains(script, "assert(") {
				t.Fatal("a scenario with nothing to assert")
			}
			runPageInEngines(t, firstRunPage(script), substrateModules(t))
		})
	}
}

// FIRST RUN IS THE PAGE AT EVERY ADDRESS (step 7, sent back). While no
// identity exists, an address of any place, opened cold (a bookmark, a
// reload, a notice's link), and a last view the browser kept, show the one
// screen and nothing else: Begin is there and on top, no view, top bar or
// panel is drawn, the shell cannot be reached, and the address is the one
// opened.
func TestFirstRunIsThePageAtEveryAddress(t *testing.T) {
	s := New("127.0.0.1", 0, &WSHandler{
		GetStats: func() (*StatsResponse, error) { return &StatsResponse{}, nil },
		GetProviders: func() ProviderDirectory {
			return ProviderDirectory{Providers: []ProviderInfo{{Name: "OpenAI", Endpoint: "https://api.openai.test/v1", Chat: true, Models: []string{"o-small"}}}}
		},
	})
	runDashboardInEngines(t, s, "/", shellDriver+`
const cases = [
  ['#/home', ''], ['#/settings/model', ''], ['#/settings/substrate', ''], ['#/settings', ''], ['#/identity', ''], ['#/identity/record/3', ''],
  ['#/projects', ''], ['#/chat', ''], ['', 'settings'], ['', 'home'], ['', 'identity'],
];
// An old Settings name may already read as its new place, from the design
// (Substrate is the first section of Model): the same place, not a move
// away. Which one shows depends on whether views/settings.js, the only
// file that knows the new names, has loaded yet.
const newPlace = { '#/settings/substrate': '#/settings/model', '#/home': '#/chat' };
// Drawn: it has a box and can be seen (a view not open keeps its box,
// hidden by visibility).
const drawn = el => !!el && el.getClientRects().length > 0 && getComputedStyle(el).visibility !== 'hidden';
(async () => {
  const { ready } = await import('/recovery.js');
  await ready;
  let i = +(sessionStorage.getItem('fr-case') || '-1');
  if (i >= 0) {
    const [hash, kept] = cases[i];
    const at = hash || ('a kept view of ' + kept);
    const screen = document.getElementById('firstboot');
    await until(() => screen && screen.classList.contains('on') && drawn(document.getElementById('fb-birth')) && document.querySelector('[data-row]'));
    const begin = document.getElementById('fb-birth');
    if (!drawn(begin)) return fail(at + ': the one screen is not shown');
    if (screen.parentElement !== document.body) return fail(at + ': the screen is still inside ' + screen.parentElement.id);
    begin.scrollIntoView({ block: 'center' });
    const r = begin.getBoundingClientRect();
    const top = document.elementFromPoint(r.left + r.width / 2, r.top + r.height / 2);
    if (!top || !screen.contains(top)) return fail(at + ': Begin is under ' + (top && (top.id || top.className)));
    const views = [...document.querySelectorAll('.view')].filter(drawn).map(v => v.id);
    if (views.length) return fail(at + ': a view is drawn beside the screen: ' + views.join());
    for (const sel of ['.c-top-bar', '#slot-panel', '#composer-wrap', '#place-tabs']) {
      if (drawn(document.querySelector(sel))) return fail(at + ': ' + sel + ' is drawn beside the screen');
    }
    if (!document.querySelector('.app').inert) return fail(at + ': the shell behind the screen can be reached');
    // (Settings' own address names the section it opens on, as it does
    // for a born page: TestEveryOldRouteLandsOnItsNewPlace.)
    const same = hash === '#/settings' ? /^#\/settings(\/[a-z-]+)?$/.test(location.hash) : location.hash === hash || location.hash === newPlace[hash];
    if (hash && !same) return fail(hash + ' was moved to ' + location.hash);
  }
  i++;
  if (i === cases.length) return report('OK');
  sessionStorage.setItem('fr-case', String(i));
  const [hash, kept] = cases[i];
  try { if (kept) localStorage.setItem('aii.view', kept); else localStorage.removeItem('aii.view'); } catch (e) {}
  history.replaceState(null, '', location.pathname + hash);
  location.reload();
})().catch(e => fail((e && e.message) || String(e)));
`)
}

// SIXTEEN PIXELS ON A PHONE (a review's read of step 7). iOS zooms into a
// field under 16 px when it is focused and does not zoom back: at a phone's
// width every field of the chosen row's detail is 16 px or more, on the
// real page with every sheet it loads. Chrome and Firefox open no window
// narrower than 500 px, so there the phone is 500 px wide and in WebKit
// 390; the page's phone rules begin at 767, and a window wider than that
// fails rather than passing against rules that never applied. (The exact
// 390 in every engine is the native-key run's, on a real daemon.)
func TestFirstRunFieldsAreSixteenOnAPhone(t *testing.T) {
	s := New("127.0.0.1", 0, &WSHandler{
		GetStats: func() (*StatsResponse, error) { return &StatsResponse{}, nil },
		GetProviders: func() ProviderDirectory {
			return ProviderDirectory{Providers: []ProviderInfo{{Name: "OpenAI", Endpoint: "https://api.openai.test/v1", Chat: true, Models: []string{"o-small"}}}}
		},
	})
	runDashboardInEnginesAtSize(t, s, "/", shellDriver+`
(async () => {
  const { ready } = await import('/recovery.js');
  await ready;
  await until(() => document.querySelector('[data-row="OpenAI"] input'));
  const pick = document.querySelector('[data-row="OpenAI"] input');
  if (!pick) return fail('no OpenAI row');
  if (innerWidth > 767) return fail('the window is ' + innerWidth + ' px wide: no phone rule is in force');
  pick.click();
  await until(() => document.getElementById('fb-apikey').getClientRects().length);
  if (!document.getElementById('fb-apikey').getClientRects().length) return fail('the chosen row does not ask for the key');
  const small = [...document.querySelectorAll('.first-run__field input, .first-run__field select')]
    .map(el => [el.id, parseFloat(getComputedStyle(el).fontSize)]).filter(([, px]) => !(px >= 16));
  if (small.length) return fail('fields under 16 px at ' + innerWidth + ' px: ' + JSON.stringify(small));
  // With no other assistant's notes here, the notes box is not drawn, under every sheet the page loads.
  if (document.getElementById('fb-notes-row').getClientRects().length) return fail('the notes box is drawn where no notes are');
  report('OK');
})().catch(e => fail((e && e.message) || String(e)));
`, 390, 844)
}
