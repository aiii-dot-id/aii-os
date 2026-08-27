// fakechild is the supervisor suite's native-child stand-in: a real
// process speaking framed BBB on stdio per DELTA_D1 D1-1 — exactly the
// contract a native T3 plugin child speaks — with one misbehavior per
// mode so every supervisor promise is provable against a live process.
// Built at test time by the suite's TestMain; never shipped.
package main

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"
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

func writeFrame(w io.Writer, payload []byte) {
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

	if mode == "failstart" {
		fmt.Fprintln(os.Stderr, "fakechild: refusing to start (mode failstart)")
		os.Exit(2)
	}

	// canary reports whether the parent's environment leaked in. The
	// supervisor gives a child EXACTLY spec.Env, so a variable set in
	// the test process must arrive empty here.
	fmt.Fprintf(os.Stderr, "fakechild: child-ready mode=%s socket=%s canary=%q\n",
		mode, os.Getenv("SEV_PLUGIN_SOCKET"), os.Getenv("AII_SUPERVISOR_CANARY"))

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

	case "alloc":
		// The RLIMIT_AS probe: reserve far more address space than the
		// envelope allows, touching sparsely so an UNlimited run stays
		// cheap. Under the limit the allocation (or the runtime) dies.
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
		// Then serve normally so the un-enveloped control case works.
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
