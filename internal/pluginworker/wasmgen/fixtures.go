package wasmgen

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
	retArea    = 16
	impRetArea = 24
	evtStash   = 32
	respFrame  = 64
	descFrame  = 512
	heapBase   = 1024
	oneMiB     = 1 << 20
)

// .
// .
type scaffold struct {
	m         *Module
	alloc     uint32
	bump      uint32
	tPI       uint32
	tPost     uint32
	tEvent    uint32
	importIdx []uint32
}

type scaffoldOpts struct {
	version    int32
	bbbImports bool
	wasiImport bool
}

func newScaffold(o scaffoldOpts) *scaffold {
	m := NewModule(2)
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

	// .
	// .
	// .
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

// .
// .
func (s *scaffold) finish(piBody []byte, extraLocals uint32) []byte {
	pi := s.m.Func(s.tPI, extraLocals, piBody)
	s.m.ExportFunc("plugin-invoke", pi)
	return s.m.Encode()
}

// .
// .
// .
func storeRet(ptr, length []byte) []byte {
	return cat(
		i32Const(retArea), ptr, i32Store(0),
		i32Const(retArea), length, i32Store(4),
		i32Const(retArea),
	)
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
func Echo() []byte {
	s := newScaffold(scaffoldOpts{version: 2})
	postCalls := s.m.GlobalI32(0)
	post := s.m.Func(s.tPost, 0, cat(globalGet(postCalls), i32Const(1), opI32Add, globalSet(postCalls), i32Const(heapBase), globalSet(s.bump)))
	s.m.ExportFunc("cabi_post_plugin-invoke", post)
	s.m.ExportGlobal("post-calls", postCalls)
	// .
	return s.finish(cat(
		localGet(1), call(s.alloc), localSet(2),
		localGet(2), localGet(0), localGet(1), memoryCopy(),
		storeRet(localGet(2), localGet(1)),
	), 1)
}

// .
// .
// .
// .
func Event() []byte {
	s := newScaffold(scaffoldOpts{version: 2})
	guard := s.m.GlobalI32(0)
	enter := cat(globalGet(guard), ifVoid(cat(opUnreachable)), i32Const(1), globalSet(guard))
	leave := cat(i32Const(0), globalSet(guard))
	// .
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
	// .
	// .
	return s.finish(cat(
		enter,
		i32Const(retArea), i32Const(evtStash), i32Load(0), i32Store(0),
		i32Const(retArea), i32Const(evtStash), i32Load(4), i32Store(4),
		leave,
		i32Const(retArea),
	), 0)
}

// .
func Trap() []byte {
	s := newScaffold(scaffoldOpts{version: 2})
	return s.finish(cat(opUnreachable), 0)
}

// .
func Loop() []byte {
	s := newScaffold(scaffoldOpts{version: 2})
	return s.finish(cat(loopVoid(br(0)), i32Const(0)), 0)
}

// .
// .
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

// .
// .
func Bloat() []byte {
	s := newScaffold(scaffoldOpts{version: 2})
	return s.finish(cat(
		i32Const(oneMiB+1), call(s.alloc), localSet(2),
		storeRet(localGet(2), i32Const(oneMiB+1)),
	), 1)
}

// .
// .
func WASIImport() []byte {
	s := newScaffold(scaffoldOpts{version: 2, wasiImport: true})
	return s.finish(storeRet(i32Const(0), i32Const(0)), 0)
}

// .
// .
// .
func WrongVersion() []byte {
	s := newScaffold(scaffoldOpts{version: 1})
	return s.finish(storeRet(i32Const(0), i32Const(0)), 0)
}

// .
// .
// .
// .
// .
// .
func Responder() []byte {
	const frame = `{"jsonrpc":"2.0","id":"h1","result":{"status":"succeeded","operation_result":{"echoed":true}}}`
	s := newScaffold(scaffoldOpts{version: 2})
	s.m.Data(respFrame, []byte(frame))
	return s.finish(storeRet(i32Const(respFrame), i32Const(int32(len(frame)))), 0)
}

// .
// .
// .
func Caller() []byte {
	s := newScaffold(scaffoldOpts{version: 2, bbbImports: true})
	invokeCall := s.importIdx[2]
	return s.finish(cat(
		localGet(0), localGet(1), i32Const(impRetArea), call(invokeCall),
		storeRet(cat(i32Const(impRetArea), i32Load(0)), cat(i32Const(impRetArea), i32Load(4))),
	), 0)
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
func CannedResponder(frame []byte) []byte {
	if respFrame+len(frame) > heapBase {
		panic("wasmgen: canned frame collides with the bump heap")
	}
	s := newScaffold(scaffoldOpts{version: 2})
	s.m.Data(respFrame, frame)
	return s.finish(storeRet(i32Const(respFrame), i32Const(int32(len(frame)))), 0)
}

// .
// .
// .
// .
func DescribingResponder(descriptor []byte) []byte {
	const frame = `{"jsonrpc":"2.0","id":"h1","result":{"status":"succeeded","operation_result":{"echoed":true}}}`
	if descFrame+len(descriptor) > heapBase {
		panic("wasmgen: descriptor collides with the bump heap")
	}
	s := newScaffold(scaffoldOpts{version: 2})
	s.m.Data(respFrame, []byte(frame))
	s.m.Data(descFrame, descriptor)
	tU32 := s.m.Type(nil, []byte{vtI32})
	describe := s.m.Func(tU32, 0, storeRet(i32Const(descFrame), i32Const(int32(len(descriptor)))))
	s.m.ExportFunc("aiii-plugin-describe", describe)
	return s.finish(storeRet(i32Const(respFrame), i32Const(int32(len(frame)))), 0)
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
func CannedCaller(paramsFrames ...[]byte) []byte {
	if len(paramsFrames) == 0 {
		panic("wasmgen: CannedCaller needs at least one params frame")
	}
	const (
		prefix = `{"jsonrpc":"2.0","id":"h1","result":{"status":"succeeded","operation_result":{"relayed":[`
		suffix = `]}}}`
	)
	s := newScaffold(scaffoldOpts{version: 2, bbbImports: true})
	invokeCall := s.importIdx[2]

	// .
	// .
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

	// .
	// .
	n := len(params)
	lBuf, lCur := uint32(2), uint32(3)
	lPtr := func(i int) uint32 { return uint32(4 + 2*i) }
	lLen := func(i int) uint32 { return uint32(5 + 2*i) }

	var body []byte
	// .
	for i, p := range params {
		body = cat(body,
			i32Const(p.off), i32Const(p.n), i32Const(impRetArea), call(invokeCall),
			i32Const(impRetArea), i32Load(0), localSet(lPtr(i)),
			i32Const(impRetArea), i32Load(4), localSet(lLen(i)),
		)
	}
	// .
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
	// .
	body = cat(body, storeRet(localGet(lBuf), cat(localGet(lCur), localGet(lBuf), []byte{0x6B})))
	return s.finish(body, uint32(2+2*n))
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
func DecoyShim() []byte {
	m := NewModule(0)
	// .
	t := m.Type([]byte{vtI32, vtI32, vtI32}, nil)
	m.ExportFunc("0", m.Func(t, 0, nil))
	m.ExportFunc("1", m.Func(t, 0, nil))
	return m.Encode()
}

// .
// .
func ComponentEcho() []byte {
	return cat(componentHeader(), section(secComponentCoreModule, Echo()))
}

// .
// .
// .
// .
// .
func ComponentDecoyShim() []byte {
	return cat(componentHeader(),
		section(secComponentCustom, cat(nameBytes("fixture:decoy"), []byte("skip me"))),
		section(secComponentCoreModule, DecoyShim()),
		// .
		// .
		section(secComponentAlias, []byte{0xAA, 0xBB, 0xCC}),
		section(secComponentCoreModule, Echo()),
	)
}

// .
// .
func ComponentAmbiguous() []byte {
	return cat(componentHeader(),
		section(secComponentCoreModule, Echo()),
		section(secComponentCoreModule, Echo()),
	)
}

// .
func ComponentNoMatch() []byte {
	return cat(componentHeader(), section(secComponentCoreModule, DecoyShim()))
}

// .
// .
func ComponentTruncated() []byte {
	return cat(componentHeader(), []byte{secComponentCoreModule}, uleb(100), []byte{0x00, 0x61, 0x73})
}

// .
// .
// .
// .
func ComponentNested() []byte {
	return cat(componentHeader(),
		section(secComponentCoreModule, Echo()),
		section(secComponentNested, componentHeader()),
	)
}
