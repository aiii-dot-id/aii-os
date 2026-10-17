package dashboard

import (
	"context"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/audio"
	"github.com/coder/websocket"
)

// heldSession is an engine that never ends and never gives its endpoints
// back: Done and Released never fire, and Close is accepted and changes
// nothing — a wedged-but-alive engine as the dashboard sees it.
type heldSession struct {
	done, released, inputClosed chan struct{}
	mu                          sync.Mutex
	closed                      string
}

func (s *heldSession) closedWith() string { s.mu.Lock(); defer s.mu.Unlock(); return s.closed }

func (s *heldSession) ID() string                          { return "vs-held" }
func (s *heldSession) Finish(context.Context, int64) error { return nil }
func (s *heldSession) Close(_ context.Context, mode string) error {
	s.mu.Lock()
	s.closed = mode
	s.mu.Unlock()
	return nil
}
func (s *heldSession) Interrupt(context.Context) error                      { return nil }
func (s *heldSession) PlaybackReport(context.Context, PlaybackReport) error { return nil }
func (s *heldSession) Label() string                                        { return "Listening" }
func (s *heldSession) Malformed() uint64                                    { return 0 }
func (s *heldSession) Done() <-chan struct{}                                { return s.done }
func (s *heldSession) Released() <-chan struct{}                            { return s.released }
func (s *heldSession) InputClosed() <-chan struct{}                         { return s.inputClosed }
func (s *heldSession) InputCompletionReason() string                        { return "" }

// voiceStreamGoroutines is every goroutine standing in this package's
// voice_stream.go: what a page's open left running.
func voiceStreamGoroutines() []string {
	buf := make([]byte, 1<<20)
	buf = buf[:runtime.Stack(buf, true)]
	var held []string
	for _, g := range strings.Split(string(buf), "\n\n") {
		if strings.Contains(g, "/internal/dashboard/voice_stream.go:") {
			held = append(held, g)
		}
	}
	return held
}

// NOTHING A PAGE'S OPEN STARTS OUTLIVES THE DASHBOARD (T08). Each open
// started two watchers that nothing stopped — one for the engine's own
// end of input, one for the session's end and its endpoints' return — so
// with an engine that never ends they were still standing after Shutdown.
// The open now starts ONE watcher, as a job of the dashboard's own runner,
// whose waits on Done and on Released each have an arm on the dashboard's
// lifetime. Shutdown joins its jobs, and the app calls it with no
// deadline, so those arms are what let it return at all: one for an
// engine that never ends, one for an engine that ends and never gives the
// page's endpoints back.
func TestDashboardShutdownReturnsWithASessionNeverReleased(t *testing.T) {
	for _, c := range []struct {
		name string
		ends bool
	}{
		{"the engine never ends", false},
		{"the engine ends and never gives the endpoints back", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			plane := audio.NewPlane()
			held := &heldSession{done: make(chan struct{}), released: make(chan struct{}), inputClosed: make(chan struct{})}
			h := &WSHandler{
				GetStats:    func() (*StatsResponse, error) { return &StatsResponse{}, nil },
				AudioPlane:  func() *audio.Plane { return plane },
				VoiceEngine: func() bool { return true },
				VoiceSessionOpen: func(ctx context.Context, in, out, mode string, processing *CaptureProcessing) (VoiceSession, error) {
					return held, nil
				},
			}
			s := New("127.0.0.1", 0, h)
			addr, err := s.Start(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			conn := dialWS(t, addr)
			drainUntil(t, conn, "status")
			// What earlier tests in this process left standing is theirs.
			before := len(voiceStreamGoroutines())
			sendMsg(t, conn, ClientMessage{Type: "voice_session", Voice: &VoiceRequest{Action: "open", Mode: "meeting", Rate: 16000, Channels: 1}})
			if st := drainUntil(t, conn, "voice_session"); st.VoiceSession == nil || st.VoiceSession.State != "open" {
				t.Fatalf("open: %+v", st.VoiceSession)
			}
			if c.ends {
				close(held.done)
				if st := drainUntil(t, conn, "voice_session"); st.VoiceSession == nil || st.VoiceSession.State != "closed" {
					t.Fatalf("the page is told the session ended: %+v", st.VoiceSession)
				}
			}
			// The control half: the open is being watched while the dashboard runs.
			if len(voiceStreamGoroutines()) <= before {
				t.Fatal("the open started no watcher, so its absence after Shutdown would prove nothing")
			}

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			began := time.Now()
			if err := s.Shutdown(ctx); err != nil {
				t.Fatalf("Shutdown with a session whose endpoints never come back: %v after %v", err, time.Since(began))
			}
			if took := time.Since(began); took > 5*time.Second {
				t.Fatalf("Shutdown took %v", took)
			}
			deadline := time.Now().Add(2 * time.Second)
			for len(voiceStreamGoroutines()) > before && time.Now().Before(deadline) {
				time.Sleep(10 * time.Millisecond)
			}
			if left := voiceStreamGoroutines(); len(left) > before {
				t.Fatalf("%d of the open's goroutines outlived Shutdown:\n%s", len(left)-before, strings.Join(left, "\n\n"))
			}
		})
	}
}

