package dashboard

// The bridge contract, executed (threat row S1's frame half): node
// (which ships MessageChannel) runs the REAL frame bridge core
// (bridge.js — allowlists, wired-command registry, topic projections)
// against the REAL section client (section-api.js) over a live
// MessageChannel, and asserts refusal-by-name for undeclared topics
// and undeclared/unwired commands, the exact v0 command frame with its
// server gate's argument shape, token push, and the resize flow. The
// js_syntax walk proves the modules PARSE; this proves they BEHAVE —
// same skip rule when node is absent.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const bridgeDriver = `
import { createFrameBridge, topicOf, WIRED_COMMANDS } from './bridge.mjs';
import { ready, _accept } from './section_api.mjs';

function fail(msg) { console.error('FAIL: ' + msg); process.exit(1); }
function assert(cond, msg) { if (!cond) fail(msg); }
async function settle() { for (let i = 0; i < 8; i++) await new Promise(r => setTimeout(r, 0)); }

// --- topicOf: the precise ws.js-dispatch → topic mapping ---
let t = topicOf({ type: 'status', stats: { name: 'Dawn' } });
assert(t && t.topic === 'status' && t.data.name === 'Dawn', 'status projection');
t = topicOf({ type: 'projects', projects: [{ id: 'p1' }] });
assert(t && t.topic === 'projects' && t.data[0].id === 'p1', 'projects projection');
t = topicOf({ type: 'work', work: { queued: 2 } });
assert(t && t.topic === 'work' && t.data.queued === 2, 'work projection');
assert(topicOf({ type: 'identity', identity: {} }) === null, 'identity is NOT a v0 topic');
assert(topicOf({ type: 'config', config: {} }) === null, 'config is NOT a v0 topic');

// --- a real channel: frame bridge on port1, section api on port2 ---
const ch = new MessageChannel();
const wsFrames = [];
let resized = 0;
const bridge = createFrameBridge({
  topics: ['status', 'future.topic'],           // declared by the section
  commands: ['project.select', 'future.cmd'],   // declared; future.cmd is NOT wired
  wired: WIRED_COMMANDS,                        // the frame registry (v0: exactly one command)
  post: m => ch.port1.postMessage(m),
  sendToServer: f => wsFrames.push(f),
  onResize: h => { resized = h; },
});
ch.port1.onmessage = e => bridge.onFrame(e.data);
// v1 is the bridge contract; the mismatch case must throw BY NAME.
let threw = false;
try { _accept({ tokens: {} }, new MessageChannel().port2); } catch (e) { threw = /is not v1/.test(String(e)); }
if (!threw) { console.error('FAIL: version-less connect must be refused'); process.exit(1); }
_accept({ v: 1, tokens: { '--acc': '#8b6cff' } }, ch.port2);
const api = await ready();

// tokens: initial set arrives with the connect
let toks = null;
api.tokens(x => { toks = x; });
assert(toks && toks['--acc'] === '#8b6cff', 'initial tokens push');

// subscribe (declared) → relay flows
let seen = null;
await api.data.subscribe('status', d => { seen = d; });
bridge.publish('status', { name: 'Dawn' });
await settle();
assert(seen && seen.name === 'Dawn', 'declared+subscribed topic relays');

// undeclared subscribe → refused reply NAMING the topic; cb never fires
let leaked = false;
let refusedMsg = '';
await api.data.subscribe('identity', () => { leaked = true; }).then(
  () => fail('undeclared subscribe must refuse'),
  e => { refusedMsg = e.message; });
assert(refusedMsg.includes('identity') && refusedMsg.includes('not declared'), 'refusal names the topic: ' + refusedMsg);
bridge.publish('identity', { private: true });
await settle();
assert(!leaked, 'an undeclared topic must never reach the section');

// undeclared-by-section publish is dropped even if frame tried
bridge.publish('work', { queued: 1 });
await settle(); // 'work' was never declared by THIS section

// act: the ONE wired v0 command → the EXACT existing WS frame
const r = await api.act('project.select', { id: 'p-42' });
assert(r && r.forwarded === true, 'act resolves as forwarded');
assert(wsFrames.length === 1, 'exactly one WS frame sent');
const f = wsFrames[0];
assert(f.type === 'project' && f.project && f.project.action === 'select' && f.project.id === 'p-42',
  'v0 command maps to the existing project-select case, got ' + JSON.stringify(f));

// act with bad args → validate refuses before any send
await api.act('project.select', {}).then(
  () => fail('argument validation must refuse'),
  e => assert(e.message.includes('args.id'), 'refusal names the requirement: ' + e.message));
assert(wsFrames.length === 1, 'refused act must not touch the socket');

// act undeclared → refused naming the command
await api.act('config.set', { x: 1 }).then(
  () => fail('undeclared command must refuse'),
  e => assert(e.message.includes('config.set') && e.message.includes('not declared'), 'names the command: ' + e.message));

// act declared-but-unwired → refused naming the command (double allowlist)
await api.act('future.cmd', {}).then(
  () => fail('unwired command must refuse'),
  e => assert(e.message.includes('future.cmd') && e.message.includes('not wired'), 'names the unwired command: ' + e.message));
assert(wsFrames.length === 1, 'no refused act reaches the socket');

// tokens: on change
bridge.pushTokens({ '--acc': '#ffffff' });
await settle();
assert(toks['--acc'] === '#ffffff', 'token change pushes through');

// resize: section reports; frame hears the clamped px
api.resize(320);
await settle();
assert(resized === 320, 'resize flows to the frame, got ' + resized);
api.resize(999999);
await settle();
assert(resized === 4000, 'resize clamps at the ceiling, got ' + resized);

console.log('BRIDGE-OK');
process.exit(0);
`

func TestSectionBridgeContract(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not available — bridge contract check skipped")
	}
	tmp := t.TempDir()
	for src, dst := range map[string]string{
		"static/bridge.js":      "bridge.mjs",
		"static/section-api.js": "section_api.mjs",
	} {
		raw, rerr := staticFS.ReadFile(src)
		if rerr != nil {
			t.Fatalf("read embedded %s: %v", src, rerr)
		}
		if werr := os.WriteFile(filepath.Join(tmp, dst), raw, 0o600); werr != nil {
			t.Fatal(werr)
		}
	}
	driver := filepath.Join(tmp, "driver.mjs")
	if err := os.WriteFile(driver, []byte(bridgeDriver), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(node, driver).CombinedOutput()
	if err != nil || !strings.Contains(string(out), "BRIDGE-OK") {
		t.Fatalf("bridge contract failed (%v):\n%s", err, out)
	}
}
