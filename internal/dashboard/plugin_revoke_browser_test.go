//go:build !windows

// Runs only through the unix browser-engine rig (browserengine_test.go,
// same tag) — this file calls its helpers, so it shares its build row.

package dashboard

import "testing"

/*
A REVOKE NAMES THE ONE STANDING CONFIRMATION IT TAKES AWAY.

	The plugin's card revoked a standing confirmation by sending the list
	without it, from the page's own copy, through the settings door. A
	second page that had not heard of one revoke sent its own older list
	with its next, and the confirmation another page had revoked ran
	without asking again.

	This drives the REAL ws.js, Plugins view and settings view over a
	socket the page owns: a revoke sends the plugin and the one operation
	and no list, waits for the answer to its request while another page's
	configuration arrives, says a refusal beside the card, says what the
	host took, says a lost connection leaves it unknown, and says a press
	with no connection sent nothing.
*/
const pluginRevokePage = substratePageMarkup + `<div class="app"><div class="stack" id="plugins-stack"></div></div>
<script type="module">
import { S } from './state.js';
import { connect } from './ws.js';
import { renderPlugins } from './views/plugins.js';
import { assert, run } from './__harness.js';
` + offerSettingsJS + `run(() => {
 const sockets=[];
 window.setTimeout=()=>1;window.clearTimeout=()=>{};
 window.WebSocket=class {
   constructor(){this.readyState=0;this.frames=[];sockets.push(this);}
   send(bytes){this.frames.push(JSON.parse(bytes));}
   recv(frame){this.onmessage({data:JSON.stringify(frame)});}
 };
 const config=standing=>({plugins:{installed:[{id:'org.example.uid',version:'1.0.0',tier:'T3',mode:'wasm',settings:[],capabilities:[],
   grants:{listed:true,read_only:false,auto_confirm:standing}}]}});
 S.view='plugins';S.identityExists=true;S.providersLoaded=true;S.config=config(['enroll','list']);
 connect();const socket=sockets.at(-1);socket.readyState=1;socket.onopen();
 const st=document.getElementById('plugins-stack');
 const rv=op=>st.querySelector('[data-revoke-auto="org.example.uid"][data-op="'+op+'"]');
 const said=()=>{const el=st.querySelector('[data-revoke-said]');return el?el.textContent:'';};
 socket.recv({type:'config',config:config(['enroll','list'])});
 assert(rv('enroll') && rv('list'),'rig: the card did not list the standing confirmations');

 rv('enroll').click();
 const asked=socket.frames.at(-1);
 assert(asked.type==='plugin' && asked.plugin && asked.plugin.action==='revoke' && asked.plugin.id==='org.example.uid' && asked.plugin.act==='enroll' && asked.request_id,
   'A REVOKE SENT THE PAGE\'S OWN LIST: another page that has not heard of a revoke puts it back ('+JSON.stringify(asked)+')');
 assert(!socket.frames.some(f=>f.type==='config_set'),'a revoke went through the settings door');
 assert(rv('enroll').disabled && rv('enroll').textContent==='Revoking…' && rv('list').disabled,'a revoke did not wait for the host');
 socket.recv({type:'config',config:config(['enroll','list'])});
 assert(rv('enroll').textContent==='Revoking…','another page\'s configuration ended this page\'s wait');

 window.__think='untouched';
 socket.recv({type:'error',request_id:asked.request_id,message:'fixture: nothing to revoke; nothing was changed'});
 assert(said().includes('fixture: nothing to revoke') && !rv('enroll').disabled,'A REFUSED REVOKE SAID NOTHING BESIDE THE CARD, or stayed waiting');
 assert(window.__think==='untouched','a refused revoke was read as a turn\'s error');

 rv('enroll').click();const again=socket.frames.at(-1);
 socket.recv({type:'config',request_id:again.request_id,config:config(['list'])});
 assert(!rv('enroll') && rv('list') && !rv('list').disabled && said()==='Revoked: enroll asks you again before it runs.','the host\'s answer did not settle the revoke: '+said());

 rv('list').click();
 socket.readyState=3;socket.onclose();
 assert(said().includes('it is not known whether list was revoked') && !rv('list').disabled,'a connection lost under a revoke said nothing, or left it waiting');

 const before=socket.frames.length;rv('list').click();
 assert(socket.frames.length===before && said()==='Not connected — list was not revoked.','A REVOKE PRESSED WITH NO CONNECTION SAID NOTHING');
});
</script>`

func TestARevokeNamesTheOneConfirmationAndWaitsForTheHost(t *testing.T) {
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
	runPageInEngines(t, pluginRevokePage, modules)
}
