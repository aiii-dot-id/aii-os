//go:build !windows

package dashboard

import (
	"strings"
	"testing"
)

const providerLocalMarkPage = `<!doctype html><div id="settings-stack"></div>
<script type="module">
import { S } from './state.js';
import { renderSettings } from './views/settings.js';
import { frames } from './ws.js';
import { assert, run } from './__harness.js';
run(() => {
 S.config={llm:{provider:'Main',model:'m'},dashboard:{},plugins:{},witness:{},prompt:{},agency:{},genesis:{},tools:{}};
 S.providersLoaded=true;
 S.providers=[{name:'oMLX',api_type:'openai',endpoint:'http://127.0.0.1:8081/v1',default_model:'q',configured_models:[],local:true},
              {name:'By hand',api_type:'openai',endpoint:'http://127.0.0.1:9000/v1',default_model:'q',configured_models:[]}];
 renderSettings();
 S.settingsSection('providers'); S.requestSettingsSection(); renderSettings();
 const last=()=>frames.findLast(f=>f.type==='provider_set');
 const box=(i)=>{
  if(!document.getElementById('pv-local-'+i)) document.querySelector('[data-prov-row="'+i+'"]').click();
  const b=document.getElementById('pv-local-'+i);
  assert(b,'the editor for entry '+i+' must open with its Local box');
  return b;
 };
 const save=(i)=>document.querySelector('[data-prov-commit="'+i+'"]').click();

 assert(box(0).checked,'an entry marked local shows its box checked');
 const url=document.getElementById('pv-url-0');
 assert(url && url.compareDocumentPosition(box(0)) & Node.DOCUMENT_POSITION_FOLLOWING,'the box sits after the address');
 save(0);
 assert(last().entry.local===true,'a save that leaves the box alone keeps the mark: '+JSON.stringify(last().entry.local));
 box(0).checked=false;
 save(0);
 assert(last().entry.local===false,'unchecked, the save says false, which clears it: '+JSON.stringify(last().entry.local));

 assert(!box(1).checked,'an entry added by hand and not marked shows its box clear');
 box(1).checked=true;
 save(1);
 assert(last().entry.local===true&&last().entry.name==='By hand','checked, the hand-added entry is sent as local: '+JSON.stringify(last().entry));
});
</script>`

// THE PROVIDERS CARD MARKS AN ENTRY LOCAL: the box beside the address shows
// the stored mark, and a save says what the box holds, so a server on a
// port the look does not ask (an oMLX server on 8081) can be marked from
// the card. The server's keeping of an absent
// mark is the app's TestTheCardMarksAHandAddedEntryLocal.
func TestTheProvidersCardShowsAndSendsTheLocalMark(t *testing.T) {
	modules := map[string][]byte{}
	for _, path := range []string{"static/views/settings.js", "static/views/model-picker.js"} {
		data, err := staticFS.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		modules["/"+strings.TrimPrefix(path, "static/")] = data
	}
	modules["/state.js"] = []byte(`export const S={providers:[],config:null,providersLoaded:false};`)
	modules["/util.js"] = utilStub(`export const $=id=>document.getElementById(id);export const esc=v=>String(v??'');export const hueOf=()=>0;export const copyText=async()=>true;`)
	modules["/ws.js"] = []byte(`export const frames=[];export function send(f){frames.push(f);return 'request-1';}export function query(n,e){return send({type:'query',query:n,...e});}`)
	modules["/pending.js"] = []byte(`export function pendingSlot(){return {arm(){return true;},claim(){return false;},drop(){},waiting(){return false;}};} export function anyPending() { return false; }`)
	modules["/sandbox.js"] = []byte(`export function sandboxCardHTML(){return '';}export function wireSandboxCard(){}`)
	runPageInEngines(t, providerLocalMarkPage, modules)
}
