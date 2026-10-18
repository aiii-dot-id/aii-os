package app

import (
	"fmt"
	"os"
	"strconv"
	"syscall"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

const startRetryEnv = "AII_START_RETRY"

const (
	defaultStartRetryInterval = 30 * time.Second
	defaultStartRetryLimit    = 6
)

const (
	maxStartRetrySeconds = 24 * 60 * 60
	maxStartRetryLimit   = 100
)

func validStartNumbers(cfg *Config) error {
	if n := cfg.Identity.StartRetrySeconds; n < 0 || n > maxStartRetrySeconds {
		return fmt.Errorf("identity.start_retry_seconds is %d: it must be 0 (take the default, %d) or at most %d", n, int(defaultStartRetryInterval/time.Second), maxStartRetrySeconds)
	}
	if p := cfg.Identity.StartRetryLimit; p != nil && (*p < 0 || *p > maxStartRetryLimit) {
		return fmt.Errorf("identity.start_retry_limit is %d: it must be from 0 (never tried again by itself) to %d, or left out for the default, %d", *p, maxStartRetryLimit, defaultStartRetryLimit)
	}
	if n := cfg.Updates.HandoverWaitSeconds; n < 0 || n > maxHandoverWaitSeconds {
		return fmt.Errorf("updates.handover_wait_seconds is %d: it must be 0 (take the default, %d) or at most %d", n, int(defaultHandoverWait/time.Second), maxHandoverWaitSeconds)
	}
	return nil
}

func (a *App) startRetryInterval() time.Duration {
	if n := a.configSnapshot().Identity.StartRetrySeconds; n > 0 {
		return time.Duration(min(n, maxStartRetrySeconds)) * time.Second
	}
	return defaultStartRetryInterval
}

func (a *App) startRetryLimit() int {
	if p := a.configSnapshot().Identity.StartRetryLimit; p != nil && *p >= 0 {
		return min(*p, maxStartRetryLimit)
	}
	return defaultStartRetryLimit
}

func takeStartAttempt() int {
	raw, ok := os.LookupEnv(startRetryEnv)
	if !ok {
		return 0
	}
	os.Unsetenv(startRetryEnv)
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

func passesWithTime(err error) bool {
	return anyErrno(err, func(errno syscall.Errno) bool {
		switch errno {
		case syscall.EIO, syscall.ESTALE, syscall.EMFILE, syscall.ENFILE, syscall.ENOMEM:
			return true
		}
		return passesWithTimeHere(errno)
	})
}

func anyErrno(err error, is func(syscall.Errno) bool) bool {
	if err == nil {
		return false
	}
	if errno, ok := err.(syscall.Errno); ok && is(errno) {
		return true
	}
	switch u := err.(type) {
	case interface{ Unwrap() []error }:
		for _, e := range u.Unwrap() {
			if anyErrno(e, is) {
				return true
			}
		}
	case interface{ Unwrap() error }:
		return anyErrno(u.Unwrap(), is)
	}
	return false
}

func (a *App) planStartRetry(rollback bool) (said string, arm func()) {
	next, limit := a.startAttempt+1, a.startRetryLimit()
	restores := ""
	if rollback {
		restores = ", and that restart restores the previous program"
	}
	if limit == 0 {
		return "; it is not tried again by itself (identity.start_retry_limit is 0): restart the identity once what this line names is put right" + restores, nil
	}
	if next > limit {
		return fmt.Sprintf("; it was tried again by itself %d time(s) (identity.start_retry_limit), and is not tried again: restart the identity once what this line names is put right%s", limit, restores), nil
	}
	delay, longest := a.startRetryInterval(), time.Duration(maxStartRetrySeconds)*time.Second
	for i := 1; i < next && delay < longest; i++ {
		delay *= 2
	}
	delay = min(delay, longest)
	at := time.Now().Add(delay).UTC().Format(time.RFC3339)
	said = fmt.Sprintf("; this start is tried again by itself at %s (retry %d of %d), as the cause may pass with time", at, next, limit)
	if rollback {
		said = fmt.Sprintf("; an update installed this program and it has not completed a boot, so this start is tried again by itself at %s (retry %d of %d), and that start restores the previous program", at, next, limit)
	}
	return said, func() {
		a.startRetryTimer = startRetryAfter(delay, func() {
			os.Setenv(startRetryEnv, strconv.Itoa(next))
			logsink.Info("boot.decision", "a start held at its page by a cause that may pass with time is tried again (retry %d of %d)", next, limit)
			a.Restart()
		})
	}
}

func (a *App) stopStartRetry() {
	if a.startRetryTimer != nil {
		a.startRetryTimer.Stop()
	}
}
