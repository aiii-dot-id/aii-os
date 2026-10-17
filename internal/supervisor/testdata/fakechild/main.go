package main

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
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

// frameMu keeps a frame whole: the loopback engine names its streams
// from its own goroutine, on the same lane the request loop answers on.
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

// materialModeSuffix names a declared model or runtime file whose name
// chooses the mode of a child launched with no arguments — a native
// package's artifact, which the host runs by path alone. A file
// "<mode>.fakechild-mode" in AII_MODELS_DIR or AII_RUNTIME_ROOT selects
// <mode>.
const materialModeSuffix = ".fakechild-mode"

func modeFromMaterial() string {
	for _, env := range []string{"AII_MODELS_DIR", "AII_RUNTIME_ROOT"} {
		dir := os.Getenv(env)
		if dir == "" {
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if name := e.Name(); strings.HasSuffix(name, materialModeSuffix) {
				return strings.TrimSuffix(name, materialModeSuffix)
			}
		}
	}
	return ""
}

// readySDK is the kit the ready-sdk report names: written here, by the
// test, in the form the kit writes (a pseudo-version naming a commit).
const readySDK = "v0.1.15-0.20261008000000-0123456789ab"

func main() {
	mode := "respond"
	if len(os.Args) > 1 {
		mode = os.Args[1]
	} else if m := modeFromMaterial(); m != "" {
		mode = m
		// The mode it was given, before anything else it says, so a test
		// knows the host is relaying THIS child. (The pid is the child's
		// own view: inside a Linux PID namespace it is not the host's.)
		fmt.Fprintf(os.Stderr, "fakechild: pid=%d mode=%s\n", os.Getpid(), mode)
	}
	if mode == "session-audio" {
		// A loopback engine (V-E): every input frame on the inherited
		// audio descriptors comes back as output with its span intact,
		// and the input's end ends the output.
		if in, ok := openAudioPair(); ok {
			go audioEcho(in)
		}
	}

	if mode == "ready-fields" {
		// A native engine's readiness report, as S11 shapes it: the mark
		// plus the fields the host keeps. No shell, so every platform can
		// run the proof that they survive.
		fmt.Fprintln(os.Stderr, "child-ready event=ready models_loaded=2 accelerator=mlx probe_ms=17")
		block()
	}

	if mode == "ready-real" || mode == "ready-unreal" || mode == "ready-sdk" {
		// A native engine's readiness report that the host keeps
		// (ready-real), the same report naming the kit that built it as
		// the kit writes it (ready-sdk), or one carrying the mark without
		// the fields a profile requires (ready-unreal); each then serves.
		line := "child-ready event=ready accelerator=cpu"
		switch mode {
		case "ready-real":
			line = "child-ready event=ready models_loaded=1 accelerator=cpu probe_ms=3"
		case "ready-sdk":
			line = "child-ready event=ready models_loaded=1 accelerator=cpu probe_ms=3 sdk=" + readySDK
		}
		fmt.Fprintln(os.Stderr, line)
		serveRespond()
		return
	}

	if mode == "worker-args" {
		// A worker stand-in that reports the arguments it was given on
		// its readiness line — the proof mark the host waits for, read
		// back from the -descriptor-proof argument — and then serves.
		proof := ""
		for _, a := range os.Args[2:] {
			if v, ok := strings.CutPrefix(a, "-descriptor-proof="); ok {
				proof = v
			}
		}
		fmt.Fprintf(os.Stderr, "fakechild: descriptor_proof=%s args=%s\n", proof, strings.Join(os.Args[2:], " "))
		serveRespond()
		return
	}

	if mode == "failstart" {
		fmt.Fprintln(os.Stderr, "fakechild: refusing to start (mode failstart)")
		os.Exit(2)
	}

	if mode == "wall" {
		wallProbe(os.Args[2:])
		return
	}

	if mode == "reach" {
		reachProbe(os.Args[2:])
		return
	}

	if mode == "carrier" {
		// A carrier: it starts a worker from its runtime root, as a real
		// engine's carrier does, tries one program of the system's, says
		// what happened to each, and then serves like any child.
		fmt.Fprintf(os.Stderr, "fakechild: carrier %s\n", carrierProbe())
		mode = "respond"
	}

	// canary reports whether the parent's environment leaked in. The
	// supervisor gives a child EXACTLY spec.Env, so a variable set in
	// the test process must arrive empty here.
	fmt.Fprintf(os.Stderr, "fakechild: child-ready mode=%s socket=%s canary=%q runtime_root=%q\n",
		mode, os.Getenv("SEV_PLUGIN_SOCKET"), os.Getenv("AII_SUPERVISOR_CANARY"), os.Getenv("AII_RUNTIME_ROOT"))

	if knob, ok := strings.CutPrefix(mode, "session-check"); ok {
		serveSessionCheck(strings.TrimPrefix(knob, "-"))
		return
	}

	switch mode {
	case "crash":
		// Ready, then die shortly after: the restart-policy driver.
		time.Sleep(150 * time.Millisecond)
		fmt.Fprintln(os.Stderr, "fakechild: simulated crash")
		os.Exit(7)

	case "ignore-term":
		// Swallow SIGTERM and never read stdin: proves the kill
		// escalation (EOF grace → TERM grace → KILL).
		signal.Ignore(syscall.SIGTERM)
		block()

	case "deaf":
		// Announces readiness and then NEVER READS STDIN: a large
		// enough permitted frame fills the pipe and parks the host's
		// write, which is how Invoke used to sit through its own
		// deadline (audit finding 5).
		block()

	case "hold":
		// Holds a stated number of bytes, every page of them written,
		// and with one more argument starts a child of its own that
		// holds that many besides: what the supervisor's measure of a
		// child and everything below it reads (held.go). The child below
		// lives until this process ends: it serves a pipe this one holds.
		n, _ := strconv.Atoi(os.Args[2])
		heldForTest = make([]byte, n)
		for i := 0; i < n; i += 4096 {
			heldForTest[i] = 1
		}
		if len(os.Args) > 3 {
			self, err := os.Executable()
			if err != nil {
				panic(err)
			}
			below := exec.Command(self, "hold", os.Args[3])
			below.Stderr = os.Stderr
			if _, err := below.StdinPipe(); err != nil {
				panic(err)
			}
			if err := below.Start(); err != nil {
				panic(err)
			}
		}
		fmt.Fprintf(os.Stderr, "fakechild: holding bytes=%d\n", n)
		serveRespond()

	case "alloc", "alloc-on-request":
		// The RLIMIT_AS probe: reserve far more address space than the
		// envelope allows, touching sparsely so an UNlimited run stays
		// cheap. Under the limit the allocation (or the runtime) dies.
		var request []byte
		if mode == "alloc-on-request" {
			var err error
			request, err = readFrame(os.Stdin)
			if err != nil {
				return
			}
		}
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
		if marker := os.Getenv("AII_TEST_ALLOCATED"); marker != "" {
			if err := os.WriteFile(marker, []byte("allocated"), 0o600); err != nil {
				panic(err)
			}
		}
		if request != nil {
			writeFrame(os.Stdout, respond(rawID(request), `{"allocated":true}`))
		}
		// Then serve normally so the un-enveloped control case works.
		serveRespond()

	default:
		serveModes(mode)
	}
}

