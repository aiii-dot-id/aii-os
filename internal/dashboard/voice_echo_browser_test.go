//go:build !windows

package dashboard

import (
	"strings"
	"testing"
)

const voiceEchoPage = `<!doctype html><script type="module">
import { assert, run } from './__harness.js';
import { CaptureProcessor } from './voice-capture.worklet.js';
import { StreamLane, decodeStreamFrame, Playback } from './voice.js';
import { echoModes, setEchoMode, echoMode, nativeEcho, browserEcho, referenceRoles } from './voice-echo.js';
run(() => {
 for (const [mode] of echoModes) {
  setEchoMode(mode);
  assert(echoMode()===mode && nativeEcho()===(mode==='native'||mode==='combined') && browserEcho()===(mode==='browser'||mode==='combined'), 'four independent processing conditions');
 }
 setEchoMode('browser');
 for(let tail=1;tail<480;tail++) {
  const p=new CaptureProcessor({processorOptions:{frame:480,reference:true}}), messages=[];
  p.port.postMessage=m=>messages.push(m);
  for(let i=0;i<480+tail;i+=128) {
   const n=Math.min(128,480+tail-i);
   p.process([[new Float32Array(n).fill(.25)],[new Float32Array(n).fill(-.5)]]);
  }
  p.flush(1);p.process([[new Float32Array(128)],[new Float32Array(128)]]);
  assert(messages.length===2 && messages[1].samples===tail,'flush once on the frame clock');
  const frames=[],json=[];
  const lane=new StreamLane({rate:48000,channels:2,processing:{channel_roles:referenceRoles},sendFrame:b=>frames.push(decodeStreamFrame(b)),sendJSON:m=>json.push(m)});
  lane.open();lane.onState({state:'open'});
  for (const m of messages) lane.chunk(new Float32Array(m instanceof ArrayBuffer?m:m.buffer));
  lane.finishInput();
  assert(json[0].voice.processing.channel_roles[1]==='playback_reference','explicit roles');
  assert(frames[0].start===0&&frames[1].start===480&&frames[2].start===480+tail,'paired channel clock/tail');
  for (const f of frames.slice(0,2))for(let i=0;i<f.pcm.length;i+=2)assert(f.pcm[i]===8191&&f.pcm[i+1]===-16384,'no channel bleed');
 }
 const gain={channelCount:0,channelCountMode:''};
 const c={state:'running',createGain:()=>gain,currentTime:0,close:()=>Promise.resolve()};
 const play=new Playback();
 assert(play.useCaptureContext(c)===gain&&play.ctx===c&&gain.channelCount===1,'shared output/capture clock');
 let refused=false;try{play.useCaptureContext(c)}catch(e){refused=true}assert(refused,'no replacement of live context');
});</script>`

func TestReferenceCaptureAndTailInBrowser(t *testing.T) {
	runPageInEngines(t, voiceEchoPage, map[string][]byte{
		"/app.js": []byte("export function toast() {}"),
	})
}

const referenceGraphPage = `<!doctype html><script type="module">
import { assert, run } from './__harness.js';
import { Playback, encodeStreamFrame } from './voice.js';
run(async () => {
 const ctx=new OfflineAudioContext(1,4800,48000);
 const playback=new Playback();
 const reference=playback.useCaptureContext(ctx);
 await ctx.audioWorklet.addModule('./voice-capture.worklet.js');
 const node=new AudioWorkletNode(ctx,'voice-capture',{numberOfInputs:2,numberOfOutputs:0,channelCount:1,channelCountMode:'explicit',processorOptions:{frame:128,reference:true}});
 const chunks=[];node.port.onmessage=e=>{if(e.data instanceof ArrayBuffer)chunks.push(new Float32Array(e.data));};
 reference.connect(node,0,1);
 const buf=ctx.createBuffer(1,4800,48000);buf.getChannelData(0).fill(.25);
 const mic=ctx.createBufferSource();mic.buffer=buf;mic.connect(node,0,0);mic.start(0);
 playback.feed(encodeStreamFrame(16000,1,1,1,0,new Int16Array(1600).fill(16384),7));
 playback.sources[0].src.stop(.05);
 await ctx.startRendering();
 await new Promise(r=>setTimeout(r,50));
 const all=Float32Array.from(chunks.flatMap(x=>Array.from(x)));
 assert(all.length>=9000,'worklet received actual paired render quanta: '+all.length);
 for(let i=0;i<4500;i++) {
  assert(Math.abs(all[2*i]-.25)<1e-5,'microphone unchanged by graph');
  if(i<800||i>2600)assert(Math.abs(all[2*i+1])<1e-5,'unscheduled or cancelled output absent from reference');
  if(i>1100&&i<2200)assert(Math.abs(all[2*i+1]-.5)<.005,'actual scheduled output in reference');
 }
});</script>`

func TestPlaybackReferenceOnActualBrowserGraph(t *testing.T) {
	runPageInEngines(t, referenceGraphPage, map[string][]byte{"/app.js": []byte("export function toast() {}")})
}

func TestTheVoiceCaptureWorkletRunsUnderTheFramesPolicy(t *testing.T) {
	if !strings.Contains(uiCSP, "worker-src 'none'") {
		t.Fatalf("the frame's policy does not refuse workers: %s", uiCSP)
	}
	graph := `import { assert, run, until } from './__harness.js';
import { Playback, encodeStreamFrame } from './voice.js';
run(async () => {
 let registered = null;
 try { registered = await navigator.serviceWorker.register('/sw-probe.js'); } catch (_) {}
 if (registered) await registered.unregister();
 assert(!registered, 'a service worker was registered under the frame\'s policy');
 const ctx=new OfflineAudioContext(1,4800,48000);
 const playback=new Playback();
 const reference=playback.useCaptureContext(ctx);
 await ctx.audioWorklet.addModule('./voice-capture.worklet.js');
 const node=new AudioWorkletNode(ctx,'voice-capture',{numberOfInputs:2,numberOfOutputs:0,channelCount:1,channelCountMode:'explicit',processorOptions:{frame:128,reference:true}});
 const chunks=[];node.port.onmessage=e=>{if(e.data instanceof ArrayBuffer)chunks.push(new Float32Array(e.data));};
 reference.connect(node,0,1);
 const buf=ctx.createBuffer(1,4800,48000);buf.getChannelData(0).fill(.25);
 const mic=ctx.createBufferSource();mic.buffer=buf;mic.connect(node,0,0);mic.start(0);
 playback.feed(encodeStreamFrame(16000,1,1,1,0,new Int16Array(1600).fill(16384),7));
 playback.sources[0].src.stop(.05);
 await ctx.startRendering();
 await until(() => chunks.reduce((n, c) => n + c.length, 0) >= 9000, 2000);
 const all=Float32Array.from(chunks.flatMap(x=>Array.from(x)));
 assert(all.length>=9000,'the worklet received the graph\'s render quanta under the frame\'s policy: '+all.length);
 for(let i=0;i<4500;i++) assert(Math.abs(all[2*i]-.25)<1e-5,'microphone unchanged by graph');
});
`
	runPageInEnginesWithHeaders(t, `<!doctype html><script type="module" src="/graph.js"></script>`, map[string][]byte{
		"/app.js":      []byte("export function toast() {}"),
		"/graph.js":    []byte(graph),
		"/sw-probe.js": []byte("self.addEventListener('install', () => self.skipWaiting());\n"),
	}, map[string]string{"Content-Security-Policy": uiCSP})
}
