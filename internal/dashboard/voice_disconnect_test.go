package dashboard

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/audio"
)

// Hold the actual read owner inside an existing handler callback until a
// failed writer has removed its client. Socket scheduling cannot choose which
// cleanup wins. Cover both an established session and an open returning after
// the map entry disappeared, including the speaker-only lane.
func TestFailedPageWriteStillAbortsItsVoiceSession(t *testing.T) {
	for _, mode := range []string{"meeting", voiceOutputMode} {
		for _, phase := range []string{"opening", "open"} {
			for _, failure := range []string{"text", "audio", "peer-close"} {
				t.Run(mode+"/"+phase+"/"+failure, func(t *testing.T) {
					testVoiceDisconnect(t, mode, phase, failure)
				})
			}
		}
	}
}

func testVoiceDisconnect(t *testing.T, mode, phase, failure string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	plane := audio.NewPlane()
	opened := make(chan *outputStub, 1)
	readerHeld, resumeReader := make(chan struct{}), make(chan struct{})
	var holdStatus atomic.Bool
	var resumeOnce sync.Once
	resume := func() { resumeOnce.Do(func() { close(resumeReader) }) }
	hold := func() {
		close(readerHeld)
		select {
		case <-resumeReader:
		case <-ctx.Done():
		}
	}
	h := &WSHandler{
		GetStats: func() (*StatsResponse, error) {
			if holdStatus.Swap(false) {
				hold()
			}
			return &StatsResponse{}, nil
		},
		AudioPlane:  func() *audio.Plane { return plane },
		VoiceEngine: func() bool { return true },
		VoiceSessionOpen: func(_ context.Context, in, out, _ string, _ *CaptureProcessing) (VoiceSession, error) {
			var b *audio.Binding
			var err error
			if in == "" {
				b, err = plane.BindOutput("disconnect-session", out, true)
			} else {
				b, err = plane.Bind("disconnect-session", in, out, true)
			}
			if err != nil {
				return nil, err
			}
			ss := &outputStub{stubSession: stubSession{id: "disconnect-session", b: b, done: make(chan struct{})}, closes: make(chan string, 4)}
			opened <- ss
			if phase == "opening" {
				hold()
			}
			return ss, nil
		},
	}
	s := New("127.0.0.1", 0, h)
	handlerDone := make(chan struct{})
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(handlerDone)
		s.handleWS(w, r)
	}))
	defer server.Close()
	s.AllowHost(server.Listener.Addr().String())
	conn := dialWS(t, strings.TrimPrefix(server.URL, "https://"))
	var ss *outputStub
	var endOnce sync.Once
	end := func() {
		if ss != nil {
			endOnce.Do(func() {
				close(ss.done)
				ss.b.Release()
			})
		}
	}
	defer func() {
		resume()
		conn.CloseNow()
		select {
		case <-handlerDone:
		case <-ctx.Done():
			t.Error("WebSocket read owner did not exit")
		}
		end() // even a rejected mutation must not leave fixture waiters behind
	}()
	drainUntil(t, conn, "status")
	sendMsg(t, conn, ClientMessage{Type: "voice_session", Voice: &VoiceRequest{Action: "open", Mode: mode, Rate: 16000, Channels: 1}})
	select {
	case ss = <-opened:
	case <-ctx.Done():
		t.Fatal("voice open was not reached")
	}
	if phase == "open" {
		st := drainUntil(t, conn, "voice_session")
		if st.VoiceSession == nil || st.VoiceSession.State != "open" {
			t.Fatalf("voice open: %+v", st)
		}
		holdStatus.Store(true)
		sendMsg(t, conn, ClientMessage{Type: "query", Query: "status"})
	}
	select {
	case <-readerHeld:
	case <-ctx.Done():
		t.Fatal("read owner never reached the controlled step")
	}
	s.wsMu.Lock()
	var serverConn carrier
	var cl *wsClient
	for c, client := range s.wsConns {
		serverConn, cl = c, client
	}
	s.wsMu.Unlock()
	if serverConn == nil {
		t.Fatal("fixture has no registered connection")
	}
	if failure == "peer-close" {
		conn.CloseNow() // control: no early server-map deletion
	} else {
		// The dead transport deterministically exercises the same production
		// failure branch as the bounded write deadline, without filling a
		// machine-dependent TCP buffer or sleeping for writeWait. The read
		// owner is held, so it cannot clean up before this write removes cl.
		serverConn.CloseNow()
		var err error
		if failure == "text" {
			err = s.sendMsg(ctx, serverConn, ServerMessage{Type: "status"})
		} else {
			err = ss.b.Sink.Write(ctx, audio.Frame{Kind: audio.KindPCM, PCM: make([]byte, 16)})
		}
		if err == nil {
			t.Fatal("injected write failure was accepted")
		}
		if s.client(serverConn) != nil {
			t.Fatal("failed writer left the page in the broadcast map")
		}
	}
	select {
	case mode := <-ss.closes:
		t.Fatalf("writer took cleanup from the still-running read owner: %s", mode)
	default:
	}
	resume()
	select {
	case <-handlerDone:
	case <-ctx.Done():
		t.Fatal("read owner did not settle after disconnection")
	}
	// This join, not elapsed time, proves the cleanup attempt has finished.
	select {
	case got := <-ss.closes:
		if got != "abort" {
			t.Fatalf("disconnected page requested %q, want abort", got)
		}
	default:
		t.Fatal("read owner lost its voice session after the connection was dropped")
	}
	select {
	case got := <-ss.closes:
		t.Fatalf("duplicate disconnect cleanup: %s", got)
	default:
	}
	if ss.b.Source != nil {
		if _, err := ss.b.Source.Read(ctx); !errors.Is(err, io.EOF) {
			t.Fatalf("disconnected microphone did not end: %v", err)
		}
	}
	expectedEndpoints := 2
	if mode == voiceOutputMode {
		expectedEndpoints = 1
	}
	held := plane.Endpoints()
	if len(held) != expectedEndpoints {
		t.Fatalf("abort admission removed endpoints before engine release: %+v", held)
	}
	for _, ep := range held {
		if ep.BoundTo != ss.ID() {
			t.Fatalf("abort admission released an endpoint before the engine: %+v", ep)
		}
	}
	end()
	// Wait only for the existing endpoint-reaper's observable completion.
	// The failure ordering above is already established by handlerDone.
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		cl.vmu.Lock()
		settled := cl.voice == nil
		cl.vmu.Unlock()
		if settled {
			break
		}
		select {
		case <-tick.C:
		case <-ctx.Done():
			t.Fatal("voice endpoint cleanup did not finish after engine release")
		}
	}
	if eps := plane.Endpoints(); len(eps) != 0 {
		t.Fatalf("disconnected page retained endpoints after engine release: %+v", eps)
	}
}
