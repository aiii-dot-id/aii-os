package audio

import (
	"encoding/binary"
	"math"
)

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
type Resampler struct {
	from, to Format
	same     bool
	ratio    float64
	scale    float64
	half     int

	hist      []float64
	histStart int64
	inPos     int64
	outPos    int64
}

// .
func NewResampler(from, to Format) *Resampler {
	r := &Resampler{from: from, to: to, same: from.Rate == to.Rate}
	r.ratio = float64(to.Rate) / float64(from.Rate)
	r.scale = 1
	if r.ratio < 1 {
		r.scale = 1 / r.ratio
	}
	r.half = int(math.Ceil(8 * r.scale))
	return r
}

// .
func (r *Resampler) TargetPos(src int64) int64 {
	if r.same {
		return src
	}
	return (src*int64(r.to.Rate) + int64(r.from.Rate) - 1) / int64(r.from.Rate)
}

// .
func (r *Resampler) Position() int64 { return r.outPos }

// .
// .
func (r *Resampler) Reset(src int64) {
	r.hist = r.hist[:0]
	r.histStart, r.inPos = src, src
	r.outPos = r.TargetPos(src)
}

// .
func (r *Resampler) Feed(pcm []byte) {
	n := len(pcm) / r.from.BytesPerSample()
	if r.same {
		r.inPos += int64(n)
		r.hist = append(r.hist, r.mono(pcm, n)...)
		return
	}
	r.hist = append(r.hist, r.mono(pcm, n)...)
	r.inPos += int64(n)
}

// .
func (r *Resampler) mono(pcm []byte, n int) []float64 {
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

// .
// .
func (r *Resampler) Take() []byte { return r.produce(false) }

// .
// .
// .
func (r *Resampler) Finish() []byte { return r.produce(true) }

func (r *Resampler) produce(flush bool) []byte {
	var vals []float64
	if r.same {
		vals = append(vals, r.hist...)
		r.hist = r.hist[:0]
		r.histStart = r.inPos
		r.outPos += int64(len(vals))
		return r.encode(vals)
	}
	limit := r.TargetPos(r.inPos)
	for r.outPos < limit {
		t := float64(r.outPos) / r.ratio
		center := int64(math.Floor(t))
		if !flush && center+int64(r.half) >= r.inPos {
			break
		}
		var acc, wsum float64
		for k := center - int64(r.half); k <= center+int64(r.half)+1; k++ {
			u := (float64(k) - t) / r.scale
			if u <= -8 || u >= 8 {
				continue
			}
			w := sinc(u) * blackman(u/8)
			wsum += w
			if k < r.histStart || k >= r.inPos {
				continue
			}
			acc += w * r.hist[k-r.histStart]
		}
		if wsum != 0 {
			acc /= wsum
		}
		vals = append(vals, acc)
		r.outPos++
	}
	// .
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

func (r *Resampler) encode(vals []float64) []byte {
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

func sinc(x float64) float64 {
	if x == 0 {
		return 1
	}
	px := math.Pi * x
	return math.Sin(px) / px
}

// .
func blackman(x float64) float64 {
	if x <= -1 || x >= 1 {
		return 0
	}
	return 0.42 + 0.5*math.Cos(math.Pi*x) + 0.08*math.Cos(2*math.Pi*x)
}