// heldForTest keeps the "hold" mode's bytes reachable for as long as the
// process lives.
var heldForTest []byte

// wallProbe tries what a native T3 child's containment denies and says
// what happened, one line each: a TCP connection to args[0], and, when
// args[1] is given, creating that file. The verdict is the parent's; the
// probe only reports (pluginhost's contained-child wall test).
func wallProbe(args []string) {
	if len(args) > 0 {
		if c, err := net.DialTimeout("tcp", args[0], 2*time.Second); err != nil {
			fmt.Printf("dial=refused %v\n", err)
		} else {
			c.Close()
			fmt.Println("dial=connected")
		}
	}
	if len(args) > 1 {
		if err := os.WriteFile(args[1], []byte("written from inside"), 0o600); err != nil {
			fmt.Printf("write=refused %v\n", err)
		} else {
			fmt.Println("write=written")
		}
	}
}

func serveRespond() { serveModes("respond") }

// vectorFor answers as a source of vectors does. The broken mode is an
// install that computes a memory another way than its publisher's build
// did: the cue still agrees, the memory does not.
func vectorFor(mode string, frame []byte) string {
	var req struct {
		Params struct {
			Arguments struct {
				Kind string `json:"kind"`
			} `json:"arguments"`
		} `json:"params"`
	}
	_ = json.Unmarshal(frame, &req)
	switch {
	case req.Params.Arguments.Kind == "cue":
		return `{"vector":[0.6,0.8]}`
	case mode == "vectors-broken":
		return `{"vector":[1,0]}`
	}
	return `{"vector":[0.8,0.6]}`
}

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

		case "vectors", "vectors-broken":
			// A source of vectors: one vector for the kind of text it was
			// handed, the two the suite's member signs for its probes.
			writeFrame(os.Stdout, respond(id, vectorFor(mode, frame)))

		case "vectors-slow":

			time.Sleep(300 * time.Millisecond)
			writeFrame(os.Stdout, respond(id, vectorFor(mode, frame)))

		case "crash-after-respond":
			if responded {
				// unreachable: we exit after the first
				os.Exit(7)
			}
			writeFrame(os.Stdout, respond(id, `{"answered":true}`))
			responded = true
			fmt.Fprintln(os.Stderr, "fakechild: crashing after first answer")
			os.Exit(7)

		case "hostcall":
			// One nested upstream request per invocation — the guest-
			// call-forwarding shape without a wasm guest: request up,
			// response down, then answer the original embedding what
			// came back.
			writeFrame(os.Stdout, []byte(`{"jsonrpc":"2.0","id":"c1","method":"invoke.call","params":{"operation":"kv.get","target":{"key":"probe"},"arguments":{}}}`))
			reply, err := readFrame(os.Stdin)
			if err != nil {
				os.Exit(4)
			}
			writeFrame(os.Stdout, respond(id, fmt.Sprintf(`{"upstream":%s}`, reply)))

		case "hostcall-deaf":
			// The nested shape of "deaf": the child asks the host
			// something and then never reads the reply, so a reply larger
			// than the pipe parks the host's write of it. A call that
			// says "listen" is served as hostcall serves it, the reply's
			// size answered in place of the reply.
			writeFrame(os.Stdout, []byte(`{"jsonrpc":"2.0","id":"c1","method":"invoke.call","params":{"operation":"kv.get","target":{"key":"probe"},"arguments":{}}}`))
			if !strings.Contains(string(frame), `"listen"`) {
				block()
			}
			reply, err := readFrame(os.Stdin)
			if err != nil {
				os.Exit(4)
			}
			writeFrame(os.Stdout, respond(id, fmt.Sprintf(`{"upstream_bytes":%d}`, len(reply))))

		case "die-on-request":
			// Take the request and exit without answering it: a death
			// the host notices while it is invoking.
			fmt.Fprintln(os.Stderr, "fakechild: dying with the request unanswered")
			os.Exit(9)

		case "badid":
			writeFrame(os.Stdout, []byte(`{"jsonrpc":"2.0","id":"not-the-id","result":{}}`))

		case "bigframe":
			// Declare one byte over the plugin-side ceiling: the host
			// must refuse without reading and treat the stream as dead.
			var header [4]byte
			binary.BigEndian.PutUint32(header[:], maxFrame+1)
			_, _ = os.Stdout.Write(header[:])
			_, _ = os.Stdout.Write([]byte("junk"))
			block()

		case "sleep":
			// Swallow the request and never answer: the deadline-kill
			// probe (N-8).
			block()

		case "session", "session-audio":
			// The resident session lane (V-W): admit every control request
			// at once, and after a synthesize emit its terminal event as a
			// notification — a method and no id.
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
			// The open's admission NAMES the engine's audio formats (req 4):
			// a fixed FAKE_ENGINE_RATE clock, else a plain engine speaks the
			// endpoints' own clock (echoing the formats the host named), and
			// FAKE_NO_FORMATS admits WITHOUT naming formats so the host's
			// confirmed-binding refusal can be proven.
			if req.Params.Operation == "speech.session.open" {
				// The formats, read from the raw object so that a null input
				// and a missing one stay different things.
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
				// OUTPUT-ONLY, as the contract encodes it: audio.input PRESENT
				// and null, and no input_handle. The admission confirms the
				// topology in the same words. FAKE_OMIT_INPUT answers as an
				// engine that never learned of it would (the key left out),
				// and FAKE_DUPLEX_ANYWAY as one that opened a microphone path
				// nobody asked for — so the host's refusals can be proven.
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
				// FAKE_SPEAK=<samples>: the reply is SPOKEN — its output stream
				// named on this lane with the session it belongs to, then that
				// many samples and an END on the audio pair. No microphone is
				// involved, which is what an output-only session needs proven.
				if n, err := strconv.Atoi(os.Getenv("FAKE_SPEAK")); err == nil && n > 0 {
					speak(req.Params.Arguments.SynthesisID, n)
				}
				writeFrame(os.Stdout, []byte(`{"jsonrpc":"2.0","method":"session.event","params":{"type":"synthesis_end","synthesis_id":"s1","sequence":1}}`))
			}
			if req.Params.Operation == "speech.session.close" && os.Getenv("FAKE_NO_TERMINAL") == "" {
				// Admission first (above), then the terminal event: closure
				// is the engine's word, never the acknowledgement.
				// FAKE_NO_TERMINAL withholds it, so a host that must hold
				// custody + the pin until a real terminal / reap is observable.
				writeFrame(os.Stdout, []byte(fmt.Sprintf(`{"jsonrpc":"2.0","method":"session.event","params":{"type":"session_end","session_id":%q,"sequence":2}}`, currentSession())))
			}
		}
	}
}

