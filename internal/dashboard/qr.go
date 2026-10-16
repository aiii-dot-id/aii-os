package dashboard

import (
	"fmt"
	"strings"

	"rsc.io/qr"
)

func qrSVG(text string) string {
	code, err := qr.Encode(text, qr.M)
	if err != nil {
		return ""
	}
	const quiet = 4
	n := code.Size + 2*quiet
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" shape-rendering="crispEdges" role="img" aria-label="A code to scan with the other device's camera"><rect width="%d" height="%d" fill="#fff"/><path fill="#000" d="`, n, n, n, n)
	for y := 0; y < code.Size; y++ {
		for x := 0; x < code.Size; x++ {
			if code.Black(x, y) {
				fmt.Fprintf(&b, "M%d %dh1v1h-1z", x+quiet, y+quiet)
			}
		}
	}
	b.WriteString(`"/></svg>`)
	return b.String()
}
