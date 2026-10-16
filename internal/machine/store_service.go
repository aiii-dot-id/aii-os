//go:build (linux && !android) || (darwin && !ios) || windows

package machine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/plugstore"
)

const (
	storeGrace = time.Hour

	defaultSettle = 10 * time.Minute

	collectEvery = time.Hour

	needPoll = 2 * time.Second

	repairWait = 30 * time.Second

	maxNeedWorks = 64
)

var errForgotten = errors.New("this slot was unregistered: the machine service no longer records it")

type storeOptions struct {
	fetch pluginhost.ModelFetcher

	settleWait time.Duration

	collected func()

	freeDisk func(string) (int64, error)
}

type storeService struct {
	s      *Server
	acq    *pluginhost.Acquirer
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	kick   chan struct{}
	once   sync.Once

	where atomic.Pointer[string]

	spawnMu  sync.Mutex
	stopping bool

	mu sync.Mutex

	w   *plugstore.Writer
	err error
	dir string

	drain []*plugstore.Writer

	works  map[string]pluginhost.StoreWork
	adopt  map[string]pluginhost.Adoption
	repair map[string]bool
	ended  map[string]error

	linked map[string]bool
}

func (s *Server) openStore(ctx context.Context) {
	ss := &storeService{s: s, kick: make(chan struct{}, 1), works: map[string]pluginhost.StoreWork{}, adopt: map[string]pluginhost.Adoption{},
		repair: map[string]bool{}, ended: map[string]error{}, linked: map[string]bool{}}
	ss.ctx, ss.cancel = context.WithCancel(ctx)
	s.store = ss
	s.mu.Lock()
	st := s.state
	s.mu.Unlock()
	ss.dir = st.Store
	if ss.dir == "" {
		ss.dir = defaultStoreDir(s.opts.Dir)
	}
	ss.where.Store(&ss.dir)
	w, err := plugstore.OpenWriter(ss.dir)
	if err != nil {
		ss.err = err
		logsink.Error("machine.error", "the plugin store %s cannot be written: %v — every need is refused until it can, or until the operator names another place (aii machine store <dir>)", ss.dir, err)
	} else {
		ss.w = w
		logsink.Info("machine.decision", "the plugin store is %s; this service is its one writer", ss.dir)
	}
	for _, d := range st.Draining {
		dw, err := plugstore.OpenWriter(d)
		if err != nil {
			logsink.Warn("machine.error", "the plugin store %s, moved away from, cannot be drained this time: %v", d, err)
			continue
		}
		ss.drain = append(ss.drain, dw)
	}
	fetch := s.opts.store.fetch
	if fetch == nil {
		fetch = pluginhost.GuardedFetch
	}
	ss.acq = pluginhost.NewAcquirer(pluginhost.AcquirerConfig{
		Materialize: ss.attempt,
		Fetcher:     fetch,
		Logf:        func(format string, args ...any) { logsink.Info("machine.decision", format, args...) },
		Spawn:       ss.spawn,
		Ready:       ss.published,
		Ended:       ss.jobEnded,
	})
	ss.acq.Attach(ss.ctx)
	ss.resume(st)
	ss.wg.Add(1)
	go ss.collectLoop()
}

func (s *Server) closeStore() {
	ss := s.store
	if ss == nil {
		return
	}
	ss.once.Do(func() {
		ss.spawnMu.Lock()
		ss.stopping = true
		ss.spawnMu.Unlock()
		ss.cancel()
		ss.wg.Wait()
		ss.mu.Lock()
		defer ss.mu.Unlock()
		if ss.w != nil {
			ss.w.Close()
		}
		for _, d := range ss.drain {
			d.Close()
		}
	})
}

func (ss *storeService) spawn(f func()) bool {
	ss.spawnMu.Lock()
	defer ss.spawnMu.Unlock()
	if ss.stopping {
		return false
	}
	ss.wg.Add(1)
	go func() {
		defer ss.wg.Done()
		f()
	}()
	return true
}

func (s *Server) stateNow() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

