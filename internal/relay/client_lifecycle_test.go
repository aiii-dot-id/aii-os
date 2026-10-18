package relay

import (
	"context"
	"net"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	aiicrypto "github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/witness"
)

type lifecycleEnvelope struct{ raw []byte }

func (m *lifecycleEnvelope) SaveWitnessEnvelope(raw []byte) error    { m.raw = raw; return nil }
func (m *lifecycleEnvelope) LoadWitnessEnvelope() ([]byte, error)    { return m.raw, nil }
func (m *lifecycleEnvelope) ReplaceWitnessEnvelope(raw []byte) error { m.raw = raw; return nil }

// No system.witnessed in this fake's record: the envelope is minted.
func (m *lifecycleEnvelope) WitnessEnrollment() ([]string, time.Time, time.Time, error) {
	return nil, time.Time{}, time.Time{}, nil
}

type parkedRelayClose struct {
	net.Conn
	entered chan struct{}
	release <-chan struct{}
	once    sync.Once
}

func (c *parkedRelayClose) Close() error {
	c.once.Do(func() { close(c.entered) })
	<-c.release
	return c.Conn.Close()
}

// A cancelled attach is parked inside Close: Run must not advertise completion
// until that owned connection has actually ended. synctest proves the blocked
// state and the completion ordering, without a sleep or probabilistic loop.
func TestRelayRunJoinsCancelledAttachBeforeReturning(t *testing.T) {
	kp, err := aiicrypto.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	key := witness.AsIdentityKey(kp)
	raw, env, err := witness.EnsureIdentityEnvelope(key, &lifecycleEnvelope{}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		control, server := net.Pipe()
		attach, attachedServer := net.Pipe()
		defer server.Close()
		defer attachedServer.Close()
		release, closing, opened := make(chan struct{}), make(chan struct{}), make(chan struct{})
		parked := &parkedRelayClose{Conn: attach, entered: closing, release: release}
		c := &Client{Relay: "fixture.test:443", Name: "fixture.example.test", Key: key, Env: env, Envelope: raw}
		dials := 0
		c.dialRelay = func(context.Context) (net.Conn, error) {
			dials++
			if dials == 1 {
				return control, nil
			}
			return parked, nil
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
			if err := WriteMessage(server, Message{Type: "open", Conn: "fixture"}); err != nil {
				t.Error(err)
			}
		}()
		go func() {
			if _, err := ReadMessage(NewReader(attachedServer)); err != nil {
				t.Error(err)
				return
			}
			close(opened)
		}()
		returned := false
		go func() { c.Run(ctx); returned = true }()
		<-opened
		server.Close()
		synctest.Wait()
		select {
		case <-closing:
			t.Error("control reconnect cancelled a carried connection")
		default:
		}
		cancel()
		synctest.Wait()
		select {
		case <-closing:
		default:
			t.Error("cancel did not close the attach handshake")
		}
		if returned {
			t.Error("Run returned before its carried connection finished")
		}
		close(release)
		synctest.Wait()
		if !returned {
			t.Fatal("Run did not finish after its connection ended")
		}
	})
}
