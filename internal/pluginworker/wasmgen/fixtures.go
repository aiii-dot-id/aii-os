package wasmgen

// The ten fixture guests, plus the component-artifact fixtures that
// wrap them (bottom of this file). Every guest implements the
// canonical-ABI core lowering of the ADR-033 world pinned in
// ../abi.go:
//
//	exports: memory, cabi_realloc(i32,i32,i32,i32)->i32,
//	         aiii-plugin-bbb-protocol-version()->i32,
//	         aiii-plugin-smoke()->i32,
//	         plugin-invoke(i32,i32)->i32  (return-area pointer)
//
// Shared linear-memory layout (all fixtures):
//
//	@16..23  plugin-invoke return area {ptr:u32le, len:u32le}
//	@24..31  import-call return area (caller.wasm only)
//	@32..39  stashed-event {ptr, len} (event.wasm only)
//	@64…     constant response frame data segment (responder.wasm only)
//	@1024…   bump heap (cabi_realloc; 8-byte aligned, grows memory
//	         one page at a time, traps on denied grow — the same
//	         cabi_oom→unreachable shape as the SDK's own bridge,
//	         sdk/rust/aiii/src/lib.rs:79-81)
//
// The one-page grow step is deliberate: a denied grow leaves memory
// exactly at the host's ceiling, which is what makes the memory-hog
// trap classifiable as a resource kill rather than a generic fault.

// Fixture artifacts by testdata file name.
func Fixtures() map[string][]byte {
	return map[string][]byte{
		"echo.wasm":      Echo(),
		"event.wasm":     Event(),
		"trap.wasm":      Trap(),
		"loop.wasm":      Loop(),
		"memhog.wasm":    MemHog(),
		"bloat.wasm":     Bloat(),
		"wasi.wasm":      WASIImport(),
		"wrongver.wasm":  WrongVersion(),
		"caller.wasm":    Caller(),
		"responder.wasm": Responder(),

		"component-echo.wasm":      ComponentEcho(),
		"component-decoy.wasm":     ComponentDecoyShim(),
		"component-ambig.wasm":     ComponentAmbiguous(),
		"component-nomatch.wasm":   ComponentNoMatch(),
		"component-truncated.wasm": ComponentTruncated(),
		"component-nested.wasm":    ComponentNested(),
	}
}

const (
	retArea    = 16   // plugin-invoke return area
	impRetArea = 24   // return area a guest passes to an aiii:bbb/bbb import
	evtStash   = 32   // {ptr,len} of the last delivered event
	respFrame  = 64   // responder.wasm constant frame data segment
	heapBase   = 1024 // bump allocator start
	oneMiB     = 1 << 20
)

// scaffold assembles the common module skeleton and returns it with the
// resolved indices the specific plugin-invoke body needs.
type scaffold struct {
	m         *Module
	alloc     uint32 // internal allocator func index
	bump      uint32 // bump-pointer global index
	tPI       uint32 // (i32,i32)->i32 type for plugin-invoke
	tPost     uint32 // (i32)->()
	tEvent    uint32 // (i32,i32,i32,i32)->()
	importIdx []uint32
}

type scaffoldOpts struct {
	version    int32 // value of aiii-plugin-bbb-protocol-version
	bbbImports bool  // import the full aiii:bbb/bbb surface
	wasiImport bool  // import wasi_snapshot_preview1.fd_write (forbidden-import probe)
}

