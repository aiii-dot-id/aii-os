//go:build !windows

// Runs only through the unix browser-engine rig (browserengine_test.go,
// same tag) — this file calls its helpers, so it shares its build row.

package dashboard

import "testing"

const typedRefusalPage = substratePageMarkup + `<script type="module">
import { S } from './state.js';
import { connect } from './ws.js';
import { sendChat } from './views/chat.js';
import { assert, run } from './__harness.js';
run(() => {
 const sockets=[];
 window.setTimeout=()=>1;window.clearTimeout=()=>{};
 window.WebSocket=class {
   constructor(){this.readyState=0;this.frames=[];sockets.push(this);}
   send(bytes){this.frames.push(JSON.parse(bytes));}
   recv(frame){this.onmessage({data:JSON.stringify(frame)});}
 };
 S.view='chat';S.identityExists=true;S.stats={name:'Fixture'};
 connect();const socket=sockets.at(-1);socket.readyState=1;socket.onopen();
 socket.recv({type:'response',message:'',stream:true});
 assert(window.__think===true,'rig: the running turn did not set the thinking state');

 assert(sendChat('and also this'),'rig: the typed message was not sent');
 const typed=socket.frames.filter(f=>f.type==='chat').at(-1);
 assert(typed && typed.request_id,'rig: the typed message carried no request id');
 window.__think='running';
 const why='fixture: eight messages already wait in the turn';
 socket.recv({type:'chat_receipt',request_id:typed.request_id,chat_recording:{error:{code:'CHAT_NOT_RECORDED',message:why}}});
 socket.recv({type:'error',request_id:typed.request_id,message:why});
 socket.recv({type:'chat_ack',request_id:typed.request_id,message:'refused'});
 const pending=document.querySelector('.chat-pending[data-request-id="'+typed.request_id+'"]');
 assert(pending && pending.querySelector('summary').textContent.includes(why),'the reason is not said beside the message: '+(pending&&pending.querySelector('summary').textContent));
 assert(pending.querySelector('.body').textContent==='and also this','the refused message\'s text was not kept');
 assert(window.__think==='running','A REFUSED TYPED MESSAGE ENDED THE RUNNING TURN\'S THINKING STATE');
 assert((window.__toasts||[]).length===0,'A REFUSED TYPED MESSAGE WAS SAID A SECOND TIME, AS A TOAST: '+JSON.stringify(window.__toasts));

 // With no receipt before it, the admission's answer carries the reason.
 assert(sendChat('one more'),'rig: the second message was not sent');
 const second=socket.frames.filter(f=>f.type==='chat').at(-1);window.__think='running';
 socket.recv({type:'error',request_id:second.request_id,message:'fixture: the identity is stopping'});
 socket.recv({type:'chat_ack',request_id:second.request_id,message:'refused'});
 const p2=document.querySelector('.chat-pending[data-request-id="'+second.request_id+'"] summary').textContent;
 assert(p2==='Not accepted: fixture: the identity is stopping — your text is kept below' && window.__think==='running' && (window.__toasts||[]).length===0,'a refusal with no receipt before it was not said beside its message: '+p2);

 // The turn's own error still ends the thinking state.
 socket.recv({type:'error',message:'identity error: fixture'});
 assert(window.__think===false,'the turn\'s own error no longer ended the thinking state');
});
</script>`

func TestARefusedTypedMessageIsSaidBesideItAndLeavesTheTurnAlone(t *testing.T) {
	ws, err := staticFS.ReadFile("static/ws.js")
	if err != nil {
		t.Fatal(err)
	}
	modules := wsPageModules(ws, []byte(`export const S={providers:[]};`))
	for _, file := range []string{"views/chat.js", "util.js"} {
		data, err := staticFS.ReadFile("static/" + file)
		if err != nil {
			t.Fatal(err)
		}
		modules["/"+file] = data
	}
	modules["/presence.js"] = []byte(`import { S } from './state.js'; export const renderPresence = () => {}; export const toolPulse = () => {}; export const setThinking = on => { S.thinking = on; window.__think = on; };`)
	modules["/app.js"] = []byte(`export const sysLine = () => {}; export const toast = t => { (window.__toasts ||= []).push(t); }; export const go = () => {}; export const renderFirstbootVisibility = () => {};`)
	runPageInEngines(t, typedRefusalPage, modules)
}
