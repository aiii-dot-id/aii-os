//go:build !windows

package dashboard

import (
	"strings"
	"testing"
)

const cacheControlsPage = `<!doctype html><div id="settings-stack"></div>
<script type="module">
import { S } from './state.js';
import { renderSettings } from './views/settings.js';
import { frames } from './ws.js';
import { assert, run } from './__harness.js';
run(() => {
 S.config={llm:{provider:'Claude',model:'m'},dashboard:{},plugins:{},witness:{},prompt:{},agency:{},genesis:{},tools:{}};
 S.providersLoaded=true;
 S.providers=[{name:'Claude',api_type:'anthropic',endpoint:'https://example.test',default_model:'m',configured_models:[],cache_modes:['auto','explicit','off'],cache_ttls:['5m','1h'],cache_diagnostics:true,cache:{ttl:'1h',tail_ttl:'5m',diagnostics:true}}];
 renderSettings();
 document.querySelector('[data-sec="providers"]').click();
 document.querySelector('[data-prov-row="0"]').click();
 assert(document.getElementById('pv-cache-ttl-0').value==='1h','stored retention must render');
 assert(document.getElementById('pv-cache-diag-0').checked,'diagnostics must render');
 document.getElementById('pv-cache-mode-0').value='explicit';
 document.getElementById('pv-cache-ttl-0').value='5m';
 document.getElementById('pv-cache-tail-0').value='';
 document.getElementById('pv-cache-diag-0').checked=false;
 document.querySelector('[data-prov-commit="0"]').click();
 const frame=frames.findLast(f=>f.type==='provider_set');
 assert(frame && frame.entry.cache.mode==='explicit','mode did not reach update');
 assert(frame.entry.cache.ttl==='5m' && frame.entry.cache.tail_ttl==='','retention/clear did not reach update');
 assert(frame.entry.cache.diagnostics===false,'diagnostic clear did not reach update');
});
</script>`

func TestCacheControlsRenderAndSubmit(t *testing.T) {
	modules := map[string][]byte{}
	for _, path := range []string{"static/views/settings.js", "static/views/model-picker.js"} {
		data, err := staticFS.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		modules["/"+strings.TrimPrefix(path, "static/")] = data
	}
	modules["/state.js"] = []byte(`export const S={providers:[],config:null,providersLoaded:false};`)
	modules["/util.js"] = []byte(`export const $=id=>document.getElementById(id);export const esc=v=>String(v??'');export const hueOf=()=>0;export const copyText=async()=>true;`)
	modules["/ws.js"] = []byte(`export const frames=[];export function send(f){frames.push(f);return 'request-1';}export function query(n,e){return send({type:'query',query:n,...e});}`)
	modules["/pending.js"] = []byte(`export function pendingSlot(){return {arm(){return true;},claim(){return false;},drop(){},waiting(){return false;}};}`)
	modules["/sandbox.js"] = []byte(`export function sandboxCardHTML(){return '';}export function wireSandboxCard(){}`)
	runPageInEngines(t, cacheControlsPage, modules)
}
