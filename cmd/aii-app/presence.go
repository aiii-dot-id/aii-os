//go:build (darwin && !ios) || (linux && !android)

package main

import (
	"fmt"
	"path/filepath"
	"strings"
)

var processTable = processArgs

func slotRunning(slotDir string) (bool, error) {
	procs, err := processTable()
	if err != nil {
		return false, fmt.Errorf("read the process table: %w", err)
	}
	return runsSlot(procs, slotDir), nil
}

func runsSlot(procs [][]string, slotDir string) bool {
	want := filepath.Clean(slotDir)
	for _, argv := range procs {
		if len(argv) == 0 || filepath.Base(argv[0]) != "aii" {
			continue
		}
		for i := 1; i < len(argv); i++ {
			name, value, inline := strings.Cut(argv[i], "=")
			if name != "-dir" && name != "--dir" {
				continue
			}
			if !inline {
				if i+1 >= len(argv) {
					break
				}
				value = argv[i+1]
			}
			if filepath.Clean(value) == want {
				return true
			}
		}
	}
	return false
}
