package supervisor

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
	"io"
	"sync"
	"sync/atomic"

	"github.com/aiii-dot-id/aii-os/internal/bbb"
)

var (
	// .
	// .
	ErrNotAdmitted = errors.New("session control not admitted: the request never reached the engine")
	// .
	// .
	// .
	ErrAdmissionUnknown = errors.New("session control admission unknown: sent, no reply")
)

const (
	upstreamQueue   = 64
	upstreamWorkers = 16
	outboundQueue   = 64
)

// .
// .
var telemetryEvents = map[string]bool{"vad_probability": true, "transcript_partial": true}

// .
type SessionClient struct {
	in     io.Reader
	frames <-chan []byte
	out    io.Writer
	disp   Dispatcher

	rdOnce sync.Once
	rdCh   chan readResult

	outQ       chan []byte
	writerOnce sync.Once

	nextID  atomic.Uint64
	pendMu  sync.Mutex
	pending map[uint64]chan hostReply

	events  chan json.RawMessage
	dropped atomic.Uint64
	faulted atomic.Bool
	faultMu sync.Mutex
	fault   string

	upQ chan upstreamReq

	closeOnce sync.Once
	closed    chan struct{}
}

type hostReply struct {
	result json.RawMessage
	errObj json.RawMessage
}
type readResult struct {
	frame []byte
	err   error
}
type upstreamReq struct {
	id, method json.RawMessage
	params     json.RawMessage
}

// .
// .
func NewSessionClient(in io.Reader, out io.Writer, disp Dispatcher, eventBuf int) *SessionClient {
	if eventBuf <= 0 {
		eventBuf = 256
	}
	return &SessionClient{
		in: in, out: out, disp: disp,
		pending: make(map[uint64]chan hostReply),
		events:  make(chan json.RawMessage, eventBuf),
		closed:  make(chan struct{}),
		outQ:    make(chan []byte, outboundQueue),
		upQ:     make(chan upstreamReq, upstreamQueue),
	}
}

// .
// .
// .
func NewSessionClientFrames(frames <-chan []byte, out io.Writer, disp Dispatcher, eventBuf int) *SessionClient {
	c := NewSessionClient(nil, out, disp, eventBuf)
	c.frames = frames
	return c
}

// .
// .
// .
func (c *SessionClient) Events() <-chan json.RawMessage { return c.events }

// .
func (c *SessionClient) Dropped() uint64 { return c.dropped.Load() }

// .
// .
// .
func (c *SessionClient) Faulted() bool { return c.faulted.Load() }
func (c *SessionClient) FaultReason() string {
	c.faultMu.Lock()
	defer c.faultMu.Unlock()
	return c.fault
}

// .
// .
// .
// .
func (c *SessionClient) Done() <-chan struct{} { return c.closed }

func (c *SessionClient) setFault(reason string) {
	if c.faulted.CompareAndSwap(false, true) {
		c.faultMu.Lock()
		c.fault = reason
		c.faultMu.Unlock()
	}
}

// .
// .
// .
// .
// .
// .
func (c *SessionClient) end() {
	c.closeOnce.Do(func() { close(c.closed) })
}

// .
// .
var errLaneEnded = errors.New("the lane ended")

