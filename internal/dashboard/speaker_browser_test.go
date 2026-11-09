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
  assert(el.dataset.voiceRef==='vs-1/7' && el.querySelector('.who').textContent==='You','recorded voice reference lost');
  publish('spoken','[voice] hello there',7,{decision:'known',speaker:'Quinn',speaker_id:'quinn-one'});
  const other=publish('other','[voice] another person',9,{decision:'known',speaker:'Quinn',speaker_id:'quinn-two'});
  assert(el.querySelector('.who').textContent==='You · Quinn' && other.querySelector('.who').textContent==='You · Quinn','same-name speakers do not each read the name');
  assert(!publish('typed','typed words',0,null).dataset.voiceRef,'typed words acquired a voice identity');
  // The note is the words before the engine's bookkeeping, for every form it has.
  for (const [speaker,note] of [
    [{reason:'speaker_profile_pending',decision:'uncertain',continuity:'pending'},'speaker identification pending'],
    [{speaker_uuid:'u-1',display_label:'Same label',continuity:'matched',registry_revision:'9'},'"Same label"'],
    [{speaker_uuid:'u-1',continuity:'new_profile',registry_revision:'9'},'anonymous speaker'],
    [{decision:'known',speaker:'Quinn',speaker_id:'quinn-one'},'Quinn'],
    [{decision:'known',speaker_id:'quinn-one'},'unknown speaker'],
    [{decision:'uncertain',speaker:'Quinn',score:0.60},'uncertain: Quinn'],
    [{decision:'uncertain',reason:'too_short'},'uncertain: too short'],
    [{decision:'uncertain',reason:'enrollment_unavailable'},'speaker enrollment unavailable'],
    [{decision:'uncertain',reason:'no_enrollments'},'no speaker is enrolled'],
    [{decision:'unknown',reason:'unknown speaker'},'unknown speaker'],
  ]) {
    publish('spoken','[voice] hello there',7,speaker);
    assert(el.querySelector('.who').textContent==='You · '+note,'the note for '+JSON.stringify(speaker)+' reads '+el.querySelector('.who').textContent);
  }
  // A row kept across a redraw follows its role: what it is drawn as, and whether it copies.
  const row=history.find(r=>r.id==='spoken'), msg=el.querySelector('.record-message');
  const reads=(role,who,copies)=>{
    row.role=role; renderHistory(history);
    assert(document.querySelector('[data-turn-id="spoken"]')===el,'the row lost its element');
    assert(msg.classList.contains(role) && msg.classList.contains(role==='operator'?'operator':'participant') && !msg.classList.contains(role==='operator'?'participant':'operator'),'the row as '+role+' has the class '+msg.className);
    assert(msg.querySelector('.who').textContent.startsWith(who),'the row as '+role+' says '+msg.querySelector('.who').textContent);
    const copy=msg.querySelector('.copyb');
    assert(!!copy===copies && (!copy || copy.dataset.copyText==='[voice] hello there'),'the row as '+role+' copies '+(copy&&copy.dataset.copyText));
    assert(!copy || msg.lastElementChild===copy.parentElement,'the row as '+role+' has its actions out of place');
  };
  reads('participant','participant',false);
  reads('operator','You',true);
  reads('participant','participant',false);
  reads('operator','You',true);

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
import { assert, report, until } from './__harness.js';
try {
 window.calls=[];
 let socket;
 window.WebSocket=class {constructor(){socket=this;} send(){} };
 const {connect}=await import('./ws.js');const {S}=await import('./state.js');S.interactionWake=()=>window.calls.push(['read']);connect();
 socket.onmessage({data:JSON.stringify({type:'voice_event',voice_event:{type:'transcript_final',operator:true,session_id:'s',sequence:7,text:'words',attribution:'uncertain: host attribution wait expired'}})});
 // The voice is fetched once the shell has started (step 22): the event
 // waits for it, and reaches it as it arrives.
 await until(()=>window.calls.length===2,3000);
 assert(window.calls.length===2 && window.calls[0][0]==='read' && window.calls[1][0]==='voice','voice final did not wake history and reach its voice owner');
 const ev=window.calls[1][1];assert(ev.session_id==='s' && ev.sequence===7 && ev.attribution==='uncertain: host attribution wait expired','exact final reference and reason lost');
 report('OK');
} catch(e) {report('FAIL: '+e.message);}
</script>`, modules)
}
