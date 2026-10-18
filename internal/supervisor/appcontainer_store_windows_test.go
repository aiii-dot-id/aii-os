//go:build windows

package supervisor_test

import (
	"bufio"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/plugstore"
	"github.com/aiii-dot-id/aii-os/internal/supervisor"
)

func TestTwoChildrenReachOneBlobThroughTwoViewsByCapability(t *testing.T) {
	probe := supervisor.BuildWallProbeForTest(t)
	w, err := plugstore.OpenWriter(filepath.Join(t.TempDir(), "plugins"))
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	blobOf := func(name, body string) string {
		p := filepath.Join(w.Dir, plugstore.DirBlobs, strings.Repeat(name, 64))
		if err := os.WriteFile(p, []byte(body), 0o400); err != nil {
			t.Fatal(err)
		}
		return p
	}
	viewOf := func(key string, links map[string]string) string {
		dir := filepath.Join(w.Dir, plugstore.DirViews, key)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		for name, blob := range links {
			if err := os.Link(blob, filepath.Join(dir, name)); err != nil {
				t.Fatal(err)
			}
		}
		if err := plugstore.GrantView(dir, key); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	shared := blobOf("a", "one model, two views")
	theirs := blobOf("b", "another plugin's model")
	k1, k2, k3 := "1111111111111111", "2222222222222222", "3333333333333333"
	v1 := viewOf(k1, map[string]string{"m.bin": shared})
	v2 := viewOf(k2, map[string]string{"m.bin": shared})
	v3 := viewOf(k3, map[string]string{"t.bin": theirs})
	capName := func(key string) string {
		return plugstore.CapabilityName(plugstore.Entry{Kind: plugstore.DirViews, ID: key})
	}
	capSID := func(key string) string {
		sid, err := plugstore.CapabilitySID(plugstore.Entry{Kind: plugstore.DirViews, ID: key})
		if err != nil {
			t.Fatal(err)
		}
		return sid.String()
	}
	caps, err := plugstore.Capabilities(shared)
	if err != nil {
		t.Fatal(err)
	}
	var on []string
	for _, c := range caps {
		on = append(on, c.String())
	}
	slices.Sort(on)
	want := []string{capSID(k1), capSID(k2)}
	slices.Sort(want)
	if !slices.Equal(on, want) {
		t.Fatalf("the shared blob's DACL lists %v, want exactly the two views' %v", on, want)
	}

	type child struct {
		answers map[string]string
		lines   chan string
		stdin   io.WriteCloser
		end     func() error
	}
	start := func(key string, env ...string) *child {
		cmd := exec.Command(probe, "store")
		cmd.Env = env
		l, err := supervisor.LaunchContainedForTest(cmd, &supervisor.AppContainer{Profile: "aiios.test.store." + key, Capabilities: []string{capName(key)}})
		if err != nil {
			t.Fatalf("launch under the wall: %v", err)
		}
		c := &child{answers: map[string]string{}, lines: make(chan string, 64), stdin: l.Stdin}
		go func() {
			sc := bufio.NewScanner(l.Stdout)
			for sc.Scan() {
				c.lines <- sc.Text()
			}
			close(c.lines)
		}()
		var once sync.Once
		var ended error
		c.end = func() error {
			once.Do(func() {
				// Its stdin closed, the probe says done and exits on its
				// own: it is waited for, and killed only if it has not
				// left within the bound (Windows answers TerminateProcess
				// on a process that has exited with "Access is denied").
				l.Stdin.Close()
				for range c.lines {
				}
				exited := make(chan error, 1)
				go func() { _, err := cmd.Process.Wait(); exited <- err }()
				var err error
				select {
				case err = <-exited:
				case <-time.After(10 * time.Second):
					err = errors.Join(errors.New("the probe did not exit within 10s of its stdin closing"), cmd.Process.Kill())
					<-exited
				}
				ended = errors.Join(err, l.Contained())
			})
			return ended
		}
		t.Cleanup(func() { _ = c.end() })
		return c
	}
	until := func(c *child, last string) {
		t.Helper()
		deadline := time.After(30 * time.Second)
		for {
			select {
			case line, ok := <-c.lines:
				if !ok {
					t.Fatalf("the probe ended before %q: %v", last, c.answers)
				}
				if k, v, ok := strings.Cut(line, "="); ok {
					c.answers[k] = v
				}
				if line == last {
					return
				}
			case <-deadline:
				t.Fatalf("the probe never said %q: %v", last, c.answers)
			}
		}
	}
	late := filepath.Join(v1, "late.bin")
	one := start(k1, "WALL_READ_0="+filepath.Join(v1, "m.bin"), "WALL_READ_1="+filepath.Join(v3, "t.bin"),
		"WALL_READ_2="+theirs, "WALL_LATE="+late)
	two := start(k2, "WALL_READ_0="+filepath.Join(v2, "m.bin"))
	// A model linked into the first view while its child runs.
	lateBlob := blobOf("c", "linked while the child runs")
	time.Sleep(500 * time.Millisecond)
	if err := os.Link(lateBlob, late); err != nil {
		t.Fatal(err)
	}
	if err := plugstore.GrantView(v1, k1); err != nil {
		t.Fatal(err)
	}
	until(one, "waiting")
	until(two, "waiting")
	if got := one.answers["caps"]; got != capSID(k1) {
		t.Errorf("the first child's token holds %q, want exactly its view's capability %s", got, capSID(k1))
	}
	if got := two.answers["caps"]; got != capSID(k2) {
		t.Errorf("the second child's token holds %q, want exactly %s", got, capSID(k2))
	}
	for k, want := range map[string]string{"read0": "ok", "read1": "denied", "read2": "denied", "late": "ok"} {
		if one.answers[k] != want {
			t.Errorf("the first child: %s=%s, want %s (%v)", k, one.answers[k], want, one.answers)
		}
	}
	if two.answers["read0"] != "ok" {
		t.Errorf("the second child could not read the shared blob through its view: %v", two.answers)
	}
	if err := one.end(); err != nil {
		t.Errorf("ending the first child: %v", err)
	}
	if _, err := io.WriteString(two.stdin, "again\n"); err != nil {
		t.Fatal(err)
	}
	until(two, "done")
	if two.answers["again0"] != "ok" {
		t.Errorf("ending one child cut the other off the blob: %v", two.answers)
	}
}
