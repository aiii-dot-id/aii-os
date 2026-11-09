//go:build !windows

package dashboard

import "testing"

const tokenPromptProbePage = `<!DOCTYPE html><html><head><title>token</title></head><body>
<button id="send-btn">send</button>
<script type="module" src="/probe.js"></script>
</body></html>`

// The HTTP handler test proves that a successful POST installs an HttpOnly
// cookie. This browser half runs the shipped ws.js and proves the page uses
// that route: a refusal is asked twice before the form (step 17), a refused
// token retries, success reconnects, page code writes no token cookie, and a
// later transport close does not ask again while the server says the cookie
// is valid.
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
    if (method === 'GET') return { status: authorized ? 204 : 401, ok: authorized, headers: new Headers() };
    if (options.body === 'right-token') { authorized = true; return { status: 204, ok: true, headers: new Headers() }; }
    if (options.body === 'code-seen') return { status: 403, ok: false, text: async () => 'the host said why\n', headers: new Headers() };
    return { status: 401, ok: false, text: async () => 'words that are not for the person', headers: new Headers() };
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
  assert(document.getElementById('dashboard-signin').textContent.includes('Open the AII OS app on this computer') && !/token/i.test(document.getElementById('dashboard-signin').textContent), 'the form does not send the person to the app, or asks for a token');

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
  assert(calls.length >= 5 && calls[0].method === 'GET' && calls[1].method === 'GET' && calls[2].body === 'wrong-token' && calls[3].body === 'code-seen' && calls[4].body === 'right-token',
    'login did not pass through the server route: ' + JSON.stringify(calls));
  assert(!document.cookie.includes('aii_token'), 'page JavaScript wrote the access cookie');

  sockets[1].open();
  sockets[1].fail();
  await waitFor(() => calls.filter(c => c.method === 'GET').length === 3, 'post-connect auth status');
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
  if(options.method!=='POST')return {status:authorized?204:401,headers:new Headers()};
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
 // The answer passes through the page key's keeper (pagekey.js signIn) on its way: a few turns, not a timer.
 authorized=true;resolvePost({status:204,ok:true, headers: new Headers()});for(let i=0;i<10;i++)await Promise.resolve();
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

