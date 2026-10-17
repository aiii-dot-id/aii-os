package relay

import (
	"bufio"
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	aiicrypto "github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/witness"
)

// played is a relay a test plays by hand. Each connection the client opens
// is a pipe; play runs on the relay's end of the nth one (from 0) and the
// connection closes when it returns. dials is when each was opened, on the
// test's clock.
type played struct {
	mu    sync.Mutex
	dials []time.Time
	play  func(n int, relay net.Conn)
}

func (p *played) dial(context.Context) (net.Conn, error) {
	client, relay := net.Pipe()
	p.mu.Lock()
	n := len(p.dials)
	p.dials = append(p.dials, time.Now())
	p.mu.Unlock()
	go func() {
		defer relay.Close()
		p.play(n, relay)
	}()
	return client, nil
}

func (p *played) dialed() []time.Time {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]time.Time(nil), p.dials...)
}

// admit reads the registration and answers it with registered, carrying
// whatever the test's relay states. Nil when the client did not register.
func admit(t *testing.T, relay net.Conn, registered Message) *bufio.Reader {
	t.Helper()
	r := NewReader(relay)
	m, err := ReadMessage(r)
	if err != nil || m.Type != "register" {
		t.Errorf("the client did not open with a registration: %q, %v", m.Type, err)
		return nil
	}
	registered.Type = "registered"
	if err := WriteMessage(relay, registered); err != nil {
		t.Errorf("registered was not taken: %v", err)
		return nil
	}
	return r
}

// answer serves the control connection as a relay does: every ping is
// answered, and its arrival noted, until the connection ends.
func answer(relay net.Conn, r *bufio.Reader, ping func()) {
	for r != nil {
		m, err := ReadMessage(r)
		if err != nil {
			return
		}
		if m.Type == "ping" {
			if ping != nil {
				ping()
			}
			if WriteMessage(relay, Message{Type: "pong"}) != nil {
				return
			}
		}
	}
}

// hold keeps the connection open and answers nothing.
func hold(r *bufio.Reader) {
	for r != nil {
		if _, err := ReadMessage(r); err != nil {
			return
		}
	}
}

// leanClient is a client with a real key and envelope, made outside the
// test's clock, that draws what the test says.
func leanClient(t *testing.T, draw float64) *Client {
	t.Helper()
	kp, err := aiicrypto.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	key := witness.AsIdentityKey(kp)
	raw, env, err := witness.EnsureIdentityEnvelope(key, &lifecycleEnvelope{}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return &Client{Relay: "fixture.test:443", Name: "fixture.example.test", Key: key, Env: env, Envelope: raw, random: func() float64 { return draw }}
}

// run starts the client against the played relay and returns the step that
// stops it and waits for it to have stopped.
func run(c *Client, p *played) (stop func()) {
	c.dialRelay = p.dial
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { c.Run(ctx); close(done) }()
	return func() { cancel(); <-done }
}

// A WAIT IS DRAWN, NEVER KEPT EXACTLY. A ping is a random 80 to 100% of its
// interval; a reconnect is drawn from the upper half of a wait that doubles
// to the longest. Without the draw every identity a relay admitted in one
// second pings in one second for as long as it stays, and every identity a
// relay dropped returns in the same instant, then again, then again.
func TestAWaitIsDrawnInsideItsBounds(t *testing.T) {
	const interval = 120 * time.Second
	for _, c := range []struct {
		draw float64
		want time.Duration
	}{{0, 96 * time.Second}, {0.5, 108 * time.Second}, {0.999999, 120 * time.Second}} {
		got := pingWait(interval, c.draw)
		if got < interval*4/5 || got > interval || (got-c.want).Abs() > time.Millisecond {
			t.Errorf("a ping at draw %v waits %s, want about %s and always 80 to 100%% of %s", c.draw, got, c.want, interval)
		}
	}
	if pingWait(interval, 0) == pingWait(interval, 0.5) {
		t.Fatal("two draws gave the same wait: the pings are in step")
	}
	base, max := time.Second, time.Minute
	for failures, whole := range []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 32 * time.Second, time.Minute, time.Minute, time.Minute} {
		low, high := backoffWait(base, max, failures, 0), backoffWait(base, max, failures, 0.999999)
		if low != whole/2 || high <= low || high > whole || (whole-high) > time.Millisecond {
			t.Errorf("after %d failures the wait runs %s to %s, want the upper half of %s", failures, low, high, whole)
		}
	}
	if got := backoffWait(base, max, 1<<30, 0); got != max/2 {
		t.Fatalf("a long run of failures waits %s, want the upper half of the longest wait, from %s", got, max/2)
	}
}

