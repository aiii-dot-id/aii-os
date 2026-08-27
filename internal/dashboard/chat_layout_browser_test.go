//go:build !windows

// Runs only through the unix browser-engine rig (browserengine_test.go,
// same tag) — this file calls its helpers, so it shares its build row.

package dashboard

import (
	"strings"
	"testing"
)

// The chat surface's two structural invariants, asserted as geometry in a
// real engine against the SHIPPED stylesheet.
//
// Both were live defects reported as one sentence: "no scrolling, text
// entry box was unreachable" (657c8d7, e1b0e8c). Both were fixed with
// live proof on an iPhone and measured geometry in-engine, and neither
// had a guard that would fail if it regressed. A CSS rule with a comment
// explaining why it matters is text nothing executes.
//
// Neither assertion needs a narrow viewport, which is what makes them
// testable here: the causal property is the flex DIRECTION turning, not
// the pixel width that turns it.
//
// NOT covered, and it needs harness work rather than another test: the
// third invariant is that form controls reach 16px on phones, or iOS
// zooms on focus and never zooms back. That one lives inside the
// max-width:767px block, so it needs the engine launched at a phone
// width (--window-size), which is shared engine config.

const chatLayoutPage = `<!doctype html>
<style>__LAYOUT_CSS__</style>
<div class="app" id="app">
  <main class="stage">
    <div class="views" id="views">
      <section class="view on" id="view-chat">
        <div class="chat-body">
          <div class="thread" id="thread"><div class="thread-inner" id="thread-inner"></div></div>
          <div class="fb on" id="firstboot"><div class="fb-card"><h2>No identity lives here yet.</h2>
            <label class="f">YOUR NAME</label><input type="text" id="fb-operator">
            <div style="margin-top:18px"><button class="btn" id="fb-birth" style="width:100%">Birth</button></div>
          </div></div>
        </div>
        <div class="composer-wrap" id="composer-wrap">
          <div class="composer"><textarea id="msg-input" rows="1"></textarea></div>
        </div>
      </section>
    </div>
  </main>
</div>
<script type="module">
import { assert, run } from './__harness.js';

run(() => {
  const app = document.getElementById('app');
  const stage = document.querySelector('.stage');
  const thread = document.getElementById('thread');
  const inner = document.getElementById('thread-inner');
  const fb = document.getElementById('firstboot');
  const composer = document.getElementById('composer-wrap');

  const r = e => e.getBoundingClientRect();

  // 0. Guard: real geometry, or every inequality below passes vacuously
  //    on a stack of zero-sized rectangles.
  assert(r(app).height > 50 && r(composer).height > 0 && r(fb).height > 0,
    'zero-size geometry — nothing rendered (app ' + r(app).height +
    ', composer ' + r(composer).height + ', fb ' + r(fb).height + ')');

  // 1. THE BIRTH FORM AND THE COMPOSER MUST NOT SHARE A BOX.
  //    #firstboot used to be a sibling of the views, so inset:0 spanned
  //    the whole chat view including the composer, which then painted
  //    over the form at z-index 6 vs 5 and hid the Birth button.
  assert(r(fb).bottom <= r(composer).top + 0.5,
    'the firstboot overlay runs under the composer: overlay ends at ' +
    r(fb).bottom + ' but the composer starts at ' + r(composer).top);

  // 2. THE OVERLAY MUST BE ABLE TO REACH ITS OWN BOTTOM.
  //    Not merely "scrollable": the last control has to be reachable.
  fb.scrollTop = fb.scrollHeight;
  const birth = document.getElementById('fb-birth');
  assert(r(birth).bottom <= r(fb).bottom + 0.5 && r(birth).top >= r(fb).top - 0.5,
    'the Birth button cannot be scrolled into view: button ' + r(birth).top +
    '..' + r(birth).bottom + ' vs overlay ' + r(fb).top + '..' + r(fb).bottom);

  // 3. THE STAGE MUST NOT OUTGROW ITS BOX WHEN THE FLEX DIRECTION TURNS.
  //    .app is a row on desktop and a COLUMN on phones, so the axis that
  //    needs guarding rotates; .stage carried min-width:0 only, and on a
  //    phone it grew to its content — 14946px inside an 812px viewport,
  //    measured live. Forcing the direction reproduces the cause without
  //    needing the width that normally causes it.
  app.style.flexDirection = 'column';
  inner.innerHTML = '';
  for (let i = 0; i < 60; i++) {
    const d = document.createElement('div');
    d.className = 'msg';
    d.innerHTML = '<div class="body">' + 'a long conversation turn. '.repeat(40) + '</div>';
    inner.appendChild(d);
  }
  // Layout is read back after the mutation, not assumed.
  const appH = r(app).height, stageH = r(stage).height;
  assert(stageH <= appH + 0.5,
    'the stage outgrew its container with the flex direction turned: stage ' +
    stageH + ' inside app ' + appH + ' — the thread cannot become a scroll ' +
    'container and the composer leaves the screen');
  assert(thread.scrollHeight > thread.clientHeight,
    'the thread did not become a scroll container (scrollHeight ' +
    thread.scrollHeight + ' vs clientHeight ' + thread.clientHeight + ')');
  assert(r(composer).bottom <= r(app).bottom + 0.5,
    'the composer was pushed outside the app box: composer ends at ' +
    r(composer).bottom + ' but the app ends at ' + r(app).bottom);
});
</script>`

