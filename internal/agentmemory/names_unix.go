//go:build !windows

package agentmemory

import (
	"fmt"
	"os"
	"syscall"
)

const openNonblock = syscall.O_NONBLOCK

func names(f *os.File) (uint64, error) {
	fi, err := f.Stat()
	if err != nil {
		return 0, err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, fmt.Errorf("%s: no link count in its stat", f.Name())
	}
	return uint64(st.Nlink), nil
}
