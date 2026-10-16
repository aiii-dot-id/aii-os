//go:build (linux && !android) || (darwin && !ios) || windows

package machine

import (
	"context"
	"errors"
	"net"
	"slices"

	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
)

type TestService struct {
	srv  *Server
	ctx  context.Context
	stop context.CancelFunc
	down bool
}

func StartServiceForTest(dir string, fetch pluginhost.ModelFetcher, messages ...string) (*TestService, error) {
	if err := ensureOwnerOnlyDir(dir); err != nil {
		return nil, err
	}
	srv := NewServer(Options{Dir: dir, store: storeOptions{fetch: fetch, settleWait: -1}})
	if len(messages) > 0 {
		say := speaks()
		say.Messages = slices.DeleteFunc(say.Messages, func(m string) bool { return !slices.Contains(messages, m) && m != MsgHello && m != MsgHandshake })
		srv.say = &say
	}
	st, err := loadState(srv.statePath)
	if err != nil {
		return nil, err
	}
	srv.state = st
	ctx, cancel := context.WithCancel(context.Background())
	srv.openStore(ctx)
	return &TestService{srv: srv, ctx: ctx, stop: cancel}, nil
}

func (t *TestService) Stop() {
	t.srv.mu.Lock()
	t.down = true
	conns := make([]net.Conn, 0, len(t.srv.conns))
	for c := range t.srv.conns {
		conns = append(conns, c)
	}
	t.srv.mu.Unlock()
	for _, c := range conns {
		c.Close()
	}
	t.stop()
	t.srv.closeStore()
}

func (t *TestService) Collect() error { return t.srv.store.collect() }

func (t *TestService) ClientForTest(opts ClientOptions) *Client {
	opts.register = true
	opts.dial = func(context.Context) (net.Conn, error) {
		t.srv.mu.Lock()
		down := t.down
		t.srv.mu.Unlock()
		if down {
			return nil, errors.New("the test's machine service is stopped")
		}
		a, b := net.Pipe()
		go t.srv.serveConn(t.ctx, a)
		return b, nil
	}
	return NewClient(opts)
}

func (t *TestService) Store() string { return t.srv.store.place() }

func (t *TestService) Refs() []Ref { return t.srv.stateNow().Refs }

func (t *TestService) Unreferenced() map[string]string { return t.srv.stateNow().Unreferenced }
