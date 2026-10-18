//go:build !windows

package dashboard

import (
	"strings"
	"testing"
)

const pluginStorePage = `<!doctype html>
<style>__LAYOUT_CSS__</style>
<div class="app"><div class="stack" id="plugins-stack"></div></div>
<script type="module">
import { assert, run } from './__harness.js';
import { S } from './state.js';
import { renderPlugins } from './views/plugins.js';

run(() => {
  S.config = { plugins: { autoload: 'T1', catalog_url: 'https://plugins.example.test/aiios-plugins.md', catalog_updates: 1,
    catalog: [
      { id: 'org.example.memory', title: 'Acme Working Memory', version: '0.5.0', tier: 'T3', category: 'memory', keywords: ['notes', 'recall'],
        summary: 'A bounded working memory for an identity.', description: 'Store, search and recall notes.', publisher: 'AIII', license: 'Apache-2.0',
        homepage: 'https://example.test/acme-memory', size: 530611, updated: '2026-09-10', available: true, installed: false },
      { id: 'org.example.voice', title: 'Example Voice', version: '2.0.0', tier: 'T2', category: 'voice', keywords: ['speech'],
        summary: 'Talks.', updated: '2026-09-01', available: true, installed: true, installed_version: '1.0.0', update_available: true },
      { id: 'org.example.zed', version: '1.0.0', tier: 'T1', category: 'memory', summary: 'A second memory.', updated: '2026-09-05', available: true, installed: false },
    ], installed: [] } };
  renderPlugins();
  const st = document.getElementById('plugins-stack');
  const rows = () => Array.from(st.querySelectorAll('[data-entry]')).map(r => r.dataset.entry);

  const text = st.textContent;
  assert(text.includes('Acme Working Memory') && text.includes('org.example.zed'), 'titles or ids missing: ' + text.slice(0, 300));
  assert(text.includes('platform-signed') && text.includes('reviewed'), 'tiers are letters, not words');
  assert(rows().join(',') === 'org.example.memory,org.example.voice,org.example.zed', 'the default order is not by name: ' + rows());

  const q = document.getElementById('store-q');
  assert(q !== null, 'no search field');
  q.focus();
  q.value = 'recall'; q.dispatchEvent(new Event('input', { bubbles: true }));
  assert(rows().join(',') === 'org.example.memory', 'a keyword search did not narrow to the memory plugin: ' + rows());
  assert(st.textContent.includes('1 of 3 plugins'), 'the count does not say 1 of 3: ' + st.textContent.slice(-240));
  assert(document.activeElement === q, 'the search field lost its focus while typing');
  q.value = ''; q.dispatchEvent(new Event('input', { bubbles: true }));
  assert(rows().length === 3, 'clearing the search did not restore the list');

  const chips = Array.from(st.querySelectorAll('[data-cat]')).map(c => c.dataset.cat);
  assert(chips.join(',') === ',memory,voice', 'the chips are not all,memory,voice: ' + chips);
  st.querySelector('[data-cat="voice"]').click();
  assert(rows().join(',') === 'org.example.voice', 'the voice chip did not filter: ' + rows());
  st.querySelector('[data-cat=""]').click();
  assert(rows().length === 3, 'the all chip did not restore the list');

  const sort = document.getElementById('store-sort');
  sort.value = 'updated'; sort.dispatchEvent(new Event('change', { bubbles: true }));
  assert(rows().join(',') === 'org.example.memory,org.example.zed,org.example.voice', 'the recently-updated order is wrong: ' + rows());
  sort.value = 'updates'; sort.dispatchEvent(new Event('change', { bubbles: true }));
  assert(rows()[0] === 'org.example.voice', 'updates-first does not lead with the update: ' + rows());

  const d = st.querySelector('[data-detail-of="org.example.memory"]');
  assert(d !== null && d.hidden, 'the detail is not collapsed at rest');
  st.querySelector('[data-detail="org.example.memory"]').click();
  assert(!d.hidden, 'the title did not open the detail');
  const dt = d.textContent;
  assert(dt.includes('Store, search and recall notes.') && dt.includes('Apache-2.0') && dt.includes('518 KB') && dt.includes('2026-09-10'), 'the detail is incomplete: ' + dt);
  const link = d.querySelector('a[href="https://example.test/acme-memory"]');
  assert(link !== null && link.getAttribute('rel') === 'noopener', 'the homepage is not a safe link');

  assert(st.querySelector('[data-plugin="install:org.example.memory"]').textContent.trim() === 'Install', 'no Install control');
  assert(st.querySelector('[data-plugin="install:org.example.voice"]').textContent.trim() === 'Update', 'no Update control');
  assert(st.textContent.includes('1 update'), 'the update count is missing');
});
</script>`

