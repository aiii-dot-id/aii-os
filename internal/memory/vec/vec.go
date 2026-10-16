package vec

import (
	"fmt"
	"math"
	"math/bits"
)

func Quantize(x []float32) (q []byte, scale float64) {
	q = make([]byte, len(x))
	var norm float64
	for _, v := range x {
		norm += float64(v) * float64(v)
	}
	norm = math.Sqrt(norm)
	if norm == 0 {
		return q, 1
	}
	var peak float64
	for _, v := range x {
		if a := math.Abs(float64(v) / norm); a > peak {
			peak = a
		}
	}
	scale = peak / 127
	if scale == 0 {
		return q, 1
	}
	for i, v := range x {
		r := math.Round(float64(v) / norm / scale)
		if r > 127 {
			r = 127
		}
		if r < -127 {
			r = -127
		}
		q[i] = byte(int8(r))
	}
	return q, scale
}

func Dequantize(q []byte, scale float64) []float32 {
	out := make([]float32, len(q))
	for i, b := range q {
		out[i] = float32(float64(int8(b)) * scale)
	}
	return out
}

func Cosine(a []byte, _ float64, b []byte, _ float64) (float64, error) {
	if len(a) != len(b) {
		return 0, fmt.Errorf("vec: dimensions differ (%d vs %d)", len(a), len(b))
	}
	var dot, na, nb int64
	for i := range a {
		x, y := int64(int8(a[i])), int64(int8(b[i]))
		dot, na, nb = dot+x*y, na+x*x, nb+y*y
	}
	if na == 0 || nb == 0 {
		return 0, nil
	}
	c := float64(dot) / math.Sqrt(float64(na)*float64(nb))
	if c > 1 {
		c = 1
	}
	if c < -1 {
		c = -1
	}
	return c, nil
}

func FloatCosine(got []float32, ref []float64) float64 {
	if len(got) != len(ref) || len(got) == 0 {
		return 0
	}

	var dot, ng, nr float64
	for i := range got {
		g := float64(got[i])
		dot += float64(g * ref[i])
		ng += float64(g * g)
		nr += float64(ref[i] * ref[i])
	}
	if ng == 0 || nr == 0 {
		return 0
	}
	return dot / (math.Sqrt(ng) * math.Sqrt(nr))
}

func L2(a []byte, sa float64, b []byte, sb float64) (float64, error) {
	if len(a) != len(b) {
		return 0, fmt.Errorf("vec: dimensions differ (%d vs %d)", len(a), len(b))
	}
	var sum float64
	for i := range a {
		d := float64(int8(a[i]))*sa - float64(int8(b[i]))*sb
		sum += d * d
	}
	return math.Sqrt(sum), nil
}

func Hamming(a, b []byte) (int, error) {
	if len(a) != len(b) {
		return 0, fmt.Errorf("vec: dimensions differ (%d vs %d)", len(a), len(b))
	}
	var n int
	var word uint64
	var filled uint
	for i := range a {
		if (a[i]^b[i])&0x80 != 0 {
			word |= 1 << filled
		}
		filled++
		if filled == 64 {
			n += bits.OnesCount64(word)
			word, filled = 0, 0
		}
	}
	n += bits.OnesCount64(word)
	return n, nil
}
