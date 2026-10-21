//go:build !windows

package dashboard

import "testing"

// THE PAGE NAMES A PLUGIN'S PRIMARY TOOLS AND OFFERS NO CONTROL OVER
// THEM: they are in the identity's tool list because their author
// marked them, derived at each request, so there is nothing to select or
// release. A plugin that marks none shows no line, and rendering sends
// nothing.
func TestPluginsPageNamesPrimaryToolsWithoutAControl(t *testing.T) {
	page := formRecoveryMarkup + `<script type="module">
import {S} from '/state.js';import {frames} from '/ws.js';import {renderPlugins} from '/views/plugins.js';import {run} from '/__harness.js';
run(assert=>{
 S.view='plugins';S.providers=[];S.providersLoaded=true;
 S.config={secrets_ok:true,llm:{},dashboard:{},prompt:{},witness:{},logs:{},agency:{},restart_required:[],plugins:{autoload:'T2',catalog:[],catalog_url:'',auth_profiles:[],providers:[],installed:[{id:'org.example.notes',version:'1.0.0',tier:'T3',mode:'wasm',variant:'fixture',settings:[],grants:{},tools:['pl_notes_search','pl_notes_store','pl_notes_old'],primary_tools:['search','store']},{id:'org.example.legacy',version:'1.0.0',tier:'T1',mode:'wasm',variant:'fixture',settings:[],grants:{},tools:['pl_legacy_one']}]} };
 renderPlugins();
 const cards=[...document.querySelectorAll('.card')];
 const notes=cards.find(c=>c.textContent.includes('org.example.notes')),legacy=cards.find(c=>c.textContent.includes('org.example.legacy'));
 assert(notes&&notes.textContent.includes("Primary tools, in the identity's tool list: search, store"),'the declared primaries are named');
 assert(legacy&&!legacy.textContent.includes('Primary tools'),'a plugin that marks none shows no line');
 assert(document.querySelectorAll('[data-plugin^="offer:"],[data-plugin^="release:"]').length===0,'no selection or release control');
 assert(cards.every(c=>!c.textContent.includes('Use primary tools')&&!c.textContent.includes('Stop using tools')),'no selection words');
 assert(frames.length===0,'rendering sends nothing');
});</script>`
	runPageInEngines(t, page, formRecoveryModules())
}

func TestPluginActionFailureRestoresOnlyItsCurrentControl(t *testing.T) {
	page := formRecoveryMarkup + `<button id="send-btn"></button><button id="mic"></button><script type="module">
import {S} from '/state.js';import {connect} from '/ws.js';import {renderPlugins} from '/views/plugins.js';import {run} from '/__harness.js';
run(assert=>{
 const sockets=[];window.setTimeout=()=>1;window.clearTimeout=()=>{};
 window.WebSocket=class{constructor(){this.readyState=0;this.frames=[];sockets.push(this);}send(bytes){this.frames.push(JSON.parse(bytes));}recv(frame){this.onmessage({data:JSON.stringify(frame)});}};
 S.view='plugins';S.identityExists=true;S.providers=[];S.providersLoaded=true;
 S.config={secrets_ok:true,llm:{},dashboard:{},prompt:{},witness:{},logs:{},agency:{},restart_required:[],plugins:{autoload:'T2',catalog:[],auth_profiles:[],providers:[],installed:[],pending:['org.example.one','org.example.two'].map(id=>({id,version:'1',phase:'refused',summary:'fixture refusal'}))}};
 connect();const socket=sockets.at(-1);socket.readyState=1;socket.onopen();renderPlugins();
 const one=()=>document.querySelector('[data-plugin="retry:org.example.one"]');
 const two=()=>document.querySelector('[data-plugin="retry:org.example.two"]');
 const actions=()=>socket.frames.filter(f=>f.type==='plugin');
 one().click();two().click();const first=actions()[0].request_id,other=actions()[1].request_id;
 socket.recv({type:'error',request_id:'unrelated',message:'unrelated failure'});
 assert(one().disabled&&two().disabled,'unrelated error resets another action');
 socket.recv({type:'error',request_id:first,message:'fixture save refused'});
 assert(!one().disabled&&one().textContent==='Try again'&&two().disabled,'failure does not restore only its own control');
 assert(S.config.plugins.pending[0].phase==='refused','failure changed the confirmed state');
 one().click();const newer=actions().at(-1).request_id;
 socket.recv({type:'error',request_id:first,message:'stale refusal'});
 assert(one().disabled&&two().disabled,'stale reply reset a newer action');
 renderPlugins();one().click();const afterRender=actions().at(-1).request_id;
 socket.recv({type:'error',request_id:newer,message:'pre-render refusal'});
 assert(one().disabled,'pre-render reply reset current control');
 socket.recv({type:'error',request_id:afterRender,message:'current save refused'});
 assert(!one().disabled,'current correlated failure left disabled control');
 const before=actions().length;socket.recv({type:'error',request_id:other,message:'old other action'});
 assert(actions().length===before,'failure automatically retried an action');
 socket.readyState=3;one().click();
 assert(!one().disabled&&actions().length===before,'offline click left disabled control or submitted action');
});</script>`
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
	runPageInEngines(t, page, modules)
}
