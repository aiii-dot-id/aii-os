//go:build !windows && !linux

package main

import "fmt"

// reachPlatform: this platform has no attempt of its own; what its wall
// refuses is tried by the attempts every Unix shares.
func reachPlatform(key, value string) { fmt.Printf("%s=unknown\n", key) }
