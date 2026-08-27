/* voice.js — the microphone and the voice.

   THE BROWSER IS THE AUDIO PLANE. It holds the microphone and it holds
   the speaker, because it is where the person is; the identity is on
   another machine entirely. So capture happens here, one whole utterance
   crosses the socket that already spans that link, and the identity's
   reply is spoken here too. Nothing else carries audio anywhere.

   A superseded design carried PCM to a contained plugin over a second
   transport. A contained plugin cannot reach a socket, so none of it
   worked (aii-os docs/VOICE_FIRST_PRINCIPLES.md). This module is the
   whole of what replaced it.

   PUSH TO TALK, AND ONLY THAT FOR NOW. The operator holds the button,
   speaks, releases; that release is what defines the end of an
   utterance. Voice-activity detection can decide that boundary later
   without changing one byte on the wire — the server is told "here is a
   complete utterance" either way, and has never needed to know how the
   page decided.

   NO RESAMPLING HERE. An AudioContext hands you whatever the device
   decided — 44100 and 48000 are both common and neither was asked for —
   so we send the true rate in the frame header and let the transcription
   engine resample, which every one of them already does correctly.
   Resampling in the page would put the most delicate arithmetic in the
   codebase where it is hardest to prove right, and a wrong rate is a
   silently sped-up transcript rather than a visible failure. */
import { S } from './state.js';
import { $ } from './util.js';
import { toast } from './app.js';

/* --- frame ------------------------------------------------- */
/* Matches internal/dashboard/voice_ws.go exactly:
     [0]   version (1)
     [1]   channels
     [2:4] reserved, zero
     [4:8] sample rate, uint32 little-endian
   These two definitions ship in the same binary — the static files are
   embedded — so they cannot drift apart in the field. */
const FRAME_VERSION = 1;
const HEADER_BYTES = 8;

/* --- state ------------------------------------------------- */
let media = null;      /* MediaStream, while the mic is open */
let ctx = null;        /* AudioContext */
let node = null;       /* the capture node */
let chunks = [];       /* Float32Array slices of the utterance in progress */
let captured = 0;      /* samples accumulated so far in this utterance */
let overflow = false;  /* the server's ceiling was reached; stop and send */
let capturing = false;
let sendFrame = null;  /* injected by ws.js — this module never touches the socket */
/* HELD IS AN INTENT, AND IT IS NOT THE SAME AS CAPTURING. getUserMedia
   is asynchronous — it can prompt, and it takes real time even when it
   does not — so a quick tap releases the button BEFORE capture starts.
   Without this flag the release found capturing===false and returned,
   the permission then resolved, and the microphone came up LIVE with
   nobody holding the button: the exact failure push-to-talk exists to
   prevent. The release now records that the operator let go, and
   whichever of the two finishes last is the one that tears down. */
let held = false;

/* THE SOCKET IS NOT OURS. ws.js owns it exclusively (its own header says
   so), so this module is handed a way to send one frame and knows
   nothing else about the connection. */
export function bindTransport(fn) { sendFrame = fn; }

/* --- capture ----------------------------------------------- */
async function startCapture() {
  if (capturing) return;
  /* getUserMedia EXISTS ONLY IN A SECURE CONTEXT. On http:// to a
     private address the property is simply absent, which reads as "this
     browser has no microphone" unless we say otherwise — and the fix is
     not something the operator can guess. */
  if (!navigator.mediaDevices || !navigator.mediaDevices.getUserMedia) {
    toast('The microphone needs a secure context — serve the dashboard over HTTPS, or reach it on localhost.');
    return;
  }
  try {
    media = await navigator.mediaDevices.getUserMedia({
      audio: {
        /* Let the browser do the parts it does well. These are hints,
           not guarantees, and every engine downstream copes either way. */
        echoCancellation: true,
        noiseSuppression: true,
        autoGainControl: true,
        channelCount: 1,
      },
    });
  } catch (err) {
    /* A REFUSAL IS NOT A FAULT. The operator said no, or the device is
       busy; either way the honest thing is to say what happened and
       leave the button where it was. */
    toast('No microphone: ' + (err && err.message ? err.message : err));
    return;
  }
  /* LET GO WHILE WE WERE ASKING. Everything below opens a live
     microphone, so it must not run for an operator who is no longer
     holding the button. */
  if (!held) { media.getTracks().forEach(t => t.stop()); media = null; return; }
  ctx = new (window.AudioContext || window.webkitAudioContext)();
  const src = ctx.createMediaStreamSource(media);
  /* ScriptProcessorNode is deprecated and universally implemented;
     AudioWorklet is current and needs a separate module file the CSP
     must also admit. This path works in all five platforms' browsers
     today, and the node is the only thing that would change. */
  node = ctx.createScriptProcessor(4096, 1, 1);
  chunks = [];
  captured = 0;
  overflow = false;
  node.onaudioprocess = e => {
    if (!capturing || overflow) return;
    /* COPY. The event's buffer is reused by the audio thread the moment
       this returns, so keeping the reference keeps a view of whatever
       is recorded next. */
    const buf = new Float32Array(e.inputBuffer.getChannelData(0));
    /* THE CEILING IS THE SERVER'S, AND IT IS REACHED HERE RATHER THAN
       THERE. Past maxVoiceFrameBytes the socket's read limit closes the
       connection: the operator's sentence is lost, the page reconnects,
       and nothing anywhere says why. Stopping at the same number and
       SENDING what was said turns a mystery disconnect into a truncated
       utterance the operator was told about. */
    const limit = frameLimit();
    if (limit && (captured + buf.length) * 2 + HEADER_BYTES > limit) {
      overflow = true;
      /* Out of the audio callback before tearing down its own node. */
      setTimeout(() => { held = false; stopCapture(true); }, 0);
      return;
    }
    captured += buf.length;
    chunks.push(buf);
  };
  src.connect(node);
  node.connect(ctx.destination);
  capturing = true;
  render();
}