func TestChatSurfaceHoldsItsLayoutInBrowser(t *testing.T) {
	// The SHIPPED stylesheet. A copy of the rules under test has no owner
	// and the copy is the one that drifts.
	layoutCSS, err := staticFS.ReadFile("static/layout.css")
	if err != nil {
		t.Fatal(err)
	}
	page := strings.Replace(chatLayoutPage, "__LAYOUT_CSS__", string(layoutCSS), 1)
	if page == chatLayoutPage {
		t.Fatal("stylesheet placeholder not substituted — the page would assert against no CSS at all")
	}
	runPageInEngines(t, page, nil)
}

// The geometry above proves the CSS holds GIVEN the structure. This
// proves the shipped document actually has that structure — otherwise a
// page that regressed #firstboot back out of the chat view would keep a
// green geometry test built on a fixture that no longer matches.
// The third invariant, which the header above says needs harness work:
// form controls reach 16px on phones, or iOS zooms on focus and never
// zooms back. The rule lives inside max-width:767px, so it only exists
// at a phone viewport — the engine must be LAUNCHED at 375x812.
const phoneChatPage = `<!doctype html>
<style>__LAYOUT_CSS__</style>
<div class="app" id="app">
	<main class="stage">
		<div class="views" id="views">
			<section class="view on" id="view-chat">
				<div class="chat-body">
					<div class="thread" id="thread"><div class="thread-inner" id="thread-inner"></div></div>
				</div>
				<div class="composer-wrap" id="composer-wrap">
					<div class="composer"><textarea id="msg-input" rows="1"></textarea></div>
				</div>
			</section>
		</div>
	</main>
</div>
<script type="module">
import { assert, run } from "/__harness.js";
run(() => {
	const q = s => document.querySelector(s);
	const fs = e => parseFloat(getComputedStyle(e).fontSize);

	// 0. Guard: we are actually INSIDE the phone block, or every
	//    assertion below passes against rules that never applied.
	const inner = document.documentElement.clientWidth;
	assert(inner <= 767,
		'the viewport is ' + inner + 'px - the phone rules never applied; ' +
		'a 16px pass here would validate nothing');

	// 1. THE ONE CONTROL AN OPERATOR TYPES INTO IS 16px ON A PHONE.
	//    iOS zooms on focus below 16px and never zooms back out; an
	//    overlay .composer textarea rule wins at every width because
	//    custom.css loads last. This pins the SHIPPED floor.
	const ta = q('#msg-input');
	assert(fs(ta) >= 16,
		'#msg-input computes to ' + fs(ta) + 'px at phone width - iOS will zoom on focus and never zoom back out');

	// 2. THE PRESENCE STRIP COVERS THE TOP, NOT THE CHAT.
	//    At 767px .presence goes position:fixed across the top and
	//    .stage gains padding-top to clear it. An overlay that
	//    drops the padding slams content under the strip.
	const stage = q('.stage');
	const pad = parseFloat(getComputedStyle(stage).paddingTop);
	assert(pad >= 52,
		'.stage padding-top is ' + pad + 'px at phone width - the fixed presence strip paints over the thread');
});
</script>`

