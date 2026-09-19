package audio

import (
	"bytes"
	"context"
	"sync"
	"testing"
)

// .
// .
// .
// .
// .
// .
// .
// .
func TestNothingCrossesAFiniteEngineCutoffFrom48k(t *testing.T) {
	browser := Format{Rate: 48000, Channels: 1}
	// .
	src, err := NewFileSource(bytes.NewReader(make([]byte, 48000*2)), browser, 960)
	if err != nil {
		t.Fatal(err)
	}
	ch := &recordingInput{}
	p := NewPump(&Binding{Source: src, Sink: NewCaptureSink(mono16k), InFormat: browser, OutFormat: mono16k}, ch)
	p.EngineIn = mono16k

	// .
	// .
	const engineEnd = 5000
	browserEnd := engineEnd * 3
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

// .
// .
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