func newScaffold(o scaffoldOpts) *scaffold {
	m := NewModule(2) // two pages: layout constants + heap start
	s := &scaffold{m: m}

	tAlloc := m.Type([]byte{vtI32}, []byte{vtI32})
	tRealloc := m.Type([]byte{vtI32, vtI32, vtI32, vtI32}, []byte{vtI32})
	tU32 := m.Type(nil, []byte{vtI32})
	s.tPI = m.Type([]byte{vtI32, vtI32}, []byte{vtI32})
	s.tPost = m.Type([]byte{vtI32}, nil)
	s.tEvent = m.Type([]byte{vtI32, vtI32, vtI32, vtI32}, nil)

	if o.bbbImports {
		tBBB := m.Type([]byte{vtI32, vtI32, vtI32}, nil)
		for _, name := range []string{
			"rpc-connect", "plugin-register-interface", "invoke-call",
			"rpc-cancel", "observe-subscribe", "heartbeat-signal",
			"heartbeat-tempo-request", "heartbeat-config",
		} {
			s.importIdx = append(s.importIdx, m.ImportFunc("aiii:bbb/bbb", name, tBBB))
		}
	}
	if o.wasiImport {
		tWasi := m.Type([]byte{vtI32, vtI32, vtI32, vtI32}, []byte{vtI32})
		s.importIdx = append(s.importIdx, m.ImportFunc("wasi_snapshot_preview1", "fd_write", tWasi))
	}

	s.bump = m.GlobalI32(heapBase)

	// alloc(size) -> ptr: 8-byte-aligned bump; grows one page at a
	// time; unreachable when the host denies growth (cabi_oom shape).
	// local 0 = size, local 1 = p.
	s.alloc = m.Func(tAlloc, 1, cat(
		globalGet(s.bump), localSet(1),
		globalGet(s.bump), localGet(0), opI32Add, i32Const(7), opI32Add, i32Const(-8), opI32And, globalSet(s.bump),
		blockVoid(loopVoid(cat(
			globalGet(s.bump), memorySize(), i32Const(16), opI32Shl, opI32LeU, brIf(1),
			i32Const(1), memoryGrow(), i32Const(-1), opI32Eq, ifVoid(cat(opUnreachable)),
			br(0),
		))),
		localGet(1),
	))
	realloc := m.Func(tRealloc, 0, cat(localGet(3), call(s.alloc)))
	version := m.Func(tU32, 0, i32Const(o.version))
	smoke := m.Func(tU32, 0, i32Const(1))

	m.ExportMemory("memory")
	m.ExportFunc("cabi_realloc", realloc)
	m.ExportFunc("aiii-plugin-bbb-protocol-version", version)
	m.ExportFunc("aiii-plugin-smoke", smoke)
	return s
}

// finish adds plugin-invoke (extraLocals beyond its two params) and
// encodes.
func (s *scaffold) finish(piBody []byte, extraLocals uint32) []byte {
	pi := s.m.Func(s.tPI, extraLocals, piBody)
	s.m.ExportFunc("plugin-invoke", pi)
	return s.m.Encode()
}

// storeRet writes {ptr,len} into the plugin-invoke return area and
// leaves its address on the stack. ptr/len are instruction sequences
// that each push one i32.
func storeRet(ptr, length []byte) []byte {
	return cat(
		i32Const(retArea), ptr, i32Store(0),
		i32Const(retArea), length, i32Store(4),
		i32Const(retArea),
	)
}

// Echo returns the request bytes unchanged, exports the optional
// post-return hook, and counts its calls in the exported global
// "post-calls" so tests can prove the host honored post-return.
func Echo() []byte {
	s := newScaffold(scaffoldOpts{version: 2})
	postCalls := s.m.GlobalI32(0)
	post := s.m.Func(s.tPost, 0, cat(globalGet(postCalls), i32Const(1), opI32Add, globalSet(postCalls)))
	s.m.ExportFunc("cabi_post_plugin-invoke", post)
	s.m.ExportGlobal("post-calls", postCalls)
	// local 2 = copy destination.
	return s.finish(cat(
		localGet(1), call(s.alloc), localSet(2),
		localGet(2), localGet(0), localGet(1), memoryCopy(),
		storeRet(localGet(2), localGet(1)),
	), 1)
}

