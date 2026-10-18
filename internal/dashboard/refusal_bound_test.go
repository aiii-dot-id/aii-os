package dashboard

import (
	"bufio"
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

func TestAtMostSoManyRefusalsAreSlowedAtOnce(t *testing.T) {
	for _, c := range []struct {
		name string
		set  func() int
		want int
	}{
		{"unset", nil, DefaultRefusedPauses},
		{"set to two", func() int { return 2 }, 2},
		{"set under one", func() int { return -5 }, DefaultRefusedPauses},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := New("127.0.0.1", 0, &WSHandler{GetConfig: func() *ConfigState { return &ConfigState{} }, RefusedPauses: c.set})
			s.SetAccessToken(true, "the-right-token")
			addr, err := s.Start(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { s.Shutdown(context.Background()) })
			_, port, _ := net.SplitHostPort(addr)
			var asleep, answered atomic.Int64
			release := make(chan struct{})
			s.refusedPause = func(time.Duration) { asleep.Add(1); <-release }
			const over = 8
			var all sync.WaitGroup
			statuses := make(chan int, c.want+over)
			for range c.want + over {
				all.Go(func() {
					req := httptest.NewRequest(http.MethodGet, "https://"+addr+"/auth/token", nil)
					req.RemoteAddr = "203.0.113.7:4711"
					req.Header.Set("Cookie", "aii_token_"+port+"=a-wrong-guess")
					w := httptest.NewRecorder()
					s.server.Handler.ServeHTTP(w, req)
					statuses <- w.Code
					answered.Add(1)
				})
			}
			for deadline := time.Now().Add(20 * time.Second); asleep.Load() < int64(c.want) || answered.Load() < over; time.Sleep(5 * time.Millisecond) {
				if time.Now().After(deadline) {
					t.Fatalf("%d guesses in flight: %d are in their pause and %d were answered at once, want %d and %d", c.want+over, asleep.Load(), answered.Load(), c.want, over)
				}
			}
			if got := asleep.Load(); got != int64(c.want) {
				t.Fatalf("%d refusals are being slowed at one moment, want at most %d", got, c.want)
			}
			close(release)
			all.Wait()
			close(statuses)
			for code := range statuses {
				if code != http.StatusUnauthorized {
					t.Fatalf("a guess was answered %d, want refused whether it was slowed or not", code)
				}
			}
			// The places come back: the next guess is slowed again.
			before := asleep.Load()
			req := httptest.NewRequest(http.MethodGet, "https://"+addr+"/auth/token", nil)
			req.Header.Set("Cookie", "aii_token_"+port+"=a-wrong-guess")
			s.server.Handler.ServeHTTP(httptest.NewRecorder(), req)
			if asleep.Load() != before+1 {
				t.Fatal("after the slowed refusals ended, the next refusal was not slowed: a place was not given back")
			}
		})
	}
}

func TestAFloodOfRefusalsDoesNotFillTheLog(t *testing.T) {
	s := New("127.0.0.1", 0, &WSHandler{})
	s.refusedPause = func(time.Duration) {}
	said := logsink.CaptureForTest(t)
	req := httptest.NewRequest(http.MethodGet, "https://dashboard.example.test/auth/token", nil)
	req.RemoteAddr = "203.0.113.7:4711"
	const flood = 500
	synctest.Test(t, func(t *testing.T) {
		for range flood {
			s.credentialRefused(req)
		}
		if n := strings.Count(said.String(), "access token refused from 203.0.113.7"); n != refusalLinesAMinute {
			t.Fatalf("%d refusals in one minute wrote %d lines of their own, want %d", flood, n, refusalLinesAMinute)
		}
		if said.Contains("more access token(s) refused") {
			t.Fatal("the minute's count was said before the minute ended")
		}
		time.Sleep(refusalMinute)
		synctest.Wait()
		if n := strings.Count(said.String(), "380 more access token(s) refused in the last minute"); n != 1 {
			t.Fatalf("the refusals that got no line of their own were not counted in one line when the minute ended:\n%s", lastLines(said.String(), 3))
		}
		said.Reset()
		s.credentialRefused(req)
		if n := strings.Count(said.String(), "access token refused from 203.0.113.7"); n != 1 {
			t.Fatalf("in the next minute a refusal wrote %d lines of its own, want one", n)
		}
		time.Sleep(refusalMinute)
		synctest.Wait()
		if said.Contains("more access token(s) refused") {
			t.Fatal("a minute with nothing left uncounted wrote a count")
		}
	})
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

func TestAStrangersBodyHasItsTime(t *testing.T) {
	s := New("127.0.0.1", 0, &WSHandler{GetConfig: func() *ConfigState { return &ConfigState{} }})
	s.SetAccessToken(true, "the-right-token")
	s.refusedPause = func(time.Duration) {}
	s.strangerBodyWait = 200 * time.Millisecond
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Shutdown(context.Background()) })
	// The bind is TLS; the certificate is the test's own.
	conn, err := tls.Dial("tcp", addr, &tls.Config{InsecureSkipVerify: true})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	// Fifteen bytes promised, two sent, and then nothing.
	if _, err := conn.Write([]byte("POST /auth/token HTTP/1.1\r\nHost: " + addr + "\r\nContent-Type: text/plain\r\nContent-Length: 15\r\n\r\nth")); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	status, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		t.Fatalf("a sign-in whose body stopped part way was not answered in ten seconds: %v", err)
	}
	if !strings.Contains(status, "401") {
		t.Fatalf("a sign-in whose body stopped part way was answered %q, want refused", strings.TrimSpace(status))
	}
}

