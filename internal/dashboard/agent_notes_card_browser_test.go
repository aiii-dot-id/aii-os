//go:build !windows

package dashboard

import "testing"

func TestTheNotesCardCopiesOnlyWhenPressedAndSaysWhatTheHostDid(t *testing.T) {
	page := substratePageMarkup + `<script type="module">
import { S } from './state.js';
import { connect } from './ws.js';
import { renderSettings } from './views/settings.js';
import { assert, run } from './__harness.js';
run(() => {
 const sockets=[];
 window.setTimeout=()=>1;window.clearTimeout=()=>{};
 window.WebSocket=class {
   constructor(){this.readyState=0;this.frames=[];sockets.push(this);}
   send(bytes){this.frames.push(JSON.parse(bytes));}
   recv(frame){this.onmessage({data:JSON.stringify(frame)});}
 };
 S.view='settings';S.identityExists=true;S.providersLoaded=true;S.config={llm:{}};
 connect();const socket=sockets.at(-1);socket.readyState=1;socket.onopen();
 const el=id=>document.getElementById(id), one=sel=>document.querySelector(sel);
 const card=()=>one('[data-agent-notes]');
 const asked=()=>socket.frames.filter(f=>f.type==='agent_notes_copy');
 const host=(notes,id,message)=>socket.recv({type:'sandbox',request_id:id,message,sandbox:{root:'/home',extra_roots:[],off:false,agent_notes:notes}});
 renderSettings();one('[data-sec="sandbox"]').click();
 host({copied:false});
 assert(card() && el('notes-copy') && el('notes-copy').textContent==='Copy notes','the card does not offer Copy notes while no copy is there');
 for (const says of ['the notes other AI agents keep on this machine','by default Claude Code\u2019s, the Claude desktop app\u2019s and Codex\u2019s, or those of the stores set in genesis.agent_memory','the kinds the copy takes are copied, by default .md and .txt','never credentials, sessions or databases','not as their own memories','model’s provider'])
   assert(card().textContent.includes(says),'the card does not say: '+says);
 assert(card().querySelector('h3').title.length>20,'the card’s heading has no sentence for a resting pointer');
 assert(asked().length===0,'the card asked for a copy before it was pressed');

 // A refusal: the host's words, the button stays.
 el('notes-copy').click();
 assert(asked().length===1 && el('notes-copy').disabled && card().textContent.includes('Copying'),'the press did not send one request and wait');
 socket.recv({type:'error',request_id:asked()[0].request_id,message:'fixture: <b>refused</b>'});
 assert(one('[data-agent-notes-said]').textContent==='fixture: <b>refused</b>' && !card().querySelector('b') && !el('notes-copy').disabled,'a refusal is not shown in the host’s words as text, or the button did not come back');

 // Copied: the host's sentence, and the card's read of the home.
 el('notes-copy').click();
 host({copied:false},'another-page');
 assert(el('notes-copy').disabled,'an answer to another request was taken for this one');
 host({copied:true,folder:'/home/other-agents'},asked()[1].request_id,'Just now, 12 notes <i>were</i> copied');
 assert(one('[data-agent-notes-said]').textContent==='Just now, 12 notes <i>were</i> copied' && !card().querySelector('i'),'the host’s sentence is not shown as text');
 assert(!el('notes-copy') && one('[data-agent-notes-copied]').textContent.includes('/home/other-agents'),'the card does not say the copy is there');

 // The folder is deleted: the next read offers the button again.
 host({copied:false});
 assert(el('notes-copy'),'the card does not follow the host’s read of the home');

 // Lost connection while a press waits: nothing assumed, nothing retried.
 el('notes-copy').click();
 const before=socket.frames.length;
 socket.readyState=3;socket.onclose();
 assert(one('[data-agent-notes-said]').textContent.includes('It is not known whether the notes were copied') && socket.frames.length===before,'a lost connection was not said, or the press was retried');
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
