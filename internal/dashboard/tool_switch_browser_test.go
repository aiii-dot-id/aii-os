//go:build !windows

// Runs only through the unix browser-engine rig (browserengine_test.go,
// same tag) — this file calls its helpers, so it shares its build row.

package dashboard

import "testing"

const toolSwitchPage = substratePageMarkup + `<script type="module">
import { S } from './state.js';
import { connect } from './ws.js';
import { renderSettings } from './views/settings.js';
import { assert, run } from './__harness.js';
run(() => {
 const sockets=[];
 window.setTimeout=()=>1;window.clearTimeout=()=>{};
 window.WebSocket=class {
   constructor(){this.readyState=0;this.frames=[];sockets.push(this);}
   send(bytes){this.frames.push(JSON.parse(bytes));}
   recv(frame){this.onmessage({data:JSON.stringify(frame)});}
 };
 S.view='settings';S.identityExists=true;S.providersLoaded=true;S.config={llm:{}};S.tools=[];
 connect();const socket=sockets.at(-1);socket.readyState=1;socket.onopen();
 const sw=name=>document.querySelector('[data-tool="'+name+'"]');
 const said=()=>document.getElementById('settings-stack').textContent;
 const press=name=>{const s=sw(name);s.checked=!s.checked;s.dispatchEvent(new Event('change',{bubbles:true}));return socket.frames.at(-1);};
 const list=shell=>[{name:'shell',description:'run a command',enabled:shell},{name:'web_fetch',description:'read a page',enabled:true}];
 renderSettings();document.querySelector('[data-sec="tools"]').click();
 socket.recv({type:'tools',tools:list(true)});
 assert(sw('shell') && sw('shell').checked && !sw('shell').disabled,'rig: the tools card did not draw the host\'s list');

 const asked=press('shell');
 assert(asked.type==='tool_toggle' && asked.tool==='shell' && asked.enabled===false && asked.request_id,'rig: the press was not sent with a request id');
 assert(sw('shell').disabled && sw('web_fetch').disabled && said().includes('Waiting for the runtime'),
   'A PRESSED SWITCH DID NOT WAIT: the card shows a change the host has not answered');
 socket.recv({type:'tools',tools:list(true)});
 assert(sw('shell').disabled && !sw('shell').checked,'another page\'s list ended this page\'s wait');

 window.__think='untouched';
 socket.recv({type:'error',request_id:asked.request_id,message:'fixture: shell is held on'});
 assert(sw('shell').checked && !sw('shell').disabled,'A REFUSED SWITCH STAYED WHERE IT WAS PRESSED');
 assert(said().includes('fixture: shell is held on'),'the refusal\'s words are not beside the switch');
 assert(window.__think==='untouched','a refused switch was read as a turn\'s error');

 const again=press('shell');
 socket.recv({type:'tools',request_id:again.request_id,tools:list(false)});
 assert(!sw('shell').checked && !sw('shell').disabled && !said().includes('Waiting') && !said().includes('fixture'),'the host\'s answer did not settle the switch');

 press('web_fetch');
 socket.readyState=3;socket.onclose();
 assert(sw('web_fetch').checked && !sw('web_fetch').disabled && said().includes('completion is unknown'),
   'a connection lost under a press left the switch pressed, or said nothing');

 const before=socket.frames.length;press('web_fetch');
 assert(socket.frames.length===before,'rig: a press with no connection was sent');
 assert(sw('web_fetch').checked && said().includes('Not connected'),
   'A SWITCH PRESSED WITH NO CONNECTION STAYED PRESSED: the page shows a tool off that the host still holds on');
});
</script>`

func TestAToolSwitchWaitsForTheHostAndGoesBackOnARefusal(t *testing.T) {
	ws, err := staticFS.ReadFile("static/ws.js")
	if err != nil {
		t.Fatal(err)
	}
	modules := wsPageModules(ws, []byte(`export const S={providers:[]};`))
	for _, file := range []string{"views/settings.js", "util.js"} {
		data, err := staticFS.ReadFile("static/" + file)
		if err != nil {
			t.Fatal(err)
		}
		modules["/"+file] = data
	}
	runPageInEngines(t, toolSwitchPage, modules)
}
