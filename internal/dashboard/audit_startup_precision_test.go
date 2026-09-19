// .
// .
// .

//go:build !windows

package dashboard

import "testing"

func TestAuditStartupCardDoesNotRoundAwayItsAllowance(t *testing.T) {
	page := `<!doctype html><div id="plugins-stack"></div><script type="module">
import { assert, run } from './__harness.js';
import { S } from './state.js';
import { renderPlugins } from './views/plugins.js';
run(() => {
 S.config={plugins:{autoload:'T3',catalog:[],installed:[{id:'org.example.precise',version:'1.0.0',tier:'T3',mode:'native',variant:'native',settings:[],startup:{effective_ms:20,requested_ms:25,ceiling_ms:20,source:'operator',capped:true}}]}};
 renderPlugins();const text=document.querySelector('.startup-note')?.textContent||'';
 assert(text.includes('20 ms')||text.includes('0.02 s'),'20ms effective allowance was rounded away: '+text);
 assert(text.includes('25 ms')||text.includes('0.025 s'),'25ms requested allowance was rounded away: '+text);
 assert(text.includes('capped'),'cap provenance lost: '+text);
});</script>`
	runPageInEngines(t, page, map[string][]byte{
		"/views/settings.js": []byte(`export function saveConfigSection(){}; export function sendConfigChanges(){}; export function configFeedbackHTML(){return ''}; export function savebarHTML(){return ''};`),
		"/ws.js":             []byte(`export function send(){}`),
	})
}
