//go:build !windows

// Runs only through the unix browser-engine rig (browserengine_test.go,
// same tag) — this file calls its helpers, so it shares its build row.

package dashboard

import (
	"testing"
)

/*
Five surfaces waited on a correlated request id, each with its own

	copy of the rules, and each copy grew its own bug: two banners that
	waited forever on an answer that had already arrived, and a Create
	button left inert until a page reload because the disconnect sweep
	named four pendings and not that one.

	pending.js owns both rules now, and this pins the one that cannot be
	pinned by testing any single surface: A SLOT CANNOT BE LEFT OUT OF
	THE SWEEP. The page registers a slot the production code has never
	heard of, arms it, and drops the socket. If ws.js ever goes back to
	clearing pendings by name, this slot survives the disconnect and the
	test says so — which is exactly the shape of the bug that shipped.

	The claim rules are pinned here too, on the real module: the answer
	clears the wait whatever it says, a foreign request id never steals
	another surface's answer, and an unsent request arms nothing.
*/
const pendingSlotsPage = `<!doctype html>
<button id="send-btn" disabled></button>
<button id="mic" disabled></button>
<script type="module">
import { assert, run } from './__harness.js';
import { pendingSlot, dropAllPending } from './pending.js';
import { connect } from './ws.js';

let sock;
window.WebSocket = function (url) { sock = this; this.readyState = 0; this.send = () => {}; this.close = () => {}; };

run(() => {
  // CLAIMING ALWAYS CLEARS — the wedge class, at its source.
  const slot = pendingSlot();
  assert(slot.arm({ what: 'a save' }, '7') === true, 'arming with a request id must report that we are waiting');
  assert(slot.waiting().what === 'a save', 'the slot did not hold what it was armed with');
  assert(slot.claim('9') === null, 'a FOREIGN request id claimed our answer');
  assert(slot.waiting() !== null, 'a foreign claim cleared the slot it did not own');
  const got = slot.claim('7');
  assert(got && got.what === 'a save', 'the matching claim did not return the payload');
  assert(slot.waiting() === null, 'CLAIM DID NOT CLEAR: this is the banner that waits forever on an answer already in hand');
  assert(slot.claim('7') === null, 'the same answer was claimed twice');

  // An unsent request arms nothing: send() on a closed socket yields no id.
  assert(slot.arm({ what: 'x' }, '') === false, 'arming with no request id must report that nothing is pending');
  assert(slot.waiting() === null, 'a request that was never sent left the surface waiting');

  // THE SWEEP REACHES A SLOT THE FRAME NEVER NAMED.
  const unknown = pendingSlot();
  unknown.arm({ what: 'a create' }, '11');
  connect();          // real ws.js, against the fake socket above
  sock.onclose();     // the socket dies mid-flight
  assert(unknown.waiting() === null,
    'THE DISCONNECT LEFT A SLOT ARMED: its answer wears a request id only the dead socket could deliver, ' +
    'so the surface holding it is inert until a page reload');
});
</script>
`

func TestPendingSlotsClearOnClaimAndOnDisconnect(t *testing.T) {
	wsJS, err := staticFS.ReadFile("static/ws.js")
	if err != nil {
		t.Fatal(err)
	}
	stateJS, err := staticFS.ReadFile("static/state.js")
	if err != nil {
		t.Fatal(err)
	}
	// pending.js is served REAL (unstubbed paths resolve to the shipped
	// file) — stubbing the module under test would prove nothing.
	runPageInEngines(t, pendingSlotsPage, map[string][]byte{
		"/ws.js":    wsJS,
		"/state.js": stateJS,
		// ws.js imports many modules; stubs suffice — the connect path
		// only calls S mutation, sysLine (identityExists false), and the
		// reconnect scheduler. None of the renders fire without payloads.
		"/views/projects.js": []byte(`export const renderProjPill = () => {}; export const renderProjects = () => {}; export const newlyCreatedID = () => null; export const rejectCreate = () => false; export const acceptCreate = () => ''; export const acceptFocusSave = () => false;`),
		"/views/memory.js":   []byte(`export const renderMemory = () => {};`),
		"/views/identity.js": []byte(`export const renderIdentity = () => {};`),
		"/views/plugins.js":  []byte(`export const renderPlugins = () => {};`),
		"/views/settings.js": []byte(`export const renderSettings = () => {}; export const acceptSettingsConfig = () => {}; export const rejectSettingsConfig = () => {}; export const acceptProviderSave = () => {}; export const rejectProviderSave = () => {}; export const settingsConnectionLost = () => {};`),
		"/firstboot.js":      []byte(`export const renderProviderOptions = () => {}; export const setModelOptions = () => {}; export const fbHint = () => {}; export const fbResult = () => {}; export const acceptDiscoveryResponse = () => {}; export const firstbootConnectionLost = () => {};`),
		"/sections.js":       []byte(`export const publish = () => {}; export const onSections = () => {}; export const onLayout = () => {}; export const onTokensChanged = () => {};`),
		"/panel.js":          []byte(`export const renderPanel = () => {};`),
		"/theme.js":          []byte(`export const onTheme = () => {};`),
		"/overlay.js":        []byte(`export const onOverlayChanged = () => {};`),
		"/voice.js":          []byte(`export const bindTransport = () => {}; export const render = () => {}; export const speak = () => {}; export const connectionLost = () => {};`),
		"/app.js":            []byte(`export const sysLine = () => {}; export const toast = () => {}; export const go = () => {}; export const renderFirstbootVisibility = () => {};`),
		"/views/chat.js":     []byte(`export const addMsg = () => {}; export const sysLine = () => {}; export const toolEventLive = () => {}; export const thinkingEvent = () => {}; export const renderHistory = () => {}; export const renderChatSubstrate = () => {}; export const acceptSubstrateConfig = () => {}; export const rejectSubstrateConfig = () => {}; export const substrateConnectionLost = () => {}; export const renderSteering = () => {};`),
		"/views/home.js":     []byte(`export const renderHome = () => {};`),
		"/views/work.js":     []byte(`export const renderWorkPill = () => {};`),
		"/presence.js":       []byte(`export const renderPresence = () => {}; export const setThinking = () => {};`),
		"/util.js":           []byte(`export const $ = id => document.getElementById(id); export const esc = v => String(v ?? '');`),
		"/bridge.js":         []byte(`export const send = () => ''; export const query = () => '';`),
	})
}
