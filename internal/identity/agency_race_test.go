package identity

import (
	"sync"
	"testing"
)

// .
// .
// .
func TestAgencyLimitsSurviveAConcurrentReload(t *testing.T) {
	e := &Engine{}
	e.SetAgencyLimits(3, 2, 5, 600)

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// .
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			e.SetAgencyLimits(3+i%2, 2+i%2, 5+i%2, 600+i%2)
		}
	}()

	// .
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 2000; i++ {
				_, _, _, _ = e.agencyLimits()
			}
		}()
	}

	close(stop)
	wg.Wait()
}

// .
// .
// .
func TestAgencyLimitsSnapshotIsInternallyConsistent(t *testing.T) {
	e := &Engine{}
	// .
	// .
	e.SetAgencyLimits(3, 2, 5, 3000)

	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for d := 3; ; d++ {
			select {
			case <-stop:
				return
			default:
			}
			e.SetAgencyLimits(d, 2, 5, d*1000)
		}
	}()

	bad := 0
	for i := 0; i < 20000; i++ {
		depth, _, _, wall := e.agencyLimits()
		if wall != depth*1000 {
			bad++
		}
	}
	close(stop)
	wg.Wait()

	if bad > 0 {
		t.Fatalf("torn agency snapshot %d times: wall did not match the depth it was published with", bad)
	}
}
