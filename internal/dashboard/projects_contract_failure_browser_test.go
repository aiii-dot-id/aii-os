//go:build !windows

package dashboard

import (
	"testing"
)

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
  fresh();renderProjects();author('blurred outcome','blurred a','blurred c','other');
  document.getElementById('ct-outcome').dispatchEvent(new Event('input',{bubbles:true}));
  document.getElementById('ct-save').focus();renderProjects();
  assert(document.getElementById('ct-outcome').value==='blurred outcome','blurred contract was lost');
  document.getElementById('ct-save').click();
  document.getElementById('ct-outcome').value='newer outcome';
  document.getElementById('ct-outcome').dispatchEvent(new Event('input',{bubbles:true}));
  document.getElementById('ct-save').focus();
  assert(acceptContractSave('req-1'),'save was not claimed');renderProjects();
  assert(document.getElementById('ct-outcome').value==='newer outcome','old ack discarded newer contract');
  assert(Array.from(document.querySelectorAll('.tab,.dock-item')).every(el=>el.tagName==='BUTTON'&&el.tabIndex===0),'project destinations are not keyboard controls');
  const focus=document.getElementById('focus-edit');focus.value='blurred focus';
  focus.dispatchEvent(new Event('input',{bubbles:true}));document.getElementById('focus-save').focus();renderProjects();
  assert(document.getElementById('focus-edit').value==='blurred focus','blurred focus was lost');

  fresh();
  renderProjects();
  author('authored outcome', 'authored a', 'authored c', 'other');
  document.getElementById('ct-save').onclick();
  assert(__sent.length === 1, 'first save was not sent');
  const reqID = 'req-1';

  assert(rejectContractSave(reqID) === true, 'the refusal was not claimed by the contract slot');

  renderProjects();
  assert(document.getElementById('ct-outcome').value === 'authored outcome',
    'a refused save erased the outcome: ' + document.getElementById('ct-outcome').value);
  assert(document.getElementById('ct-accept').value === 'authored a', 'a refused save erased acceptance');
  assert(document.getElementById('ct-constr').value === 'authored c', 'a refused save erased constraints');
  assert(document.getElementById('ct-parent').value === 'other', 'a refused save erased the chosen parent');

  __reset();
  document.getElementById('ct-save').onclick();
  assert(__sent.length === 1, 'the editor stayed wedged after a refused save — no further save was transmitted');
  assert(__sent[0].project.contract.outcome === 'authored outcome', 'the retried save lost the outcome');
  assert(__sent[0].project.parent === 'other', 'the retried save lost the parent');

  fresh();
  renderProjects();
  author('acked outcome', 'x', 'y', 'other');
  document.getElementById('ct-save').onclick();
  assert(acceptContractSave('req-1') === true, 'the ack was not claimed');
  assert(S.contractDraft === null, 'the ack must clear the draft');

  fresh();
  renderProjects();
  author('live outcome', 'live a', 'live c', 'other');
  document.getElementById('ct-parent').focus();
  renderProjects();
  assert(document.getElementById('ct-parent').value === 'other',
    'a broadcast while the parent selector was open discarded the chosen parent');
  assert(document.getElementById('ct-outcome').value === 'live outcome',
    'a broadcast while the parent selector was open discarded the outcome');

  document.getElementById('ct-parent').blur();
  renderProjects();
  assert(document.getElementById('ct-outcome').value === 'live outcome', 'the draft lost the outcome');
  assert(document.getElementById('ct-accept').value === 'live a', 'the draft lost acceptance');
  assert(document.getElementById('ct-constr').value === 'live c', 'the draft lost constraints');
  assert(document.getElementById('ct-parent').value === 'other', 'the draft lost the parent');

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

func TestProjectsContractFailurePathsInBrowser(t *testing.T) {
	runPageInEngines(t, projectsContractFailurePage, overrideModuleStubs(
		"/ws.js", []byte(wsRecordingStub),
	))
}
