//go:build !windows

package dashboard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// .
// .
// .
// .
// .

var pluginsPageStubs = map[string][]byte{
	"/views/settings.js": []byte(`export function saveConfigSection(){}; export function savebarHTML(){return ''}; export function sendConfigChanges(){}; export function configFeedbackHTML(){return ''}`),
	"/ws.js":             []byte(`export function send(){}`),
}

func TestPluginsPageShowsAStoppingPluginAndOffersNoRetry(t *testing.T) {
	page := `<!doctype html><div class="app"><div class="stack" id="plugins-stack"></div></div><script type="module">
import { assert, run } from './__harness.js';
import { S } from './state.js';
import { renderPlugins } from './views/plugins.js';
run(() => {
 S.config={plugins:{autoload:'T1',catalog:[],installed:[],pending:[
  {id:'org.example.gone',version:'1.0.0',phase:'retiring',summary:'stopping — what it held has not all come back',cleanup_at:'2026-01-02T03:04:05Z',
   lifecycle:{state:'draining',residue:['activation: retirement pending: [child 4821 not yet reaped]'],activations:[{gen:7,role:'retiring',version:'1.0.0'}]}},
  {id:'org.example.never',version:'2.0.0',phase:'retiring',summary:'stopping — what it held has not all come back',
   lifecycle:{state:'draining',residue:['activation: child 99 not yet reaped'],activations:[{gen:9,role:'retiring',version:'2.0.0'}],
    refusal:{stage:'health',class:'transient',cause:'the engine did not answer its health check'}}}]}};
 renderPlugins();
 const card = document.querySelector('[data-pending="org.example.gone"]');
 assert(card, 'a stopping plugin has a card');
 const text = card.textContent;
 assert(text.includes('stopping'), 'it says it is stopping: ' + text);
 assert(text.includes('generation 7 retiring'), 'it names the generation still held: ' + text);
 assert(text.includes('child 4821 not yet reaped'), 'it says what is still held: ' + text);
 assert(text.includes('03:04:05 UTC'), 'it says when the cleanup is asked again: ' + text);
 assert(!card.querySelector('[data-plugin^="retry:"]'), 'Try again is not offered: another engine does not make this one let go');
 assert(!card.querySelector('[data-plugin^="uninstall:"]'), 'Uninstall is not offered for what is already leaving');
 for (const promise of ['Activation follows', 'downloading', 'preparing']) assert(!text.includes(promise), 'it must not promise ' + promise + ': ' + text);
 const failed = document.querySelector('[data-pending="org.example.never"]').textContent;
 assert(failed.includes('last attempt refused: the engine did not answer its health check'), 'a failed start says why while its cleanup is pending: ' + failed);
 assert(failed.includes('transient at health'), 'with the class and stage: ' + failed);
});</script>`
	runPageInEngines(t, page, pluginsPageStubs)
}

func TestPluginsPageNamesARefusedPackageByWhereItWasFound(t *testing.T) {
	page := `<!doctype html><div class="app"><div class="stack" id="plugins-stack"></div></div><script type="module">
import { assert, run } from './__harness.js';
import { S } from './state.js';
import { renderPlugins } from './views/plugins.js';
run(() => {
 S.config={plugins:{autoload:'T1',catalog:[],installed:[],skips:[
  {kind:'policy',dir:'plugins/low',package:'plugins/low/low.aiiospkg',id:'org.example.low',tier:'T0',reason:'verified T0 is below plugins.autoload T1'},
  {kind:'unverified',dir:'plugins/broken',package:'plugins/broken/claims-to-be-core.aiiospkg',id:'',tier:'',reason:'verification failed — refused at every autoload level: the signature does not verify'},
  {kind:'ambiguous',dir:'plugins/two-of-them',id:'',tier:'',reason:'more than one package in this directory — refused whole; keep exactly one'},
  {kind:'duplicate',dir:'plugins/copy',package:'plugins/copy/low.aiiospkg',id:'org.example.low',tier:'T1',reason:'its verified id is already provided by plugins/low — duplicate refused; remove one'}]}};
 renderPlugins();
 const blocks = [...document.querySelectorAll('.store-skips')];
 assert(blocks.length === 2, 'kept-off and refused are two lists, found ' + blocks.length);
 const kept = blocks.find(b => !b.classList.contains('refused')).textContent, refused = blocks.find(b => b.classList.contains('refused')).textContent;
 assert(kept.includes('PRESENT, VERIFIED — NOT LOADED') && kept.includes('org.example.low') && !kept.includes('broken'), 'the policy list holds only what verified: ' + kept);
 assert(refused.includes('PRESENT — REFUSED'), 'the refusals have their own heading: ' + refused);
 assert(refused.includes('plugins/broken/claims-to-be-core.aiiospkg') && refused.includes('the signature does not verify'), 'an archive that does not verify is named by its path, with the reason: ' + refused);
 assert(refused.includes('plugins/two-of-them') && refused.includes('more than one package'), 'an ambiguous directory is named: ' + refused);
 assert(refused.includes('plugins/copy/low.aiiospkg') && refused.includes('(org.example.low)'), 'a duplicate carries the id verification established: ' + refused);
 assert(!refused.includes('undefined') && !refused.includes('()'), 'nothing is invented for a package with no verified identity: ' + refused);
});</script>`
	runPageInEngines(t, page, pluginsPageStubs)
}

