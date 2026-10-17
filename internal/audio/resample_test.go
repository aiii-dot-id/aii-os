package audio

import (
	"encoding/binary"
	"math"
	"testing"
)

func sineS16(rate, n int, hz float64, start int64) []byte {
	out := make([]byte, n*2)
	for i := 0; i < n; i++ {
		v := 0.5 * math.Sin(2*math.Pi*hz*float64(start+int64(i))/float64(rate))
		binary.LittleEndian.PutUint16(out[i*2:], uint16(int16(math.Round(v*32768))))
	}
	return out
}

func samplesOf(pcm []byte) []float64 {
	out := make([]float64, len(pcm)/2)
	for i := range out {
		out[i] = float64(int16(binary.LittleEndian.Uint16(pcm[i*2:]))) / 32768
	}
	return out
}

// The conversion keeps the clock: counts and positions map by
// ceil(X·to/from), exact at integer ratios; a 1 kHz tone through 48 kHz
// to 16 kHz and back is the same tone; a discontinuity restarts the clock
// where it was declared; equal rates pass samples through.
func TestResamplerKeepsTheClockAndTheTone(t *testing.T) {
	src, dst := Format{Rate: 48000, Channels: 1}, Format{Rate: 16000, Channels: 1}
	r := NewResampler(src, dst)
	var out []byte
	fed := 0
	for _, n := range []int{4096, 4096, 4096, 1000} { // chunks not divisible by 3
		r.Feed(sineS16(48000, n, 1000, int64(fed)))
		fed += n
		out = append(out, r.Take()...)
	}
	out = append(out, r.Finish()...)
	if got, want := int64(len(out)/2), r.TargetPos(int64(fed)); got != want || want != int64(math.Ceil(float64(fed)/3)) || r.Position() != want {
		t.Fatalf("48k→16k: %d samples out for %d in, want %d (position %d)", got, fed, want, r.Position())
	}
	// The tone survives: after the kernel's settling, each output sample
	// matches a 1 kHz sine at 16 kHz within 2% of full scale.
	vals := samplesOf(out)
	for i := 40; i < len(vals)-40; i++ {
		want := 0.5 * math.Sin(2*math.Pi*1000*float64(i)/16000)
		if math.Abs(vals[i]-want) > 0.02 {
			t.Fatalf("sample %d: %.4f, want %.4f", i, vals[i], want)
		}
	}
	// Up again: 16 kHz → 48 kHz, the count exact, the tone intact.
	up := NewResampler(dst, src)
	up.Feed(out)
	back := append(up.Take(), up.Finish()...)
	if got := len(back) / 2; got != len(out)/2*3 {
		t.Fatalf("16k→48k: %d samples for %d in", got, len(out)/2)
	}
	bv := samplesOf(back)
	for i := 120; i < len(bv)-120; i++ {
		want := 0.5 * math.Sin(2*math.Pi*1000*float64(i)/48000)
		if math.Abs(bv[i]-want) > 0.03 {
			t.Fatalf("up sample %d: %.4f, want %.4f", i, bv[i], want)
		}
	}
	// Positions: a boundary at source sample 1000 is target 334 (the next
	// whole sample of the coarser clock); at 999 it is exactly 333.
	if r.TargetPos(1000) != 334 || r.TargetPos(999) != 333 || up.TargetPos(333) != 999 {
		t.Fatalf("position mapping: %d %d %d", r.TargetPos(1000), r.TargetPos(999), up.TargetPos(333))
	}
	// A discontinuity: the clock restarts where it was declared.
	r2 := NewResampler(src, dst)
	r2.Feed(sineS16(48000, 3000, 1000, 0))
	first := append(r2.Take(), r2.Finish()...)
	r2.Reset(96000)
	if r2.Position() != 32000 || len(first) != 1000*2 {
		t.Fatalf("after a gap declared at 96000 the clock stands at %d (want 32000); first segment %d samples", r2.Position(), len(first)/2)
	}
	r2.Feed(sineS16(48000, 3000, 1000, 96000))
	second := append(r2.Take(), r2.Finish()...)
	if len(second) != 1000*2 || r2.Position() != 33000 {
		t.Fatalf("second segment: %d samples, position %d", len(second)/2, r2.Position())
	}
	// Equal rates: through, stereo mixed to mono.
	st := NewResampler(Format{Rate: 16000, Channels: 2}, Format{Rate: 16000, Channels: 1})
	pcm := make([]byte, 8)
	for i, v := range []int16{1000, 3000, -2000, -2000} {
		binary.LittleEndian.PutUint16(pcm[i*2:], uint16(v))
	}
	st.Feed(pcm)
	got := samplesOf(append(st.Take(), st.Finish()...))
	if len(got) != 2 || math.Abs(got[0]-2000.0/32768) > 1e-4 || math.Abs(got[1]+2000.0/32768) > 1e-4 || st.Position() != 2 {
		t.Fatalf("stereo to mono at one rate: %v, position %d", got, st.Position())
	}
}
