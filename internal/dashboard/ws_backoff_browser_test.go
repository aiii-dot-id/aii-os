//go:build !windows

package dashboard

import (
	"testing"
)

const wsBackoffPage = `<!doctype html>
<button id="send-btn" disabled></button>
<button id="mic" disabled></button>
<script type="module">
import { assert, report } from './__harness.js';
try {
  let nextTimer = 1;
  const scheduled = [];
  window.setTimeout = (fn, delay, ...args) => {
    const timer = { id: nextTimer++, fn, delay, args };
    scheduled.push(timer);
    return timer.id;
  };
  window.clearTimeout = id => {
    const i = scheduled.findIndex(timer => timer.id === id);
    if (i >= 0) scheduled.splice(i, 1);
  };

  const randoms = [0, 0.999, 0, 0.999, 0, 0.999];
  Math.random = () => randoms.shift() ?? 0.5;
  const sockets = [];
  class ControlledSocket {
    constructor(url) {
      this.url = url;
      this.readyState = 0;
      this.onopen = null;
      this.onclose = null;
      this.onmessage = null;
      sockets.push(this);
    }
    send() {}
    fail() { this.readyState = 3; if (this.onclose) this.onclose({}); }
    open() { this.readyState = 1; if (this.onopen) this.onopen({}); }
  }
  ControlledSocket.CONNECTING = 0; ControlledSocket.OPEN = 1;
  ControlledSocket.CLOSING = 2; ControlledSocket.CLOSED = 3;
  window.WebSocket = ControlledSocket;

  const { connect } = await import('./ws.js');
  connect();
  assert(sockets.length === 1, 'initial connect did not create exactly one socket');

  const expected = [800, 2399.2, 3200, 9596.8, 8000, 10000];
  const near = (got, want) => Math.abs(got - want) < 0.001;
  for (let i = 0; i < expected.length; i++) {
    sockets[sockets.length - 1].fail();
    assert(scheduled.length === 1, 'failure ' + (i + 1) + ' left ' + scheduled.length + ' pending reconnects');
    assert(near(scheduled[0].delay, expected[i]),
      'failure ' + (i + 1) + ' scheduled ' + scheduled[0].delay + 'ms, want ' + expected[i] + 'ms');
    const timer = scheduled.shift();
    timer.fn(...timer.args);
  }
  assert(sockets.length === expected.length + 1, 'timer execution did not reconnect exactly once per failure');

  Math.random = () => 0.5;
  sockets[sockets.length - 1].open();
  sockets[sockets.length - 1].fail();
  assert(scheduled.length === 1, 'post-success close did not leave exactly one reconnect');
  assert(scheduled[0].delay === 1000, 'success did not reset the reconnect floor: ' + scheduled[0].delay + 'ms');
  report('OK');
} catch (e) { report('FAIL: ' + ((e && e.message) || String(e))); }
window.addEventListener('error', ev => report('FAIL: uncaught ' + ((ev.error && ev.error.message) || ev.message)));
window.addEventListener('unhandledrejection', ev => report('FAIL: rejected ' + String(ev.reason)));
</script>`

func TestWSReconnectBacksOff(t *testing.T) {
	wsJS, err := staticFS.ReadFile("static/ws.js")
	if err != nil {
		t.Fatal(err)
	}
	stateJS := []byte(`export const S = { connected: false, reconnectTimer: null, identityExists: false, view: 'home', tokenPrompted: false, wsEverOpened: false };`)
	runPageInEngines(t, wsBackoffPage, wsPageModules(wsJS, stateJS))
}