// .
// .
// .
// .
// .
func (c *SessionClient) Run(ctx context.Context) error {
	c.startWriter()
	for i := 0; i < upstreamWorkers; i++ {
		go c.upstreamWorker(ctx)
	}
	defer func() {
		c.end()
		close(c.events)
	}()
	for {
		frame, err := c.next(ctx)
		if err == io.EOF {
			return nil
		}
		if errors.Is(err, errLaneEnded) {
			if r := c.FaultReason(); r != "" {
				return fmt.Errorf("supervisor: session ended: %s", r)
			}
			return nil
		}
		if err != nil {
			return fmt.Errorf("supervisor: session read: %w", err)
		}
		var m struct {
			ID     json.RawMessage `json:"id"`
			Method json.RawMessage `json:"method"`
			Result json.RawMessage `json:"result"`
			Error  json.RawMessage `json:"error"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(frame, &m) != nil {
			continue
		}
		switch {
		case len(m.Method) == 0 && len(m.ID) != 0:
			c.deliverReply(m.ID, hostReply{result: m.Result, errObj: m.Error})
		case len(m.Method) != 0 && len(m.ID) == 0:
			c.observe(frame)
		case len(m.Method) != 0 && len(m.ID) != 0:
			// .
			// .
			select {
			case c.upQ <- upstreamReq{id: m.ID, method: m.Method, params: m.Params}:
			default:
				c.setFault("upstream hostcall queue saturated; a guest request was dropped")
			}
		}
	}
}

// .
// .
// .
// .
func (c *SessionClient) next(ctx context.Context) ([]byte, error) {
	if c.frames != nil {
		select {
		case frame, ok := <-c.frames:
			if !ok {
				return nil, io.EOF
			}
			return frame, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-c.closed:
			return nil, errLaneEnded
		}
	}
	c.rdOnce.Do(func() {
		c.rdCh = make(chan readResult, 1)
		go func() {
			for {
				frame, err := bbb.ReadFrame(c.in, bbb.MaxServerFrameBytes)
				select {
				case c.rdCh <- readResult{frame, err}:
				case <-c.closed:
					return
				}
				if err != nil {
					return
				}
			}
		}()
	})
	select {
	case r := <-c.rdCh:
		return r.frame, r.err
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.closed:
		return nil, errLaneEnded
	}
}

// .
// .
// .
func (c *SessionClient) startWriter() {
	c.writerOnce.Do(func() {
		go func() {
			for {
				select {
				case frame := <-c.outQ:
					if err := bbb.WriteFrame(c.out, frame, bbb.MaxControlFrameBytes); err != nil {
						c.setFault("transport write failed: " + err.Error())
						c.end()
						return
					}
				case <-c.closed:
					return
				}
			}
		}()
	})
}

// .
// .
func (c *SessionClient) send(ctx context.Context, frame []byte) error {
	c.startWriter()
	select {
	case c.outQ <- frame:
		return nil
	case <-c.closed:
		return ErrNotAdmitted
	case <-ctx.Done():
		return ErrNotAdmitted
	}
}

// .
// .
// .
func (c *SessionClient) Control(ctx context.Context, op string, args any) (json.RawMessage, error) {
	argsRaw, err := json.Marshal(args)
	if err != nil {
		return nil, fmt.Errorf("supervisor: encode control args: %w", err)
	}
	id := c.nextID.Add(1)
	ch := make(chan hostReply, 1)
	c.pendMu.Lock()
	c.pending[id] = ch
	c.pendMu.Unlock()
	defer func() {
		c.pendMu.Lock()
		delete(c.pending, id)
		c.pendMu.Unlock()
	}()
	frame := []byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"invoke.call","params":{"operation":%q,"arguments":%s}}`, id, op, argsRaw))
	if err := c.send(ctx, frame); err != nil {
		return nil, err
	}
	select {
	case reply := <-ch:
		if len(reply.errObj) != 0 {
			return nil, &SessionRefusedError{Op: op, Err: reply.errObj}
		}
		return reply.result, nil
	case <-ctx.Done():
		return nil, ErrAdmissionUnknown
	case <-c.closed:
		return nil, ErrAdmissionUnknown
	}
}

