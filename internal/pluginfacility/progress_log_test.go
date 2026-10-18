package pluginfacility

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

// A COUNT OF FILES PRESENT IS SAID WHEN IT MOVES, not each time a waiting
// prepare is asked again: an unchanged "preparing — 37 of 38 files
// present" was logged once a second for an hour.
func TestAnUnchangedCountOfFilesPresentIsLoggedOnce(t *testing.T) {
	var mu sync.Mutex
	var lines []string
	f := New(Config{Runtime: newSharedRuntime(), Capacity: ampleCapacity{}, Log: func(format string, args ...any) {
		mu.Lock()
		lines = append(lines, fmt.Sprintf(format, args...))
		mu.Unlock()
	}})
	gen := f.nextGen()
	f.record(Event{PluginID: "org.example.one", Gen: gen, Kind: EventStarted, Version: "1"})
	for i := 0; i < 5; i++ {
		f.record(Event{PluginID: "org.example.one", Gen: gen, Kind: EventProgress, Material: &MaterialStatus{FilesPresent: 37, FilesTotal: 38}})
	}
	f.record(Event{PluginID: "org.example.one", Gen: gen, Kind: EventProgress, Material: &MaterialStatus{FilesPresent: 38, FilesTotal: 38}})
	count := func(s string) int {
		mu.Lock()
		defer mu.Unlock()
		n := 0
		for _, l := range lines {
			if strings.Contains(l, s) {
				n++
			}
		}
		return n
	}
	if n := count("preparing — 37 of 38 files present"); n != 1 {
		t.Fatalf("an unchanged count was logged %d times; want once", n)
	}
	if n := count("preparing — 38 of 38 files present"); n != 1 {
		t.Fatalf("the count that moved was logged %d times; want once", n)
	}
}
