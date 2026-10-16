//go:build (linux && !android) || (darwin && !ios) || windows

package pluginhost

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/firewall"
)

func GuardedFetch(ctx context.Context, url string, offset int64, w io.Writer) (int64, error) {
	return guardedFetchWith(ctx, firewall.FetchGuard, firewall.GuardedClient(30*time.Minute, nil, nil), url, offset, w)
}

func guardedFetchWith(ctx context.Context, guard func(context.Context, string) error, client *http.Client, url string, offset int64, w io.Writer) (int64, error) {
	if err := guard(ctx, url); err != nil {
		var dns *net.DNSError
		if errors.As(err, &dns) && ctx.Err() == nil {
			return 0, &NetworkError{Cause: err}
		}
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", "AII-OS/1.0 (model acquisition)")
	if offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil || errors.Is(err, firewall.ErrEgressBlocked) {
			return 0, err
		}
		return 0, &NetworkError{Cause: err}
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusPartialContent:
	case resp.StatusCode == http.StatusOK && offset > 0:

		return 0, fmt.Errorf("%w; the partial download will be discarded and refetched", ErrNoResume)
	case resp.StatusCode == http.StatusOK:
	default:
		return 0, &FetchStatusError{Status: resp.StatusCode}
	}
	local := &writeSide{w: w}
	n, err := io.Copy(local, resp.Body)
	if err != nil && local.err == nil && ctx.Err() == nil {
		return n, &NetworkError{Cause: err}
	}
	return n, err
}

type writeSide struct {
	w   io.Writer
	err error
}

func (s *writeSide) Write(p []byte) (int, error) {
	n, err := s.w.Write(p)
	if err != nil {
		s.err = err
	}
	return n, err
}
