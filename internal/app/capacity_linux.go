//go:build linux

package app

import (
	"bufio"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/pluginfacility"
)

// .
// .
// .
// .
type hostCapacity struct{}

func (hostCapacity) Measure() pluginfacility.Availability {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return pluginfacility.Availability{}
	}
	defer f.Close()
	return parseMeminfo(f)
}

// .
func parseMeminfo(f io.Reader) pluginfacility.Availability {
	// .
	// .
	// .
	// .
	total, avail := int64(0), int64(-1)
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 {
			continue
		}
		kb, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			continue
		}
		switch fields[0] {
		case "MemTotal:":
			total = kb * 1024
		case "MemAvailable:":
			avail = kb * 1024
		}
	}
	if total <= 0 || avail < 0 {
		return pluginfacility.Availability{}
	}
	return pluginfacility.Availability{HostKnown: true, HostTotal: total, HostAvailable: avail}
}
