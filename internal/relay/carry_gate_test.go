package relay

import (
	"context"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	aiicrypto "github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/witness"
)

func TestTheIdentityBoundsWhatARelaysRequestsMakeItSpend(t *testing.T) {
	at := time.Date(2026, 10, 12, 22, 0, 0, 0, time.UTC)
	set := Tuning{Carried: 3, AttachesPerSecond: 2, AttachBurst: 4}
	c := &Client{Name: "fixture.example.test", Relay: "fixture.test:443", Tune: func() Tuning { return set }}
	admitted := func(n int, now time.Time) (took int) {
		for range n {
			if c.admitOpen(now) {
				took++
			}
		}
		return took
	}
	if got := admitted(10, at); got != 3 {
		t.Fatalf("with room for three at once, %d of ten requests at one moment were answered", got)
	}
	for range 3 {
		c.carriedDone()
	}
	if got := admitted(10, at); got != 1 {
		t.Fatalf("a burst of four had three spent, and %d more were answered at the same moment, want one", got)
	}
	c.carriedDone()
	if got := admitted(10, at.Add(-time.Hour)); got != 0 {
		t.Fatalf("the clock stepped back and %d requests were answered on time that had not passed", got)
	}
	// The gate now reads the earlier time; one second on from it, two.
	if got := admitted(10, at.Add(-time.Hour).Add(time.Second)); got != 2 {
		t.Fatalf("a second at two a second answered %d requests, want two", got)
	}
	c.carriedDone()
	c.carriedDone()
	if got := admitted(10, at.Add(24*time.Hour)); got != 3 {
		t.Fatalf("after a day the burst is four and the room three, and %d were answered: the burst grew past its size, or the room was not held", got)
	}
	for range 3 {
		c.carriedDone()
	}
	// A burst set lower applies to what is already saved up.
	set.AttachBurst = 1
	if got := admitted(10, at.Add(48*time.Hour)); got != 1 {
		t.Fatalf("with the burst set to one, %d requests were answered at one moment", got)
	}

	plain := &Client{Name: "fixture.example.test", Relay: "fixture.test:443"}
	took := 0
	for range 200 {
		if plain.admitOpen(at) {
			took++
		}
	}
	if took != AttachBurst {
		t.Fatalf("with nothing set, %d of two hundred requests at one moment were answered, want the default burst of %d", took, AttachBurst)
	}
	// The burst comes back with time; the room does not, while none has ended.
	for _, later := range []time.Duration{time.Hour, 2 * time.Hour, 3 * time.Hour} {
		for range 200 {
			if plain.admitOpen(at.Add(later)) {
				took++
			}
		}
	}
	if took != CarriedAtOnce {
		t.Fatalf("with nothing set and none ended, %d connections were carried at once, want the default of %d", took, CarriedAtOnce)
	}
}

func TestAFloodOfRequestsIsAnsweredAtTheIdentitysOwnRate(t *testing.T) {
	kp, err := aiicrypto.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	key := witness.AsIdentityKey(kp)
	raw, env, err := witness.EnsureIdentityEnvelope(key, &lifecycleEnvelope{}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	said := logsink.CaptureForTest(t)
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		control, server := net.Pipe()
		defer server.Close()
		c := &Client{Relay: "fixture.test:443", Name: "fixture.example.test", Key: key, Env: env, Envelope: raw,
			Tune: func() Tuning { return Tuning{Carried: 100, AttachesPerSecond: 1, AttachBurst: 5} }}
		var dials, attaches atomic.Int64
		c.dialRelay = func(context.Context) (net.Conn, error) {
			if dials.Add(1) == 1 {
				return control, nil
			}
			// An attach: the relay reads it and says the browser has gone.
			up, relaySide := net.Pipe()
			go func() {
				defer relaySide.Close()
				if m, err := ReadMessage(NewReader(relaySide)); err == nil && m.Type == "attach" {
					attaches.Add(1)
					_ = WriteMessage(relaySide, Message{Type: "refused", Code: "unknown_connection", Reason: "unknown connection"})
				}
			}()
			return up, nil
		}
		ask := func(n int) {
			for range n {
				if err := WriteMessage(server, Message{Type: "open", Conn: "fixture"}); err != nil {
					t.Error(err)
					return
				}
			}
		}
		go func() {
			if _, err := ReadMessage(NewReader(server)); err != nil {
				t.Error(err)
				return
			}
			if err := WriteMessage(server, Message{Type: "registered"}); err != nil {
				t.Error(err)
				return
			}
			ask(40)
		}()
		go c.Run(ctx)
		synctest.Wait()
		if got := attaches.Load(); got != 5 {
			t.Fatalf("forty requests at one moment were answered with %d connections, want the burst of five", got)
		}
		if got := dials.Load(); got != 6 {
			t.Fatalf("the identity dialled %d times for forty requests, want once for its control connection and five times more", got)
		}
		time.Sleep(3 * time.Second)
		go ask(10)
		synctest.Wait()
		if got := attaches.Load(); got != 8 {
			t.Fatalf("three seconds on at one a second, ten more requests brought the answered to %d, want eight", got)
		}
		time.Sleep(DeclinedSaidEvery)
		synctest.Wait()
		cancel()
		synctest.Wait()
	})
	if n := strings.Count(said.String(), "were not carried"); n != 1 {
		t.Fatalf("forty-two requests were left unanswered and the log said so %d times, want once:\n%s", n, said.String())
	}
	if !said.Contains("42 browser connection(s) were not carried") || !said.Contains("certificate.relay_attaches_per_second") {
		t.Fatalf("the log does not say how many were left and which settings bound it:\n%s", said.String())
	}
}
