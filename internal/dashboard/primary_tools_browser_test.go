//go:build !windows

package dashboard

import "testing"

func TestPluginsPageSelectsPrimaryToolsWithoutChangingGrants(t *testing.T) {
	page := formRecoveryMarkup + `<script type="module">
import {S} from '/state.js';import {frames} from '/ws.js';import {renderPlugins} from '/views/plugins.js';import {run} from '/__harness.js';
run(assert=>{
 S.view='plugins';S.providers=[];S.providersLoaded=true;
 S.config={secrets_ok:true,llm:{},dashboard:{},prompt:{},witness:{},logs:{},agency:{},restart_required:[],plugins:{autoload:'T2',catalog:[],catalog_url:'',auth_profiles:[],providers:[],installed:[{id:'org.example.notes',version:'1.0.0',tier:'T3',mode:'wasm',variant:'fixture',settings:[],grants:{},tools:['pl_notes_search','pl_notes_store','pl_notes_old'],primary_tools:['search','store']},{id:'org.example.legacy',version:'1.0.0',tier:'T1',mode:'wasm',variant:'fixture',settings:[],grants:{},tools:['pl_legacy_one']}]} };
 renderPlugins();
 const buttons=()=>[...document.querySelectorAll('[data-plugin^="offer:"]')];
 assert(buttons().length===1,'only a plugin declaring primary operations has a selection');
 assert(buttons()[0].textContent==='Use primary tools'&&!buttons()[0].disabled,'selection is available');
 assert(buttons()[0].closest('.card').textContent.includes('Primary tools: search, store'),'declared operations named before selection');
 buttons()[0].click();
 assert(frames.length===1&&JSON.stringify(frames[0].plugin)===JSON.stringify({action:'offer',id:'org.example.notes'}),'one exact plugin selection, without grant or config fields');
 assert(frames[0].type==='plugin'&&!frames[0].config,'selection uses existing plugin action');
 assert(buttons()[0].disabled&&buttons()[0].textContent==='Selecting…','pending action visible');
 S.config.plugins.installed[0].primary_offered=true;S.config.plugins.installed[0].tools_selected=true;renderPlugins();
 assert(buttons()[0].disabled&&buttons()[0].textContent==='Primary tools in use','only host readback confirms selection');
 S.config.plugins.installed[0].primary_offered=false;renderPlugins();
 assert(!buttons()[0].disabled,'release or refusal restores explicit selection');
 assert(frames.length===1,'rendering never selects automatically');
 const releases=()=>[...document.querySelectorAll('[data-plugin^="release:"]')];
 assert(releases().length===1&&releases()[0].textContent==='Stop using tools','partial selection exposes release');
 assert(releases()[0].closest('.card').textContent.includes('all selected operations of this plugin'),'release scope explained');
 S.config.plugins.installed[0].tools_waiting=2;renderPlugins();
 assert(releases()[0].closest('.card').textContent.includes('2 selected operations waiting for free offer space'),'waiting selection is distinct from callable tools');
 releases()[0].click();
 assert(frames.length===2&&JSON.stringify(frames[1].plugin)===JSON.stringify({action:'release',id:'org.example.notes'}),'one exact release without grants or uninstall');
 assert(releases()[0].disabled&&releases()[0].textContent==='Stopping use…','release pending state');
 S.config.plugins.installed[0].tools_selected=false;S.config.plugins.installed[0].tools_waiting=0;renderPlugins();
 assert(releases().length===0&&!buttons()[0].disabled,'host readback confirms released choice');
 S.config.plugins.installed[1].tools_selected=true;renderPlugins();
 assert(releases().length===1&&releases()[0].dataset.plugin==='release:org.example.legacy','withheld choice without primary hints can be released');
 assert(frames.length===2,'readback never submits changes');
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
 S.config={secrets_ok:true,llm:{},dashboard:{},prompt:{},witness:{},logs:{},agency:{},restart_required:[],plugins:{autoload:'T2',catalog:[],auth_profiles:[],providers:[],installed:['org.example.one','org.example.two'].map(id=>({id,version:'1',tier:'T3',mode:'wasm',variant:'fixture',settings:[],grants:{},tools:['search'],primary_tools:['search'],primary_offered:true,tools_selected:true}))}};
 connect();const socket=sockets.at(-1);socket.readyState=1;socket.onopen();renderPlugins();
 const one=()=>document.querySelector('[data-plugin="release:org.example.one"]');
 const two=()=>document.querySelector('[data-plugin="release:org.example.two"]');
 const actions=()=>socket.frames.filter(f=>f.type==='plugin');
 one().click();two().click();const first=actions()[0].request_id,other=actions()[1].request_id;
 socket.recv({type:'error',request_id:'unrelated',message:'unrelated failure'});
 assert(one().disabled&&two().disabled,'unrelated error resets another action');
 socket.recv({type:'error',request_id:first,message:'fixture save refused'});
 assert(!one().disabled&&one().textContent==='Stop using tools'&&two().disabled,'failure does not restore only its own control');
 assert(S.config.plugins.installed[0].tools_selected&&S.config.plugins.installed[0].primary_offered,'failure changed confirmed selection');
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
