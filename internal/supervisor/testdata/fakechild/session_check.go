package main

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/audio"
)

func serveSessionCheck(knob string) {
	in, hasPair := openAudioPair()
	if hasPair {
		go countInput(in)
	}
	reqs := make(chan []byte, 64)
	go func() {
		defer close(reqs)
		for {
			frame, err := readFrame(os.Stdin)
			if err != nil {
				return
			}
			var probe struct {
				ID     json.RawMessage `json:"id"`
				Method string          `json:"method"`
			}
			_ = json.Unmarshal(frame, &probe)
			if probe.Method == "" {
				upstreamAnswered(string(probe.ID), frame)
				continue
			}
			reqs <- frame
		}
	}()
	e := &checkEngine{knob: knob}
	for frame := range reqs {
		e.control(frame)
	}
	fmt.Fprintln(os.Stderr, "fakechild: eof-exit")
	os.Exit(0)
}

type checkEngine struct {
	knob    string
	session string
	seq     int
	closed  bool
	synths  int
}

func (e *checkEngine) emit(typ string, fields map[string]any) {
	e.seq++
	ev := map[string]any{"type": typ, "session_id": e.session, "sequence": e.seq}
	for k, v := range fields {
		ev[k] = v
	}
	raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": "session.event", "params": ev})
	writeFrame(os.Stdout, raw)
}

