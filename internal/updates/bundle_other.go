//go:build !darwin

package updates

// .
// .
// .
// .
// .

func applyIfBundle(string, []byte) (bool, error) { return false, nil }
