//go:build (linux && !android) || (darwin && !ios)

package pluginhost

import (
	"context"
	"testing"
	"time"
)

// Linux and macOS only, of the systems that supervise a child: a stop's
// graces are the end of input and SIGTERM, and on Windows a stop is the kill
// (supervisor's TestCloseEscalatesToKill), so a child deaf to both cannot be
// told apart there from one given no grace.

// A CHILD IS STOPPED WITHIN THE STOP ITS OPERATOR GAVE IT. The supervisor
// gave every child five seconds before the kill. A worker deaf to the end of
// its input and to SIGTERM, given one second, is killed after its second.
func TestAChildIsStoppedWithinItsOperatorsStop(t *testing.T) {
	ap := wasmWorker(t, "org.example.bounds-stop", "ready-ignore-term", ChildBounds{Stop: time.Second})
	began := time.Now()
	if err := ap.closeChannel(context.Background()); err != nil {
		t.Fatalf("close: %v", err)
	}
	if took := time.Since(began); took < 900*time.Millisecond || took >= 3*time.Second {
		t.Fatalf("a child given 1s to stop was killed after %s, not after its own second (the default is %s)", took, DefaultStopBudget)
	}
}

func TestANativeChildIsStoppedWithinItsOperatorsStop(t *testing.T) {
	ap := nativeChild(t, "org.example.bounds-native-stop", "ignore-term", ChildBounds{Stop: time.Second})
	began := time.Now()
	if err := ap.CloseQuiet(context.Background()); err != nil {
		t.Fatalf("close: %v", err)
	}
	if took := time.Since(began); took < 900*time.Millisecond || took >= 3*time.Second {
		t.Fatalf("a native child given 1s to stop was killed after %s, not after its own second (the default is %s)", took, DefaultStopBudget)
	}
}
