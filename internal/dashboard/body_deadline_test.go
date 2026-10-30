package dashboard

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// slowPost opens a connection of its own, sends a POST's headers whole and
// only the first sent bytes of its body, and hands the connection back.
func slowPost(t *testing.T, addr, path, host, cookie, body string, sent int) net.Conn {
	t.Helper()
	c, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	head := "POST " + path + " HTTP/1.1\r\nHost: " + host + "\r\nContent-Length: " + fmt.Sprint(len(body)) + "\r\n"
	if cookie != "" {
		head += "Cookie: " + cookie + "\r\n"
	}
	if _, err := io.WriteString(c, head+"\r\n"+body[:sent]); err != nil {
		t.Fatal(err)
	}
	return c
}

// answeredAndClosed reads the one answer the dashboard gives on c, and then
// requires the dashboard to have closed the connection: the place it held
// is free. Both within within.
func answeredAndClosed(t *testing.T, c net.Conn, within time.Duration, what string) int {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(within))
	r := bufio.NewReader(c)
	res, err := http.ReadResponse(r, nil)
	if err != nil {
		t.Fatalf("%s: THE DASHBOARD WAITED FOR THE REST OF THE BODY WITHOUT A BOUND (%s and no answer): %v", what, within, err)
	}
	_, _ = io.Copy(io.Discard, res.Body)
	res.Body.Close()
	if _, err := r.ReadByte(); err != io.EOF {
		t.Fatalf("%s: answered %d and kept the connection open: %v", what, res.StatusCode, err)
	}
	return res.StatusCode
}

