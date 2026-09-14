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
// .
// .
// .
// .
// .
// .
// .
// .
// .
package pluginworker

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/aiii-dot-id/aii-os/internal/bbb"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

const (
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	DefaultMemoryMaxBytes = 64 << 20

	// .
	wasmPageBytes = 64 * 1024
)

// .
var errClosed = errors.New("pluginworker: module closed")

// .
// .
type Config struct {
	// .
	// .
	// .
	// .
	MemoryMaxBytes uint64

	// .
	// .
	// .
	// .
	Dispatcher HostDispatcher
}

// .
// .
// .
// .
// .
// .
type Module struct {
	rt           wazero.Runtime
	mod          api.Module
	mem          api.Memory
	invoke       api.Function
	post         api.Function
	onEvent      api.Function
	describe     api.Function
	postDescribe api.Function
	capBytes     uint64
	class        ArtifactClass

	mu    sync.Mutex
	fatal error
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
// .
// .
// .
// .
// .
func Load(ctx context.Context, wasmBytes []byte, cfg Config) (m *Module, err error) {
	capBytes := cfg.MemoryMaxBytes
	if capBytes == 0 {
		capBytes = DefaultMemoryMaxBytes
	}
	pages := capBytes / wasmPageBytes
	if pages == 0 {
		return nil, fmt.Errorf("pluginworker: memory cap %d bytes is below one wasm page (%d)", capBytes, wasmPageBytes)
	}
	if pages > 65536 {
		return nil, fmt.Errorf("pluginworker: memory cap %d bytes exceeds wasm32 addressing (4 GiB)", capBytes)
	}
	dispatcher := cfg.Dispatcher
	if dispatcher == nil {
		dispatcher = denyAll{}
	}

	core, class, uerr := unwrapArtifact(wasmBytes)
	if uerr != nil {
		return nil, uerr
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
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	rt := wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfig().
		WithCloseOnContextDone(true).
		WithMemoryLimitPages(uint32(pages)))
	defer func() {
		if err != nil {
			_ = rt.Close(ctx)
		}
	}()

	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	if minPages, declared, perr := declaredMemoryMinPages(core); perr == nil && declared && minPages > pages {
		return nil, &ResourceLimitError{LimitBytes: capBytes,
			Cause: fmt.Errorf("declared memory minimum %d pages exceeds the envelope of %d pages", minPages, pages)}
	}
	compiled, cerr := rt.CompileModule(ctx, core)
	if cerr != nil {
		return nil, fmt.Errorf("pluginworker: compile module: %w", cerr)
	}

	if werr := checkImportWall(compiled); werr != nil {
		return nil, werr
	}

	// .
	// .
	if herr := instantiateBBBHost(ctx, rt, dispatcher); herr != nil {
		return nil, herr
	}

	// .
	// .
	// .
	mod, ierr := rt.InstantiateModule(ctx, compiled, wazero.NewModuleConfig().
		WithName("plugin").
		WithStartFunctions(ExportInitialize))
	if ierr != nil {
		return nil, fmt.Errorf("pluginworker: instantiate module: %w", ierr)
	}

	m = &Module{rt: rt, mod: mod, capBytes: capBytes, class: class}
	if xerr := m.bindExports(); xerr != nil {
		return nil, xerr
	}
	if aerr := m.admit(ctx); aerr != nil {
		return nil, aerr
	}
	return m, nil
}

// .
// .
// .
func (m *Module) ArtifactClass() ArtifactClass { return m.class }

// .
// .
// .
// .
// .
// .
// .
func checkImportWall(compiled wazero.CompiledModule) error {
	allowed := make(map[string]bool, len(bbbImportNames))
	for _, n := range bbbImportNames {
		allowed[n] = true
	}
	for _, def := range compiled.ImportedFunctions() {
		module, name, _ := def.Import()
		switch {
		case module != BBBWITModule:
			return &ForbiddenImportError{Module: module, Name: name,
				Reason: fmt.Sprintf("only the %s surface is provided; no WASI, no ambient host modules", BBBWITModule)}
		case !allowed[name]:
			return &ForbiddenImportError{Module: module, Name: name,
				Reason: "not one of the eight audited aiii:bbb/bbb functions"}
		case !typesEqual(def.ParamTypes(), []api.ValueType{api.ValueTypeI32, api.ValueTypeI32, api.ValueTypeI32}) ||
			len(def.ResultTypes()) != 0:
			return &ForbiddenImportError{Module: module, Name: name,
				Reason: "signature is not the canonical lowering (param i32 i32 i32) -> ()"}
		}
	}
	for _, mem := range compiled.ImportedMemories() {
		module, name, _ := mem.Import()
		return &ForbiddenImportError{Module: module, Name: name,
			Reason: "imported memories are refused; the guest owns its linear memory"}
	}
	// .
	// .
	// .
	return nil
}

// .
func (m *Module) bindExports() error {
	mem := m.mod.ExportedMemory(ExportMemory)
	if mem == nil {
		return &ExportError{Name: ExportMemory, Reason: "missing (the canonical lowering exports the linear memory)"}
	}
	m.mem = mem

	i32 := api.ValueTypeI32
	need := []struct {
		name    string
		params  []api.ValueType
		results []api.ValueType
	}{
		{ExportProtocolVersion, nil, []api.ValueType{i32}},
		{ExportSmoke, nil, []api.ValueType{i32}},
		{ExportPluginInvoke, []api.ValueType{i32, i32}, []api.ValueType{i32}},
		{ExportRealloc, []api.ValueType{i32, i32, i32, i32}, []api.ValueType{i32}},
	}
	defs := m.mod.ExportedFunctionDefinitions()
	for _, want := range need {
		def, ok := defs[want.name]
		if !ok {
			return &ExportError{Name: want.name, Reason: "missing required world export"}
		}
		if !typesEqual(def.ParamTypes(), want.params) || !typesEqual(def.ResultTypes(), want.results) {
			return &ExportError{Name: want.name, Reason: "core signature does not match the canonical lowering"}
		}
	}
	m.invoke = m.mod.ExportedFunction(ExportPluginInvoke)

	// .
	if def, ok := defs[ExportPostReturn]; ok {
		if !typesEqual(def.ParamTypes(), []api.ValueType{i32}) || len(def.ResultTypes()) != 0 {
			return &ExportError{Name: ExportPostReturn, Reason: "post-return must be (param i32) -> ()"}
		}
		m.post = m.mod.ExportedFunction(ExportPostReturn)
	}
	if def, ok := defs[ExportDescribe]; ok {
		if len(def.ParamTypes()) != 0 || !typesEqual(def.ResultTypes(), []api.ValueType{i32}) {
			return &ExportError{Name: ExportDescribe, Reason: "describe must be () -> i32 (a return-area pointer)"}
		}
		m.describe = m.mod.ExportedFunction(ExportDescribe)
		if pdef, ok := defs[ExportPostDescribe]; ok {
			if !typesEqual(pdef.ParamTypes(), []api.ValueType{i32}) || len(pdef.ResultTypes()) != 0 {
				return &ExportError{Name: ExportPostDescribe, Reason: "post-describe must be (param i32) -> ()"}
			}
			m.postDescribe = m.mod.ExportedFunction(ExportPostDescribe)
		}
	}
	if def, ok := defs[ExportOnEvent]; ok {
		if !typesEqual(def.ParamTypes(), []api.ValueType{i32, i32, i32, i32}) || len(def.ResultTypes()) != 0 {
			return &ExportError{Name: ExportOnEvent, Reason: "on_event must be (param i32 i32 i32 i32) -> ()"}
		}
		m.onEvent = m.mod.ExportedFunction(ExportOnEvent)
	}
	return nil
}

// .
// .
func (m *Module) admit(ctx context.Context) error {
	got, err := m.callU32(ctx, ExportProtocolVersion)
	if err != nil {
		return fmt.Errorf("pluginworker: admission call %s: %w", ExportProtocolVersion, m.classify(err))
	}
	if got != RequiredProtocolVersion {
		return &ProtocolVersionError{Got: got}
	}
	got, err = m.callU32(ctx, ExportSmoke)
	if err != nil {
		return fmt.Errorf("pluginworker: admission call %s: %w", ExportSmoke, m.classify(err))
	}
	if got != RequiredSmokeCode {
		return &SmokeCodeError{Got: got}
	}
	return nil
}

func (m *Module) callU32(ctx context.Context, name string) (uint32, error) {
	res, err := m.mod.ExportedFunction(name).Call(ctx)
	if err != nil {
		return 0, err
	}
	return uint32(res[0]), nil
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
// .
// .
func (m *Module) Invoke(ctx context.Context, frame []byte) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fatal != nil {
		return nil, &ModuleUnusableError{Cause: m.fatal}
	}
	if len(frame) == 0 {
		// .
		// .
		return nil, errors.New("pluginworker: refusing to invoke with an empty frame")
	}
	if len(frame) > bbb.MaxControlFrameBytes {
		return nil, &FrameTooLargeError{Direction: "host-to-guest", Size: len(frame), Limit: bbb.MaxControlFrameBytes}
	}

	// .
	// .
	reqPtr, err := guestAlloc(ctx, m.mod, uint32(len(frame)))
	if err != nil {
		return nil, m.fail(m.classify(err))
	}
	if !m.mem.Write(reqPtr, frame) {
		return nil, m.fail(&AbiError{Detail: fmt.Sprintf("cabi_realloc returned (ptr=%d,len=%d) outside linear memory", reqPtr, len(frame))})
	}

	res, err := m.invoke.Call(ctx, uint64(reqPtr), uint64(len(frame)))
	if err != nil {
		return nil, m.fail(m.classify(err))
	}

	// .
	retPtr := uint32(res[0])
	respPtr, ok1 := m.mem.ReadUint32Le(retPtr)
	respLen, ok2 := m.mem.ReadUint32Le(retPtr + 4)
	if !ok1 || !ok2 {
		return nil, m.fail(&AbiError{Detail: fmt.Sprintf("plugin-invoke return area (ptr=%d) outside linear memory", retPtr)})
	}
	if respLen > bbb.MaxControlFrameBytes {
		// .
		// .
		return nil, m.fail(&FrameTooLargeError{Direction: "guest-to-host", Size: int(respLen), Limit: bbb.MaxControlFrameBytes})
	}
	var out []byte
	if respLen > 0 {
		view, ok := m.mem.Read(respPtr, respLen)
		if !ok {
			return nil, m.fail(&AbiError{Detail: fmt.Sprintf("plugin-invoke response (ptr=%d,len=%d) outside linear memory", respPtr, respLen)})
		}
		out = append([]byte(nil), view...)
	}
	if m.post != nil {
		if _, perr := m.post.Call(ctx, uint64(retPtr)); perr != nil {
			return nil, m.fail(m.classify(perr))
		}
	}
	return out, nil
}

// .
// .
// .
// .
// .
// .
func (m *Module) Describe(ctx context.Context) ([]byte, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.describe == nil {
		return nil, false, nil
	}
	if m.fatal != nil {
		return nil, true, &ModuleUnusableError{Cause: m.fatal}
	}
	res, err := m.describe.Call(ctx)
	if err != nil {
		return nil, true, m.fail(m.classify(err))
	}
	retPtr := uint32(res[0])
	ptr, ok1 := m.mem.ReadUint32Le(retPtr)
	n, ok2 := m.mem.ReadUint32Le(retPtr + 4)
	if !ok1 || !ok2 {
		return nil, true, m.fail(&AbiError{Detail: fmt.Sprintf("describe return area (ptr=%d) outside linear memory", retPtr)})
	}
	if n > bbb.MaxControlFrameBytes {
		return nil, true, m.fail(&FrameTooLargeError{Direction: "guest-to-host", Size: int(n), Limit: bbb.MaxControlFrameBytes})
	}
	var out []byte
	if n > 0 {
		view, ok := m.mem.Read(ptr, n)
		if !ok {
			return nil, true, m.fail(&AbiError{Detail: fmt.Sprintf("describe list (ptr=%d,len=%d) outside linear memory", ptr, n)})
		}
		out = append([]byte(nil), view...)
	}
	if m.postDescribe != nil {
		if _, perr := m.postDescribe.Call(ctx, uint64(retPtr)); perr != nil {
			return nil, true, m.fail(m.classify(perr))
		}
	}
	return out, true, nil
}

// .
// .
func (m *Module) HasOnEvent() bool { return m.onEvent != nil }

// .
// .
// .
// .
// .
// .
// .
func (m *Module) DeliverEvent(ctx context.Context, topic string, payload []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fatal != nil {
		return &ModuleUnusableError{Cause: m.fatal}
	}
	if m.onEvent == nil {
		return ErrNoOnEvent
	}
	if topic == "" {
		return errors.New("pluginworker: refusing to deliver an event without a topic")
	}
	if len(topic) > bbb.MaxControlFrameBytes {
		return &FrameTooLargeError{Direction: "host-to-guest", Size: len(topic), Limit: bbb.MaxControlFrameBytes}
	}
	if len(payload) > bbb.MaxControlFrameBytes {
		return &FrameTooLargeError{Direction: "host-to-guest", Size: len(payload), Limit: bbb.MaxControlFrameBytes}
	}

	topicPtr, err := guestAlloc(ctx, m.mod, uint32(len(topic)))
	if err != nil {
		return m.fail(m.classify(err))
	}
	if !m.mem.Write(topicPtr, []byte(topic)) {
		return m.fail(&AbiError{Detail: "cabi_realloc returned a topic buffer outside linear memory"})
	}
	payloadPtr := uint32(0)
	if len(payload) > 0 {
		payloadPtr, err = guestAlloc(ctx, m.mod, uint32(len(payload)))
		if err != nil {
			return m.fail(m.classify(err))
		}
		if !m.mem.Write(payloadPtr, payload) {
			return m.fail(&AbiError{Detail: "cabi_realloc returned a payload buffer outside linear memory"})
		}
	}
	if _, err := m.onEvent.Call(ctx,
		uint64(topicPtr), uint64(len(topic)), uint64(payloadPtr), uint64(len(payload))); err != nil {
		return m.fail(m.classify(err))
	}
	return nil
}

// .
// .
func (m *Module) Close(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fatal == nil {
		m.fatal = errClosed
	}
	return m.rt.Close(ctx)
}

// .
// .
func (m *Module) fail(err error) error {
	if m.fatal == nil {
		m.fatal = err
	}
	return err
}

// .
func (m *Module) classify(err error) error {
	// .
	// .
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return &TimeoutError{Err: err}
	}
	// .
	// .
	var fe *FrameTooLargeError
	if errors.As(err, &fe) {
		return fe
	}
	var ae *AbiError
	if errors.As(err, &ae) {
		return ae
	}
	var re *ResourceLimitError
	if errors.As(err, &re) {
		if re.LimitBytes == 0 {
			re.LimitBytes = m.capBytes
		}
		return re
	}
	var ee *ExportError
	if errors.As(err, &ee) {
		return ee
	}
	// .
	// .
	// .
	// .
	trap := &TrapError{Reason: firstLine(err.Error())}
	if !m.mod.IsClosed() && uint64(m.mem.Size()) >= m.capFloorBytes() {
		return &ResourceLimitError{LimitBytes: m.capBytes, Cause: trap}
	}
	return trap
}

