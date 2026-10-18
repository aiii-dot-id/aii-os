//go:build !windows

package dashboard

import "testing"

func TestHeldMailRepairBrowserWaitsForMatchingReceipt(t *testing.T) {
	page := `<!doctype html><div id="panel"></div><script type="module">
import { S } from './state.js';
import { frames } from './ws.js';
import { mailRepairHTML,wireMailRepair,requestHeldMail,acceptMailRepair,rejectMailRepair,mailRepairDisconnected } from './views/mail-repair.js';
import { assert,run } from './__harness.js';
const panel=document.getElementById('panel');
S.renderMailRepair=()=>{panel.innerHTML=mailRepairHTML();wireMailRepair(panel);};
const last=()=>frames[frames.length-1];
const state={messages:[{id:'held',recipient:'old',channel:'old-channel',reason:'unresolved',preview:'<img src=x onerror=bad()>',truncated:true,sha256:'observed'}],routes:[{name:'quinn',channel:'signal'}]};
run(async()=>{
 requestHeldMail();const read=last();
 assert(!acceptMailRepair({request_id:'unrelated',mail_repair:state}),'unrelated receipt accepted');
 acceptMailRepair({request_id:read.request_id,mail_repair:state});
 assert(!panel.querySelector('img'),'untrusted message became executable HTML');
 panel.querySelector('[data-mail-repair]').click();
 assert(frames.length===1,'missing route sent a correction');
 panel.querySelector('[data-mail-inspect]').click();
 assert(last().type==='inspect_mail','complete text has no operation');
 acceptMailRepair({request_id:last().request_id,mail_repair:{...state,messages:[{...state.messages[0],preview:'full text',truncated:false}]}});
 assert(panel.textContent.includes('full text'),'complete text absent');
 panel.querySelector('select').value='0';panel.querySelector('[data-mail-repair]').click();
 assert(last().mail_repair.sha256==='observed'&&last().mail_repair.recipient==='quinn'&&last().mail_repair.channel==='signal','correction not bound to selected route and observed message');
 assert(panel.querySelector('[data-mail-repair]').disabled&&!panel.textContent.includes('Correction saved'),'success before acknowledgment');
 const before=frames.length;mailRepairDisconnected();
 assert(frames.length===before&&panel.textContent.includes('nothing is automatically retried'),'disconnect retried or hid uncertainty');
 requestHeldMail();acceptMailRepair({request_id:last().request_id,mail_repair:state});
 panel.querySelector('select').value='0';panel.querySelector('[data-mail-repair]').click();
 rejectMailRepair(last().request_id,'stale view; refresh');
 assert(panel.textContent.includes('stale view')&&!panel.textContent.includes('Correction saved'),'refusal rendered as success');
 requestHeldMail();acceptMailRepair({request_id:last().request_id,mail_repair:{messages:null,routes:null,queued:'held'}});
 assert(panel.textContent.includes('Delivery is not yet confirmed'),'queue receipt claimed delivery');
});</script>`
	modules := map[string][]byte{"/state.js": []byte(`export const S={};`), "/ws.js": []byte(`export const frames=[];export function send(f){const request_id='r'+(frames.length+1);frames.push({...f,request_id});return request_id;}`)}
	for _, file := range []string{"views/mail-repair.js", "pending.js", "util.js"} {
		raw, err := staticFS.ReadFile("static/" + file)
		if err != nil {
			t.Fatal(err)
		}
		modules["/"+file] = raw
	}
	runPageInEngines(t, page, modules)
}
