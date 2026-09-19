// .
// .
// .

package pluginhost

import (
	"context"
	"github.com/aiii-dot-id/aii-os/internal/audio"
	"io"
	"testing"
	"time"
)

type reviewOpenMic struct{ entered chan struct{} }

func (s reviewOpenMic) Format() audio.Format { return mono16k }
func (s reviewOpenMic) Close() error         { return nil }
func (s reviewOpenMic) Read(ctx context.Context) (audio.Frame, error) {
	close(s.entered)
	<-ctx.Done()
	return audio.Frame{}, ctx.Err()
}
func TestReviewCleanTerminalDoesNotFaultOpenMic(t *testing.T) {
	reviewCleanTerminal(t, false)
}
func TestReviewCleanTerminalEOFControl(t *testing.T) {
	reviewCleanTerminal(t, true)
}

type reviewEOFMic struct{ reviewOpenMic }

func (s reviewEOFMic) Read(ctx context.Context) (audio.Frame, error) {
	close(s.entered)
	return audio.Frame{}, io.EOF
}
func reviewCleanTerminal(t *testing.T, eof bool) {
	t.Helper()
	h := auditNewAudioHarness(t)
	source := reviewOpenMic{make(chan struct{})}
	var mic audio.Source = source
	if eof {
		mic = reviewEOFMic{source}
	}
	plane := audio.NewPlane()
	if err := plane.Register(&audio.Endpoint{ID: "mic", Source: mic}); err != nil {
		t.Fatal(err)
	}
	if err := plane.Register(&audio.Endpoint{ID: "spk", Sink: audio.NewCaptureSink(mono16k)}); err != nil {
		t.Fatal(err)
	}
	b, err := plane.Bind("clean", "mic", "spk", true)
	if err != nil {
		t.Fatal(err)
	}
	if err = h.v.OpenWithAudio(h.ctx, "clean", b, nil); err != nil {
		t.Fatal(err)
	}
	auditAudioJoin(t, "mic blocked", source.entered)
	_, p := h.v.Audio()
	h.terminal(t, "clean")
	auditAudioJoin(t, "pump retired", p.Done())
	select {
	case <-h.v.Untrusted():
		h.v.mu.Lock()
		why := h.v.untrustWhy
		h.v.mu.Unlock()
		t.Fatalf("clean terminal became fault: %s", why)
	case <-time.After(100 * time.Millisecond):
	}
}
