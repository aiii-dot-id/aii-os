package dashboard

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fixtureInteractionReader struct {
	st    *store.Store
	reads atomic.Uint64
}

func (f *fixtureInteractionReader) QueryInteractions(ctx context.Context, q interaction.Query) (*interaction.Page, error) {
	f.reads.Add(1)
	var p *interaction.Page
	var err error
	if q.Source == "transient" {
		p, err = interaction.MemoryPage(q, "fixture-process", nil, 0)
	} else {
		p, err = f.st.QueryInteractions(ctx, q)
	}
	if err == nil {
		p.Identity = "isolated-fixture"
		interaction.Seal(p)
	}
	return p, err
}
func (f *fixtureInteractionReader) ReadInteraction(ctx context.Context, r interaction.ReadRequest) (*interaction.ReadResult, error) {
	return f.st.ReadInteraction(ctx, r)
}

// Opt-in isolated physical-browser fixture. It owns an in-memory store, no
// identity keys, ledger, LLM, live tools or identity configuration. Its optional
// microphone path counts and discards audio; it never transcribes or saves it.
func TestInteractionPhysicalFixture(t *testing.T) {
	addr := os.Getenv("AII_INTERACTION_FIXTURE_ADDR")
	if addr == "" {
		t.Skip("physical fixture is opt-in")
	}
	st, err := store.NewMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for i := 0; i < 65; i++ {
		if _, err = st.RecordInteraction(t.Context(), interaction.Input{ID: fmt.Sprintf("seed-%02d", i), Kind: interaction.Message, Role: interaction.Operator, Content: fmt.Sprintf("Recorded fixture occurrence %02d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	for i, tool := range []string{"read", "check"} {
		if err := st.RecordToolStart("fixture-tools", i+1, "main", "fixture", "", tool, `{"path":"fixture.txt"}`); err != nil {
			t.Fatal(err)
		}
		if err := st.RecordToolDoneMeasured("fixture-tools", i+1, tool, `{"path":"fixture.txt"}`, "Fixture result — no tool was executed", interaction.Succeeded, false, store.ToolMeasurement{}); err != nil {
			t.Fatal(err)
		}
	}
	reader := &fixtureInteractionReader{st: st}
	var active atomic.Bool
	var audioFrames atomic.Uint64
	var effort atomic.Value
	effort.Store("high")
	h := &WSHandler{Speaker: "identity", Interactions: reader, GetStats: func() (*StatsResponse, error) {
		return &StatsResponse{Name: "Isolated history fixture", Version: "test"}, nil
	}, GetConfig: func() *ConfigState {
		return &ConfigState{LLM: LLMConfigState{EffortChoice: &EffortChoice{Levels: []string{"low", "high"}, InForce: effort.Load().(string)}}}
	},
		SetEffort: func(level string) error {
			if level != "" && level != "low" && level != "high" {
				return fmt.Errorf("unsupported fixture effort")
			}
			if level == "" {
				level = "high"
			}
			effort.Store(level)
			return nil
		},
		VoiceConfigured: func() bool { return true }, VoiceStatus: func() (string, string, string) { return "cloud", "", "Discard-only fixture" },
		VoiceMode:     func() (string, string, uint64) { return "interactive", "off", 1 },
		HearUtterance: func(context.Context, []byte, int, int, bool) error { audioFrames.Add(1); return nil },
		GetAsks:       func() []AskView { return nil }, TurnActive: active.Load, CancelTurn: func() bool { return active.Swap(false) }}
	h.GetWork = func() (*WorkState, error) { return &WorkState{}, nil }
	h.GetProjects = func() ([]ProjectState, error) { return []ProjectState{}, nil }
	h.GetIdentity = func() (*IdentityState, error) { return &IdentityState{}, nil }
	h.GetTools = func() []ToolState { return []ToolState{} }
	h.GetContinuity = func() (*ContinuityState, error) { return &ContinuityState{Mode: "normal"}, nil }
	h.HandleMessage = func(ctx context.Context, text string) (string, error) {
		active.Store(true)
		defer active.Store(false)
		ref, err := st.RecordConversation(ctx, "operator", text, "", interaction.Details{})
		loc := &interaction.Location{Identity: "isolated-fixture", Source: "recorded", Incarnation: st.InteractionIncarnation(), ID: ref.ID}
		ChatRecorded(ctx, loc, err)
		if err != nil {
			return "", err
		}
		reply := "Fixture answer: " + text
		return reply, st.AddConversationTurn("resident", reply)
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil {
		t.Fatal(err)
	}
	s := New(host, portNumber, h)
	st.SetInteractionObserver(s.PokeInteractions)
	s.anyHostPort = port
	stop := make(chan struct{}, 1)
	var reportMu sync.Mutex
	var reports []json.RawMessage
	assets := map[string]string{}
	for _, path := range []string{"index.html", "app.js", "ws.js", "views/chat.js", "presence.js", "interaction-view.js", "interaction-display.js", "voice.js", "layout.css"} {
		data, err := staticFS.ReadFile("static/" + path)
		if err != nil {
			t.Fatal(err)
		}
		assets[path] = fmt.Sprintf("%x", sha256.Sum256(data))
	}
	assetJSON, _ := json.Marshal(assets)
	fixtureBuild := fmt.Sprintf("%x", sha256.Sum256(assetJSON))
	base := s.server.Handler
	s.server.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/__accept.js" {
			w.Header().Set("Content-Type", "text/javascript")
			w.Header().Set("Cache-Control", "no-store")
			fmt.Fprintf(w, "const fixtureBuild=%q;\n%s", fixtureBuild, interactionPhysicalScript)
			return
		}
		if r.URL.Path == "/__report" {
			raw, err := io.ReadAll(io.LimitReader(r.Body, 32769))
			if err != nil || len(raw) > 32768 || !json.Valid(raw) {
				http.Error(w, "invalid fixture report", 400)
				return
			}
			t.Logf("PHYSICAL_REPORT remote=%s agent=%s result=%s", r.RemoteAddr, r.UserAgent(), raw)
			reportMu.Lock()
			if len(reports) == 20 {
				reports = reports[1:]
			}
			reports = append(reports, append(json.RawMessage(nil), raw...))
			reportMu.Unlock()
			w.WriteHeader(204)
			return
		}
		if r.URL.Path == "/" && r.URL.Query().Get("fixture_accept") == "1" {
			rec := httptest.NewRecorder()
			base.ServeHTTP(rec, r)
			for k, v := range rec.Header() {
				w.Header()[k] = v
			}
			w.Header().Del("Content-Length")
			w.Header().Del("ETag")
			body := bytes.Replace(rec.Body.Bytes(), []byte("</body>"), []byte(`<script type="module" src="/__accept.js"></script></body>`), 1)
			w.WriteHeader(rec.Code)
			w.Write(body)
			return
		}
		if r.URL.Path != "/__fixture" {
			base.ServeHTTP(w, r)
			return
		}
		switch r.URL.Query().Get("action") {
		case "busy":
			active.Store(true)
			s.broadcast(ServerMessage{Type: "response", Stream: true})
		case "idle":
			active.Store(false)
			s.broadcast(ServerMessage{Type: "response", Role: "identity", Done: true})
		case "append":
			if err := st.AddConversationTurn("resident", "Fixture notification while another client was idle"); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
		case "disconnect":
			s.wsMu.Lock()
			for c := range s.wsConns {
				c.CloseNow()
			}
			s.wsMu.Unlock()
		case "stop":
			select {
			case stop <- struct{}{}:
			default:
			}
		}
		page, err := st.QueryInteractions(t.Context(), interaction.Query{Version: 1})
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		reportMu.Lock()
		copyReports := append([]json.RawMessage(nil), reports...)
		reportMu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"page": page, "reads": reader.reads.Load(), "audio_frames": audioFrames.Load(), "assets": assets, "reports": copyReports})
	})
	tlsDir := ""
	if os.Getenv("AII_INTERACTION_FIXTURE_TLS") == "1" {
		tlsDir = os.Getenv("AII_INTERACTION_FIXTURE_TLS_DIR")
		if tlsDir == "" {
			tlsDir = t.TempDir()
		}
	}
	actual, err := s.Start(tlsDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	}()
	t.Logf("isolated fixture ready %s (%s)", s.Origin(), actual)
	select {
	case <-stop:
	case <-time.After(45 * time.Minute):
		t.Error("physical fixture safety timeout")
	}
}

