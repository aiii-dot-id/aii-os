package app

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
)

type engineWork struct {
	mu sync.Mutex

	running map[string]int

	idle chan struct{}
}

const engineJoinBound = pluginhost.StopBudget

func (w *engineWork) start(name string, work func()) {
	w.mu.Lock()
	if w.running == nil {
		w.running = map[string]int{}
	}
	w.running[name]++
	w.mu.Unlock()
	go func() {
		defer w.end(name)
		work()
	}()
}

func (w *engineWork) end(name string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.running[name]--; w.running[name] <= 0 {
		delete(w.running, name)
	}
	if len(w.running) == 0 && w.idle != nil {
		close(w.idle)
		w.idle = nil
	}
}

func (w *engineWork) join(bound time.Duration) []string {
	w.mu.Lock()
	if len(w.running) == 0 {
		w.mu.Unlock()
		return nil
	}
	if w.idle == nil {
		w.idle = make(chan struct{})
	}
	idle := w.idle
	w.mu.Unlock()
	timer := time.NewTimer(bound)
	defer timer.Stop()
	select {
	case <-idle:
		return nil
	case <-timer.C:
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	names := make([]string, 0, len(w.running))
	for name, n := range w.running {
		if n > 1 {
			name = fmt.Sprintf("%s (×%d)", name, n)
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (a *App) joinEngineWork() error {
	left := a.engines.join(engineJoinBound)
	if len(left) == 0 {
		return nil
	}
	logsink.Error("plugins.error", "%d engine-bound goroutine(s) still ran %s after the plugins closed; the store closes under them: %v", len(left), engineJoinBound, left)
	return fmt.Errorf("%d engine-bound goroutine(s) still running %s after the plugins closed: %s", len(left), engineJoinBound, strings.Join(left, "; "))
}
