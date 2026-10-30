package dashboard

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

// refusalLines is every line of the captured log that holds marker.
func refusalLines(log, marker string) []string {
	var out []string
	for _, line := range strings.Split(log, "\n") {
		if strings.Contains(line, marker) {
			out = append(out, line)
		}
	}
	return out
}

// A REFUSAL LINE DOES NOT CARRY A PEER'S WHOLE HEADER. A refused Host or
// Origin is logged before any credential is asked, at the rate a peer
// sends, and a header may be 64 KiB. With the live log unbounded, a peer
// that could reach the dashboard could grow the log beside the record by
// that much a request. Both lines that carry a peer's text are held here,
// through the server's whole handler chain: a request with a foreign Host,
// and a session refused for its Origin. Each keeps enough to see what was
// sent and says how much it left out; a short value is logged whole.
func TestARefusalLineDoesNotCarryAPeersWholeHeader(t *testing.T) {
	s := New("127.0.0.1", 0, &WSHandler{GetConfig: func() *ConfigState { return &ConfigState{} }})
	s.SetAccessToken(true, "the-right-token")
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Shutdown(context.Background()) })
	said := logsink.CaptureForTest(t)
	long := "evil-" + strings.Repeat("a", 64<<10) + ".example:1"

	synctest.Test(t, func(t *testing.T) {
		send := func(path, host, origin string) {
			t.Helper()
			req := httptest.NewRequest(http.MethodGet, "https://"+addr+path, nil)
			if host != "" {
				req.Host = host
			}
			if origin != "" {
				req.Header.Set("Origin", origin)
			}
			w := httptest.NewRecorder()
			s.server.Handler.ServeHTTP(w, req)
			if w.Code != http.StatusForbidden {
				t.Fatalf("%s (Host %.40q, Origin %.40q) was answered %d", path, host, origin, w.Code)
			}
		}
		send("/", long, "")
		send("/ws", "", "https://"+long)
		for marker, what := range map[string]string{
			"refused request with foreign Host": "a foreign Host",
			"refused: origin":                   "a session's Origin",
		} {
			lines := refusalLines(said.String(), marker)
			if len(lines) != 1 {
				t.Fatalf("the refusal of %s must still be logged, once: %d lines", what, len(lines))
			}
			if !strings.Contains(lines[0], "bytes more)") {
				t.Errorf("the line for %s does not say it left text out:\n%.400s", what, lines[0])
			}
			if len(lines[0]) > 1000 {
				t.Errorf("a refusal line carried %d bytes of %s", len(lines[0]), what)
			}
		}

		// A short one is logged whole.
		send("/", "elsewhere.example:9", "")
		send("/ws", "", "https://elsewhere.example")
		for _, want := range []string{`refused request with foreign Host "elsewhere.example:9"`, `refused: origin "https://elsewhere.example"`} {
			lines := refusalLines(said.String(), want)
			if len(lines) != 1 || strings.Contains(lines[0], "bytes more") {
				t.Errorf("a short value must be logged as it was sent: %q in %q", want, lines)
			}
		}
		time.Sleep(refusalMinute)
		synctest.Wait()
	})

	if got := peerSaid(strings.Repeat("é", 400), refusalPeerTextBytes); len(got) > refusalPeerTextBytes+40 || !strings.Contains(got, "bytes more)") || strings.ContainsRune(got, '�') || !strings.HasPrefix(got, strings.Repeat("é", refusalPeerTextBytes/2)+"…") {
		t.Fatalf("a cut must fall between characters and say what it left out: %d bytes, %.80q", len(got), got)
	}
}

func TestARefusalLineIsBoundedAsTheOperatorSetsIt(t *testing.T) {
	var lines atomic.Int64
	s := New("127.0.0.1", 0, &WSHandler{
		GetConfig:             func() *ConfigState { return &ConfigState{} },
		RefusalLinesPerMinute: func() int { return int(lines.Load()) },
	})
	s.SetAccessToken(true, "the-right-token")
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Shutdown(context.Background()) })
	said := logsink.CaptureForTest(t)
	long := strings.Repeat("b", 20000) + ".example"
	const marker = "refused request with foreign Host"
	foreign := func() {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "https://"+addr+"/", nil)
		req.Host = long
		w := httptest.NewRecorder()
		s.server.Handler.ServeHTTP(w, req)
		if w.Code != http.StatusForbidden {
			t.Fatalf("a foreign Host was answered %d", w.Code)
		}
	}
	carried := func(line string) int {
		_, quoted, _ := strings.Cut(line, `Host "`)
		text, _, _ := strings.Cut(quoted, "…")
		return len(text)
	}

	synctest.Test(t, func(t *testing.T) {
		lines.Store(3)
		for range 10 {
			foreign()
		}
		got := refusalLines(said.String(), marker)
		if len(got) != 3 {
			t.Fatalf("set to 3 a minute, 10 refusals wrote %d lines of their own", len(got))
		}
		for _, line := range got {
			if n := carried(line); n != refusalPeerTextBytes {
				t.Fatalf("a line carried %d bytes of the peer's Host, not the fixed %d", n, refusalPeerTextBytes)
			}
		}
		// Read at the next line: two more lines this minute.
		lines.Store(5)
		for range 10 {
			foreign()
		}
		got = refusalLines(said.String(), marker)
		if len(got) != 5 {
			t.Fatalf("raised to 5 a minute: %d lines", len(got))
		}
		time.Sleep(refusalMinute)
		synctest.Wait()
		if n := len(refusalLines(said.String(), "15 more request(s) with a foreign Host refused in the last minute")); n != 1 {
			t.Fatalf("the refusals with no line of their own were not counted in one line: %d", n)
		}

		// Outside the bounds: held to them. Unset: the defaults.
		lines.Store(1 << 20)
		if l := s.refusalLines(); l != MaxRefusalLinesPerMinute {
			t.Fatalf("set past the bound, the lines read as %d a minute", l)
		}
		lines.Store(0)
		if l := s.refusalLines(); l != DefaultRefusalLinesPerMinute {
			t.Fatalf("unset, the lines read as %d a minute", l)
		}
	})
}

func TestTheKeylessRefusalLineIsBoundedAsTheOperatorSetsIt(t *testing.T) {
	var lines atomic.Int64
	s := New("127.0.0.1", 0, &WSHandler{
		GetConfig:             func() *ConfigState { return &ConfigState{} },
		RefusalLinesPerMinute: func() int { return int(lines.Load()) },
	})
	said := logsink.CaptureForTest(t)
	const marker = "that carried the sign-in cookie without the page key:"
	synctest.Test(t, func(t *testing.T) {
		lines.Store(2)
		for range 10 {
			s.keyless(httptest.NewRequest(http.MethodGet, "/", nil))
		}
		if got := len(refusalLines(said.String(), marker)); got != 2 {
			t.Fatalf("set to 2 a minute, 10 keyless requests wrote %d lines of their own", got)
		}
		lines.Store(4)
		for range 10 {
			s.keyless(httptest.NewRequest(http.MethodGet, "/", nil))
		}
		if got := len(refusalLines(said.String(), marker)); got != 4 {
			t.Fatalf("raised to 4 a minute, the keyless lines did not follow at the next line: %d", got)
		}
	})
}
