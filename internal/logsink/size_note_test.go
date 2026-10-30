package logsink

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTheLiveLogsSizeIsSaidAndAChangeNotTheHostsShows(t *testing.T) {
	s := newTestSink(t, Config{})
	live := filepath.Join(s.Dir(), LiveName)
	size := func() int64 {
		t.Helper()
		fi, err := os.Stat(live)
		if err != nil {
			t.Fatal(err)
		}
		return fi.Size()
	}
	body := func() string {
		t.Helper()
		b, err := os.ReadFile(live)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	const changed = "something other than the host changed it"

	Info("logs.pass", "a line the host wrote")
	n := size()
	s.file.noteSize()
	if want := fmt.Sprintf("the live log %s holds %d bytes\n", LiveName, n); !strings.Contains(body(), want) || strings.Contains(body(), changed) {
		t.Fatalf("the hour says the size the host wrote, %d bytes, and nothing else:\n%s", n, body())
	}

	written := size()
	if err := os.Truncate(live, 0); err != nil {
		t.Fatal(err)
	}
	s.file.noteSize()
	if want := fmt.Sprintf("the live log %s holds 0 bytes, and the host wrote %d to it: %s", LiveName, written, changed); !strings.Contains(body(), want) {
		t.Fatalf("an emptied log is said as emptied, %q:\n%s", want, body())
	}

	n = size()
	s.file.noteSize()
	if want := fmt.Sprintf("the live log %s holds %d bytes\n", LiveName, n); !strings.Contains(body(), want) || strings.Count(body(), changed) != 1 {
		t.Fatalf("the count takes the size on disk: the next hour is the size again, the change said once:\n%s", body())
	}
}
