package audio

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
)

type FileSource struct {
	f       Format
	r       io.Reader
	chunk   int
	stream  uint32
	next    int64
	seq     uint32
	ended   bool
	endSent bool
}

func NewFileSource(r io.Reader, f Format, chunkSamples int) (*FileSource, error) {
	if chunkSamples <= 0 {
		chunkSamples = 320
	}
	br := &peekReader{r: r}
	src := io.Reader(br)
	head, err := br.peek(12)
	if err == nil && string(head[0:4]) == "RIFF" && string(head[8:12]) == "WAVE" {
		file, err := io.ReadAll(br)
		if err != nil {
			return nil, fmt.Errorf("audio: wav: %w", err)
		}
		w, err := ReadWAV(file)
		if err != nil {
			return nil, fmt.Errorf("audio: %w", err)
		}
		if w.Tag != 1 || w.Bits != 16 {
			return nil, errors.New("audio: only 16-bit PCM WAV is read")
		}
		f, src = w.Format, bytes.NewReader(w.PCM)
	}
	if f.Rate <= 0 || f.Channels <= 0 {
		return nil, errors.New("audio: a file source needs a rate and channels")
	}
	return &FileSource{f: f, r: src, chunk: chunkSamples, stream: 1}, nil
}

func (s *FileSource) Format() Format { return s.f }

func (s *FileSource) Read(ctx context.Context) (Frame, error) {
	if s.endSent {
		return Frame{}, io.EOF
	}
	if err := ctx.Err(); err != nil {
		return Frame{}, err
	}
	if s.ended {
		s.endSent = true
		s.seq++
		return Frame{Kind: KindEnd, Stream: s.stream, Seq: s.seq, Start: s.next}, nil
	}
	buf := make([]byte, s.chunk*s.f.BytesPerSample())
	n, err := io.ReadFull(s.r, buf)
	n -= n % s.f.BytesPerSample()
	if n > 0 {
		s.seq++
		fr := Frame{Kind: KindPCM, Stream: s.stream, Seq: s.seq, Start: s.next, PCM: buf[:n]}
		s.next += fr.Samples(s.f)
		if err == nil {
			return fr, nil
		}

		s.ended = true
		return fr, nil
	}
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return Frame{}, err
	}
	s.ended, s.endSent = true, true
	s.seq++
	return Frame{Kind: KindEnd, Stream: s.stream, Seq: s.seq, Start: s.next}, nil
}

type peekReader struct {
	r   io.Reader
	buf []byte
}

func (p *peekReader) peek(n int) ([]byte, error) {
	for len(p.buf) < n {
		b := make([]byte, n-len(p.buf))
		k, err := p.r.Read(b)
		p.buf = append(p.buf, b[:k]...)
		if err != nil {
			return p.buf, err
		}
	}
	return p.buf[:n], nil
}

func (p *peekReader) Read(b []byte) (int, error) {
	if len(p.buf) > 0 {
		n := copy(b, p.buf)
		p.buf = p.buf[n:]
		return n, nil
	}
	return p.r.Read(b)
}

type CaptureSink struct {
	f      Format
	mu     sync.Mutex
	frames []Frame
}

func NewCaptureSink(f Format) *CaptureSink { return &CaptureSink{f: f} }

func (c *CaptureSink) Format() Format { return c.f }
func (c *CaptureSink) Write(ctx context.Context, fr Frame) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	cp := fr
	cp.PCM = append([]byte(nil), fr.PCM...)
	c.frames = append(c.frames, cp)
	return nil
}

func (c *CaptureSink) Close() error { return nil }

func (c *CaptureSink) Frames() []Frame {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]Frame(nil), c.frames...)
}

func (c *CaptureSink) Streams() []uint32 {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []uint32
	seen := map[uint32]bool{}
	for _, fr := range c.frames {
		if !seen[fr.Stream] {
			seen[fr.Stream] = true
			out = append(out, fr.Stream)
		}
	}
	return out
}

func (c *CaptureSink) StreamPCM(stream uint32) []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []byte
	for _, fr := range c.frames {
		if fr.Stream == stream {
			out = append(out, fr.PCM...)
		}
	}
	return out
}

func (c *CaptureSink) StreamEnd(stream uint32) int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := len(c.frames) - 1; i >= 0; i-- {
		if c.frames[i].Stream == stream && c.frames[i].Kind == KindEnd {
			return c.frames[i].Start
		}
	}
	return -1
}

func (c *CaptureSink) PCM() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []byte
	for _, fr := range c.frames {
		out = append(out, fr.PCM...)
	}
	return out
}

func (c *CaptureSink) End() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := len(c.frames) - 1; i >= 0; i-- {
		if c.frames[i].Kind == KindEnd {
			return c.frames[i].Start
		}
	}
	return -1
}