// A SIGN-IN IS ASKED FOR ONLY AFTER THE REFUSAL IS REVALIDATED (step 17:
// a stale refusal once asked a signed-in browser to sign in). The shipped
// ws.js, the door answered by hand, in every engine:
//   - a 401 answered after a reconnect opened asks nothing, and the door is
//     not asked again;
//   - a 401 answered after another tab signed in (the page key it wrote)
//     asks nothing, and the page reconnects;
//   - a 401 the second ask contradicts asks nothing;
//   - a genuinely missing cookie asks, after the door refuses twice, and
//     the form says which half was not sent and what to do; another tab's
//     sign-in then takes the form away and reconnects;
//   - a genuinely missing page key asks, and says so.
func TestASignInIsAskedOnlyAfterTheRefusalIsRevalidated(t *testing.T) {
	runTokenPromptProbe(t, `
import {report} from '/__harness.js';
try {
 const assert=(ok,why)=>{if(!ok)throw Error(why)};
 const nativeFetch=window.fetch.bind(window);
 const asks=[];
 window.fetch=(url,options={})=>url==='/auth/token'?new Promise(resolve=>asks.push(resolve)):nativeFetch(url,options);
 const refused=missing=>({status:401,ok:false,headers:new Headers({'Aii-Missing':missing})});
 const admitted={status:204,ok:true,headers:new Headers()};
 const sockets=[];
 window.WebSocket=class{constructor(){this.readyState=0;sockets.push(this);}send(){}open(){this.readyState=1;this.onopen&&this.onopen({});}fail(){this.readyState=3;this.onclose&&this.onclose({});}};
 Object.assign(window.WebSocket,{CONNECTING:0,OPEN:1,CLOSING:2,CLOSED:3});
 const turns=async()=>{for(let i=0;i<30;i++)await Promise.resolve();};
 const form=()=>document.getElementById('dashboard-signin');
 const words=()=>(form()&&form().querySelector('p')||{}).textContent||'';
 const anotherTab=()=>window.dispatchEvent(new StorageEvent('storage',{key:'aii.signin',newValue:'a key another tab was given'}));
 const {connect}=await import('/ws.js');
 connect();

 // A delayed 401 beside a successful reconnect.
 sockets.at(-1).fail();
 assert(asks.length===1,'a lost connection did not ask the door');
 connect();sockets.at(-1).open();
 asks[0](refused('cookie'));await turns();
 assert(!form() && asks.length===1,'a 401 answered after a reconnect opened asked for a sign-in, or asked the door again');

 // Another tab signs in while the door is asked.
 sockets.at(-1).fail();
 assert(asks.length===2,'the second loss did not ask the door');
 const before=sockets.length;
 anotherTab();
 asks[1](refused('cookie'));await turns();
 assert(!form() && asks.length===2,'a 401 answered after another tab signed in asked for a sign-in');
 assert(sockets.length>before,'the page did not reconnect on another tab\'s sign-in');

 // A 401 the second ask contradicts.
 sockets.at(-1).fail();
 asks[2](refused('cookie'));await turns();
 assert(asks.length===4 && !form(),'a refusal was not asked again before the person was');
 asks[3](admitted);await turns();
 assert(!form(),'a refusal the second ask contradicted asked for a sign-in');

 // A genuinely missing cookie: asked, and said.
 connect();sockets.at(-1).fail();
 asks[4](refused('cookie'));await turns();
 asks[5](refused('cookie'));await turns();
 assert(form(),'a genuinely missing cookie did not ask for a sign-in');
 assert(words().includes('Open the AII OS app on this computer') && !/token/i.test(form().textContent),'the form does not say what to do, or asks for a token: '+words());
 const reconnects=sockets.length;
 anotherTab();await turns();
 assert(!form() && sockets.length>reconnects,'another tab\'s sign-in left the form, or did not reconnect');

 // A genuinely missing page key: asked, and said.
 sockets.at(-1).fail();
 asks[6](refused('page-key'));await turns();
 asks[7](refused('page-key'));await turns();
 assert(form() && words().includes('Open the AII OS app on this computer'),'the form does not say what to do: '+words());
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

		"/presence.js":               stubModule("renderPresence", "setThinking"),
		"/app.js":                    stubModule("go", "renderFirstbootVisibility", "toast"),
		"/views/chat.js":             stubModule("addMsg", "attachSpeaker", "sysLine", "toolEventLive", "thinkingEvent", "renderInteractionPages", "renderHistory", "renderChatSubstrate", "renderComposer", "acceptSubstrateConfig", "rejectSubstrateConfig", "substrateConnectionLost", "renderSteering", "renderAsks"),
		"/views/home.js":             stubModule("renderHome"),
		"/views/work.js":             stubModule("renderWorkPill"),
		"/views/projects.js":         stubModule("renderProjPill", "renderProjects", "newlyCreatedID", "rejectCreate", "rejectFocusSave", "rejectContractSave", "acceptCreate", "acceptFocusSave", "acceptContractSave", "projectsConnectionLost"),
		"/shell/project-pill.js":     []byte(`export const renderProjPill = () => {};`),
		"/views/memory.js":           stubModule("renderMemory"),
		"/views/identity.js":         stubModule("renderIdentity"),
		"/views/identity/between.js": stubModule("renderBetween"),
		"/views/plugins.js":          stubModule("renderPlugins", "rejectPluginAction", "applyPluginStored"),
		"/views/settings.js":         stubModule("renderSettings", "acceptSettingsConfig", "rejectSettingsConfig", "acceptProviderSave", "rejectProviderSave", "acceptSpeechLists", "rejectSpeechLists", "acceptAccessCode", "rejectAccessCode", "settingsConnectionLost"),
		"/firstboot.js":              stubModule("renderProviderOptions", "setModelOptions", "fbHint", "fbResult", "acceptDiscoveryResponse", "firstbootConnectionLost", "discoveryAnswered", "discoveryFailed"),
		"/sections.js":               stubModule("publish", "onSections", "onLayout", "onTokensChanged"),
		"/panel.js":                  stubModule("renderPanel"),
		"/theme.js":                  stubModule("onTheme"),
		"/overlay.js":                stubModule("onOverlayChanged", "holdReloadWhile", "reloadForTheOverlay"),
		"/voice.js":                  stubModule("bindTransport", "render", "speak", "connectionLost"),
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
    if (options.body === 'SEEN') return { status: 403, ok: false, text: async () => 'the host said why\n', headers: new Headers() };
    return { status: 401, ok: false, text: async () => 'words that are not for the person', headers: new Headers() };
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

// A SOCKET REFUSED FOR ITS SIGN-IN IS NOT A LOST CONNECTION. The real server
// opens the socket on the cookie, then closes it with 4401 when the page holds
// no page key (pagekey.go admitHello). Read as a lost connection, every retry
// opened, cleared the form and its typing, drew "Loading conversation…", and
// was refused again, about once a second: the page could not be signed in
// (a Windows beta user, 2026-11-02). Asked once, it waits: no retry, the same
// form, the typing kept, until a sign-in here or in another tab.
func TestASocketRefusedForItsSignInAsksOnceAndWaits(t *testing.T) {
	runTokenPromptProbe(t, `
import {report} from '/__harness.js';
try {
 const assert=(ok,why)=>{if(!ok)throw Error(why)};
 const nativeFetch=window.fetch.bind(window);
 window.fetch=(url,options={})=>url==='/auth/token'?Promise.resolve({status:401,ok:false,headers:new Headers({'Aii-Missing':'page-key'})}):nativeFetch(url,options);
 const sockets=[];
 window.WebSocket=class{constructor(){this.readyState=0;sockets.push(this);}send(){}open(){this.readyState=1;this.onopen&&this.onopen({});}refuse(){this.readyState=3;this.onclose&&this.onclose({code:4401});}};
 Object.assign(window.WebSocket,{CONNECTING:0,OPEN:1,CLOSING:2,CLOSED:3});
 const turns=async()=>{for(let i=0;i<30;i++)await Promise.resolve();};
 const sleep=ms=>new Promise(r=>setTimeout(r,ms));
 const {connect,wake}=await import('/ws.js');
 connect();sockets[0].open();sockets[0].refuse();await turns();
 const box=document.getElementById('dashboard-signin');
 assert(box && box.textContent.includes('Open the AII OS app on this computer') && !/token/i.test(box.textContent),'a refused socket did not ask, or asked for a token');
 assert(document.documentElement.classList.contains('signin-asked'),'the page asking for a sign-in shows more than the sign-in');
 const input=box.querySelector('input');input.value='typed';
 // waits by design: past the old loop's one-second retry, more than twice over
 await sleep(2600);
 wake();window.dispatchEvent(new Event('online'));await turns();
 assert(sockets.length===1,'a socket refused for its sign-in was retried: '+sockets.length+' sockets');
 assert(document.getElementById('dashboard-signin')===box && input.value==='typed','the form was drawn again or its typing lost');
 // Another tab signs in: the page key it wrote is this page's too.
 window.dispatchEvent(new StorageEvent('storage',{key:'aii.signin',newValue:'a key another tab was given'}));await turns();
 assert(sockets.length===2 && !document.getElementById('dashboard-signin'),'a sign-in in another tab did not end the wait');
 assert(!document.documentElement.classList.contains('signin-asked'),'the page stayed bare after the sign-in');
 report('OK');
}catch(e){report('FAIL: '+e.message)}
`)
}

// THE RELAYED PATH CARRIES THE REFUSAL TOO. A page reached through a relay
// holds its socket in settle() until the identity says what carries the
// session; a 4401 before that is passed on as a 4401, so the page waits for
// a sign-in there as it does on a direct socket.
func TestTheRelayedPathPassesOnASignInRefusal(t *testing.T) {
	runTokenPromptProbe(t, `
import {report} from '/__harness.js';
import {settle} from '/transport/channel.js';
try {
 const socket={readyState:0,send(){},close(){}};
 const codes=[];
 settle(socket,()=>{throw Error('a refused socket was taken as the carrier')},code=>codes.push(code),()=>({}));
 socket.readyState=1;socket.onopen&&socket.onopen();
 socket.readyState=3;socket.onclose({code:4401});
 if(codes.length!==1||codes[0]!==4401)throw Error('the relayed path lost the refusal: '+JSON.stringify(codes));
 report('OK');
}catch(e){report('FAIL: '+e.message)}
`)
}

// ONLY THE DOOR'S ADMISSION ENDS A REFUSAL (a review's return on the fix). After
// a 4401, the door asked again may answer an error (a 500, a proxy's 502) or
// not answer at all: neither proves the page is signed in, so the form stays
// with what was typed, and nothing reconnects or asks again by itself. A 2xx
// from the door does prove it (the sign-in was turned off meanwhile), and the
// page reconnects at once, asking nobody.
func TestOnlyTheDoorsAdmissionEndsASignInRefusal(t *testing.T) {
	for name, answers := range map[string]string{
		"an error from the door":            `[{status:500,ok:false},{status:500,ok:false}]`,
		"a second check that never answers": `[{status:401,ok:false},'throw']`,
	} {
		t.Run(name, func(t *testing.T) {
			runTokenPromptProbe(t, `
import {report} from '/__harness.js';
try {
 const assert=(ok,why)=>{if(!ok)throw Error(why)};
 const nativeFetch=window.fetch.bind(window);
 let answers=`+answers+`, asked=0;
 window.fetch=(url,options={})=>{
  if(url!=='/auth/token')return nativeFetch(url,options);
  // The answers cycle: a page that wakes on an unconfirmed door asks it again and again.
  const a=answers[asked++%answers.length];
  if(a==='throw')return Promise.reject(new TypeError('the door did not answer'));
  return Promise.resolve(Object.assign({headers:new Headers({'Aii-Missing':'page-key'})},a));
 };
 const sockets=[];
 window.WebSocket=class{constructor(){this.readyState=0;sockets.push(this);}send(){}open(){this.readyState=1;this.onopen&&this.onopen({});}refuse(){this.readyState=3;this.onclose&&this.onclose({code:4401});}};
 Object.assign(window.WebSocket,{CONNECTING:0,OPEN:1,CLOSING:2,CLOSED:3});
 const sleep=ms=>new Promise(r=>setTimeout(r,ms));
 const until=async(ok,why)=>{for(let i=0;i<400;i++){if(ok())return;await new Promise(r=>setTimeout(r,15));}throw Error(why);};
 const {connect,wake}=await import('/ws.js');
 connect();sockets[0].open();sockets[0].refuse();await until(()=>document.getElementById('dashboard-signin'),'a refusal the door could not confirm asked nothing');
 const box=document.getElementById('dashboard-signin');
 assert(box,'a refusal the door could not confirm asked nothing');
 box.querySelector('input').value='typed';
 const before=asked;
 // waits by design: past the old loop's one-second retry, more than twice over
 await sleep(2600);
 // waits by design: a wake or coming online must start nothing; this is the time a start would take
 wake();window.dispatchEvent(new Event('online'));await sleep(300);
 assert(sockets.length===1,'an unconfirmed door ended the refusal: '+sockets.length+' sockets');
 assert(asked-before<=2,'the door was asked in a loop: '+(asked-before)+' more times');
 assert(document.getElementById('dashboard-signin')===box && box.querySelector('input').value==='typed','the form was drawn again or its typing lost');
 report('OK');
}catch(e){report('FAIL: '+e.message)}
`)
		})
	}
}

func TestADoorThatAdmitsEndsASignInRefusal(t *testing.T) {
	runTokenPromptProbe(t, `
import {report} from '/__harness.js';
try {
 const nativeFetch=window.fetch.bind(window);
 window.fetch=(url,options={})=>url==='/auth/token'?Promise.resolve({status:204,ok:true,headers:new Headers()}):nativeFetch(url,options);
 const sockets=[];
 window.WebSocket=class{constructor(){this.readyState=0;sockets.push(this);}send(){}open(){this.readyState=1;this.onopen&&this.onopen({});}refuse(){this.readyState=3;this.onclose&&this.onclose({code:4401});}};
 Object.assign(window.WebSocket,{CONNECTING:0,OPEN:1,CLOSING:2,CLOSED:3});
 const {connect}=await import('/ws.js');
 const until=async(ok,why)=>{for(let i=0;i<400;i++){if(ok())return;await new Promise(r=>setTimeout(r,15));}throw Error(why);};
 connect();sockets[0].open();sockets[0].refuse();await until(()=>sockets.length===2,'a door that admits did not end the refusal');
 if(document.getElementById('dashboard-signin')||sockets.length!==2)throw Error('a door that admits did not end the refusal: '+sockets.length+' sockets');
 report('OK');
}catch(e){report('FAIL: '+e.message)}
`)
}