const interactionPhysicalScript = `
const wait=ms=>new Promise(r=>setTimeout(r,ms));
async function until(fn){for(let i=0;i<150;i++){if(await fn())return;await wait(100)}throw Error('fixture convergence timeout')}
const rows=()=>Array.from(document.querySelectorAll('#thread-inner [data-turn-id]')).flatMap(e=>{
 const records=JSON.parse(e.dataset.records);const message=e.querySelector('.record-message .body');
 if(message&&message.textContent!==records[0].content)throw Error('displayed message differs from occurrence');
 const retained=Array.from(e.querySelectorAll('.record-content'));if(retained.some((el,i)=>el.textContent!==records[i].content))throw Error('retained detail differs from occurrence');
 return records;
}).sort((a,b)=>BigInt(a.sequence)<BigInt(b.sequence)?-1:BigInt(a.sequence)>BigInt(b.sequence)?1:0);
const oracle=async(action='')=>(await(await fetch('/__fixture'+(action?'?action='+action:''))).json()).page.rows;
const equal=(a,b)=>JSON.stringify(a)===JSON.stringify(b);
const button=title=>Array.from(document.querySelectorAll('.interaction-controls button')).find(e=>e.textContent===title&&!e.disabled&&!e.hidden&&e.getClientRects().length);
const report={browser:navigator.userAgent,build:fixtureBuild,checks:[]};
const proofKey='interaction-fixture-proof:'+fixtureBuild;
async function finish(){
 await fetch('/__report',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(report)});
 const marker=document.createElement('pre');marker.id='fixture-verdict';marker.hidden=true;marker.textContent=JSON.stringify(report,null,2);document.body.appendChild(marker);
 if(!report.passed)return;
 const button=document.createElement('button');button.id='fixture-microphone-check';button.type='button';button.className='btn ghost';button.textContent=window.isSecureContext?'Start microphone check':'Microphone check needs HTTPS';button.disabled=!window.isSecureContext;
 button.style.cssText='position:fixed;right:8px;top:58px;z-index:99;background:var(--panel-solid)';
 button.title='Isolated fixture: audio is counted and discarded, never transcribed or saved.';
 button.onclick=async()=>{
  button.disabled=true;button.textContent='Checking microphone…';
  const result={browser:navigator.userAgent,build:fixtureBuild,check:'physical microphone continuity',secure:window.isSecureContext};
  const original=navigator.mediaDevices.getUserMedia;let stream=null,opens=0,openError=null;
  navigator.mediaDevices.getUserMedia=async function(c){opens++;try{stream=await original.call(this,c);return stream}catch(e){openError=e;throw e}};
  const mic=document.getElementById('mic'), effort=document.getElementById('effort-val').textContent;
  const press=type=>mic.dispatchEvent(new PointerEvent(type,{bubbles:true,cancelable:true}));
  const untilMic=async fn=>{for(let i=0;i<600;i++){if(fn())return;await wait(100)}throw Error('Microphone permission/capture did not complete')};
  try{
   const beforeRead=fetch('/__fixture').then(r=>r.json());
   press('pointerdown');press('pointerup');
   const before=await beforeRead;
   await untilMic(()=>openError||stream&&mic.classList.contains('live'));if(openError)throw openError;
   const track=stream.getAudioTracks()[0];result.sample_rate=track.getSettings?.().sampleRate;
   const tool=document.querySelector('.tool-ev');tool.querySelector('summary').click();if(!tool.open)throw Error('Tool detail did not open');
   await oracle('append');await until(async()=>equal(rows(),await oracle()));
   tool.querySelector('summary').click();if(tool.open)throw Error('Tool detail did not close');
   if(opens!==1||track.readyState!=='live'||document.getElementById('mic')!==mic||!mic.classList.contains('live'))throw Error('Disclosure/history update disrupted microphone capture');
   if(document.getElementById('effort-val').textContent!==effort)throw Error('Disclosure/history update changed effort');
   await wait(1000);press('pointerdown');press('pointerup');
   await untilMic(()=>track.readyState==='ended'&&!mic.classList.contains('live'));
   await until(async()=>((await(await fetch('/__fixture')).json()).audio_frames>before.audio_frames));
   Object.assign(result,{passed:true,capture_requests:opens,track_ended:true,effort_preserved:true,audio_discarded:true});button.textContent='Microphone check passed';
  }catch(e){result.passed=false;result.error=String(e);button.textContent='Microphone check failed — retry';button.disabled=false;}
  finally{
   navigator.mediaDevices.getUserMedia=original;
   if(stream?.getTracks().some(t=>t.readyState==='live')){const voice=await import('/voice.js');voice.connectionLost();voice.render();stream.getTracks().forEach(t=>t.stop());}
   await fetch('/__report',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(result)});
  }
 };
 document.body.appendChild(button);
}
try {
 const prior=JSON.parse(sessionStorage.getItem(proofKey)||'null');
 if(prior?.stage==='complete'){report.passed=true;report.checks=['Already completed; clear fixture sessionStorage to repeat'];}
 else if(prior?.stage==='reload'){
  await until(async()=>equal(rows(),await oracle()));
  if(performance.timeOrigin===prior.origin)throw Error('document did not reload');
  report.checks=prior.checks.concat('Cold reload matches the same store occurrences');report.passed=true;
  sessionStorage.setItem(proofKey,JSON.stringify({stage:'complete'}));
 }else{
  await until(async()=>rows().length===50&&equal(rows(),await oracle()));report.checks.push('Initial displayed bodies and occurrence metadata match the store oracle');
  const mic=document.getElementById('mic'), effort=document.getElementById('effort-btn');
  if(document.getElementById('chat-detail'))throw Error('Global display selector still clutters the conversation');
  const beforeDisclosure=JSON.stringify(rows());
  const tools=Array.from(document.querySelectorAll('.tool-ev'));if(tools.length!==2||tools.some(e=>e.open))throw Error('tool executions are not compact single entries');
  tools[0].querySelector('summary').click();if(!tools[0].open||tools[1].open)throw Error('Tool summaries did not open independently');
  tools[0].querySelector('summary').click();if(tools.some(e=>e.open))throw Error('Tool detail did not close');
  if(document.getElementById('mic')!==mic||document.getElementById('effort-btn')!==effort||document.getElementById('effort-val').textContent!=='High')throw Error('disclosure changed microphone/effort controls');
  if(JSON.stringify(rows())!==beforeDisclosure)throw Error('disclosure changed history');report.checks.push('One compact entry per execution; independent disclosures retain identical facts and composer controls');

  const input=document.querySelector('#msg-input');input.value='Safari fixture question';input.dispatchEvent(new Event('input',{bubbles:true}));document.querySelector('#send-btn').click();
  await until(async()=>{const expected=await oracle();return expected.at(-1)?.content==='Fixture answer: Safari fixture question'&&equal(rows(),expected)&&!document.querySelector('[data-request-id]')});report.checks.push('Composer input and reply appear exactly once; pending acknowledgement retires');
  await oracle('append');await until(async()=>equal(rows(),await oracle()));report.checks.push('Independent append refreshes without reload');
  const thread=document.getElementById('thread');thread.scrollTop=0;
  thread.dispatchEvent(new WheelEvent('wheel',{deltaY:-120,bubbles:true}));
  await until(()=>rows()[0]?.id==='seed-00'&&rows().length>50);report.checks.push('Upward scroll reveals contiguous earlier history');
  if(button('Earlier')||button('Newer')||button('Latest'))throw Error('Paging buttons clutter history');
  document.getElementById('jump-latest').click();await until(async()=>equal(rows(),await oracle()));
  const origin=performance.timeOrigin;await oracle('disconnect');await wait(1500);const {S}=await import('/state.js');await until(async()=>S.connected&&equal(rows(),await oracle())&&!document.querySelector('.interaction-controls').textContent.includes('Last known'));
  if(performance.timeOrigin!==origin)throw Error('reconnect required reload');report.checks.push('Reconnect preserves the document and recovers authoritative history');
  sessionStorage.setItem(proofKey,JSON.stringify({stage:'reload',origin,checks:report.checks}));location.reload();
 }
 if(report.passed)await finish();
}catch(e){report.passed=false;report.error=String(e);await finish()}
`
