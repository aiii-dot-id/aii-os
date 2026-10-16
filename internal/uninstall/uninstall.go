//go:build (linux && !android) || (darwin && !ios) || windows

package uninstall

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/install"
	"github.com/aiii-dot-id/aii-os/internal/machine"
	"github.com/aiii-dot-id/aii-os/internal/updates"
)

type Env struct {
	OwnAccount   func() error
	Root         func() (string, error)
	MachineDir   func() (string, error)
	StartupSlots func() ([]install.StartupSlot, error)
	SlotDirs     func(root string) ([]string, error)
	Name         func(dir string) string

	SlotKey     func(dir string) (*crypto.KeyPair, error)
	SlotService func(dir string) (string, error)
	SlotRunning func(dir string) (bool, error)
	Unregister  func(slot string) error
	SlotGone    func(slot string) error
	Forget      func(ctx context.Context, dir, slot string, key *crypto.KeyPair, image string) error

	MachineRegistration func() (string, bool, error)
	UnregisterMachine   func() error
	MachineGone         func() error
	ServiceRuns         func(dir string) (bool, error)
	StorePlaces         func(dir string) ([]string, error)
	StoreSize           func(dir string) int64
	Remove              func(dir string) (machine.Removal, error)

	Programs  func() []install.InstalledProgram
	Processes func() ([]Process, error)
	Leftovers func(exe string) []string

	Sweep func() ([]string, error)

	Shortcuts func([]install.InstalledProgram) ([]string, []Stay)

	Settle time.Duration
	Self   int
}

type Process struct {
	UID int
	Exe string
}

func DefaultEnv(slotKey func(string) (*crypto.KeyPair, error), slotService func(string) (string, error)) Env {
	return Env{
		OwnAccount:          install.OwnAccount,
		Root:                install.Root,
		MachineDir:          install.MachineDir,
		StartupSlots:        install.StartupSlots,
		SlotDirs:            install.SlotDirs,
		Name:                install.Name,
		SlotKey:             slotKey,
		SlotService:         slotService,
		SlotRunning:         install.SlotRunning,
		Unregister:          install.Unregister,
		SlotGone:            install.SlotGone,
		Forget:              machine.Forget,
		MachineRegistration: install.MachineRegistration,
		UnregisterMachine:   install.UnregisterMachine,
		MachineGone:         install.MachineGone,
		ServiceRuns:         machine.ServiceRuns,
		StorePlaces:         machine.StorePlaces,
		StoreSize:           machine.StoreSize,
		Remove:              machine.Remove,
		Programs:            install.InstalledPrograms,
		Processes:           processes,
		Leftovers:           updates.Leftovers,
		Sweep:               sweep,
		Shortcuts:           shortcuts,
		Settle:              10 * time.Second,
		Self:                os.Getuid(),
	}
}

type slot struct {
	Identity
	dir string
}

type survey struct {
	root, machineDir string
	slots            []slot
	registered       bool
	image            string
	stores           []string
	storeBytes       int64
	programs         []install.InstalledProgram
	others           map[string][]int
}

