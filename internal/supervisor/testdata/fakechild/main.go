// .
// .
// .
// .
// .
package main

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/audio"
)

const maxFrame = 1 << 20

func readFrame(r io.Reader) ([]byte, error) {
	var header [4]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(header[:])
	if n > maxFrame {
		return nil, fmt.Errorf("oversize frame %d", n)
	}
	payload := make([]byte, n)
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, err
	}
	return payload, nil
}

// .
// .
var frameMu sync.Mutex

func writeFrame(w io.Writer, payload []byte) {
	frameMu.Lock()
	defer frameMu.Unlock()
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(payload)))
	if _, err := w.Write(header[:]); err != nil {
		os.Exit(4)
	}
	if _, err := w.Write(payload); err != nil {
		os.Exit(4)
	}
}

func rawID(frame []byte) json.RawMessage {
	var probe struct {
		ID json.RawMessage `json:"id"`
	}
	_ = json.Unmarshal(frame, &probe)
	if len(probe.ID) == 0 {
		return json.RawMessage("null")
	}
	return probe.ID
}

func respond(id json.RawMessage, result string) []byte {
	return []byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":%s}`, id, result))
}

func main() {
	mode := "respond"
	if len(os.Args) > 1 {
		mode = os.Args[1]
	}
	if mode == "session-audio" {
		// .
		// .
		// .
		if in, ok := openAudioPair(); ok {
			go audioEcho(in)
		}
	}

	if mode == "ready-fields" {
		// .
		// .
		// .
		fmt.Fprintln(os.Stderr, "child-ready event=ready models_loaded=2 accelerator=mlx probe_ms=17")
		block()
	}

	if mode == "failstart" {
		fmt.Fprintln(os.Stderr, "fakechild: refusing to start (mode failstart)")
		os.Exit(2)
	}

	// .
	// .
	// .
	fmt.Fprintf(os.Stderr, "fakechild: child-ready mode=%s socket=%s canary=%q runtime_root=%q\n",
		mode, os.Getenv("SEV_PLUGIN_SOCKET"), os.Getenv("AII_SUPERVISOR_CANARY"), os.Getenv("AII_RUNTIME_ROOT"))

	switch mode {
	case "crash":
		// .
		time.Sleep(150 * time.Millisecond)
		fmt.Fprintln(os.Stderr, "fakechild: simulated crash")
		os.Exit(7)

	case "ignore-term":
		// .
		// .
		signal.Ignore(syscall.SIGTERM)
		block()

	case "deaf":
		// .
		// .
		// .
		// .
		block()

	case "alloc":
		// .
		// .
		// .
		n := 4 << 30
		if len(os.Args) > 2 {
			if v, err := strconv.Atoi(os.Args[2]); err == nil {
				n = v
			}
		}
		buf := make([]byte, n)
		for i := 0; i < n; i += 64 << 20 {
			buf[i] = 1
		}
		fmt.Fprintf(os.Stderr, "fakechild: alloc-ok bytes=%d sum=%d\n", n, buf[0])
		// .
		serveRespond()

	default:
		serveModes(mode)
	}
}

func serveRespond() { serveModes("respond") }

