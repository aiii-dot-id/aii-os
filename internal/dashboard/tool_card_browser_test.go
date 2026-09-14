//go:build !windows

// .
// .

package dashboard

import (
	"strings"
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
const toolCardPage = `<!doctype html>
<style>__THEME_CSS__</style>
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
  <div class="presence" id="presence">
    <div class="orb-wrap" id="orb-wrap"><div class="orb" id="orb"></div></div>
    <div id="p-name">AII OS</div>
    <div class="p-state" id="p-state">present</div>
    <div class="pill" id="pill-mode" style="display:none">mode <b>normal</b></div>
  </div>
  <button id="send-btn">&#10148;</button>
  <div id="steerq" class="steerq"></div>
  <div id="toast"></div>
</div>
<script type="module">
import { assert, run } from './__harness.js';
import { toolEventLive, renderHistory } from './views/chat.js';

run(() => {
  const inner = document.getElementById('thread-inner');

  // 1. LIVE RENDER: full args behind the card, nothing destroyed
  //    client-side. The command is 150+ chars; the old renderer kept
  //    87 of them and the details body was null. prettyArgs may
  //    pretty-print (JSON parses), so the assert is CONTAINMENT of
  //    every value the wire carried, not byte-equality.
  const cmd = JSON.stringify({ command: 'for i in $(seq 1 40); do echo line_$i_of_a_deliberately_long_shell_command; done' });
  toolEventLive('shell', cmd);
  let card = inner.lastElementChild;
  assert(card && card.tagName === 'DETAILS', 'live event did not render a details card');
  const pre = card.querySelector('pre');
  assert(pre && pre.textContent.indexOf('deliberately_long_shell_command') !== -1 && pre.textContent.indexOf('line_$i') !== -1,
    'full command is not behind the card: ' + (pre ? JSON.stringify(pre.textContent.slice(0, 80)) : 'no pre'));

  // 2. THE TITLE CONTRACT: bold name in the summary, derived for
  //    plugin families, arrow-and-parens wire syntax gone.
  const tname = card.querySelector('.tname');
  assert(tname && tname.textContent === 'shell', 'plain tool title wrong: ' + (tname && tname.textContent));
  assert(!card.textContent.match(/^→/), 'wire arrow syntax leaked into the card');

  // 3. ONE CLICK EXPANDS, A SECOND COLLAPSES. Native toggle.
  assert(!card.open, 'card renders expanded before any click');
  card.querySelector('summary').dispatchEvent(new MouseEvent('click', { bubbles: true }));
  assert(card.open, 'first click did not expand the card');
  card.querySelector('summary').dispatchEvent(new MouseEvent('click', { bubbles: true }));
  assert(!card.open, 'second click did not collapse the card');

  // 4. PLUGIN TITLE DERIVATION: pl_org_example_memory_store →
  //    "plugin · org.example.memory · store" — family joined with dots,
  //    method last, no guessed id/method boundary.
  toolEventLive('pl_org_example_memory_store', '{"id": 1}');
  const ptitle = inner.lastElementChild.querySelector('.tname');
  assert(ptitle && ptitle.textContent === 'plugin · org.example.memory · store',
    'plugin title derivation wrong: ' + (ptitle && ptitle.textContent));

  // 5. GEOMETRY: the summary is a RECTANGLE now, not a 999px pill.
  //    border-radius 999px on a ~30px element is the oval the
  //    operator described; the card radius is var(--r-sm)=9px. The
  //    longhand is read because the open state sets two corners to 0,
  //    which makes the shorthand compute empty in some engines.
  //    Read BEFORE renderHistory — that call wipes the thread, and
  //    getComputedStyle on a detached node is empty string.
  const st = getComputedStyle(card.querySelector('summary'));
  const tl = parseFloat(st.borderTopLeftRadius);
  assert(tl > 0 && tl < 20,
    'summary is still an oval: border-top-left-radius ' + st.borderTopLeftRadius);

  // 6. HISTORY PARITY: a transcript turn "→ name(args)\n← result"
  //    renders the same card shape — full args recoverable, result
  //    excerpt present — so reload shows what live showed.
  const hargs = JSON.stringify({ command: 'git log --oneline | head -5', workdir: '/home/user' });
  renderHistory([
    { role: 'system', content: '→ shell(' + hargs + ')\n← 6231a955 ui-reform: overlay asset resolution\n1234abcd next line' }
  ]);
  const hcard = inner.lastElementChild;
  assert(hcard && hcard.tagName === 'DETAILS', 'history turn did not render a card');
  const hpre = hcard.querySelector('pre:not(.tres)');
  assert(hpre && hpre.textContent.indexOf('git log --oneline') !== -1,
    'history card lost the full args: ' + (hpre ? hpre.textContent.slice(0, 60) : 'no pre'));
  const tres = hcard.querySelector('.tres');
  assert(tres && tres.textContent.indexOf('6231a955') !== -1,
    'history card lost the result excerpt: ' + (tres ? tres.textContent.slice(0, 60) : 'no result pre'));
  assert(hcard.querySelector('.tname').textContent === 'shell', 'history title wrong');
});
</script>`

func TestToolCallCardDisclosesFullCommand(t *testing.T) {
	layoutCSS, err := staticFS.ReadFile("static/layout.css")
	if err != nil {
		t.Fatal(err)
	}
	themeCSS, err := staticFS.ReadFile("static/theme.css")
	if err != nil {
		t.Fatal(err)
	}
	page := strings.Replace(toolCardPage, "__LAYOUT_CSS__", string(layoutCSS), 1)
	page = strings.Replace(page, "__THEME_CSS__", string(themeCSS), 1)
	if page == toolCardPage {
		t.Fatal("stylesheet placeholders not substituted")
	}
	// .
	// .
	// .
	// .
	// .
	// .
	runPageInEngines(t, page, map[string][]byte{
		"/ws.js":  stubModule("send", "wsReady"),
		"/app.js": stubModule("toast"),
	})
}
