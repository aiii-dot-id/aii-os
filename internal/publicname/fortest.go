package publicname

import (
	"context"
	"net"
	"net/url"
	"strconv"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/certs"
	"github.com/aiii-dot-id/aii-os/internal/relay"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

func (r *Runtime) ReconcileForTest(ctx context.Context) (time.Duration, error) {
	return r.reconcileRoute(ctx)
}

func (r *Runtime) StartRelayForTest(name, addr string) error {
	host, ps, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	port, err := strconv.Atoi(ps)
	if err != nil {
		return err
	}
	r.startRelay(r.w.Settings(), name, certs.RelayEndpoint{Name: host, Port: port})
	return nil
}

func (r *Runtime) RelayForTest() *relay.Client {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.relay
}

func (r *Runtime) StopRelayForTest() <-chan struct{} {
	return r.stopRelay()
}

func (r *Runtime) CarryForTest(name string, cancel context.CancelFunc, done <-chan struct{}) *relay.Client {
	c := &relay.Client{Name: name}
	r.mu.Lock()
	r.relay, r.relayCancel, r.relayDone = c, cancel, done
	r.mu.Unlock()
	return c
}

func (r *Runtime) ServeForTest(name store.PublicName, origin string, service *certs.ServiceStatus) {
	bound := ""
	if u, err := url.Parse(origin); err == nil {
		if bound = u.Port(); bound == "" && u.Scheme == "https" {
			bound = "443"
		}
	}
	r.mu.Lock()
	r.name, r.origin, r.bound, r.service = &name, origin, bound, service
	r.mu.Unlock()
}

func InterfaceAddrsForTest(fn func() ([]net.Addr, error)) (restore func()) {
	saved := interfaceAddrs
	interfaceAddrs = fn
	return func() { interfaceAddrs = saved }
}

func (r *Runtime) LookAtRouteForTest(ctx context.Context) error {
	_, err := r.lookAtRoute(ctx)
	return err
}

func (r *Runtime) RouteRevisionForTest() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.routeRev
}

func (r *Runtime) ManageForTest(m CertificateManager) {
	r.mu.Lock()
	r.manager = m
	r.mu.Unlock()
}

func PrepareOffByDefaultForTest() { prepareWhenUnset = false }
