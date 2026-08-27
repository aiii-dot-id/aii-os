//go:build !windows

// Runs only through the unix browser-engine rig (browserengine_test.go,
// same tag) — this file calls its helpers, so it shares its build row.

package dashboard

import "testing"

// The project cluster's client half, driven through the REAL ws.js and
// views/projects.js against a controllable fake socket (D72, D73):
//
//   - an unrelated projects broadcast landing while a create is in
//     flight must not adopt whatever id it carries (the old code moved
//     the operator to the wrong project);
//   - the error wearing OUR create's request id disarms the pending
//     create (the old code left it armed forever);
//   - the answer wearing our id adopts exactly the new project;
//   - a CLOSED project's page offers "Reopen project", never "Work in
//     this project" — the backend refuses that move anyway, so the old
//     button could only ever error — and clicking it sends the real
//     reopen act over the wire.
const projClusterPage = `<!DOCTYPE html><html><head><title>projects</title></head><body>
<button id="send-btn">send</button>
<span id="pill-proj"></span>
<div id="proj-space"></div>
<div id="dock"></div>
<script type="module" src="/probe.js"></script>
</body></html>`

const projClusterProbeJS = `
import { report } from '/__harness.js';

const fail = m => report('FAIL: ' + m);
try {
  let sock;
  window.WebSocket = class {
    constructor(url) { sock = this; this.readyState = 0; this.sent = []; }
    send(f) { this.sent.push(f); }
    close() {}
  };

  const { S } = await import('/state.js');
  const { connect } = await import('/ws.js');
  const { createPending } = await import('/views/projects.js');
  S.identityExists = true;
  connect();
  sock.readyState = 1;
  sock.onopen();
  const deliver = m => sock.onmessage({ data: JSON.stringify(m) });

  // Baseline list, then arm a create as the view does (request id '7').
  deliver({ type: 'projects', projects: [{ id: 'p1', name: 'One', state: 'open' }] });
  createPending.arm({}, '7');

  // An unrelated broadcast (no request id) lands first: it must not
  // disarm, and it must not move the operator anywhere.
  deliver({ type: 'projects', projects: [{ id: 'p1', name: 'One', state: 'open' }, { id: 'p9', name: 'Nine', state: 'open' }] });
  if (!createPending.waiting() || createPending.waiting().requestID !== '7') fail('an unrelated broadcast disarmed the pending create (D72)');
  else if (S.viewedProject !== null) fail('an unrelated broadcast moved the view to ' + S.viewedProject + ' (D72)');
  else {
    // The refusal wearing our id disarms it.
    deliver({ type: 'error', message: 'name refused', request_id: '7' });
    if (createPending.waiting()) fail('the correlated refusal did not disarm the create (D72)');
    else {
      // The answer wearing our id adopts exactly the new project.
      createPending.arm({}, '8');
      deliver({ type: 'projects', request_id: '8', projects: [{ id: 'p1', name: 'One', state: 'open' }, { id: 'p9', name: 'Nine', state: 'open' }, { id: 'p2', name: 'Two', state: 'open' }] });
      if (S.viewedProject !== 'p2') fail('the correlated answer did not adopt the new project: ' + S.viewedProject);
      else if (createPending.waiting()) fail('the matching answer did not disarm the create');
      else {
        // D73: a closed project's page offers reopen, never the move
        // the backend must refuse.
        S.view = 'projects';
        S.viewedProject = 'p9';
        deliver({ type: 'projects', projects: [{ id: 'p9', name: 'Nine', state: 'closed', focus: '', dir: '/x', description: '' }] });
        const reopen = document.getElementById('proj-reopen');
        const work = document.getElementById('proj-work');
        if (!reopen) fail('a closed project offers no Reopen button (D73)');
        else if (work) fail('a closed project still offers "Work in this project" (D73)');
        else {
          reopen.click();
          const last = JSON.parse(sock.sent[sock.sent.length - 1]);
          if (last.type !== 'project' || last.project.action !== 'reopen' || last.project.id !== 'p9')
            fail('the Reopen button did not send the reopen act: ' + JSON.stringify(last));
          else report('OK');
        }
      }
    }
  }
} catch (e) { fail((e && e.message) || String(e)); }
`

func TestProjectCreateCorrelationAndClosedAffordance(t *testing.T) {
	real := func(p string) []byte {
		b, err := staticFS.ReadFile("static/" + p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		return b
	}
	// Map DERIVED from the shared base (fork removed). Two deliberate
	// overrides: the real ws.js (this test drives the full dispatch —
	// the recording stub would replace real ws.js's message pump,
	// breaking the request-id correlation path), and an inert app.js —
	// this page drives ws.js + projects.js directly through a fake
	// socket; the real router's boot connect() would race it.
	modules := overrideModuleStubs(
		"/probe.js", []byte(projClusterProbeJS),
		"/ws.js", real("ws.js"),
		"/app.js", stubModule("go", "renderFirstbootVisibility", "toast"),
	)
	runPageInEngines(t, projClusterPage, modules)
}
