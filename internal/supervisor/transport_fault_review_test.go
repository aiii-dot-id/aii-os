package supervisor

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

type reviewBrokenWriter struct{}

func (reviewBrokenWriter) Write([]byte) (int, error) { return 0, errors.New("injected broken pipe") }

func TestReviewWriterFailureCannotPanicReader(t *testing.T) {
	if os.Getenv("AII_REVIEW_FAULT_CHILD") == "1" {
		frames := make(chan []byte, 1)
		c := NewSessionClientFrames(frames, reviewBrokenWriter{}, nil, 8)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		done := make(chan error, 1)
		go func() { done <- c.Run(ctx) }()
		_, _ = c.Control(ctx, "speech.session.open", map[string]any{"session_id": "s1"})
		<-c.Done()
		if _, ok := <-c.Events(); ok {
			t.Fatal("fixture had an unexpected prior event")
		}
		frames <- []byte(`{"jsonrpc":"2.0","method":"session.event","params":{"type":"session_ready","session_id":"s1","sequence":1}}`)
		select {
		case <-done:
			return
		case <-time.After(100 * time.Millisecond):
			t.Fatal("reader survived fault but failed to terminate")
		}
		return
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=^TestReviewWriterFailureCannotPanicReader$", "-test.count=1")
	cmd.Env = append(os.Environ(), "AII_REVIEW_FAULT_CHILD=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("writer failure did not terminate safely (send-on-closed=%v): %v\n%s", strings.Contains(string(out), "send on closed channel"), err, out)
	}
}
