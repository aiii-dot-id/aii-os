//go:build !windows

// .
// .

package dashboard

import (
	"strings"
	"testing"
)

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .

const pluginsViewPage = `<!doctype html>
<style>__LAYOUT_CSS__</style>
<div class="app"><div class="stack" id="plugins-stack"></div></div>
<script type="module">
import { assert, run } from './__harness.js';
import { S } from './state.js';
import { renderPlugins } from './views/plugins.js';
import { saved, sent } from './views/settings.js';

run(() => {
  S.config = {
    plugins: {
      autoload: 'T2',
      skips: [{ dir: 'plugins/memoryd', id: 'org.example.memoryd', tier: 'T0',
                reason: 'unsigned — below the T2 auto-load level' }],
      catalog_url: 'https://plugins.example.test/aiios-plugins.md',
      catalog_updates: 1,
      catalog: [{ id: 'org.example.foo', version: '1.0.0', tier: 'T3',
                  summary: 'a foo that foos', available: true, installed: false },
                { id: 'org.example.bar', version: '1.1.0', tier: 'T1', available: true,
                  installed: true, installed_version: '1.0.0', update_available: true,
                  description: 'Bar keeps the bar.', publisher: 'Example Co', license: 'Apache-2.0', homepage: 'https://example.test/bar' }],
      installed: [{ id: 'org.example.bar', version: '1.0.0', tier: 'T1', mode: 'wasm', variant: 'linux-x86_64-wasm',
                    publisher: 'Example Co', publisher_id: 'pub-7', family: 'tool', interfaces: ['tools.provider v1'],
                    capabilities: ['net.fetch', 'files.read'], runtime: 'wasm_component',
                    package_hash: 'sha256:0123456789abcdef0123456789abcdef', settings: [],
                    grants: { listed: true, read_only: false, auto_confirm: ['enroll'] } }],
    },
  };
  renderPlugins();
  const st = document.getElementById('plugins-stack');
  const text = st.textContent;

  // 1. The real control is present and reflects the CURRENT setting —
  //    not a default, or the operator would silently change it by saving.
  const sel = document.getElementById('cfg-plevel');
  assert(sel !== null, 'the Plugins view has no auto-load control — it is still a placeholder');
  assert(sel.value === 'T2', 'the control shows ' + sel.value + ', not the configured T2');
  assert(sel.getBoundingClientRect().height > 0, 'the auto-load control is not visible');

  // 2. A verified-but-not-loaded package is NAMED, with its reason. This
  //    is the difference between "no plugins" and "plugins you refused".
  assert(text.includes('org.example.memoryd'),
    'a present, verified, unloaded package is not shown: ' + text.slice(0, 200));
  assert(text.includes('below the T2 auto-load level'),
    'the reason a package did not load is missing');

  // 3. Saving reaches the ONE config owner, tagged as the plugins section.
  const save = st.querySelector('[data-save="plugins"]');
  assert(save !== null, 'there is no way to save the level that was just changed');
  save.click();
  assert(saved.length === 1 && saved[0] === 'plugins',
    'the save did not reach the config owner as the plugins section: ' + JSON.stringify(saved));

  // 4. The catalog browser (S12): an available offering is named and
  //    carries an Install control that targets its id.
  assert(text.includes('org.example.foo'), 'the catalog offering is not shown: ' + text.slice(0, 200));
  const inst = st.querySelector('[data-plugin="install:org.example.foo"]');
  assert(inst !== null, 'the catalog entry has no install control');
  assert(inst.textContent.trim() === 'Install', 'an uninstalled offering is offered Install, not ' + inst.textContent);

  // 5. A store, not a roadmap: no placeholder cards;
  //    the index is fetched from a URL the operator can see and edit,
  //    refreshed by hand, and an installed release the index outranks
  //    is offered its update.
  assert(!/FLAGSHIP|COMING/.test(text), 'a placeholder card is shown — beta ships no roadmap in the store');
  const url = document.getElementById('cfg-caturl');
  assert(url !== null && url.value === 'https://plugins.example.test/aiios-plugins.md', 'the catalog URL is not editable here');
  assert(st.querySelector('[data-catalog-refresh]') !== null, 'the index cannot be refreshed by hand');

  // 5a. The connect scope and the standing confirmations:
  //     read only is one box on the card; an operation the operator said
  //     Always to is listed by name and revoked by name — the list without it.
  const ro = st.querySelector('[data-grant-plugin="org.example.bar"][data-grant-field="read_only"]');
  assert(ro !== null && !ro.checked, 'the read-only box is missing or wrong');
  assert(text.includes('always confirmed, without asking: enroll'), 'the standing confirmation is not listed: ' + text.slice(0, 300));
  const rv = st.querySelector('[data-revoke-auto="org.example.bar"][data-op="enroll"]');
  assert(rv !== null, 'the standing confirmation cannot be revoked');
  rv.click();
  const revoke = sent.find(ch => Object.keys(ch).includes('plugins.grants.org.example.bar.auto_confirm'));
  assert(revoke && revoke['plugins.grants.org.example.bar.auto_confirm'].length === 0, 'the revoke did not send the list without it: ' + JSON.stringify(sent));
  assert(saved.includes('grants:org.example.bar'), 'the revoke is not tagged as the grants section');
  const upd = st.querySelector('[data-plugin="install:org.example.bar"]');
  assert(upd !== null && upd.textContent.trim() === 'Update', 'an installed release the index outranks is not offered its Update');
  assert(st.querySelector('[data-plugin="uninstall:org.example.bar"]') !== null, 'an installed release keeps its Uninstall beside the Update');
  assert(text.includes('1 update'), 'the catalog card does not count the update');

  // 6. Detail: the catalog entry's description,
  //    publisher, license and homepage; the installed plugin's publisher
  //    (certified id), family, interfaces, signed capability surface,
  //    runtime and package hash — from the verified manifest.
  assert(text.includes('Bar keeps the bar.') && text.includes('by Example Co') && text.includes('Apache-2.0'), 'the catalog entry shows no detail');
  const home = st.querySelector('a[href="https://example.test/bar"]');
  assert(home !== null && home.getAttribute('rel') === 'noopener', 'the homepage is not a safe link');
  const detail = st.querySelector('.plugin-detail');
  assert(detail !== null, 'the installed plugin shows no detail block');
  const dt = detail.textContent;
  assert(dt.includes('Example Co') && dt.includes('certified pub-7') && dt.includes('tools.provider v1') && dt.includes('net.fetch, files.read') && dt.includes('wasm_component') && dt.includes('sha256:0123456789abcdef'),
    'the installed plugin detail is incomplete: ' + dt);
});
</script>`