func wsPageModules(wsJS, stateJS []byte) map[string][]byte {
	return map[string][]byte{
		"/ws.js":             wsJS,
		"/state.js":          stateJS,
		"/views/projects.js": []byte(`export const renderProjPill = () => {}; export const renderProjects = () => {}; export const newlyCreatedID = () => null; export const rejectCreate = () => false; export const acceptCreate = () => ''; export const acceptFocusSave = () => false; export const acceptContractSave = () => false; export const rejectFocusSave = () => false; export const rejectContractSave = () => false; export const projectsConnectionLost = () => false; export function viewProject() {}`),
		"/views/memory.js":   []byte(`export const renderMemory = () => {};`),
		"/views/identity.js": []byte(`export const renderIdentity = () => {};`),
		"/views/plugins.js":  []byte(`export const renderPlugins = () => {}; export const rejectPluginAction = () => false;`),
		"/views/settings.js": []byte(`export const renderSettings = () => {}; export const acceptSettingsConfig = () => {}; export const rejectSettingsConfig = () => {}; export const acceptProviderSave = () => {}; export const rejectProviderSave = () => {}; export const acceptSpeechLists = () => {}; export const rejectSpeechLists = () => false; export const acceptDashboardToken = () => false; export const acceptAccessCode = () => {}; export const rejectAccessCode = () => {}; export const settingsConnectionLost = () => {};`),
		"/firstboot.js":      []byte(`export const renderProviderOptions = () => {}; export const setModelOptions = () => {}; export const fbHint = () => {}; export const fbResult = () => {}; export const acceptDiscoveryResponse = () => {}; export const firstbootConnectionLost = () => {}; export const discoveryAnswered = () => {}; export const discoveryFailed = () => false;`),
		"/sections.js":       []byte(`export const publish = () => {}; export const onSections = () => {}; export const onLayout = () => {}; export const onTokensChanged = () => {};`),
		"/panel.js":          []byte(`export const renderPanel = () => {};`),
		"/theme.js":          []byte(`export const onTheme = () => {};`),
		"/overlay.js":        []byte(`export const onOverlayChanged = () => {}; export const holdReloadWhile = () => {};`),
		"/voice.js":          []byte(`export const bindTransport = () => {}; export const render = () => {}; export const speak = () => {}; export const connectionLost = () => {};`),
		"/app.js":            []byte(`export const sysLine = () => {}; export const toast = () => {}; export const go = () => {}; export const renderFirstbootVisibility = () => {};`),
		"/views/chat.js":     []byte(`export const addMsg = () => null; export const attachSpeaker = () => false; export const sysLine = s => { (window.__chatLines ||= []).push(s); }; export const toolEventLive = () => {}; export const thinkingEvent = () => {}; export const renderInteractionPages = () => {}; export const renderHistory = () => {}; export const renderChatSubstrate = () => {}; export const renderComposer = () => {}; export const acceptSubstrateConfig = () => {}; export const rejectSubstrateConfig = () => {}; export const substrateConnectionLost = () => {}; export const renderSteering = () => {}; export const renderAsks = () => {};`),
		"/views/home.js":     []byte(`export const renderHome = () => {};`),
		"/views/work.js":     []byte(`export const renderWorkPill = () => {};`),
		"/presence.js":       []byte(`import { S } from './state.js'; export const renderPresence = () => {}; export const setThinking = on => { S.thinking = on; window.__think = on; };`),
		"/util.js":           utilStub(`export const $ = id => document.getElementById(id); export const esc = v => String(v ?? '');`),
		"/bridge.js":         []byte(`export const send = () => ''; export const query = () => '';`),
	}
}

