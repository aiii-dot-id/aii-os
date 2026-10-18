package audio

import (
	"context"
	"sync"
	"time"
)

type PacedSource struct {
	src     Source
	start   chan struct{}
	started sync.Once
	origin  time.Time
	ended   chan struct{}
	endOnce sync.Once

	now  func() time.Time
	wait func(ctx context.Context, d time.Duration) error
}

func NewPacedSource(src Source) *PacedSource {
	return &PacedSource{src: src, start: make(chan struct{}), ended: make(chan struct{}), now: time.Now, wait: sleepCtx}
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *PacedSource) Format() Format { return s.src.Format() }

func (s *PacedSource) Start() {
	s.started.Do(func() {
		s.origin = s.now()
		close(s.start)
	})
}

func (s *PacedSource) Ended() <-chan struct{} { return s.ended }

func (s *PacedSource) Read(ctx context.Context) (Frame, error) {
	select {
	case <-s.start:
	case <-ctx.Done():
		return Frame{}, ctx.Err()
	}
	fr, err := s.src.Read(ctx)
	if err != nil {
		return fr, err
	}
	at := s.origin.Add(time.Duration(fr.Start) * time.Second / time.Duration(s.src.Format().Rate))
	if d := at.Sub(s.now()); d > 0 {
		if err := s.wait(ctx, d); err != nil {
			return Frame{}, err
		}
	}
	if fr.Kind == KindEnd {
		s.endOnce.Do(func() { close(s.ended) })
	}
	return fr, nil
}
