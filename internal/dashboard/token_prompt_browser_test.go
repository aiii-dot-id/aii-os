//go:build !windows

package dashboard

import "testing"

const tokenPromptProbePage = `<!DOCTYPE html><html><head><title>token</title></head><body>
<button id="send-btn">send</button>
<script type="module" src="/probe.js"></script>
</body></html>`

const tokenPromptProbeJS = `
import { report } from '/__harness.js';

try {
  const assert = (condition, message) => { if (!condition) throw new Error(message); };
  const nativeFetch = window.fetch.bind(window);
  const calls = [];
  let authorized = false;
  window.fetch = async (url, options = {}) => {
    if (url !== '/auth/token') return nativeFetch(url, options);
    const method = options.method || 'GET';
    calls.push({ method, body: options.body || '' });
    if (method === 'GET') return { status: authorized ? 204 : 401, ok: authorized };
    if (options.body === 'right-token') { authorized = true; return { status: 204, ok: true }; }
    if (options.body === 'code-seen') return { status: 403, ok: false, text: async () => 'the host said why\n' };
    return { status: 401, ok: false, text: async () => 'words that are not for the person' };
  };

  let asks = 0;
  window.prompt = () => { asks++; throw Error('modal authentication is forbidden'); };

  const sockets = [];
  class ControlledSocket {
    constructor(url) { this.url = url; this.readyState = 0; sockets.push(this); }
    send() {}
    open() { this.readyState = 1; if (this.onopen) this.onopen({}); }
    fail() { this.readyState = 3; if (this.onclose) this.onclose({}); }
  }
  ControlledSocket.CONNECTING = 0; ControlledSocket.OPEN = 1;
  ControlledSocket.CLOSING = 2; ControlledSocket.CLOSED = 3;
  window.WebSocket = ControlledSocket;

  const waitFor = async (predicate, label) => {
    const deadline = performance.now() + 5000;
    while (!predicate()) {
      if (performance.now() >= deadline) throw new Error(label + ' timed out');
      await new Promise(resolve => setTimeout(resolve, 5));
    }
  };

  const { connect } = await import('/ws.js');
  connect();
  sockets[0].fail();
  await waitFor(() => document.querySelector('#dashboard-signin input'), 'inline sign-in');
  const form=document.querySelector('#dashboard-signin form'), input=form.querySelector('input');
  assert(document.getElementById('dashboard-signin').textContent.includes('aii dashboard-token'), 'recovery route missing');

  const show=form.querySelector('[data-signin-show]');
  const type=text=>{ input.value=text; input.dispatchEvent(new Event('input',{bubbles:true})); };
  assert(input.type==='text' && show.hidden, 'an empty field is masked, or Show is offered with nothing to show');
  type('abcd-efgh jkmn'); assert(input.type==='text' && show.hidden, 'a twelve-character code typed with its hyphens and in small letters was masked');
  type('oil0-1234-5678'); assert(input.type==='text', 'O, I and L are read as the digits they are typed for, and the field masked them');
  type('ABCDEFGHJKMNP'); assert(input.type==='password' && !show.hidden, 'thirteen characters cannot be a generated code and were shown');
  type('ABCD-EFGH-JKM!'); assert(input.type==='password', 'a character no code carries was shown');
  type('ABCD-EFGH-JKMU'); assert(input.type==='password', 'U is in no code and was shown');
  assert(show.textContent==='Show' && show.getAttribute('aria-pressed')==='false', 'Show does not say what it does');
  show.click(); assert(input.type==='text' && show.textContent==='Hide' && show.getAttribute('aria-pressed')==='true', 'Show did not show what was typed');
  show.click(); assert(input.type==='password', 'Hide did not mask it again');
  show.click(); type('ABCD'); type('a long token, typed after Show was pressed for another one');
  assert(input.type==='password', 'Show stayed pressed across a value that was cleared: the next token was shown');
  type('');

  input.value='wrong-token'; form.dispatchEvent(new Event('submit',{cancelable:true}));
  assert(input.value==='' && input.type==='text' && show.hidden, 'secret remained in the form after submission, or the emptied field stayed masked');
  await waitFor(() => document.getElementById('dashboard-signin').textContent.includes('not accepted'), 'typed refusal');
  assert(!document.getElementById('dashboard-signin').textContent.includes('not for the person'), 'a refused credential was answered with the host\'s plain refusal text');
  input.value='code-seen'; form.dispatchEvent(new Event('submit',{cancelable:true}));
  await waitFor(() => document.getElementById('dashboard-signin').textContent.includes('That was not accepted: the host said why.'), 'the host\'s reason for a refusal');
  input.value='right-token'; form.dispatchEvent(new Event('submit',{cancelable:true}));
  await waitFor(() => authorized && sockets.length === 2, 'login and reconnect');
  assert(asks===0 && !document.getElementById('dashboard-signin'), 'modal prompt or stale inline form');
  assert(calls.length >= 4 && calls[0].method === 'GET' && calls[1].body === 'wrong-token' && calls[2].body === 'code-seen' && calls[3].body === 'right-token',
    'login did not pass through the server route: ' + JSON.stringify(calls));
  assert(!document.cookie.includes('aii_token'), 'page JavaScript wrote the access cookie');

  sockets[1].open();
  sockets[1].fail();
  await waitFor(() => calls.filter(c => c.method === 'GET').length === 2, 'post-connect auth status');
  assert(asks === 0 && !document.getElementById('dashboard-signin'), 'transport close requested a token despite valid authentication');
  report('OK');
} catch (e) { report('FAIL: ' + ((e && e.message) || String(e))); }
`