// THE INTERVAL IN FORCE: the operator's when set, else the relay's, else
// the default an older relay was built with; and never more than half the
// silence limit, so that one lost ping does not end the registration.
func TestThePingIntervalInForce(t *testing.T) {
	for _, c := range []struct {
		what           string
		set            int
		stated, silent time.Duration
		want           time.Duration
		clamped        bool
	}{
		{"an older relay states nothing", 0, 0, SilentFor, PingEvery, false},
		{"the relay's stated interval", 0, 120 * time.Second, 270 * time.Second, 120 * time.Second, false},
		{"the operator's overrides the relay's", 45, 120 * time.Second, 270 * time.Second, 45 * time.Second, false},
		{"the operator's, held to half the silence limit", 3600, 120 * time.Second, 270 * time.Second, 135 * time.Second, true},
		{"a relay whose own figures disagree is held too, and that is not the operator's doing", 0, 120 * time.Second, 100 * time.Second, 50 * time.Second, false},
	} {
		got, clamped := pingInterval(c.set, c.stated, c.silent)
		if got != c.want || clamped != c.clamped {
			t.Errorf("%s: %s (held: %v), want %s (held: %v)", c.what, got, clamped, c.want, c.clamped)
		}
	}
}

func TestWhatARelayStatesIsHeldToBounds(t *testing.T) {
	for _, c := range []struct {
		what                 string
		ping, silent         time.Duration
		wantPing, wantSilent time.Duration
	}{
		{"nothing stated", 0, 0, 0, 0},
		{"inside the bounds", 25 * time.Second, 75 * time.Second, 25 * time.Second, 75 * time.Second},
		{"a ping every second", time.Second, 3 * time.Second, StatedPingLeast, 2 * StatedPingLeast},
		{"a ping every eleven days", 264 * time.Hour, 528 * time.Hour, StatedPingMost, 2 * StatedPingMost},
		{"only a silence limit, of a month", 0, 720 * time.Hour, 0, 2 * StatedPingMost},
	} {
		if ping, silent := heldStated(c.ping, c.silent); ping != c.wantPing || silent != c.wantSilent {
			t.Errorf("%s: held to a ping of %s and a silence limit of %s, want %s and %s", c.what, ping, silent, c.wantPing, c.wantSilent)
		}
	}
}

func TestARelaysWordsAreOneLineAndBounded(t *testing.T) {
	e := refusal(Message{Type: "refused", Code: "capacity", Reason: "wait\n2026-10-13 INFO the operator signed in\r\x1b[2J" + strings.Repeat("x", 5000)})
	said := e.Error()
	if strings.ContainsAny(said, "\n\r\x1b") {
		t.Fatalf("a relay's reason reached the identity's words with a control character in it: %q", said[:80])
	}
	if len(e.Reason) > 210 || !strings.HasSuffix(e.Reason, "…") {
		t.Fatalf("a relay's reason of five thousand characters was kept at %d", len(e.Reason))
	}
	if got := relaysWords("conn-0123456789abcdef", 64); got != "conn-0123456789abcdef" {
		t.Fatalf("an ordinary id was changed: %q", got)
	}
	if e := refusal(Message{Code: "capacity", Reason: "the relay is full"}); e.Code != "capacity" || e.Reason != "the relay is full" {
		t.Fatalf("an ordinary refusal was changed: %+v", e)
	}
}

