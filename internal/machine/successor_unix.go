//go:build (linux && !android) || (darwin && !ios)

package machine

func AccountSuccessor(string) func() error { return nil }