func TestTokenLoginAndReconnectUnderTheShippingCSP(t *testing.T) {
	runTokenPromptProbe(t, tokenPromptProbeJS)
}

func TestInlineAuthenticationCancelKeepsTheFormQuietAndRevalidatesLateSuccess(t *testing.T) {
	runTokenPromptProbe(t, `
import {report} from '/__harness.js';
try {
 const assert=(ok,why)=>{if(!ok)throw Error(why)};
 let resolvePost, authorized=false, posts=0, socket;
 const nativeFetch=window.fetch.bind(window);
 window.fetch=async(url,options={})=>{
  if(url!=='/auth/token')return nativeFetch(url,options);
  if(options.method!=='POST')return {status:authorized?204:401};
  posts++;return new Promise(resolve=>{resolvePost=resolve;});
 };
 window.prompt=()=>{throw Error('modal prompt used')};
 window.WebSocket=class{constructor(){this.readyState=0;socket=this;}send(){} };
 const {recoverAuthentication}=await import('/ws.js');
 await recoverAuthentication();
 const box=document.getElementById('dashboard-signin'), form=box.querySelector('form'), input=box.querySelector('input');
 input.value='fixture-token';form.dispatchEvent(new Event('submit',{cancelable:true}));
 assert(posts===1 && input.value==='','secret was retained or submit failed');
 form.querySelector('button[type="button"]').click();
 assert(form.hidden && box.textContent.includes('paused'),'cancel did not dismiss the form');
 authorized=true;resolvePost({status:204,ok:true});await Promise.resolve();await Promise.resolve();await Promise.resolve();
 assert(form.hidden && !box.textContent.includes('not accepted'),'late response reopened/overwrote the cancelled form');
 assert(socket,'late success did not revalidate the connection');
 socket.readyState=1;socket.onopen();assert(!document.getElementById('dashboard-signin'),'confirmed cookie left a stale sign-in form');
 authorized=false;await recoverAuthentication();
 const next=document.getElementById('dashboard-signin');next.querySelector('form button[type="button"]').click();
 await recoverAuthentication();assert(next===document.getElementById('dashboard-signin') && next.querySelector('form').hidden,'recovery repeated or reopened a dismissed form');
 report('OK');
}catch(e){report('FAIL: '+e.message)}
`)
}

func runTokenPromptProbe(t *testing.T, script string) {
	t.Helper()
	runPageInEnginesWithHeaders(t, tokenPromptProbePage, wsProbeModules(t, script),
		map[string]string{"Content-Security-Policy": uiCSP})
}

