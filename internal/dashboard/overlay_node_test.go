package dashboard

// The W3 applier contract, executed (node): overlay.js is the client
// half of overlay hot reload — the seam the Go harness cannot reach.
// The js_syntax walk proves the module parses; THIS proves it behaves.
// Contract after the P1 fix: the live trigger is overlay_changed
// (fresh monotonic token + changed paths); the `overlays` audit list
// is render-only and MUST NOT apply anything. Stale tokens are
// ignored, css swaps href without reload, .js forks reload with the
// draft persisted first. Same skip rule when node is absent.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const overlayDriver = `
import { onOverlayChanged } from './overlay.mjs';

function fail(msg) { console.error('FAIL: ' + msg); process.exit(1); }
function assert(cond, msg) { if (!cond) fail(msg); }

// --- a minimal DOM: the frame ships one stylesheet tag per servable
// css, with relative hrefs (./custom.css etc). ---
const tags = [];
function makeLink(href) {
  const el = {
    href,
    attrs: { href },
    getAttribute(k) { return this.attrs[k]; },
    setAttribute(k, v) { this.attrs[k] = v; this.href = v; },
  };
  tags.push(el);
  return el;
}
makeLink('./theme.css');    // the frame's compiled stylesheet
makeLink('./custom.css');   // the shipped additive-layer stub
let reloaded = false;
let stored = null;
const input = { value: '  half-typed words  ' };
globalThis.document = {
  querySelectorAll: sel => (sel === 'link[rel="stylesheet"]' ? tags : []),
  getElementById: id => (id === 'msg-input' ? input : null),
};
globalThis.location = { reload: () => { reloaded = true; } };
globalThis.sessionStorage = {
  getItem: k => (k === 'aii.draft' ? stored : null),
  setItem: (k, v) => { if (k === 'aii.draft') stored = v; },
  removeItem: k => { if (k === 'aii.draft') stored = null; },
};

// --- live token 1: css path swaps href with the token, no reload ---
onOverlayChanged(1, ['/custom.css']);
const tag = tags[1];
assert(tag.attrs.href === './custom.css?v=1', 'css href swapped with token, got ' + tag.attrs.href);
assert(!reloaded, 'css swap must NOT reload');

// --- stale/duplicate token: ignored entirely ---
onOverlayChanged(1, ['/custom.css']);
assert(tag.attrs.href === './custom.css?v=1', 'duplicate token left href alone, got ' + tag.attrs.href);
onOverlayChanged(0, ['/custom.css']);
assert(tag.attrs.href === './custom.css?v=1', 'stale token (0 <= last) left href alone, got ' + tag.attrs.href);

// --- token 2: a real edit moves the href ---
onOverlayChanged(2, ['/custom.css']);
assert(tag.attrs.href === './custom.css?v=2', 'fresh token moved the href, got ' + tag.attrs.href);

// --- stale token carrying a NEW path: ignored entirely (the guard's
// real job — an out-of-order push must never apply behind token 2).
// theme.css is the tagged path: an unguarded stale apply would move
// tags[0]'s href, which the assertion can see. ---
onOverlayChanged(1, ['/theme.css']);
assert(tags[0].attrs.href === './theme.css', 'stale token with new path must not apply, got ' + tags[0].attrs.href);
assert(!reloaded, 'stale token must not reload');

// --- .js path in the changed set: reload fires, draft persisted first ---
onOverlayChanged(3, ['/app.js']);
assert(reloaded, 'changed .js path must reload');
assert(stored === '  half-typed words  ', 'draft persisted before reload, got ' + JSON.stringify(stored));

// --- malformed pushes are ignored, never throw ---
onOverlayChanged('nope', ['/custom.css']);
onOverlayChanged(4, 'not-an-array');
onOverlayChanged(4, [42]);

// --- restoreDraft is exercised at boot by app.js (unit: direct) ---
import('./overlay.mjs').then(m => {
  stored = 'returned draft';
  m.restoreDraft();
  assert(input.value === 'returned draft', 'restoreDraft fills the composer');
  assert(stored === null, 'restoreDraft is one-shot: value removed on read');
  console.log('OVERLAY-OK');
  process.exit(0);
});
`

func TestOverlayApplierContract(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not available — overlay applier contract check skipped")
	}
	tmp := t.TempDir()
	raw, rerr := staticFS.ReadFile("static/overlay.js")
	if rerr != nil {
		t.Fatalf("read embedded overlay.js: %v", rerr)
	}
	if werr := os.WriteFile(filepath.Join(tmp, "overlay.mjs"), raw, 0o600); werr != nil {
		t.Fatal(werr)
	}
	driver := filepath.Join(tmp, "driver.mjs")
	if err := os.WriteFile(driver, []byte(overlayDriver), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(node, driver).CombinedOutput()
	if err != nil || !strings.Contains(string(out), "OVERLAY-OK") {
		t.Fatalf("overlay applier contract failed (%v):\n%s", err, out)
	}
}