func TestPluginsViewShowsRealControlsInBrowser(t *testing.T) {
	read := func(name string) []byte {
		t.Helper()
		b, err := staticFS.ReadFile("static/" + name)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	layoutCSS := read("layout.css")
	page := strings.Replace(pluginsViewPage, "__LAYOUT_CSS__", string(layoutCSS), 1)
	if page == pluginsViewPage {
		t.Fatal("stylesheet placeholder not substituted — the page would assert against no CSS at all")
	}
	runPageInEngines(t, page, map[string][]byte{
		"/views/plugins.js": read("views/plugins.js"),
		"/state.js":         read("state.js"),
		"/util.js":          read("util.js"),
		// .
		// .
		// .
		"/views/settings.js": []byte(`export const saved = [];
export function saveConfigSection(section) { saved.push(section); }
export const sent = [];
export function sendConfigChanges(section, ch) { saved.push(section); sent.push(ch); }
export function configFeedbackHTML() { return ''; }`),
		// .
		// .
		// .
		"/ws.js": []byte(`export function send() {}`),
	})
}

// .
// .
// .
// .
// .
func TestANativeEnginesReadinessShowsOnItsCard(t *testing.T) {
	page := `<!doctype html>
<div id="plugins-stack"></div>
<script type="module">
import { assert, run } from './__harness.js';
import { S } from './state.js';
import { renderPlugins } from './views/plugins.js';
run(() => {
  S.config = { plugins: { autoload: 'T3', catalog: [], catalog_url: '', installed: [
    { id: 'id.aiii.voice', version: '0.1.0-native-cp1', tier: 'T3', mode: 'supervised', variant: 'macos-arm64-native',
      title: 'AII Voice native English checkpoint',
      description: 'Ten stable voices, configurable pause/VAD, STT/TTS. English only. Enrollment editing incomplete.',
      applies: 'next_session', settings: [{ key: 'tts_voice', type: 'enum', title: 'Voice', values: ['alba', 'marius'], default: 'alba' }],
      readiness: { models_loaded: 5, accelerator: 'cpu', probe_ms: 710 } },
    { id: 'org.example.quiet', version: '1.0.0', tier: 'T1', mode: 'wasm', variant: 'linux-x86_64-wasm', settings: [] },
  ] } };
  renderPlugins();
  const text = document.getElementById('plugins-stack').textContent;
  assert(text.includes('5 models loaded'), 'the card says how many models came up: ' + text.slice(0, 240));
  assert(text.includes('computing on cpu'), 'and what it is computing on');
  assert(text.includes('warm in 710 ms'), 'and how long the warm probe took');
  assert(!/reported itself ready[\s\S]*reported itself ready/.test(text), 'a plugin that reported no readiness says nothing');
  // THE PACKAGE'S OWN WORDS LEAD THE CARD. An operator reading only an
  // id and a version takes the version for a statement of what the
  // thing is — and 0.1.0-native-cp1 is the author's checkpoint ladder,
  // not the name anyone uses for it. The identifiers stay
  // beside the title; they do not stand in for a description.
  const cards = Array.from(document.querySelectorAll('#plugins-stack .card'));
  const cardWith = frag => cards.find(c => (c.querySelector('h3') || {}).textContent && c.querySelector('h3').textContent.includes(frag));
  const voice = cardWith('0.1.0-native-cp1');
  assert(voice, 'the voice card is rendered');
  assert(voice.querySelector('h3').textContent.startsWith('AII Voice native English checkpoint'), 'the title heads the card: ' + voice.querySelector('h3').textContent);
  assert(voice.querySelector('.pkg-id').textContent === 'id.aiii.voice', 'the id is still shown, under the title');
  assert(text.includes('Enrollment editing incomplete'), "the package's own account of what it cannot do reaches the operator: " + text.slice(0, 200));
  const quiet = cardWith('org.example.quiet');
  assert(quiet, 'the untitled card is rendered');
  assert(quiet.querySelector('.pkg-id') === null && quiet.querySelector('.pkg-desc') === null, 'a package that titles itself nothing is headed by its id and gains no empty lines');
});
</script>`
	read := func(name string) []byte {
		t.Helper()
		b, err := staticFS.ReadFile("static/" + name)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	runPageInEngines(t, page, map[string][]byte{
		"/views/plugins.js": read("views/plugins.js"),
		"/state.js":         read("state.js"),
		"/util.js":          read("util.js"),
		"/views/settings.js": []byte(`export const saved = []; export function saveConfigSection(s) { saved.push(s); }
export const sent = []; export function sendConfigChanges(s, ch) { saved.push(s); sent.push(ch); }
export function configFeedbackHTML() { return ''; }`),
		"/ws.js": []byte(`export function send() {}`),
	})
}
