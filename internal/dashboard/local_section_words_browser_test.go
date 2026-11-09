//go:build !windows

package dashboard

import (
	"strings"
	"testing"
)

const localSectionWordsPage = `<!doctype html><div id="settings-stack"></div>
<script type="module">
import { S } from './state.js';
import { renderSettings, localPart } from './views/settings.js';
import { frames } from './ws.js';
import { assert, run } from './__harness.js';
run(() => {
 S.config={llm:{provider:'Main',model:'m'},dashboard:{},plugins:{},witness:{},prompt:{},genesis:{},tools:{},
   agency:{prefer_local_for_roles:true,roles:[{name:'critic',provider:'Cloud',model:'big'}]}};
 S.providersLoaded=true;
 S.providers=[{name:'Old local',api_type:'openai',endpoint:'http://127.0.0.1:8081/v1',default_model:'q',models:['q'],configured_models:[],local:true,status:'ok'},
              {name:'Cloud',api_type:'openai',endpoint:'https://cloud.example/v1',default_model:'big',models:['big'],configured_models:[],has_key:true,status:'ok'}];
 renderSettings();
 S.settingsSection('providers'); S.requestSettingsSection(); renderSettings(); renderSettings();
 const part=()=>document.querySelector('[data-local]');
 assert(part(),'the local-model part is drawn in Providers');

 // 0. Drawn before any report reached it (one sent before Settings was in
 // answers nothing), the part asks for one, once, and not again once it has it.
 const asks=()=>frames.filter(f=>f.type==='query'&&f.query==='providers').length;
 assert(asks()===1,'drawn without a report, the part asked for providers '+asks()+' times, not once');
 // a review 1331 #3: before the first report the line says it is checking,
 // never that the local model isn't answering, and the first healthy
 // report replaces it.
 const lineText=()=>{const l=part().querySelector('[data-local-line]');return l?l.textContent:'(no line)';};
 assert(lineText()==='Checking your local model\u2026','F3: before any report the line says: '+lineText());
 localPart({type:'providers',request_id:'p',local_models:{marked:1,provider:'Old local',model:'q'}}); renderSettings();
 assert(asks()===1,'the part asked again once it had the report: '+asks());
 assert(lineText().startsWith('Sub-agents with a role will use Old local \u00b7 q.')&&!lineText().includes('Checking'),'F3: the first healthy report did not replace the waiting line: '+lineText());

 // 6. "1 model", never "1 models", on both of a card's lines.
 const rows=[...document.querySelectorAll('[data-prov-row]')].map(r=>r.textContent);
 assert(rows.length===2,'two cards drawn: '+rows.length);
 // No \b after "models": a card's text runs into its Manage button ("1 modelsManage").
 assert(rows.every(t=>!/\b1 models/.test(t)),'a card says "1 models": '+JSON.stringify(rows));
 assert(rows[0].includes('Local · 1 model')&&rows[1].includes('1 model'),'the cards do not say "1 model": '+JSON.stringify(rows));

 // 5. No file named; a role with its own model named.
 const line=part().querySelector('[data-local-line]');
 assert(line&&line.textContent.includes('The critic role keeps its own model.'),'the line does not name the role that keeps its model: '+(line&&line.textContent));
 assert(!part().textContent.includes('config.json'),'the local part names config.json: '+part().textContent);

 // 3. The checkbox label is drawn as Settings' other checkboxes are.
 const box=document.getElementById('local-roles-box');
 assert(box&&box.closest('label').classList.contains('settings-access__check'),'the checkbox label is not a settings-access__check: '+(box&&box.closest('label').className));

 // 4. Look and Add a provider are buttons, not quiet text, in the order used.
 const lookB=part().querySelector('[data-local-look]'), addP=document.getElementById('pv-open-new');
 for (const [b,n] of [[lookB,'Look'],[addP,'Add a provider']]) {
  assert(b&&b.tagName==='BUTTON'&&b.classList.contains('c-button')&&!b.classList.contains('c-button--quiet'),n+' is not drawn as a button: '+(b&&b.className));
 }
 assert(lookB.compareDocumentPosition(box)&Node.DOCUMENT_POSITION_FOLLOWING,'Look does not come before the checkbox');
 assert(lookB.compareDocumentPosition(addP)&Node.DOCUMENT_POSITION_FOLLOWING,'Look does not come before Add a provider');

 // 2. A found server is one line, its words in the hint style, no dot left.
 lookB.click();
 localPart({type:'local_found',request_id:'request-1',local_found:[{usual:'Ollama',port:11434,models:['m1','m2']}]}); renderSettings();
 const found=()=>part().querySelector('[data-local-found="11434"]');
 const words=found().querySelector('.settings-access__hint');
 assert(words&&words.textContent==='Ollama on port 11434','the found line\'s words: '+(words&&words.textContent));
 assert(!found().textContent.includes('·'),'the found line keeps a dot: '+found().textContent);
 assert(found().querySelector('select')&&found().querySelector('[data-local-add]'),'the found line has no model choice or Add');

 // 1. After Add, the added server is said as added; never "nothing answered".
 found().querySelector('[data-local-add]').click();
 assert(frames.findLast(f=>f.type==='local_add').local_server.port===11434,'Add did not send the port');
 localPart({type:'providers',request_id:'request-1',local_models:{marked:1,provider:'Old local',model:'q'}}); renderSettings();
 assert(!/nothing answered/i.test(part().textContent),'after Add, the part says nothing answered: '+part().textContent);
 assert(found()&&found().textContent.includes('Ollama on port 11434 is added.'),'after Add, the server is not said as added: '+(found()&&found().textContent));
 // A look that truly found nothing still says so.
 part().querySelector('[data-local-look]').click();
 localPart({type:'local_found',request_id:'request-1',local_found:[]}); renderSettings();
 assert(part().textContent.includes('Nothing answered on the usual ports.'),'an empty look no longer says nothing answered');

 // 7. a review 1331 #2, in a real menu: a second look whose list drops the
 // picked model. What the menu shows and what Add sends are one model, and
 // one the newest list offers.
 part().querySelector('[data-local-look]').click();
 localPart({type:'local_found',request_id:'request-1',local_found:[{usual:'oMLX or vLLM',port:8000,address:'http://127.0.0.1:8000/v1',models:['m1','m2']}]}); renderSettings();
 const menu=()=>part().querySelector('[data-local-pick="8000"]');
 menu().value='m2'; menu().dispatchEvent(new Event('change',{bubbles:true}));
 part().querySelector('[data-local-look]').click();
 localPart({type:'local_found',request_id:'request-1',local_found:[{usual:'oMLX or vLLM',port:8000,address:'http://127.0.0.1:8000/v1',models:['m1','m3']}]}); renderSettings();
 const shownModel=menu().options[menu().selectedIndex].textContent;
 part().querySelector('[data-local-add="8000"]').click();
 const sentModel=frames.findLast(f=>f.type==='local_add').local_server.model;
 assert(shownModel===sentModel,'F2: the menu shows '+shownModel+' but Add sent '+sentModel);
 assert(['m1','m3'].includes(sentModel),'F2: Add sent a model the newest list does not offer: '+sentModel);
});
</script>`

// THE LOCAL MODEL PART'S WORDS AND DRAWING (0.2.1 #2, host 0217, from the
// window's three shots): no false "nothing answered" after an Add, no
// dangling dot, the checkbox and the two presses drawn as Settings' own, no
// file named, and "1 model". Classes and words, not rendered sizes: the
// harness loads no style sheets, so sizes are read in the window's shots.
func TestTheLocalModelPartSaysAndDrawsWhatTheHostAsked(t *testing.T) {
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
	runPageInEngines(t, localSectionWordsPage, modules)
}
