//go:build !windows

package dashboard

import (
	"strings"
	"testing"
)

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
import { toolEventLive, renderInteractionPages } from './views/chat.js';

function renderHistory(history) { renderInteractionPages(new Map([['recorded',{rows:history.map((r,i)=>({id:'fixture-'+i,sequence:String(i+1),kind:'legacy',details:{},...r}))}],['transient',{rows:[]}]]),()=>{},false); }

run(() => {
  const inner = document.getElementById('thread-inner');

  const cmd=JSON.stringify({command:'for i in $(seq 1 40); do echo line_$i_of_a_deliberately_long_shell_command; done'});
  toolEventLive('shell',cmd);
  assert(!inner.querySelector('details.tool-ev'),'live progress invented a record');
  const rows=[{id:'call',sequence:'1',kind:'tool_call',role:'system',content:cmd,details:{tool:'shell'}}];
  const render=()=>renderInteractionPages(new Map([['recorded',{identity:'fixture',incarnation:'disk',rows}],['transient',{rows:[]}]]),()=>{},false);
  render();
  const card=inner.querySelector('details.tool-ev');
  assert(card && !card.open && card.querySelector('.tname').textContent==='shell','tool title/disclosure missing');
  assert(!card.querySelector('summary').textContent.includes('deliberately_long'),'arguments crowded the compact summary');
  assert(card.querySelector('.record-content').textContent===cmd,'retained arguments changed');
  card.querySelector('summary').click();assert(card.open,'click did not expand');
  card.querySelector('summary').click();assert(!card.open,'second click did not collapse');
  rows.push({id:'outcome',related_id:'call',sequence:'2',kind:'tool_result',role:'system',content:'retained result',outcome:'succeeded',details:{tool:'shell'}});
  render();assert(inner.querySelectorAll('.tool-ev').length===1 && card.textContent.includes('retained result'),'result duplicated or lost the execution');
  rows.push({id:'plugin',sequence:'3',kind:'tool_call',role:'system',content:'{"id":1}',details:{tool:'pl_org_example_memory_store'}});render();
  assert(inner.querySelectorAll('.tname')[1].textContent==='pl_org_example_memory_store','renderer invented a package/method boundary');
  const radius=parseFloat(getComputedStyle(card.querySelector('summary')).borderTopLeftRadius);
  assert(radius>0 && radius<20,'disclosure became an oval');
  renderHistory([{role:'system',content:'→ shell({"command":"git log"})\n← retained legacy result'}]);
  const legacy=inner.querySelector('details.tool-ev');
  assert(legacy && legacy.querySelector('.record-content').textContent.includes('git log') && legacy.querySelector('.record-content').textContent.includes('retained legacy result'),'legacy retained detail disappeared');

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
	runPageInEngines(t, page, map[string][]byte{
		"/ws.js":  stubModule("send", "wsReady"),
		"/app.js": stubModule("toast"),
	})
}