func (ss *storeService) resume(st State) {
	if len(st.Pending) == 0 {
		return
	}
	referred := map[string]bool{}
	for _, r := range st.Refs {
		referred[r.Entry] = true
	}
	var keep []Pending
	for _, p := range st.Pending {
		if referred[p.Entry] {
			if _, err := p.Work.Check(); err == nil && p.Work.Entry == p.Entry {
				keep = append(keep, p)
				continue
			}
		}
		logsink.Info("machine.decision", "the plugin store's pending %s is no longer referred to; it is not resumed", p.Entry)
	}
	if len(keep) != len(st.Pending) {
		if err := ss.s.updateState(func(next *State) error { next.Pending = keep; return nil }); err != nil {
			logsink.Warn("machine.error", "the plugin store's pending entries were not cleared: %v", err)
		}
	}
	ss.mu.Lock()
	defer ss.mu.Unlock()
	if ss.w == nil {
		return
	}
	for _, p := range keep {
		ss.works[p.Entry] = p.Work
		if p.Adopt != nil {
			ss.adopt[p.Entry] = *p.Adopt
		}
		logsink.Info("machine.decision", "the plugin store resumes %s, pending since %s", p.Entry, p.Since)
		ss.acq.Want(pluginhost.WorkMaterial(ss.w.Dir, p.Work))
	}
}

func (ss *storeService) attempt(ctx context.Context, m pluginhost.Material, fetch pluginhost.ModelFetcher) error {
	works := m.Works()
	if len(works) != 1 {
		return &pluginhost.WorkError{Entry: m.PluginID, Detail: "a store job runs one entry"}
	}
	work := works[0]
	ss.mu.Lock()
	w := ss.w
	adopt := ss.adoptionLocked(work.Entry)
	repair := ss.repair[work.Entry]

	var others []pluginhost.StoreWork
	for e, o := range ss.works {
		if _, ended := ss.ended[e]; e != work.Entry && !ended {
			others = append(others, o)
		}
	}
	ss.mu.Unlock()
	if w == nil {
		return &pluginhost.MaterialWait{Cause: "the plugin store cannot be written", After: time.Minute}
	}
	if err := pluginhost.StoreRoom(w.Dir, work, others, ss.s.opts.store.freeDisk); err != nil {
		return err
	}
	logf := func(format string, args ...any) { logsink.Info("machine.decision", format, args...) }
	if repair {
		err := pluginhost.RepairEntry(ctx, w, work, fetch, adopt, logf)
		if errors.Is(err, pluginhost.ErrRepairWaits) || errors.Is(err, plugstore.ErrBusy) {
			return &pluginhost.MaterialWait{Cause: "an activation runs from the damaged " + work.Entry + "; it is repaired once none does", After: repairWait}
		}
		if err != nil {
			return err
		}
		ss.mu.Lock()
		delete(ss.repair, work.Entry)
		ss.mu.Unlock()
	}
	return pluginhost.EnsureEntry(ctx, w, work, fetch, adopt, logf)
}

func (ss *storeService) adoptionLocked(entry string) pluginhost.Adoption {
	a := ss.adopt[entry]
	a.Stores = nil
	for _, d := range ss.drain {
		a.Stores = append(a.Stores, d.Dir)
	}
	return a
}

func (ss *storeService) published(entry string) {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	if ss.repair[entry] || ss.w == nil || !pluginhost.EntryPresent(ss.w, ss.works[entry]) {
		return
	}
	delete(ss.works, entry)
	delete(ss.adopt, entry)
	delete(ss.ended, entry)
	if err := ss.clearPending(entry); err != nil {
		logsink.Warn("machine.error", "the plugin store's %s is published and still recorded pending: %v", entry, err)
	}
}

func (ss *storeService) jobEnded(entry string, _ error) {
	ss.mu.Lock()
	defer ss.mu.Unlock()

	cause := ss.acq.EndCause(entry)
	if cause == nil {
		return
	}
	ss.ended[entry] = cause
	if err := ss.clearPending(entry); err != nil {
		logsink.Warn("machine.error", "the plugin store's %s ended and is still recorded pending: %v", entry, err)
	}
}

