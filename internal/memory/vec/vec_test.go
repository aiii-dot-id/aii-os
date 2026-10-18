package vec

import (
	"math"
	"math/rand"
	"testing"
)

func TestQuantizeKeepsTheCosineWithinAPercent(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	for trial := 0; trial < 20; trial++ {
		dims := 64 + r.Intn(1500)
		a, b := make([]float32, dims), make([]float32, dims)
		for i := range a {
			a[i] = float32(r.NormFloat64())
			b[i] = float32(r.NormFloat64())*0.3 + a[i] // correlated with a
		}
		want := cosine(a, b)
		qa, sa := Quantize(a)
		qb, sb := Quantize(b)
		got, err := Cosine(qa, sa, qb, sb)
		if err != nil {
			t.Fatal(err)
		}
		if math.Abs(got-want) > 0.01 {
			t.Fatalf("dims %d: quantized cosine %v, float cosine %v", dims, got, want)
		}
		self, _ := Cosine(qa, sa, qa, sa)
		if self < 0.99 || self > 1 {
			t.Fatalf("a vector's cosine with itself = %v", self)
		}
		if d := Dequantize(qa, sa); len(d) != dims {
			t.Fatal("dequantize lost dimensions")
		}
	}
}

func TestDistancesRefuseAnotherBasis(t *testing.T) {
	qa, sa := Quantize([]float32{1, 0, 0})
	qb, sb := Quantize([]float32{0, 1})
	if _, err := Cosine(qa, sa, qb, sb); err == nil {
		t.Fatal("cosine across dimensions must be an error")
	}
	if _, err := L2(qa, sa, qb, sb); err == nil {
		t.Fatal("l2 across dimensions must be an error")
	}
	if _, err := Hamming(qa, qb); err == nil {
		t.Fatal("hamming across dimensions must be an error")
	}
}

func TestOrthogonalZeroAndHamming(t *testing.T) {
	qa, sa := Quantize([]float32{1, 0, 0, 0})
	qb, sb := Quantize([]float32{0, 1, 0, 0})
	if c, _ := Cosine(qa, sa, qb, sb); c != 0 {
		t.Fatalf("orthogonal cosine = %v", c)
	}
	if d, _ := L2(qa, sa, qb, sb); math.Abs(d-math.Sqrt2) > 0.02 {
		t.Fatalf("orthogonal unit vectors are √2 apart, got %v", d)
	}
	qz, sz := Quantize([]float32{0, 0, 0, 0})
	if sz != 1 || qz[0] != 0 {
		t.Fatalf("a zero vector quantizes to zeros with scale 1: %v %v", qz, sz)
	}
	// Signs: (+,-,+,-) against (+,+,-,-) differ in two dimensions.
	qc, _ := Quantize([]float32{1, -1, 1, -1})
	qd, _ := Quantize([]float32{1, 1, -1, -1})
	if h, _ := Hamming(qc, qd); h != 2 {
		t.Fatalf("hamming = %d, want 2", h)
	}
	// And a long vector crosses the 64-bit word boundary correctly.
	long1, long2 := make([]float32, 130), make([]float32, 130)
	for i := range long1 {
		long1[i], long2[i] = 1, 1
	}
	long2[0], long2[64], long2[129] = -1, -1, -1
	q1, _ := Quantize(long1)
	q2, _ := Quantize(long2)
	if h, _ := Hamming(q1, q2); h != 3 {
		t.Fatalf("hamming over 130 dims = %d, want 3", h)
	}
}

func TestTheCosineIsNormalisedByTheQuantizedNorms(t *testing.T) {
	const dims, floor = 202, 0.30
	cue := make([]float32, dims)
	cue[0] = 1
	// Lengthened: forty on the cue's axis (39.6 rounded up) and a hundred
	// small entries that round up from 0.6 to one.
	long := make([]float32, dims)
	long[0], long[1] = 39.6, 127
	for i := 2; i < 102; i++ {
		long[i] = 0.6
	}
	// Shortened: forty (40.4 rounded down) and a hundred entries that
	// round down from 0.45 to nothing.
	short := make([]float32, dims)
	short[0], short[1] = 40.4, 127
	for i := 102; i < 202; i++ {
		short[i] = 0.45
	}
	qc, sc := Quantize(cue)
	for _, tc := range []struct {
		name  string
		x     []float32
		above bool // the normalised cosine is at or above the floor
	}{{"lengthened", long, false}, {"shortened", short, true}} {
		q, s := Quantize(tc.x)
		var dot, nc, nq int64
		for i := range q {
			a, b := int64(int8(qc[i])), int64(int8(q[i]))
			dot, nc, nq = dot+a*b, nc+a*a, nq+b*b
		}
		scaled := float64(dot) * sc * s
		normalised := float64(dot) / math.Sqrt(float64(nc)*float64(nq))
		if (scaled >= floor) == (normalised >= floor) {
			t.Fatalf("%s: rig: the scaled %.5f and the normalised %.5f do not straddle %.2f", tc.name, scaled, normalised, floor)
		}
		got, err := Cosine(qc, sc, q, s)
		if err != nil {
			t.Fatal(err)
		}
		if math.Abs(got-normalised) > 1e-12 || (got >= floor) != tc.above {
			t.Fatalf("%s: cosine %.5f; the normalised cosine is %.5f and the dot product scaled back %.5f (float %.5f)", tc.name, got, normalised, scaled, cosine(cue, tc.x))
		}
		if self, _ := Cosine(q, s, q, s); self != 1 {
			t.Fatalf("%s: a quantized vector's cosine with itself = %v, want exactly 1", tc.name, self)
		}
	}
}

func cosine(a, b []float32) float64 {
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	return dot / math.Sqrt(na*nb)
}

// A NUMBER THAT IS NOT ONE MEETS NO BOUND. The float cosine of a vector
// holding NaN is NaN, which is neither at least a bound nor below it; a
// caller asks !(cos >= bound). The values it computes are pinned by
// internal/pluginhost/testdata/validation_checks.json, which the kit
// mirrors.
func TestTheFloatCosineOfNoNumberMeetsNoBound(t *testing.T) {
	cos := FloatCosine([]float32{float32(math.NaN()), 0}, []float64{1, 0})
	if cos >= 0.5 || !math.IsNaN(cos) {
		t.Fatalf("a vector holding NaN came back at %v", cos)
	}
}