func TestUnsignedRequestsThroughARelayHaveARateOfTheirOwn(t *testing.T) {
	const token = "the-right-token"
	served := func(set func() (int, int)) (ask func(carried, signed bool) int) {
		s := New("127.0.0.1", 0, &WSHandler{GetConfig: func() *ConfigState { return &ConfigState{} }, UnsignedRequests: set})
		s.SetAccessToken(true, token)
		addr, err := s.Start(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Shutdown(context.Background()) })
		_, port, _ := net.SplitHostPort(addr)
		return func(carried, signed bool) int {
			req := httptest.NewRequest(http.MethodGet, "https://"+addr+"/auth/token", nil)
			if signed {
				req.Header.Set("Cookie", "aii_token_"+port+"="+verifierOf(token))
			}
			if carried {
				req = req.WithContext(context.WithValue(req.Context(), carriedKey{}, true))
			}
			w := httptest.NewRecorder()
			s.server.Handler.ServeHTTP(w, req)
			if w.Code == http.StatusTooManyRequests && w.Header().Get("Retry-After") == "" {
				t.Error("a request refused for the rate was not told when to come back")
			}
			return w.Code
		}
	}
	const refused, unsigned, signedIn = http.StatusTooManyRequests, http.StatusUnauthorized, http.StatusNoContent
	rate, burst := 2, 3
	ask := served(func() (int, int) { return rate, burst })
	plain := served(nil)
	said := logsink.CaptureForTest(t)
	synctest.Test(t, func(t *testing.T) {
		want := func(what string, got, code int) {
			t.Helper()
			if got != code {
				t.Fatalf("%s: answered %d, want %d", what, got, code)
			}
		}
		for range burst {
			want("an unsigned request through the relay, inside the burst", ask(true, false), unsigned)
		}
		want("an unsigned request through the relay, past the burst", ask(true, false), refused)
		want("a signed-in request through the relay, with the burst spent", ask(true, true), signedIn)
		want("an unsigned request on the dashboard's own network, with the burst spent", ask(false, false), unsigned)
		if said.Contains("answered 429") {
			t.Fatal("a request refused for the rate was written down by itself")
		}
		time.Sleep(time.Second)
		want("a second on, the first of two", ask(true, false), unsigned)
		want("a second on, the second of two", ask(true, false), unsigned)
		want("a second on, a third", ask(true, false), refused)
		burst = 1
		time.Sleep(time.Hour)
		want("an hour on with the burst set to one, the first", ask(true, false), unsigned)
		want("an hour on with the burst set to one, the second", ask(true, false), refused)

		for i := range DefaultUnsignedRequestBurst {
			if got := plain(true, false); got != unsigned {
				t.Fatalf("with nothing set, unsigned request %d of the default burst was answered %d", i+1, got)
			}
		}
		want("with nothing set, one past the default burst", plain(true, false), refused)

		time.Sleep(refusalMinute)
		synctest.Wait()
	})
	// Three minutes had a refusal in them: the first dashboard's first (two
	// refused), its later one (one), and the second dashboard's (one).
	if n := strings.Count(said.String(), "unsigned request(s) through the relay were answered 429 in the last minute"); n != 3 {
		t.Fatalf("three minutes had requests refused for the rate, and a count was said %d times, want once for each:\n%s", n, lastLines(said.String(), 4))
	}
	if !said.Contains("2 unsigned request(s) through the relay were answered 429 in the last minute: more arrived than dashboard.unsigned_requests_per_second (2, in bursts of 3)") ||
		!said.Contains("1 unsigned request(s) through the relay were answered 429 in the last minute: more arrived than dashboard.unsigned_requests_per_second (20, in bursts of 100)") {
		t.Fatalf("the counts do not say how many, and under which setting:\n%s", lastLines(said.String(), 4))
	}
}

func TestRefusalsAStrangerCanCauseDoNotFillTheLog(t *testing.T) {
	s := New("127.0.0.1", 0, &WSHandler{GetConfig: func() *ConfigState { return &ConfigState{} }})
	s.SetAccessToken(true, "the-right-token")
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Shutdown(context.Background()) })
	said := logsink.CaptureForTest(t)
	const flood = refusalLinesAMinute + 80
	synctest.Test(t, func(t *testing.T) {
		for range flood {
			req := httptest.NewRequest(http.MethodGet, "https://"+addr+"/", nil)
			req.Host = "elsewhere.example"
			w := httptest.NewRecorder()
			s.server.Handler.ServeHTTP(w, req)
			if w.Code != http.StatusForbidden {
				t.Fatalf("a request with a foreign Host was answered %d", w.Code)
			}
			req = httptest.NewRequest(http.MethodGet, "https://"+addr+"/ws", nil)
			req.Header.Set("Origin", "https://elsewhere.example")
			w = httptest.NewRecorder()
			s.server.Handler.ServeHTTP(w, req)
			if w.Code != http.StatusForbidden {
				t.Fatalf("a session from a foreign origin was answered %d", w.Code)
			}
		}
		for line, what := range map[string]string{`refused request with foreign Host "elsewhere.example"`: "requests with a foreign Host", `refused: origin "https://elsewhere.example"`: "sessions from a foreign origin"} {
			if n := strings.Count(said.String(), line); n != refusalLinesAMinute {
				t.Fatalf("%d %s in one minute wrote %d lines of their own, want %d", flood, what, n, refusalLinesAMinute)
			}
		}
		time.Sleep(refusalMinute)
		synctest.Wait()
		for _, count := range []string{"80 more request(s) with a foreign Host refused in the last minute", "80 more session(s) refused for their origin or sign-in in the last minute"} {
			if n := strings.Count(said.String(), count); n != 1 {
				t.Fatalf("the minute's count %q was said %d times, want once:\n%s", count, n, lastLines(said.String(), 4))
			}
		}
	})
}