// .
// .
// .
// .
// .
// .
// .
// .
func (c *SessionClient) ControlEnqueue(ctx context.Context, op string, args any) (func(context.Context) (json.RawMessage, error), error) {
	argsRaw, err := json.Marshal(args)
	if err != nil {
		return nil, fmt.Errorf("supervisor: encode control args: %w", err)
	}
	id := c.nextID.Add(1)
	ch := make(chan hostReply, 1)
	c.pendMu.Lock()
	c.pending[id] = ch
	c.pendMu.Unlock()
	release := func() {
		c.pendMu.Lock()
		delete(c.pending, id)
		c.pendMu.Unlock()
	}
	frame := []byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"invoke.call","params":{"operation":%q,"arguments":%s}}`, id, op, argsRaw))
	if err := c.send(ctx, frame); err != nil {
		release()
		return nil, err
	}
	var once sync.Once
	return func(ctx context.Context) (json.RawMessage, error) {
		defer once.Do(release)
		select {
		case reply := <-ch:
			if len(reply.errObj) != 0 {
				return nil, &SessionRefusedError{Op: op, Err: reply.errObj}
			}
			return reply.result, nil
		case <-ctx.Done():
			return nil, ErrAdmissionUnknown
		case <-c.closed:
			return nil, ErrAdmissionUnknown
		}
	}, nil
}

// .
// .
// .
// .
func (c *SessionClient) observe(frame []byte) {
	var m struct {
		Params json.RawMessage `json:"params"`
	}
	_ = json.Unmarshal(frame, &m)
	ev := m.Params
	if len(ev) == 0 {
		ev = frame
	}
	if telemetryEvents[eventType(ev)] {
		select {
		case c.events <- ev:
		default:
			c.dropped.Add(1)
		}
		return
	}
	for {
		select {
		case c.events <- ev:
			return
		default:
		}
		select {
		case old := <-c.events:
			if telemetryEvents[eventType(old)] {
				c.dropped.Add(1)
				continue
			}
			// .
			c.setFault("critical observation shed: the observer fell too far behind")
			continue
		default:
			continue
		}
	}
}

func eventType(ev json.RawMessage) string {
	var t struct {
		Type string `json:"type"`
	}
	_ = json.Unmarshal(ev, &t)
	return t.Type
}

func (c *SessionClient) upstreamWorker(ctx context.Context) {
	for {
		select {
		case req := <-c.upQ:
			var method string
			if json.Unmarshal(req.method, &method) != nil {
				continue
			}
			importName, known := bbb.ImportForMethod(method)
			var reply []byte
			switch {
			case !known:
				reply = upstreamError(req.id, fmt.Sprintf("unknown method %q", method))
			case c.disp == nil:
				reply = upstreamError(req.id, "no dispatcher: every hostcall is denied")
			default:
				resp, derr := c.disp.Dispatch(ctx, importName, req.params)
				if derr != nil {
					reply = upstreamError(req.id, derr.Error())
				} else {
					reply = upstreamResult(req.id, resp)
				}
			}
			_ = c.send(ctx, reply)
		case <-c.closed:
			return
		case <-ctx.Done():
			return
		}
	}
}

func (c *SessionClient) deliverReply(idRaw json.RawMessage, reply hostReply) {
	var id uint64
	if json.Unmarshal(idRaw, &id) != nil {
		return
	}
	c.pendMu.Lock()
	ch := c.pending[id]
	c.pendMu.Unlock()
	if ch != nil {
		select {
		case ch <- reply:
		default:
		}
	}
}

func upstreamResult(idRaw, result json.RawMessage) []byte {
	if len(idRaw) == 0 {
		idRaw = json.RawMessage("null")
	}
	if len(result) == 0 {
		result = json.RawMessage("null")
	}
	f := append([]byte(`{"jsonrpc":"2.0","id":`), idRaw...)
	f = append(f, []byte(`,"result":`)...)
	f = append(f, result...)
	return append(f, '}')
}

func upstreamError(idRaw json.RawMessage, message string) []byte {
	if len(idRaw) == 0 {
		idRaw = json.RawMessage("null")
	}
	msg, _ := json.Marshal(message)
	f := append([]byte(`{"jsonrpc":"2.0","id":`), idRaw...)
	f = append(f, []byte(`,"error":{"code":-32000,"message":`)...)
	f = append(f, msg...)
	return append(f, []byte(`}}`)...)
}

// .
// .
type SessionRefusedError struct {
	Op  string
	Err json.RawMessage
}

func (e *SessionRefusedError) Error() string {
	return fmt.Sprintf("session control %q refused: %s", e.Op, e.Err)
}
