//go:build linux && !android

package supervisor

import (
	"bytes"
	"os"
	"strconv"
)

func treeHeld(root int) (int64, bool) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0, false
	}
	parent := map[int]int{}
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		if ppid, ok := procParent(pid); ok {
			parent[pid] = ppid
		}
	}
	tree := descendants(root, parent)
	if len(tree) == 0 {
		return 0, false
	}
	var total int64
	for _, pid := range tree {
		held, ok := procHeld(pid)
		if !ok && pid == root {
			return 0, false
		}
		total += held
	}
	return total, true
}

func procParent(pid int) (int, bool) {
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0, false
	}
	end := bytes.LastIndexByte(raw, ')')
	if end < 0 {
		return 0, false
	}
	fields := bytes.Fields(raw[end+1:])
	if len(fields) < 2 {
		return 0, false
	}
	ppid, err := strconv.Atoi(string(fields[1]))
	return ppid, err == nil
}

func procHeld(pid int) (int64, bool) {
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/status")
	if err != nil {
		return 0, false
	}
	var total int64
	found := false
	for _, line := range bytes.Split(raw, []byte("\n")) {
		if !bytes.HasPrefix(line, []byte("RssAnon:")) && !bytes.HasPrefix(line, []byte("RssShmem:")) {
			continue
		}
		fields := bytes.Fields(line)
		if len(fields) < 2 {
			continue
		}
		kb, err := strconv.ParseInt(string(fields[1]), 10, 64)
		if err != nil {
			continue
		}
		total += kb << 10
		found = true
	}
	return total, found
}
