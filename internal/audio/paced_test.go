package audio

import (
	"bytes"
	"context"
	"errors"
	"math"
	"testing"
	"time"
)

// fakeClock is a clock that moves only when the paced source waits on it.
type fakeClock struct {
	now   time.Time
	waits []time.Duration
}

func (c *fakeClock) Now() time.Time { return c.now }
func (c *fakeClock) Wait(_ context.Context, d time.Duration) error {
	c.waits = append(c.waits, d)
	c.now = c.now.Add(d)
	return nil
}

// A RECORDING IS SENT AS IF SPOKEN: nothing before Start, and then each
// frame no earlier than the origin plus its first sample over the rate —
// its END at the recording's exclusive end — never early, never waiting
// for a frame whose time has already come.
func TestAPacedSourceReleasesNothingBeforeStartAndEachFrameAtItsTime(t *testing.T) {
	f := Format{Rate: 16000, Channels: 1}
	file, err := NewFileSource(bytes.NewReader(make([]byte, 2*1000)), f, 160) // 1000 samples: six frames and an END
	if err != nil {
		t.Fatal(err)
	}
	s := NewPacedSource(file)
	clock := &fakeClock{now: time.Unix(5000, 0)}
	s.now, s.wait = clock.Now, clock.Wait

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if fr, err := s.Read(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("a frame left before Start: %+v %v", fr, err)
	}
	clock.now = clock.now.Add(time.Hour) // the gate opens later than the source was made
	s.Start()
	origin := clock.now
	var starts []int64
	for {
		fr, err := s.Read(context.Background())
		if err != nil {
			t.Fatalf("after %v: %v", starts, err)
		}
		due := origin.Add(time.Duration(fr.Start) * time.Second / 16000)
		if clock.now.Before(due) {
			t.Fatalf("frame at sample %d left at %v, before its time %v", fr.Start, clock.now.Sub(origin), due.Sub(origin))
		}
		starts = append(starts, fr.Start)
		if fr.Kind == KindEnd {
			break
		}
	}
	if len(starts) != 8 || starts[7] != 1000 || clock.now.Sub(origin) != 1000*time.Second/16000 {
		t.Fatalf("frames at %v, ending %v after the origin", starts, clock.now.Sub(origin))
	}
	select {
	case <-s.Ended():
	default:
		t.Fatal("Ended must fire once the END is released")
	}
	// A reader that is late is never made later still.
	waits := len(clock.waits)
	s2 := NewPacedSource(mustFile(t, f, 320))
	s2.now, s2.wait = clock.Now, clock.Wait
	s2.Start()
	clock.now = clock.now.Add(time.Second)
	for i := 0; i < 2; i++ {
		if _, err := s2.Read(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if len(clock.waits) != waits {
		t.Fatalf("frames whose time had come waited: %v", clock.waits[waits:])
	}
}

func mustFile(t *testing.T, f Format, samples int) *FileSource {
	t.Helper()
	file, err := NewFileSource(bytes.NewReader(make([]byte, 2*samples)), f, 160)
	if err != nil {
		t.Fatal(err)
	}
	return file
}

// A REPLY'S LEVEL: silence and nothing at all have none; a full-scale
// square is full scale; half of full scale is six decibels under it.
func TestALevelIsTheRootMeanSquareInDecibelsOfFullScale(t *testing.T) {
	pcm := func(vs ...int16) []byte {
		var b []byte
		for _, v := range vs {
			b = append(b, byte(uint16(v)), byte(uint16(v)>>8))
		}
		return b
	}
	if !math.IsInf(RMSDBFS(pcm(0, 0, 0, 0)), -1) || !math.IsInf(RMSDBFS(nil), -1) {
		t.Fatal("silence and nothing have no level")
	}
	if got := RMSDBFS(pcm(32767, -32768, 32767, -32768)); math.Abs(got) > 1e-3 {
		t.Fatalf("a full-scale square is full scale: %v", got)
	}
	if got := RMSDBFS(pcm(16384, -16384)); math.Abs(got-20*math.Log10(0.5)) > 1e-12 {
		t.Fatalf("half of full scale: %v", got)
	}
	var l Level
	l.Add(pcm(16384))
	l.Add(pcm(-16384, 16384))
	if l.DBFS() != RMSDBFS(pcm(16384, -16384, 16384)) {
		t.Fatal("a level counted frame by frame is the level of the whole")
	}
}
