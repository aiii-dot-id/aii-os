package logsink

import (
	"bytes"
	"log"
	"log/slog"
	"os"
	"strings"
	"sync"
)

// .
// .
type TB interface {
	Helper()
	Cleanup(func())
}

// .
// .
// .
// .
type Capture struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (c *Capture) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.Write(p)
}

// .
func (c *Capture) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.String()
}

// .
func (c *Capture) Lines() []string {
	s := strings.TrimSuffix(c.String(), "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// .
// .
// .
// .
// .
func (c *Capture) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.buf.Reset()
}

// .
func (c *Capture) Contains(s string) bool { return strings.Contains(c.String(), s) }

// .
// .
// .
func CaptureForTest(t TB) *Capture {
	t.Helper()
	c := &Capture{}
	resetTicks()
	prevLogger := slog.Default()
	prevFlags := log.Flags()
	prevWriter := log.Writer()
	slog.SetDefault(slog.New(newHandler(c, nil)))
	t.Cleanup(func() {
		slog.SetDefault(prevLogger)
		log.SetOutput(prevWriter)
		log.SetFlags(prevFlags)
	})
	return c
}

// .
func stderrOrNil() *os.File { return os.Stderr }