func serveModes(mode string) {
	responded := false
	for {
		frame, err := readFrame(os.Stdin)
		if err != nil {
			if err == io.EOF {
				fmt.Fprintln(os.Stderr, "fakechild: eof-exit")
				os.Exit(0)
			}
			os.Exit(4)
		}
		id := rawID(frame)

		switch mode {
		case "respond":
			writeFrame(os.Stdout, respond(id, `{"answered":true}`))

		case "crash-after-respond":
			if responded {
				// .
				os.Exit(7)
			}
			writeFrame(os.Stdout, respond(id, `{"answered":true}`))
			responded = true
			fmt.Fprintln(os.Stderr, "fakechild: crashing after first answer")
			os.Exit(7)

		case "hostcall":
			// .
			// .
			// .
			// .
			writeFrame(os.Stdout, []byte(`{"jsonrpc":"2.0","id":"c1","method":"invoke.call","params":{"operation":"kv.get","target":{"key":"probe"},"arguments":{}}}`))
			reply, err := readFrame(os.Stdin)
			if err != nil {
				os.Exit(4)
			}
			writeFrame(os.Stdout, respond(id, fmt.Sprintf(`{"upstream":%s}`, reply)))

		case "badid":
			writeFrame(os.Stdout, []byte(`{"jsonrpc":"2.0","id":"not-the-id","result":{}}`))

		case "bigframe":
			// .
			// .
			var header [4]byte
			binary.BigEndian.PutUint32(header[:], maxFrame+1)
			_, _ = os.Stdout.Write(header[:])
			_, _ = os.Stdout.Write([]byte("junk"))
			block()

		case "sleep":
			// .
			// .
			block()

		case "session", "session-audio":
			// .
			// .
			// .
			var req struct {
				ID     json.RawMessage `json:"id"`
				Method string          `json:"method"`
				Params struct {
					Operation string `json:"operation"`
					Arguments struct {
						SessionID   string                     `json:"session_id"`
						SynthesisID string                     `json:"synthesis_id"`
						InputHandle *string                    `json:"input_handle"`
						AudioRaw    map[string]json.RawMessage `json:"audio"`
					} `json:"arguments"`
				} `json:"params"`
			}
			_ = json.Unmarshal(frame, &req)
			if req.Method == "" || len(req.ID) == 0 {
				continue
			}
			// .
			// .
			// .
			// .
			// .
			if req.Params.Operation == "speech.session.open" {
				// .
				// .
				var a struct {
					Input  *struct{ Rate, Channels int }
					Output *struct{ Rate, Channels int }
				}
				if raw, ok := req.Params.Arguments.AudioRaw["input"]; ok && string(raw) != "null" {
					_ = json.Unmarshal(raw, &a.Input)
				}
				if raw, ok := req.Params.Arguments.AudioRaw["output"]; ok && string(raw) != "null" {
					_ = json.Unmarshal(raw, &a.Output)
				}
				engineSession(req.Params.Arguments.SessionID)
				// .
				// .
				// .
				// .
				// .
				// .
				rawIn, inPresent := req.Params.Arguments.AudioRaw["input"]
				outputOnly := inPresent && string(rawIn) == "null" && req.Params.Arguments.InputHandle == nil && a.Output != nil
				switch {
				case outputOnly && os.Getenv("FAKE_OMIT_INPUT") != "":
					writeFrame(os.Stdout, respond(id, fmt.Sprintf(`{"accepted":true,"audio":{"output":{"rate":%d,"channels":%d}}}`, a.Output.Rate, a.Output.Channels)))
				case outputOnly && os.Getenv("FAKE_DUPLEX_ANYWAY") != "":
					writeFrame(os.Stdout, respond(id, fmt.Sprintf(`{"accepted":true,"audio":{"input":{"rate":%d,"channels":%d},"output":{"rate":%d,"channels":%d}}}`, a.Output.Rate, a.Output.Channels, a.Output.Rate, a.Output.Channels)))
				case outputOnly:
					writeFrame(os.Stdout, respond(id, fmt.Sprintf(`{"accepted":true,"audio":{"input":null,"output":{"rate":%d,"channels":%d}}}`, a.Output.Rate, a.Output.Channels)))
				case os.Getenv("FAKE_NO_FORMATS") != "":
					writeFrame(os.Stdout, respond(id, `{"accepted":true}`))
				case os.Getenv("FAKE_ENGINE_RATE") != "":
					r := os.Getenv("FAKE_ENGINE_RATE")
					writeFrame(os.Stdout, respond(id, `{"accepted":true,"audio":{"input":{"rate":`+r+`,"channels":1},"output":{"rate":`+r+`,"channels":1}}}`))
				case a.Input != nil && a.Output != nil:
					writeFrame(os.Stdout, respond(id, fmt.Sprintf(`{"accepted":true,"audio":{"input":{"rate":%d,"channels":%d},"output":{"rate":%d,"channels":%d}}}`, a.Input.Rate, a.Input.Channels, a.Output.Rate, a.Output.Channels)))
				default:
					writeFrame(os.Stdout, respond(id, `{"accepted":true}`))
				}
			} else {
				writeFrame(os.Stdout, respond(id, `{"accepted":true}`))
			}
			if req.Params.Operation == "speech.session.synthesize" {
				// .
				// .
				// .
				// .
				if n, err := strconv.Atoi(os.Getenv("FAKE_SPEAK")); err == nil && n > 0 {
					speak(req.Params.Arguments.SynthesisID, n)
				}
				writeFrame(os.Stdout, []byte(`{"jsonrpc":"2.0","method":"session.event","params":{"type":"synthesis_end","synthesis_id":"s1","sequence":1}}`))
			}
			if req.Params.Operation == "speech.session.close" && os.Getenv("FAKE_NO_TERMINAL") == "" {
				// .
				// .
				// .
				// .
				writeFrame(os.Stdout, []byte(fmt.Sprintf(`{"jsonrpc":"2.0","method":"session.event","params":{"type":"session_end","session_id":%q,"sequence":2}}`, currentSession())))
			}
		}
	}
}

