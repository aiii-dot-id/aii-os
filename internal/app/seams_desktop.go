//go:build !android && !ios

package app

var harnessLane func() (string, []string, error)
