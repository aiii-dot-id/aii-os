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
const projectsExitAndViewerPage = `<!doctype html>
<div id="proj-space"></div><div id="dock"></div><span id="pill-proj"></span>
<div id="crumb"></div><div id="toast"></div><div id="firstboot"></div><div id="composer-wrap"></div>
<script type="module">
import { report, assert } from '/__harness.js';
import { S } from '/state.js';
import { renderProjects, aiiOpenFile, closeFileViewer } from '/views/projects.js';
import { __sent, __reset } from '/ws.js';

/* Driven manually rather than through run(): the second half is
   asynchronous by construction, and run() reports OK the moment its
   body returns. report() is idempotent, so the first failure wins. */
function fail(e) { report('FAIL: ' + ((e && e.message) || String(e))); }

const P = { id: 'alpha', name: 'Alpha', state: 'open', description: 'first', dir: '/p/alpha', focus: 'fa' };

try {
  /* ---- the exit an operator can actually reach ---- */
  __reset();
  S.projects = [P];
  S.activeProject = P;
  S.viewedProject = 'alpha';
  S.projTab = 'overview';
  S.view = 'projects';
  S.workspace = null; S.workspaceFor = '';
  renderProjects();

  const btn = document.getElementById('proj-deselect');
  assert(btn, 'no way out: the project the identity is working in offers no deselect control, so the only exit from a focus is to CLOSE the project');
  btn.click();
  assert(__sent.length === 1 && __sent[0].project && __sent[0].project.action === 'deselect',
    'the exit control sent ' + JSON.stringify(__sent));

  /* It must not offer to leave a project you are not in. */
  __reset();
  S.activeProject = null;
  renderProjects();
  assert(!document.getElementById('proj-deselect'),
    'offered to leave a project the identity is not working in');
} catch (e) { fail(e); }

/* ---- a settled response belongs to the overlay that asked for it ----
   Two files opened; the FIRST request completes LAST. That is the
   interleaving reproduced in both engines: the first file's bytes
   painted under the second file's title, because the promise callbacks
   addressed #fv-body by id instead of their own overlay. */
const pending = [];
window.fetch = (u) => new Promise(res => pending.push({ u: u, res: res }));
const txt = (b) => new Response(b, { headers: { 'content-type': 'text/plain' } });

try {
  aiiOpenFile('alpha', 'first.txt');
  aiiOpenFile('alpha', 'second.txt');
} catch (e) { fail(e); }

setTimeout(() => {
  try {
    assert(pending.length === 2, 'expected two in-flight fetches, got ' + pending.length);
    pending[1].res(txt('SECOND-BYTES'));
    setTimeout(() => {
      try {
        pending[0].res(txt('FIRST-BYTES'));
        setTimeout(() => {
          try {
            const title = document.getElementById('fv-title').textContent;
            const body = document.getElementById('fv-body').textContent;
            assert(title === 'second.txt', 'the visible overlay is not the last one opened: ' + title);
            assert(body.indexOf('FIRST-BYTES') === -1,
              'the FIRST file was painted under the SECOND file title: ' + body);
            assert(body.indexOf('SECOND-BYTES') !== -1,
              'the viewer lost its own content: ' + body);
            closeFileViewer();
            report('OK');
          } catch (e) { fail(e); }
        }, 40);
      } catch (e) { fail(e); }
    }, 10);
  } catch (e) { fail(e); }
}, 0);
</script>`

func TestProjectExitAndFileViewerOwnershipInBrowser(t *testing.T) {
	runPageInEngines(t, projectsExitAndViewerPage, overrideModuleStubs(
		"/ws.js", []byte(wsRecordingStub),
	))
}
