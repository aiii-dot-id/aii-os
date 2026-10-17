package publicname

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/certs"
	"github.com/aiii-dot-id/aii-os/internal/cognitive"
	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// relayFixture is the runtime with what a relay's admission reads: an
// identity's key and store (a fresh one; the identity envelope is minted
// on its first use), a dashboard asking for its access token, and a
// lifecycle whose Go tracks what it starts, as the app's runBackground
// does — refusing once stopping is set.
type relayFixture struct {
	r    *Runtime
	dash *dashboard.Server

	mu       sync.Mutex
	ctx      context.Context
	stopping bool
	wg       sync.WaitGroup
}

func newRelayFixture(t *testing.T) *relayFixture {
	t.Helper()
	dir := t.TempDir()
	st, err := store.NewForTest(filepath.Join(dir, "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	kp, err := crypto.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	f := &relayFixture{dash: dashboard.New("127.0.0.1", 0, &dashboard.WSHandler{})}
	f.dash.SetAccessToken(true, "fixture-token")
	ctx, cancel := context.WithCancel(context.Background())
	f.ctx = ctx
	f.r = New(Wires{
		Lifetime: func() context.Context {
			if ctx := f.lifecycle(); ctx != nil {
				return ctx
			}
			return context.Background()
		},
		Ctx: f.lifecycle,
		Go: func(run func()) bool {
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.stopping {
				return false
			}
			f.wg.Add(1)
			go func() {
				defer f.wg.Done()
				run()
			}()
			return true
		},
		Door:        func() AppendFunc { return nil },
		Store:       func() *store.Store { return st },
		Dashboard:   func() *dashboard.Server { return f.dash },
		TIME:        func() *cognitive.TIME { return nil },
		Key:         func() *crypto.KeyPair { return kp },
		IdentityDir: func() string { return dir },
		Settings:    func() Settings { return Settings{} },
		Alert:       func(string, string) {},
		Stopping:    errors.New("the fixture is stopping"),
	})
	t.Cleanup(func() { cancel(); f.r.stopRelay(); f.wg.Wait() })
	return f
}

// lifecycle is the fixture's lifecycle context, nil when there is none.
func (f *relayFixture) lifecycle() context.Context {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ctx
}

func (f *relayFixture) published() bool {
	f.r.mu.Lock()
	defer f.r.mu.Unlock()
	return f.r.relay != nil
}

// A token downgrade that lands while the relay is being prepared is seen
// at the admission: the downgrade is the app's rearmDashboardToken, which
// relaxes the running dashboard through WithoutRelay, the relay's own
// decision lock.
func TestRelayAdmissionRechecksAuthenticationAfterPreparation(t *testing.T) {
	testRelayAdmissionAfterPreparation(t, func(f *relayFixture) {
		if !f.r.WithoutRelay(func() { f.dash.SetAccessToken(false, "") }) {
			t.Error("the downgrade was refused before any relay was admitted")
		}
	})
}

func TestRelayAdmissionHonorsChangedRouteIntent(t *testing.T) {
	testRelayAdmissionAfterPreparation(t, func(f *relayFixture) { f.r.mu.Lock(); f.r.routeRev++; f.r.mu.Unlock() })
}

func testRelayAdmissionAfterPreparation(t *testing.T, change func(*relayFixture)) {
	t.Helper()
	f := newRelayFixture(t)
	prepared, resume, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	f.r.beforeRelayAdmission = func() { close(prepared); <-resume }
	go func() {
		defer close(finished)
		f.r.startRelay(Settings{}, "fixture.example.test", certs.RelayEndpoint{Name: "127.0.0.1", Port: 1})
	}()
	select {
	case <-prepared:
	case <-time.After(5 * time.Second):
		close(resume)
		t.Fatal("relay did not reach final admission")
	}
	change(f)
	close(resume)
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("relay admission did not finish")
	}
	if f.published() {
		t.Fatal("relay published after admission became invalid")
	}
}

func TestRelayAdmissionRequiresAnAcceptingApplicationLifecycle(t *testing.T) {
	for _, state := range []string{"missing", "stopping"} {
		t.Run(state, func(t *testing.T) {
			f := newRelayFixture(t)
			f.mu.Lock()
			if state == "missing" {
				f.ctx = nil
			} else {
				f.stopping = true
			}
			f.mu.Unlock()
			f.r.startRelay(Settings{}, "fixture.example.test", certs.RelayEndpoint{Name: "127.0.0.1", Port: 1})
			if f.published() {
				t.Fatal("relay published without an accepting application lifecycle")
			}
		})
	}
}

func TestARelayAlreadyChosenKeepsCarryingBehindAShortToken(t *testing.T) {
	f := newRelayFixture(t)
	f.dash.SetAccessToken(true, "short")
	if n := f.dash.AccessTokenLength(); n >= dashboard.ShortAccessToken {
		t.Fatalf("the fixture's token is not short: %d", n)
	}
	f.r.startRelay(Settings{}, "fixture.example.test", certs.RelayEndpoint{Name: "127.0.0.1", Port: 1})
	if !f.published() {
		t.Fatal("a relay already chosen was refused its tunnel because the token is short: an upgrade would cut the operator off")
	}
}