func (ss *storeService) clearPending(entry string) error {
	return ss.s.updateState(func(st *State) error {
		st.Pending = slices.DeleteFunc(st.Pending, func(p Pending) bool { return p.Entry == entry })
		return nil
	})
}

func (s *Server) storeLinked(slot string) {
	if ss := s.store; ss != nil {
		ss.mu.Lock()
		ss.linked[slot] = true
		ss.mu.Unlock()
		ss.poke()
	}
}

func (ss *storeService) poke() {
	select {
	case ss.kick <- struct{}{}:
	default:
	}
}

func (s *Server) storeStatusLocked(st *Status, l *link) {
	if s.store == nil {
		return
	}
	st.Store = s.store.place()
	for _, r := range s.state.Refs {
		if r.Slot == l.slot {
			st.Refs = append(st.Refs, LinkRef{Plugin: r.Plugin, Entry: r.Entry})
		}
	}
}

func (ss *storeService) place() string {
	if p := ss.where.Load(); p != nil {
		return *p
	}
	return ""
}

func (s *Server) dispatchStore(l *link, m message) (message, func(), bool) {
	ss := s.store
	if ss == nil {
		return message{}, nil, false
	}
	switch m.Method {
	case MsgNeed:
		var p NeedParams
		if err := decodeStrict(m.Params, &p); err != nil {
			return refusalReply(m.ID, &Refusal{Reason: ReasonBadRequest, Message: "a need does not read: " + err.Error()}), nil, true
		}
		res, ref := ss.need(l, p)
		if ref != nil {
			return refusalReply(m.ID, ref), nil, true
		}
		return message{ID: m.ID, Result: mustJSON(res)}, nil, true
	case MsgRelease:
		var p ReleaseParams
		if err := decodeStrict(m.Params, &p); err != nil {
			return refusalReply(m.ID, &Refusal{Reason: ReasonBadRequest, Message: "a release does not read: " + err.Error()}), nil, true
		}
		if ref := ss.release(l, p); ref != nil {
			return refusalReply(m.ID, ref), nil, true
		}
		return message{ID: m.ID, Result: mustJSON(map[string]int{"released": len(p.Refs)})}, nil, true
	case MsgStorePlace:
		var p PlaceParams
		if err := decodeStrict(m.Params, &p); err != nil {
			return refusalReply(m.ID, &Refusal{Reason: ReasonBadRequest, Message: "store.place does not read: " + err.Error()}), nil, true
		}
		res, ref := ss.placeAt(l, p.Dir)
		if ref != nil {
			return refusalReply(m.ID, ref), nil, true
		}
		return message{ID: m.ID, Result: mustJSON(res)}, nil, true
	}
	return message{}, nil, false
}

var needOrder = []string{NeedPresent, NeedPending, NeedWaiting, NeedEnded, NeedRefused}