func (e Env) survey() (survey, error) {
	var s survey
	var err error
	if s.root, err = e.Root(); err != nil {
		return s, err
	}
	if s.machineDir, err = e.MachineDir(); err != nil {
		return s, err
	}
	startup, err := e.StartupSlots()
	if err != nil {
		return s, fmt.Errorf("read this account's startup registrations: %w", err)
	}
	dirs, err := e.SlotDirs(s.root)
	if err != nil {
		return s, err
	}
	byName := map[string]*slot{}
	add := func(name, dir string, registered bool) {
		if sl, ok := byName[name]; ok {
			sl.Registered = sl.Registered || registered
			return
		}
		sl := &slot{dir: dir, Identity: Identity{Slot: name, Path: dir, RealPath: realPath(dir), Registered: registered}}
		byName[name] = sl
	}
	for _, st := range startup {
		add(st.Slot, st.Dir, true)
	}
	for _, d := range dirs {
		add(filepath.Base(d), d, false)
	}
	for _, sl := range byName {
		sl.Name = e.Name(sl.dir)
		if dev, err := e.SlotService(sl.dir); err == nil {
			sl.Service = dev
		}
		running, err := e.SlotRunning(sl.dir)
		if err != nil {
			return s, fmt.Errorf("whether %s runs cannot be read: %w", sl.Slot, err)
		}
		sl.Running = running
		s.slots = append(s.slots, *sl)
	}
	sort.Slice(s.slots, func(i, j int) bool { return slotLess(s.slots[i].Slot, s.slots[j].Slot) })
	if s.image, s.registered, err = e.MachineRegistration(); err != nil {
		return s, fmt.Errorf("read the machine service's registration: %w", err)
	}
	if places, err := e.StorePlaces(s.machineDir); err == nil {
		s.stores = places
		for _, p := range places {
			s.storeBytes += e.StoreSize(p)
		}
	} else {
		return s, fmt.Errorf("the machine service's state does not say where the stores lie: %w", err)
	}
	s.programs = e.Programs()
	s.others = map[string][]int{}
	if procs, err := e.Processes(); err == nil {
		for _, p := range s.programs {
			for _, pr := range procs {
				if pr.UID != e.Self && samePath(pr.Exe, p.Exe) && !slices.Contains(s.others[p.Exe], pr.UID) {
					s.others[p.Exe] = append(s.others[p.Exe], pr.UID)
				}
			}
		}
	}
	return s, nil
}

func Run(e Env, plan bool) (Report, error) {
	r := Report{Format: Format, Plan: plan, Done: []Did{}, Stays: []Stay{}, Identities: []Identity{}}
	if err := e.OwnAccount(); err != nil {
		return r, err
	}
	s, err := e.survey()
	if err != nil {
		return r, err
	}
	for _, sl := range s.slots {
		r.Identities = append(r.Identities, sl.Identity)
	}
	r.Stays = staysOf(s)
	r.Programs = programsOf(e, s)
	if plan {
		r.Steps = planOf(s)
		r.FreedBytes = s.storeBytes
		return r, nil
	}
	if !e.slots(s, &r) || !e.service(s, &r) || !e.store(s, &r) {
		return r, nil
	}
	e.app(s, &r)
	return r, nil
}

func (r Report) ExitCode() int {
	if r.Stopped != nil {
		return 3
	}
	return 0
}

func (e Env) slots(s survey, r *Report) bool {
	serviceUp, _ := e.ServiceRuns(s.machineDir)
	var stays []string
	n := 0
	for _, sl := range s.slots {
		if sl.Registered {
			if err := e.Unregister(sl.Slot); err != nil {
				stays = append(stays, sl.Slot+" did not unregister: "+err.Error())
				continue
			}
			n++
		}
		if serviceUp && sl.Service == "" {
			if key, err := e.SlotKey(sl.dir); err == nil {
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				if ferr := e.Forget(ctx, s.machineDir, sl.dir, key, s.image); ferr != nil {
					r.Notes = append(r.Notes, "the machine service did not let go of "+sl.Slot+" ("+ferr.Error()+"); its record goes with the service's state")
				}
				cancel()
			}
		}
		if sl.Registered {
			if err := e.SlotGone(sl.Slot); err != nil {
				stays = append(stays, sl.Slot+" is still registered or running: "+err.Error())
				continue
			}
		}
		running, err := e.SlotRunning(sl.dir)
		if err == nil && running && sl.Registered {

			running, err = e.stillRunning(sl.dir)
		}
		switch {
		case err != nil:
			stays = append(stays, "whether "+sl.Slot+" runs cannot be read: "+err.Error())
		case running && sl.Service != "":

		case running:
			stays = append(stays, sl.Slot+" runs outside its registration (started by hand); it is never killed")
		}
	}
	if len(stays) > 0 {
		r.Stopped = &Stopped{Step: StepSlots, Reason: strings.Join(stays, "; "),
			Remedy: "quit each identity named (or stop it where it was started), then run aii uninstall again; the slots already unregistered stay so"}
		return false
	}
	r.Done = append(r.Done, Did{Step: StepSlots, Did: fmt.Sprintf("unregistered and stopped %d of %d identities; every slot's data stays", n, len(s.slots))})
	return true
}

