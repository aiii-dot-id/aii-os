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
			b[i] = float32(r.NormFloat64())*0.3 + a[i]
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
	// .
	qc, _ := Quantize([]float32{1, -1, 1, -1})
	qd, _ := Quantize([]float32{1, 1, -1, -1})
	if h, _ := Hamming(qc, qd); h != 2 {
		t.Fatalf("hamming = %d, want 2", h)
	}
	// .
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

func cosine(a, b []float32) float64 {
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	return dot / math.Sqrt(na*nb)
}
