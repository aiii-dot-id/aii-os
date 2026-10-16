package pluginhost

import (
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"runtime"
	"syscall"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/firewall"
)

const MaxDownloadAttempts = 5

type FetchStatusError struct{ Status int }

func (e *FetchStatusError) Error() string {
	return fmt.Sprintf("the server answered %d", e.Status)
}

type NetworkError struct{ Cause error }

func (e *NetworkError) Error() string { return e.Cause.Error() }
func (e *NetworkError) Unwrap() error { return e.Cause }

var errShortTransfer = errors.New("the transfer ended before the declared size")

var errNotDeclaredBytes = errors.New("the bytes are not the ones the package declares")

func transientDownload(err error) bool {
	if err == nil || diskFull(err) {
		return false
	}
	var se *FetchStatusError
	if errors.As(err, &se) {
		return se.Status == 408 || se.Status == 429 || se.Status >= 500
	}

	var ne *NetworkError
	return errors.As(err, &ne) || errors.Is(err, errShortTransfer) || errors.Is(err, io.ErrUnexpectedEOF)
}

func diskFull(err error) bool {
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return false
	}
	if errno == syscall.ENOSPC || errno == syscall.EDQUOT {
		return true
	}

	return runtime.GOOS == "windows" && (errno == 39 || errno == 112)
}

func downloadWait(n int) time.Duration {
	d := 5 * time.Second
	for i := 1; i < n && d < time.Minute; i++ {
		d *= 2
	}
	return min(d, time.Minute)
}

func spread(d time.Duration) time.Duration {
	q := d / 4
	if q <= 0 {
		return d
	}
	return d - q + time.Duration(rand.Int64N(int64(q)+1))
}

type DownloadError struct {
	Attempts int
	Cause    error
}

func (e *DownloadError) Error() string {
	if transientDownload(e.Cause) {
		return fmt.Sprintf("the download stopped after %d attempts in a row brought nothing: %s", e.Attempts, shortCause(e.Cause))
	}
	return "the download stopped: " + shortCause(e.Cause)
}

func (e *DownloadError) Unwrap() error { return e.Cause }

func (e *DownloadError) Final() string {
	cause := e.Cause
	var se *FetchStatusError
	var mm *ModelsMissingError
	switch {
	case transientDownload(cause):
		return fmt.Sprintf("%d attempts in a row brought nothing; check this machine's connection to the address it names, then Try again.", e.Attempts)
	case errors.As(cause, &se) && (se.Status == 404 || se.Status == 410):
		return "Nothing is at the address the package declares; its publisher must correct the release. Try again once they have, or install a corrected release."
	case errors.As(cause, &se) && (se.Status == 401 || se.Status == 403):
		return "The server refused the download, and refused it again when asked from the package's own address; Try again later, and if it still refuses, its publisher must correct the release."
	case errors.As(cause, &se):
		return fmt.Sprintf("The server refused the request (HTTP %d); Try again later, or install a corrected release.", se.Status)
	case errors.Is(cause, errNotDeclaredBytes):
		return "The server sent bytes that are not the ones the package declares, fetched whole; install a corrected release, or Try again."
	case diskFull(cause):
		return "The disk the plugin's files are written to is full; free space on it, then Try again."
	case errors.Is(cause, firewall.ErrEgressBlocked):
		return "This host's egress guard refuses the address the package declares; install a release whose files are at a public address."
	case errors.Is(cause, errNoDownloadPath) || errors.As(cause, &mm) && mm.Cause == nil:
		return "The machine service has no download path: place the files with their declared hashes at their declared paths in the identity's own plugins-models directory (it adopts them by digest), then Try again."
	}
	return "Mend what the error names, then Try again."
}

func parked(err error) bool {
	var final *DownloadError
	return materialWaitsOnPackage(err) || errors.As(err, &final)
}
