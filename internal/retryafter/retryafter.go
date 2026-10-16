package retryafter

import (
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const maxSeconds = math.MaxInt64 / int64(time.Second)

func Parse(h string, now time.Time) (time.Duration, bool) {
	v := strings.TrimSpace(h)
	if v == "" {
		return 0, false
	}
	var secs int64
	if strings.Trim(v, "0123456789") == "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return 0, false
		}
		secs = n
	} else {
		when, err := http.ParseTime(v)
		if err != nil {
			return 0, false
		}
		d := when.Sub(now)
		if d <= 0 {
			return 0, false
		}
		secs = int64(d / time.Second)
		if d%time.Second != 0 {
			secs++
		}
	}
	if secs > maxSeconds {
		return 0, false
	}
	return time.Duration(secs) * time.Second, true
}
