package audio

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"testing"
)

type refResampler struct {
	from, to Format
	channels []*refResampler
	same     bool    // equal rates: samples pass through, channels aside
	ratio    float64 // to.Rate / from.Rate
	scale    float64 // kernel stretch in source samples: 1 up, from/to down
	half     int     // kernel half-width in source samples

	hist      []float64 // mono source samples from histStart
	histStart int64     // source position of hist[0]
	inPos     int64     // source position after the last sample fed
	outPos    int64     // next target sample to produce
}

func newRefResampler(from, to Format) *refResampler {
	r := &refResampler{from: from, to: to, same: from.Rate == to.Rate}
	if from.Channels == to.Channels && from.Channels > 1 {
		for i := 0; i < from.Channels; i++ {
			r.channels = append(r.channels, newRefResampler(Format{Rate: from.Rate, Channels: 1}, Format{Rate: to.Rate, Channels: 1}))
		}
	}
	r.ratio = float64(to.Rate) / float64(from.Rate)
	r.scale = 1
	if r.ratio < 1 {
		r.scale = 1 / r.ratio
	}
	r.half = int(math.Ceil(8 * r.scale))
	return r
}

func (r *refResampler) TargetPos(src int64) int64 {
	if r.same {
		return src
	}
	return (src*int64(r.to.Rate) + int64(r.from.Rate) - 1) / int64(r.from.Rate)
}

func (r *refResampler) Position() int64 { return r.outPos }

func (r *refResampler) Reset(src int64) {
	for _, ch := range r.channels {
		ch.Reset(src)
	}
	r.hist = r.hist[:0]
	r.histStart, r.inPos = src, src
	r.outPos = r.TargetPos(src)
}

func (r *refResampler) Feed(pcm []byte) {
	n := len(pcm) / r.from.BytesPerSample()
	if len(r.channels) != 0 {
		for c, ch := range r.channels {
			mono := make([]byte, n*2)
			for i := 0; i < n; i++ {
				copy(mono[i*2:i*2+2], pcm[(i*len(r.channels)+c)*2:][:2])
			}
			ch.Feed(mono)
		}
		r.inPos += int64(n)
		return
	}
	if r.same {
		r.inPos += int64(n)
		r.hist = append(r.hist, r.mono(pcm, n)...)
		return
	}
	r.hist = append(r.hist, r.mono(pcm, n)...)
	r.inPos += int64(n)
}

func (r *refResampler) mono(pcm []byte, n int) []float64 {
	out := make([]float64, n)
	ch := r.from.Channels
	for i := 0; i < n; i++ {
		var acc float64
		for c := 0; c < ch; c++ {
			acc += float64(int16(binary.LittleEndian.Uint16(pcm[(i*ch+c)*2:])))
		}
		out[i] = acc / float64(ch) / 32768
	}
	return out
}

func (r *refResampler) Take() []byte { return r.produce(false) }

func (r *refResampler) Finish() []byte { return r.produce(true) }

func (r *refResampler) produce(flush bool) []byte {
	if len(r.channels) != 0 {
		parts := make([][]byte, len(r.channels))
		for c, ch := range r.channels {
			parts[c] = ch.produce(flush)
		}
		out := make([]byte, len(parts[0])*len(parts))
		for i := 0; i < len(parts[0])/2; i++ {
			for c, part := range parts {
				copy(out[(i*len(parts)+c)*2:][:2], part[i*2:][:2])
			}
		}
		r.outPos = r.channels[0].Position()
		return out
	}
	var vals []float64
	if r.same {
		vals = append(vals, r.hist...)
		r.hist = r.hist[:0]
		r.histStart = r.inPos
		r.outPos += int64(len(vals))
		return r.encode(vals)
	}
	limit := r.TargetPos(r.inPos) // exclusive: the last target sample before inPos
	for r.outPos < limit {
		t := float64(r.outPos) / r.ratio // source time of this target sample
		center := int64(math.Floor(t))
		if !flush && center+int64(r.half) >= r.inPos {
			break // its kernel reaches past what has been fed
		}
		var acc, wsum float64
		for k := center - int64(r.half); k <= center+int64(r.half)+1; k++ {
			u := (float64(k) - t) / r.scale
			if u <= -8 || u >= 8 {
				continue
			}
			w := refSinc(u) * refBlackman(u/8)
			wsum += w
			if k < r.histStart || k >= r.inPos {
				continue // before the segment, or beyond it: silence
			}
			acc += w * r.hist[k-r.histStart]
		}
		if wsum != 0 {
			acc /= wsum
		}
		vals = append(vals, acc)
		r.outPos++
	}
	if keep := int64(math.Floor(float64(r.outPos)/r.ratio)) - int64(r.half) - 1; keep > r.histStart {
		if keep > r.inPos {
			keep = r.inPos
		}
		r.hist = append(r.hist[:0], r.hist[keep-r.histStart:]...)
		r.histStart = keep
	}
	if flush {
		r.hist = r.hist[:0]
		r.histStart = r.inPos
	}
	return r.encode(vals)
}

