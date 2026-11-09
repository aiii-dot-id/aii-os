//go:build !windows

// Runs only through the unix browser-engine rig (browserengine_test.go,
// same tag) — this file calls its helpers, so it shares its build row.

package dashboard

import "testing"

const pluginActWaitsPage = substratePageMarkup + `<div class="app"><div class="stack" id="plugins-stack"></div></div>
<script type="module">
import { S } from './state.js';
import { connect } from './ws.js';
import { assert, run } from './__harness.js';
` + offerSettingsJS + `run(() => {
 const sockets=[];
 window.setTimeout=()=>1;window.clearTimeout=()=>{};
 window.WebSocket=class {
   constructor(){this.readyState=0;this.frames=[];sockets.push(this);}
   send(bytes){this.frames.push(JSON.parse(bytes));}
   recv(frame){this.onmessage({data:JSON.stringify(frame)});}
 };
 const config={plugins:{installed:[{id:'org.example.uid',version:'1.0.0',tier:'T3',mode:'wasm',settings:[],capabilities:[],
   grants:{listed:true,read_only:false,auto_confirm:[]},
   acts:[{id:'act-9',operation:'enroll',summary:'Enroll Quinn',args:{name:'Quinn'},proposed:'2030-01-01T10:00:00Z',expires:'2030-01-01T10:10:00Z'}]}]}};
 S.view='plugins';S.identityExists=true;S.providersLoaded=true;S.config=config;
 connect();const socket=sockets.at(-1);socket.readyState=1;socket.onopen();
 socket.recv({type:'config',config});
 const st=document.getElementById('plugins-stack');
 const btn=d=>st.querySelector('[data-act-id="act-9"][data-act-decision="'+d+'"]');
 const said=()=>{const el=st.querySelector('[data-act-card="act-9"] [data-act-said]');return el?el.textContent:'';};
 assert(btn('confirm') && !btn('confirm').disabled,'rig: the act card did not draw');

 socket.readyState=3;const before=socket.frames.length;
 btn('confirm').click();
 assert(socket.frames.length===before,'rig: a decision was sent with no connection');
 assert(!btn('confirm').disabled && said()==='Not connected — your answer was not sent.',
   'A DECISION PRESSED WITH NO CONNECTION SAID NOTHING, or left the card dead: '+said());
 socket.readyState=1;

 btn('confirm').click();
 const asked=socket.frames.at(-1);
 assert(asked.type==='plugin' && asked.plugin.action==='confirm' && asked.plugin.act==='act-9' && asked.request_id,'rig: the decision was not sent with a request id');
 assert(btn('confirm').disabled && btn('deny').disabled && btn('confirm').textContent==='Running…' && said()==='','a sent decision did not wait');
 socket.recv({type:'config',config});
 assert(btn('confirm').disabled && btn('confirm').textContent==='Running…','another page\'s configuration ended this card\'s wait');
 window.__think='untouched';
 socket.recv({type:'error',request_id:asked.request_id,message:'fixture: the act expired'});
 assert(!btn('confirm').disabled && said()==='Not taken: fixture: the act expired','A REFUSED DECISION WAS ONLY A TOAST: the card says nothing of it: '+said());
 assert(window.__think==='untouched','a refused decision was read as a turn\'s error');

 btn('deny').click();
 assert(btn('deny').textContent==='Dropping…' && btn('deny').disabled && said()==='','rig: a deny did not wait');
 socket.readyState=3;socket.onclose();
 assert(!btn('deny').disabled && btn('deny').textContent==='Deny' && said().includes('Connection lost'),'a connection lost under a decision left the card waiting');
});
</script>`

func TestAPluginsActCardWaitsForTheHostAsTheThreadsCardDoes(t *testing.T) {
	ws, err := staticFS.ReadFile("static/ws.js")
	if err != nil {
		t.Fatal(err)
	}
	modules := wsPageModules(ws, []byte(`export const S={providers:[]};`))
	for _, file := range []string{"views/plugins.js", "views/settings.js", "util.js"} {
		data, err := staticFS.ReadFile("static/" + file)
		if err != nil {
			t.Fatal(err)
		}
		modules["/"+file] = data
	}
	runPageInEngines(t, pluginActWaitsPage, modules)
}