// .
func (m *Module) capFloorBytes() uint64 {
	return (m.capBytes / wasmPageBytes) * wasmPageBytes
}

func typesEqual(got, want []api.ValueType) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// .
// .
// .
// .
func declaredMemoryMinPages(wasm []byte) (minPages uint64, declared bool, err error) {
	if len(wasm) < 8 || string(wasm[:4]) != "\x00asm" {
		return 0, false, fmt.Errorf("not a wasm binary")
	}
	pos := 8
	readLEB := func() (uint64, error) {
		var v uint64
		var shift uint
		for {
			if pos >= len(wasm) {
				return 0, fmt.Errorf("truncated LEB128")
			}
			b := wasm[pos]
			pos++
			v |= uint64(b&0x7f) << shift
			if b&0x80 == 0 {
				return v, nil
			}
			shift += 7
			if shift > 63 {
				return 0, fmt.Errorf("LEB128 overflow")
			}
		}
	}
	for pos < len(wasm) {
		id := wasm[pos]
		pos++
		size, err := readLEB()
		if err != nil {
			return 0, false, err
		}
		if uint64(pos)+size > uint64(len(wasm)) {
			return 0, false, fmt.Errorf("section %d overruns the binary", id)
		}
		if id != 5 {
			pos += int(size)
			continue
		}
		end := pos + int(size)
		count, err := readLEB()
		if err != nil {
			return 0, false, err
		}
		if count == 0 {
			return 0, false, nil
		}
		if pos >= end {
			return 0, false, fmt.Errorf("memory section truncated")
		}
		pos++
		min, err := readLEB()
		if err != nil {
			return 0, false, err
		}
		return min, true, nil
	}
	return 0, false, nil
}