// Event exports on_event; it stashes topic ++ '\n' ++ payload, and
// plugin-invoke returns the stash. Both entrypoints trap through a
// reentrancy-guard global if the host ever overlaps them — the proof
// mechanism for the ADR-033 line 161 bounded-reentrancy rule.
func Event() []byte {
	s := newScaffold(scaffoldOpts{version: 2})
	guard := s.m.GlobalI32(0)
	enter := cat(globalGet(guard), ifVoid(cat(opUnreachable)), i32Const(1), globalSet(guard))
	leave := cat(i32Const(0), globalSet(guard))
	// on_event(tp,tl,pp,pl); local 4 = buf.
	onEvent := s.m.Func(s.tEvent, 1, cat(
		enter,
		localGet(1), localGet(3), opI32Add, i32Const(1), opI32Add, call(s.alloc), localSet(4),
		localGet(4), localGet(0), localGet(1), memoryCopy(),
		localGet(4), localGet(1), opI32Add, i32Const('\n'), i32Store8(0),
		localGet(4), localGet(1), opI32Add, i32Const(1), opI32Add, localGet(2), localGet(3), memoryCopy(),
		i32Const(evtStash), localGet(4), i32Store(0),
		i32Const(evtStash), localGet(1), i32Const(1), opI32Add, localGet(3), opI32Add, i32Store(4),
		leave,
	))
	s.m.ExportFunc("on_event", onEvent)
	// Copy the stash into the return area, release the guard, THEN
	// push the area address (storeRet would push before the release).
	return s.finish(cat(
		enter,
		i32Const(retArea), i32Const(evtStash), i32Load(0), i32Store(0),
		i32Const(retArea), i32Const(evtStash), i32Load(4), i32Store(4),
		leave,
		i32Const(retArea),
	), 0)
}

// Trap executes unreachable immediately.
func Trap() []byte {
	s := newScaffold(scaffoldOpts{version: 2})
	return s.finish(cat(opUnreachable), 0)
}

// Loop never returns — the deadline-kill probe.
func Loop() []byte {
	s := newScaffold(scaffoldOpts{version: 2})
	return s.finish(cat(loopVoid(br(0)), i32Const(0)), 0)
}

// MemHog grows memory one page at a time until the host denies growth,
// then traps — landing exactly at the ceiling.
func MemHog() []byte {
	s := newScaffold(scaffoldOpts{version: 2})
	return s.finish(cat(
		loopVoid(cat(
			i32Const(1), memoryGrow(), i32Const(-1), opI32Eq, ifVoid(cat(opUnreachable)),
			br(0),
		)),
		i32Const(0),
	), 0)
}

// Bloat answers with a response one byte over the 1 MiB plugin-side
// ceiling — the outbound frame-budget probe.
func Bloat() []byte {
	s := newScaffold(scaffoldOpts{version: 2})
	return s.finish(cat(
		i32Const(oneMiB+1), call(s.alloc), localSet(2),
		storeRet(localGet(2), i32Const(oneMiB+1)),
	), 1)
}

// WASIImport is a conformant guest that additionally imports
// wasi_snapshot_preview1.fd_write — the forbidden-import probe.
func WASIImport() []byte {
	s := newScaffold(scaffoldOpts{version: 2, wasiImport: true})
	return s.finish(storeRet(i32Const(0), i32Const(0)), 0)
}

// WrongVersion reports bbb_protocol_version 1 — the RPC envelope
// number, the WRONG one of the audit's two version numbers — and must
// be refused at admission.
func WrongVersion() []byte {
	s := newScaffold(scaffoldOpts{version: 1})
	return s.finish(storeRet(i32Const(0), i32Const(0)), 0)
}

// Responder ignores the request and answers the one constant frame a
// pluginhost tool handler accepts: a well-formed JSON-RPC response
// echoing the harness's fixed "h1" id with a succeeded invoke.call
// result (BBB_V2_AUDIT §4 response rules, §6.3 result vocabulary).
// Same ptr/len return pattern as echo, sourced from a data segment
// instead of the request.
func Responder() []byte {
	const frame = `{"jsonrpc":"2.0","id":"h1","result":{"status":"succeeded","operation_result":{"echoed":true}}}`
	s := newScaffold(scaffoldOpts{version: 2})
	s.m.Data(respFrame, []byte(frame))
	return s.finish(storeRet(i32Const(respFrame), i32Const(int32(len(frame)))), 0)
}

// Caller imports the full aiii:bbb/bbb surface, forwards the request
// through invoke-call, and returns whatever the host lowered back —
// the fail-closed-stub and import-lowering probe.
func Caller() []byte {
	s := newScaffold(scaffoldOpts{version: 2, bbbImports: true})
	invokeCall := s.importIdx[2] // WIT declaration order: invoke-call is third
	return s.finish(cat(
		localGet(0), localGet(1), i32Const(impRetArea), call(invokeCall),
		storeRet(cat(i32Const(impRetArea), i32Load(0)), cat(i32Const(impRetArea), i32Load(4))),
	), 0)
}

