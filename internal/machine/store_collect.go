//go:build (linux && !android) || (darwin && !ios) || windows

package machine

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/plugstore"
)

func (ss *storeService) collectLoop() {
	defer ss.wg.Done()
	if ss.s.opts.store.settleWait < 0 || !ss.awaitSettled() {
		return
	}
	every := time.NewTicker(collectEvery)
	defer every.Stop()
	for {
		if err := ss.collect(); err != nil {
			logsink.Warn("machine.error", "%v", err)
		}
		if ss.s.opts.store.collected != nil {
			ss.s.opts.store.collected()
		}
		select {
		case <-ss.ctx.Done():
			return
		case <-every.C:
		case <-ss.kick:
		}
	}
}

func (ss *storeService) awaitSettled() bool {
	wait := ss.s.opts.store.settleWait
	if wait <= 0 {
		wait = defaultSettle
	}
	settle := time.NewTimer(wait)
	defer settle.Stop()
	for !ss.settled() {
		select {
		case <-ss.ctx.Done():
			return false
		case <-settle.C:
			logsink.Info("machine.decision", "the plugin store's collector starts: %s since the service started", wait)
			return true
		case <-ss.kick:
		}
	}
	logsink.Info("machine.decision", "the plugin store's collector starts: every recorded identity has linked")
	return true
}

func (ss *storeService) settled() bool {
	st := ss.s.stateNow()
	ss.mu.Lock()
	defer ss.mu.Unlock()
	for _, r := range st.Identities {
		if !ss.linked[r.Slot] {
			return false
		}
	}
	return true
}

func (ss *storeService) collect() error {
	ss.mu.Lock()
	w := ss.w
	stores := append([]*plugstore.Writer(nil), ss.drain...)
	ss.mu.Unlock()
	if w == nil {
		return nil
	}
	now := ss.s.opts.now()
	if err := ss.releaseGoneSlots(now); err != nil {
		return err
	}
	stores = append([]*plugstore.Writer{w}, stores...)
	for _, sw := range stores {
		if err := readEveryRecord(sw); err != nil {
			return fmt.Errorf("the plugin store's collector took nothing this pass: %w", err)
		}
	}
	before := ss.s.stateNow().Unreferenced
	found := map[string]string{}
	var took []string
	for _, sw := range stores {
		took = append(took, ss.collectStore(sw, sw == w, now, before, found)...)
	}
	if !maps.Equal(found, before) {
		if err := ss.s.updateState(func(st *State) error {
			st.Unreferenced = found
			return nil
		}); err != nil {
			return fmt.Errorf("the plugin store's collector could not record what it found: %w", err)
		}
	}
	ss.retireDrained(stores[1:])
	if len(took) > 0 || len(found) > 0 {
		logsink.Info("machine.pass", "the plugin store's collector took %d entries (%s); %d wait out their hour", len(took), strings.Join(took, ", "), len(found))
	}
	return nil
}

func (ss *storeService) collectStore(w *plugstore.Writer, current bool, now time.Time, before, found map[string]string) []string {
	entries, err := storeEntries(w)
	if err != nil {
		logsink.Warn("machine.error", "the plugin store %s does not list: %v", w.Dir, err)
		return nil
	}
	var took []string
	emptied := false
	for _, e := range entries {
		if !emptied && (e.Kind == plugstore.DirArchives || e.Kind == plugstore.DirBlobs) {

			ss.emptyTrash(w)
			emptied = true
		}
		path := filepath.Join(w.Dir, e.String())
		ss.mu.Lock()
		if ss.liveLocked(w, current, e) {
			ss.mu.Unlock()
			continue
		}
		since, err := time.Parse(time.RFC3339, before[path])
		if err != nil || now.Sub(since) < storeGrace {
			ss.mu.Unlock()
			if err != nil {
				found[path] = now.UTC().Format(time.RFC3339)
			} else {
				found[path] = before[path]
			}
			continue
		}
		err = retire(w, e)
		ss.mu.Unlock()
		switch {
		case err == nil:
			took = append(took, path)
		case errors.Is(err, plugstore.ErrPinned):
			found[path] = before[path]
		case errors.Is(err, plugstore.ErrBusy):
			found[path] = before[path]
			logsink.Info("machine.decision", "the plugin store's %s is open (an engine holds it); the collector tries again in an hour", path)
		default:
			found[path] = before[path]
			logsink.Warn("machine.error", "the plugin store's %s was not taken: %v", path, err)
		}
	}
	ss.emptyTrash(w)
	ss.collectDebris(w, now)
	return took
}

