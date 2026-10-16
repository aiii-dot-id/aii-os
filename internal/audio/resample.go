package audio

import (
	"encoding/binary"
	"math"
)

type Resampler struct {
	from, to Format

	channels []*Resampler
	same     bool
	ratio    float64
	scale    float64
	half     int

	hist      []float64
	histStart int64
	inPos     int64
	outPos    int64

	vals  []float64
	split []byte

	phases []phase
}

type phase struct {
	f      float64
	w      []float64
	lo, hi int
	wsum   float64
}

const maxKernelWeights = 1 << 16

func NewResampler(from, to Format) *Resampler {
	r := &Resampler{from: from, to: to, same: from.Rate == to.Rate}
	if from.Channels == to.Channels && from.Channels > 1 {
		for i := 0; i < from.Channels; i++ {
			r.channels = append(r.channels, NewResampler(Format{Rate: from.Rate, Channels: 1}, Format{Rate: to.Rate, Channels: 1}))
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

func (r *Resampler) TargetPos(src int64) int64 {
	if r.same {
		return src
	}
	return (src*int64(r.to.Rate) + int64(r.from.Rate) - 1) / int64(r.from.Rate)
}

func (r *Resampler) Position() int64 { return r.outPos }

func (r *Resampler) Reset(src int64) {
	for _, ch := range r.channels {
		ch.Reset(src)
	}
	r.hist = r.hist[:0]
	r.histStart, r.inPos = src, src
	r.outPos = r.TargetPos(src)
}

func (r *Resampler) Feed(pcm []byte) {
	n := len(pcm) / r.from.BytesPerSample()
	if len(r.channels) != 0 {
		for c, ch := range r.channels {
			r.split = r.split[:0]
			for i := 0; i < n; i++ {
				r.split = append(r.split, pcm[(i*len(r.channels)+c)*2:][:2]...)
			}
			ch.Feed(r.split)
		}
		r.inPos += int64(n)
		return
	}

	ch := r.from.Channels
	for i := 0; i < n; i++ {
		var acc float64
		for c := 0; c < ch; c++ {
			acc += float64(int16(binary.LittleEndian.Uint16(pcm[(i*ch+c)*2:])))
		}
		r.hist = append(r.hist, acc/float64(ch)/32768)
	}
	r.inPos += int64(n)
}

func (r *Resampler) Take() []byte { return r.produce(false) }

func (r *Resampler) Finish() []byte { return r.produce(true) }

func (r *Resampler) produce(flush bool) []byte {
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
	if r.same {
		out := r.encode(r.hist)
		r.outPos += int64(len(r.hist))
		r.hist = r.hist[:0]
		r.histStart = r.inPos
		return out
	}
	vals := r.vals[:0]
	limit := r.TargetPos(r.inPos)
	for r.outPos < limit {
		t := float64(r.outPos) / r.ratio
		center := int64(math.Floor(t))
		if !flush && center+int64(r.half) >= r.inPos {
			break
		}

		first := center - int64(r.half)
		ph := r.kernel(t, center)
		var acc float64
		for j := max(int64(ph.lo), r.histStart-first); j < min(int64(ph.hi), r.inPos-first); j++ {
			acc += ph.w[j] * r.hist[first+j-r.histStart]
		}
		if ph.wsum != 0 {
			acc /= ph.wsum
		}
		vals = append(vals, acc)
		r.outPos++
	}
	r.vals = vals

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

func (r *Resampler) kernel(t float64, center int64) *phase {
	if r.phases == nil {
		g, b := int64(r.from.Rate), int64(r.to.Rate)
		for b != 0 {
			g, b = b, g%b
		}
		n := int64(r.to.Rate) / g
		if n*int64(2*r.half+2) > maxKernelWeights {
			n = 1
		}
		r.phases = make([]phase, n)
	}
	n := int64(len(r.phases))
	ph := &r.phases[(r.outPos%n+n)%n]
	f := t - float64(center)
	if t < 0 {
		f = math.NaN()
	}
	if ph.w != nil && ph.f == f {
		return ph
	}
	if ph.w == nil {
		ph.w = make([]float64, 2*r.half+2)
	}
	ph.f, ph.lo, ph.hi, ph.wsum = f, len(ph.w), 0, 0
	for j := range ph.w {
		u := (float64(center+int64(j-r.half)) - t) / r.scale
		if u <= -8 || u >= 8 {
			continue
		}
		w := sinc(u) * blackman(u/8)
		ph.w[j] = w
		ph.wsum += w
		ph.lo, ph.hi = min(ph.lo, j), j+1
	}
	return ph
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

func blackman(x float64) float64 {
	if x <= -1 || x >= 1 {
		return 0
	}
	return 0.42 + 0.5*math.Cos(math.Pi*x) + 0.08*math.Cos(2*math.Pi*x)
}
