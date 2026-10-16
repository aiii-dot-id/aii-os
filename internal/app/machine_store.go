package app

import (
	"context"
	"errors"
	"maps"
	"os"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/machine"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/plugstore"
)

func (a *App) pluginStoreDir(cfg Config) string {
	if !machine.Supported {
		return ""
	}
	if c := a.machine.client.Load(); c != nil {
		if s := c.Snapshot(); s.State == machine.StateLinked && s.Store != "" {
			return s.Store
		}
	}
	slot, err := os.Getwd()
	if err != nil {
		logsink.Warn("plugins.error", "no plugin store: the slot directory does not resolve: %v", err)
		return ""
	}
	dir, err := machineDirOf(cfg, slot)
	if err == nil {
		var store string
		if store, err = machine.StoreDir(dir); err == nil {
			return store
		}
	}
	logsink.Warn("plugins.error", "no plugin store: %v", err)
	return ""
}

func (a *App) publishPluginStore(dir string) bool {
	a.pluginMu.Lock()
	defer a.pluginMu.Unlock()
	if a.pluginOpts == nil || dir == "" || a.pluginOpts.StoreDir == dir {
		return false
	}
	logsink.Info("plugins.decision", "the account's plugin store is %s; activations read their material there from now on", dir)
	a.pluginOpts.StoreDir = dir
	return true
}

func (a *App) materializer() func(context.Context, pluginhost.Material, pluginhost.ModelFetcher) error {
	if a.storeMaterialize != nil {
		return a.storeMaterialize
	}
	return a.needOverLink
}

func (a *App) needOverLink(ctx context.Context, m pluginhost.Material, _ pluginhost.ModelFetcher) error {
	if _, safe := a.SafeMode(); safe {

		return &pluginhost.MaterialWait{Cause: "SAFE holds: this identity asks the plugin store for nothing until it lifts", After: time.Minute}
	}
	a.noteMaterial(m)
	c := a.machine.client.Load()
	if c == nil {
		return &pluginhost.MaterialWait{Cause: "this identity has no link to the machine service, the plugin store's writer", After: time.Minute}
	}
	res, err := c.Need(ctx, needOf(m))
	if err != nil {
		return a.linkWait(ctx, c, err)
	}
	if res.Store != "" && res.Store != m.Store() {

		if a.publishPluginStore(res.Store) {
			a.pokePluginSweep()
		}
		return &pluginhost.MaterialWait{Cause: "the plugin store moved to " + res.Store + "; this plugin is prepared there", After: 5 * time.Second}
	}
	return materialAnswer(res)
}

func needOf(m pluginhost.Material) machine.NeedParams {
	return machine.NeedParams{Plugin: m.PluginID, Works: m.Works(), Adopt: m.Adoption(), Damaged: m.Damaged(), Retry: m.Retry()}
}

func materialAnswer(res machine.NeedResult) error {
	after := time.Duration(res.AfterMS) * time.Millisecond
	switch res.State {
	case machine.NeedPresent:
		return nil
	case machine.NeedPending:
		return &pluginhost.MaterialWait{Cause: "the machine service is bringing it into the plugin store", Fetching: true, After: max(after, time.Second)}
	case machine.NeedWaiting:
		return &pluginhost.MaterialWait{Cause: res.Cause, Remedy: res.Remedy, After: max(after, time.Second)}
	case machine.NeedEnded:
		return &pluginhost.MaterialEnded{Cause: res.Cause, Remedy: res.Remedy}
	case machine.NeedRefused:
		return &pluginhost.MaterialRefused{Cause: res.Cause, Remedy: res.Remedy}
	}
	return &pluginhost.MaterialWait{Cause: "the machine service answered a need with " + res.State + ", which this identity does not read", After: time.Minute}
}

func (a *App) linkWait(ctx context.Context, c *machine.Client, err error) error {
	if cerr := ctx.Err(); cerr != nil {
		return cerr
	}
	var missing *machine.MissingError
	if errors.As(err, &missing) {
		return &pluginhost.MaterialEnded{Cause: missing.Error(), Remedy: "update the account's machine service to a program that speaks it (aii machine register from the installed program), then Try again"}
	}
	var ref *machine.Refusal
	if errors.As(err, &ref) {
		return &pluginhost.MaterialWait{Cause: ref.Message, Remedy: ref.Remedy, After: 30 * time.Second}
	}
	if errors.Is(err, machine.ErrNotLinked) {
		s := c.Snapshot()
		switch s.State {
		case machine.StateRefused:
			cause, remedy := "the machine service refused this identity", ""
			if s.Refusal != nil {
				cause, remedy = cause+": "+s.Refusal.Message, s.Refusal.Remedy
			}
			return &pluginhost.MaterialWait{Cause: cause, Remedy: remedy, After: 30 * time.Second}
		case machine.StateDown:
			return &pluginhost.MaterialWait{Cause: "the machine service is not running", Remedy: a.machineRemedy(), After: 5 * time.Second}
		}
		return &pluginhost.MaterialWait{Cause: "this identity is linking to the machine service", After: 2 * time.Second}
	}
	return &pluginhost.MaterialWait{Cause: "the machine service did not answer the need: " + err.Error(), After: 5 * time.Second}
}