// --- step-4 broker fixtures (test-time parameterized, never checked in) ---
//
// The broker suites build these guests AT TEST TIME because their
// canned invoke-call params can carry values only the running test
// knows (an httptest server's URL). They use the same scaffold and ABI
// as the checked-in fixtures; nothing here joins the testdata
// generator.

// CannedResponder ignores the request and answers the one constant
// frame — the Responder shape with a caller-chosen body. The broker
// suite uses it to build a receipt-FORGING guest: a response whose
// result carries a guest-authored external_receipt, which the harness
// must reject (the daemon-injects rule, invoke_contract.c:598-628).
func CannedResponder(frame []byte) []byte {
	if respFrame+len(frame) > heapBase {
		panic("wasmgen: canned frame collides with the bump heap")
	}
	s := newScaffold(scaffoldOpts{version: 2})
	s.m.Data(respFrame, frame)
	return s.finish(storeRet(i32Const(respFrame), i32Const(int32(len(frame)))), 0)
}

// CannedCaller performs one aiii:bbb/bbb invoke-call per given params
// frame, in order, on every plugin-invoke — the broker-driving guest.
// It answers the harness with a well-formed succeeded response that
// embeds every broker reply VERBATIM as JSON values:
//
//	{"jsonrpc":"2.0","id":"h1","result":{"status":"succeeded",
//	 "operation_result":{"relayed":[<reply1>,<reply2>,…]}}}
//
// A broker reply is always one JSON value (result object bytes or
// error object bytes — hostbbb's ADR-033 Decision 6 mapping), so the
// splice is well-formed either way, and the test sees exactly what the
// guest saw.
func CannedCaller(paramsFrames ...[]byte) []byte {
	if len(paramsFrames) == 0 {
		panic("wasmgen: CannedCaller needs at least one params frame")
	}
	const (
		prefix = `{"jsonrpc":"2.0","id":"h1","result":{"status":"succeeded","operation_result":{"relayed":[`
		suffix = `]}}}`
	)
	s := newScaffold(scaffoldOpts{version: 2, bbbImports: true})
	invokeCall := s.importIdx[2] // WIT declaration order: invoke-call is third

	// Lay the constant data end to end from respFrame up; the builder,
	// not the runtime, proves it stays clear of the bump heap.
	off := int32(respFrame)
	place := func(content []byte) int32 {
		at := off
		s.m.Data(at, content)
		off += int32(len(content))
		if off > heapBase {
			panic("wasmgen: canned params collide with the bump heap")
		}
		return at
	}
	prefixOff := place([]byte(prefix))
	suffixOff := place([]byte(suffix))
	type seg struct {
		off int32
		n   int32
	}
	var params []seg
	for _, p := range paramsFrames {
		params = append(params, seg{place(p), int32(len(p))})
	}

	// Locals: l0,l1 request (unused); l2 buf; l3 cursor; then per call
	// i: l(4+2i) reply ptr, l(5+2i) reply len.
	n := len(params)
	lBuf, lCur := uint32(2), uint32(3)
	lPtr := func(i int) uint32 { return uint32(4 + 2*i) }
	lLen := func(i int) uint32 { return uint32(5 + 2*i) }

	var body []byte
	// Phase 1: perform every invoke-call, stashing each reply.
	for i, p := range params {
		body = cat(body,
			i32Const(p.off), i32Const(p.n), i32Const(impRetArea), call(invokeCall),
			i32Const(impRetArea), i32Load(0), localSet(lPtr(i)),
			i32Const(impRetArea), i32Load(4), localSet(lLen(i)),
		)
	}
	// Phase 2: total = prefix + Σlen + (n-1) separators + suffix.
	body = cat(body, i32Const(int32(len(prefix)+len(suffix)+n-1)))
	for i := range params {
		body = cat(body, localGet(lLen(i)), opI32Add)
	}
	body = cat(body, call(s.alloc), localSet(lBuf), localGet(lBuf), localSet(lCur))
	copyAdvance := func(srcPush []byte, lenPush []byte) {
		body = cat(body,
			localGet(lCur), srcPush, lenPush, memoryCopy(),
			localGet(lCur), lenPush, opI32Add, localSet(lCur),
		)
	}
	copyAdvance(i32Const(prefixOff), i32Const(int32(len(prefix))))
	for i := range params {
		if i > 0 {
			body = cat(body,
				localGet(lCur), i32Const(','), i32Store8(0),
				localGet(lCur), i32Const(1), opI32Add, localSet(lCur),
			)
		}
		copyAdvance(localGet(lPtr(i)), localGet(lLen(i)))
	}
	copyAdvance(i32Const(suffixOff), i32Const(int32(len(suffix))))
	// Phase 3: return {buf, cursor-buf}.
	body = cat(body, storeRet(localGet(lBuf), cat(localGet(lCur), localGet(lBuf), []byte{0x6B} /* i32.sub */)))
	return s.finish(body, uint32(2+2*n))
}