func (ss *storeService) need(l *link, p NeedParams) (NeedResult, *Refusal) {
	if ref := checkNeed(p); ref != nil {
		return NeedResult{}, ref
	}
	entries := map[string]bool{}
	for _, w := range p.Works {
		if _, err := w.Check(); err != nil {
			return NeedResult{Store: ss.place(), State: NeedRefused, Cause: err.Error(),
				Remedy: "the package declares material the store cannot hold as declared; install a corrected release"}, nil
		}
		entries[w.Entry] = true
	}
	adopt := ownCopies(l.slot, p.Adopt)
	now := ss.s.opts.now().UTC().Format(time.RFC3339)

	ss.mu.Lock()
	defer ss.mu.Unlock()
	if ss.w == nil {
		return NeedResult{}, &Refusal{Reason: ReasonNoStore, Message: fmt.Sprintf("the plugin store %s cannot be written: %v", ss.dir, ss.err),
			Remedy: "name a place on a volume that keeps hard links: aii machine store <dir>"}
	}
	type plan struct {
		entry   string
		state   string
		cause   error
		want    *pluginhost.StoreWork
		adopt   pluginhost.Adoption
		damaged bool
		retry   bool
	}
	var plans []plan
	var pending []Pending
	for _, w := range p.Works {
		pl := plan{entry: w.Entry, state: NeedPresent}
		damaged := slices.Contains(p.Damaged, w.Entry)
		switch {
		case w.Root == nil && w.View == nil:

		case !damaged && !ss.repair[w.Entry] && pluginhost.EntryPresent(ss.w, w):

		default:
			running, has := ss.works[w.Entry]
			merged := mergeWork(running, w)
			_, parked := ss.ended[w.Entry]

			st, ok := ss.acq.Status(w.Entry)
			ready := ok && st.Phase == pluginhost.PhaseReady
			if has && !ready && !p.Retry && !damaged && sameWork(running, merged) {

				pl.state, pl.cause = ss.jobState(w.Entry, merged)
				break
			}
			a := unionAdoption(ss.adopt[w.Entry], adopt)
			pending = append(pending, Pending{Entry: w.Entry, Since: now, Work: merged, Adopt: &a})
			pl.state, pl.want, pl.adopt, pl.damaged, pl.retry = NeedPending, &merged, a, damaged, parked || p.Retry
		}
		plans = append(plans, pl)
	}

	err := ss.s.updateState(func(st *State) error {
		if _, ok := st.record(l.slot); !ok {
			return errForgotten
		}
		for e := range entries {
			r := Ref{Slot: l.slot, Plugin: p.Plugin, Entry: e}
			if !slices.Contains(st.Refs, r) {
				st.Refs = append(st.Refs, r)
			}
		}
		for _, pe := range pending {
			st.Pending = slices.DeleteFunc(st.Pending, func(o Pending) bool { return o.Entry == pe.Entry })
			st.Pending = append(st.Pending, pe)
		}
		return nil
	})
	if errors.Is(err, errForgotten) {
		return NeedResult{}, &Refusal{Reason: ReasonUnregistered, Message: err.Error(), Remedy: "register it: aii register " + filepath.Base(l.slot)}
	}
	if err != nil {
		return NeedResult{}, &Refusal{Reason: ReasonNotRecorded, Message: "the need could not be recorded: " + err.Error()}
	}
	res := NeedResult{Store: ss.w.Dir, State: NeedPresent}
	for _, pl := range plans {
		if pl.want != nil {

			delete(ss.ended, pl.entry)
			ss.works[pl.entry], ss.adopt[pl.entry] = *pl.want, pl.adopt
			if pl.damaged {
				ss.repair[pl.entry] = true
			}
			ss.acq.Want(pluginhost.WorkMaterial(ss.w.Dir, *pl.want))
			if pl.retry {
				ss.acq.Retry(pl.entry)
			}
		}
		if slices.Index(needOrder, pl.state) > slices.Index(needOrder, res.State) {
			res.State = pl.state
			res.Cause, res.Remedy, res.AfterMS = "", "", 0
			if pl.cause != nil {
				res.Cause = pl.cause.Error()
				var final interface{ Final() string }
				if errors.As(pl.cause, &final) {
					res.Remedy = final.Final()
				}
			}
			if st, ok := ss.acq.Status(pl.entry); ok && pl.state == NeedWaiting && !st.RetryAt.IsZero() {
				res.AfterMS = max(time.Until(st.RetryAt), time.Second).Milliseconds()
			}
		}
	}
	if res.State == NeedPending {
		res.AfterMS = needPoll.Milliseconds()
	}
	if res.State == NeedEnded && res.Remedy == "" {
		res.Remedy = "then Try again"
	}
	return res, nil
}