// .
// .
// .
func block() {
	for {
		time.Sleep(time.Hour)
	}
}

// .
// .
var (
	engineMu   sync.Mutex
	engineSess string
	audioOutMu sync.Mutex
	audioOut   *os.File
	spoken     uint32 = 1000
)

func engineSession(id string) {
	engineMu.Lock()
	engineSess = id
	engineMu.Unlock()
}

func currentSession() string {
	engineMu.Lock()
	defer engineMu.Unlock()
	return engineSess
}

// .
// .
func nameStream(synthesis string, stream uint32) {
	writeFrame(os.Stdout, []byte(fmt.Sprintf(`{"jsonrpc":"2.0","method":"session.event","params":{"type":"synthesis_start","session_id":%q,"synthesis_id":%q,"output_stream":%d}}`, currentSession(), synthesis, stream)))
}

func writeAudio(fr audio.Frame) error {
	audioOutMu.Lock()
	defer audioOutMu.Unlock()
	if audioOut == nil {
		return fmt.Errorf("fakechild: no audio pair")
	}
	return audio.WriteFrame(audioOut, fr)
}

// .
func speak(synthesis string, n int) {
	spoken++
	stream := spoken
	nameStream(synthesis, stream)
	const per = 320
	seq := uint32(0)
	for at := 0; at < n; at += per {
		k := per
		if n-at < k {
			k = n - at
		}
		pcm := make([]byte, 2*k)
		for i := 0; i < k; i++ {
			binary.LittleEndian.PutUint16(pcm[2*i:], uint16(at+i))
		}
		seq++
		if writeAudio(audio.Frame{Kind: audio.KindPCM, Stream: stream, Seq: seq, Start: int64(at), PCM: pcm}) != nil {
			return
		}
	}
	_ = writeAudio(audio.Frame{Kind: audio.KindEnd, Stream: stream, Seq: seq + 1, Start: int64(n)})
}

// .
// .
// .
// .
// .
// .
func openAudioPair() (in *os.File, ok bool) {
	inFD, err1 := strconv.Atoi(os.Getenv("AII_AUDIO_IN_FD"))
	outFD, err2 := strconv.Atoi(os.Getenv("AII_AUDIO_OUT_FD"))
	if err1 != nil || err2 != nil {
		fmt.Fprintln(os.Stderr, "fakechild: session-audio needs AII_AUDIO_IN_FD and AII_AUDIO_OUT_FD")
		return nil, false
	}
	audioOutMu.Lock()
	audioOut = os.NewFile(uintptr(outFD), "audio-out")
	audioOutMu.Unlock()
	return os.NewFile(uintptr(inFD), "audio-in"), true
}

func audioEcho(in *os.File) {
	defer func() {
		audioOutMu.Lock()
		out := audioOut
		audioOutMu.Unlock()
		if out != nil {
			out.Close()
		}
	}()
	named := map[uint32]bool{}
	for {
		fr, err := audio.ReadFrame(in)
		if err != nil {
			return
		}
		// .
		// .
		// .
		// .
		// .
		if !named[fr.Stream] {
			named[fr.Stream] = true
			nameStream(fmt.Sprintf("echo-%d", fr.Stream), fr.Stream)
		}
		if err := writeAudio(fr); err != nil {
			return
		}
		// .
		// .
	}
}