func TestPhoneWidthFrameHoldsInBrowser(t *testing.T) {
	layoutCSS, err := staticFS.ReadFile("static/layout.css")
	if err != nil {
		t.Fatal(err)
	}
	page := strings.Replace(phoneChatPage, "__LAYOUT_CSS__", string(layoutCSS), 1)
	if page == phoneChatPage {
		t.Fatal("stylesheet placeholder not substituted")
	}
	runPageInEnginesAtSize(t, page, nil, 375, 812)
}

// TestPhoneWidthFixtureDetectsOverlayHazard pins the negative control
// as a permanent green test: the identical fixture with one overlay
// rule - .composer textarea { font-size:14px } - appended after the
// shipped stylesheet, exactly as custom.css loads last. The documented
// hazard is that such a rule wins at every width and reinstates the
// iOS zoom trap invisibly. Here that hazard is the EXPECTED outcome:
// if the 14px override ever stopped winning in this fixture (cascade
// insulation, specificity change, harness serving order), the phone
// test above would pass while guarding nothing - so this test fails
// first, naming the loss of sensitivity.
func TestPhoneWidthFixtureDetectsOverlayHazard(t *testing.T) {
	layoutCSS, err := staticFS.ReadFile("static/layout.css")
	if err != nil {
		t.Fatal(err)
	}
	hazard := string(layoutCSS) + "\n.composer textarea { font-size: 14px; }\n"
	page := strings.Replace(phoneChatPage, "__LAYOUT_CSS__", hazard, 1)
	if page == phoneChatPage {
		t.Fatal("stylesheet placeholder not substituted")
	}
	proof := strings.Replace(phoneChatPage, `assert(fs(ta) >= 16,
		'#msg-input computes to ' + fs(ta) + 'px at phone width - iOS will zoom on focus and never zoom back out');`,
		`assert(fs(ta) < 16, 'fixture lost sensitivity: a late 14px overlay rule no longer wins - TestPhoneWidthFrameHoldsInBrowser would pass while guarding nothing');`, 1)
	proof = strings.Replace(proof, "__LAYOUT_CSS__", hazard, 1)
	runPageInEnginesAtSize(t, proof, nil, 375, 812)
}

func TestShippedChatMarkupNestsTheOverlayInsideTheChatBody(t *testing.T) {
	index, err := staticFS.ReadFile("static/index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(index)

	start := strings.Index(html, `id="view-chat"`)
	if start < 0 {
		t.Fatal("no #view-chat section in the shipped document")
	}
	end := strings.Index(html[start:], "</section>")
	if end < 0 {
		t.Fatal("#view-chat is never closed")
	}
	view := html[start : start+end]

	body := strings.Index(view, `class="chat-body"`)
	thread := strings.Index(view, `id="thread"`)
	fb := strings.Index(view, `id="firstboot"`)
	composer := strings.Index(view, `id="composer-wrap"`)

	for name, at := range map[string]int{
		"chat-body": body, "thread": thread, "firstboot": fb, "composer-wrap": composer,
	} {
		if at < 0 {
			t.Fatalf("%s is not inside #view-chat — the overlay's containing block is not what the layout test asserts against", name)
		}
	}
	if !(body < thread && thread < fb && fb < composer) {
		t.Fatalf("order inside #view-chat is chat-body=%d thread=%d firstboot=%d composer=%d; "+
			"the overlay must sit in .chat-body beside the thread and BEFORE the composer, "+
			"or flex no longer owns the composer's clearance", body, thread, fb, composer)
	}
}