func (e *checkEngine) control(frame []byte) {
	var req struct {
		ID     json.RawMessage `json:"id"`
		Params struct {
			Operation string         `json:"operation"`
			Arguments map[string]any `json:"arguments"`
		} `json:"params"`
	}
	_ = json.Unmarshal(frame, &req)
	args := req.Params.Arguments
	id := req.ID
	refuse := func(why string) {
		writeFrame(os.Stdout, []byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"error":{"code":-32000,"message":%q}}`, id, why)))
	}
	switch req.Params.Operation {
	case "speech.session.open":
		if e.knob == "refuse" {
			refuse("a session is already open")
			return
		}

		sid := fmt.Sprint(args["session_id"])
		au, _ := args["audio"].(map[string]any)
		_, hearing := au["input"].(map[string]any)
		for k := range args {
			switch k {
			case "session_id", "check", "input_handle", "output_handle", "audio":
			default:
				refuse("the open carries " + k + ", which a check's open does not")
				return
			}
		}
		switch {
		case args["check"] != true:
			refuse("this engine opens only a check's session, which says check: true")
			return
		case args["output_handle"] != sid+"-out", hearing && args["input_handle"] != sid+"-in", !hearing && args["input_handle"] != nil:
			refuse(fmt.Sprintf("the handles are not the check's: %v %v", args["input_handle"], args["output_handle"]))
			return
		}
		e.session, e.seq, e.closed = sid, 0, false
		input := "null"
		if f, ok := au["input"].(map[string]any); ok {
			input = fmt.Sprintf(`{"rate":%v,"channels":%v}`, f["rate"], f["channels"])
		}
		writeFrame(os.Stdout, respond(id, `{"accepted":true,"check":`+fmt.Sprint(args["check"] == true)+`,"audio":{"format":"s16le","input":`+input+`,"output":{"rate":16000,"channels":1}}}`))
		e.emit("session_ready", nil)
		switch e.knob {
		case "failure":
			e.emit("failure", map[string]any{"reason": "the recognizer could not load", "reason_code": "MODEL_MISSING"})
			e.closed = true
		case "retry":
			e.emit("failure", map[string]any{"reason": "the model did not answer in time", "reason_code": "MODEL_DEADLINE", "retry_may_pass": true})
			e.closed = true
		case "hostcalls":
			e.emit("transcript_final", map[string]any{"text": askTheHost()})
		}
	case "speech.session.finish_input":
		writeFrame(os.Stdout, respond(id, `{"accepted":true}`))
		if e.knob == "slow" {
			time.Sleep(1500 * time.Millisecond)
		}
		text := "Cobalt lantern, seventeen."
		if e.knob == "wrong" {
			text = "cobalt seventeen"
		}
		e.emit("speech_start", nil)
		e.emit("transcript_final", map[string]any{"text": text})
		end := args["end_sample"]
		e.emit("input_finished", map[string]any{"stream_id": args["stream_id"], "end_sample": end, "processed_end_sample": end})
	case "speech.session.synthesize":
		writeFrame(os.Stdout, respond(id, `{"accepted":true}`))
		e.synths++
		stream := uint32(5000 + e.synths)
		synth := fmt.Sprint(args["synthesis_id"])
		e.emit("synthesis_start", map[string]any{"synthesis_id": synth, "output_stream": stream})
		if e.knob == "slow" {
			time.Sleep(1500 * time.Millisecond)
		}
		n := speakTone(stream, e.knob == "silent")
		e.emit("synthesis_end", map[string]any{"synthesis_id": synth, "output_stream": stream, "delivered_samples": n})
	case "speech.session.close":
		writeFrame(os.Stdout, respond(id, `{"accepted":true}`))
		if e.knob != "noend" && !e.closed {
			e.closed = true
			e.emit("cancellation", map[string]any{"reason": fmt.Sprint(args["reason"])})
		}
	case "speech.session.status":
		lifecycle := "open"
		if e.closed {
			lifecycle = "closed"
		}
		writeFrame(os.Stdout, respond(id, fmt.Sprintf(`{"session_id":%q,"state_sequence":1,"lifecycle":%q}`, e.session, lifecycle)))
	default:
		writeFrame(os.Stdout, respond(id, `{"accepted":true}`))
	}
}

// speakTone writes half a second of a 440 Hz tone at 16 kHz (amplitude
// 6000, about -17.8 dBFS), or of zeros, under stream, and its END.
func speakTone(stream uint32, silent bool) int {
	const n, per = 8000, 800
	seq := uint32(0)
	for at := 0; at < n; at += per {
		pcm := make([]byte, 2*per)
		for i := 0; i < per && !silent; i++ {
			v := int16(math.Round(6000 * math.Sin(2*math.Pi*440*float64(at+i)/16000)))
			binary.LittleEndian.PutUint16(pcm[2*i:], uint16(v))
		}
		seq++
		if writeAudio(audio.Frame{Kind: audio.KindPCM, Stream: stream, Seq: seq, Start: int64(at), PCM: pcm}) != nil {
			return at
		}
	}
	_ = writeAudio(audio.Frame{Kind: audio.KindEnd, Stream: stream, Seq: seq + 1, Start: n})
	return n
}

// countInput reads what the host sends on the input descriptor until it
// closes: this engine hears nothing in it, as a stand-in.
func countInput(in io.Reader) {
	for {
		if _, err := audio.ReadFrame(in); err != nil {
			return
		}
	}
}

// The engine's own host calls, answered by the host on the session lane.
var (
	upMu     sync.Mutex
	upWait   = map[string]chan []byte{}
	upNumber int
)

func upstreamAnswered(id string, frame []byte) {
	upMu.Lock()
	ch := upWait[id]
	delete(upWait, id)
	upMu.Unlock()
	if ch != nil {
		ch <- frame
	}
}

// ask makes one host call and returns its answer and how long it took.
func ask(operation string) (string, time.Duration) {
	upMu.Lock()
	upNumber++
	id := fmt.Sprintf(`"up-%d"`, upNumber)
	ch := make(chan []byte, 1)
	upWait[id] = ch
	upMu.Unlock()
	start := time.Now()
	writeFrame(os.Stdout, []byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"method":"invoke.call","params":{"operation":%q,"target":{"key":"probe","path":"probe.json"},"arguments":{"query":"probe","value":"x"}}}`, id, operation)))
	select {
	case frame := <-ch:
		took := time.Since(start)
		var reply struct {
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Data struct {
					ReasonCode string `json:"reasonCode"`
				} `json:"data"`
			} `json:"error"`
		}
		_ = json.Unmarshal(frame, &reply)
		if reply.Error != nil {
			return "refused " + reply.Error.Data.ReasonCode, took
		}
		return "answered " + string(reply.Result), took
	case <-time.After(5 * time.Second):
		return "no answer", time.Since(start)
	}
}

// askTheHost asks each call a check's mark governs and says, one line
// each, what came back and in how many milliseconds.
func askTheHost() string {
	var lines []string
	for _, op := range []string{"fs.read", "kv.put", "memory.recall", "settings.get"} {
		answer, took := ask(op)
		lines = append(lines, fmt.Sprintf("%s %s in %d ms", op, answer, took.Milliseconds()))
	}
	return strings.Join(lines, "; ")
}
