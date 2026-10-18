package pluginhosttest

import (
	"encoding/json"
	"io"

	"github.com/aiii-dot-id/aii-os/internal/supervisor"
)

func SessionClientFrames(frames <-chan []byte, out io.Writer, eventBuf int) *supervisor.SessionClient {
	return supervisor.NewSessionClientFrames(frames, out, nil, eventBuf)
}

func Refusal(op, raw string) error {
	return &supervisor.SessionRefusedError{Op: op, Err: json.RawMessage(raw)}
}

func CallTimedOut(pluginID string, cause error) error {
	return &supervisor.InvokeTimeoutError{PluginID: pluginID, Err: cause}
}

var ErrAdmissionUnknown = supervisor.ErrAdmissionUnknown