func (ss *storeService) jobState(entry string, work pluginhost.StoreWork) (string, error) {
	if cause, ok := ss.ended[entry]; ok {
		return endedState(cause), cause
	}
	st, ok := ss.acq.Status(entry)
	switch {
	case !ok && pluginhost.EntryPresent(ss.w, work):
		return NeedPresent, nil
	case !ok, st.Phase == pluginhost.PhaseFetching, st.Remedy != "", ss.acq.EndCause(entry) != nil:

		return NeedPending, nil
	case st.LastError != "":
		return NeedWaiting, errors.New(st.LastError)
	}
	return NeedPending, nil
}

func endedState(cause error) string {
	var bound interface{ WaitsOnPackage() bool }
	if errors.As(cause, &bound) && bound.WaitsOnPackage() {
		return NeedRefused
	}
	return NeedEnded
}

func checkNeed(p NeedParams) *Refusal {
	bad := func(format string, args ...any) *Refusal {
		return &Refusal{Reason: ReasonBadRequest, Message: "a need " + fmt.Sprintf(format, args...)}
	}
	switch {
	case p.Plugin == "" || len(p.Plugin) > 256 || strings.ContainsFunc(p.Plugin, func(r rune) bool { return r < 0x20 || r == 0x7f }):
		return bad("names no plugin it can be recorded for")
	case len(p.Works) == 0 || len(p.Works) > maxNeedWorks:
		return bad("names %d entries; one need names 1 to %d", len(p.Works), maxNeedWorks)
	case len(p.Adopt.Stores) > 0:
		return bad("names stores to adopt from; which stores the writer adopts from is the service's own")
	}
	seen := map[string]bool{}
	for _, w := range p.Works {
		if seen[w.Entry] {
			return bad("names %s twice", w.Entry)
		}
		seen[w.Entry] = true
	}
	return nil
}

func ownCopies(slot string, a pluginhost.Adoption) pluginhost.Adoption {
	keep := func(dirs []string) []string {
		var out []string
		for _, d := range dirs {
			if filepath.IsAbs(d) && within(filepath.Clean(d), slot) {
				out = append(out, filepath.Clean(d))
			} else {
				logsink.Warn("machine.refusal", "a need named %s to adopt from, which is not under its slot %s; passed over", d, slot)
			}
		}
		return out
	}
	return pluginhost.Adoption{ModelDirs: keep(a.ModelDirs), ArchiveDirs: keep(a.ArchiveDirs)}
}

