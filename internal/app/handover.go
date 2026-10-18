package app

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

const handoverFromEnv = "AII_HANDOVER_FROM"

const defaultHandoverWait = 30 * time.Second

const maxHandoverWaitSeconds = 60 * 60

func (a *App) handoverWait() time.Duration {
	if n := a.configSnapshot().Updates.HandoverWaitSeconds; n > 0 {
		return time.Duration(min(n, maxHandoverWaitSeconds)) * time.Second
	}
	return defaultHandoverWait
}

const handoverLook = 25 * time.Millisecond

type handover struct {
	pid      int
	ended    bool
	later    bool
	waited   time.Duration
	bound    time.Duration
	seen     time.Time
	deadline time.Time
}

func (a *App) waitForHandover() handover {
	raw, ok := os.LookupEnv(handoverFromEnv)
	if !ok {
		return handover{}
	}
	os.Unsetenv(handoverFromEnv)
	pid, err := strconv.Atoi(raw)
	if err != nil || pid <= 0 || pid == os.Getpid() {
		logsink.Warn("boot.start", "%s names no other process (%q): this start waits for no one", handoverFromEnv, raw)
		return handover{}
	}
	h := handover{pid: pid, bound: a.handoverWait()}
	start := time.Now()
	h.deadline = start.Add(h.bound)
	h.ended, h.later = processEnded(pid, h.bound)
	h.seen = time.Now()
	h.waited = h.seen.Sub(start).Round(time.Millisecond)
	switch {
	case h.later:
		logsink.Info("boot.start", "this start took the identity over from process %d, and the process that number names now was created after this one: the one this start took over from had ended and its number was given again; the identity's claim is asked for now", pid)
	case h.ended:
		logsink.Info("boot.start", "this start took the identity over from process %d, which had ended %s later: the identity's claim is asked for now", pid, h.waited)
	default:
		logsink.Warn("boot.start", "this start took the identity over from process %d, which had not ended after %s (updates.handover_wait_seconds): the identity's claim is asked for all the same", pid, h.bound)
	}
	return h
}

func (h handover) claim(ask func() (func(), error)) (func(), error) {
	release, err := ask()
	if h.pid == 0 {
		return release, err
	}
	asked := 1
	for err != nil && errors.Is(err, errIdentityRunning) && time.Now().Before(h.deadline) {
		time.Sleep(min(handoverLook, time.Until(h.deadline)))
		release, err = ask()
		asked++
	}
	if err == nil && asked > 1 {
		logsink.Info("boot.start", "the identity's claim was still held when process %d was seen to end, and was let go %s later (asked %d times)", h.pid, time.Since(h.seen).Round(time.Millisecond), asked)
	}
	return release, err
}

func (h handover) refusedAfter(err error) error {
	switch {
	case h.pid == 0:
		return err
	case !h.ended:
		return fmt.Errorf("%w (the process this start took the identity over from, %d, had not ended after %s: updates.handover_wait_seconds)", err, h.pid, h.bound)
	case errors.Is(err, errIdentityRunning):
		return fmt.Errorf("%w (the process this start took the identity over from, %d, had ended, and the claim was still held at the end of the wait, %s: another process holds it; updates.handover_wait_seconds)", err, h.pid, h.bound)
	}
	return err
}
