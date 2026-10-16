package main

import (
	"image"
	"image/color"
	"math"
)

type fimg struct {
	w, h int
	p    []float64
}

func newF(w, h int) *fimg { return &fimg{w: w, h: h, p: make([]float64, w*h*4)} }

func fromNRGBA(src *image.NRGBA) *fimg {
	b := src.Bounds()
	f := newF(b.Dx(), b.Dy())
	for y := 0; y < f.h; y++ {
		for x := 0; x < f.w; x++ {
			c := src.NRGBAAt(b.Min.X+x, b.Min.Y+y)
			a := float64(c.A) / 255
			i := (y*f.w + x) * 4
			f.p[i] = float64(c.R) / 255 * a
			f.p[i+1] = float64(c.G) / 255 * a
			f.p[i+2] = float64(c.B) / 255 * a
			f.p[i+3] = a
		}
	}
	return f
}

func (f *fimg) nrgba() *image.NRGBA {
	out := image.NewNRGBA(image.Rect(0, 0, f.w, f.h))
	for y := 0; y < f.h; y++ {
		for x := 0; x < f.w; x++ {
			i := (y*f.w + x) * 4
			a := clamp01(f.p[i+3])
			if a <= 0 {
				continue
			}
			out.SetNRGBA(x, y, color.NRGBA{
				R: to8(clamp01(f.p[i] / a)), G: to8(clamp01(f.p[i+1] / a)), B: to8(clamp01(f.p[i+2] / a)), A: to8(a),
			})
		}
	}
	return out
}

func clamp01(v float64) float64 { return math.Max(0, math.Min(1, v)) }
func to8(v float64) uint8       { return uint8(math.Floor(v*255 + 0.5)) }

func lanczos3(x float64) float64 {
	x = math.Abs(x)
	if x < 1e-9 {
		return 1
	}
	if x >= 3 {
		return 0
	}
	px := math.Pi * x
	return float64(3*math.Sin(px)*math.Sin(px/3)) / float64(px*px)
}

type tap struct {
	i int
	w float64
}

func weights(src, n int) [][]tap {
	scale := float64(src) / float64(n)
	support := 3.0
	if scale > 1 {
		support *= scale
	}
	out := make([][]tap, n)
	for o := 0; o < n; o++ {
		center := (float64(o) + 0.5) * scale
		lo := int(math.Floor(center - support))
		hi := int(math.Ceil(center + support))
		var ts []tap
		sum := 0.0
		for i := lo; i <= hi; i++ {
			if i < 0 || i >= src {
				continue
			}
			d := (float64(i) + 0.5 - center)
			if scale > 1 {
				d /= scale
			}
			w := lanczos3(d)
			if w == 0 {
				continue
			}
			ts = append(ts, tap{i, w})
			sum += w
		}
		for k := range ts {
			ts[k].w /= sum
		}
		out[o] = ts
	}
	return out
}

func (f *fimg) resize(w, h int) *fimg {
	wx := weights(f.w, w)
	mid := newF(w, f.h)
	for y := 0; y < f.h; y++ {
		row := f.p[y*f.w*4:]
		for x := 0; x < w; x++ {
			var r, g, b, a float64
			for _, t := range wx[x] {
				j := t.i * 4
				r += float64(row[j] * t.w)
				g += float64(row[j+1] * t.w)
				b += float64(row[j+2] * t.w)
				a += float64(row[j+3] * t.w)
			}
			i := (y*w + x) * 4
			mid.p[i], mid.p[i+1], mid.p[i+2], mid.p[i+3] = r, g, b, a
		}
	}
	wy := weights(f.h, h)
	out := newF(w, h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var r, g, b, a float64
			for _, t := range wy[y] {
				j := (t.i*w + x) * 4
				r += float64(mid.p[j] * t.w)
				g += float64(mid.p[j+1] * t.w)
				b += float64(mid.p[j+2] * t.w)
				a += float64(mid.p[j+3] * t.w)
			}

			a = clamp01(a)
			i := (y*w + x) * 4
			out.p[i], out.p[i+1], out.p[i+2], out.p[i+3] = math.Min(clamp01(r), a), math.Min(clamp01(g), a), math.Min(clamp01(b), a), a
		}
	}
	return out
}

func (dst *fimg) over(src *fimg, ox, oy int) {
	for y := 0; y < src.h; y++ {
		dy := y + oy
		if dy < 0 || dy >= dst.h {
			continue
		}
		for x := 0; x < src.w; x++ {
			dx := x + ox
			if dx < 0 || dx >= dst.w {
				continue
			}
			s := (y*src.w + x) * 4
			d := (dy*dst.w + dx) * 4
			k := 1 - src.p[s+3]
			for c := 0; c < 4; c++ {
				dst.p[d+c] = src.p[s+c] + float64(dst.p[d+c]*k)
			}
		}
	}
}

