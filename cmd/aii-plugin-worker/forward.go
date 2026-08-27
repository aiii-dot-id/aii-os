package main

// The -forward dispatcher: guest-call forwarding across the process
// boundary (build-order step 5). In forward mode the worker does not
// answer guest-outgoing aiii:bbb/bbb calls with the in-process
// deny-all stub — it serializes each hostcall as a BBB REQUEST frame
// on stdout and awaits the host's response frame on stdin, making the
// one stdio pair fully duplex (DELTA_D1 D1-1: both streams carry only
// frames; the supervisor's bridge answers on the other side).
//
// Frame discipline (the audited classification, adopted): a frame WITH
// "method" is a request, a frame WITHOUT it carrying id + exactly one
// of result|error is a response (rpc.c:3433-3441). Interleaving is
// strictly nested — one guest invocation at a time (module lock), so
// during an in-flight downstream request the only traffic is this
// dispatcher's upstream request/response pairs. Both loops therefore
// read stdin from the SAME goroutine, alternating by protocol phase,
// and neither side buffers: os.Stdin/os.Stdout are read and written
// directly so a frame is visible to the peer the moment it is written
// (wrapping either stream in bufio would deadlock the nesting).
//
// Ids: this side mints string ids "w1","w2",… — the C SDK client
// precedent (bbb_client.c:800: a string holding a decimal counter) —
// and requires the echo byte-form verbatim (AUDIT finding F-1).
//
// The guest-facing reply contract is unchanged from the in-process
// wall (ADR-033 Decision 6): the response's result member bytes on
// success, the error member bytes on denial — the guest SDK cannot
// tell which wall answered it.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/bbb"
	"github.com/aiii-dot-id/aii-os/internal/pluginworker"
)

type forwardDispatcher struct {
	in  *os.File
	out *os.File
	seq uint64
}

func newForwardDispatcher() *forwardDispatcher {
	return &forwardDispatcher{in: os.Stdin, out: os.Stdout}
}

// Dispatch implements pluginworker.HostDispatcher over the stdio pair.
// A returned error fails the guest's call through the host-function
// error path (the module poisons and the worker exits for restart) —
// correct here because every error case below is a dead or lawless
// stream, not a semantic denial; denials travel as error OBJECTS in
// the reply bytes.
func (f *forwardDispatcher) Dispatch(ctx context.Context, method string, params []byte) ([]byte, error) {
	wire, ok := bbb.MethodForImport(method)
	if !ok {
		// Unreachable: the import wall admits exactly the eight names.
		return nil, &pluginworker.FatalDispatchError{Err: fmt.Errorf("forward: %q is not an aiii:bbb/bbb import", method)}
	}
	if len(params) == 0 {
		// The SDK contract: params is always an object, defaulting to
		// {} (AUDIT §4; bbb_client.c:653-657).
		params = []byte("{}")
	}
	if !json.Valid(params) {
		// The guest's params bytes are embedded raw in the request
		// envelope; non-JSON bytes would poison the frame.
		return nil, &pluginworker.FatalDispatchError{Err: fmt.Errorf("forward: guest %s params are not valid JSON", method)}
	}

	f.seq++
	id := fmt.Sprintf(`"w%d"`, f.seq)
	frame, err := json.Marshal(map[string]json.RawMessage{
		"jsonrpc": json.RawMessage(`"2.0"`),
		"id":      json.RawMessage(id),
		"method":  json.RawMessage(fmt.Sprintf("%q", wire)),
		"params":  json.RawMessage(params),
	})
	if err != nil {
		return nil, &pluginworker.FatalDispatchError{Err: fmt.Errorf("forward: encode %s request: %w", wire, err)}
	}
	if err := bbb.WriteFrame(f.out, frame, bbb.MaxControlFrameBytes); err != nil {
		return nil, &pluginworker.FatalDispatchError{Err: fmt.Errorf("forward: write %s request: %w", wire, err)}
	}

	// Await the host's response. Best-effort read deadline from the
	// invocation context where the platform supports pipe deadlines;
	// the authoritative deadline is the HOST's (N-8: on expiry the
	// supervisor kills this process — a blocked read here cannot
	// outlive it).
	if deadline, has := ctx.Deadline(); has {
		if f.in.SetReadDeadline(deadline) == nil {
			defer func() { _ = f.in.SetReadDeadline(time.Time{}) }()
		}
	}
	reply, err := bbb.ReadFrame(f.in, bbb.MaxControlFrameBytes)
	if err != nil {
		if errors.Is(err, os.ErrDeadlineExceeded) {
			return nil, &pluginworker.FatalDispatchError{Err: fmt.Errorf("forward: %s response deadline exceeded", wire)}
		}
		// EOF or framing violation mid-call: the connection is the
		// process's lifetime (D1-1 rule 3) and it just ended.
		return nil, &pluginworker.FatalDispatchError{Err: fmt.Errorf("forward: read %s response: %w", wire, err)}
	}

	var members map[string]json.RawMessage
	if err := json.Unmarshal(reply, &members); err != nil {
		return nil, &pluginworker.FatalDispatchError{Err: fmt.Errorf("forward: %s response is not a JSON object", wire)}
	}
	var version string
	if err := json.Unmarshal(members["jsonrpc"], &version); err != nil || version != "2.0" {
		return nil, &pluginworker.FatalDispatchError{Err: fmt.Errorf("forward: %s response jsonrpc member invalid", wire)}
	}
	if _, hasMethod := members["method"]; hasMethod {
		// A request is not a response: the host must never initiate a
		// new request while ours is outstanding (the nesting rule).
		return nil, &pluginworker.FatalDispatchError{Err: fmt.Errorf("forward: host sent a request while %s awaited its response", wire)}
	}
	if string(members["id"]) != id {
		return nil, &pluginworker.FatalDispatchError{Err: fmt.Errorf("forward: %s response id %s does not echo %s byte-form verbatim", wire, members["id"], id)}
	}
	resultRaw, hasResult := members["result"]
	errorRaw, hasError := members["error"]
	if hasResult == hasError {
		return nil, &pluginworker.FatalDispatchError{Err: fmt.Errorf("forward: %s response must carry exactly one of result|error", wire)}
	}
	if hasError {
		return errorRaw, nil
	}
	return resultRaw, nil
}
