package dashboard

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// endless is a database that never ends: a download a peer can stop
// reading in the middle of.
type endless struct{}

func (endless) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

// rawRequest opens its own connection to the dashboard, writes the head of
// one request and whatever of its body is given, and leaves the rest
// unsent and the answer unread: a peer that holds a connection.
func rawRequest(t *testing.T, addr, line string, declared int, sent string) net.Conn {
	t.Helper()
	c, err := tls.Dial("tcp", addr, &tls.Config{InsecureSkipVerify: true})
	if err != nil {
		t.Fatal(err)
	}
	return sendHead(t, c, "https", addr, line, declared, sent)
}

func sendHead(t *testing.T, c net.Conn, scheme, addr, line string, declared int, sent string) net.Conn {
	t.Helper()
	t.Cleanup(func() { c.Close() })
	head := fmt.Sprintf("%s HTTP/1.1\r\nHost: %s\r\nOrigin: %s://%s\r\nSec-Fetch-Site: same-origin\r\nContent-Length: %d\r\n\r\n%s", line, addr, scheme, addr, declared, sent)
	if _, err := io.WriteString(c, head); err != nil {
		t.Fatal(err)
	}
	return c
}

// A STOP ENDS AT ITS BOUND, WHATEVER A PEER OR A JOB IS DOING. Three things
// each held the dashboard's stop for as long as they liked: a download the
// peer stopped reading, a body the peer sends a byte of and no more, and a
// job waiting for something only the rest of the teardown ends. With an
// idle page's socket beside them, the stop here is given three hundred
// milliseconds. It returns then and says it was cut. The two handlers that
// were blocked on their connections have returned by the time Settle is
// asked, because the connections are closed; the job has not, and Settle
// says exactly that. When the job's wait ends, Settle has nothing left.
func TestAStopEndsAtItsBoundWhateverAPeerOrAJobIsDoing(t *testing.T) {
	uploading := make(chan struct{})
	uploaded := make(chan error, 1)
	s := New("127.0.0.1", 0, &WSHandler{
		GetStats: func() (*StatsResponse, error) { return &StatsResponse{}, nil },
		DatabaseExport: func(context.Context) (io.ReadCloser, int64, error) {
			return io.NopCloser(endless{}), 1 << 40, nil
		},
		RestoreNew: &RestoreNewHooks{Upload: func(_, _ string, body io.Reader) error {
			close(uploading)
			_, err := io.Copy(io.Discard, body)
			uploaded <- err
			return err
		}},
	})
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	download := rawRequest(t, addr, "POST /database/export", 0, "")
	status, err := bufio.NewReader(download).ReadString('\n')
	if err != nil || !strings.Contains(status, " 200 ") {
		t.Fatalf("the download did not begin, so nothing is held: %q %v", status, err)
	}
	rawRequest(t, addr, "POST /restore/upload?snapshot=s&member=m", 1<<20, "x")
	select {
	case <-uploading:
	case <-time.After(5 * time.Second):
		t.Fatal("the upload did not begin, so nothing is held")
	}
	dialWS(t, addr)
	jobMayEnd := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-jobMayEnd:
		default:
			close(jobMayEnd)
		}
	})
	if !s.runJob(func() { <-jobMayEnd }) {
		t.Fatal("the job runner refused the job")
	}
	// The download's handler is blocked once the connection's buffers are
	// full; give it the moment that takes.
	time.Sleep(200 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	began := time.Now()
	err = s.Shutdown(ctx)
	var cut *DrainCut
	if !errors.As(err, &cut) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("a stop that reached its bound must say it was cut: %v", err)
	}
	if took := time.Since(began); took > 3*time.Second {
		t.Fatalf("a stop bounded at 300ms took %s", took)
	}
	select {
	case err := <-uploaded:
		if err == nil {
			t.Fatal("the upload ended without an error: its connection was not what ended it")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the upload's handler is still reading a connection the stop should have closed")
	}

	settle, done := context.WithTimeout(context.Background(), 2*time.Second)
	err = s.Settle(settle)
	done()
	if err == nil || !strings.Contains(err.Error(), "jobs had not all ended, and 0 of its requests") {
		t.Fatalf("with the connections closed only the job is left, and Settle must say so: %v", err)
	}
	close(jobMayEnd)
	settle, done = context.WithTimeout(context.Background(), 5*time.Second)
	defer done()
	if err := s.Settle(settle); err != nil {
		t.Fatalf("with the job's wait ended nothing is left: %v", err)
	}
}

// A STOP WITH NOTHING HELD IS NOT A CUT. A page's idle socket and a
// request that has been answered are the ordinary case, and the stop ends
// well inside its bound and says nothing.
func TestAStopWithNothingHeldIsNotCut(t *testing.T) {
	s := New("127.0.0.1", 0, &WSHandler{GetStats: func() (*StatsResponse, error) { return &StatsResponse{}, nil }})
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	conn := dialWS(t, addr)
	drainUntil(t, conn, "status")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	began := time.Now()
	if err := s.Shutdown(ctx); err != nil {
		t.Fatalf("a stop with nothing held: %v", err)
	}
	if took := time.Since(began); took > 5*time.Second {
		t.Fatalf("a stop with nothing held took %s", took)
	}
	if err := s.Settle(ctx); err != nil {
		t.Fatalf("and nothing is left running: %v", err)
	}
}