func (ss *storeService) emptyTrash(w *plugstore.Writer) {
	if err := w.EmptyTrash(); err != nil {
		logsink.Warn("machine.error", "the plugin store %s's trash was not emptied: %v", w.Dir, err)
	}
}

func (ss *storeService) liveLocked(w *plugstore.Writer, current bool, e plugstore.Entry) bool {
	if !current && ss.w != nil && heldIn(ss.w, e) {
		return false
	}
	st := ss.s.stateNow()
	name := e.String()
	for _, r := range st.Refs {
		if r.Entry == name {
			return true
		}
	}
	for _, p := range st.Pending {
		if p.Entry == name || slices.Contains(pendingDigests(p), name) {
			return true
		}
	}
	if e.Kind == plugstore.DirBlobs {
		path, _ := w.Blob(e.ID)
		if n, err := plugstore.Links(path); err != nil || n > 1 {
			return true
		}
	}
	return false
}

func heldIn(w *plugstore.Writer, e plugstore.Entry) bool {
	path, err := w.Path(e)
	if err != nil {
		return false
	}
	if _, err := os.Lstat(path); err != nil {
		return false
	}
	if rec, _ := w.Record(e); rec != "" {
		if _, err := os.Lstat(rec); err != nil {
			return false
		}
	}
	return true
}

func pendingDigests(p Pending) []string {
	var out []string
	if v := p.Work.View; v != nil {
		for _, d := range v.Models {
			out = append(out, plugstore.DirBlobs+"/"+d.SHA256)
		}
	}
	if r := p.Work.Root; r != nil {
		out = append(out, plugstore.DirArchives+"/"+r.Runtime.SHA256)
	}
	return out
}

func retire(w *plugstore.Writer, e plugstore.Entry) error {
	h, err := w.HoldExclusive(e)
	if err != nil {
		return err
	}
	if e.Kind == plugstore.DirViews {
		view, _ := w.View(e.ID)
		if err := plugstore.RevokeView(view, e.ID); err != nil {
			h.Close()
			return plugstore.Busy(err)
		}
	}
	return h.Retire()
}

func storeEntries(w *plugstore.Writer) ([]plugstore.Entry, error) {
	var out []plugstore.Entry
	seen := map[string]bool{}
	for _, kind := range []string{plugstore.DirRoots, plugstore.DirViews, plugstore.DirArchives, plugstore.DirBlobs} {
		names, err := os.ReadDir(filepath.Join(w.Dir, kind))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, n := range names {
			id := strings.TrimSuffix(n.Name(), plugstore.RecordSuffix)
			if kind == plugstore.DirArchives || kind == plugstore.DirBlobs {
				id = n.Name()
			}
			e, err := plugstore.ParseEntry(kind + "/" + id)
			if err != nil || seen[e.String()] {
				continue
			}
			seen[e.String()] = true
			out = append(out, e)
		}
	}
	return out, nil
}

func readEveryRecord(w *plugstore.Writer) error {
	for _, kind := range []string{plugstore.DirRoots, plugstore.DirViews} {
		names, err := os.ReadDir(filepath.Join(w.Dir, kind))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		for _, n := range names {
			if !strings.HasSuffix(n.Name(), plugstore.RecordSuffix) {
				continue
			}
			path := filepath.Join(w.Dir, kind, n.Name())
			raw, err := os.ReadFile(path)
			if err == nil {
				if kind == plugstore.DirRoots {
					_, err = plugstore.DecodeRootRecord(path, raw)
				} else {
					_, err = plugstore.DecodeViewRecord(path, raw)
				}
			}
			if err != nil {
				return fmt.Errorf("the record %s does not read: %w", path, err)
			}
		}
	}
	return nil
}

