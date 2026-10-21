//go:build linux || android

package tools

import (
	"bytes"
	"os"
	"strconv"
)

func processGroups() map[int]map[int]uint64 {
	dir, err := os.Open("/proc")
	if err != nil {
		return nil
	}
	names, _ := dir.Readdirnames(-1)
	dir.Close()
	groups := make(map[int]map[int]uint64)
	for _, name := range names {
		pid, err := strconv.Atoi(name)
		if err != nil {
			continue
		}
		stat, err := os.ReadFile("/proc/" + name + "/stat")
		if err != nil {
			continue
		}
		pgid, started, live := procStat(stat)
		if !live {
			continue
		}
		if groups[pgid] == nil {
			groups[pgid] = make(map[int]uint64)
		}
		groups[pgid][pid] = started
	}
	return groups
}

func procStat(stat []byte) (pgid int, started uint64, live bool) {
	name := bytes.LastIndexByte(stat, ')')
	if name < 0 {
		return 0, 0, false
	}
	f := bytes.Fields(stat[name+1:])
	if len(f) < 20 || f[0][0] == 'Z' || f[0][0] == 'X' {
		return 0, 0, false
	}
	pgid, err := strconv.Atoi(string(f[2]))
	if err != nil {
		return 0, 0, false
	}
	started, err = strconv.ParseUint(string(f[19]), 10, 64)
	return pgid, started, err == nil
}
