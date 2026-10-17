//go:build !android && !ios

package app

func init() {
	harnessLane = func() (string, []string, error) { return "", nil, nil }
}
