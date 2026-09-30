package pluginhost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/supervisor"
)

const SessionOperationCeiling = 60 * time.Second

type sessionInvoker struct{ voice func() *VoiceSession }

func (s sessionInvoker) Invoke(ctx context.Context, frame []byte) ([]byte, error) {
	var req struct {
		Params struct {
			Operation string                 `json:"operation"`
			Arguments map[string]interface{} `json:"arguments"`
		} `json:"params"`
	}
	if err := json.Unmarshal(frame, &req); err != nil {
		return nil, fmt.Errorf("pluginhost: encode a resident operation: %w", err)
	}
	var v *VoiceSession
	if s.voice != nil {
		v = s.voice()
	}
	if v == nil {
		return nil, errors.New("pluginhost: this activation has no resident session lane")
	}
	if !v.Alive() {
		return nil, fmt.Errorf("%w: %s", ErrSessionDead, v.FaultReason())
	}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, SessionOperationCeiling)
		defer cancel()
	}
	raw, err := v.control(ctx, req.Params.Operation, req.Params.Arguments)
	if err != nil {

		var refused *supervisor.SessionRefusedError
		if errors.As(err, &refused) {
			return json.Marshal(map[string]interface{}{
				"jsonrpc": "2.0", "id": harnessRequestID, "error": json.RawMessage(refused.Err),
			})
		}
		return nil, err
	}
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	return json.Marshal(map[string]interface{}{"jsonrpc": "2.0", "id": harnessRequestID, "result": raw})
}
