//go:build darwin && !ios

package supervisor

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const treeSurvivedWords = "%d process(es) of the plugin's process tree still running %s after SIGKILL (pids %v); retirement pending"

const treeUnlistedWords = "the process list could not be read, so the plugin's process tree is not known to have ended; retirement pending"

const treeKeeperWords = "start the keeper of the plugin's process group: %w"

const treePoll = 10 * time.Millisecond

var treeKill = syscall.Kill

func treeKeeperArgv() []string {
	return []string{"/bin/sh", "-c", "read _; kill -9 0"}
}

type procTree struct {
	keeper *exec.Cmd
	hold   *os.File
	pgid   int

	mu      sync.Mutex
	seen    map[int]uint64
	retired bool

	once sync.Once
	err  error
}

func startTree(cmd, keeper *exec.Cmd) (*procTree, error) {
	r, w, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf(treeKeeperWords, err)
	}
	keeper.Stdin = r
	keeper.Env = []string{}
	keeper.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := keeper.Start(); err != nil {
		_ = r.Close()
		_ = w.Close()
		return nil, fmt.Errorf(treeKeeperWords, err)
	}
	_ = r.Close()
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	cmd.SysProcAttr.Pgid = keeper.Process.Pid
	return &procTree{keeper: keeper, hold: w, pgid: keeper.Process.Pid, seen: map[int]uint64{}}, nil
}

type treeProc struct {
	ppid, pgid int
	started    uint64
	zombie     bool
}

func processList() map[int]treeProc {
	procs, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil
	}
	const szomb = 5
	list := make(map[int]treeProc, len(procs))
	for i := range procs {
		p := &procs[i]
		list[int(p.Proc.P_pid)] = treeProc{
			ppid:    int(p.Eproc.Ppid),
			pgid:    int(p.Eproc.Pgid),
			started: uint64(p.Proc.P_starttime.Sec)*1_000_000 + uint64(p.Proc.P_starttime.Usec),
			zombie:  p.Proc.P_stat == szomb,
		}
	}
	return list
}

func (t *procTree) note(root int) {
	if t == nil || root <= 0 {
		return
	}
	list := processList()
	if list == nil {
		return
	}
	parent := make(map[int]int, len(list))
	for pid, p := range list {
		parent[pid] = p.ppid
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.retired {

		return
	}
	for _, pid := range descendants(root, parent) {
		if p := list[pid]; pid != root && !p.zombie {
			t.seen[pid] = p.started
		}
	}
}

func (t *procTree) retire() error {
	if t == nil {
		return nil
	}
	t.once.Do(func() { t.err = t.retireOnce() })
	return t.err
}

func (t *procTree) retireOnce() error {
	t.mu.Lock()
	t.retired = true
	t.mu.Unlock()
	defer func() {

		_ = t.hold.Close()
		_ = t.keeper.Process.Kill()
		_ = t.keeper.Wait()
	}()
	deadline := time.Now().Add(closeGraceKill)
	for {
		_ = treeKill(-t.pgid, syscall.SIGKILL)
		list := processList()
		if list == nil {
			return errors.New(treeUnlistedWords)
		}
		running := t.running(list)
		if len(running) == 0 {
			return nil
		}
		for _, pid := range running {
			_ = treeKill(pid, syscall.SIGKILL)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf(treeSurvivedWords, len(running), closeGraceKill, running)
		}
		time.Sleep(treePoll)
	}
}

func (t *procTree) running(list map[int]treeProc) []int {
	set := map[int]bool{}
	for pid, p := range list {
		if p.pgid == t.pgid && pid != t.pgid && !p.zombie {
			set[pid] = true
		}
	}
	t.mu.Lock()
	for pid, started := range t.seen {
		if p, ok := list[pid]; ok && !p.zombie && p.started == started {
			set[pid] = true
		}
	}
	t.mu.Unlock()
	out := make([]int, 0, len(set))
	for pid := range set {
		out = append(out, pid)
	}
	sort.Ints(out)
	return out
}