// AN OLDER RELAY IS MET AS BEFORE, AND A NEWER ONE IS FOLLOWED. A relay
// that states nothing is pinged inside thirty seconds and given ninety to
// be heard from; one that states 120 and 270 is pinged inside 120 and
// given 270. Two identities admitted in the same instant do not ping in
// the same instant.
func TestTheRelaysStatedIntervalsAreFollowedAndAnOlderRelayIsMetAsBefore(t *testing.T) {
	for _, c := range []struct {
		what       string
		registered Message
		interval   time.Duration
		silent     time.Duration
	}{
		{"an older relay", Message{}, PingEvery, SilentFor},
		{"a relay that states its own", Message{PingSeconds: 120, SilentSeconds: 270}, 120 * time.Second, 270 * time.Second},
		// What a relay states is held to bounds on the connection itself.
		{"a relay that states a ping every second", Message{PingSeconds: 1, SilentSeconds: 2}, StatedPingLeast, 2 * StatedPingLeast},
		{"a relay that states a ping every eleven days", Message{PingSeconds: 950400, SilentSeconds: 1900800}, StatedPingMost, 2 * StatedPingMost},
	} {
		early, late := leanClient(t, 0), leanClient(t, 0.5)
		quiet := leanClient(t, 0)
		synctest.Test(t, func(t *testing.T) {
			start := time.Now()
			first := map[*Client]time.Duration{}
			var mu sync.Mutex
			var stops []func()
			for _, cl := range []*Client{early, late} {
				p := &played{play: func(n int, relay net.Conn) {
					answer(relay, admit(t, relay, c.registered), func() {
						mu.Lock()
						if _, seen := first[cl]; !seen {
							first[cl] = time.Since(start)
						}
						mu.Unlock()
					})
				}}
				stops = append(stops, run(cl, p))
			}
			// A relay that admits and is then never heard from again.
			silent := &played{play: func(n int, relay net.Conn) { hold(admit(t, relay, c.registered)) }}
			stops = append(stops, run(quiet, silent))

			time.Sleep(c.silent + 10*time.Second)
			mu.Lock()
			a, b := first[early], first[late]
			mu.Unlock()
			if a != c.interval*4/5 || b != c.interval*9/10 {
				t.Errorf("%s: first pings after %s and %s, want %s and %s (80%% and 90%% of %s)", c.what, a, b, c.interval*4/5, c.interval*9/10, c.interval)
			}
			// Silence is given the limit and no longer: the second connection
			// opens at the limit plus the shortest wait.
			dials := silent.dialed()
			if len(dials) < 2 || dials[1].Sub(start) != c.silent+BackoffBase/2 {
				t.Errorf("%s: a silent relay was left and returned to at %v, want one return at %s", c.what, dials, c.silent+BackoffBase/2)
			}
			for _, stop := range stops {
				stop()
			}
		})
	}
}