func (a *App) noteMaterial(m pluginhost.Material) {
	ms := &a.machine
	ms.mu.Lock()
	defer ms.mu.Unlock()
	if ms.needs == nil {
		ms.needs = map[string]map[string]pluginhost.Material{}
	}
	byEntry := ms.needs[m.PluginID]
	if byEntry == nil {
		byEntry = map[string]pluginhost.Material{}
		ms.needs[m.PluginID] = byEntry
	}
	for _, e := range m.Entries() {
		byEntry[e] = m
	}
}

func (a *App) noteActivated(id string, m pluginhost.Material) {
	if len(m.Entries()) == 0 {
		return
	}
	ms := &a.machine
	ms.mu.Lock()
	if ms.serving == nil {
		ms.serving = map[string][]string{}
	}
	if was := ms.serving[id]; len(was) > 0 {
		for e, n := range ms.needs[id] {
			if slices.Equal(n.Entries(), was) && !slices.Contains(m.Entries(), e) {
				delete(ms.needs[id], e)
			}
		}
	}
	ms.serving[id] = m.Entries()
	ms.mu.Unlock()
	a.noteMaterial(m)
}

func (a *App) reconcileStoreRefs(ctx context.Context, installed map[string]bool, safe bool, rootsKept int) {
	c := a.machine.client.Load()
	if c == nil {
		return
	}
	s := c.Snapshot()
	if s.State != machine.StateLinked || c.Covers(machine.NeederPlugin("references"), machine.StoreMessages) != nil {
		return
	}
	ms := &a.machine
	ms.mu.Lock()
	needs := map[string]map[string]pluginhost.Material{}
	for id, byEntry := range ms.needs {
		if installed[id] {
			needs[id] = maps.Clone(byEntry)
		} else {
			delete(ms.needs, id)
			delete(ms.serving, id)
		}
	}
	serving := maps.Clone(ms.serving)
	ms.mu.Unlock()

	recorded := map[string]map[string]bool{}
	for _, r := range s.Refs {
		if recorded[r.Plugin] == nil {
			recorded[r.Plugin] = map[string]bool{}
		}
		recorded[r.Plugin][r.Entry] = true
	}
	if !safe {
		for id, byEntry := range needs {
			asked := map[string]bool{}
			for e, m := range byEntry {
				key := strings.Join(m.Entries(), " ")
				if recorded[id][e] || asked[key] {
					continue
				}
				asked[key] = true
				if _, err := c.Need(ctx, needOf(m)); err != nil {
					logsink.Warn("plugins.error", "plugin %s: its reference to the plugin store's %s was not recorded: %v", id, e, err)
				}
			}
		}
	}
	var release []machine.LinkRef
	store := plugstore.Store{Dir: s.Store}
	for id, entries := range recorded {
		var drop []string
		switch {
		case !installed[id]:
			for e := range entries {
				drop = append(drop, e)
			}
		case safe:
			for e := range entries {
				if !entryPublished(store, e) {
					drop = append(drop, e)
				}
			}
		case serving[id] != nil:
			drop = superseded(store, entries, needs[id], serving[id], rootsKept)
		}
		sort.Strings(drop)
		for _, e := range drop {
			release = append(release, machine.LinkRef{Plugin: id, Entry: e})
		}
	}
	if len(release) == 0 {
		return
	}
	if err := c.Release(ctx, release); err != nil {
		logsink.Warn("plugins.error", "the plugin store's references this identity no longer needs were not released: %v", err)
		return
	}
	logsink.Info("plugins.decision", "released %d reference(s) in the plugin store that no installed plugin here names", len(release))
}

func superseded(store plugstore.Store, recorded map[string]bool, named map[string]pluginhost.Material, serving []string, rootsKept int) []string {
	type root struct {
		entry, archive string
		at             time.Time
	}
	var old []root
	var drop []string
	for e := range recorded {
		if _, ok := named[e]; ok || slices.Contains(serving, e) {
			continue
		}
		if k, ok := strings.CutPrefix(e, plugstore.DirRoots+"/"); ok {
			if rec, at, err := readRootRecord(store, k); err == nil {
				old = append(old, root{entry: e, archive: plugstore.DirArchives + "/" + rec.ArchiveSHA256, at: at})
				continue
			}
		}
		drop = append(drop, e)
	}
	sort.Slice(old, func(i, k int) bool { return old[i].at.After(old[k].at) })
	keep := map[string]bool{}
	for i, r := range old {
		if i < rootsKept-1 {
			keep[r.entry], keep[r.archive] = true, true
		} else {
			drop = append(drop, r.entry)
		}
	}
	return slices.DeleteFunc(drop, func(e string) bool { return keep[e] })
}

func readRootRecord(store plugstore.Store, key string) (plugstore.RootRecord, time.Time, error) {
	path, err := store.RootRecord(key)
	if err != nil {
		return plugstore.RootRecord{}, time.Time{}, err
	}
	fi, err := os.Stat(path)
	if err != nil {
		return plugstore.RootRecord{}, time.Time{}, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return plugstore.RootRecord{}, time.Time{}, err
	}
	rec, err := plugstore.DecodeRootRecord(path, raw)
	return rec, fi.ModTime(), err
}

func entryPublished(store plugstore.Store, entry string) bool {
	e, err := plugstore.ParseEntry(entry)
	if err != nil {
		return false
	}
	path, err := store.Record(e)
	if err == nil && path == "" {
		path, err = store.Path(e)
	}
	if err != nil {
		return false
	}
	_, err = os.Lstat(path)
	return err == nil
}
