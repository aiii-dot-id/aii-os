//go:build !windows

package dashboard

import "testing"

// Step 12, the Agreed-by caption (host ruling ...-0332, F-DASH-044).
//
// The wire carries contract.agreed_by ("operator" or "identity") and
// agreed_at, both omitempty. wire.go:2329 states the rule the page must
// follow: "Both absent for a contract written before the hand was kept: the
// page then says nothing about who agreed it." The board's shape
// (Project.dc.html:32) is "Agreed by you and Mira on 20 October".
//
// The cases that matter are the refusals. A page that invents a hand for an
// old contract, or renders half a fact when the date is missing, is worse
// than one that stays silent — so the caption is asserted absent in both.
const agreedCaptionPage = `<!doctype html>
<div id="proj-space"></div><div id="dock"></div><span id="pill-proj"></span>
<div id="crumb"></div><div id="toast"></div><div id="firstboot"></div><div id="composer-wrap"></div>
<script type="module">
import { run, assert } from '/__harness.js';
import { S } from '/state.js';
import { renderProjects } from '/views/projects.js';
import { __reset } from '/ws.js';

function fresh(contract, dir = '/p/alpha') {
  __reset();
  const p = {
    id: 'alpha', name: 'Grant letter', state: 'open', dir: dir, active: true,
    contract: contract,
    progress: { has_criteria: true, items: [], next_index: -1, counts: {},
                closure_allowed: true, closure_reason: '' },
  };
  S.projects = [p];
  S.activeProject = p;
  S.viewedProject = 'alpha';
  S.projTab = 'overview';
  S.view = 'projects';
  S.contractDraft = null;
  S.workspace = null; S.workspaceFor = '';
  S.identityExists = true;
  S.stats = { name: 'Quill' };
  renderProjects();
  const el = document.querySelector('.agreed');
  return el ? el.textContent : '';
}

run(() => {
  // The date is the page's own zone, read by an independent formatter so a
  // UTC date cannot pass where the zone differs; en-GB writes the day first,
  // as the board does ("20 October").
  const AT = '2026-10-20T12:00:00Z';
  const day = new Date(AT).toLocaleDateString('en-GB', { day: 'numeric', month: 'long' });

  // ---- THE OPERATOR WROTE IT LAST: "you" ----
  let got = fresh({ outcome: 'o', agreed_by: 'operator', agreed_at: AT });
  assert(got.startsWith('Agreed by you on ' + day),
    'the operator hand did not render as "Agreed by you on ' + day + '" in the page zone, got: ' + got);

  // ---- THE IDENTITY WROTE IT LAST: its own public name ----
  got = fresh({ outcome: 'o', agreed_by: 'identity', agreed_at: AT });
  assert(got.startsWith('Agreed by Quill on ' + day),
    'the identity hand did not render the identity name, got: ' + got);

  // ---- THE BOARD'S FOLDER PART: said when the wire carries dir ----
  assert(got.endsWith(' \u00b7 the project\u2019s own folder: /p/alpha'),
    'the caption does not carry the folder as the board draws it, got: ' + got);
  got = fresh({ outcome: 'o', agreed_by: 'identity', agreed_at: AT }, '');
  assert(got === 'Agreed by Quill on ' + day,
    'a project with no folder on the wire still printed a folder part, got: ' + got);

  // ---- NO HAND KEPT: the page says NOTHING ----
  got = fresh({ outcome: 'o' });
  assert(got === '',
    'a contract written before the hand was kept invented a caption: ' + got);

  // ---- HALF A FACT IS NOT A FACT ----
  got = fresh({ outcome: 'o', agreed_by: 'operator' });
  assert(got === '',
    'a hand with no date rendered a partial caption: ' + got);

  // ---- A DATE WITH NO HAND IS ALSO HALF A FACT ----
  // This case is what pins the agreed_by guard: agreed_at alone parses, so
  // the date check cannot refuse it. With no hand the page must still say
  // nothing about who agreed it (wire.go's rule), not name a guess.
  got = fresh({ outcome: 'o', agreed_at: '2026-10-20T09:00:00Z' });
  assert(got === '',
    'a date with no hand rendered a caption naming a guess: ' + got);

  // ---- AN UNPARSEABLE DATE IS ALSO NO FACT ----
  got = fresh({ outcome: 'o', agreed_by: 'operator', agreed_at: 'not-a-date' });
  assert(got === '',
    'an unparseable agreed_at rendered a caption: ' + got);

  // ---- AN ABSENT CONTRACT DOES NOT CRASH OR INVENT ----
  got = fresh(null);
  assert(got === '', 'a null contract invented a caption: ' + got);
});
</script>`

// TestAgreedCaptionNamesTheHandOrSaysNothing pins the Agreed-by caption in a
// real engine: both hands, and the three cases where it must stay silent.
func TestAgreedCaptionNamesTheHandOrSaysNothing(t *testing.T) {
	runPageInEngines(t, agreedCaptionPage, overrideModuleStubs(
		"/ws.js", []byte(wsRecordingStub),
	))
}
