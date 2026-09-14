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
const projectsContractPage = `<!doctype html>
<div id="proj-space"></div><div id="dock"></div><span id="pill-proj"></span>
<div id="crumb"></div><div id="toast"></div><div id="firstboot"></div><div id="composer-wrap"></div>
<script type="module">
import { run, assert } from '/__harness.js';
import { S } from '/state.js';
import { renderProjects, projectsConnectionLost } from '/views/projects.js';
import { __sent, __reset } from '/ws.js';

const UMBRELLA = { id: 'umbrella', name: 'Umbrella', state: 'open', dir: '/p/umbrella' };
const ALPHA = {
  id: 'alpha', name: 'Alpha', state: 'open', description: 'first', dir: '/p/alpha',
  active: true,
  contract: {
    outcome: 'ship the beta on three platforms',
    acceptance: ['windows installs clean', 'the dmg is stapled'],
    constraints: ['no unsigned binaries'],
  },
  parent: 'umbrella',
};

function fresh(project) {
  __reset();
  /* Drop any unacked save from the previous case. The pending slot is
     real state that outlives a re-render on purpose — a save waiting for
     its ack must not be re-sent — so a new case has to clear it the way
     a dropped socket does. */
  projectsConnectionLost();
  S.projects = [project, UMBRELLA];
  S.activeProject = project;
  S.viewedProject = project.id;
  S.projTab = 'overview';
  S.view = 'projects';
  S.contractDraft = null;
  S.workspace = null; S.workspaceFor = '';
}

run(() => {
  const space = document.getElementById('proj-space');

  // RENDERED FROM PERSISTED STATE — the operator sees what is stored,
  // in the order it was authored.
  fresh(ALPHA);
  renderProjects();
  const outcome = document.getElementById('ct-outcome');
  assert(outcome, 'the contract editor is missing entirely');
  assert(outcome.value === 'ship the beta on three platforms',
    'outcome not rendered from persisted state: ' + outcome.value);
  const accept = document.getElementById('ct-accept');
  assert(accept.value === 'windows installs clean\nthe dmg is stapled',
    'acceptance must render one per line in authored order: ' + JSON.stringify(accept.value));
  assert(document.getElementById('ct-constr').value === 'no unsigned binaries',
    'constraints not rendered');
  const parent = document.getElementById('ct-parent');
  assert(parent && parent.value === 'umbrella', 'parent selector does not show the stored parent');
  // A project is never its own parent, and "none" is always offered.
  const opts = Array.from(parent.options).map((o) => o.value);
  assert(opts.indexOf('alpha') === -1, 'a project must not be offered as its own parent');
  assert(opts.indexOf('') === 0, 'there must be a "none" option — it is how a hierarchy is left');

  // AUTHORED AND SENT — the whole contract, in the typed fields.
  outcome.value = 'ship the beta everywhere';
  accept.value = 'windows installs clean\n\nthe deb registers\n';
  document.getElementById('ct-constr').value = 'no unsigned binaries';
  document.getElementById('ct-save').onclick();
  assert(__sent.length === 1, 'save sent ' + __sent.length + ' messages');
  const req = __sent[0].project;
  assert(req.action === 'update' && req.id === 'alpha', 'wrong target: ' + JSON.stringify(req));
  assert(req.contract.outcome === 'ship the beta everywhere', 'outcome not sent');
  // Blank lines are dropped; order is preserved.
  assert(JSON.stringify(req.contract.acceptance) === JSON.stringify(['windows installs clean','the deb registers']),
    'acceptance lines wrong: ' + JSON.stringify(req.contract.acceptance));

  // A SECOND SAVE IS NOT SENT WHILE THE FIRST IS UNACKED — the same
  // once-only rule the focus note follows.
  document.getElementById('ct-save').onclick();
  assert(__sent.length === 1, 'a second save was sent before the first was acknowledged');

  // CLEARING THE HIERARCHY — "none" must travel as an explicit empty
  // string, because the wire distinguishes clear from absent.
  fresh(ALPHA);
  renderProjects();
  document.getElementById('ct-parent').value = '';
  document.getElementById('ct-save').onclick();
  assert(__sent[0].project.parent === '',
    'choosing "none" must send an explicit clear, got ' + JSON.stringify(__sent[0].project.parent));

  // AN UNAUTHORED CONTRACT IS EMPTY, NOT ABSENT — the editor still
  // exists so the operator can author one.
  fresh({ id: 'bare', name: 'Bare', state: 'open', dir: '/p/bare', active: true });
  renderProjects();
  assert(document.getElementById('ct-outcome').value === '',
    'an unauthored contract must render empty fields, not stale ones');
  assert(document.getElementById('ct-parent').value === '', 'an unparented project must show none');
});
</script>`

// .
// .
// .
func TestProjectsContractAuthoringInBrowser(t *testing.T) {
	runPageInEngines(t, projectsContractPage, overrideModuleStubs(
		"/ws.js", []byte(wsRecordingStub),
	))
}