func (r *refResampler) encode(vals []float64) []byte {
	if len(vals) == 0 {
		return nil
	}
	ch := r.to.Channels
	out := make([]byte, len(vals)*ch*2)
	for i, v := range vals {
		s := math.Round(v * 32768)
		if s > 32767 {
			s = 32767
		} else if s < -32768 {
			s = -32768
		}
		for c := 0; c < ch; c++ {
			binary.LittleEndian.PutUint16(out[(i*ch+c)*2:], uint16(int16(s)))
		}
	}
	return out
}

func refSinc(x float64) float64 {
	if x == 0 {
		return 1
	}
	px := math.Pi * x
	return math.Sin(px) / px
}

func refBlackman(x float64) float64 {
	if x <= -1 || x >= 1 {
		return 0
	}
	return 0.42 + 0.5*math.Cos(math.Pi*x) + 0.08*math.Cos(2*math.Pi*x)
}

// goldenSignal is n frames of from: a tone, a chirp and a deterministic
// noise, driven to full scale now and then so the clamp is exercised;
// each channel its own.
func goldenSignal(from Format, n int, seed uint32) []byte {
	out := make([]byte, n*from.BytesPerSample())
	x := seed
	for i := 0; i < n; i++ {
		tt := float64(i) / float64(from.Rate)
		for c := 0; c < from.Channels; c++ {
			x = x*1664525 + 1013904223
			v := 0.45*math.Sin(2*math.Pi*(1000+170*float64(c))*tt) + 0.3*math.Sin(2*math.Pi*(200+300*tt)*tt) + 0.1*(float64(x>>8)/float64(1<<24)-0.5)
			if i%4000 < 40 {
				v *= 3 // past full scale: the clamp
			}
			s := max(-32768, min(32767, math.Round(v*32767)))
			binary.LittleEndian.PutUint16(out[(i*from.Channels+c)*2:], uint16(int16(s)))
		}
	}
	return out
}

