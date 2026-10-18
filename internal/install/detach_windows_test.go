//go:build windows

package install

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestDetachedRetryUsesANewCommandAndKeepsTheCallersFiles(t *testing.T) {
	dir := t.TempDir()
	system, err := windows.GetSystemDirectory()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(system, "cmd.exe")
	output, err := os.Create(filepath.Join(dir, "output.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	var attempts []*exec.Cmd
	factory := func() *exec.Cmd {
		c := exec.Command(path, "/d", "/c", "exit", "0")
		c.Dir = dir
		c.Env = append(os.Environ(), "AII_DETACHED_TEST=owned")
		c.Stdout, c.Stderr = output, output
		return c
	}
	p, err := startDetached(factory, func(c *exec.Cmd) error {
		attempts = append(attempts, c)
		if c.Dir != dir || c.Stdout != output || c.Stderr != output || c.Env[len(c.Env)-1] != "AII_DETACHED_TEST=owned" {
			t.Fatal("a retry lost the caller's command inputs")
		}
		want := uint32(detachedProcess | createNoWindow)
		if len(attempts) == 1 {
			want |= createBreakawayFromJob
		}
		if c.SysProcAttr == nil || !c.SysProcAttr.HideWindow || c.SysProcAttr.CreationFlags != want {
			t.Fatalf("attempt %d flags: %+v", len(attempts), c.SysProcAttr)
		}
		if len(attempts) == 1 {
			// Exercise a real failed Cmd.Start (which consumes Cmd even
			// without a process), then inject the breakaway refusal class.
			c.Path = filepath.Join(dir, "absent.exe")
			startErr := c.Start()
			c.Path = path

			if !errors.Is(startErr, os.ErrNotExist) && !errors.Is(startErr, windows.ERROR_ACCESS_DENIED) {
				t.Fatalf("fixture start: %v", startErr)
			}
			return fmt.Errorf("fixture breakaway refusal: %w", windows.ERROR_ACCESS_DENIED)
		}
		return c.Start()
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 2 || attempts[0] == attempts[1] {
		t.Fatal("a single-use exec.Cmd was reused")
	}
	waitDetachedProbe(t, p)
	if _, err := output.WriteString("the caller still owns its log"); err != nil {
		t.Fatalf("a failed attempt closed the caller's file: %v", err)
	}
}

func TestDetachedStartPreservesFailuresAndBoundsTheFallback(t *testing.T) {
	for _, tc := range []struct {
		name      string
		first     error
		wantCalls int
	}{
		{"missing", windows.ERROR_FILE_NOT_FOUND, 1},
		{"bad-image", windows.ERROR_BAD_EXE_FORMAT, 1},
		{"cancelled", context.Canceled, 1},
		{"breakaway-denied", windows.ERROR_ACCESS_DENIED, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			made, calls := 0, 0
			last := errors.New("second attempt refused")
			p, err := startDetached(func() *exec.Cmd { made++; return exec.Command("unused") }, func(*exec.Cmd) error {
				calls++
				if calls == 1 {
					return tc.first
				}
				return last
			})
			want := tc.first
			if tc.wantCalls == 2 {
				want = last
			}
			if p != nil || !errors.Is(err, want) || calls != tc.wantCalls || made != calls {
				t.Fatalf("process=%v error=%v calls=%d commands=%d", p, err, calls, made)
			}
		})
	}
}

func TestDetachedStartRunsOnlyItsDisposableChild(t *testing.T) {
	system, err := windows.GetSystemDirectory()
	if err != nil {
		t.Fatal(err)
	}
	p, err := StartDetached(func() *exec.Cmd { return exec.Command(filepath.Join(system, "cmd.exe"), "/d", "/c", "exit", "0") })
	if err != nil {
		t.Fatal(err)
	}
	waitDetachedProbe(t, p)
}

func waitDetachedProbe(t *testing.T, p *os.Process) {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		state, err := p.Wait()
		if err == nil && !state.Success() {
			err = fmt.Errorf("child exit: %s", state)
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		_ = p.Kill() // only the owned test child, through its original handle
		<-done
		t.Fatal("disposable child did not finish")
	}
}
