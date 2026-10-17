//go:build !windows

package dashboard

import (
	"strings"
	"testing"
)

const providerEmbeddingsPage = `<!doctype html><div id="settings-stack"></div>
<script type="module">
import { S } from './state.js';
import { renderSettings } from './views/settings.js';
import { frames } from './ws.js';
import { assert, run } from './__harness.js';
run(() => {
 S.config={llm:{provider:'Local',model:'m'},dashboard:{},plugins:{},witness:{},prompt:{},agency:{},genesis:{},tools:{}};
 S.providersLoaded=true;
 S.providers=[{name:'Local',api_type:'openai',endpoint:'http://127.0.0.1:8080/v1',default_model:'m',configured_models:[],embeddings_model:'bge-m3'},
              {name:'Other',api_type:'openai',endpoint:'https://example.test/v1',default_model:'m',configured_models:[]}];
 renderSettings();
 document.querySelector('[data-sec="providers"]').click();
 const last=()=>frames.findLast(f=>f.type==='provider_set');
 const editor=(i)=>{
  if(!document.getElementById('pv-embed-'+i)) document.querySelector('[data-prov-row="'+i+'"]').click();
  const f=document.getElementById('pv-embed-'+i);
  assert(f,'the editor for entry '+i+' must open');
  return f;
 };
 const save=(i)=>document.querySelector('[data-prov-commit="'+i+'"]').click();

 assert(editor(0).value==='bge-m3','the stored embeddings model must show in the field: '+editor(0).value);
 assert(editor(0).closest('details'),'the field sits with the advanced serving settings');
 save(0);
 assert(last().entry.embeddings_model==='bge-m3','a save that leaves the field alone sends the stored model back, which keeps it: '+JSON.stringify(last().entry.embeddings_model));

 editor(0).value='  text-embedding-3-small ';
 save(0);
 assert(last().entry.embeddings_model==='text-embedding-3-small','a typed model is sent without the spaces around it: '+JSON.stringify(last().entry.embeddings_model));

 editor(0).value='';
 save(0);
 assert(last().entry.embeddings_model==='','an emptied field is sent empty, which is what clears it: '+JSON.stringify(last().entry));
 assert('embeddings_model' in last().entry,'the field is always in the save, so the server never has to guess');

 assert(editor(1).value==='','an entry that names none shows an empty field');
});
</script>`

func TestTheProvidersCardShowsAndSendsTheEmbeddingsModel(t *testing.T) {
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
	runPageInEngines(t, providerEmbeddingsPage, modules)
}
