//go:build !windows

package dashboard

import "testing"

func TestTheSandboxSwitchWarnsBeforeItAsksAndShowsOnlyWhatTheHostConfirmed(t *testing.T) {
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
 const words=()=>el('settings-stack').textContent;
 const asked=()=>socket.frames.filter(f=>f.type==='sandbox_switch');
 const box=()=>el('sbx-off');
 const tick=()=>{ assert(!box().disabled,'the box was clicked while disabled'); box().checked=!box().checked; box().dispatchEvent(new Event('change',{bubbles:true})); };
 const host=(off,id)=>socket.recv({type:'sandbox',request_id:id,sandbox:{root:'/home',extra_roots:['/project'],off}});
 renderSettings();one('[data-sec="sandbox"]').click();
 host(false);
 assert(box() && !box().checked && !box().disabled && !one('[data-sandbox-warning]') && !one('[data-sandbox-off]'),'a sandbox that is on shows an unticked box and no warning');

 tick();
 assert(asked().length===0,'ticking the box sent a request');
 assert(el('sbx-off-allow').textContent==='Allow' && el('sbx-off-cancel').textContent==='Cancel','the warning has exactly Allow and Cancel');
 assert(!box().checked && box().disabled,'the box shows off before the host was even asked');
 const warning=one('[data-sandbox-warning]').textContent;
 for (const says of ['read, change and delete any file','your own documents','their own record, keys, database, config and data directory','every other identity on this account, whole','own update and start-up files and the keys it borrows','Their tools still refuse these, with the sandbox off as with it on','A plugin you granted files still reaches only their home','until you untick this box, through restarts too'])
   assert(warning.includes(says),'the warning does not say: '+says);
 const paragraphs=[...one('[data-sandbox-warning] [data-announce]').children].map(d=>d.textContent);
 assert(paragraphs[0]==='Turn the sandbox off?' && paragraphs[1]==="With the sandbox off they are no longer kept to their home and the folders listed here. Their tools, the shell included, can read, change and delete any file this computer's account can: your own documents, and the keys and sign-ins other programs have saved.",'the first paragraph is not as approved: '+paragraphs.slice(0,2).join(' | '));
 assert(paragraphs[2].endsWith("(/proc). These are checks their tools make on each path. With the sandbox off an identity that sets out to get around them can, for example by changing what this account runs at its next login. Turn it off only for an identity you trust with this account."),'the warning omits required sandbox limitations: '+paragraphs[2]);
 for (const never of ['That wall does not move','stays closed','nothing more'])
   assert(!warning.includes(never),'the warning still says: '+never);

 el('sbx-off-cancel').click();
 assert(asked().length===0 && !one('[data-sandbox-warning]') && !box().checked && !box().disabled,'Cancel must close the warning, send nothing and leave the box unticked');

 tick();el('sbx-off-allow').click();
 assert(asked().length===1 && asked()[0].sandbox_off===true,'Allow must send exactly one request to turn the sandbox off');
 assert(!box().checked && box().disabled && !one('[data-sandbox-warning]') && words().includes('Waiting for the runtime to confirm'),'while the host has not answered the box is not ticked');
 host(false,'another-page');
 assert(!box().checked && box().disabled,'an answer to another request confirmed this one');
 host(true,asked()[0].request_id);
 assert(box().checked && !box().disabled,'the host confirmed off and the box does not show it');
 assert(one('[data-sandbox-off]') && words().includes('The sandbox is off.'),'while it is off the card says so');
 assert(one('[data-roots-idle]') && words().includes('/project') && !words().includes('their world is their home tree'),'while it is off the roots are shown as not limiting them');

 tick();
 assert(asked().length===2 && asked()[1].sandbox_off===false && !one('[data-sandbox-warning]'),'unticking must ask at once, with no warning');
 assert(box().checked && box().disabled,'the box shows on before the host confirmed it');
 socket.recv({type:'error',request_id:asked()[1].request_id,message:'fixture: not saved'});
 assert(words().includes('fixture: not saved The sandbox is off.') && box().checked && !box().disabled,'a refused switch must leave the box on what the host holds, and say it');
 const reread=socket.frames.at(-1);
 assert(reread.type==='query' && reread.query==='sandbox','after a refusal the page must read the state again');
 host(true,reread.request_id);
 tick();host(false,asked().at(-1).request_id);
 assert(asked().length===3 && !box().checked && !one('[data-sandbox-off]') && words().includes('The sandbox is on.'),'the host confirmed on and the card does not show it');

 tick();el('sbx-off-allow').click();
 socket.recv({type:'error',request_id:asked().at(-1).request_id,message:'fixture: config could not be saved'});
 assert(asked().length===4 && words().includes('fixture: config could not be saved The sandbox is still on.') && !box().checked && !one('[data-sandbox-off]'),'a failed save must leave the box unticked and say the sandbox is still on');

 tick();assert(one('[data-sandbox-warning]'),'the warning did not open');
 host(true);
 assert(box().checked && !one('[data-sandbox-warning]') && one('[data-sandbox-off]'),'a state another page changed must be shown');
 host(false);
 assert(!box().checked && !one('[data-sandbox-off]'),'a state another page changed back must be shown');
 socket.recv({type:'sandbox',sandbox:{root:'/home',extra_roots:['/project'],off:false,held:'words with no code'}});
 assert(!one('[data-sandbox-held]'),'the held line is drawn by the code the host sends, not by its words');
 socket.recv({type:'sandbox',sandbox:{root:'/home',extra_roots:['/project'],off:false,held_why:'SIBLINGS_UNLISTED',held:'The saved setting is off, but the sandbox stays on: fixture reason'}});
 assert(!box().checked && !box().disabled && one('[data-sandbox-held]') && one('[data-sandbox-held]').textContent==='The saved setting is off, but the sandbox stays on: fixture reason','a sandbox held on must say why');
 socket.recv({type:'sandbox',sandbox:{root:'/home',extra_roots:['/project'],off:false,held_why:'SAFE',held:'SAFE holds: fixture'}});
 assert(!box().checked && box().disabled && one('[data-sandbox-held]').textContent==='SAFE holds: fixture','a sandbox SAFE holds on must not offer the box');
 socket.recv({type:'sandbox',sandbox:{root:'/home',extra_roots:['/project'],off:false,held_why:'ACCESS_TOKEN_OFF',held:'The sandbox stays on: fixture token line.'}});
 assert(!box().checked && box().disabled && !one('[data-sandbox-warning]') && one('[data-sandbox-held]').textContent==='The sandbox stays on: fixture token line.','with no access token asked for the box must not be offered');
 host(false);
 assert(!box().disabled && !one('[data-sandbox-held]'),'a setting in force shows no held line and offers the box');
 const unkept={root:'/home',extra_roots:['/project'],not_kept:['/project/units/aii-os@*','/project/<b>slot']};
 const names='Not kept, because a folder you listed holds it: /project/units/aii-os@*; /project/<b>slot';
 assert(!one('[data-sandbox-not-kept]'),'nothing is named while no listed folder holds a kept place');
 socket.recv({type:'sandbox',sandbox:{...unkept,off:false}});
 assert(!one('[data-sandbox-not-kept]'),'with the sandbox on and no warning open nothing is named');
 tick();
 assert(one('[data-sandbox-warning] [data-sandbox-not-kept]').textContent===names,'the warning must name what a listed folder leaves unkept, as text');
 el('sbx-off-cancel').click();
 socket.recv({type:'sandbox',sandbox:{...unkept,off:true}});
 assert(one('[data-sandbox-off] [data-sandbox-not-kept]').textContent===names,'while off the card must name what a listed folder leaves unkept');
 host(false);
 assert(!one('[data-sandbox-not-kept]'),'a state that names nothing shows nothing');

 tick();el('sbx-off-allow').click();
 const before=socket.frames.length;
 socket.readyState=3;socket.onclose();
 assert(words().includes('It is not known whether the sandbox was changed') && !box().checked && !box().disabled,'a lost connection must not tick the box');
 assert(socket.frames.length===before,'a lost connection replayed the request');
 tick();el('sbx-off-allow').click();
 assert(socket.frames.length===before && words().includes('Not connected. The sandbox was not changed.') && !box().checked,'Allow while not connected must send nothing and say so');
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
