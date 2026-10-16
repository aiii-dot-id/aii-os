package plugstore

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const (
	DirArchives   = "a"
	DirRoots      = "r"
	DirBlobs      = "b"
	DirViews      = "v"
	DirTrash      = ".trash"
	LockFile      = ".lock"
	RecordSuffix  = ".record.json"
	PartialSuffix = ".partial"

	LinkSuffix = ".link-"
)

var (
	reDigest = regexp.MustCompile(`^[0-9a-f]{64}$`)
	reKey    = regexp.MustCompile(`^[0-9a-f]{16}$`)
)

func ValidDigest(s string) bool { return reDigest.MatchString(s) }

func ValidKey(s string) bool { return reKey.MatchString(s) }

type FormatError struct {
	What  string
	Value string
}

func (e *FormatError) Error() string {
	v := e.Value
	if len(v) > 80 {
		v = v[:80] + "…"
	}
	switch e.What {
	case "digest":
		return fmt.Sprintf("%q is not a sha256 digest (64 lowercase hex digits)", v)
	case "key":
		return fmt.Sprintf("%q is not a store key (16 lowercase hex digits)", v)
	}
	return fmt.Sprintf("%q is not a store entry (a/, b/ and a digest, or r/, v/ and a key)", v)
}

type Store struct{ Dir string }

func (s Store) digestPath(dir, sha string) (string, error) {
	if !ValidDigest(sha) {
		return "", &FormatError{What: "digest", Value: sha}
	}
	return filepath.Join(s.Dir, dir, sha), nil
}

func (s Store) keyPath(dir, key, suffix string) (string, error) {
	if !ValidKey(key) {
		return "", &FormatError{What: "key", Value: key}
	}
	return filepath.Join(s.Dir, dir, key+suffix), nil
}

func (s Store) Archive(sha string) (string, error) { return s.digestPath(DirArchives, sha) }

func (s Store) Blob(sha string) (string, error) { return s.digestPath(DirBlobs, sha) }

func (s Store) Root(key string) (string, error) { return s.keyPath(DirRoots, key, "") }

func (s Store) RootRecord(key string) (string, error) { return s.keyPath(DirRoots, key, RecordSuffix) }

func (s Store) View(key string) (string, error) { return s.keyPath(DirViews, key, "") }

func (s Store) ViewRecord(key string) (string, error) { return s.keyPath(DirViews, key, RecordSuffix) }

type Entry struct {
	Kind string
	ID   string
}

func ParseEntry(s string) (Entry, error) {
	kind, id, ok := strings.Cut(s, "/")
	if !ok {
		return Entry{}, &FormatError{What: "entry", Value: s}
	}
	switch kind {
	case DirArchives, DirBlobs:
		if !ValidDigest(id) {
			return Entry{}, &FormatError{What: "digest", Value: id}
		}
	case DirRoots, DirViews:
		if !ValidKey(id) {
			return Entry{}, &FormatError{What: "key", Value: id}
		}
	default:
		return Entry{}, &FormatError{What: "entry", Value: s}
	}
	return Entry{Kind: kind, ID: id}, nil
}

func (e Entry) String() string { return e.Kind + "/" + e.ID }

func RootKey(variantID, inventorySHA256, entryName, entryDigest string) string {
	sum := sha256.Sum256([]byte("aii-runtime-root\n" + variantID + "\n" + inventorySHA256 + "\n" + entryName + "\n" + entryDigest + "\n"))
	return hex.EncodeToString(sum[:8])
}

func ViewKey(modelsSHA256 string, names []string) string {
	sorted := append([]string(nil), names...)
	sort.Strings(sorted)
	sum := sha256.Sum256([]byte("aii-model-view\n" + modelsSHA256 + "\n" + strings.Join(sorted, "\n") + "\n"))
	return hex.EncodeToString(sum[:8])
}

func (s Store) Path(e Entry) (string, error) {
	switch e.Kind {
	case DirArchives:
		return s.Archive(e.ID)
	case DirBlobs:
		return s.Blob(e.ID)
	case DirRoots:
		return s.Root(e.ID)
	case DirViews:
		return s.View(e.ID)
	}
	return "", &FormatError{What: "entry", Value: e.String()}
}

func (s Store) Record(e Entry) (string, error) {
	switch e.Kind {
	case DirRoots:
		return s.RootRecord(e.ID)
	case DirViews:
		return s.ViewRecord(e.ID)
	case DirArchives, DirBlobs:
		return "", nil
	}
	return "", &FormatError{What: "entry", Value: e.String()}
}
