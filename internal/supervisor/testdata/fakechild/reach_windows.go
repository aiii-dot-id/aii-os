//go:build windows

package main

import "fmt"

// reachProbe: the Windows wall is tried from inside by the supervisor's
// own AppContainer tests, with a child built for them.
func reachProbe(args []string) { fmt.Println("reach=unsupported") }

// carrierProbe: the Windows wall grants a child its runtime root to read
// and execute by the AppContainer's own rules, tried by the supervisor's
// tests.
func carrierProbe() string { return "unsupported" }
