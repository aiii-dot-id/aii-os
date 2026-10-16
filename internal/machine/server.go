package machine

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

var ErrHandover = errors.New("the machine service handed over to its newer program")

const (
	handshakeWait = 10 * time.Second

	defaultWatchEvery = 5 * time.Second

	successorWait = 15 * time.Second
)

type Options struct {
	Dir string

	Program string

	StartupSlots func() ([]string, error)

	Successor func() error

	Build Build

	watchEvery time.Duration
	now        func() time.Time

	hold func(method string)

	store storeOptions
}

type Server struct {
	opts      Options
	instance  string
	build     Build
	statePath string

	mu       sync.Mutex
	state    State
	conns    map[net.Conn]struct{}
	stopping bool
	links    map[*link]struct{}
	inFlight int
	update   *Update
	handing  bool
	handover chan struct{}

	store *storeService

	say *Speaks
}

type link struct {
	conn     net.Conn
	identity string
	slot     string
}

func NewServer(opts Options) *Server {
	if opts.watchEvery <= 0 {
		opts.watchEvery = defaultWatchEvery
	}
	if opts.now == nil {
		opts.now = time.Now
	}
	if opts.Build == (Build{}) {
		opts.Build = Self()
	}
	if opts.StartupSlots == nil {
		opts.StartupSlots = func() ([]string, error) { return nil, nil }
	}
	return &Server{
		opts:      opts,
		instance:  randomHex(16),
		build:     opts.Build,
		statePath: filepath.Join(opts.Dir, stateFileName),
		conns:     map[net.Conn]struct{}{},
		links:     map[*link]struct{}{},
		handover:  make(chan struct{}),
	}
}

func (s *Server) Serve(ctx context.Context) error {
	h, err := Listen(s.opts.Dir)
	if err != nil {
		return err
	}
	st, err := loadState(s.statePath)
	if err != nil {
		h.Close()
		return err
	}
	s.mu.Lock()
	s.state = st
	s.mu.Unlock()

	s.openStore(ctx)

	if err := os.Remove(filepath.Join(s.opts.Dir, failureFile)); err != nil && !errors.Is(err, os.ErrNotExist) {
		logsink.Warn("machine.error", "the last failure's record was not removed: %v", err)
	}
	logsink.Info("machine.start", "machine service %s (%s) holds its link at %s; %d identities recorded", s.instance, s.build, h.Path(), len(st.Identities))

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		s.accept(ctx, h.Listener(), &wg)
	}()
	if s.opts.Program != "" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.watchProgram(ctx)
		}()
	}
	stopRequested := watchStop(ctx, s.opts.Dir, &wg)

	handover := false
	select {
	case <-ctx.Done():
	case <-stopRequested:
	case <-s.handover:
		handover = true
	}
	cancel()

	lerr := h.Listener().Close()
	for _, c := range s.closeAll() {
		c.Close()
	}
	wg.Wait()
	s.closeStore()
	if !handover {
		logsink.Info("machine.end", "machine service %s stopped", s.instance)
		return errors.Join(ignoreClosed(lerr), h.Close())
	}

	if err := h.Close(); err != nil {
		logsink.Warn("machine.error", "releasing the link before the handover: %v", err)
	}
	if s.opts.Successor != nil {
		if err := s.opts.Successor(); err != nil {
			logsink.Error("machine.error", "the newer program could not be started: %v — an identity host starts it again once the link has been gone a few seconds", err)
		} else if !awaitLink(s.opts.Dir, successorWait) {
			logsink.Warn("machine.end", "the newer program did not take the link within %s; this one exits regardless", successorWait)
		}
	}
	logsink.Info("machine.end", "machine service %s handed over to its newer program", s.instance)
	return ErrHandover
}

func ignoreClosed(err error) error {
	if errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}

func awaitLink(dir string, within time.Duration) bool {
	path, err := LinkPath(dir)
	if err != nil {
		return false
	}
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if c, err := dialLink(context.Background(), path, ""); err == nil {
			c.Close()
			return true
		}
		time.Sleep(200 * time.Millisecond)
	}
	return false
}