// AN OPEN THE DASHBOARD CANNOT WATCH IS NOT LEFT OPEN. Once Shutdown has
// closed the job runner, a page's open cannot start its watcher: the
// session it just opened is aborted, its endpoints leave the plane, and
// the page is told it was refused — never "open" with nothing following it.
func TestAnOpenWhileTheDashboardStopsIsRefusedAndClosed(t *testing.T) {
	plane := audio.NewPlane()
	held := &heldSession{done: make(chan struct{}), released: make(chan struct{}), inputClosed: make(chan struct{})}
	h := &WSHandler{
		GetStats:    func() (*StatsResponse, error) { return &StatsResponse{}, nil },
		AudioPlane:  func() *audio.Plane { return plane },
		VoiceEngine: func() bool { return true },
		VoiceSessionOpen: func(ctx context.Context, in, out, mode string, processing *CaptureProcessing) (VoiceSession, error) {
			return held, nil
		},
	}
	s := New("127.0.0.1", 0, h)
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	conn := dialWS(t, addr)
	drainUntil(t, conn, "status")
	s.jobsMu.Lock()
	s.jobsStopping = true // what Shutdown's first step does
	s.jobsMu.Unlock()
	sendMsg(t, conn, ClientMessage{Type: "voice_session", Voice: &VoiceRequest{Action: "open", Mode: "meeting", Rate: 16000, Channels: 1}})
	if st := drainUntil(t, conn, "voice_session"); st.VoiceSession == nil || st.VoiceSession.State != "refused" || st.VoiceSession.Reason != "identity is stopping" {
		t.Fatalf("an open the dashboard cannot watch is refused: %+v", st.VoiceSession)
	}
	if got := held.closedWith(); got != "abort" {
		t.Fatalf("the session it opened is aborted, not left open: %q", got)
	}
	if eps := plane.Endpoints(); len(eps) != 0 {
		t.Fatalf("its endpoints leave the plane: %+v", eps)
	}
}

func TestAWatcherWaitingForItsOpenDoesNotHoldShutdown(t *testing.T) {
	s := New("127.0.0.1", 0, &WSHandler{GetStats: func() (*StatsResponse, error) { return &StatsResponse{}, nil }})
	if _, err := s.Start(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	never := make(chan struct{}) // the open's "open" never goes out
	watching := make(chan struct{})
	if !s.runJob(func() {
		close(watching)
		s.watchVoiceSession(context.Background(), nil, nil, nil, never, func() {})
	}) {
		t.Fatal("the job runner refused the watcher")
	}
	<-watching
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := s.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown with a watcher still waiting for its open's \"open\": %v", err)
	}
}

