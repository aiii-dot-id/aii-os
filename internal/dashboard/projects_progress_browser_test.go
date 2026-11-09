//go:build !windows

// Runs only through the unix browser-engine rig (browserengine_test.go,
// same tag) — this file calls its helpers, so it shares its build row.

package dashboard

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/project"
)

// projectsProgressPage drives the DERIVED acceptance rendering in a real
// engine.
//
// The defect this pins: the server has derived five acceptance states per
// project since progress.go landed — open, supported, verified, waived,
// unsupported — with next_index, counts, closure_allowed and closure_reason,
// assigned on every push at dashboard_state.go and carried on the wire as
// "progress". The page rendered NONE of it. "acceptance" appeared in
// projects.js only as the authoring textarea, so an operator could write a
// claim and never learn whether evidence had settled it, contradicted it, or
// left it stale.
//
// A renderer is not proved by compiling. These cases pin the things that
// can be wrong while looking right: the states must appear as their own
// names (never as a percentage standing in for them), each claim must say
// what it rests on in words rather than the store's codes, one Next line must
// name the claim at next_index and count what stands in the way of a close as
// the server counts it, a stale claim must say so in words rather than in the
// store's word, and an absent contract must not invent states. The words are the spec's (266–267) and the Project board's.
//
// The inputs are not written by hand. Each case's progress is what
// project.DeriveContractProgress returns for its criteria and observations,
// copied into the wire struct as the app's adapter copies it, and written
// into the page's one slot as JSON (host 1021). The hand-written inputs this
// replaced carried progress the server cannot send: next_index 2 where
// progress.go names the first claim neither verified nor waived (the
// supported one), stale on a supported item where it flags stale only an
// open one, closure refused with no criteria where it allows it, counts
// without the zero states it always sends, and refusal reasons not in its
// format. The page drew them faithfully, so no assertion could catch it; it
// was found by reading progress.go against the fixture.
const projectsProgressPage = `<!doctype html>
<div id="proj-space"></div><div id="dock"></div><span id="pill-proj"></span>
<div id="crumb"></div><div id="toast"></div><div id="firstboot"></div><div id="composer-wrap"></div>
<script type="module">
import { run, assert } from '/__harness.js';
import { S } from '/state.js';
import { renderProjects } from '/views/projects.js';
import { __reset } from '/ws.js';

// What progress.go derives for each case, written in by the Go test below.
const D = __DERIVED__;

function fresh(c) {
  __reset();
  const p = {
    id: 'alpha', name: 'Alpha', state: 'open', dir: '/p/alpha', active: true,
    contract: { outcome: 'an outcome', acceptance: c.acceptance, constraints: [] },
  };
  if (c.progress !== undefined) p.progress = c.progress;
  S.projects = [p];
  S.activeProject = p;
  S.viewedProject = 'alpha';
  S.projTab = 'overview';
  S.view = 'projects';
  S.contractDraft = null;
  S.workspace = null; S.workspaceFor = '';
  // The identity's own name, which is what "{name} says so" names.
  S.identityExists = true;
  S.stats = Object.assign({}, S.stats, { name: 'Mira' });
}

const card = () => Array.from(document.querySelectorAll('.card'))
  .find((c) => c.querySelector('h3') && c.querySelector('h3').textContent === 'How you will know it is done');
const whys = () => Array.from(card().querySelectorAll('li')).map((li) => {
  const w = li.querySelector('.prog-why');
  return w ? w.textContent : '';
});
const nextLines = () => Array.from(card().querySelectorAll('.prog-next')).map((n) => n.textContent);
function oneNext(want, what) {
  const n = nextLines();
  assert(n.length === 1, what + ': expected one Next line, got ' + JSON.stringify(n));
  assert(n[0] === want, what + ': the Next line reads ' + JSON.stringify(n[0]) + ', want ' + JSON.stringify(want));
}

// The spec's TestProofApartFromClaim, row by row, for every case the page
// draws: each claim's chip comes from its own derived state alone, a claim the
// identity only says is done never reads "Verified" in its row, and no state is
// folded, since each of the five states has its own word here and a fold
// changes a row. Run on the five-state case alone it missed two shapes: a
// supported claim with an evidence reference read as "Verified", and a claim
// verified where the work was done read as "says so". The five-state case's
// supported claim has no reference and its verified claim has a receipt, so
// only the other cases carry those shapes.
function rowsFromDerived(c, what, says) {
  const CHIP = { verified: 'Verified', supported: says, open: 'Open', unsupported: 'Not supported', waived: 'Waived' };
  const derived = c.progress.items;
  const items = Array.from(card().querySelectorAll('li'));
  const wantChips = derived.map((it) => CHIP[it.state] || ('no chip word for ' + it.state));
  const rowChips = items.map((li) => { const ch = li.querySelector('.chip'); return ch ? ch.textContent.trim() : ''; });
  assert(JSON.stringify(rowChips) === JSON.stringify(wantChips),
    what + ': each claim\u2019s chip must come from its own derived state: want ' +
    JSON.stringify(wantChips) + ', got ' + JSON.stringify(rowChips));
  derived.forEach((it, i) => {
    if (it.state !== 'supported') return;
    assert(items[i] && !items[i].textContent.includes('Verified'),
      what + ': a claim the identity only says is done must never read "Verified" in its row: ' +
      JSON.stringify(items[i] ? items[i].textContent : ''));
  });
}

run(() => {
  // ---- ALL FIVE STATES RENDER AS THEIR OWN NAMES ----
  fresh(D.five);
  renderProjects();
  assert(card(), 'no acceptance card rendered at all');
  const text = card().textContent;
  // a review STEP 12 item 1 names the operator-facing words. Asserting the raw
  // tokens would pass on the defect: 'supported' is a substring of 'says so,
  // not verified' only by accident, and printing the token is exactly the bug
  // (it reads as confirmation when the server means not independently verified).
  for (const w of ['Verified', 'Open', 'Waived', 'Not supported']) {
    assert(text.includes(w), 'the derived state must read "' + w + '" to the operator');
  }
  // The defect is a CHIP reading as the bare token, so scope this to chips:
  // the counts summary legitimately carries the server's own token.
  const chipWords = Array.from(card().querySelectorAll('.chip')).map((c) => c.textContent.trim());
  assert(!chipWords.includes('supported'),
    'a chip still reads as the raw token "supported" instead of the phrase');
  // The {name} is the identity's own name, as the board has it ("1 Mira says
  // so", Project.dc.html:39). The fixture sets the identity's name, because
  // that is where the wire puts it - not on the acceptance item.
  assert(chipWords.includes('Mira says so'),
    'an unverified claim must read "{name} says so" with the identity\'s own name, got: ' +
    JSON.stringify(chipWords));
  // Every claim's own text must be visible — the states are attached to
  // claims, not floated above them.
  for (const claim of ['the door opens', 'the light is green', 'the floor is level', 'the bell rings', 'the sign is legible']) {
    assert(text.includes(claim), 'the claim "' + claim + '" is not shown');
  }
  // A percentage would be the authored-summary mistake this design forbids.
  assert(!/%/.test(text), 'the card shows a percentage where states belong');
  const items = Array.from(card().querySelectorAll('li'));
  assert(items.length === 5, 'expected five claim rows, got ' + items.length);

  // ---- EACH ROW'S CHIP FROM ITS OWN DERIVED STATE ----
  // The checks above find every word somewhere on the card, which two rows
  // carrying each other's chips would still satisfy; so read row by row
  // against what progress.go derived for that row, here and after every
  // case below.
  rowsFromDerived(D.five, 'five states', 'Mira says so');

  // ---- THE COUNTS LINE IN THE APPROVED WORDS ----
  // The approved shape is "1 verified · 1 {name} says so · 2 open": the
  // states lower-case beside the chips, the identity named.
  const status = card().querySelector('[role="status"]').textContent;
  assert(status === '1 verified \u00b7 1 Mira says so \u00b7 1 open \u00b7 1 not supported \u00b7 1 waived',
    'the counts line is not in the approved words: ' + JSON.stringify(status));

  // ---- UNDER EACH CLAIM, WHAT IT RESTS ON, IN WORDS ----
  // The class is the store's; the page says what it means and draws no code.
  const w5 = whys();
  const want5 = [
    'Evidence: receipt-door.txt, with a receipt from AII OS',
    'A helper reported it; no independent check is recorded yet',
    'Waits on evidence',
    'Part of it was done, or the results were mixed',
    'Set aside',
  ];
  for (let i = 0; i < want5.length; i++) {
    assert(w5[i] === want5[i], 'claim ' + (i + 1) + ' rests on ' + JSON.stringify(w5[i]) + ', want ' + JSON.stringify(want5[i]));
  }
  for (const code of ['host_receipted', 'worker_report_only', 'partial_or_mixed']) {
    assert(!text.includes(code), 'the card draws the evidence code "' + code + '" instead of its meaning');
  }

  // ---- ONE NEXT LINE: THE CLAIM AT next_index, AND WHY IT CANNOT CLOSE ----
  // progress.go names the first claim neither verified nor waived (the
  // supported one) and refuses closure while any claim is open, supported or
  // unsupported: three here, which the Go side checks against its own items.
  assert(!text.includes('\u2190 next'), 'a row still carries the old next marker');
  oneNext('Next: the light is green. It can\u2019t be closed yet: 3 things are not verified or waived.',
    'a refused closure');
  // The server's closure_reason is written for the identity's prompt; the
  // page says it in plain words instead (spec 267). The Go side has checked
  // the reason is not empty, so this cannot pass on an includes('').
  assert(!text.includes(D.five.progress.closure_reason),
    'the card prints the prompt’s refusal sentence instead of plain words');
  assert(!text.includes('Never stored'), 'the card still carries the footnote');

  // ---- AN ALLOWED CLOSURE SAYS SO ----
  fresh(D.allowed);
  renderProjects();
  rowsFromDerived(D.allowed, 'verified where the work was done', 'Mira says so');
  assert(whys()[0] === 'Evidence: check-door.txt, checked where the work was done',
    'a locally verified claim does not say it was checked where the work was done: ' + JSON.stringify(whys()[0]));
  oneNext('Next: nothing. It can be closed.', 'an allowed closure');

  // ---- STALE IS SAID UNDER THE CLAIM, IN WORDS ----
  // progress.go flags stale only on an item no current observation decides,
  // so the stale claim is open; one claim stands in the way, said singly. The
  // line under the claim says it; the store's word "stale" is not drawn.
  fresh(D.stale);
  renderProjects();
  rowsFromDerived(D.stale, 'stale', 'Mira says so');
  assert(!card().textContent.includes('stale'),
    'the card draws the store\u2019s word "stale" beside the claim');
  assert(whys()[0] === 'Evidence was recorded before these words changed; it needs checking again',
    'a stale claim does not say what it waits on: ' + JSON.stringify(whys()[0]));
  oneNext('Next: the door opens smoothly. It can\u2019t be closed yet: 1 thing is not verified or waived.',
    'one claim in the way');

  // ---- EVERY OTHER CLASS IN WORDS, AND A CLASS THE PAGE DOES NOT KNOW ----
  fresh(D.local);
  renderProjects();
  rowsFromDerived(D.local, 'completed locally', 'Mira says so');
  assert(whys()[0] === 'Evidence: notes.md. Seen done; no independent check is recorded yet',
    'a claim completed locally does not say so: ' + JSON.stringify(whys()[0]));
  fresh(D.unsup4);
  renderProjects();
  rowsFromDerived(D.unsup4, 'four unsupported', 'Mira says so');
  const w4 = whys();
  const want4 = [
    'Whether it took effect can\u2019t be seen from here',
    'It was refused before anything happened',
    'It was never run',
    'Recorded as \u201cmade_up_class\u201d, which this page does not know',
  ];
  for (let i = 0; i < want4.length; i++) {
    assert(w4[i] === want4[i], 'unsupported claim ' + (i + 1) + ' rests on ' + JSON.stringify(w4[i]) + ', want ' + JSON.stringify(want4[i]));
  }

  // ---- NO OBSERVER IS NAMED, WHATEVER NAME THE PAGE KNOWS ----
  // The wire carries a claim's class and reference, never who checked it: an
  // observation's author is who recorded it, not who ran a check. So no line
  // under a claim names anyone, with a name known or not.
  for (const nm of ['Mira', '']) {
    fresh(D.local);
    S.stats = Object.assign({}, S.stats, { name: nm });
    renderProjects();
    rowsFromDerived(D.local, 'completed locally, name ' + JSON.stringify(nm), nm ? 'Mira says so' : 'says so, not verified');
    assert(whys()[0] === 'Evidence: notes.md. Seen done; no independent check is recorded yet',
      'with the name ' + JSON.stringify(nm) + ' a claim completed locally reads ' + JSON.stringify(whys()[0]));
    fresh(D.allowed);
    S.stats = Object.assign({}, S.stats, { name: nm });
    renderProjects();
    rowsFromDerived(D.allowed, 'verified where the work was done, name ' + JSON.stringify(nm), nm ? 'Mira says so' : 'says so, not verified');
    assert(whys()[0] === 'Evidence: check-door.txt, checked where the work was done',
      'with the name ' + JSON.stringify(nm) + ' a locally verified claim reads ' + JSON.stringify(whys()[0]));
  }

  // ---- THE HINT FOR unsupported DOES NOT CLAIM CONTRADICTION ----
  // project/progress.go:59-71 makes unsupported from partial-or-mixed,
  // external-effect-unknown, rejected-before-effect, not-run and unknown. The
  // page may say the evidence does not establish the item; it may not say the
  // evidence contradicts it (host 2300 item 2).
  fresh(D.unsupported);
  renderProjects();
  rowsFromDerived(D.unsupported, 'unsupported', 'Mira says so');
  const hintChips = Array.from(card().querySelectorAll('.chip'));
  const unsup = hintChips.find((c) => c.textContent.trim() === 'Not supported');
  assert(unsup, 'an unsupported claim does not render its own chip');
  const unsupHint = unsup.getAttribute('title') || '';
  assert(!/contradict/i.test(unsupHint),
    'the unsupported hint claims the evidence contradicts the claim, which the server never said: ' + unsupHint);
  assert(/does not establish/i.test(unsupHint),
    'the unsupported hint does not say the evidence fails to establish the item: ' + unsupHint);

  // ---- THE EVIDENCE REFERENCE IS DRAWN ----
  // it.ref is on the wire (dashboard/progress.go:23) and the board draws a
  // "why" line for it (Project.dc.html:68).
  fresh(D.ref);
  renderProjects();
  rowsFromDerived(D.ref, 'verified with its reference', 'Mira says so');
  const refText = card().textContent;
  assert(refText.includes('grant-draft-v3.md'),
    'the evidence reference on the wire is not drawn, got: ' + refText);
  assert(refText.includes('Evidence:'),
    'the evidence reference is not introduced as evidence');
  assert(whys()[0] === 'Evidence: grant-draft-v3.md, checked where the work was done',
    'the evidence reference is drawn as ' + JSON.stringify(whys()[0]));
  assert(!refText.includes('locally_verified'), 'the reference is drawn with its evidence code');

  // ---- NO CRITERIA: SAY WHAT WOULD MAKE ONE, DO NOT INVENT STATES ----
  // progress.go allows closure when there are no criteria; the card still
  // says how to write one rather than announcing the project may close.
  fresh(D.none);
  renderProjects();
  const empty = card();
  assert(empty, 'a project with no criteria renders no acceptance card');
  assert(!empty.querySelector('li'), 'a project with no criteria invented claim rows');
  assert(empty.textContent.includes('No acceptance criteria yet'),
    'a project with no criteria does not say how to make one');
  assert(!empty.textContent.includes('stale'), 'an empty contract was flagged stale');
  // has_criteria false also derives next_index -1 and closure_allowed true:
  // informal work, not proof that criteria were verified (host 1422 item 1).
  // So no Next line, and never "can be closed", by the element or by its words.
  assert(nextLines().length === 0 && !empty.textContent.includes('Next:') &&
    !empty.textContent.includes('can be closed'),
    'a project with no criteria draws a Next line or says it can be closed: ' + JSON.stringify(empty.textContent));

  // ---- AN ABSENT PROGRESS FIELD IS NOT A CRASH AND NOT A CLAIM ----
  // Missing progress means AII OS has not read this project's acceptance, which
  // is a different fact from the project having none. Saying "No acceptance
  // criteria yet" here would be the page inventing the server's answer
  // (host 2300 item 2). The server never sends this; it is the page before
  // the first push.
  fresh({ acceptance: ['a'] });
  renderProjects();
  assert(card(), 'a project whose progress field is absent renders no acceptance card');
  assert(!card().querySelector('li'), 'an absent progress field invented claim rows');
  const absentText = card().textContent;
  assert(!absentText.includes('No acceptance criteria yet'),
    'an unread progress field claims the project has no criteria, which the wire never said');
  assert(absentText.includes('has not read'),
    'an unread progress field does not say it has not been read, got: ' + absentText);
  assert(nextLines().length === 0, 'an unread progress field names a next claim');

  // ---- STATES ARE TEXT, NOT MARKUP ----
  // The acceptance text is operator-authored. A claim containing markup must
  // render as characters, or the card becomes an injection point. The claim
  // reaches the page twice, in its row and on the Next line; each path is
  // checked apart.
  fresh(D.markup);
  renderProjects();
  rowsFromDerived(D.markup, 'markup', 'Mira says so');
  const mk = card();
  const imgs = Array.from(mk.querySelectorAll('img'));
  assert(!imgs.some((i) => i.closest('ul')), 'a claim’s markup was rendered as markup');
  assert(!imgs.some((i) => !i.closest('ul')), 'the Next line’s markup was rendered as markup');
  assert(mk.querySelector('ul').textContent.includes('<img src=x onerror=alert(1)>'),
    'a claim containing markup is not shown as its own characters');
  oneNext('Next: <img src=x onerror=alert(1)>. It can\u2019t be closed yet: 1 thing is not verified or waived.',
    'a claim containing markup on the Next line');
});
</script>
`