// A STRANGER'S BODY HAS ITS TIME ON EVERY ROUTE. The sign-in's body was
// bounded and nothing else's: a body sent with a foreign Host is answered
// without being read, and the server read what the handler left, without a
// deadline, before it answered; a webhook's body was read without one too.
// A peer that sent the headers and one byte held its connection, and
// through a relay one of the places the identity carries, for as long as
// it liked. A request that carries the token in force is not bounded, and
// a body that arrived whole leaves no deadline behind it for the handler
// that works on.
func TestAStrangersBodyThatDoesNotArriveEndsAtItsBoundOnEveryRoute(t *testing.T) {
	const token = "the-token-in-force"
	const bound = 300 * time.Millisecond
	s := New("127.0.0.1", 0, nil)
	s.SetAccessToken(true, token)
	s.strangerBodyWait = bound
	s.refusedPause = func(time.Duration) {}
	worked := make(chan error, 1)
	s.SetWebhookHandler(func(w http.ResponseWriter, r *http.Request, _, _ string) {
		if _, err := io.ReadAll(r.Body); err != nil {
			http.Error(w, "unreadable body", http.StatusBadRequest)
			return
		}
		// The operation works on, past the bound its body had.
		select {
		case <-r.Context().Done():
			worked <- fmt.Errorf("the request was cancelled under the handler: %w", context.Cause(r.Context()))
			http.Error(w, "cancelled", http.StatusServiceUnavailable)
		case <-time.After(3 * bound):
			worked <- nil
			w.WriteHeader(http.StatusNoContent)
		}
	})
	addr, err := s.Start("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Shutdown(context.Background()) })
	_, port, _ := net.SplitHostPort(addr)
	cookie := "aii_token_" + port + "=" + verifierOf(token)
	whole := strings.Repeat("x", 64)

	// The sign-in, bounded before this repair by its own handler.
	if code := answeredAndClosed(t, slowPost(t, addr, "/auth/token", addr, "", whole, 1), 5*time.Second, "a sign-in whose body stops after one byte"); code != http.StatusUnauthorized {
		t.Fatalf("a sign-in whose body never arrived was answered %d", code)
	}
	// A route that refuses without reading: the server's own read of what
	// the handler left is the one that waited.
	if code := answeredAndClosed(t, slowPost(t, addr, "/auth/token", "elsewhere.example.test:80", "", whole, 1), 5*time.Second, "a refused Host with a body that stops after one byte"); code != http.StatusForbidden {
		t.Fatalf("a foreign Host was answered %d", code)
	}
	// A webhook: its handler reads the body whole before anything else.
	if code := answeredAndClosed(t, slowPost(t, addr, "/hooks/a-plugin/in", addr, "", whole, 1), 5*time.Second, "a webhook whose body stops after one byte"); code != http.StatusBadRequest {
		t.Fatalf("a webhook whose body never arrived was answered %d", code)
	}

	// The holder of the token is not bounded: the rest of the body, sent
	// after three times the bound, is taken.
	held := slowPost(t, addr, "/hooks/a-plugin/in", addr, cookie, whole, 1)
	time.Sleep(3 * bound)
	if _, err := io.WriteString(held, whole[1:]); err != nil {
		t.Fatalf("the token holder's connection was closed under its body: %v", err)
	}
	_ = held.SetReadDeadline(time.Now().Add(5 * time.Second))
	res, err := http.ReadResponse(bufio.NewReader(held), nil)
	if err != nil {
		t.Fatalf("the token holder's slow body was not answered: %v", err)
	}
	res.Body.Close()
	if werr := <-worked; werr != nil || res.StatusCode != http.StatusNoContent {
		t.Fatalf("THE TOKEN HOLDER'S BODY WAS CUT OFF AT THE BOUND MEANT FOR STRANGERS: answered %d, %v", res.StatusCode, werr)
	}

	// A body that arrived whole, from a peer with no token: the handler
	// works on for longer than the bound and is not cancelled.
	hook := slowPost(t, addr, "/hooks/a-plugin/in", addr, "", "{}", 2)
	_ = hook.SetReadDeadline(time.Now().Add(5 * time.Second))
	res, err = http.ReadResponse(bufio.NewReader(hook), nil)
	if err != nil {
		t.Fatalf("a webhook whose body arrived whole was not answered: %v", err)
	}
	res.Body.Close()
	if werr := <-worked; werr != nil || res.StatusCode != http.StatusNoContent {
		t.Fatalf("THE BODY'S DEADLINE OUTLIVED THE BODY: answered %d, %v", res.StatusCode, werr)
	}
}

// THE BOUND IS THE OPERATOR'S (dashboard.stranger_body_seconds). It was a
// constant, ten seconds; it is read at each request that has proved
// nothing, held to one second to ten minutes, and ten seconds unset. A body
// that stops after one byte is answered, and its connection closed, at the
// bound the operator set: one second here, well inside the five a test
// waits, where the old constant would have held it ten.
func TestTheStrangersBodyBoundIsTheOperatorsSetting(t *testing.T) {
	var set atomic.Int64
	s := New("127.0.0.1", 0, &WSHandler{GetConfig: func() *ConfigState { return &ConfigState{} }, StrangerBodySeconds: func() int { return int(set.Load()) }})
	for stated, want := range map[int]time.Duration{
		0:      DefaultStrangerBodySeconds * time.Second,
		-3:     DefaultStrangerBodySeconds * time.Second,
		30:     30 * time.Second,
		100000: MaxStrangerBodySeconds * time.Second,
	} {
		set.Store(int64(stated))
		if got := s.strangerBodyTime(); got != want {
			t.Errorf("dashboard.stranger_body_seconds %d: the bound in force is %s; want %s", stated, got, want)
		}
	}
	if got := New("127.0.0.1", 0, &WSHandler{}).strangerBodyTime(); got != 10*time.Second {
		t.Errorf("with no setting the bound is %s; want the ten seconds it always was", got)
	}

	set.Store(MinStrangerBodySeconds)
	s.SetAccessToken(true, "the-token-in-force") // with no token asked for, every peer is the operator and nothing is bounded
	addr, err := s.Start("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Shutdown(context.Background()) })
	began := time.Now()
	if code := answeredAndClosed(t, slowPost(t, addr, "/auth/token", "elsewhere.example.test:80", "", strings.Repeat("x", 64), 1), 5*time.Second, "a body that stops after one byte, under a bound of one second"); code != http.StatusForbidden {
		t.Fatalf("a foreign Host was answered %d", code)
	}
	if took := time.Since(began); took < time.Second {
		t.Fatalf("the body was given %s, under the second the operator set", took)
	}
}