func TestForegroundProbeRecoversStaleOpenSocketWithoutErrorCascade(t *testing.T) {
	page := `<!doctype html><button id="send-btn" disabled></button><button id="mic" disabled></button>
<script type="module">
import { run } from '/__harness.js';
import { S } from '/state.js';
const timers = [];
let nextTimer = 1;
window.setTimeout = (fn, delay) => { const x = {id:nextTimer++,fn,delay}; timers.push(x); return x.id; };
window.clearTimeout = id => { const i=timers.findIndex(x=>x.id===id); if(i>=0) timers.splice(i,1); };
const sockets=[];
class Socket {
  constructor(url){this.url=url;this.readyState=0;this.sent=[];sockets.push(this);}
  send(m){this.sent.push(JSON.parse(m));}
  open(){this.readyState=1;this.onopen?.({});}
  fail(){this.readyState=3;this.onclose?.({});}
  close(){this.readyState=3;}
  recv(m){this.onmessage?.({data:JSON.stringify(m)});}
}
window.WebSocket=Socket;
const ws=await import('/ws.js');
run((assert)=>{
  S.identityExists=true; S.view='chat'; S.stats={name:'Test'};
  let viewReads=0; S.refreshCurrentView=()=>{ viewReads++; };
  ws.connect(); sockets[0].open();
  sockets[0].recv({type:'interaction_changed'});
  function finishReads(){
    for(let n=0;n<4;n++){
      const q=sockets[0].sent.find(x=>x.query==='interaction'&&!x.answered);if(!q)break;q.answered=true;
      sockets[0].recv({type:'interaction_page',request_id:q.request_id,interaction_page:{version:1,source:q.interaction_query.source,cursor:q.interaction_query.cursor,fingerprint:'fixture',rows:[]},interaction_window:{fingerprint:'fixture',has_older:false,has_newer:false}});
    }
  }
  finishReads();
  ws.wake();
  const probe=sockets[0].sent.find(x=>x.type==='probe');
  assert(probe && timers.length===1 && timers[0].delay===8000, 'foreground did not probe the OPEN socket');
  sockets[0].recv({type:'probe',request_id:'someone-else'});
  assert(timers.length===1, 'an unrelated frame falsely proved liveness');
  sockets[0].recv({type:'probe',request_id:probe.request_id});
  assert(timers.length===0, 'matching probe did not cancel its deadline');
  assert(sockets[0].sent.some(x=>x.type==='query'&&x.query==='interaction') &&
    sockets[0].sent.some(x=>x.type==='query'&&x.query==='status'), 'resumed page did not ask for current activity');
  assert(viewReads===1, 'foreground did not refresh the current view');

  finishReads();
  ws.wake();
  const activeProbe=sockets[0].sent.filter(x=>x.type==='probe').at(-1);
  const historyBefore=sockets[0].sent.filter(x=>x.type==='query'&&x.query==='interaction').length;
  sockets[0].recv({type:'probe',request_id:activeProbe.request_id,turn_active:true});
  assert(window.__think===true && sockets[0].sent.filter(x=>x.type==='query'&&x.query==='interaction').length===historyBefore+1,
    'an active turn did not retain its marker while history refreshed');
  sockets[0].recv({type:'system_line',message:'one question was withdrawn'});
  assert(window.__think===true && (window.__chatLines||[]).at(-1)==='one question was withdrawn',
    'an informational system line ended the turn or vanished');
  sockets[0].recv({type:'error',request_id:'settings-check',message:'unrelated settings refusal'});
  assert(window.__think===true, 'an unrelated query error ended the active turn marker');
  const beforeReject=sockets[0].sent.filter(x=>x.type==='probe').length;
  S.reconcileTurn();S.reconcileTurn();
  assert(sockets[0].sent.filter(x=>x.type==='probe').length===beforeReject+1,'refusal frames duplicated the turn-state probe');
  sockets[0].recv({type:'error',request_id:'rejected-steer',message:'steer queue full'});
  assert(window.__think===true,'submission refusal hid Stop');
  const rejectProbe=sockets[0].sent.filter(x=>x.type==='probe').at(-1);
  sockets[0].recv({type:'probe',request_id:rejectProbe.request_id,turn_active:true});
  assert(window.__think===true,'active probe lost Stop');

  finishReads(); const beforeFinal=sockets[0].sent.filter(x=>x.query==='interaction').length;
  sockets[0].recv({type:'response',role:'identity',message:'finished'});
  assert(window.__think===false && sockets[0].sent.filter(x=>x.type==='query'&&x.query==='interaction').length===beforeFinal+1,
    'finished turn did not reconcile missed history');

  const noticesBeforeCancel=(window.__chatLines||[]).length;
  sockets[0].recv({type:'response',stream:true});
  sockets[0].recv({type:'cancelled'});
  assert(window.__think===false, 'host cancellation did not restore the idle action');
  assert((window.__chatLines||[]).length===noticesBeforeCancel, 'successful Stop added a confirmation banner');
  sockets[0].recv({type:'response',stream:true});
  sockets[0].recv({type:'error',message:'fixture turn failed'});
  assert(window.__think===false, 'turn failure did not restore the idle action');

  ws.wake();
  const staleProbe=sockets[0].sent.filter(x=>x.type==='probe').at(-1);
  sockets[0].recv({type:'response',role:'identity',message:'newly finished'});
  sockets[0].recv({type:'probe',request_id:staleProbe.request_id,turn_active:true});
  assert(window.__think===false, 'a stale active probe revived a finished turn');

  const noticesBeforeDisconnect=(window.__chatLines||[]).length;
  ws.wake();
  assert(timers.length===1, 'second foreground probe missing');
  timers.shift().fn(); // no answer: the mobile socket is half-open
  assert(sockets[0].readyState===3 && !S.connected && timers.length===1, 'stale socket was not retired and scheduled once');
  assert((window.__chatLines||[]).length===noticesBeforeDisconnect, 'disconnect appended a chat error');
  timers.shift().fn();
  assert(sockets.length===2, 'the page did not reconnect without a reload');
  sockets[1].fail();
  assert(timers.length===1 && (window.__chatLines||[]).length===noticesBeforeDisconnect, 'failed retry cascaded chat errors');
});
</script>`
	wsJS, err := staticFS.ReadFile("static/ws.js")
	if err != nil {
		t.Fatal(err)
	}
	stateJS := []byte(`export const S = { connected:false, reconnectTimer:null, identityExists:false, view:'chat', tokenPrompted:false, wsEverOpened:false, thinking:false };`)
	runPageInEngines(t, page, wsPageModules(wsJS, stateJS))
}

