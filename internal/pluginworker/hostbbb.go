package pluginworker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/logsink"

	"github.com/aiii-dot-id/aii-os/internal/bbb"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

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
type HostDispatcher interface {
	Dispatch(ctx context.Context, method string, params []byte) (reply []byte, err error)
}

// .
// .
// .
// .
// .
// .
// .
// .
// .
type denyAll struct{}

// .
// .
type denialError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    denialCause `json:"data"`
}

type denialCause struct {
	ReasonCode string `json:"reasonCode"`
}

func (denyAll) Dispatch(_ context.Context, method string, _ []byte) ([]byte, error) {
	reply, err := json.Marshal(denialError{
		Code:    -32000,
		Message: fmt.Sprintf("no capability broker attached to this worker; %s denied", method),
		Data:    denialCause{ReasonCode: "POLICY_DENY"},
	})
	if err != nil {
		// .
		return nil, fmt.Errorf("pluginworker: encode denial: %w", err)
	}
	return reply, nil
}

// .
// .
// .
// .
// .
// .
func instantiateBBBHost(ctx context.Context, rt wazero.Runtime, dispatcher HostDispatcher) error {
	b := rt.NewHostModuleBuilder(BBBWITModule)
	for _, name := range bbbImportNames {
		method := name
		b.NewFunctionBuilder().
			WithGoModuleFunction(api.GoModuleFunc(func(ctx context.Context, mod api.Module, stack []uint64) {
				if err := hostCall(ctx, mod, dispatcher, method, stack); err != nil {
					// .
					// .
					// .
					// .
					// .
					panic(err)
				}
			}), []api.ValueType{api.ValueTypeI32, api.ValueTypeI32, api.ValueTypeI32}, nil).
			Export(method)
	}
	_, err := b.Instantiate(ctx)
	if err != nil {
		return fmt.Errorf("pluginworker: instantiate %s host module: %w", BBBWITModule, err)
	}
	return nil
}

// .
// .
// .
func hostCall(ctx context.Context, mod api.Module, dispatcher HostDispatcher, method string, stack []uint64) error {
	paramsPtr := uint32(stack[0])
	paramsLen := uint32(stack[1])
	retPtr := uint32(stack[2])

	// .
	// .
	// .
	if paramsLen > bbb.MaxControlFrameBytes {
		return &FrameTooLargeError{Direction: "guest-to-host", Size: int(paramsLen), Limit: bbb.MaxControlFrameBytes}
	}

	mem := mod.Memory()
	if mem == nil {
		return &AbiError{Detail: "host call from a module with no memory"}
	}
	var params []byte
	if paramsLen > 0 {
		view, ok := mem.Read(paramsPtr, paramsLen)
		if !ok {
			return &AbiError{Detail: fmt.Sprintf("%s params (ptr=%d,len=%d) outside linear memory", method, paramsPtr, paramsLen)}
		}
		// .
		// .
		params = append([]byte(nil), view...)
	}

	reply, derr := dispatcher.Dispatch(ctx, method, params)
	var fatal *FatalDispatchError
	if errors.As(derr, &fatal) {
		// .
		// .
		// .
		return derr
	}
	if derr != nil {
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		logsink.Warn("plugins.error", "%s dispatch failed: %v (answered -32603)", method, derr)
		reply = []byte(`{"code":-32603,"message":"host dispatch failed"}`)
	}
	// .
	if len(reply) > bbb.MaxControlFrameBytes {
		return &FrameTooLargeError{Direction: "host-to-guest", Size: len(reply), Limit: bbb.MaxControlFrameBytes}
	}

	// .
	// .
	// .
	replyPtr := uint32(0)
	if len(reply) > 0 {
		p, err := guestAlloc(ctx, mod, uint32(len(reply)))
		if err != nil {
			return err
		}
		replyPtr = p
		if !mem.Write(replyPtr, reply) {
			return &AbiError{Detail: fmt.Sprintf("cabi_realloc returned (ptr=%d,len=%d) outside linear memory", replyPtr, len(reply))}
		}
	}
	if !mem.WriteUint32Le(retPtr, replyPtr) || !mem.WriteUint32Le(retPtr+4, uint32(len(reply))) {
		return &AbiError{Detail: fmt.Sprintf("%s return area (ptr=%d) outside linear memory", method, retPtr)}
	}
	return nil
}

// .
// .
// .
// .
func guestAlloc(ctx context.Context, mod api.Module, n uint32) (uint32, error) {
	realloc := mod.ExportedFunction(ExportRealloc)
	if realloc == nil {
		return 0, &ExportError{Name: ExportRealloc, Reason: "required to lower list values into guest memory"}
	}
	res, err := realloc.Call(ctx, 0, 0, 1, uint64(n))
	if err != nil {
		return 0, err
	}
	ptr := uint32(res[0])
	if ptr == 0 {
		return 0, &ResourceLimitError{Cause: fmt.Errorf("guest cabi_realloc refused %d bytes", n)}
	}
	return ptr, nil
}
