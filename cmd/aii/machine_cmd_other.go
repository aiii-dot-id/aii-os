//go:build !((linux && !android) || (darwin && !ios) || windows)

package main

import (
	"fmt"
	"io"
)

func runMachine(_ []string, _, stderr io.Writer) int {
	fmt.Fprintln(stderr, "machine: there is no machine service on this platform")
	return 1
}

func registerMachineService(io.Writer)            {}
func recordWithMachine(string, string, io.Writer) {}
func forgetWithMachine(string, string, io.Writer) {}
func retireMachineIfLast(io.Writer, io.Writer)    {}
