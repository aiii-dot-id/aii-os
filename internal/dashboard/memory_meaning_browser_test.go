//go:build !windows

package dashboard

import (
	"strings"
	"testing"
)

func TestTheMemoryViewsMeaningCardSaysOffOrWhatItIsBuiltFrom(t *testing.T) {
	page := `<!doctype html><html><body>
<div id="memory-stack"></div>
<script type="module">
import { run } from '/__harness.js';
import { S } from '/state.js';
import { renderMemory } from '/views/memory.js';
import { sent } from '/ws.js';
run(async (assert) => {
  const base = { beliefs: [], intentions: [], experiences: [] };
  const card = () => document.getElementById('mem-meaning');
  S.view = 'memory';

  S.identity = { ...base };
  renderMemory();
  assert(!card(), 'no meaning state from the server draws no card');

  S.identity = { ...base, meaning: { unavailable: 'provider "zAI" names no embeddings_model <b>(Settings → Providers)</b>' } };
  renderMemory();
  assert(card() && card().textContent.includes('Off: provider "zAI" names no embeddings_model <b>(Settings → Providers)</b>.'), 'off, with the reason drawn as text: ' + card().textContent);
  assert(!card().querySelector('b'), 'the reason is text, never markup');
  assert(card().textContent.includes('Settings → Providers → Advanced'), 'off says where to turn it on');
  assert(card().textContent.includes('text is then sent to that provider once to be embedded'), 'off says what turning it on sends, before it is turned on');
  assert(!card().textContent.includes('hold a vector'), 'off draws no coverage');

  S.identity = { ...base, meaning: { source: 'provider', basis: 'Local/bge-m3', floor: 0.3,
    coverage: [{ store: 'experiences', rows: 5, embedded: 2 }, { store: 'beliefs', rows: 1, embedded: 1 },
      { store: 'conversations', rows: 2000, embedded: 2000, beyond: 340, beyond_embedded: 120 }],
    last_pass: { at: '2026-01-02T03:04:05Z', embedded: 2, stopped: 'the endpoint said <i>no</i>' } } };
  renderMemory();
  const text = card().textContent;
  assert(text.includes('conversations 2000 of 2000 hold a vector 340 older are outside the provider\'s window (memory.conversation_vector_window) and get no new vector: 120 keep the one they have and are read by meaning, 220 are read by words alone'),
    'a provider\'s window bounds new vectors, and the older rows that keep one are read by meaning: ' + text);
  assert(!text.includes('experiences 2 of 5 hold a vector 0 older') && (text.match(/outside the provider's window/g) || []).length === 1, 'a store with nothing beyond a window says nothing of one: ' + text);
  assert(text.includes('basis Local/bge-m3') && text.includes('floor 0.300'), 'on names the basis and the floor, to three decimals as a recall shows it: ' + text);
  assert(text.includes('text is sent to that provider once to be embedded, and again if the model changes'), 'on says where the record\'s text goes: ' + text);
  assert(text.includes('it was measured for none'), 'the floor is not left to read as a measurement of this model: ' + text);
  assert(text.includes('experiences 2 of 5 hold a vector') && text.includes('beliefs 1 of 1 hold a vector'), 'each store has its coverage: ' + text);
  assert(text.includes('3 waiting for the backfill'), 'what waits is counted across the stores: ' + text);
  assert(text.includes('2 embedded, then stopped: the endpoint said <i>no</i>.') && !card().querySelector('i'), 'the last pass says why it stopped, as text: ' + text);
  assert(!text.includes('Off:'), 'on is not also off');

  S.identity = { ...base, meaning: { source: 'provider', basis: 'Local/bge-m3', floor: 0.3,
    coverage: [{ store: 'conversations', rows: 50, embedded: 50, beyond: 20 }] } };
  renderMemory();
  assert(card().textContent.includes('20 older are outside the provider\'s window (memory.conversation_vector_window) and get no new vector: 20 are read by words alone') &&
    !card().textContent.includes('keep the one they have'), 'older rows that kept no vector are all read by words alone: ' + card().textContent);

  S.identity = { ...base, meaning: { source: 'provider', choice: 'provider', provider_model: true, basis: 'Local/bge-m3', floor: 0.6,
    coverage: [{ store: 'experiences', rows: 5, embedded: 5 }] } };
  renderMemory();
  assert(card().textContent.includes('Every row in scope holds a vector') && !card().textContent.includes('waiting for the backfill'), 'a complete record says so: ' + card().textContent);
  assert(!card().textContent.includes('Last pass'), 'no pass since this start draws no pass line');

  const choice = () => document.getElementById('mem-source');
  const labels = () => Array.from(choice().options).map(o => o.value + '=' + o.textContent);
  assert(choice() && choice().value === 'provider' && labels().length === 2, 'with a choice stored and no plugin running, the card offers off and the provider: ' + labels().join(' | '));
  S.identity = { ...base, meaning: { unavailable: 'no source of vectors is set up' } };
  renderMemory();
  assert(choice().value === '' && labels().length === 3 && labels()[0] === '=not chosen yet', 'with nothing stored the card shows that nothing is chosen yet: ' + labels().join(' | '));
  assert(labels()[2].includes('(none is named)'), 'a provider that names no model says so where it is offered: ' + labels()[2]);

  S.identity = { ...base, meaning: { source: 'plugin', plugin: 'org.example.vectors', choice: 'plugin:org.example.vectors', basis: 'org.example.vectors/03d923bb1850/f1', floor: 0.3,
    plugins: ['org.example.vectors'], provider_model: true, waiting: true,
    question: 'A plugin is running and the provider is in use. <u>Choose.</u>',
    coverage: [{ store: 'experiences', rows: 5, embedded: 2 }] } };
  renderMemory();
  const plug = card().textContent;
  assert(plug.includes('Source: the plugin org.example.vectors, on this machine') && plug.includes('basis org.example.vectors/03d923bb1850/f1') && plug.includes('floor 0.300'), 'a plugin source is named with its basis and floor, to three decimals: ' + plug);
  assert(plug.includes('text is handed to that plugin once to be embedded'), 'the card says where the text goes: ' + plug);
  assert(plug.includes('declares no permission of any kind') && plug.includes('only while the operator has granted it none'), 'the card says what the host holds the plugin to: ' + plug);
  assert(!plug.includes('sent to that provider') && !plug.includes('measured for none'), 'a plugin source is not described as a provider: ' + plug);
  assert(plug.includes('3 waiting for voice: the backfill holds off while a session is listening or a reply is being spoken') && !plug.includes('silence') && !plug.includes('next pass'), 'a backfill held off for voice says what it waits for, and promises no silence: ' + plug);
  assert(plug.includes('A plugin is running and the provider is in use. <u>Choose.</u>') && !card().querySelector('u'), 'the host\'s question is drawn as text: ' + plug);
  assert(choice().value === 'plugin:org.example.vectors' && labels().length === 3 && labels()[2] === 'plugin:org.example.vectors=the plugin org.example.vectors, on this machine', 'the stored plugin is shown chosen, and "not chosen yet" is no longer offered: ' + labels().join(' | '));
  assert(!labels()[1].includes('none is named'), 'a provider that names a model is offered plainly');

  assert(!plug.includes('set aside'), 'nothing set aside draws no line for it');
  S.identity = { ...base, meaning: { source: 'plugin', plugin: 'org.example.vectors', basis: 'org.example.vectors/03d923bb1850/f1', floor: 0.3,
    plugins: ['org.example.vectors'], set_aside: 2, set_aside_why: 'plugin org.example.vectors gave no vector: <b>it</b> answered with an error',
    coverage: [{ store: 'experiences', rows: 5, embedded: 3 }] } };
  renderMemory();
  assert(card().textContent.includes('2 set aside: the source could not embed them (plugin org.example.vectors gave no vector: <b>it</b> answered with an error). Asked again when the source next starts.') && !card().querySelector('b'),
    'memories set aside are counted with the reason, drawn as text: ' + card().textContent);
  S.identity.meaning.set_aside = 1;
  renderMemory();
  assert(card().textContent.includes('1 set aside: the source could not embed it ('), 'one is said as one: ' + card().textContent);
  S.identity = { ...base, meaning: { source: 'plugin', plugin: 'org.example.vectors', basis: 'org.example.vectors/03d923bb1850/f1', floor: 0.3,
    plugins: ['org.example.vectors'], provider_model: true, coverage: [{ store: 'experiences', rows: 5, embedded: 2 }] } };
  renderMemory();

  const before = sent.length;
  choice().value = 'provider';
  choice().dispatchEvent(new Event('change'));
  assert(sent.length === before + 1 && sent[before][0] === 'send' && sent[before][1].type === 'config_set' &&
    JSON.stringify(sent[before][1].config) === '{"memory.meaning_source":"provider"}', 'a choice is sent as the one key: ' + JSON.stringify(sent.slice(before)));
  choice().value = 'none';
  choice().dispatchEvent(new Event('change'));
  assert(JSON.stringify(sent[before + 1][1].config) === '{"memory.meaning_source":"none"}', 'turning the layer off is sent as its own value: ' + JSON.stringify(sent[before + 1]));

  S.identity = { ...base, meaning: { choice: 'plugin:gone.example', why: 'not_running', unavailable: 'the plugin chosen to make the vectors, gone.example, is not running' } };
  renderMemory();
  assert(choice().value === 'plugin:gone.example' && labels()[2].includes('(not running)'), 'a stored choice of a plugin that is not running stays shown: ' + labels().join(' | '));
  assert(card().textContent.includes('Off: the plugin chosen to make the vectors, gone.example, is not running.'), 'and the card says why the layer is off: ' + card().textContent);
  assert(card().textContent.includes('install one in Plugins'), 'off names the plugin way too: ' + card().textContent);
});
</script></body></html>`
	modules := map[string][]byte{}
	for _, path := range []string{"static/views/memory.js", "static/state.js", "static/util.js"} {
		data, err := staticFS.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		modules["/"+strings.TrimPrefix(path, "static/")] = data
	}
	modules["/ws.js"] = []byte(`export const sent = []; export function query(q, extra) { sent.push([q, extra || {}]); return true; } export function send(m) { sent.push(['send', m]); return '1'; } export function wsReady() { return true; }`)
	runPageInEngines(t, page, modules)
}