// THE TABLE CHANGES NO SAMPLE. The per-phase kernel must yield, call for
// call, the bytes the per-tap formula yields: on every rate pair the
// pump meets (48 and 44.1 kHz pages, 16 and 24 kHz engines, both ways)
// and the channel paths (mixed down, duplicated, kept apart, passed
// through), with ragged frames, Take after every feed and Finish at
// every segment's end. The segments start at the origin, half an hour
// in, astride source position 2^40 — where t's rounding coarsens and a
// phase's fraction moves, so a slot kept by its phase alone would be
// stale — and at a negative position only a peer's error declares. The
// existing tests allow 2% (the tone) and would not see one LSB; this
// one sees it.
func TestResamplerTableMatchesThePerTapFormula(t *testing.T) {
	m := func(rate int) Format { return Format{Rate: rate, Channels: 1} }
	s := func(rate int) Format { return Format{Rate: rate, Channels: 2} }
	pairs := []struct{ from, to Format }{
		{m(48000), m(16000)}, {m(16000), m(48000)}, {m(48000), m(24000)}, {m(24000), m(48000)},
		{m(44100), m(16000)}, {m(16000), m(44100)}, {m(44100), m(24000)}, {m(24000), m(44100)},
		{m(24000), m(16000)}, {m(16000), m(24000)}, {m(44100), m(48000)}, {m(48000), m(44100)},
		{s(48000), m(16000)}, {m(24000), s(48000)}, {s(44100), s(16000)}, {s(16000), m(16000)},
	}
	for _, pr := range pairs {
		t.Run(fmt.Sprintf("%dx%d-%dx%d", pr.from.Rate, pr.from.Channels, pr.to.Rate, pr.to.Channels), func(t *testing.T) {
			want, got := newRefResampler(pr.from, pr.to), NewResampler(pr.from, pr.to)
			n := 2 * pr.from.Rate
			sig := goldenSignal(pr.from, n, uint32(pr.from.Rate+pr.to.Rate))
			bps := pr.from.BytesPerSample()
			sizes := []int{pr.from.Rate / 50, pr.from.Rate/1000*7 + 3, pr.from.Rate / 25, 1}
			for seg, start := range []int64{0, 1800 * int64(pr.from.Rate), 1<<40 - int64(n/2), -100003} {
				if seg > 0 {
					want.Reset(start)
					got.Reset(start)
				}
				for off, i := 0, 0; off < n; i++ {
					k := min(sizes[i%len(sizes)], n-off)
					want.Feed(sig[off*bps : (off+k)*bps])
					got.Feed(sig[off*bps : (off+k)*bps])
					if a, b := want.Take(), got.Take(); !bytes.Equal(a, b) {
						t.Fatalf("segment at %d, after source sample %d: %s", start, off+k, sampleDiff(a, b))
					}
					off += k
				}
				if a, b := want.Finish(), got.Finish(); !bytes.Equal(a, b) {
					t.Fatalf("segment at %d, the tail: %s", start, sampleDiff(a, b))
				}
				if want.Position() != got.Position() {
					t.Fatalf("segment at %d ends at %d, the formula's at %d", start, got.Position(), want.Position())
				}
			}
		})
	}
}

func sampleDiff(want, got []byte) string {
	if len(want) != len(got) {
		return fmt.Sprintf("%d bytes, the formula's %d", len(got), len(want))
	}
	for i := 0; i+1 < len(want); i += 2 {
		if want[i] != got[i] || want[i+1] != got[i+1] {
			return fmt.Sprintf("sample %d is %d, the formula's %d", i/2, int16(binary.LittleEndian.Uint16(got[i:])), int16(binary.LittleEndian.Uint16(want[i:])))
		}
	}
	return "equal"
}

// One op is one 20 ms source frame fed and taken, as the pump does it;
// "formula" is the per-tap reference, "table" the resampler.
func BenchmarkResampler(b *testing.B) {
	for _, c := range []struct {
		name     string
		from, to Format
	}{
		{"48k-16k", Format{Rate: 48000, Channels: 1}, Format{Rate: 16000, Channels: 1}},
		{"48k2-16k", Format{Rate: 48000, Channels: 2}, Format{Rate: 16000, Channels: 1}},
		{"16k-48k", Format{Rate: 16000, Channels: 1}, Format{Rate: 48000, Channels: 1}},
		{"24k-48k", Format{Rate: 24000, Channels: 1}, Format{Rate: 48000, Channels: 1}},
		{"44k1-16k", Format{Rate: 44100, Channels: 1}, Format{Rate: 16000, Channels: 1}},
		{"24k-44k1", Format{Rate: 24000, Channels: 1}, Format{Rate: 44100, Channels: 1}},
	} {
		frame := c.from.Rate / 50 * c.from.BytesPerSample()
		sig := goldenSignal(c.from, c.from.Rate, 1) // one second, looped
		run := func(b *testing.B, feed func([]byte), take func() []byte) {
			b.ReportAllocs()
			b.SetBytes(int64(frame))
			for i := 0; b.Loop(); i++ {
				off := i % 50 * frame
				feed(sig[off : off+frame])
				take()
			}
		}
		b.Run(c.name+"/formula", func(b *testing.B) {
			r := newRefResampler(c.from, c.to)
			run(b, r.Feed, r.Take)
		})
		b.Run(c.name+"/table", func(b *testing.B) {
			r := NewResampler(c.from, c.to)
			run(b, r.Feed, r.Take)
		})
	}
}