// progressCase is one acceptance list and the progress the server derives
// from it, in the wire shape the page receives.
type progressCase struct {
	Acceptance []string         `json:"acceptance"`
	Progress   ContractProgress `json:"progress"`
}

// deriveProgressCase runs progress.go's own derivation, then copies the
// result into the wire struct field for field. The copy mirrors
// projectDashboardProgress (internal/app/projects_adapter.go), which this
// package cannot import because app imports dashboard.
func deriveProgressCase(acceptance []string, obs ...project.AcceptanceObservation) progressCase {
	prog := project.DeriveContractProgress(project.Contract{Outcome: "an outcome", Acceptance: acceptance}, obs)
	items := make([]AcceptanceItem, 0, len(prog.Items))
	for _, it := range prog.Items {
		items = append(items, AcceptanceItem{
			Index: it.Index, Text: it.Text, State: it.State,
			Class: it.Class, Ref: it.Ref, Stale: it.Stale,
		})
	}
	return progressCase{Acceptance: acceptance, Progress: ContractProgress{
		HasCriteria: prog.HasCriteria, Items: items, NextIndex: prog.NextIndex,
		Counts: prog.Counts, ClosureAllowed: prog.ClosureAllowed, ClosureReason: prog.ClosureReason,
	}}
}

func observed(item int, text, class, ref string) project.AcceptanceObservation {
	return project.AcceptanceObservation{Item: item, ItemText: text, Class: class, Ref: ref, By: "identity", At: "2026-11-01T10:00:00Z"}
}

