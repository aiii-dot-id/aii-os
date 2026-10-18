//go:build !windows

package dashboard

import (
	"strings"
	"testing"
)

func shippedBirthForm(t *testing.T) string {
	t.Helper()
	index, err := staticFS.ReadFile("static/index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(index)
	const first, last = `<label class="f">PROVIDER</label>`, `<div id="fb-restore-body" hidden></div>`
	start, end := strings.Index(html, first), strings.Index(html, last)
	if start < 0 || end < start {
		t.Fatal("index.html has no birth form")
	}
	return `<!doctype html><div>` + html[start:end+len(last)] + `</div></div>`
}

const firstbootLocalServerScript = `<script type="module">
import { S } from './state.js';
import { frames } from './ws.js';
import { renderProviderOptions, acceptDiscoveryResponse, discoveryAnswered, discoveryFailed } from './firstboot.js';
import { assert, run } from './__harness.js';
const $ = id => document.getElementById(id);
const pick = i => { $('fb-provider').value = String(i); $('fb-provider').onchange(); };
const shown = () => getComputedStyle($('fb-url-row')).display !== 'none';
const discovery = () => frames.filter(f => f.type === 'query' && f.query === 'discover').at(-1);
const births = () => frames.filter(f => f.type === 'genesis');
const retype = url => { $('fb-url').value = url; $('fb-url').dispatchEvent(new Event('change')); };
run(() => {
  S.providers = [
    { name: 'Hosted', endpoint: 'https://hosted.test/v1', models: ['h1'], default_model: 'h1', chat: true },
    { name: 'Local server', endpoint: 'http://127.0.0.1:8080/v1', local: true, models: ['from-the-stored-address'], chat: true, status: 'unreachable', status_reason: 'stored address' },
  ];
  renderProviderOptions();

  pick(0);
  assert(!shown(), 'a hosted entry shows the server URL field');
  assert(!('base_url' in discovery()), 'a hosted entry sent an address to discovery: ' + JSON.stringify(discovery()));

  pick(1);
  assert(shown(), 'the local entry does not show the server URL field');
  assert($('fb-url').value === 'http://127.0.0.1:8080/v1', 'the field does not start at the entry\'s address: ' + $('fb-url').value);
  assert(discovery().provider === 'Local server' && discovery().base_url === 'http://127.0.0.1:8080/v1', 'discovery was not asked at the address in the field: ' + JSON.stringify(discovery()));
  assert($('fb-model').value === '' && $('fb-model').options.length === 1, 'models were offered before the address in the field answered');

  retype('http://127.0.0.1:1/v1');
  const silent = discovery();
  assert(silent.base_url === 'http://127.0.0.1:1/v1', 'a changed address was not asked: ' + JSON.stringify(silent));
  assert(acceptDiscoveryResponse(silent.request_id, 'Local server'), 'the answer to the latest ask was refused');
  assert(discoveryFailed('dial tcp 127.0.0.1:1: connect: connection refused'), 'a local server\'s silence was not taken by the form');
  assert($('fb-hint').textContent.startsWith('Nothing at http://127.0.0.1:1/v1 answered with its models'), 'the silence was not said plainly: ' + $('fb-hint').textContent);
  $('fb-birth').click();
  assert(births().length === 0, 'Birth went to an address where nothing answered');
  assert($('fb-result').textContent.includes('Birth waits until the server at http://127.0.0.1:1/v1 lists its models'), 'the refusal was not said: ' + $('fb-result').textContent);
  assert(!$('fb-birth').disabled, 'a refused Birth left the button disabled');

  retype('http://127.0.0.1:1234/v1');
  $('fb-birth').click();
  assert(births().length === 0, 'Birth went before the new address answered');
  const answered = discovery();
  assert(acceptDiscoveryResponse(answered.request_id, 'Local server'), 'the answer to the latest ask was refused');
  discoveryAnswered(['local-model']);
  assert($('fb-model').value === 'local-model' && $('fb-hint').textContent === '', 'the listed models were not offered');
  $('fb-birth').click();
  const g = births().at(-1);
  assert(g && g.genesis.provider === 'Local server' && g.genesis.endpoint === 'http://127.0.0.1:1234/v1' && g.genesis.model === 'local-model',
    'Birth did not go to the address that answered: ' + JSON.stringify(g));

  pick(0);
  assert(!shown() && !discoveryFailed('x'), 'a hosted entry kept the field or had its failure taken as a local server\'s');
});
</script>`

func TestBirthOnALocalServerWaitsForItsAddressToAnswer(t *testing.T) {
	runPageInEngines(t, shippedBirthForm(t)+firstbootLocalServerScript, substrateModules(t))
}
