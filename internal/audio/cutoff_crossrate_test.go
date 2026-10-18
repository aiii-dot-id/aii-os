package audio

import (
	"bytes"
	"context"
	"sync"
	"testing"
)

// ZERO SAMPLES PAST THE ACCEPTED CUTOFF, ACROSS TWO CLOCKS. A page
// captures at 48 kHz; the engine listens at 16 kHz and names a finite
// boundary on ITS clock — a listening limit, or the operator's Finish.
// Three samples of the page's clock are one of the engine's, so the
// boundary falls inside a browser chunk more often than not, and audio
// delivered past it is speech the operator was told had stopped being
// heard. The engine's cutoff is exact in both directions: everything up
// to it arrives, nothing after it does.
func TestNothingCrossesAFiniteEngineCutoffFrom48k(t *testing.T) {
	browser := Format{Rate: 48000, Channels: 1}
	// A second of capture, in 20 ms chunks: 50 chunks of 960 samples.
	src, err := NewFileSource(bytes.NewReader(make([]byte, 48000*2)), browser, 960)
	if err != nil {
		t.Fatal(err)
	}
	ch := &recordingInput{}
	p := NewPump(&Binding{Source: src, Sink: NewCaptureSink(mono16k), InFormat: browser, OutFormat: mono16k}, ch)
	p.EngineIn = mono16k

	// A cutoff the engine names at 5000 of its own samples, landing well
	// inside one of the page's chunks.
	const engineEnd = 5000
	browserEnd := engineEnd * 3 // the same instant on the page's clock
	if got := p.EngineCutoff(int64(browserEnd)); got != engineEnd {
		t.Fatalf("the two clocks must name the same instant: %d on the engine's, not %d", got, engineEnd)
	}
	p.Cutoff(int64(browserEnd))
	p.runInput(context.Background())

	pcm, end := ch.delivered()
	if pcm != engineEnd {
		t.Fatalf("the engine is given exactly the audio inside its boundary: %d samples, want %d", pcm, engineEnd)
	}
	if end != engineEnd {
		t.Fatalf("the stream ends AT the cutoff, on the engine's clock: %d, want %d", end, engineEnd)
	}
}

// recordingInput counts the engine-clock samples actually delivered and
// remembers where the stream was declared to end.
type recordingInput struct {
	mu  sync.Mutex
	pcm int64
	end int64
}

func (c *recordingInput) WriteInput(fr Frame) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch fr.Kind {
	case KindPCM:
		c.pcm += int64(len(fr.PCM) / 2)
	case KindEnd:
		c.end = fr.Start
	}
	return nil
}
func (c *recordingInput) ReadOutput() (Frame, error) { select {} }
func (c *recordingInput) Close() error               { return nil }
func (c *recordingInput) delivered() (int64, int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.pcm, c.end
}
