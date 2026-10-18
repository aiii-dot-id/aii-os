package pluginworker

import (
	"context"
	"os"
	"os/exec"
	"sync"
	"testing"
	"time"
)

const (
	// firstRuntimeChildEnv marks the fresh process the test re-executes
	// itself as.
	firstRuntimeChildEnv = "AII_PLUGINWORKER_FIRST_RUNTIME_CHILD"

	firstRuntimeLoads     = 4
	firstRuntimeProcesses = 3
)

func TestConcurrentFirstLoadsRaceNothing(t *testing.T) {
	if os.Getenv(firstRuntimeChildEnv) != "" {
		concurrentFirstLoads(t)
		return
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	for range firstRuntimeProcesses {
		child := exec.CommandContext(ctx, exe, "-test.run=^TestConcurrentFirstLoadsRaceNothing$", "-test.timeout=50s")
		child.Env = append(os.Environ(), firstRuntimeChildEnv+"=1")
		if out, err := child.CombinedOutput(); err != nil {
			t.Fatalf("%d Loads at the start of a process: %v\n%s", firstRuntimeLoads, err, out)
		}
	}
}

// concurrentFirstLoads is the child's whole life: the Loads released
// together, nothing before them.
func concurrentFirstLoads(t *testing.T) {
	wasm := loadFixture(t, "echo.wasm")
	start := make(chan struct{})
	errs := make([]error, firstRuntimeLoads)
	var wg sync.WaitGroup
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			m, err := Load(t.Context(), wasm, Config{})
			if err != nil {
				errs[i] = err
				return
			}
			errs[i] = m.Close(t.Context())
		}()
	}
	close(start)
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("load %d: %v", i, err)
		}
	}
}
