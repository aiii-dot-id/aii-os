//go:build !windows

package tools

import (
	"errors"
	"fmt"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

type leftJobs struct {
	mu   sync.Mutex
	jobs []leftJob

	ended bool
}

type leftJob struct {
	pgid    int
	members map[int]uint64
}

var errNotAJob = errors.New("not a process group the shell made")

var errNoProcessTable = errors.New("the process table could not be read")

func readGroups() map[int]map[int]uint64 {
	for try := 0; try < 3; try++ {
		if now := groupsReader(); now != nil {
			return now
		}
		time.Sleep(10 * time.Millisecond)
	}
	return nil
}

var groupsReader = processGroups

const leftJobPoll = 50 * time.Millisecond

func (l *leftJobs) keep(cmd *exec.Cmd) bool {
	if cmd.Process == nil || cmd.ProcessState == nil {
		return false
	}
	pgid := cmd.Process.Pid
	left := syscall.Kill(-pgid, 0) != syscall.ESRCH
	l.mu.Lock()
	defer l.mu.Unlock()
	if !left && len(l.jobs) == 0 {
		return false
	}

	now := readGroups()
	if now == nil {

		return false
	}
	l.jobs = running(l.jobs, now)

	members := now[pgid]
	if len(members) == 0 {
		return false
	}
	if l.ended {
		_ = signalGroup(pgid, syscall.SIGKILL)
		return false
	}
	l.jobs = append(l.jobs, leftJob{pgid: pgid, members: members})
	return true
}

func (l *leftJobs) end(wait time.Duration) (int, error) {
	l.mu.Lock()
	l.ended = true
	jobs := l.jobs
	l.jobs = nil
	l.mu.Unlock()
	if len(jobs) == 0 {
		return 0, nil
	}

	now := readGroups()
	if now == nil {

		l.mu.Lock()
		l.jobs = append(jobs, l.jobs...)
		l.mu.Unlock()
		return 0, fmt.Errorf("%d left job(s) not ended: %w", len(jobs), errNoProcessTable)
	}
	jobs = running(jobs, now)
	found := len(jobs)
	for _, j := range jobs {
		_ = signalGroup(j.pgid, syscall.SIGTERM)
	}
	for deadline := time.Now().Add(wait); len(jobs) > 0 && time.Now().Before(deadline); {
		time.Sleep(leftJobPoll)
		if now := readGroups(); now != nil {
			jobs = running(jobs, now)
		}
	}
	var errs []error
	for _, j := range jobs {
		if err := signalGroup(j.pgid, syscall.SIGKILL); err != nil {
			errs = append(errs, fmt.Errorf("process group %d: %w", j.pgid, err))
		}
	}
	return found - len(errs), errors.Join(errs...)
}

func running(jobs []leftJob, now map[int]map[int]uint64) []leftJob {
	var still []leftJob
	for _, j := range jobs {
		if j.in(now[j.pgid]) {
			still = append(still, leftJob{pgid: j.pgid, members: now[j.pgid]})
		}
	}
	return still
}

func (j leftJob) in(members map[int]uint64) bool {
	for pid, started := range j.members {
		if s, ok := members[pid]; ok && s == started {
			return true
		}
	}
	return false
}

func signalGroup(pgid int, sig syscall.Signal) error {
	if pgid <= 1 || pgid == syscall.Getpgrp() {
		return fmt.Errorf("process group %d: %w", pgid, errNotAJob)
	}
	if err := syscall.Kill(-pgid, sig); err != nil && err != syscall.ESRCH {
		return err
	}
	return nil
}