func within(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func unionAdoption(a, b pluginhost.Adoption) pluginhost.Adoption {
	add := func(into, from []string) []string {
		for _, d := range from {
			if !slices.Contains(into, d) {
				into = append(into, d)
			}
		}
		return into
	}
	return pluginhost.Adoption{ModelDirs: add(append([]string(nil), a.ModelDirs...), b.ModelDirs), ArchiveDirs: add(append([]string(nil), a.ArchiveDirs...), b.ArchiveDirs)}
}

func mergeWork(running, w pluginhost.StoreWork) pluginhost.StoreWork {
	if running.View == nil || w.View == nil {
		return w
	}
	v := *running.View
	v.Models = append([]pluginhost.ModelDecl(nil), running.View.Models...)
	for _, d := range w.View.Models {
		if !slices.ContainsFunc(v.Models, func(o pluginhost.ModelDecl) bool { return o.Name == d.Name }) {
			v.Models = append(v.Models, d)
		}
	}
	return pluginhost.StoreWork{Entry: w.Entry, View: &v}
}

func sameWork(a, b pluginhost.StoreWork) bool {
	return string(mustJSON(a)) == string(mustJSON(b))
}

func (ss *storeService) release(l *link, p ReleaseParams) *Refusal {
	if len(p.Refs) > 4*maxNeedWorks {
		return &Refusal{Reason: ReasonBadRequest, Message: fmt.Sprintf("a release names %d references; one names at most %d", len(p.Refs), 4*maxNeedWorks)}
	}
	ss.mu.Lock()
	err := ss.letGoLocked(func(st *State) {
		st.Refs = slices.DeleteFunc(st.Refs, func(r Ref) bool {
			return r.Slot == l.slot && slices.Contains(p.Refs, LinkRef{Plugin: r.Plugin, Entry: r.Entry})
		})
	})
	ss.mu.Unlock()
	if err != nil {
		return &Refusal{Reason: ReasonNotRecorded, Message: "the release could not be recorded: " + err.Error()}
	}
	ss.poke()
	return nil
}

func (s *Server) letGo(f func(*State)) error {
	ss := s.store
	if ss == nil {
		return s.updateState(func(st *State) error { f(st); return nil })
	}
	ss.mu.Lock()
	defer ss.mu.Unlock()
	return ss.letGoLocked(f)
}

func (ss *storeService) letGoLocked(f func(*State)) error {
	if err := ss.s.updateState(func(st *State) error {
		f(st)
		referred := referredEntries(*st)
		st.Pending = slices.DeleteFunc(st.Pending, func(p Pending) bool { return !referred[p.Entry] })
		return nil
	}); err != nil {
		return err
	}
	referred := referredEntries(ss.s.stateNow())
	for e := range ss.works {
		if referred[e] {
			continue
		}
		ss.acq.Forget(e)
		delete(ss.works, e)
		delete(ss.adopt, e)
		delete(ss.repair, e)
		delete(ss.ended, e)
		logsink.Info("machine.decision", "the plugin store's %s is referred to by no slot now; its acquisition stops", e)
	}
	return nil
}

func referredEntries(st State) map[string]bool {
	out := map[string]bool{}
	for _, r := range st.Refs {
		out[r.Entry] = true
	}
	return out
}

func (ss *storeService) placeAt(l *link, dir string) (PlaceResult, *Refusal) {
	refuse := func(format string, args ...any) (PlaceResult, *Refusal) {
		return PlaceResult{}, &Refusal{Reason: ReasonStorePlace, Message: fmt.Sprintf("the plugin store cannot be placed at %s: ", dir) + fmt.Sprintf(format, args...)}
	}
	if !filepath.IsAbs(dir) {
		return refuse("it is not an absolute path")
	}
	dir = filepath.Clean(dir)
	ss.mu.Lock()
	defer ss.mu.Unlock()
	if dir == ss.dir && ss.w != nil {
		return PlaceResult{Store: dir, Draining: ss.drainDirsLocked()}, nil
	}
	if within(dir, ss.dir) || within(ss.dir, dir) {
		return refuse("it lies inside the store, or the store inside it")
	}

	if _, err := filepath.EvalSymlinks(nearestExisting(dir)); err != nil {
		return refuse("its path does not resolve (%v)", err)
	}
	nw, err := plugstore.OpenWriter(dir)
	if err != nil {
		return refuse("%v", err)
	}
	drain := ss.drain
	if ss.w != nil {
		drain = append(append([]*plugstore.Writer(nil), drain...), ss.w)
	}
	var dirs []string
	for _, d := range drain {
		dirs = append(dirs, d.Dir)
	}
	if err := ss.s.updateState(func(st *State) error {
		st.Store, st.Draining = dir, dirs
		return nil
	}); err != nil {
		nw.Close()
		return PlaceResult{}, &Refusal{Reason: ReasonNotRecorded, Message: "the store's new place could not be recorded: " + err.Error()}
	}
	old := ss.dir
	ss.w, ss.err, ss.dir, ss.drain = nw, nil, dir, drain
	ss.where.Store(&dir)

	for e, work := range ss.works {
		if _, ended := ss.ended[e]; !ended {
			ss.acq.Want(pluginhost.WorkMaterial(dir, work))
		}
	}
	logsink.Info("machine.decision", "identity %s's operator placed the plugin store at %s; %s is adopted from by digest and drained as nothing runs from it", short(l.identity), dir, old)
	ss.poke()
	return PlaceResult{Store: dir, Draining: dirs}, nil
}

func (ss *storeService) drainDirsLocked() []string {
	var out []string
	for _, d := range ss.drain {
		out = append(out, d.Dir)
	}
	return out
}

func nearestExisting(dir string) string {
	for {
		if _, err := os.Lstat(dir); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return dir
		}
		dir = parent
	}
}