const tokenCancelPage = `<!doctype html>
<button id="send-btn" disabled></button>
<button id="mic" disabled></button>
<script type="module">
import { assert, report } from './__harness.js';
import { S } from './state.js';
import { recoverAuthentication } from './ws.js';
try {
  window.fetch = async () => ({ status: 401, ok: false });
  let prompts = 0;
  window.prompt = () => { prompts++; return null; };
  await recoverAuthentication();
  const box=document.getElementById('dashboard-signin');assert(box && !box.querySelector('form').hidden,'inline form missing');
  box.querySelector('form button[type="button"]').click();
  assert(S.tokenPrompted===false && box.querySelector('form').hidden,'cancel did not dismiss');
  await recoverAuthentication();
  assert(prompts===0 && document.getElementById('dashboard-signin')===box && box.querySelector('form').hidden,'reconnect reopened or duplicated authentication');
  box.querySelector('button').click();assert(!box.querySelector('form').hidden,'Sign in could not resume');
  report('OK');
} catch (e) {
  report('FAIL ' + (e && e.message ? e.message : String(e)));
}
</script>`

func TestACancelledTokenPromptAsksAgainNextTime(t *testing.T) {
	wsJS, err := staticFS.ReadFile("static/ws.js")
	if err != nil {
		t.Fatal(err)
	}
	stateJS := []byte(`export const S = { connected: false, reconnectTimer: null, identityExists: false, view: 'home', tokenPrompted: false, wsEverOpened: false };`)
	runPageInEngines(t, tokenCancelPage, wsPageModules(wsJS, stateJS))
}

