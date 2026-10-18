//go:build !windows && (!darwin || ios)

package app

func readSystemVersion() string { return "" }