// block parks forever without tripping the runtime's deadlock
// detector (a bare select{} in a single-goroutine program is a fatal
// error, which is exactly not the misbehavior these modes simulate).
func block() {
	for {
		time.Sleep(time.Hour)
	}
}

// The engine's session and its audio output, shared by the request loop
// (which speaks) and the loopback (which echoes).
var (
	engineMu   sync.Mutex
	engineSess string
	audioOutMu sync.Mutex
	audioOut   *os.File
	spoken     uint32 = 1000 // spoken streams: a range the loopback's ids never reach
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

// nameStream is the word every output stream is introduced by: the
// session it belongs to, what it carries, and its id.
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

// speak writes one reply of n samples under a stream of its own.
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

// audioEcho is the loopback engine over the inherited audio pair.
// openAudioPair takes the inherited descriptors BEFORE the first control
// is admitted. Opened inside the echo goroutine instead, a synthesize that
// arrived before that goroutine ran found no pair and its reply went
// nowhere — the test read silence and blamed the host. A real engine has
// its device before it answers, and so does this one.
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
		// An engine NAMES an output stream on the control lane before it
		// speaks on it: the host routes audio by that word and by nothing
		// else. The loopback's output stream is the input's id, which the
		// host allocates once per session and never reuses — the same
		// property a real engine's counter has.
		if !named[fr.Stream] {
			named[fr.Stream] = true
			nameStream(fmt.Sprintf("echo-%d", fr.Stream), fr.Stream)
		}
		if err := writeAudio(fr); err != nil {
			return
		}
		// An end ends one session's stream; the pair lives for the spawn,
		// so the next session's frames follow on the same descriptors.
	}
}