const wsIdleRecoveryPage = `<!doctype html><html><head></head><body>
<button id="send-btn"></button><button id="mic"></button>
<script type="module">
import { run } from '/__harness.js';
import { S } from '/state.js';
let visible = true, online = true, fetches = 0;
Object.defineProperty(document, 'visibilityState', {get:()=>visible?'visible':'hidden'});
Object.defineProperty(navigator, 'onLine', {get:()=>online});
window.fetch = async () => { fetches++; return {status:200}; };
const timers = [], sockets = []; let sequence = 0;
window.setTimeout = (fn, delay) => {const t={id:++sequence,fn,delay};timers.push(t);return t.id;};
window.clearTimeout = id => {const i=timers.findIndex(t=>t.id===id);if(i>=0)timers.splice(i,1);};
class Socket {
  constructor(){this.readyState=0;this.sent=[];sockets.push(this);}
  send(m){this.sent.push(JSON.parse(m));}
  open(){this.readyState=1;this.onopen?.({});}
  close(){this.readyState=3;}
  fail(){this.readyState=3;this.onclose?.({});}
  recv(m){this.onmessage?.({data:JSON.stringify(m)});}
}
window.WebSocket=Socket;
const ws=await import('/ws.js');
const show = on => {visible=on;document.dispatchEvent(new Event('visibilitychange'));};
run(async assert => {
  S.identityExists=true; S.view='home';
  S.refreshCurrentView = read => ['status','continuity','projects','work'].forEach(read);
  ws.connect();const first=sockets[0];first.open();first.sent=[];
  assert(timers.length===0, 'healthy idle connection has a recovery timer');
  ws.wake();ws.wake();window.dispatchEvent(new Event('online'));
  assert(first.sent.filter(f=>f.type==='probe').length===1 && timers.length===1, 'overlapping wakes duplicate probes');
  const oldTimer=timers[0], oldProbe=first.sent[0];
  show(false);
  assert(timers.length===0 && first.readyState===1 && S.connected, 'hiding retired a healthy socket or kept a deadline');
  assert(document.documentElement.classList.contains('page-hidden'), 'hidden motion was not paused');
  first.recv({type:'probe',request_id:oldProbe.request_id});
  assert(first.sent.length===1, 'obsolete hidden probe triggered readback');
  window.dispatchEvent(new Event('online'));
  assert(first.sent.length===1 && timers.length===0, 'hidden online event started recovery');
  show(true);
  const fresh=first.sent.at(-1);
  oldTimer.fn();
  assert(first.readyState===1 && timers.length===1, 'old deadline killed the resumed socket');
  first.recv({type:'probe',request_id:fresh.request_id});
  assert(timers.length===0, 'answered foreground probe kept its timer');
  const queries=first.sent.filter(f=>f.type==='query').map(f=>f.query);
  assert(queries.length===6 && new Set(queries).size===6 && !queries.includes('history'), 'readback omitted or duplicated queries: '+queries);
  ws.wake();
  first.recv({type:'probe',request_id:first.sent.at(-1).request_id,turn_active:true});
  show(false);
  const beforeFinal=first.sent.length;
  first.recv({type:'response',role:'identity',message:'finished while hidden'});
  assert(first.sent.length===beforeFinal,'hidden turn completion started deferred history readback');
  first.fail();await Promise.resolve();
  assert(timers.length===0 && fetches===0, 'hidden loss retried or checked authentication');
  show(true);assert(sockets.length===2,'foreground did not reconnect');
  sockets[1].fail();await Promise.resolve();
  assert(timers.length===1 && fetches===1, 'visible failure did not schedule one retry and auth check');
  const retry=timers[0];
  online=false;window.dispatchEvent(new Event('offline'));
  assert(timers.length===0,'offline event left retry armed');
  retry.fn();assert(sockets.length===2,'cancelled offline retry dialled');
  ws.wake();assert(sockets.length===3,'explicit retry was blocked by a possibly stale offline hint');
  sockets[2].fail();await Promise.resolve();
  assert(timers.length===0 && fetches===1,'offline explicit failure started a retry loop or auth fetch');
  online=true;window.dispatchEvent(new Event('online'));
  assert(sockets.length===4,'online event did not resume recovery');
  sockets[3].open();
  window.dispatchEvent(new Event('pagehide'));
  assert(timers.length===0 && sockets[3].readyState===1,'pagehide closed healthy transport');
  window.dispatchEvent(new PageTransitionEvent('pageshow',{persisted:true}));
  assert(timers.length===1 && sockets[3].sent.at(-1).type==='probe','page-cache restoration did not check liveness');
});
</script></body></html>`

func TestInactiveDashboardDoesNoRecoveryWork(t *testing.T) {
	wsJS, err := staticFS.ReadFile("static/ws.js")
	if err != nil {
		t.Fatal(err)
	}
	stateJS := []byte(`export const S = {connected:false,reconnectTimer:null,identityExists:false,view:'home',thinking:false};`)
	runPageInEngines(t, wsIdleRecoveryPage, wsPageModules(wsJS, stateJS))
}

