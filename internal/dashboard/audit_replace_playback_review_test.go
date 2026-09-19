// .
// .
// .
// .
// .
// .

//go:build !windows

package dashboard

import (
	"strings"
	"testing"
)

const auditReplacePlaybackPage = micMarkup + `<button id="hush-speaking" hidden></button><script type="module">
import { render, wireMic, bindTransport, sessionState, receiveFrame, voiceEvent, encodeStreamFrame, speak, playbackForTest, connectionLost } from './voice.js';
import { S } from './state.js';
import { assert, run } from './__harness.js';
run(async()=>{
 const kind='__CASE__';let inGesture=false,constructedDuringGesture=false;const sources=[];
 window.AudioContext=class {
  constructor(o){this.sampleRate=(o&&o.sampleRate)||48000;this.destination={};this.currentTime=0;this.state=kind==='prime'?'suspended':'running';constructedDuringGesture=inGesture}
  createBuffer(c,n,r){const d=Array.from({length:c},()=>new Float32Array(n));return {duration:n/r,getChannelData:i=>d[i]}}
  createBufferSource(){const s={connect(){},start(){},stop(){this.stopped=true},disconnect(){},addEventListener(){},onended:null,stopped:false};sources.push(s);return s}
  resume(){if(inGesture)this.state='running';return Promise.resolve()}
  close(){}
 };
 const msgs=[];bindTransport(()=>{},m=>{msgs.push(m);return 'r'+msgs.length});
 S.stats={voice_engine:true,voice_state:'plugin',voice_listen:'off',voice_speak:'on',voice_mode_revision:1};S.connected=true;
 wireMic();render();sessionState({state:'open',session_id:'vs-output-audit'});
 try {
  if(kind==='prime'){
   // Both once-listeners run in explicit synchronous gesture scope BEFORE
   // the delayed reply. The fake models a gesture-gated audio context.
   inGesture=true;document.dispatchEvent(new Event('pointerdown'));document.dispatchEvent(new KeyboardEvent('keydown',{key:'a',bubbles:true}));inGesture=false;
   receiveFrame(encodeStreamFrame(48000,1,1,1,0,new Int16Array(4800),1));
   assert(constructedDuringGesture && playbackForTest().ctx.state==='running','the first typed reply created its context after both gesture handlers; context='+playbackForTest().ctx.state+' primedInGesture='+constructedDuringGesture);
  }else{
   receiveFrame(encodeStreamFrame(48000,1,1,1,0,new Int16Array(48000),1));
   receiveFrame(encodeStreamFrame(48000,1,3,2,48000,null,1));
   voiceEvent({type:'synthesis_end',session_id:'vs-output-audit',synthesis_id:'first',playback_verified:false});
   assert(sources.length===1&&!sources[0].stopped,'fixture lost its buffered first reply');
   speak('new answer',{route:'plugin',session_id:'vs-output-audit',synthesis_id:'second'});
   receiveFrame(encodeStreamFrame(48000,1,1,1,0,new Int16Array(4800),2));
   assert(sources[0].stopped,'new typed reply left the old END-complete but unrendered source queued ahead of it; sources='+sources.length+' stopped='+playbackForTest().stats().stopped);
  }
 }finally{S.connected=false;connectionLost()}
});</script>`

func TestAuditNewTypedReplyRetiresUnrenderedOldAudio(t *testing.T) {
	runPageInEngines(t, strings.ReplaceAll(auditReplacePlaybackPage, "__CASE__", "replace"), micModules)
}
