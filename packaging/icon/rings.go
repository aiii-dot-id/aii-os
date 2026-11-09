package main

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"strings"
)

const (
	svgPath   = "internal/dashboard/static/icon.svg"
	touchPath = "internal/dashboard/static/apple-touch-icon.png"
	touchSize = 180
)

var (
	ringRadii  = []float64{24, 17, 10}
	ringWidths = []float64{2.5, 4, 2.5}
	dotRadius  = 4.0
	paper      = color.NRGBA{0xf5, 0xf2, 0xec, 0xff}
	accent     = color.NRGBA{0x98, 0x52, 0x1d, 0xff}
)

func hex(c color.NRGBA) string { return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B) }

func ringsSVG() []byte {
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64">`)
	fmt.Fprintf(&b, `<rect width="64" height="64" rx="14" fill="%s"/>`, hex(paper))
	fmt.Fprintf(&b, `<g fill="none" stroke="%s">`, hex(accent))
	for i, r := range ringRadii {
		fmt.Fprintf(&b, `<circle cx="32" cy="32" r="%g" stroke-width="%g"/>`, r, ringWidths[i])
	}
	fmt.Fprintf(&b, `</g><circle cx="32" cy="32" r="%g" fill="%s"/></svg>`+"\n", dotRadius, hex(accent))
	return []byte(b.String())
}

func inked(x, y float64) bool {
	d := math.Hypot(x-32, y-32)
	if d <= dotRadius {
		return true
	}
	for i, r := range ringRadii {
		if math.Abs(d-r) <= ringWidths[i]/2 {
			return true
		}
	}
	return false
}

func ringsPNG(size int) *image.Paletted {
	const steps, grid = 32, 4
	palette := make(color.Palette, steps)
	for i := range palette {
		mix := func(a, b uint8) uint8 {
			return uint8(math.Round(float64(a) + (float64(b)-float64(a))*float64(i)/(steps-1)))
		}
		palette[i] = color.NRGBA{mix(paper.R, accent.R), mix(paper.G, accent.G), mix(paper.B, accent.B), 0xff}
	}
	img := image.NewPaletted(image.Rect(0, 0, size, size), palette)
	scale := 64 / float64(size)
	for py := 0; py < size; py++ {
		for px := 0; px < size; px++ {
			hits := 0
			for sy := 0; sy < grid; sy++ {
				for sx := 0; sx < grid; sx++ {
					if inked((float64(px)+(float64(sx)+0.5)/grid)*scale, (float64(py)+(float64(sy)+0.5)/grid)*scale) {
						hits++
					}
				}
			}
			img.SetColorIndex(px, py, uint8(math.Round(float64(hits)/(grid*grid)*(steps-1))))
		}
	}
	return img
}
