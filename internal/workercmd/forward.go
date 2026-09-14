package workercmd

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .

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

// .
// .
// .
// .
// .
// .
func (f *forwardDispatcher) Dispatch(ctx context.Context, method string, params []byte) ([]byte, error) {
	wire, ok := bbb.MethodForImport(method)
	if !ok {
		// .
		return nil, &pluginworker.FatalDispatchError{Err: fmt.Errorf("forward: %q is not an aiii:bbb/bbb import", method)}
	}
	if len(params) == 0 {
		// .
		// .
		params = []byte("{}")
	}
	if !json.Valid(params) {
		// .
		// .
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

	// .
	// .
	// .
	// .
	// .
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
		// .
		// .
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
		// .
		// .
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
