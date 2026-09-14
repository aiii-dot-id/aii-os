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
const projectsContractFailurePage = `<!doctype html>
<div id="proj-space"></div><div id="dock"></div><span id="pill-proj"></span>
<div id="crumb"></div><div id="toast"></div><div id="firstboot"></div><div id="composer-wrap"></div>
<script type="module">
import { run, assert } from '/__harness.js';
import { S } from '/state.js';
import { renderProjects, projectsConnectionLost, rejectContractSave, acceptContractSave } from '/views/projects.js';
import { __sent, __reset } from '/ws.js';

const UMBRELLA = { id: 'umbrella', name: 'Umbrella', state: 'open', dir: '/p/umbrella' };
const OTHER = { id: 'other', name: 'Other', state: 'open', dir: '/p/other' };
const ALPHA = {
  id: 'alpha', name: 'Alpha', state: 'open', description: 'first', dir: '/p/alpha', active: true,
  contract: { outcome: 'stored outcome', acceptance: ['stored a'], constraints: ['stored c'] },
  parent: 'umbrella',
};

function fresh() {
  __reset();
  projectsConnectionLost(); // drop any slot armed by a previous case
  S.projects = [ALPHA, UMBRELLA, OTHER];
  S.activeProject = ALPHA;
  S.viewedProject = 'alpha';
  S.projTab = 'overview';
  S.view = 'projects';
  S.contractDraft = null;
  S.workspace = null; S.workspaceFor = '';
}

function author(o, a, c, p) {
  document.getElementById('ct-outcome').value = o;
  document.getElementById('ct-accept').value = a;
  document.getElementById('ct-constr').value = c;
  document.getElementById('ct-parent').value = p;
}

run(() => {
  // ---- A REFUSED SAVE DOES NOT WEDGE THE EDITOR ----
  fresh();
  renderProjects();
  author('authored outcome', 'authored a', 'authored c', 'other');
  document.getElementById('ct-save').onclick();
  assert(__sent.length === 1, 'first save was not sent');
  const reqID = 'req-1';

  // The server refuses it, correlated by request id — what ws.js does.
  assert(rejectContractSave(reqID) === true, 'the refusal was not claimed by the contract slot');

  // The operator's text must still be on screen after a re-render.
  renderProjects();
  assert(document.getElementById('ct-outcome').value === 'authored outcome',
    'a refused save erased the outcome: ' + document.getElementById('ct-outcome').value);
  assert(document.getElementById('ct-accept').value === 'authored a', 'a refused save erased acceptance');
  assert(document.getElementById('ct-constr').value === 'authored c', 'a refused save erased constraints');
  assert(document.getElementById('ct-parent').value === 'other', 'a refused save erased the chosen parent');

  // And the next save must actually be transmitted.
  __reset();
  document.getElementById('ct-save').onclick();
  assert(__sent.length === 1, 'the editor stayed wedged after a refused save — no further save was transmitted');
  assert(__sent[0].project.contract.outcome === 'authored outcome', 'the retried save lost the outcome');
  assert(__sent[0].project.parent === 'other', 'the retried save lost the parent');

  // ---- AN ACK CLEARS THE DRAFT (the other half of the same rule) ----
  fresh();
  renderProjects();
  author('acked outcome', 'x', 'y', 'other');
  document.getElementById('ct-save').onclick();
  assert(acceptContractSave('req-1') === true, 'the ack was not claimed');
  assert(S.contractDraft === null, 'the ack must clear the draft');

  // ---- A BROADCAST WHILE THE PARENT SELECTOR IS OPEN ----
  fresh();
  renderProjects();
  author('live outcome', 'live a', 'live c', 'other');
  document.getElementById('ct-parent').focus();
  // A projects payload lands mid-edit. The render must be skipped.
  renderProjects();
  assert(document.getElementById('ct-parent').value === 'other',
    'a broadcast while the parent selector was open discarded the chosen parent');
  assert(document.getElementById('ct-outcome').value === 'live outcome',
    'a broadcast while the parent selector was open discarded the outcome');

  // The draft must carry ALL FOUR fields: a later render with nothing
  // focused restores from it, not from the stored bytes.
  document.getElementById('ct-parent').blur();
  renderProjects();
  assert(document.getElementById('ct-outcome').value === 'live outcome', 'the draft lost the outcome');
  assert(document.getElementById('ct-accept').value === 'live a', 'the draft lost acceptance');
  assert(document.getElementById('ct-constr').value === 'live c', 'the draft lost constraints');
  assert(document.getElementById('ct-parent').value === 'other', 'the draft lost the parent');

  // ---- A BROADCAST WHILE A SAVE AWAITS ITS ACK ----
  fresh();
  renderProjects();
  author('pending outcome', 'pending a', 'pending c', 'other');
  document.getElementById('ct-save').onclick();
  renderProjects(); // the broadcast arrives before the ack
  assert(document.getElementById('ct-outcome').value === 'pending outcome',
    'a broadcast before the ack reverted the outcome to stored state');
  assert(document.getElementById('ct-parent').value === 'other',
    'a broadcast before the ack reverted the parent to stored state');
});
</script>`

// .
// .
func TestProjectsContractFailurePathsInBrowser(t *testing.T) {
	runPageInEngines(t, projectsContractFailurePage, overrideModuleStubs(
		"/ws.js", []byte(wsRecordingStub),
	))
}