func (s *Server) accept(ctx context.Context, ln net.Listener, wg *sync.WaitGroup) {
	for {
		c, err := ln.Accept()
		if err != nil {
			if ctx.Err() == nil && !errors.Is(err, net.ErrClosed) {
				logsink.Error("machine.error", "the link stopped accepting: %v", err)
			}
			return
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.serveConn(ctx, c)
		}()
	}
}

func (s *Server) closeAll() []net.Conn {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopping = true
	out := make([]net.Conn, 0, len(s.conns))
	for c := range s.conns {
		out = append(out, c)
	}
	return out
}

func (s *Server) track(c net.Conn) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopping {
		return false
	}
	s.conns[c] = struct{}{}
	return true
}

func (s *Server) untrack(c net.Conn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.conns, c)
}

func (s *Server) serveConn(ctx context.Context, c net.Conn) {
	defer c.Close()

	if !s.track(c) {
		return
	}
	defer s.untrack(c)
	nonce := randomHex(32)
	if err := writeMessage(c, message{Method: MsgHello, Params: mustJSON(helloParams{Service: s.instance, Nonce: nonce, Speaks: s.speaks(), Build: s.build})}); err != nil {
		return
	}
	_ = c.SetReadDeadline(time.Now().Add(handshakeWait))
	m, err := readMessage(c)
	if err != nil {
		return
	}
	if m.Method != MsgHandshake || len(m.ID) == 0 {
		_ = writeMessage(c, refusalReply(m.ID, &Refusal{Reason: ReasonBadRequest, Message: "a link begins with a handshake"}))
		return
	}
	res, slot, ref := s.handshake(nonce, m.Params)
	if ref != nil {
		logsink.Warn("machine.refusal", "a link was refused: %s", ref.Message)
		_ = writeMessage(c, refusalReply(m.ID, ref))
		return
	}
	l := &link{conn: c, identity: res.Identity, slot: slot}
	if !s.addLink(l) {
		_ = writeMessage(c, refusalReply(m.ID, &Refusal{Reason: ReasonHandingOver, Message: "the machine service is handing over to its newer program", Remedy: "the identity links again in a moment"}))
		return
	}
	defer s.dropLink(l)
	if err := writeMessage(c, message{ID: m.ID, Result: mustJSON(res)}); err != nil {
		return
	}
	_ = c.SetReadDeadline(time.Time{})
	for {
		m, err := readMessage(c)
		if err != nil {
			return
		}
		if len(m.ID) == 0 {
			continue
		}
		if !s.begin() {
			_ = writeMessage(c, refusalReply(m.ID, &Refusal{Reason: ReasonHandingOver, Message: "the machine service is handing over to its newer program", Remedy: "the identity links again in a moment"}))
			return
		}
		if s.opts.hold != nil {
			s.opts.hold(m.Method)
		}
		reply, after := s.dispatch(l, m)
		werr := writeMessage(c, reply)
		s.end()
		if after != nil {
			after()
		}
		if werr != nil {
			return
		}
	}
}

func refusalReply(id json.RawMessage, r *Refusal) message {
	code := codeRefused
	switch r.Reason {
	case ReasonBadRequest:
		code = codeInvalidParams
	case ReasonNotSpoken:
		code = codeMethodNotFound
	}
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	return message{ID: id, Error: &rpcError{Code: code, Message: r.Message, Data: r}}
}

