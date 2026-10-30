package updates

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/atomicfile"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/version"
)

type highestVerified struct {
	Version            string    `json:"version"`
	Platform           string    `json:"platform"`
	Arch               string    `json:"arch"`
	ArchiveSHA256      string    `json:"archive_sha256"`
	VerifiedAt         time.Time `json:"verified_at"`
	ReleasePublishedAt string    `json:"release_published_at,omitempty"`
}

type BelowHighestVerifiedError struct {
	Offered, Highest string
}

const belowHighestWords = "%s is older than %s, the newest release this program has verified, so it is not installed here. An older release is installed by hand"

func (e *BelowHighestVerifiedError) Error() string {
	return fmt.Sprintf(belowHighestWords, e.Offered, e.Highest)
}

func (t target) readHighest() (h highestVerified, ok bool) {
	if t.highest == "" {
		return highestVerified{}, false
	}
	raw, err := atomicfile.ReadFile(t.highest)
	if errors.Is(err, os.ErrNotExist) {
		return highestVerified{}, false
	}
	if err == nil {
		err = json.Unmarshal(raw, &h)
	}
	if err == nil && !version.Valid(h.Version) {
		err = fmt.Errorf("it names no release version (%q)", h.Version)
	}
	if err != nil {
		logsink.Warn("updates.refusal", "the record of the newest release verified here (%s) does not read: %v — it is removed, and the next verified release writes it again", t.highest, err)
		os.Remove(t.highest)
		return highestVerified{}, false
	}
	return h, true
}

func (t target) belowHighest(offered string) error {
	h, ok := t.readHighest()
	if !ok || !version.Valid(offered) || version.Compare(offered, h.Version) >= 0 {
		return nil
	}
	return &BelowHighestVerifiedError{Offered: offered, Highest: h.Version}
}

type SecondArchiveError struct {
	Version string
}

const secondArchiveWords = "a second, different %s was offered; this program verified another archive of %s and refuses this one"

func (e *SecondArchiveError) Error() string {
	return fmt.Sprintf(secondArchiveWords, e.Version, e.Version)
}

func (t target) secondArchive(h highestVerified) error {
	cur, ok := t.readHighest()
	if !ok || cur.Version != h.Version || cur.Platform != h.Platform || cur.Arch != h.Arch || cur.ArchiveSHA256 == "" {
		return nil
	}
	if cur.ArchiveSHA256 == h.ArchiveSHA256 {
		return nil
	}
	return &SecondArchiveError{Version: h.Version}
}

func (t target) noteVerified(h highestVerified) {
	if t.highest == "" || !version.Valid(h.Version) {
		return
	}
	if cur, ok := t.readHighest(); ok && version.Compare(h.Version, cur.Version) <= 0 {
		return
	}
	raw, err := json.Marshal(h)
	if err == nil {
		err = writeFileAtomic(t.highest, raw, 0o644)
	}
	if err != nil {
		logsink.Warn("updates.error", "the newest release verified here (%s) could not be recorded beside the program: %v", h.Version, err)
	}
}

type HighestSeen struct {
	Version     string
	PublishedAt string
	VerifiedAt  time.Time
}

func HighestVerified() (HighestSeen, bool) {
	exe, err := os.Executable()
	if err != nil {
		return HighestSeen{}, false
	}
	return highestVerifiedAt(exe)
}

func highestVerifiedAt(exePath string) (HighestSeen, bool) {
	tgt, err := targetOf(exePath)
	if err != nil {
		return HighestSeen{}, false
	}
	h, ok := tgt.readHighest()
	if !ok {
		return HighestSeen{}, false
	}
	return HighestSeen{Version: h.Version, PublishedAt: h.ReleasePublishedAt, VerifiedAt: h.VerifiedAt}, true
}
