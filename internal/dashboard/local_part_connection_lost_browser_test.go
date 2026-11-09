//go:build !windows

package dashboard

import "testing"

// THE LOCAL-MODEL PART OVER A LOST CONNECTION, in real engines, with the
// page's own socket code (a review 2026-11-06 13:31Z #1). A look and an add
// asked on a connection that is lost are never answered, so neither may
// hold its button. The add may have been saved before its answer was lost:
// the providers answer every reconnect asks for says which, by the found
// server's exact address.
func TestTheLocalModelPartIsReleasedWhenTheConnectionIsLost(t *testing.T) {
	page := substratePageMarkup + `<script type="module">
import { S } from './state.js';
import { connect } from './ws.js';
import { renderSettings } from './views/settings.js';
import { assert, run } from './__harness.js';
` + offerSettingsJS + `run(() => {
 const sockets=[];
 window.setTimeout=()=>1;window.clearTimeout=()=>{};
 window.WebSocket=class {
   constructor(){this.readyState=0;this.frames=[];sockets.push(this);}
   send(bytes){this.frames.push(JSON.parse(bytes));}
   recv(frame){this.onmessage({data:JSON.stringify(frame)});}
 };
 const ADDR='http://127.0.0.1:11434/v1';
 S.view='settings';S.identityExists=true;S.providersLoaded=true;
 S.config={llm:{provider:'Cloud',model:'big'},dashboard:{},plugins:{},witness:{},prompt:{},genesis:{},tools:{},agency:{}};
 S.providers=[{name:'Cloud',api_type:'openai',endpoint:'https://cloud.example/v1',default_model:'big',models:['big'],configured_models:[],has_key:true,status:'ok'}];
 connect();let socket=sockets.at(-1);socket.readyState=1;socket.onopen();
 const one=sel=>document.querySelector(sel);
 renderSettings();S.settingsSection('providers');S.requestSettingsSection();renderSettings();
 const lookB=()=>one('[data-local] [data-local-look]'), addB=()=>one('[data-local-add="11434"]');
 assert(lookB(),'the local-model part is not drawn in Providers');
 const sent=type=>socket.frames.filter(f=>f.type===type).at(-1);

 lookB().click();
 assert(lookB().disabled,'Look is not held while it asks');
 socket.recv({type:'local_found',request_id:sent('local_look').request_id,local_found:[{port:11434,address:ADDR,usual:'Ollama',models:['m1']}]});
 renderSettings();
 assert(addB()&&!addB().disabled,'the found server offers no Add');
 addB().click();
 assert(addB().disabled,'Add is not held while it adds');
 lookB().click();
 assert(lookB().disabled,'the second Look is not held while it asks');

 // The connection is lost with both out.
 socket.readyState=3;socket.onclose();
 assert(!lookB().disabled,'Look is still held after the connection was lost');
 assert(addB()&&!addB().disabled,'Add is still held after the connection was lost');

 // Reconnect: the page asks for providers; its answer shows the saved entry.
 connect();socket=sockets.at(-1);socket.readyState=1;socket.onopen();
 const q=socket.frames.find(f=>f.type==='query'&&f.query==='providers');
 assert(q,'the reconnect asked for no providers');
 socket.recv({type:'providers',request_id:q.request_id,providers:[...S.providers,{name:'Local server, port 11434',api_type:'openai',endpoint:ADDR,default_model:'m1',models:['m1'],configured_models:[],local:true,status:'ok'}]});
 renderSettings();
 assert(one('[data-local-found="11434"]')&&one('[data-local-found="11434"]').textContent.includes('Ollama on port 11434 is added.'),'an add the server saved is not shown as added after the reconnect: '+(one('[data-local]')||{}).textContent);
});
</script>`
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
	runPageInEngines(t, page, modules)
}