// A REQUEST THAT DOES NOT WATCH ITS CONNECTION IS COUNTED. Closing a
// connection ends a handler that reads or writes it. One that waits on
// something else is still running, and Settle counts it by what it is.
func TestSettleCountsARequestThatHasNotReturned(t *testing.T) {
	entered, mayReturn := make(chan struct{}), make(chan struct{})
	s := New("127.0.0.1", 0, &WSHandler{
		DatabaseExport: func(context.Context) (io.ReadCloser, int64, error) {
			close(entered)
			<-mayReturn
			return nil, 0, errors.New("the export was never made")
		},
	})
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		select {
		case <-mayReturn:
		default:
			close(mayReturn)
		}
	})
	rawRequest(t, addr, "POST /database/export", 0, "")
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the request did not reach its handler")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	err = s.Shutdown(ctx)
	cancel()
	var cut *DrainCut
	if !errors.As(err, &cut) {
		t.Fatalf("a stop behind a request that does not return must be cut: %v", err)
	}
	settle, done := context.WithTimeout(context.Background(), 300*time.Millisecond)
	err = s.Settle(settle)
	done()
	if err == nil || !strings.Contains(err.Error(), "1 of the dashboard's requests had not returned") {
		t.Fatalf("Settle must count the request still running: %v", err)
	}
	close(mayReturn)
	settle, done = context.WithTimeout(context.Background(), 5*time.Second)
	defer done()
	if err := s.Settle(settle); err != nil {
		t.Fatalf("once it returns nothing is left: %v", err)
	}
}

// THE SERVER BESIDE THE BIND STOPS AT THE SAME BOUND. A dashboard bound to
// an address on the network serves this machine's own loopback from a
// second server. That one was drained first and alone, could spend the
// whole bound, and its connections were never closed when it had: a
// download held through it was still being written after the stop had
// returned. Both drain together now, and at the bound both are closed.
func TestTheServerBesideTheBindIsClosedAtTheBoundToo(t *testing.T) {
	var lan string
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil && !n.IP.IsLoopback() && !n.IP.IsLinkLocalUnicast() {
			lan = n.IP.String()
			break
		}
	}
	if lan == "" {
		t.Skip("this host has no address but loopback, so a dashboard has no second server here")
	}
	s := New(lan, 0, &WSHandler{DatabaseExport: func(context.Context) (io.ReadCloser, int64, error) {
		return io.NopCloser(endless{}), 1 << 40, nil
	}})
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if s.loopServer == nil {
		t.Fatalf("a dashboard bound to %s has no server beside it, so this proves nothing", addr)
	}
	_, port, _ := net.SplitHostPort(addr)
	loop := net.JoinHostPort("127.0.0.1", port)
	c, err := net.Dial("tcp", loop)
	if err != nil {
		t.Fatal(err)
	}
	download := sendHead(t, c, "http", loop, "POST /database/export", 0, "")
	status, err := bufio.NewReader(download).ReadString('\n')
	if err != nil || !strings.Contains(status, " 200 ") {
		t.Fatalf("the download through the loopback did not begin, so nothing is held: %q %v", status, err)
	}
	time.Sleep(200 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	err = s.Shutdown(ctx)
	cancel()
	var cut *DrainCut
	if !errors.As(err, &cut) {
		t.Fatalf("a stop behind a held download must be cut: %v", err)
	}
	settle, done := context.WithTimeout(context.Background(), 3*time.Second)
	defer done()
	if err := s.Settle(settle); err != nil {
		t.Fatalf("the download through the loopback is still being written after the stop closed its connections: %v", err)
	}
}

// A STOP JOINS ITS JOBS WITHIN THE BOUND. With no connection held, a job
// is all there is to wait for. One that ends inside the bound is waited
// for: the stop returns after it and says nothing. One that outlasts the
// bound cuts the stop, as a held connection does.
func TestAStopJoinsItsJobsWithinTheBoundAndIsCutBehindOneThatOutlastsIt(t *testing.T) {
	quick := New("127.0.0.1", 0, &WSHandler{})
	if _, err := quick.Start(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	var ended atomic.Bool
	if !quick.runJob(func() {
		time.Sleep(300 * time.Millisecond)
		ended.Store(true)
	}) {
		t.Fatal("the job runner refused the job")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := quick.Shutdown(ctx); err != nil || !ended.Load() {
		t.Fatalf("a stop must wait for a job that ends inside its bound: err %v, the job ended %v", err, ended.Load())
	}

	slow := New("127.0.0.1", 0, &WSHandler{})
	if _, err := slow.Start(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	mayEnd := make(chan struct{})
	defer close(mayEnd)
	if !slow.runJob(func() { <-mayEnd }) {
		t.Fatal("the job runner refused the job")
	}
	short, done := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer done()
	var cut *DrainCut
	if err := slow.Shutdown(short); !errors.As(err, &cut) {
		t.Fatalf("a stop behind a job that outlasts its bound must say it was cut: %v", err)
	}
}
