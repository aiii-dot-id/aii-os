//go:build !windows

package dashboard

import (
	"strings"
	"testing"
)

func TestSpeakerAttributionRendersInBrowser(t *testing.T) {
	page := `<!doctype html>
<div id="thread"><div id="thread-inner"></div></div>
<textarea id="msg-input"></textarea><button id="send-btn"></button>
<script type="module">
import { renderInteractionPages } from './views/chat.js';

function renderHistory(history) { renderInteractionPages(new Map([['recorded',{rows:history.map((r,i)=>({id:'fixture-'+i,sequence:String(i+1),kind:'legacy',details:{},...r}))}],['transient',{rows:[]}]]),()=>{},false); }
import { assert, run } from './__harness.js';
run(() => {
  const history=[];
  const publish=(id,content,sequence,speaker)=>{
    let r=history.find(r=>r.id===id);
    if(!r){r={id,role:'operator',content,annotations:{}};history.push(r);}
    if(sequence)r.annotations.voice={session:'vs-1',sequence};
    if(speaker)r.annotations.speaker=speaker;
    renderHistory(history);
    return document.querySelector('[data-turn-id="'+id+'"]');
  };
  const el=publish('spoken','[voice] hello there',7,null);
  assert(el.dataset.voiceRef==='vs-1/7' && el.querySelector('.who').textContent==='you','recorded voice reference lost');
  publish('spoken','[voice] hello there',7,{decision:'known',speaker:'Quinn',speaker_id:'quinn-one'});
  const other=publish('other','[voice] another person',9,{decision:'known',speaker:'Quinn',speaker_id:'quinn-two'});
  assert(el.querySelector('.who').textContent==='you · Quinn (speaker_id="quinn-one")' && other.querySelector('.who').textContent==='you · Quinn (speaker_id="quinn-two")','same-name speakers lost their distinct references');
  publish('spoken','[voice] hello there',7,{decision:'uncertain',speaker:'Quinn',score:0.60});
  assert(el.querySelector('.who').textContent==='you · uncertain: Quinn 0.60','late recorded uncertainty lost');
  assert(!el.querySelector('.who').textContent.includes('speaker_id='),'uncertain result retained a previous known ID');
  assert(!publish('typed','typed words',0,null).dataset.voiceRef,'typed words acquired a voice identity');
  publish('spoken','[voice] hello there',7,{decision:'unknown',reason:'unknown speaker'});
  assert(el.querySelector('.who').textContent==='you · unknown speaker','recorded unknown speaker lost');

});
</script>`
	modules := map[string][]byte{}
	for _, path := range []string{"static/views/chat.js"} {
		data, err := staticFS.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		modules["/"+strings.TrimPrefix(path, "static/")] = data
	}
	modules["/state.js"] = []byte(`export const S = { stats: { name: 'TestIdentity' }, providers: [], config: null, providersLoaded: false };`)
	modules["/util.js"] = utilStub(`export const $ = id => document.getElementById(id); export const esc = v => String(v ?? '').replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/"/g,'&quot;'); export const hueOf = () => 0; export const copyText = async () => true;`)
	modules["/ws.js"] = []byte(`export const frames = []; export function send(f) { frames.push(f); return 'req-1'; } export function wsReady() { return true; } export function query(n, e) { return send(Object.assign({ type: 'query', query: n }, e || {})); }`)
	modules["/presence.js"] = []byte(`export function setThinking() {} export function toolPulse() {} export function renderPresence() {}`)
	modules["/app.js"] = []byte(`export function toast() {}`)
	modules["/views/model-picker.js"] = []byte(`export function fillModelPicker() {}`)
	modules["/pending.js"] = []byte(`export function pendingSlot() { return { arm(){ return true; }, claim(){ return false; }, drop(){}, waiting(){ return false; } }; } export function anyPending() { return false; }`)
	runPageInEngines(t, page, modules)
}

func TestSpeakerTimeoutAttributionTravelsWithTheFinal(t *testing.T) {
	ws, err := staticFS.ReadFile("static/ws.js")
	if err != nil {
		t.Fatal(err)
	}
	modules := wsPageModules(ws, []byte(`export const S = { connected:false,identityExists:false,view:'home' };`))
	modules["/voice.js"] = []byte(`export function bindTransport(){return {voiceEvent:ev=>window.calls.push(['voice',ev])};}export function render(){}export function speak(){}export function connectionLost(){}`)
	runPageInEngines(t, `<!doctype html><button id="send-btn"></button><button id="mic"></button>
<script type="module">
import { assert, report } from './__harness.js';
try {
 window.calls=[];
 let socket;
 window.WebSocket=class {constructor(){socket=this;} send(){} };
 const {connect}=await import('./ws.js');const {S}=await import('./state.js');S.interactionWake=()=>window.calls.push(['read']);connect();
 socket.onmessage({data:JSON.stringify({type:'voice_event',voice_event:{type:'transcript_final',operator:true,session_id:'s',sequence:7,text:'words',attribution:'uncertain: host attribution wait expired'}})});
 assert(window.calls.length===2 && window.calls[0][0]==='read' && window.calls[1][0]==='voice','voice final did not wake history and reach its voice owner');
 const ev=window.calls[1][1];assert(ev.session_id==='s' && ev.sequence===7 && ev.attribution==='uncertain: host attribution wait expired','exact final reference and reason lost');
 report('OK');
} catch(e) {report('FAIL: '+e.message);}
</script>`, modules)
}