// projectsProgressCases derives every case the page draws, and stops with a
// named message if progress.go no longer derives the shape a case is named
// for — so a change in the derivation fails here, not as a puzzling drawing.
func projectsProgressCases(t *testing.T) map[string]progressCase {
	t.Helper()
	five := []string{"the door opens", "the light is green", "the floor is level", "the bell rings", "the sign is legible"}
	four := []string{"the mail went out", "the key was revoked", "the backup ran", "the page is signed"}
	const markup = "<img src=x onerror=alert(1)>"
	cases := map[string]progressCase{
		"five": deriveProgressCase(five,
			observed(0, five[0], "host_receipted", "receipt-door.txt"),
			observed(1, five[1], "worker_report_only", ""),
			observed(3, five[3], "partial_or_mixed", ""),
			observed(4, five[4], project.ObservationWaived, ""),
		),
		"allowed":     deriveProgressCase([]string{"the door opens"}, observed(0, "the door opens", "locally_verified", "check-door.txt")),
		"stale":       deriveProgressCase([]string{"the door opens smoothly"}, observed(0, "the door opens", "completed_locally", "")),
		"unsupported": deriveProgressCase([]string{"the bell rings"}, observed(0, "the bell rings", "partial_or_mixed", "")),
		"ref":         deriveProgressCase([]string{"the door opens"}, observed(0, "the door opens", "locally_verified", "grant-draft-v3.md")),
		"local":       deriveProgressCase([]string{"the notes are written"}, observed(0, "the notes are written", "completed_locally", "notes.md")),
		"unsup4": deriveProgressCase(four,
			observed(0, four[0], "external_effect_unknown", ""),
			observed(1, four[1], "rejected_before_effect", ""),
			observed(2, four[2], "not_run", ""),
			observed(3, four[3], "made_up_class", ""),
		),
		"none":   deriveProgressCase([]string{}),
		"markup": deriveProgressCase([]string{markup}),
	}
	want := func(name string, ok bool, what string) {
		if !ok {
			t.Fatalf("progress.go no longer derives the %q case the page draws (%s); got %+v", name, what, cases[name].Progress)
		}
	}
	single := func(name, state string) bool {
		p := cases[name].Progress
		return len(p.Items) == 1 && p.Items[0].State == state
	}
	f := cases["five"].Progress
	var states []string
	for _, it := range f.Items {
		states = append(states, it.State)
	}
	want("five", strings.Join(states, ",") == "verified,supported,open,unsupported,waived", "one claim per state, in authored order")
	want("five", f.NextIndex == 1 && !f.ClosureAllowed && f.ClosureReason != "", "the supported claim next, closure refused with a reason")
	// The page's "3 things are not verified or waived" is the number of claims
	// the server would have to see verified or waived before it allows a close.
	inTheWay := 0
	for _, it := range f.Items {
		if it.State != project.AcceptanceVerified && it.State != project.AcceptanceWaived {
			inTheWay++
		}
	}
	want("five", inTheWay == 3 && f.Counts["open"]+f.Counts["supported"]+f.Counts["unsupported"] == 3, "three claims standing in the way of a close, counted alike")
	want("local", single("local", "supported") && cases["local"].Progress.Items[0].Class == "completed_locally" && cases["local"].Progress.Items[0].Ref == "notes.md", "one claim completed locally, with its ref")
	u := cases["unsup4"].Progress
	var uc []string
	for _, it := range u.Items {
		uc = append(uc, it.State+"/"+it.Class)
	}
	want("unsup4", strings.Join(uc, ",") == "unsupported/external_effect_unknown,unsupported/rejected_before_effect,unsupported/not_run,unsupported/made_up_class", "four unsupported claims, one per class, the last unknown")
	a := cases["allowed"].Progress
	want("allowed", single("allowed", "verified") && a.NextIndex == -1 && a.ClosureAllowed, "every claim verified, closure allowed")
	want("stale", single("stale", "open") && cases["stale"].Progress.Items[0].Stale, "one open claim flagged stale")
	want("unsupported", single("unsupported", "unsupported"), "one unsupported claim")
	want("ref", single("ref", "verified") && cases["ref"].Progress.Items[0].Ref == "grant-draft-v3.md", "one verified claim carrying its ref")
	n := cases["none"].Progress
	want("none", !n.HasCriteria && len(n.Items) == 0 && n.ClosureAllowed, "no criteria, closure allowed")
	m := cases["markup"].Progress
	want("markup", single("markup", "open") && m.NextIndex == 0 && !m.ClosureAllowed, "one open claim, named next, closure refused")
	return cases
}

// TestProjectsProgressRendersDerivedStatesInBrowser pins the five derived
// acceptance states, next_index, stale, and closure against a real engine,
// fed what progress.go derives.
func TestProjectsProgressRendersDerivedStatesInBrowser(t *testing.T) {
	derived, err := json.Marshal(projectsProgressCases(t))
	if err != nil {
		t.Fatal(err)
	}
	const slot = "__DERIVED__"
	if n := strings.Count(projectsProgressPage, slot); n != 1 {
		t.Fatalf("the page must carry exactly one %s slot, has %d", slot, n)
	}
	// json.Marshal writes <, > and & as \u003c, \u003e and \u0026, so the
	// markup case cannot close the script element it is written into.
	page := strings.Replace(projectsProgressPage, slot, string(derived), 1)
	runPageInEngines(t, page, overrideModuleStubs(
		"/ws.js", []byte(wsRecordingStub),
	))
}