func (e Env) service(s survey, r *Report) bool {
	if s.registered {
		if err := e.UnregisterMachine(); err != nil {
			r.Stopped = &Stopped{Step: StepService, Reason: "the machine service did not unregister: " + err.Error(), Remedy: "run aii uninstall again"}
			return false
		}
	}
	if err := e.MachineGone(); err != nil {
		r.Stopped = &Stopped{Step: StepService, Reason: "the machine service is not gone: " + err.Error(), Remedy: "run aii uninstall again"}
		return false
	}
	for again := 0; ; again++ {
		if e.lockFree(s.machineDir) {
			break
		}
		if again == 2 {
			r.Stopped = &Stopped{Step: StepService, Reason: "a machine service still holds " + s.machineDir + " after it was stopped twice",
				Remedy: "quit any aii machine running for this account, then run aii uninstall again; its store is kept until then"}
			return false
		}
		if err := e.UnregisterMachine(); err != nil {
			r.Stopped = &Stopped{Step: StepService, Reason: "the machine service did not stop: " + err.Error(), Remedy: "run aii uninstall again"}
			return false
		}
	}
	did := "the machine service was not registered, and none runs"
	if s.registered {
		did = "unregistered and stopped the machine service"
	}
	r.Done = append(r.Done, Did{Step: StepService, Did: did})
	return true
}