func wsProbeModules(t *testing.T, script string) map[string][]byte {
	t.Helper()
	real := func(p string) []byte {
		b, err := staticFS.ReadFile("static/" + p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		return b
	}
	modules := map[string][]byte{
		"/probe.js": []byte(script),
		"/ws.js":    real("ws.js"),
		"/state.js": real("state.js"),
		"/util.js":  real("util.js"),

		"/presence.js":       stubModule("renderPresence", "setThinking"),
		"/app.js":            stubModule("go", "renderFirstbootVisibility", "toast"),
		"/views/chat.js":     stubModule("addMsg", "attachSpeaker", "sysLine", "toolEventLive", "thinkingEvent", "renderInteractionPages", "renderHistory", "renderChatSubstrate", "renderComposer", "acceptSubstrateConfig", "rejectSubstrateConfig", "substrateConnectionLost", "renderSteering", "renderAsks"),
		"/views/home.js":     stubModule("renderHome"),
		"/views/work.js":     stubModule("renderWorkPill"),
		"/views/projects.js": stubModule("renderProjPill", "renderProjects", "newlyCreatedID", "rejectCreate", "rejectFocusSave", "rejectContractSave", "acceptCreate", "acceptFocusSave", "acceptContractSave", "projectsConnectionLost"),
		"/views/memory.js":   stubModule("renderMemory"),
		"/views/identity.js": stubModule("renderIdentity"),
		"/views/plugins.js":  stubModule("renderPlugins", "rejectPluginAction"),
		"/views/settings.js": stubModule("renderSettings", "acceptSettingsConfig", "rejectSettingsConfig", "acceptProviderSave", "rejectProviderSave", "acceptSpeechLists", "rejectSpeechLists", "acceptDashboardToken", "acceptAccessCode", "rejectAccessCode", "settingsConnectionLost"),
		"/firstboot.js":      stubModule("renderProviderOptions", "setModelOptions", "fbHint", "fbResult", "acceptDiscoveryResponse", "firstbootConnectionLost", "discoveryAnswered", "discoveryFailed"),
		"/sections.js":       stubModule("publish", "onSections", "onLayout", "onTokensChanged"),
		"/panel.js":          stubModule("renderPanel"),
		"/theme.js":          stubModule("onTheme"),
		"/overlay.js":        stubModule("onOverlayChanged", "holdReloadWhile"),
		"/voice.js":          stubModule("bindTransport", "render", "speak", "connectionLost"),
	}
	return modules
}

func TestTheScannedCodesPageSaysTheHostsReasonForARefusal(t *testing.T) {
	runPageInEnginesWithHeaders(t, `<!DOCTYPE html><html><head><title>link</title></head><body>
<p id="note">Signing in…</p><p><a id="open" href="/" hidden>Open the dashboard</a></p>
<script type="module" src="/probe.js"></script>
</body></html>`, map[string][]byte{
		"/link.js":  accessCodeLinkJS,
		"/link2.js": accessCodeLinkJS,
		"/probe.js": []byte(`
import { report } from '/__harness.js';
try {
  const assert = (ok, why) => { if (!ok) throw new Error(why); };
  const sent = [];
  window.fetch = async (url, options = {}) => {
    sent.push(url + ' ' + options.method + ' ' + options.body);
    if (options.body === 'SEEN') return { status: 403, ok: false, text: async () => 'the host said why\n' };
    return { status: 401, ok: false, text: async () => 'words that are not for the person' };
  };
  const note = document.getElementById('note');
  const waitFor = async (predicate, label) => {
    const deadline = performance.now() + 5000;
    while (!predicate()) {
      if (performance.now() >= deadline) throw new Error(label + ' timed out: ' + note.textContent);
      await new Promise(resolve => setTimeout(resolve, 5));
    }
  };
  location.hash = '#SEEN';
  await import('/link.js');
  await waitFor(() => note.textContent === 'That code was not accepted: the host said why.', 'the host\'s reason');
  assert(location.hash === '' && !document.getElementById('open').hidden, 'the code stayed in the address bar, or no way on was offered');
  location.hash = '#WRONG';
  await import('/link2.js');
  await waitFor(() => note.textContent.startsWith('That code was not accepted. A code works once'), 'the page\'s own words for a wrong code');
  assert(sent.length === 2 && sent[0] === '/auth/token POST SEEN' && sent[1] === '/auth/token POST WRONG', 'the code went somewhere other than the sign-in door: ' + JSON.stringify(sent));
  report('OK');
} catch (e) { report('FAIL: ' + ((e && e.message) || String(e))); }
`),
	}, map[string]string{"Content-Security-Policy": uiCSP})
}