func openOnHeldSessions(t *testing.T) (*websocket.Conn, *heldSession, *heldSession) {
	t.Helper()
	plane := audio.NewPlane()
	first := &heldSession{done: make(chan struct{}), released: make(chan struct{}), inputClosed: make(chan struct{})}
	second := &heldSession{done: make(chan struct{}), released: make(chan struct{}), inputClosed: make(chan struct{})}
	var opens atomic.Int32
	h := &WSHandler{
		GetStats:    func() (*StatsResponse, error) { return &StatsResponse{}, nil },
		AudioPlane:  func() *audio.Plane { return plane },
		VoiceEngine: func() bool { return true },
		VoiceSessionOpen: func(ctx context.Context, in, out, mode string, processing *CaptureProcessing) (VoiceSession, error) {
			if opens.Add(1) == 1 {
				return first, nil
			}
			return second, nil
		},
	}
	s := New("127.0.0.1", 0, h)
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, held := range []*heldSession{first, second} {
			for _, c := range []chan struct{}{held.done, held.released} {
				select {
				case <-c:
				default:
					close(c)
				}
			}
		}
		_ = s.Shutdown(context.Background())
	})
	conn := dialWS(t, addr)
	drainUntil(t, conn, "status")
	sendMsg(t, conn, ClientMessage{Type: "voice_session", Voice: &VoiceRequest{Action: "open", Mode: "meeting", Rate: 16000, Channels: 1}})
	if st := drainUntil(t, conn, "voice_session"); st.VoiceSession == nil || st.VoiceSession.State != "open" {
		t.Fatalf("rig: open: %+v", st.VoiceSession)
	}
	return conn, first, second
}

// replacementAfterClosed waits for "closed", opens the queued lane as the
// page does at once, and returns what the page is told of it.
func replacementAfterClosed(t *testing.T, conn *websocket.Conn) *VoiceSessionState {
	t.Helper()
	if st := drainUntil(t, conn, "voice_session"); st.VoiceSession == nil || st.VoiceSession.State != "closed" {
		t.Fatalf("rig: closed: %+v", st.VoiceSession)
	}
	sendMsg(t, conn, ClientMessage{Type: "voice_session", Voice: &VoiceRequest{Action: "open", Mode: "conversation", Rate: 16000, Channels: 1}})
	return drainUntil(t, conn, "voice_session").VoiceSession
}

func TestAReplacementOpenedOnClosedIsAdmittedBeforeTheEndpointsAreBack(t *testing.T) {
	conn, first, _ := openOnHeldSessions(t)
	close(first.done) // the session is over; its endpoints are not yet back (a fault, a transport end)
	if st := replacementAfterClosed(t, conn); st == nil || st.State != "open" {
		t.Fatalf("the page was told closed and its replacement was then refused: %+v", st)
	}
}

func TestAReplacementOpenedOnClosedIsAdmittedAfterACleanEnd(t *testing.T) {
	conn, first, _ := openOnHeldSessions(t)
	close(first.released) // a clean drain or abort: the endpoints come back first
	close(first.done)
	if st := replacementAfterClosed(t, conn); st == nil || st.State != "open" {
		t.Fatalf("the page was told closed and its replacement was then refused: %+v", st)
	}
}

func TestASecondOpenWhileTheSessionIsLiveIsStillRefused(t *testing.T) {
	conn, _, _ := openOnHeldSessions(t)
	sendMsg(t, conn, ClientMessage{Type: "voice_session", Voice: &VoiceRequest{Action: "open", Mode: "conversation", Rate: 16000, Channels: 1}})
	st := drainUntil(t, conn, "voice_session").VoiceSession
	if st == nil || st.State != "refused" || !strings.Contains(st.Reason, "already holds a session") {
		t.Fatalf("a page holding a live session opened another: %+v", st)
	}
}
