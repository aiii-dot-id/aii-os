//go:build !windows

package dashboard

import "testing"

func TestNativeEchoPreservesMicOffListenEarbudAndReopen(t *testing.T) {
	page := micMarkup + `<script type="module">
import { render, wireMic, bindTransport, sessionState, playbackForTest, lanesForTest } from './voice.js';
import { setEchoMode } from './voice-echo.js';
import { S } from './state.js';
import { assert, run, until } from './__harness.js';
run(async()=>{
 const sent=[],contexts=[],tracks=[],nodes=[];
 bindTransport(()=>{},m=>{sent.push(m);return 'req-'+sent.length;});
 if(!navigator.mediaDevices)Object.defineProperty(navigator,'mediaDevices',{value:{},configurable:true});
 navigator.mediaDevices.getUserMedia=async()=>{
  const t={stopped:false,label:'fixture',stop(){this.stopped=true},addEventListener(){},getSettings:()=>({echoCancellation:true})};tracks.push(t);
  return {getTracks:()=>[t],getAudioTracks:()=>[t]};
 };
 window.AudioContext=class {
  constructor(){this.sampleRate=48000;this.destination={};this.currentTime=0;this.state='running';this.audioWorklet={addModule:async()=>{}};contexts.push(this);}
  createGain(){return {connect(){},disconnect(){}}}
  createMediaStreamSource(){return {connect(){}}}
  close(){this.state='closed';return Promise.resolve()}
 };
 globalThis.AudioWorkletNode=class {
  constructor(c,name,o){assert(o.numberOfInputs===2&&o.processorOptions.reference,'explicit reference worklet');this.port={onmessage:null,close(){},postMessage(m){queueMicrotask(()=>this.onmessage?.({data:{ack:m.flush,samples:0,buffer:null}}));}};nodes.push(this);}
  disconnect(){}
 };
 setEchoMode('combined');S.connected=true;
 const mode=(listen,speak,revision)=>{S.stats={voice_state:'plugin',voice_engine:true,voice_listen:listen,voice_speak:speak,voice_mode_revision:revision};render();};
 wireMic();mode('off','off',1);
 document.getElementById('converse').click();
 await until(()=>sent.some(m=>m.voice?.action==='open'&&m.voice.mode==='conversation'),'paired conversation open');
 let open=sent.find(m=>m.voice?.action==='open').voice;
 assert(open.channels===2&&open.processing.channel_roles[1]==='playback_reference','negotiated paired input');
 assert(contexts.length===1&&playbackForTest().ctx===contexts[0],'one shared context');
 sessionState({state:'open',session_id:'first'});mode('interactive','auto',2);
 mode('off','on',3);
 await until(()=>tracks[0].stopped,'earbud capture flush and retirement');
 assert(tracks[0].stopped&&contexts[0].state==='running','earbud did not retire mic independently');
 sessionState({state:'closed',session_id:'first'});
 mode('off','off',4);sessionState({state:'closed',session_id:'earbud'});
 document.getElementById('converse').click();
 await until(()=>contexts.length===2&&nodes.length===2,'fresh capture on reopen');
 assert(contexts[0].state==='closed'&&!tracks[1].stopped&&playbackForTest().ctx===contexts[1],'fresh reference state without old route');
 const before=lanesForTest().at(-1).samples;
 assert(nodes[0].port.onmessage===null,'retired capture callback remains connected');
 assert(lanesForTest().at(-1).samples===before,'reopen source clock changed');
 mode('off','off',5);await until(()=>tracks[1].stopped,'off left microphone open');
});</script>`
	runPageInEngines(t, page, micModules)
}