func (e Env) stillRunning(dir string) (bool, error) {
	deadline := time.Now().Add(e.Settle)
	for {
		running, err := e.SlotRunning(dir)
		if err != nil || !running || time.Now().After(deadline) {
			return running, err
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (e Env) lockFree(dir string) bool {
	deadline := time.Now().Add(e.Settle)
	for {
		runs, err := e.ServiceRuns(dir)
		if err == nil && !runs {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (e Env) store(s survey, r *Report) bool {
	rem, err := e.Remove(s.machineDir)
	if err != nil {
		r.Stopped = &Stopped{Step: StepStore, Reason: err.Error(), Remedy: "run aii uninstall again"}
		return false
	}
	r.FreedBytes += rem.Freed
	var kept []string
	for _, st := range rem.Stores {
		if st.Gone {
			r.Removed = append(r.Removed, st.Dir)
		} else {
			kept = append(kept, st.Dir+": "+st.Kept)
			r.Stays = append(r.Stays, Stay{What: StayStore, Path: st.Dir, Why: st.Kept})
		}
		for _, l := range st.Left {
			r.Stays = append(r.Stays, Stay{What: StayOther, Path: filepath.Join(st.Dir, l), Why: "not made by AII OS"})
		}
	}
	for _, l := range rem.Left {
		r.Stays = append(r.Stays, Stay{What: StayOther, Path: filepath.Join(s.machineDir, l), Why: "not made by AII OS"})
	}
	if rem.Gone {
		r.Removed = append(r.Removed, s.machineDir)
	}
	if !rem.Done() {
		if !rem.Gone {
			r.Stays = append(r.Stays, Stay{What: StayService, Path: s.machineDir, Why: "kept while a store it names is kept: its state is the only record of where that store lies"})
		}
		r.Stopped = &Stopped{Step: StepStore, Reason: strings.Join(kept, "; ") + rem.Kept,
			Remedy: "do what each store's reason says (quit the identity that runs from it, mount its volume), then run aii uninstall again"}
		return false
	}
	if removed, err := e.Sweep(); err != nil {
		r.Notes = append(r.Notes, "what the platform keeps for the account was not all removed: "+err.Error())
	} else {
		r.Removed = append(r.Removed, removed...)
	}
	r.Done = append(r.Done, Did{Step: StepStore, Did: fmt.Sprintf("removed the account's plugin stores and the machine service's directory (%d bytes)", rem.Freed)})
	return true
}

func (e Env) app(s survey, r *Report) {
	for i, p := range r.Programs {
		if p.LeftKept {
			continue
		}
		for _, l := range p.Leftovers {
			if err := os.RemoveAll(l); err == nil {
				r.Removed = append(r.Removed, l)
			} else {
				r.Notes = append(r.Notes, "the updater's "+l+" was not removed: "+err.Error())
				r.Programs[i].LeftKept = true
			}
		}
	}
	removed, stays := e.Shortcuts(s.programs)
	r.Removed = append(r.Removed, removed...)
	r.Stays = append(r.Stays, stays...)
	r.Done = append(r.Done, Did{Step: StepApp, Did: fmt.Sprintf("%d installed program(s) are left for you to remove", len(r.Programs))})
}

func staysOf(s survey) []Stay {
	stays := []Stay{}
	for _, sl := range s.slots {
		stays = append(stays, Stay{What: StayIdentity, Path: sl.Path, RealPath: sl.RealPath, Name: sl.Name,
			Why: "an identity's slot is never touched: its record, its key and its data stay, and aii register runs it again"})
		if sl.Service != "" {
			stays = append(stays, Stay{What: StayDevelop, Path: sl.Service, Why: sl.Slot + "'s config names this development service: it is not the account's, and is not removed"})
		}
	}
	entries, _ := os.ReadDir(s.root)
	own := map[string]bool{install.MachineDirName: true, "plugins": true}
	for _, sl := range s.slots {
		own[filepath.Base(sl.dir)] = true
	}
	for _, e := range entries {
		n := e.Name()
		switch {
		case own[n]:
		case n == install.ByName:
			stays = append(stays, Stay{What: StayByName, Path: filepath.Join(s.root, n), Why: "the links that name each slot by its identity's name; rebuilt by aii slots"})
		case n == install.FirstStartLock:

			stays = append(stays, Stay{What: StayOther, Path: filepath.Join(s.root, n), Why: "the launcher's lock on an account's first start: an empty file, made again when AII OS is opened"})
		default:
			stays = append(stays, Stay{What: StayOther, Path: filepath.Join(s.root, n), Why: "not made by AII OS"})
		}
	}
	return append(stays, platformStays()...)
}

func programsOf(e Env, s survey) []Program {
	var out []Program
	for _, p := range s.programs {
		pr := Program{Path: programPath(p.Exe), Version: p.Version, Remove: removeInstruction(p.Exe), SharedWith: s.others[p.Exe]}
		for _, l := range e.Leftovers(p.Exe) {
			if _, err := os.Lstat(l); err == nil {
				pr.Leftovers = append(pr.Leftovers, l)
			}
		}
		if len(pr.Leftovers) > 0 && (len(pr.SharedWith) > 0 || !ownedHere(filepath.Dir(pr.Leftovers[0]))) {
			pr.LeftKept = true
		}
		out = append(out, pr)
	}
	return out
}

func planOf(s survey) []string {
	var out []string
	for _, sl := range s.slots {
		switch {
		case sl.Registered && sl.Running:
			out = append(out, "stop and unregister "+sl.Slot+" ("+nameOr(sl.Name)+"); its data stays")
		case sl.Registered:
			out = append(out, "unregister "+sl.Slot+" ("+nameOr(sl.Name)+"); its data stays")
		case sl.Running && sl.Service == "":
			out = append(out, "stop: "+sl.Slot+" runs outside its registration, and is never killed — quit it first")
		}
	}
	if s.registered {
		out = append(out, "stop and unregister the machine service")
	}
	for _, d := range s.stores {
		if exists(d) {
			out = append(out, "remove the plugin store "+d)
		}
	}
	if exists(s.machineDir) {
		out = append(out, "remove the machine service's directory "+s.machineDir)
	}
	for _, p := range s.programs {
		out = append(out, removeInstruction(p.Exe))
	}
	return out
}

func nameOr(name string) string {
	if name == "" {
		return "not yet born"
	}
	return name
}

func slotLess(a, b string) bool {
	na, ea := slotNumber(a)
	nb, eb := slotNumber(b)
	if ea == nil && eb == nil && na != nb {
		return na < nb
	}
	return a < b
}

func slotNumber(s string) (int, error) {
	var n int
	_, err := fmt.Sscanf(strings.TrimPrefix(s, "identity-"), "%d", &n)
	return n, err
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func realPath(path string) string {
	if r, err := filepath.EvalSymlinks(path); err == nil {
		return r
	}
	return path
}

func samePath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	fa, errA := os.Stat(a)
	fb, errB := os.Stat(b)
	if errA != nil || errB != nil {
		return filepath.Clean(a) == filepath.Clean(b)
	}
	return os.SameFile(fa, fb)
}
