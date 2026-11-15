package updates

import (
	"errors"
	"fmt"
	neturl "net/url"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/atomicfile"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
)

var errNoRelease = errors.New("no release published yet (or the repository does not exist)")

const DefaultRepo = "aiii-dot-id/aii-os"

func validRepo(s string) bool {
	slash := strings.IndexByte(s, '/')
	if slash <= 0 || slash == len(s)-1 {
		return false
	}
	if strings.IndexByte(s[slash+1:], '/') >= 0 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		ok := c == '/' || c == '-' || c == '_' || c == '.' ||
			(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
		if !ok {
			return false
		}
	}
	return true
}

const DefaultAPIURL = "https://api.github.com"

func APIBase(raw string) (*neturl.URL, error) {
	if raw == "" {
		raw = DefaultAPIURL
	}
	u, err := neturl.Parse(raw)
	switch {
	case err != nil:
		return nil, fmt.Errorf("%q is not a URL: %v", raw, err)
	case u.Scheme != "https":
		return nil, fmt.Errorf("%q is not https: the release API must be reached over https", raw)
	case u.Opaque != "" || u.Hostname() == "":
		return nil, fmt.Errorf("%q names no host", raw)
	case u.User != nil:
		return nil, fmt.Errorf("%q carries credentials: the release API is read without them", raw)
	case u.RawQuery != "" || u.ForceQuery || u.Fragment != "":
		return nil, fmt.Errorf("%q has a query or a fragment: write the server's address, and a path if it serves the API under one", raw)
	}
	u.Path, u.RawPath = strings.TrimRight(u.Path, "/"), strings.TrimRight(u.RawPath, "/")
	return u, nil
}

const (
	DefaultCheckIntervalMinutes = 60
	MinCheckIntervalMinutes     = 15
	MaxCheckIntervalMinutes     = 1440
)

const downloadTimeout = 10 * time.Minute

const metadataTimeout = 30 * time.Second

const maxDownloadSize = 256 * 1024 * 1024

type releaseArchivePayload = ReleasePayload

const artifactKindReleaseSig = packagefmt.ArtifactKindHostRelease

const artifactKindEvidenceSig = "release.evidence_bundle"

var ErrReleaseSourceChanged = errors.New("the release source changed since the check; check again")

var ErrUpdatePending = errors.New("update already installed — restart to complete it before another update can be applied")

var ErrAlreadyApplying = errors.New("an update is already being applied — wait for that attempt to finish")

var publishBeside = func(aside bool, tmp, path string) (published bool, err error) {
	if aside {
		return atomicfile.ReplaceExecutable(tmp, path)
	}
	return atomicfile.Replace(tmp, path)
}

var (
	ErrCheckingOff     = errors.New("update checking is off for this build (no release version)")
	ErrAlreadyChecking = errors.New("already checking")
	ErrSafeMode        = errors.New("in SAFE mode — no outside-world operations")
)

const checkNowTimeout = downloadTimeout + 5*time.Minute

var ErrStopping = errors.New("this identity is stopping")

var ErrReleaseListBehind = errors.New("the release withdrawal list announced is not on this machine")

var replaceStep = func(string) {}

const (
	markerFile = ".boot_completed"

	previousFile = "aii.previous"
	pendingFile  = ".update_pending"

	retiredMarkerFile = ".boot_completed.retired"
)

var ErrBootRecoveryPending = errors.New("boot recovery remains armed; wait for boot completion before upgrading data paths")