func TestPluginStoreSearchesFiltersAndSorts(t *testing.T) {
	runPluginStorePage(t, pluginStorePage)
}

const pluginStoreTierPage = `<!doctype html>
<style>__LAYOUT_CSS__</style>
<div class="app"><div class="stack" id="plugins-stack"></div></div>
<script type="module">
import { assert, run } from './__harness.js';
import { S } from './state.js';
import { renderPlugins } from './views/plugins.js';

run(() => {
  S.config = { plugins: { autoload: 'T1', catalog_url: 'https://plugins.example.test/aiios-plugins.md', catalog_updates: 1,
    catalog: [
      { id: 'org.example.runs', version: '1.0.0', tier: 'T3', available: true, installed: true, installed_version: '1.0.0', installed_tier: 'T1' },
      { id: 'org.example.newer', version: '2.0.0', tier: 'T3', available: true, installed: true, installed_version: '1.0.0', installed_tier: 'T2', update_available: true },
      { id: 'org.example.offer', version: '0.5.0', tier: 'T2', available: true, installed: false },
    ], installed: [] } };
  renderPlugins();
  const st = document.getElementById('plugins-stack');
  const meta = id => st.querySelector('[data-entry="' + id + '"] .store-meta').textContent;

  assert(meta('org.example.runs') === '1.0.0 · signed', 'an installed row names the tier the host verified: ' + meta('org.example.runs'));
  assert(meta('org.example.newer') === '1.0.0 · reviewed', 'an installed row with an update names the release it runs: ' + meta('org.example.newer'));
  assert(st.querySelector('[data-entry="org.example.newer"]').textContent.includes('update to 2.0.0'), 'the update is still offered');
  assert(meta('org.example.offer') === '0.5.0 · the catalog says reviewed', 'an offering names its tier as the index claim: ' + meta('org.example.offer'));
  assert(!document.getElementById('store-list').textContent.includes('platform-signed'), 'an index claim the host did not verify is shown for a release it runs');
});
</script>`

func TestPluginStoreNamesTheVerifiedTierAndTheIndexClaim(t *testing.T) {
	runPluginStorePage(t, pluginStoreTierPage)
}

func runPluginStorePage(t *testing.T, template string) {
	t.Helper()
	read := func(name string) []byte {
		t.Helper()
		b, err := staticFS.ReadFile("static/" + name)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	page := strings.Replace(template, "__LAYOUT_CSS__", string(read("layout.css")), 1)
	if page == template {
		t.Fatal("stylesheet placeholder not substituted")
	}
	runPageInEngines(t, page, map[string][]byte{
		"/views/plugins.js": read("views/plugins.js"),
		"/state.js":         read("state.js"),
		"/util.js":          read("util.js"),
		"/views/settings.js": []byte(`export const saved = [];
export function captureForm(){} export function restoreForm(){} export function forgetForm(){} export function saveConfigSection(section) { saved.push(section); }
export function savebarHTML(section, note) { return '<div class="savebar"><button class="btn" data-save="' + section + '">Save</button><span class="savenote">' + note + '</span></div>'; }
export const sent = [];
export function sendConfigChanges(section, ch) { saved.push(section); sent.push(ch); }
export function configFeedbackHTML() { return ''; }`),
		"/ws.js": []byte(`export function send() {}`),
	})
}
