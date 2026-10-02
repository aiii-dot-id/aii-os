//go:build darwin && !ios

package main

import (
	"bytes"
	"encoding/binary"

	"golang.org/x/sys/unix"
)

func processArgs() ([][]string, error) {
	kinfo, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil, err
	}
	var procs [][]string
	for _, k := range kinfo {
		raw, err := unix.SysctlRaw("kern.procargs2", int(k.Proc.P_pid))
		if err != nil || len(raw) < 4 {
			continue
		}
		if argv := procargs2(raw); len(argv) > 0 {
			procs = append(procs, argv)
		}
	}
	return procs, nil
}

func procargs2(raw []byte) []string {
	argc := int(binary.LittleEndian.Uint32(raw[:4]))
	rest := raw[4:]
	i := bytes.IndexByte(rest, 0)
	if i < 0 {
		return nil
	}
	rest = rest[i:]
	for len(rest) > 0 && rest[0] == 0 {
		rest = rest[1:]
	}
	argv := make([]string, 0, argc)
	for len(argv) < argc && len(rest) > 0 {
		j := bytes.IndexByte(rest, 0)
		if j < 0 {
			argv = append(argv, string(rest))
			break
		}
		argv = append(argv, string(rest[:j]))
		rest = rest[j+1:]
	}
	return argv
}