func (s *Server) handshake(nonce string, raw json.RawMessage) (handshakeResult, string, *Refusal) {
	var p handshakeParams
	if err := decodeStrict(raw, &p); err != nil {
		return handshakeResult{}, "", &Refusal{Reason: ReasonBadRequest, Message: "the handshake does not read: " + err.Error()}
	}
	if p.Nonce != nonce {
		return handshakeResult{}, "", &Refusal{Reason: ReasonReplayedNonce, Message: "the handshake signs a nonce this connection did not issue"}
	}
	pub, err := base64.StdEncoding.DecodeString(p.PublicKey)
	if err != nil || crypto.PublicKeyFingerprint(pub) != p.Identity {
		return handshakeResult{}, "", &Refusal{Reason: ReasonBadSignature, Message: "the public key is not identity " + short(p.Identity) + "'s"}
	}
	sig, err := base64.StdEncoding.DecodeString(p.Signature)
	if err != nil || crypto.Verify(pub, linkInput(s.instance, nonce, p.Identity), sig) != nil {
		return handshakeResult{}, "", &Refusal{Reason: ReasonBadSignature, Message: "the handshake's signature does not verify under identity " + short(p.Identity) + "'s key"}
	}
	slot, err := resolveSlot(p.Slot)
	if err != nil {
		return handshakeResult{}, "", &Refusal{Reason: ReasonBadRequest, Message: err.Error()}
	}
	registered := false
	if !p.Register {

		dirs, err := s.opts.StartupSlots()
		if err != nil {
			logsink.Warn("machine.error", "the account's startup registrations do not read: %v", err)
		}
		for _, d := range dirs {
			if r, err := resolveSlot(d); err == nil && r == slot {
				registered = true
			}
		}
	}

	rec, how, ref := s.recordLink(slot, p, registered)
	if ref != nil {
		return handshakeResult{}, "", ref
	}
	if how != "known" {
		logsink.Info("machine.decision", "recorded identity %s for slot %s (%s)", short(rec.Identity), slot, how)
	}
	return handshakeResult{Identity: p.Identity, Recorded: how}, slot, nil
}

func (s *Server) recordLink(slot string, p handshakeParams, registered bool) (Record, string, *Refusal) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, known := s.state.record(slot)
	switch {
	case p.Register:
		rec = Record{Slot: slot, Identity: p.Identity, PublicKey: p.PublicKey, How: RecordedByRegister, At: s.opts.now().UTC().Format(time.RFC3339)}
	case known && rec.Identity == p.Identity && rec.PublicKey == p.PublicKey:
		return rec, "known", nil
	case known:
		return Record{}, "", &Refusal{Reason: ReasonWrongKey,
			Message: "the slot " + slot + " is recorded with identity " + short(rec.Identity) + "'s key, and this link signed as " + short(p.Identity),
			Remedy:  "if this identity is the slot's, run aii register " + filepath.Base(slot) + " from the installed program; it replaces the record"}
	case registered:
		rec = Record{Slot: slot, Identity: p.Identity, PublicKey: p.PublicKey, How: RecordedAtFirstLink, At: s.opts.now().UTC().Format(time.RFC3339)}
	default:
		return Record{}, "", &Refusal{Reason: ReasonUnregistered,
			Message: "the slot " + slot + " is not recorded, and this account's startup registrations do not name it",
			Remedy:  "register it: aii register " + filepath.Base(slot)}
	}
	next := s.state.with(rec)
	if err := saveState(s.statePath, next); err != nil {
		return Record{}, "", &Refusal{Reason: ReasonNotRecorded, Message: "the record could not be kept: " + err.Error()}
	}
	s.state = next
	return rec, rec.How, nil
}

func resolveSlot(dir string) (string, error) {
	if !filepath.IsAbs(dir) {
		return "", fmt.Errorf("the slot %q is not an absolute path", dir)
	}
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		dir = r
	}
	return filepath.Clean(dir), nil
}

func short(fp string) string {
	if len(fp) > 12 {
		return fp[:12]
	}
	return fp
}

func (s *Server) addLink(l *link) bool {
	s.mu.Lock()
	if s.handing {
		s.mu.Unlock()
		return false
	}
	s.links[l] = struct{}{}
	n := len(s.links)
	s.mu.Unlock()
	logsink.Info("machine.session", "identity %s linked (%d links)", short(l.identity), n)
	s.storeLinked(l.slot)
	return true
}

func (s *Server) dropLink(l *link) {
	s.mu.Lock()
	delete(s.links, l)
	n := len(s.links)
	s.mu.Unlock()
	logsink.Info("machine.session", "identity %s's link ended (%d links)", short(l.identity), n)
}

func (s *Server) begin() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.handing {
		return false
	}
	s.inFlight++
	return true
}

