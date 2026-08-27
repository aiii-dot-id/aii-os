package pluginworker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"

	"github.com/aiii-dot-id/aii-os/internal/bbb"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

// HostDispatcher is the narrow seam step 4's capability broker plugs
// into. The worker itself never carries broker logic (KISS: the build
// order gives capability semantics to step 4; this package only owns
// containment).
//
// method is the WIT kebab name of the aiii:bbb/bbb import the guest
// called (e.g. "invoke-call"); params is the raw method-params bytes
// the guest passed (BBB_V2_AUDIT §10.3: imports take method-params
// bytes and return result bytes).
//
// reply is what the guest receives, per the ADR-033 Decision 6 mapping
// (lines 219-223): the BBB method `result` object bytes on success, or
// the structured JSON-RPC `error` object bytes on denial — a denial is
// data the SDK classifies by reasonCode, never a hidden trap. err is
// reserved for transport/internal failure and DOES fail the guest's
// call — the C parity is the host wrapper's callback error path
// (wasm_host.c:2251→wasm_host_component_callback_error).
//
// A Dispatch implementation must not call back into the same Module
// (Invoke/DeliverEvent) synchronously: it runs on the module's
// invocation thread under the invocation lock (the ADR-033 line 161
// bounded-reentrancy rule); deliveries it wants to trigger are queued
// for after the in-flight invoke returns.
type HostDispatcher interface {
	Dispatch(ctx context.Context, method string, params []byte) (reply []byte, err error)
}

// denyAll is the fail-closed default: with no broker attached, every
// guest-outgoing BBB call is denied — never silently succeeds, never
// traps a well-behaved guest. The denial is the audited error-object
// shape (BBB_V2_AUDIT §8: {"code","message","data":{"reasonCode"}} with
// camelCase reasonCode on the wire), code -32000 FORBIDDEN — the
// daemon's capability-denial code; never -32001 (DELTA_D1 §3, finding
// F-2b). reasonCode POLICY_DENY is the audited capability-denial
// vocabulary (sev_rpc.h:128 SEV_RPC_CAP_REQUEST_REASON_POLICY_DENY):
// the step-3 worker's policy grants nothing.
type denyAll struct{}

// denialError is the JSON-RPC error object serialized for the guest.
// Field order fixed by the struct so the bytes are deterministic.
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
		// Marshalling a static struct cannot fail; keep the seam honest anyway.
		return nil, fmt.Errorf("pluginworker: encode denial: %w", err)
	}
	return reply, nil
}

// instantiateBBBHost registers the aiii:bbb/bbb host module in the
// runtime: the eight WIT imports, each with the exact core lowering
// (param i32 i32 i32) -> () — params_ptr, params_len, return-area ptr
// (abi.go). This is the ONLY host module the runtime ever gets: no
// WASI, no clock/env/random/fs. A guest importing anything else never
// reaches instantiation (the import wall in Load).
func instantiateBBBHost(ctx context.Context, rt wazero.Runtime, dispatcher HostDispatcher) error {
	b := rt.NewHostModuleBuilder(BBBWITModule)
	for _, name := range bbbImportNames {
		method := name // capture per-iteration
		b.NewFunctionBuilder().
			WithGoModuleFunction(api.GoModuleFunc(func(ctx context.Context, mod api.Module, stack []uint64) {
				if err := hostCall(ctx, mod, dispatcher, method, stack); err != nil {
					// A host-call failure is the C stack's callback-error
					// path: the guest's call fails, surfacing on the
					// in-flight Invoke. panic is wazero's documented
					// mechanism for raising an error from a Go host
					// function; it does not unwind past the VM boundary.
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

// hostCall performs one guest-outgoing BBB call: read the params list
// from guest memory, dispatch, and lower the reply list back through
// the guest's cabi_realloc into the caller-allocated return area.
func hostCall(ctx context.Context, mod api.Module, dispatcher HostDispatcher, method string, stack []uint64) error {
	paramsPtr := uint32(stack[0])
	paramsLen := uint32(stack[1])
	retPtr := uint32(stack[2])

	// Plugin-side ceiling, inbound: the C host refuses over-budget
	// lists before dispatch (wasm_host.c:1847,
	// SEV_TRANSPORT_DEFAULT_MAX_FRAME_BYTES).
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
		// Copy: the dispatcher must never hold a live view of guest
		// memory (the guest can rewrite it on the next call).
		params = append([]byte(nil), view...)
	}

	reply, derr := dispatcher.Dispatch(ctx, method, params)
	var fatal *FatalDispatchError
	if errors.As(derr, &fatal) {
		// The dispatch CHANNEL is dead (forward-stream desync, guest
		// protocol violation) — nothing sane can be answered; fault the
		// invocation so the module poisons and the supervisor restarts.
		return derr
	}
	if derr != nil {
		// Host-side internal failure: the daemon's -32603 INTERNAL
		// answer (AUDIT §8), byte-identical to the supervised bridge's —
		// and the module SURVIVES. Poison is reserved for guest and
		// containment faults; a broker's internal error must carry the
		// same blast radius on both walls (design pass 2026-08-19 —
		// previously this trapped the guest and poisoned the module,
		// so the same failure killed an in-process plugin but not a
		// supervised one).
		log.Printf("pluginworker: %s dispatch failed: %v (answered -32603)", method, derr)
		reply = []byte(`{"code":-32603,"message":"host dispatch failed"}`)
	}
	// Plugin-side ceiling, outbound: C parity wasm_host.c:2251.
	if len(reply) > bbb.MaxControlFrameBytes {
		return &FrameTooLargeError{Direction: "host-to-guest", Size: len(reply), Limit: bbb.MaxControlFrameBytes}
	}

	// Lower the reply list into guest memory via cabi_realloc — the
	// canonical `canon lower` realloc option; the component runtime
	// does exactly this inside the C host's wasmtime.
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

// guestAlloc allocates n bytes in guest memory through the guest's own
// cabi_realloc(0, 0, align=1, n) — align 1 because list<u8>. A zero
// return is the guest allocator refusing: the resource envelope did its
// job, so the failure is typed as such.
func guestAlloc(ctx context.Context, mod api.Module, n uint32) (uint32, error) {
	realloc := mod.ExportedFunction(ExportRealloc)
	if realloc == nil {
		return 0, &ExportError{Name: ExportRealloc, Reason: "required to lower list values into guest memory"}
	}
	res, err := realloc.Call(ctx, 0, 0, 1, uint64(n))
	if err != nil {
		return 0, err // trap inside the allocator; classified by the caller's invoke path
	}
	ptr := uint32(res[0])
	if ptr == 0 {
		return 0, &ResourceLimitError{Cause: fmt.Errorf("guest cabi_realloc refused %d bytes", n)}
	}
	return ptr, nil
}
