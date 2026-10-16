package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/install"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/machine"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
)

type machineLinkState struct {
	client atomic.Pointer[machine.Client]

	mu      sync.Mutex
	needs   map[string]map[string]pluginhost.Material
	serving map[string][]string
}

var (
	accountMachineDir = install.MachineDir
	accountRelaunch   = machine.AccountRelaunch
)

func machineDirOf(cfg Config, slot string) (string, error) {
	if cfg.Machine.Dir == "" {
		return accountMachineDir()
	}
	if filepath.IsAbs(cfg.Machine.Dir) {
		return filepath.Clean(cfg.Machine.Dir), nil
	}
	return filepath.Join(slot, cfg.Machine.Dir), nil
}

func (a *App) startMachineLink(ctx context.Context, cfg Config) {
	if !machine.Supported || a.keyPair == nil {
		return
	}
	slot, err := os.Getwd()
	if err != nil {
		logsink.Warn("machine.error", "no link to the machine service: the slot directory does not resolve: %v", err)
		return
	}
	dir, err := machineDirOf(cfg, slot)
	if err != nil {
		logsink.Warn("machine.error", "no link to the machine service: %v", err)
		return
	}
	opts := machine.ClientOptions{Dir: dir, Slot: slot, Key: a.keyPair, OnChange: a.machineChanged}
	if cfg.Machine.Dir == "" {

		opts.Relaunch = accountRelaunch()

		opts.ServerImage = func() string {
			p, _, _ := install.MachineRegistration()
			return p
		}
	}
	c := machine.NewClient(opts)
	a.machine.client.Store(c)
	a.runBackground(func() { c.Run(ctx) })
}

func (a *App) machineChanged() {
	if c := a.machine.client.Load(); c != nil {
		if s := c.Snapshot(); s.State == machine.StateLinked && a.publishPluginStore(s.Store) {
			a.pokePluginSweep()
		}
	}
	if a.dashboard != nil {
		a.dashboard.BroadcastConfig()
	}
}

func (a *App) machineView() *dashboard.MachineView {
	c := a.machine.client.Load()
	if c == nil {
		return nil
	}
	s := c.Snapshot()
	v := &dashboard.MachineView{State: s.State, Store: s.Store}
	if opts := a.pluginOptions(); opts != nil && opts.StoreDir != "" {
		v.Store = opts.StoreDir
	}
	switch s.State {
	case machine.StateLinked:
		v.Build, v.Linked = s.Service.String(), s.Linked
		if s.Update != nil {
			v.Update = s.Update.Build.String()
		}
		v.CanForce = c.Covers(machine.NeederForce, []string{machine.MsgUpdateForce}) == nil
		for _, m := range s.Missing {
			v.Missing = append(v.Missing, m.Error())
		}
	case machine.StateRefused:
		if s.Refusal != nil {
			v.Reason, v.Remedy = s.Refusal.Message, s.Refusal.Remedy
		}
	case machine.StateDown:
		v.Reason, v.Failing = s.Error, s.Failing
		v.Remedy = a.machineRemedy()
		if s.StartError != "" {
			v.Remedy = "this identity could not start it: " + s.StartError
		}
		if s.Failing {
			v.Remedy = "run aii machine at a prompt to see it fail; the identities start it again once the window passes, or at the next sign-in"
		}
	}
	if disabled, err := install.MachineRunDisabled(); err == nil && disabled {
		v.Warning = "the machine service's sign-in entry is switched off in Task Manager → Startup apps, so it does not start when you sign in; switch it on there"
	}
	return v
}

func (a *App) machineRemedy() string {
	cfg := a.configSnapshot()
	if cfg.Machine.Dir != "" {
		return "this slot links to a development service; start it with: aii machine -dir " + cfg.Machine.Dir
	}
	if machine.RelaunchesService {
		return "this identity starts it again within seconds; if it does not, register it from the installed program: aii machine register"
	}
	slot := "<slot>"
	if wd, err := os.Getwd(); err == nil {
		slot = filepath.Base(wd)
	}
	if _, registered, err := install.MachineRegistration(); err == nil && registered {
		return "its service manager restarts it; to start it now: " + install.MachineStartCommand()
	}
	return "it is not registered on this account: run aii register " + slot + " from the installed program, or aii machine register"
}

var errNoMachineLink = errors.New("this identity has no link to the machine service")

func (a *App) forceMachineUpdate() error {
	c := a.machine.client.Load()
	if c == nil {
		return errNoMachineLink
	}
	ctx, cancel := context.WithTimeout(a.lifetime(), 15*time.Second)
	defer cancel()
	if err := c.ForceUpdate(ctx); err != nil {
		return fmt.Errorf("the machine service's update was not forced: %w", err)
	}
	return nil
}