func (s *Server) end() {
	s.mu.Lock()
	s.inFlight--
	began := s.update != nil && s.inFlight == 0 && s.beginHandoverLocked()
	s.mu.Unlock()
	if began {
		logsink.Info("machine.decision", "handing over to the newer program: the service is idle")
	}
}

func (s *Server) beginHandoverLocked() bool {
	if s.handing {
		return false
	}
	s.handing = true
	close(s.handover)
	return true
}

func (s *Server) dispatch(l *link, m message) (reply message, after func()) {
	switch m.Method {
	case MsgStatus:
		var none struct{}
		if err := decodeStrict(m.Params, &none); err != nil {
			return refusalReply(m.ID, &Refusal{Reason: ReasonBadRequest, Message: "status takes no params; a link is its own identity: " + err.Error()}), nil
		}
		return message{ID: m.ID, Result: mustJSON(s.status(l))}, nil
	case MsgUpdateForce:
		var none struct{}
		if err := decodeStrict(m.Params, &none); err != nil {
			return refusalReply(m.ID, &Refusal{Reason: ReasonBadRequest, Message: "update.force takes no params: " + err.Error()}), nil
		}
		s.mu.Lock()
		waiting := s.update != nil
		s.mu.Unlock()
		if !waiting {
			return refusalReply(m.ID, &Refusal{Reason: ReasonNoUpdate, Message: "no newer program waits"}), nil
		}
		logsink.Info("machine.decision", "identity %s's operator forced the handover", short(l.identity))
		return message{ID: m.ID, Result: mustJSON(map[string]string{"handover": "now"})}, func() {
			s.mu.Lock()
			began := s.beginHandoverLocked()
			s.mu.Unlock()
			if began {
				logsink.Info("machine.decision", "handing over to the newer program: forced by the operator")
			}
		}
	case MsgHandshake:
		return refusalReply(m.ID, &Refusal{Reason: ReasonReplayedNonce, Message: "this link has already been proved; its nonce is spent"}), nil
	}
	if s.speaks().has(m.Method) {
		if m.Method == MsgForget {
			return s.forget(l, m)
		}
		if reply, after, ok := s.dispatchStore(l, m); ok {
			return reply, after
		}
	}
	return refusalReply(m.ID, &Refusal{Reason: ReasonNotSpoken, Message: "the machine service (" + s.build.String() + ") does not speak " + m.Method}), nil
}

func (s *Server) speaks() Speaks {
	if s.say != nil {
		return *s.say
	}
	return speaks()
}

func (s *Server) status(l *link) Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := map[string]bool{}
	for k := range s.links {
		ids[k.identity] = true
	}
	st := Status{Service: s.instance, Build: s.build, Speaks: s.speaks(), Identity: l.identity, Linked: len(ids)}
	if s.update != nil {
		u := *s.update
		st.Update = &u
	}
	s.storeStatusLocked(&st, l)
	return st
}

func (s *Server) watchProgram(ctx context.Context) {
	started, err := os.Stat(s.opts.Program)
	if err != nil {
		logsink.Warn("machine.error", "the service's program %s cannot be watched for an update: %v", s.opts.Program, err)
		return
	}
	t := time.NewTicker(s.opts.watchEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		now, err := os.Stat(s.opts.Program)
		if err != nil || os.SameFile(started, now) {
			continue
		}
		b, err := ProgramBuild(s.opts.Program)
		if err != nil {
			logsink.Warn("machine.error", "a newer program is at %s, and its build does not read: %v", s.opts.Program, err)
		}
		s.mu.Lock()
		s.update = &Update{Build: b, Since: s.opts.now().UTC().Format(time.RFC3339)}
		busy := s.inFlight
		began := busy == 0 && s.beginHandoverLocked()
		s.mu.Unlock()
		if began {
			logsink.Info("machine.decision", "a newer program (%s) is at %s; handing over: the service is idle", b, s.opts.Program)
		} else {
			logsink.Info("machine.decision", "a newer program (%s) is at %s and waits for idle: %d requests in flight", b, s.opts.Program, busy)
		}
		return
	}
}