/* frameLimit is what the host said it will accept, or 0 if it has not
   said. Zero means send and let the server judge — the old behaviour,
   which is right for a host too old to carry the field. */
function frameLimit() {
  const n = S.stats && S.stats.voice_max_frame_bytes;
  return typeof n === 'number' && n > 0 ? n : 0;
}

function stopCapture(send) {
  if (!capturing) return;
  capturing = false;
  captured = 0;
  const rate = ctx ? ctx.sampleRate : 0;
  const collected = chunks;
  chunks = [];
  teardown();
  render();
  if (!send || !collected.length || !rate) return;

  let total = 0;
  for (const c of collected) total += c.length;
  /* TOO SHORT TO BE SPEECH. A stray click produces a few milliseconds of
     room tone; sending it costs a round trip and returns nothing, or
     worse, returns a hallucinated word. */
  if (total < rate * 0.2) return;
  if (overflow) toast('That is as much as one utterance can carry — sending what I heard.');

  const frame = new ArrayBuffer(HEADER_BYTES + total * 2);
  const view = new DataView(frame);
  view.setUint8(0, FRAME_VERSION);
  view.setUint8(1, 1);              /* channels */
  view.setUint16(2, 0, true);       /* reserved */
  view.setUint32(4, rate, true);
  let off = HEADER_BYTES;
  for (const c of collected) {
    for (let i = 0; i < c.length; i++) {
      /* Float32 [-1,1] to signed 16-bit, clamped. Clamping matters:
         a sample above 1.0 wraps to a large NEGATIVE integer, which is
         a loud click in the middle of the word that caused it. */
      let v = c[i];
      if (v > 1) v = 1; else if (v < -1) v = -1;
      view.setInt16(off, v < 0 ? v * 0x8000 : v * 0x7fff, true);
      off += 2;
    }
  }
  if (sendFrame) sendFrame(frame);
}

function teardown() {
  if (node) { try { node.disconnect(); } catch (e) { /* already gone */ } node = null; }
  if (ctx) { try { ctx.close(); } catch (e) { /* already closed */ } ctx = null; }
  if (media) { media.getTracks().forEach(t => t.stop()); media = null; }
}

/* --- speaking ---------------------------------------------- */
/* THE BROWSER SPEAKS, AND IT IS THE ONE PART OF THIS THAT NEEDED NO
   BUILDING. speechSynthesis is in every major browser, so the identity's
   reply is text on the wire and sound in the room, with no audio going
   anywhere near the network and no TTS endpoint to configure. */
export function speak(text) {
  if (!S.voiceSpeak) return;
  if (!window.speechSynthesis || !text) return;
  /* WHAT IT IS SAYING NOW IS NEVER WORTH LESS THAN WHAT IT SAID BEFORE.
     Queueing would have the identity work through a backlog while the
     operator waits for the answer to the thing they just asked. */
  window.speechSynthesis.cancel();
  window.speechSynthesis.speak(new SpeechSynthesisUtterance(text));
}

/* Barge-in, such as it is: the operator picking up the microphone means
   they are talking, so the identity stops. */
export function hush() {
  if (window.speechSynthesis) window.speechSynthesis.cancel();
}

/* THE SOCKET GOING AWAY MUST CLOSE THE MICROPHONE.
   Losing the connection used only to disable the button, which stops
   the operator starting a NEW utterance and does nothing about the one
   already being recorded: the capture node kept running, the
   MediaStream stayed open, and the browser went on holding a live
   microphone with nowhere to send it. Continuing a transcription the
   host already accepted across a reconnect is deliberate and stays;
   recording into a closed socket is not the same thing. */
export function connectionLost() {
  held = false;
  stopCapture(false);
}

/* --- control ----------------------------------------------- */
export function render() {
  const b = $('mic');
  if (!b) return;
  const available = !!(S.stats && S.stats.voice);
  b.disabled = !available || !S.connected;
  b.classList.toggle('live', capturing);
  if (!available) {
    b.title = 'No speech endpoint is configured — set speech.stt in Settings';
    return;
  }
  b.title = capturing ? 'Release to send what you said' : 'Hold to speak';
}

export function wireMic() {
  const b = $('mic');
  if (!b) return;
  b.removeAttribute('data-inert');
  /* HOLD, NOT TOGGLE. A toggle that fails to untoggle leaves a hot
     microphone in the room, which is the one failure mode this feature
     must not have. Pointer events cover mouse, pen and touch with one
     path, and losing the pointer entirely still stops the capture. */
  b.addEventListener('pointerdown', e => { e.preventDefault(); held = true; hush(); startCapture(); });
  b.addEventListener('pointerup', e => { e.preventDefault(); held = false; stopCapture(true); });
  b.addEventListener('pointerleave', () => { held = false; stopCapture(false); });
  b.addEventListener('pointercancel', () => { held = false; stopCapture(false); });
  /* A page going away with the microphone open must not leave it open. */
  window.addEventListener('pagehide', () => { held = false; stopCapture(false); });
  render();
}
