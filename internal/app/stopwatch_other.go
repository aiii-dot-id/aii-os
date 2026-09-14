//go:build !windows

package app

// .
// .
// .
// .
// .
func watchStopRequest() <-chan struct{} { return nil }