const masterDisc = 1060.0

func disc(dst *fimg, m *fimg, d float64) {
	n := int(math.Round(d * float64(m.w) / masterDisc))
	if (dst.w-n)%2 != 0 {
		n++
	}
	dst.over(m.resize(n, n), (dst.w-n)/2, (dst.h-n)/2)
}

var (
	nightTop    = [3]float64{22, 18, 52}
	nightBottom = [3]float64{10, 9, 22}
	violet      = [3]float64{139, 108, 255}
)

func night(t float64) [3]float64 {
	t = clamp01(t)
	t = t * t * (3 - 2*t)
	var c [3]float64
	for i := range c {
		c[i] = (nightTop[i] + (nightBottom[i]-nightTop[i])*t) / 255
	}
	return c
}

func ground(size int, glowR float64, cover func(x, y float64) float64) *fimg {
	f := newF(size, size)
	c := float64(size) / 2
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			px, py := float64(x)+0.5, float64(y)+0.5
			a := 1.0
			if cover != nil {
				a = cover(px, py)
			}
			if a <= 0 {
				continue
			}
			col := night(py / float64(size))
			d := math.Hypot(px-c, py-c) / glowR
			if d < 1.6 {
				g := 0.22 * math.Exp(-math.Pow(math.Max(0, d-0.75)/0.35, 2))
				for i := range col {
					col[i] += (violet[i]/255 - col[i]) * g
				}
			}
			i := (y*size + x) * 4
			f.p[i], f.p[i+1], f.p[i+2], f.p[i+3] = col[0]*a, col[1]*a, col[2]*a, a
		}
	}
	return f
}

func roundedSquare(c, h, r float64) func(x, y float64) float64 {
	return func(x, y float64) float64 {
		qx, qy := math.Abs(x-c)-(h-r), math.Abs(y-c)-(h-r)
		out := math.Hypot(math.Max(qx, 0), math.Max(qy, 0)) + math.Min(math.Max(qx, qy), 0) - r
		return clamp01(0.5 - out)
	}
}

func free(m *fimg, size int) *image.NRGBA {
	if size == m.w {
		return m.nrgba()
	}
	return m.resize(size, size).nrgba()
}

func plate(m *fimg, size int) *image.NRGBA {
	s := float64(size) / 1024
	c := float64(size) / 2
	body := roundedSquare(c, 412*s, 185.4*s)
	out := newF(size, size)

	sh := newF(size, size)
	off := 10 * s
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			a := body(float64(x)+0.5, float64(y)+0.5-off)
			sh.p[(y*size+x)*4+3] = 0.32 * a
		}
	}
	blurAlpha(sh, 14*s)
	out.over(sh, 0, 0)
	out.over(ground(size, 360*s, body), 0, 0)
	disc(out, m, 690*s)
	return out.nrgba()
}

func opaque(m *fimg, size int) *image.NRGBA {
	s := float64(size) / 1024
	out := ground(size, 420*s, nil)
	disc(out, m, 820*s)
	return out.nrgba()
}

func foreground(m *fimg, size int) *image.NRGBA {
	out := newF(size, size)
	disc(out, m, float64(size)*67/108)
	return out.nrgba()
}

func blurAlpha(f *fimg, sigma float64) {
	r := int(math.Round(sigma * 0.87))
	if r < 1 {
		return
	}
	a := make([]float64, f.w*f.h)
	for i := range a {
		a[i] = f.p[i*4+3]
	}
	tmp := make([]float64, len(a))
	for pass := 0; pass < 3; pass++ {
		box(a, tmp, f.w, f.h, r, true)
		box(tmp, a, f.w, f.h, r, false)
	}
	for i := range a {
		f.p[i*4+3] = a[i]
	}
}

func box(src, dst []float64, w, h, r int, horizontal bool) {
	n, m := w, h
	if !horizontal {
		n, m = h, w
	}
	at := func(line, k int) int {
		if horizontal {
			return line*w + k
		}
		return k*w + line
	}
	span := float64(2*r + 1)
	for line := 0; line < m; line++ {
		sum := 0.0
		for k := -r; k <= r; k++ {
			if k >= 0 && k < n {
				sum += src[at(line, k)]
			}
		}
		for k := 0; k < n; k++ {
			dst[at(line, k)] = sum / span
			if k-r >= 0 {
				sum -= src[at(line, k-r)]
			}
			if k+r+1 < n {
				sum += src[at(line, k+r+1)]
			}
		}
	}
}
