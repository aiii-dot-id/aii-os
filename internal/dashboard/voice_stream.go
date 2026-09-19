package dashboard

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
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"sync"
	"sync/atomic"

	"github.com/aiii-dot-id/aii-os/internal/audio"
	"github.com/coder/websocket"
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
const (
	voiceStreamVersion     = 2
	voiceStreamHeaderBytes = 24
	// .
	// .
	voiceStreamBuffer = 256
)

// .
// .
// .
type VoiceSession interface {
	ID() string
	Finish(ctx context.Context, endSample int64) error
	Close(ctx context.Context, mode string) error
	// .
	// .
	Interrupt(ctx context.Context) error
	// .
	// .
	PlaybackReport(ctx context.Context, r PlaybackReport) error
	Label() string
	Done() <-chan struct{}
	Released() <-chan struct{}
	// .
	// .
	// .
	InputClosed() <-chan struct{}
	InputCompletionReason() string
}

// .
type VoiceRequest struct {
	Action   string          `json:"action"`
	Mode     string          `json:"mode,omitempty"`
	Rate     int             `json:"rate,omitempty"`
	Channels int             `json:"channels,omitempty"`
	Playback *PlaybackReport `json:"playback,omitempty"`
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
type PlaybackReport struct {
	SessionID string `json:"session_id"`
	Stream    uint32 `json:"stream"`
	Rendered  int64  `json:"rendered"`
	Rate      int    `json:"rate"`
	Channels  int    `json:"channels"`
	Terminal  bool   `json:"terminal"`
	Outcome   string `json:"outcome"`
}

// .
type VoiceSessionState struct {
	SessionID string `json:"session_id,omitempty"`
	State     string `json:"state"`
	Label     string `json:"label,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

// .
// .
// .
// .
// .
// .
type VoiceEvent struct {
	SessionID string `json:"session_id,omitempty"`
	Sequence  int64  `json:"sequence,omitempty"`
	Type      string `json:"type"`
	Text      string `json:"text,omitempty"`
	Speaker   string `json:"speaker,omitempty"`
	// .
	// .
	SpeakerID string `json:"speaker_id,omitempty"`
	Final     bool   `json:"final,omitempty"`
	Reason    string `json:"reason,omitempty"`
	// .
	// .
	Revision uint64 `json:"revision,omitempty"`
	// .
	// .
	// .
	Operator bool `json:"operator,omitempty"`
	// .
	// .
	// .
	Stream uint32 `json:"stream,omitempty"`
	// .
	// .
	// .
	// .
	// .
	RefersTo    int64    `json:"refers_to,omitempty"`
	Decision    string   `json:"decision,omitempty"`
	Score       *float64 `json:"score,omitempty"`
	Late        bool     `json:"late,omitempty"`
	Attribution string   `json:"attribution,omitempty"`
}

// .
// .
const voiceOutputMode = "output"

// .
// .
type connVoice struct {
	session VoiceSession
	mic     *browserSource
	micID   string
	spkID   string
	format  audio.Format
}

// .
// .
// .
type browserSource struct {
	f       audio.Format
	frames  chan audio.Frame
	closed  chan struct{}
	once    sync.Once
	mu      sync.Mutex
	gap     bool
	pending *audio.Frame
	dropped atomic.Uint64
}

func newBrowserSource(f audio.Format) *browserSource {
	return &browserSource{f: f, frames: make(chan audio.Frame, voiceStreamBuffer), closed: make(chan struct{})}
}

func (b *browserSource) Format() audio.Format { return b.f }

func (b *browserSource) push(fr audio.Frame) {
	select {
	case b.frames <- fr:
	default:
		b.mu.Lock()
		b.gap = true
		b.mu.Unlock()
		b.dropped.Add(1)
	}
}

// .
// .
func (b *browserSource) end() { b.once.Do(func() { close(b.closed) }) }

func (b *browserSource) Read(ctx context.Context) (audio.Frame, error) {
	b.mu.Lock()
	if p := b.pending; p != nil {
		b.pending = nil
		b.mu.Unlock()
		return *p, nil
	}
	b.mu.Unlock()
	select {
	case fr := <-b.frames:
		b.mu.Lock()
		if b.gap && fr.Kind == audio.KindPCM {
			// .
			// .
			b.gap = false
			cp := fr
			b.pending = &cp
			b.mu.Unlock()
			return audio.Frame{Kind: audio.KindDiscontinuity, Stream: fr.Stream, Start: fr.Start}, nil
		}
		b.mu.Unlock()
		return fr, nil
	case <-b.closed:
		return audio.Frame{}, io.EOF
	case <-ctx.Done():
		return audio.Frame{}, ctx.Err()
	}
}

// .
// .
type browserSink struct {
	s    *Server
	conn *websocket.Conn
	f    audio.Format
}

func (k *browserSink) Format() audio.Format { return k.f }
func (k *browserSink) Write(ctx context.Context, fr audio.Frame) error {
	return k.s.sendBinary(ctx, k.conn, encodeVoiceStream(k.f, fr))
}
func (k *browserSink) Close() error { return nil }

// .
func encodeVoiceStream(f audio.Format, fr audio.Frame) []byte {
	out := make([]byte, voiceStreamHeaderBytes+len(fr.PCM))
	out[0] = voiceStreamVersion
	out[1] = byte(f.Channels)
	out[2] = byte(fr.Kind)
	binary.LittleEndian.PutUint32(out[4:8], uint32(f.Rate))
	binary.LittleEndian.PutUint32(out[8:12], fr.Seq)
	binary.LittleEndian.PutUint64(out[12:20], uint64(fr.Start))
	binary.LittleEndian.PutUint32(out[20:24], uint32(fr.Stream))
	copy(out[voiceStreamHeaderBytes:], fr.PCM)
	return out
}

// .
func decodeVoiceStream(data []byte) (audio.Format, audio.Frame, error) {
	if len(data) < voiceStreamHeaderBytes {
		return audio.Format{}, audio.Frame{}, fmt.Errorf("voice stream frame is shorter than its header")
	}
	if data[3] != 0 {
		return audio.Format{}, audio.Frame{}, fmt.Errorf("voice stream frame sets a reserved header byte this identity does not understand")
	}
	f := audio.Format{Rate: int(binary.LittleEndian.Uint32(data[4:8])), Channels: int(data[1])}
	if f.Channels <= 0 || f.Channels > 2 || f.Rate < 8000 || f.Rate > 192000 {
		return audio.Format{}, audio.Frame{}, fmt.Errorf("voice stream frame declares a format that is not audio")
	}
	fr := audio.Frame{Kind: audio.Kind(data[2]), Stream: uint32(binary.LittleEndian.Uint32(data[20:24])), Seq: binary.LittleEndian.Uint32(data[8:12]), Start: int64(binary.LittleEndian.Uint64(data[12:20]))}
	switch fr.Kind {
	case audio.KindPCM:
		if (len(data)-voiceStreamHeaderBytes)%f.BytesPerSample() != 0 {
			return audio.Format{}, audio.Frame{}, fmt.Errorf("voice stream frame is not a whole number of samples for the channel count it declares")
		}
		fr.PCM = append([]byte(nil), data[voiceStreamHeaderBytes:]...)
	case audio.KindDiscontinuity, audio.KindEnd:
		if len(data) != voiceStreamHeaderBytes {
			return audio.Format{}, audio.Frame{}, fmt.Errorf("a voice stream frame of kind %d carries no samples", fr.Kind)
		}
	default:
		return audio.Format{}, audio.Frame{}, fmt.Errorf("voice stream frame kind %d is not one this identity understands", fr.Kind)
	}
	return f, fr, nil
}

var voiceConnSeq atomic.Uint64

// .
// .
func (s *Server) handleVoiceSession(ctx context.Context, conn *websocket.Conn, req *VoiceRequest) {
	h := s.currentHandler()
	if req == nil {
		s.sendError(ctx, conn, "voice_session needs an action")
		return
	}
	cl := s.client(conn)
	if cl == nil {
		return
	}
	switch req.Action {
	case "open":
		if h.AudioPlane == nil || h.VoiceSessionOpen == nil || h.VoiceEngine == nil || !h.VoiceEngine() {
			s.sendVoiceState(ctx, conn, VoiceSessionState{State: "refused", Reason: "no speech engine is active"})
			return
		}
		f := audio.Format{Rate: req.Rate, Channels: req.Channels}
		if f.Channels <= 0 || f.Channels > 2 || f.Rate < 8000 || f.Rate > 192000 {
			s.sendVoiceState(ctx, conn, VoiceSessionState{State: "refused", Reason: "the page must declare its capture rate and channels"})
			return
		}
		mode := req.Mode
		if mode == "" {
			mode = "meeting"
		}
		cl.vmu.Lock()
		if cl.voice != nil {
			cl.vmu.Unlock()
			s.sendVoiceState(ctx, conn, VoiceSessionState{State: "refused", Reason: "this page already holds a session"})
			return
		}
		cl.vmu.Unlock()
		n := voiceConnSeq.Add(1)
		cv := &connVoice{spkID: fmt.Sprintf("browser:%d:spk", n), format: f}
		plane := h.AudioPlane()
		// .
		// .
		// .
		// .
		// .
		if mode != voiceOutputMode {
			cv.mic, cv.micID = newBrowserSource(f), fmt.Sprintf("browser:%d:mic", n)
			if err := plane.Register(&audio.Endpoint{ID: cv.micID, Label: "browser", Source: cv.mic}); err != nil {
				s.sendVoiceState(ctx, conn, VoiceSessionState{State: "refused", Reason: err.Error()})
				return
			}
		}
		unregister := func() {
			if cv.micID != "" {
				_ = plane.Unregister(cv.micID)
			}
			_ = plane.Unregister(cv.spkID)
		}
		if err := plane.Register(&audio.Endpoint{ID: cv.spkID, Label: "browser", Sink: &browserSink{s: s, conn: conn, f: f}}); err != nil {
			if cv.micID != "" {
				_ = plane.Unregister(cv.micID)
			}
			s.sendVoiceState(ctx, conn, VoiceSessionState{State: "refused", Reason: err.Error()})
			return
		}
		sess, err := h.VoiceSessionOpen(ctx, cv.micID, cv.spkID, mode)
		if err != nil {
			unregister()
			s.sendVoiceState(ctx, conn, VoiceSessionState{State: "refused", Reason: err.Error()})
			return
		}
		cv.session = sess
		cl.vmu.Lock()
		cl.voice = cv
		cl.vmu.Unlock()
		// .
		// .
		// .
		// .
		log.Printf("VOICE: session %s opened by the page at %s (%s; mode %s; endpoints %s, %s)", sess.ID(), cl.addr, clip(cl.agent, 60), mode, cv.micID, cv.spkID)
		s.sendVoiceState(ctx, conn, VoiceSessionState{SessionID: sess.ID(), State: "open", Label: sess.Label()})
		// .
		// .
		// .
		// .
		// .
		// .
		go func() {
			select {
			case <-sess.InputClosed():
			case <-sess.Done():
				return
			}
			select {
			case <-sess.Done():
				return
			default:
			}
			why := sess.InputCompletionReason()
			log.Printf("VOICE: session %s stopped accepting speech (%s)", sess.ID(), clip(why, 120))
			s.sendVoiceState(ctx, conn, VoiceSessionState{SessionID: sess.ID(), State: "input_complete", Label: sess.Label(), Reason: why})
		}()
		// .
		// .
		go func() {
			<-sess.Done()
			log.Printf("VOICE: session %s of the page at %s ended (%s)", sess.ID(), cl.addr, sess.Label())
			s.sendVoiceState(context.Background(), conn, VoiceSessionState{SessionID: sess.ID(), State: "closed", Label: sess.Label()})
			if cv.mic != nil {
				cv.mic.end()
			}
			<-sess.Released()
			unregister()
			cl.vmu.Lock()
			if cl.voice == cv {
				cl.voice = nil
			}
			cl.vmu.Unlock()
		}()
	case "interrupt":
		cl.vmu.Lock()
		cv := cl.voice
		cl.vmu.Unlock()
		if cv == nil {
			return
		}
		// .
		// .
		go func() {
			if err := cv.session.Interrupt(context.Background()); err != nil {
				s.sendError(context.Background(), conn, "could not interrupt the engine: "+err.Error())
			}
		}()
	case "playback":
		// .
		// .
		// .
		// .
		// .
		cl.vmu.Lock()
		cv := cl.voice
		cl.vmu.Unlock()
		r := req.Playback
		if cv == nil || r == nil {
			s.sendVoiceEvent(ctx, conn, VoiceEvent{Type: "receipt_refused", Reason: "no voice session is open on this page, or the report is empty"})
			return
		}
		if r.SessionID != cv.session.ID() {
			s.sendVoiceEvent(ctx, conn, VoiceEvent{SessionID: r.SessionID, Type: "receipt_refused", Stream: r.Stream, Reason: fmt.Sprintf("the report names session %q; this page holds %q", r.SessionID, cv.session.ID())})
			return
		}
		// .
		go func() {
			if err := cv.session.PlaybackReport(context.Background(), *r); err != nil {
				s.sendVoiceEvent(context.Background(), conn, VoiceEvent{SessionID: r.SessionID, Type: "receipt_refused", Stream: r.Stream, Reason: err.Error()})
			}
		}()
	case "close", "abort":
		cl.vmu.Lock()
		cv := cl.voice
		cl.vmu.Unlock()
		if cv == nil {
			s.sendError(ctx, conn, "no voice session is open")
			return
		}
		mode := "drain"
		if req.Action == "abort" {
			mode = "abort"
		}
		if err := cv.session.Close(ctx, mode); err != nil {
			s.sendError(ctx, conn, "could not close the voice session: "+err.Error())
		}
	default:
		s.sendError(ctx, conn, "voice_session action must be open, close, abort, interrupt or playback")
	}
}

// .
// .
// .
func (s *Server) handleVoiceStream(ctx context.Context, conn *websocket.Conn, data []byte) {
	cl := s.client(conn)
	if cl == nil {
		return
	}
	cl.vmu.Lock()
	cv := cl.voice
	cl.vmu.Unlock()
	if cv == nil {
		s.sendError(ctx, conn, "no voice session is open — open one before streaming")
		return
	}
	if cv.mic == nil {
		// .
		// .
		// .
		s.sendError(ctx, conn, "this page's voice session has no microphone — it was opened to speak replies only")
		return
	}
	f, fr, err := decodeVoiceStream(data)
	if err != nil {
		s.sendError(ctx, conn, err.Error())
		return
	}
	if f != cv.format {
		s.sendError(ctx, conn, fmt.Sprintf("voice stream frame is %s but the session was opened at %s", f, cv.format))
		return
	}
	cv.mic.push(fr)
	if fr.Kind == audio.KindEnd {
		// .
		// .
		// .
		go func() {
			if err := cv.session.Finish(context.Background(), fr.Start); err != nil {
				s.sendError(context.Background(), conn, "could not finish the input: "+err.Error())
			}
		}()
	}
}

// .
// .
// .
func (s *Server) dropVoiceSession(conn *websocket.Conn) {
	cl := s.client(conn)
	if cl == nil {
		return
	}
	cl.vmu.Lock()
	cv := cl.voice
	cl.vmu.Unlock()
	if cv == nil {
		return
	}
	if cv.mic != nil {
		cv.mic.end()
	}
	_ = cv.session.Close(context.Background(), "abort")
}

func (s *Server) client(conn *websocket.Conn) *wsClient {
	s.wsMu.Lock()
	defer s.wsMu.Unlock()
	return s.wsConns[conn]
}

func (s *Server) sendVoiceState(ctx context.Context, conn *websocket.Conn, st VoiceSessionState) {
	s.sendMsg(ctx, conn, ServerMessage{Type: "voice_session", VoiceSession: &st})
}

// .
// .
func (s *Server) sendVoiceEvent(ctx context.Context, conn *websocket.Conn, ev VoiceEvent) {
	s.sendMsg(ctx, conn, ServerMessage{Type: "voice_event", VoiceEvent: &ev})
}

// .
// .
// .
// .
func (s *Server) BroadcastVoiceEvent(ev VoiceEvent) {
	s.broadcast(ServerMessage{Type: "voice_event", VoiceEvent: &ev})
}

// .
// .
// .
// .
// .
// .
// .
type VoiceReplyRef struct {
	SessionID   string `json:"session_id"`
	SynthesisID string `json:"synthesis_id,omitempty"`
	Route       string `json:"route"`
	TextOnly    bool   `json:"text_only,omitempty"`
	// .
	// .
	// .
	Fallback bool `json:"fallback,omitempty"`
}

// .
// .
// .
// .
// .
type VoiceHush struct {
	SessionID   string `json:"session_id"`
	SynthesisID string `json:"synthesis_id,omitempty"`
	Route       string `json:"route"`
	Reason      string `json:"reason,omitempty"`
}

// .
func (s *Server) BroadcastVoiceHush(h VoiceHush) {
	s.broadcast(ServerMessage{Type: "voice_hush", VoiceHush: &h})
}

// .
// .
// .
func (s *Server) BroadcastVoiceReply(ref VoiceReplyRef, text string) {
	s.broadcast(ServerMessage{Type: "response", Message: text, Role: "identity", Done: true, VoiceReply: &ref})
}

// .
// .
func (s *Server) sendBinary(ctx context.Context, conn *websocket.Conn, data []byte) error {
	cl := s.client(conn)
	if cl == nil {
		return fmt.Errorf("connection already dropped")
	}
	wctx, cancel := context.WithTimeout(ctx, writeWait)
	defer cancel()
	cl.mu.Lock()
	err := conn.Write(wctx, websocket.MessageBinary, data)
	cl.mu.Unlock()
	if err != nil {
		s.dropConn(conn)
	}
	return err
}

// .
func clip(v string, n int) string {
	if len(v) <= n {
		return v
	}
	return v[:n] + "…"
}