// EVERY CONNECTION SIGNS THE SAME REGISTRATION, WHATEVER A RELAY STATED. A
// relay may state a larger ceiling for a registration's expiry. A client
// that kept it for its next connection would, with its clock a little
// ahead, be refused by a relay holding the smaller figure before it could
// learn that figure, and could not recover. So the ceiling is not used: the
// same client, having been told 900 seconds, signs 840 on its next
// connection, as on its first. And a registration's expiry bounds its
// admission, not the connection: a healthy one is kept long past it.
func TestEveryConnectionSignsTheSameRegistration(t *testing.T) {
	c := leanClient(t, 0)
	synctest.Test(t, func(t *testing.T) {
		var mu sync.Mutex
		var ahead []time.Duration // how far ahead each registration was signed to expire
		drop := make(chan struct{})
		p := &played{}
		p.play = func(n int, relay net.Conn) {
			r := NewReader(relay)
			m, err := ReadMessage(r)
			if err != nil {
				return
			}
			expires, err := time.Parse(time.RFC3339, m.ExpiresAt)
			if err != nil {
				t.Errorf("a registration with no readable expiry: %q", m.ExpiresAt)
			}
			mu.Lock()
			ahead = append(ahead, time.Until(expires))
			mu.Unlock()
			// A relay that allows more than the client asks for, and says so.
			_ = WriteMessage(relay, Message{Type: "registered", RegisterWindowSeconds: 900})
			if n == 0 {
				go answer(relay, r, nil)
				<-drop
				return
			}
			answer(relay, r, nil)
		}
		stop := run(c, p)
		// The first connection outlives its registration's expiry untouched.
		time.Sleep(RegisterLifetime + 5*time.Minute)
		if d := p.dialed(); len(d) != 1 {
			t.Fatalf("a healthy connection was replaced %d time(s) by its registration's expiry", len(d)-1)
		}
		close(drop)
		time.Sleep(10 * time.Second)
		stop()
		mu.Lock()
		defer mu.Unlock()
		// An expiry is written to the second, so one signed part way through
		// a second reads up to a second short.
		same := func(d time.Duration) bool { return d <= RegisterLifetime && d > RegisterLifetime-time.Second }
		if len(ahead) != 2 || !same(ahead[0]) || !same(ahead[1]) {
			t.Fatalf("registrations were signed %v ahead, want %s each: the relay's stated ceiling was used", ahead, RegisterLifetime)
		}
	})
}

// THE COUNT OF FAILURES STARTS AGAIN ONLY AFTER THE RELAY HAS ANSWERED A
// PING. A relay that admits and drops is a relay that is failing: reset on
// registration alone, it would be returned to at the shortest wait for as
// long as it went on.
func TestTheCountOfFailuresStartsAgainOnlyAfterAnAnsweredPing(t *testing.T) {
	c := leanClient(t, 0)
	synctest.Test(t, func(t *testing.T) {
		p := &played{}
		p.play = func(n int, relay net.Conn) {
			r := admit(t, relay, Message{})
			switch {
			case n < 3: // admitted, then dropped at once
			case n == 3: // admitted, one ping answered, then dropped
				if r == nil {
					return
				}
				if m, err := ReadMessage(r); err == nil && m.Type == "ping" {
					_ = WriteMessage(relay, Message{Type: "pong"})
				}
			default:
				hold(r)
			}
		}
		stop := run(c, p)
		time.Sleep(40 * time.Second)
		stop()
		d := p.dialed()
		if len(d) < 5 {
			t.Fatalf("the client returned %d times in forty seconds, want five connections", len(d))
		}
		gaps := []time.Duration{d[1].Sub(d[0]), d[2].Sub(d[1]), d[3].Sub(d[2]), d[4].Sub(d[3])}
		// Draw 0: half a second, one, two; then the answered ping at 24 s and
		// the shortest wait again.
		want := []time.Duration{BackoffBase / 2, BackoffBase, 2 * BackoffBase, PingEvery*4/5 + BackoffBase/2}
		for i := range want {
			if gaps[i] != want[i] {
				t.Fatalf("the waits between connections were %v, want %v: a registration that was dropped counted as a success, or an answered ping did not", gaps, want)
			}
		}
	})
}

