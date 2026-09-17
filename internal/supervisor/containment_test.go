package supervisor

import (
	"context"
	"errors"
	"os/exec"
	"sync/atomic"
	"testing"
	"time"
)

type containmentImage struct{ closed atomic.Int32 }

func (i *containmentImage) Close() error { i.closed.Add(1); return nil }

func TestCloseReportsUnpublishedSpawnPending(t *testing.T) {
	oldGrace := closeGraceKill
	closeGraceKill = time.Millisecond
	t.Cleanup(func() { closeGraceKill = oldGrace })
	done := make(chan struct{})
	image := &containmentImage{}
	s := &Supervisor{spec: Spec{PluginID: "pending.spawn", Artifact: image}, state: StateStarting, spawning: done}
	var pending *ContainmentCleanupError
	if err := s.Close(); !errors.As(err, &pending) {
		t.Fatalf("unpublished spawn acknowledged as retired: %v", err)
	}
	if image.closed.Load() != 0 {
		t.Fatal("unpublished spawn lost its executable binding")
	}
	close(done)
	if err := s.Close(); err != nil || image.closed.Load() != 1 {
		t.Fatalf("completed spawn did not release its executable once: %v", err)
	}
}

func TestContainmentDescriptionCannotAuthorizeSAFE(t *testing.T) {
	for _, c := range []Containment{
		{Description: "no network; read-only filesystem"},
		{NetworkDenied: true}, {FilesystemRestricted: true},
	} {
		if c.Isolated() {
			t.Fatal("incomplete enforcement facts claimed isolation")
		}
	}
	c := Containment{Description: "wording is immaterial", NetworkDenied: true, FilesystemRestricted: true}
	if !c.Isolated() {
		t.Fatal("successful enforcement facts were ignored")
	}
	s := &Supervisor{child: &child{isolation: c, exited: make(chan struct{})}}
	if !s.Containment().Isolated() {
		t.Fatal("live child lost its enforcement facts")
	}
	close(s.child.exited)
	if s.Containment().Isolated() {
		t.Fatal("retired child still supplies containment evidence")
	}
}

func TestRetirementWaitsForContainmentCleanupAndReportsFailure(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failure"}[fail], func(t *testing.T) {
			cmd := exec.Command(fakechildBin, "echo")
			stdin, err := cmd.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			entered, release := make(chan struct{}), make(chan struct{})
			t.Cleanup(func() {
				select {
				case <-release:
				default:
					close(release)
				}
				_ = cmd.Process.Kill()
			})
			want := errors.New("job retirement observation failed")
			c := &child{cmd: cmd, stdin: stdin, exited: make(chan struct{}), abandon: make(chan struct{}), contained: func() error {
				close(entered)
				<-release
				if fail {
					return want
				}
				return nil
			}}
			_, logger := newCapture()
			s := &Supervisor{spec: Spec{PluginID: "retirement.probe", Log: logger}, child: c, state: StateRunning, gen: 1}
			go s.reap(c, 1)
			done := make(chan error, 2)
			go func() { done <- s.CloseContext(context.Background()) }()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("cleanup was not reached")
			}
			select {
			case <-c.exited:
				t.Fatal("retirement announced before cleanup")
			default:
			}
			select {
			case err := <-done:
				t.Fatalf("Close returned before cleanup: %v", err)
			default:
			}
			go func() { done <- s.CloseContext(context.Background()) }()
			close(release)
			for i := 0; i < 2; i++ {
				select {
				case err := <-done:
					if fail && !errors.Is(err, want) {
						t.Fatalf("lost cleanup failure: %v", err)
					}
					if !fail && err != nil {
						t.Fatal(err)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("Close did not finish")
				}
			}
			if s.Pid() != 0 {
				t.Fatal("retired child still reported as a live PID")
			}
		})
	}
}
