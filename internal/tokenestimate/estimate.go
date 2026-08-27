// Package tokenestimate owns the tokenizer-free fallback used where an exact
// provider counter is unavailable.
package tokenestimate

// Estimate uses the canon default of three UTF-8 bytes per token, rounded up.
func Estimate(text string) int {
	return (len(text) + 2) / 3
}
