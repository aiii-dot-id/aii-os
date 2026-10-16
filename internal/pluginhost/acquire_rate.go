package pluginhost

import (
	"context"
	"io"
	"time"
)

const minRateSample = 1 << 20

type linkSample struct {
	bytes   int64
	elapsed time.Duration
}

func (q *Acquirer) timed(fetch ModelFetcher) ModelFetcher {
	if fetch == nil {
		return nil
	}
	return func(ctx context.Context, url string, offset int64, w io.Writer) (int64, error) {
		start := time.Now()
		n, err := fetch(ctx, url, offset, w)
		if took := time.Since(start); n > 0 && took > 0 {
			host := sourceHost(url)
			q.mu.Lock()
			s := q.links[host]
			s.bytes += n
			s.elapsed += took
			q.links[host] = s
			q.mu.Unlock()
		}
		return n, err
	}
}

func (q *Acquirer) linkRate(host string) (float64, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	s := q.links[host]
	if s.bytes < minRateSample || s.elapsed <= 0 {
		return 0, false
	}
	return float64(s.bytes) / s.elapsed.Seconds(), true
}
