//go:build !windows

package dashboard

import (
	"strings"
	"testing"
)

const chatLayoutPage = `<!doctype html>
<style>__LAYOUT_CSS__</style>
<div class="app" id="app">
  <main class="stage">
    <div class="views" id="views">
      <section class="view on" id="view-chat">
        <div class="chat-body">
          <div class="thread" id="thread"><div class="thread-inner" id="thread-inner"></div></div>
          <div class="fb on" id="firstboot"><div class="fb-card"><h2>No identity lives here yet.</h2>
            <p class="fb-meet" id="fb-meet">You are about to meet your AI identity.</p>
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

  assert(r(app).height > 50 && r(composer).height > 0 && r(fb).height > 0,
    'zero-size geometry — nothing rendered (app ' + r(app).height +
    ', composer ' + r(composer).height + ', fb ' + r(fb).height + ')');

  assert(r(fb).bottom <= r(composer).top + 0.5,
    'the firstboot overlay runs under the composer: overlay ends at ' +
    r(fb).bottom + ' but the composer starts at ' + r(composer).top);

  fb.scrollTop = fb.scrollHeight;
  const birth = document.getElementById('fb-birth');
  assert(r(birth).bottom <= r(fb).bottom + 0.5 && r(birth).top >= r(fb).top - 0.5,
    'the Birth button cannot be scrolled into view: button ' + r(birth).top +
    '..' + r(birth).bottom + ' vs overlay ' + r(fb).top + '..' + r(fb).bottom);

  app.style.flexDirection = 'column';
  inner.innerHTML = '';
  for (let i = 0; i < 60; i++) {
    const d = document.createElement('div');
    d.className = 'msg';
    d.innerHTML = '<div class="body">' + 'a long conversation turn. '.repeat(40) + '</div>';
    inner.appendChild(d);
  }
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

const phoneChatPage = `<!doctype html>
<style>__LAYOUT_CSS__</style>
<div class="app" id="app">
	<header class="c-top-bar"><a class="c-top-bar__who" href="#/home"><span class="c-top-bar__name">Mira</span></a>
		<nav class="c-top-bar__nav" id="nav"><a class="c-top-bar__place c-top-bar__home" href="#/home"><span>Home</span></a><a class="c-top-bar__place" href="#/projects"><span>Projects</span></a><a class="c-top-bar__place" href="#/identity"><span>Mira</span></a><a class="c-top-bar__place" href="#/settings"><span>Settings</span></a></nav></header>
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

	const inner = document.documentElement.clientWidth;
	assert(inner <= 767,
		'the viewport is ' + inner + 'px - the phone rules never applied; ' +
		'a 16px pass here would validate nothing');

	const ta = q('#msg-input');
	assert(fs(ta) >= 16,
		'#msg-input computes to ' + fs(ta) + 'px at phone width - iOS will zoom on focus and never zoom back out');

	// 2. THE PLACES' BAR COVERS THE BOTTOM, NOT THE CHAT.
	//    At 767px the top bar's places go position:fixed along the
	//    bottom, and .stage keeps padding-bottom to clear them. An
	//    overlay that drops the padding, or a taller bar, slams the
	//    composer under the places.
	const stage = q('.stage'), bar = q('.c-top-bar__nav').getBoundingClientRect();
	const pad = parseFloat(getComputedStyle(stage).paddingBottom);
	assert(getComputedStyle(q('.c-top-bar__nav')).position === 'fixed' && Math.abs(bar.bottom - innerHeight) < 1,
		'the places are not fixed along the bottom at phone width - the fixture no longer draws what it guards');
	assert(pad >= bar.height - 0.5,
		'.stage padding-bottom is ' + pad + 'px at phone width and the places\' bar is ' + bar.height + 'px - it paints over the composer');
});
</script>`

// phoneCSS is what the phone fixture is drawn with: the tokens, the frame's
// stylesheet and the top bar's, in the page's order.
func phoneCSS(t *testing.T) string {
	t.Helper()
	var css []string
	for _, name := range []string{"static/tokens/tokens.css", "static/layout.css", "static/components/top-bar/top-bar.css"} {
		b, err := staticFS.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		css = append(css, string(b))
	}
	return strings.Join(css, "\n")
}

func TestPhoneWidthFrameHoldsInBrowser(t *testing.T) {
	page := strings.Replace(phoneChatPage, "__LAYOUT_CSS__", phoneCSS(t), 1)
	if page == phoneChatPage {
		t.Fatal("stylesheet placeholder not substituted")
	}
	runPageInEnginesAtSize(t, page, nil, 375, 812)
}

func TestPhoneWidthFixtureDetectsOverlayHazard(t *testing.T) {
	hazard := phoneCSS(t) + "\n.composer textarea { font-size: 14px; }\n"
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
