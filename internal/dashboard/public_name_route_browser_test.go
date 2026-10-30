//go:build !windows

package dashboard

import (
	"strings"
	"testing"
)

func TestThePublicNameCardSaysWhatIsPublishedAndWhyARouteFailed(t *testing.T) {
	page := `<!doctype html><html><body><div id="settings-stack"></div>
<script type="module">
import { S } from '/state.js';
import { renderSettings } from '/views/settings.js';
import { run } from '/__harness.js';
run(async (assert) => {
  const config = (pn, over) => Object.assign({ llm: {}, speech: {}, prompt: {}, witness: {}, logs: {}, agency: {}, plugins: {}, updates: {}, restart_required: [],
    dashboard: { host: '127.0.0.1', port: 8181, tls: true, require_token: true }, secrets_ok: true,
    public_name: Object.assign({ status: 'issued', name: 'ui.abc.id.example.test', origin: 'https://ui.abc.id.example.test:8181', route_mode: 'direct', relays: [] }, pn || {}) }, over || {});
  S.view = 'settings'; S.providersLoaded = true; S.providers = [];
  const show = (pn, over) => { S.config = config(pn, over); renderSettings(); };
  const text = (sel) => { const el = document.querySelector(sel); return el ? el.textContent : null; };
  show();
  document.querySelector('[data-sec="dashboard"]').click();
  assert(text('[data-route-now]'), 'the chosen route is not shown');
  assert(text('[data-route-published]') === null && text('[data-route-error]') === null, 'the card says something about a route the host has not looked at yet');

  // A route the host refused, under an issued certificate.
  const refusal = 'this machine has more than one IPv4 address (10.0.0.5, 192.168.1.5) and the dashboard binds every interface; name the ones to publish in certificate.direct_addresses';
  show({ route_error: refusal });
  assert((text('[data-route-error]') || '').includes(refusal), 'A ROUTE THE HOST REFUSED IS SHOWN NOWHERE ON THE CARD: ' + text('[data-route]'));
  assert(text('[data-public-name-state]').includes('certificate issued') && !text('[data-public-name-state]').includes('could not be obtained'), 'a route failure changed what the card says of the certificate: ' + text('[data-public-name-state]'));

  // What the service last answered, beside the choice and the failure.
  show({ address: '10.0.0.5', route_error: 'certificate service: unreachable' });
  assert((text('[data-route-published]') || '').includes('10.0.0.5'), 'what the certificate service last answered is not shown: ' + text('[data-route]'));
  assert((text('[data-route-error]') || '').includes('certificate service: unreachable'), 'a service that is down is not shown: ' + text('[data-route]'));
  show({ route_mode: 'relay', relay_endpoint: 'relay.example.test', address: 'relayed through relay.example.test' });
  assert((text('[data-route-published]') || '').includes('relayed through relay.example.test') && text('[data-route-error]') === null, 'a relayed route is not said as the service answered it: ' + text('[data-route]'));

  // A pass that ended without an error is told, and not as a failure.
  show({ address: '10.0.0.5', route_error: 'another writer moved the route; recomputing' });
  assert((text('[data-route-error]') || '').includes('another writer moved the route') && !/fail/i.test(text('[data-route-error]')), 'A PASS THAT DID NOT FAIL IS CALLED A FAILURE: ' + text('[data-route-error]'));

  // In every status that holds a name; and the certificate's failure stays
  // the certificate's.
  show({ status: 'claiming', route_error: refusal });
  assert((text('[data-route-error]') || '').includes(refusal), 'the route failure is not shown while the certificate is being obtained');
  show({ status: 'failing', last_error: 'order: the CA refused the account', route_error: refusal });
  assert((text('[data-route-error]') || '').includes(refusal), 'the route failure is not shown beside a failing certificate');
  const state = text('[data-public-name-state]');
  assert(state.includes('order: the CA refused the account') && !state.includes('more than one IPv4'), 'THE ROUTE FAILURE IS PRINTED AS THE CERTIFICATE\'S: ' + state);

  // Off a private connection the route cannot be chosen, and is still told.
  show({ address: '10.0.0.5', route_error: refusal }, { secrets_ok: false });
  assert(!document.getElementById('cfg-route') && text('[data-route-published]') && text('[data-route-error]'), 'what came of the route is hidden off a private connection');

  // Recovered: the failure goes, what is published stays.
  show({ address: '10.0.0.5' });
  assert(text('[data-route-error]') === null && (text('[data-route-published]') || '').includes('10.0.0.5'), 'a recovered route still shows its failure: ' + text('[data-route]'));

  // What a resolver on this machine last answered for the name, or why it
  // did not; nothing before the first lookup, and off a private connection
  // too.
  assert(text('[data-route-resolves]') === null, 'the card speaks of a lookup not yet made: ' + text('[data-route]'));
  show({ address: '10.0.0.5', resolves_to: '10.0.0.5' });
  assert((text('[data-route-resolves]') || '').includes('resolves to 10.0.0.5'), 'WHAT THE NAME RESOLVES TO FROM THIS MACHINE IS NOT ON THE CARD: ' + text('[data-route]'));
  const dead = 'lookup ui.abc.id.example.test: no such host';
  show({ address: '10.0.0.5', resolve_error: dead }, { secrets_ok: false });
  assert((text('[data-route-resolves]') || '').includes('does not resolve: ' + dead), 'A NAME THAT DOES NOT RESOLVE FROM THIS MACHINE IS NOT SAID: ' + text('[data-route]'));

  // No name, no route to speak of.
  show({ status: 'none', name: '', route_error: refusal });
  assert(text('[data-route]') === null, 'a route is described for an identity with no name');
});
</script></body></html>`

	modules := map[string][]byte{}
	for _, path := range []string{"static/views/settings.js", "static/views/model-picker.js", "static/util.js"} {
		data, err := staticFS.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		modules["/"+strings.TrimPrefix(path, "static/")] = data
	}
	modules["/state.js"] = []byte(`export const S = { providers: [], config: null, providersLoaded: false };`)
	modules["/ws.js"] = []byte(`export function send() { return 'r1'; } export function query() { return 'req-1'; }`)
	modules["/sandbox.js"] = []byte(`export function sandboxCardHTML() { return ''; } export function wireSandboxCard() {}`)
	runPageInEngines(t, page, modules)
}