func (ss *storeService) collectDebris(w *plugstore.Writer, now time.Time) {
	st := ss.s.stateNow()
	wanted := map[string]bool{}
	for _, p := range st.Pending {
		wanted[p.Entry] = true
		for _, d := range pendingDigests(p) {
			wanted[d] = true
		}
	}
	for _, kind := range []string{plugstore.DirArchives, plugstore.DirBlobs, plugstore.DirRoots, plugstore.DirViews} {
		names, err := os.ReadDir(filepath.Join(w.Dir, kind))
		if err != nil {
			continue
		}
		for _, n := range names {
			owner, debris := debrisOwner(n.Name())
			if !debris || wanted[kind+"/"+owner] {
				continue
			}
			path := filepath.Join(w.Dir, kind, n.Name())
			fi, err := os.Lstat(path)
			if err != nil || now.Sub(fi.ModTime()) < storeGrace {
				continue
			}
			if err := os.RemoveAll(path); err != nil {
				logsink.Warn("machine.error", "the plugin store's %s, left by an interrupted write, was not removed: %v", path, err)
			}
		}
	}
}

func debrisOwner(name string) (string, bool) {
	for _, mark := range []string{plugstore.PartialSuffix + "-", plugstore.PartialSuffix, ".repair-", ".link-"} {
		if i := strings.Index(name, mark); i > 0 {
			return name[:i], true
		}
	}
	if strings.HasPrefix(name, ".probe-") {
		return "", true
	}
	return "", false
}

func (ss *storeService) retireDrained(drained []*plugstore.Writer) {
	for _, d := range drained {
		entries, err := storeEntries(d)
		if err != nil || len(entries) > 0 {
			continue
		}
		ss.mu.Lock()
		err = ss.s.updateState(func(st *State) error {
			st.Draining = slices.DeleteFunc(st.Draining, func(o string) bool { return o == d.Dir })
			return nil
		})
		if err == nil {
			ss.drain = slices.DeleteFunc(ss.drain, func(o *plugstore.Writer) bool { return o == d })
		}
		ss.mu.Unlock()
		if err != nil {
			logsink.Warn("machine.error", "the drained plugin store %s is empty and still recorded as draining; the next pass tries again: %v", d.Dir, err)
			continue
		}
		d.Close()
		logsink.Info("machine.decision", "the plugin store %s, moved away from, holds nothing more; this service no longer writes it, and it may be removed", d.Dir)
	}
}

func (ss *storeService) releaseGoneSlots(now time.Time) error {
	st := ss.s.stateNow()
	slots := map[string]bool{}
	for _, r := range st.Refs {
		slots[r.Slot] = true
	}
	gone := map[string]string{}
	var release []string
	for slot := range slots {
		if !slotGone(slot) {
			continue
		}
		since, err := time.Parse(time.RFC3339, st.Gone[slot])
		switch {
		case err != nil:
			gone[slot] = now.UTC().Format(time.RFC3339)
		case now.Sub(since) >= storeGrace:
			release = append(release, slot)
		default:
			gone[slot] = st.Gone[slot]
		}
	}
	if len(release) == 0 && maps.Equal(gone, st.Gone) {
		return nil
	}
	ss.mu.Lock()
	defer ss.mu.Unlock()
	err := ss.letGoLocked(func(next *State) {
		next.Refs = slices.DeleteFunc(next.Refs, func(r Ref) bool { return slices.Contains(release, r.Slot) })
		next.Gone = gone
	})
	if err != nil {
		return fmt.Errorf("the plugin store's collector could not record a slot found gone: %w", err)
	}
	for _, slot := range release {
		logsink.Info("machine.decision", "the slot %s has been gone an hour (its parent is there); its references in the plugin store are released", slot)
	}
	return nil
}

func slotGone(slot string) bool {
	_, err := os.Lstat(slot)
	if !errors.Is(err, fs.ErrNotExist) {
		return false
	}
	fi, err := os.Stat(filepath.Dir(slot))
	return err == nil && fi.IsDir()
}