// --- component artifacts (the unwrapping fixtures) ---
//
// Assembly mirrors the vendored wit-component output shape verified in
// ../component.go: component preamble, core-module sections carrying
// complete core binaries, every other section skipped uninterpreted.
// The wrapped guests are the SAME core modules as above — a component
// admission must behave byte-identically to the raw module's.

// DecoyShim models wit-component's auxiliary "shim" module (vendored
// encoding.rs:1216-1218): function exports named "0","1",… — shim
// names are decimal strings (encoding.rs:2976) — no memory, and none
// of the world surface. Candidate selection must skip it wherever it
// sits in section order. (The real shim also exports the "$imports"
// funcref table, encoding.rs:93,1180 — irrelevant to selection, and
// this encoder has no table support; KISS.)
func DecoyShim() []byte {
	m := NewModule(0) // no memory, like the real shim
	// The (i32,i32,i32)->() shape the BBB import trampolines carry.
	t := m.Type([]byte{vtI32, vtI32, vtI32}, nil)
	m.ExportFunc("0", m.Func(t, 0, nil))
	m.ExportFunc("1", m.Func(t, 0, nil))
	return m.Encode()
}

// ComponentEcho is the minimal valid component: preamble plus one
// core-module section wrapping the echo guest, nothing else.
func ComponentEcho() []byte {
	return cat(componentHeader(), section(secComponentCoreModule, Echo()))
}

// ComponentDecoyShim proves candidate selection: the decoy shim comes
// FIRST, non-module sections sit between (a custom section, and an
// alias-id section with opaque bytes — the walker must skip both by
// declared length without interpreting), and the real guest comes
// last.
func ComponentDecoyShim() []byte {
	return cat(componentHeader(),
		section(secComponentCustom, cat(nameBytes("fixture:decoy"), []byte("skip me"))),
		section(secComponentCoreModule, DecoyShim()),
		// Opaque payload: a real alias section decodes differently,
		// but the walker never looks inside.
		section(secComponentAlias, []byte{0xAA, 0xBB, 0xCC}),
		section(secComponentCoreModule, Echo()),
	)
}

// ComponentAmbiguous embeds TWO world-exporting guests — admission
// must refuse to guess which one runs.
func ComponentAmbiguous() []byte {
	return cat(componentHeader(),
		section(secComponentCoreModule, Echo()),
		section(secComponentCoreModule, Echo()),
	)
}

// ComponentNoMatch embeds only the decoy shim — nothing runnable.
func ComponentNoMatch() []byte {
	return cat(componentHeader(), section(secComponentCoreModule, DecoyShim()))
}

// ComponentTruncated declares a 100-byte core-module section but
// carries three bytes — the section-overrun probe.
func ComponentTruncated() []byte {
	return cat(componentHeader(), []byte{secComponentCoreModule}, uleb(100), []byte{0x00, 0x61, 0x73})
}

// ComponentNested embeds a whole (empty) component as a nested
// component section AFTER a valid echo module: rejection must fire on
// the nesting even though a runnable candidate exists — fail closed
// beats best effort.
func ComponentNested() []byte {
	return cat(componentHeader(),
		section(secComponentCoreModule, Echo()),
		section(secComponentNested, componentHeader()),
	)
}