// IDENTITIES DROPPED TOGETHER DO NOT RETURN TOGETHER. Ten clients lose
// their relay in the same instant; their returns are spread over half the
// wait. With the wait kept exactly, all ten return in the same instant.
func TestClientsDroppedTogetherDoNotReturnTogether(t *testing.T) {
	var clients []*Client
	for i := 0; i < 10; i++ {
		clients = append(clients, leanClient(t, float64(i)/10))
	}
	synctest.Test(t, func(t *testing.T) {
		var relays []*played
		var stops []func()
		for _, cl := range clients {
			p := &played{play: func(n int, relay net.Conn) {
				if r := admit(t, relay, Message{}); n > 0 {
					hold(r)
				}
			}}
			relays = append(relays, p)
			stops = append(stops, run(cl, p))
		}
		time.Sleep(5 * time.Second)
		seen := map[time.Duration]bool{}
		var least, most time.Duration
		for i, p := range relays {
			d := p.dialed()
			if len(d) != 2 {
				t.Fatalf("client %d opened %d connections, want the first and one return", i, len(d))
			}
			back := d[1].Sub(d[0])
			if seen[back.Truncate(40*time.Millisecond)] {
				t.Fatalf("two clients returned within 40 ms of each other, at %s", back)
			}
			seen[back.Truncate(40*time.Millisecond)] = true
			if i == 0 || back < least {
				least = back
			}
			if back > most {
				most = back
			}
		}
		if least != BackoffBase/2 || most-least < 400*time.Millisecond || most > BackoffBase {
			t.Fatalf("ten returns ran from %s to %s, want them spread over the upper half of %s", least, most, BackoffBase)
		}
		for _, stop := range stops {
			stop()
		}
	})
}

// A REFUSAL IS READ BY ITS CODE, AND ITS FLOOR GOES UNDER THE DRAW. The card
// is told which requirement was unmet in words; the next attempt waits the
// relay's floor and the client's own draw, so identities told the same
// floor do not return together; and a floor is honoured up to the longest
// wait the operator set and no further.
func TestARefusalCarriesItsCodeAndItsFloor(t *testing.T) {
	c := leanClient(t, 0)
	synctest.Test(t, func(t *testing.T) {
		p := &played{}
		p.play = func(n int, relay net.Conn) {
			r := NewReader(relay)
			if _, err := ReadMessage(r); err != nil {
				return
			}
			switch n {
			case 0:
				_ = WriteMessage(relay, Message{Type: "refused", Code: "capacity", RetryAfterSeconds: 30, Reason: "registration retry limit reached"})
			case 1:
				_ = WriteMessage(relay, Message{Type: "refused", Code: "capacity", RetryAfterSeconds: 3600, Reason: "registration retry limit reached"})
			case 2:
				_ = WriteMessage(relay, Message{Type: "refused", Reason: "a refusal from a relay that sends no code"})
			default:
				_ = WriteMessage(relay, Message{Type: "registered"})
				hold(r)
			}
		}
		stop := run(c, p)
		time.Sleep(time.Second)
		if st := c.State(); st.Refused != "capacity" || st.Connected || !strings.Contains(st.LastError, "at its limit") || !strings.Contains(st.LastError, "registration retry limit reached") {
			t.Fatalf("the refusal was not kept by its code and said in words: %+v", st)
		}
		// The third refusal lands at 91.5 s and the registration at 93.5 s.
		time.Sleep(91 * time.Second)
		if st := c.State(); st.Refused != "" || !strings.Contains(st.LastError, "a refusal from a relay that sends no code") {
			t.Fatalf("a refusal with no code kept the code before it, or lost the relay's words: %+v", st)
		}
		time.Sleep(10 * time.Second)
		if st := c.State(); !st.Connected || st.Refused != "" || st.LastError != "" {
			t.Fatalf("registered, and the card still says refused: %+v", st)
		}
		stop()
		d := p.dialed()
		if len(d) != 4 {
			t.Fatalf("%d connections, want three refusals and the registration", len(d))
		}
		// Draw 0. The floor of 30 s under half a second; then the floor of an
		// hour held to the longest wait, a minute, under one second; then no
		// floor, two seconds.
		got := []time.Duration{d[1].Sub(d[0]), d[2].Sub(d[1]), d[3].Sub(d[2])}
		want := []time.Duration{30*time.Second + BackoffBase/2, BackoffMax + BackoffBase, 2 * BackoffBase}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("the waits after the refusals were %v, want %v", got, want)
			}
		}
	})
	for _, code := range []string{"not_assigned", "owner", "window", "nonce", "signature", "changed", "capacity", "unknown_connection", "malformed"} {
		if said := (&Refused{Code: code}).Error(); refusalWords[code] == "" || said != refusalWords[code] {
			t.Errorf("the relay's code %q has no words of its own: %q", code, said)
		}
	}
	if said := (&Refused{Code: "a_code_from_a_later_relay", Reason: "its own words"}).Error(); !strings.Contains(said, "its own words") {
		t.Errorf("a code this client does not know lost the relay's words: %q", said)
	}
}