// .
// .
// .
func TestPluginsPageRendersTheBackendsOwnDrainingExport(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "lifecycle", "draining.json"))
	if err != nil {
		t.Fatal(err)
	}
	page := `<!doctype html><div class="app"><div class="stack" id="plugins-stack"></div></div><script type="module">
import { assert, run } from './__harness.js';
import { S } from './state.js';
import { renderPlugins } from './views/plugins.js';
run(() => {
 S.config={plugins:__BACKEND__};
 const row = S.config.plugins.pending[0];
 assert(row && row.phase === 'retiring', 'the export holds a retiring row');
 renderPlugins();
 const card = document.querySelector('[data-pending="' + row.id + '"]');
 assert(card, 'the retiring row has a card');
 const text = card.textContent;
 assert(text.includes('stopping'), 'it says it is stopping: ' + text);
 assert(text.includes(row.residue[0]), 'it says what is still held: ' + text);
 assert(text.includes('generation ' + row.lifecycle.activations[0].gen + ' retiring'), 'it names the generation: ' + text);
 assert(!card.querySelector('[data-plugin^="retry:"]'), 'Try again is not offered');
});</script>`
	runPageInEngines(t, strings.Replace(page, "__BACKEND__", string(raw), 1), pluginsPageStubs)
}

// .
// .
// .
func TestPluginsPageShowsWhatSAFEHolds(t *testing.T) {
	page := `<!doctype html><div class="app"><div class="stack" id="plugins-stack"></div></div><script type="module">
import { assert, run } from './__harness.js';
import { S } from './state.js';
import { renderPlugins } from './views/plugins.js';
run(() => {
 const why = 'the identity is in SAFE — nothing new is activated while its integrity is unverified (the chain does not verify)';
 S.config={plugins:{autoload:'T1',catalog:[],
  installed:[{id:'org.example.serving',version:'1.0.0',tier:'T1',mode:'wasm',variant:'wasm',settings:[],
   lifecycle:{state:'active',held:why,activations:[{gen:1,role:'active',version:'1.0.0'}]}}],
  pending:[{id:'org.example.waiting',version:'2.0.0',phase:'held',summary:why}]}};
 renderPlugins();
 const card = document.querySelector('[data-pending="org.example.waiting"]');
 assert(card, 'a held plugin has a card');
 const text = card.textContent;
 assert(text.includes('held') && text.includes('the chain does not verify'), 'it says it is held, and why: ' + text);
 assert(!card.querySelector('[data-plugin^="retry:"]') && !card.querySelector('[data-plugin^="uninstall:"]'), 'nothing to press on a held row');
 for (const promise of ['Activation follows', 'downloading', 'preparing']) assert(!text.includes(promise), 'it must not promise ' + promise + ': ' + text);
 const all = document.getElementById('plugins-stack').textContent;
 assert(all.includes('held: ' + why), 'an update waiting behind what serves is said on the serving card: ' + all);
});</script>`
	runPageInEngines(t, page, pluginsPageStubs)
}