func TestAuthenticationReplyCannotPromptAfterThePageHides(t *testing.T) {
	page := `<!doctype html><html><head></head><body>
<button id="send-btn"></button><button id="mic"></button>
<script type="module">
import {run} from '/__harness.js';
let visible=true,resolveFetch,prompts=0,fetches=0;
Object.defineProperty(document,'visibilityState',{get:()=>visible?'visible':'hidden'});
window.fetch=()=>{fetches++;return new Promise(resolve=>{resolveFetch=resolve})};
window.prompt=()=>{prompts++;return null};
const ws=await import('/ws.js');
run(async assert=>{
 const pending=ws.recoverAuthentication();
 assert(fetches===1,'visible auth check did not start');
 visible=false;document.dispatchEvent(new Event('visibilitychange'));
 resolveFetch({status:401});await pending;
 assert(prompts===0 && !document.getElementById('dashboard-signin'),'a late auth response prompted a hidden page');
 await ws.recoverAuthentication();assert(fetches===1,'hidden auth check sent traffic');
 visible=true;
 const resumed=ws.recoverAuthentication();resolveFetch({status:401});await resumed;
 assert(prompts===0 && document.getElementById('dashboard-signin'),'suspension permanently suppressed legitimate inline authentication');
});</script></body></html>`
	wsJS, err := staticFS.ReadFile("static/ws.js")
	if err != nil {
		t.Fatal(err)
	}
	runPageInEngines(t, page, wsPageModules(wsJS, []byte(`export const S={connected:false,reconnectTimer:null,tokenPrompted:false};`)))
}

func TestEveryConnectionAsksForTheMode(t *testing.T) {
	wsJS, err := staticFS.ReadFile("static/ws.js")
	if err != nil {
		t.Fatal(err)
	}
	stateJS := []byte(`export const S = { connected: false, reconnectTimer: null, identityExists: true, view: 'chat', tokenPrompted: false, wsEverOpened: false };`)
	page := `<!doctype html><button id="send-btn" disabled></button><button id="mic" disabled></button>
<script type="module">
import { run } from '/__harness.js';
const sockets=[];
class Socket { constructor(url){this.url=url;this.readyState=0;this.sent=[];sockets.push(this);} send(m){this.sent.push(JSON.parse(m));} open(){this.readyState=1;this.onopen?.({});} close(){this.readyState=3;} }
window.WebSocket=Socket;
const ws=await import('/ws.js');
run(assert=>{
  ws.connect(); sockets[0].open();
  assert(sockets[0].sent.some(x=>x.type==='query'&&x.query==='continuity'), 'a connection did not ask for the mode: '+JSON.stringify(sockets[0].sent.map(x=>x.query||x.type)));
});
</script>`
	runPageInEngines(t, page, wsPageModules(wsJS, stateJS))
}

func TestContinuityWaitsForBirthAndRefreshesAfterward(t *testing.T) {
	wsJS, err := staticFS.ReadFile("static/ws.js")
	if err != nil {
		t.Fatal(err)
	}
	page := `<!doctype html><button id="send-btn"></button><button id="mic"></button>
<script type="module">
import {run} from '/__harness.js';import {S} from '/state.js';
const sockets=[];class Socket{constructor(){this.readyState=0;this.sent=[];sockets.push(this)}send(m){this.sent.push(JSON.parse(m))}open(){this.readyState=1;this.onopen?.({})}close(){this.readyState=3}}
window.WebSocket=Socket;const ws=await import('/ws.js');
run(assert=>{
 ws.connect();const socket=sockets[0];socket.open();
 const continuity=()=>socket.sent.filter(f=>f.query==='continuity').length;
 assert(continuity()===0,'unborn page asked for continuity');
 const status=name=>socket.onmessage({data:JSON.stringify({type:'status',stats:{name}})});
 status('(not born)');assert(continuity()===0,'unborn status asked for continuity');
 status('Fixture');assert(continuity()===1,'birth did not request continuity');
 status('Fixture');assert(continuity()===1,'ordinary stats repeatedly requested continuity');
 assert(S.identityExists,'birth state was not retained');
});</script>`
	runPageInEngines(t, page, wsPageModules(wsJS, []byte(`export const S={connected:false,identityExists:false,view:'chat'};`)))
}
