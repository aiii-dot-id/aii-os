//go:build !windows

// .
// .

package dashboard

import (
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
// .
// .

// .
// .
// .
// .
const pendingSlotsPage = `<!doctype html>
<button id="send-btn" disabled></button>
<button id="mic" disabled></button>
<script type="module">
import { assert, run } from './__harness.js';
import { pendingSlot } from './pending.js';
import { connect } from './ws.js';
import { __lostCalled } from './views/projects.js';

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

  // EVERY SURFACE IS NAMED IN THE DISCONNECT. The projects surface owns
  // two waits whose answers only the dead socket could deliver; if the
  // handler stops naming it, a create survives its connection and the
  // button stays inert until a page reload.
  connect();
  sock.onclose();
  assert(__lostCalled(),
    'THE DISCONNECT DID NOT REACH THE PROJECTS SURFACE: its create and focus waits outlive the socket that carried them');
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
	// .
	// .
	runPageInEngines(t, pendingSlotsPage, map[string][]byte{
		"/ws.js":    wsJS,
		"/state.js": stateJS,
		// .
		// .
		// .
		"/views/projects.js": []byte(`let lost = false;
export const renderProjPill = () => {}; export const renderProjects = () => {};
export const newlyCreatedID = () => null; export const rejectCreate = () => false;
export const acceptCreate = () => ''; export const acceptFocusSave = () => false; export const acceptContractSave = () => false;
export const rejectFocusSave = () => false; export const rejectContractSave = () => false;
export function projectsConnectionLost() { lost = true; return true; }
export function __lostCalled() { return lost; }
`),
		// .
		// .
		// .
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
		"/views/chat.js":     []byte(`export const addMsg = () => null; export const attachSpeaker = () => false; export const sysLine = () => {}; export const toolEventLive = () => {}; export const thinkingEvent = () => {}; export const renderHistory = () => {}; export const renderChatSubstrate = () => {}; export const acceptSubstrateConfig = () => {}; export const rejectSubstrateConfig = () => {}; export const substrateConnectionLost = () => {}; export const renderSteering = () => {}; export const renderAsks = () => {};`),
		"/views/home.js":     []byte(`export const renderHome = () => {};`),
		"/views/work.js":     []byte(`export const renderWorkPill = () => {};`),
		"/presence.js":       []byte(`export const renderPresence = () => {}; export const setThinking = () => {};`),
		"/util.js":           []byte(`export const $ = id => document.getElementById(id); export const esc = v => String(v ?? '');`),
		"/bridge.js":         []byte(`export const send = () => ''; export const query = () => '';`),
	})
}