// THE SERVERS THE RELAY NAMES ARE HELD, REPLACED, AND NOT HANDED OUT PAST
// THEIR TIME. registered names them; servers replaces them on the same
// connection, before the credential among them stops being accepted; a
// credential past that time is not given to a page; and a relay that names
// none at a later registration has none.
func TestTheRelaysServersAreHeldReplacedAndNotHandedOutPastTheirTime(t *testing.T) {
	c := leanClient(t, 0)
	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		at := func(d time.Duration) string { return start.Add(d).UTC().Format(time.RFC3339) }
		turn := func(credential string) []SessionServer {
			return []SessionServer{{URLs: []string{"stun:turn.example.test:3478"}}, {URLs: []string{"turn:turn.example.test:3478?transport=udp"}, Username: "1:fixture.example.test", Credential: credential}}
		}
		renew, drop := make(chan struct{}), make(chan struct{})
		p := &played{}
		p.play = func(n int, relay net.Conn) {
			if n > 0 {
				hold(admit(t, relay, Message{}))
				return
			}
			r := admit(t, relay, Message{SessionServers: turn("first"), SessionServersExpireAt: at(12 * time.Hour)})
			go answer(relay, r, nil)
			<-renew
			_ = WriteMessage(relay, Message{Type: "servers", SessionServers: turn("second"), SessionServersExpireAt: at(20 * time.Hour)})
			<-drop
		}
		stop := run(c, p)
		synctest.Wait()
		held, to := c.SessionServers()
		if len(held) != 2 || held[1].Credential != "first" || held[1].Username != "1:fixture.example.test" || len(held[0].URLs) != 1 || !to.Equal(start.Add(12*time.Hour)) {
			t.Fatalf("what registered named was not held: %+v until %s", held, to)
		}
		time.Sleep(8 * time.Hour)
		close(renew)
		synctest.Wait()
		if held, to = c.SessionServers(); len(held) != 2 || held[1].Credential != "second" || !to.Equal(start.Add(20*time.Hour)) {
			t.Fatalf("servers did not replace what was held: %+v until %s", held, to)
		}
		time.Sleep(13 * time.Hour)
		if held, _ = c.SessionServers(); held != nil {
			t.Fatalf("a credential was handed out an hour past its time: %+v", held)
		}
		// The connection drops; the relay answers the next registration and
		// names nothing.
		c.holdServers(Message{SessionServers: turn("third"), SessionServersExpireAt: at(40 * time.Hour)})
		close(drop)
		time.Sleep(5 * time.Second)
		if held, to = c.SessionServers(); len(held) != 0 || !to.IsZero() {
			t.Fatalf("a relay that names no servers left the earlier ones held: %+v until %s", held, to)
		}
		stop()
	})
}

