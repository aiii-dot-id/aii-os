//go:build linux && !android

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
)

func processArgs() ([][]string, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	var procs [][]string
	for _, e := range entries {
		if !e.IsDir() || strings.TrimLeft(e.Name(), "0123456789") != "" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join("/proc", e.Name(), "cmdline"))
		if err != nil || len(raw) == 0 {
			continue
		}
		var argv []string
		for _, a := range bytes.Split(bytes.TrimSuffix(raw, []byte{0}), []byte{0}) {
			argv = append(argv, string(a))
		}
		procs = append(procs, argv)
	}
	return procs, nil
}