func TestADeviceThatComesBackFindsOutAtOnceWhetherItsConnectionHeld(t *testing.T) {
	dead, alive, waiting := leanClient(t, 0), leanClient(t, 0), leanClient(t, 0)
	// A long wait between attempts, so that only Wake can explain an early one.
	waiting.Tune = func() Tuning { return Tuning{BackoffBase: time.Minute, BackoffMax: time.Minute} }
	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		// The relay admitted this one and has not been heard from since.
		silent := &played{play: func(n int, relay net.Conn) { hold(admit(t, relay, Message{})) }}
		// This one answers every ping.
		pings := 0
		var mu sync.Mutex
		healthy := &played{play: func(n int, relay net.Conn) {
			answer(relay, admit(t, relay, Message{}), func() { mu.Lock(); pings++; mu.Unlock() })
		}}
		// This one refuses the first connection outright.
		refusing := &played{play: func(n int, relay net.Conn) {
			if n > 0 {
				hold(admit(t, relay, Message{}))
			}
		}}
		stops := []func(){run(dead, silent), run(alive, healthy), run(waiting, refusing)}

		time.Sleep(10 * time.Second)
		dead.Wake()
		alive.Wake()
		waiting.Wake()
		time.Sleep(20 * time.Second)

		// Dead: pinged at 10 s, given the answer time, replaced at 25 s and the
		// shortest wait. Without Wake that is the silence limit, 90 s.
		if d := silent.dialed(); len(d) != 2 || d[1].Sub(start) != 10*time.Second+AnswerWithin+BackoffBase/2 {
			t.Errorf("a connection that died in the sleep was replaced at %v, want once, %s after the start", d, 10*time.Second+AnswerWithin+BackoffBase/2)
		}
		// Alive: asked at 10 s, answered, and the connection kept. The ping
		// that was due at 24 s is counted again from the one just sent.
		mu.Lock()
		got := pings
		mu.Unlock()
		if d := healthy.dialed(); len(d) != 1 || got != 1 {
			t.Errorf("a live connection was asked %d times in thirty seconds and opened %d times, want the one ping at 10 s and one connection", got, len(d))
		}
		// Waiting: the first attempt failed at once and the next was thirty
		// seconds off; Wake brought it to 10 s.
		if d := refusing.dialed(); len(d) != 2 || d[1].Sub(start) != 10*time.Second {
			t.Errorf("a client waiting to try again tried at %v, want at 10 s, when it was woken", d)
		}
		for _, stop := range stops {
			stop()
		}
	})
}

// EVERY DURATION OF THE RELAY CLIENT IS A DEFAULT IN ONE TABLE. A wait
// written where it is used is a setting nobody can set. This refuses a
// unit of time anywhere in the package outside protocol.go's table, except
// to turn a number of seconds or milliseconds that came from the relay or
// the configuration into a duration.
func TestEveryDurationOfTheRelayClientIsADefault(t *testing.T) {
	units := map[string]bool{"Nanosecond": true, "Microsecond": true, "Millisecond": true, "Second": true, "Minute": true, "Hour": true}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	checked := 0
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		file, err := parser.ParseFile(fset, name, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		checked++
		isUnit := func(e ast.Expr) bool {
			sel, ok := e.(*ast.SelectorExpr)
			if !ok {
				return false
			}
			pkg, ok := sel.X.(*ast.Ident)
			return ok && pkg.Name == "time" && units[sel.Sel.Name]
		}
		converts := func(e ast.Expr) bool {
			call, ok := e.(*ast.CallExpr)
			if !ok {
				return false
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return false
			}
			pkg, ok := sel.X.(*ast.Ident)
			return ok && pkg.Name == "time" && sel.Sel.Name == "Duration"
		}
		allowed := map[ast.Expr]bool{}
		ast.Inspect(file, func(n ast.Node) bool {
			switch v := n.(type) {
			case *ast.GenDecl:
				// protocol.go's constants are the table.
				if name == "protocol.go" && v.Tok == token.CONST {
					return false
				}
			case *ast.BinaryExpr:
				if v.Op == token.MUL && converts(v.X) && isUnit(v.Y) {
					allowed[v.Y] = true
				}
			case *ast.SelectorExpr:
				if isUnit(v) && !allowed[v] {
					t.Errorf("%s: a duration is written here and not in protocol.go's defaults", fset.Position(v.Pos()))
				}
			}
			return true
		})
	}
	if checked < 2 {
		t.Fatalf("read %d source files of the package, want the client and the protocol", checked)
	}
}
