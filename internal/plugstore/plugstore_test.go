package plugstore

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sha = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// NO PATH IS BUILT FROM AN UNCHECKED STRING. Every name the store makes a
// path of — a digest, a key, an entry — is refused before a path exists
// when it is not exactly one: a traversal, an uppercase spelling (one file
// under two names on a case-insensitive volume), one digit short or over.
func TestNoPathIsBuiltFromAnUncheckedName(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	digests := []string{"../x", "..", "", strings.ToUpper(sha), sha[:63], sha + "0", sha[:62] + "/x", sha[:63] + "g", sha[:60] + "\\..\\"}
	for _, bad := range digests {
		for name, f := range map[string]func(string) (string, error){"Blob": s.Blob, "Archive": s.Archive} {
			if p, err := f(bad); err == nil {
				t.Errorf("%s(%q) built %s", name, bad, p)
			} else if _, ok := errors.AsType[*FormatError](err); !ok {
				t.Errorf("%s(%q): want a FormatError, got %v", name, bad, err)
			}
		}
	}
	keys := []string{"../x", "0123456789ABCDEF", "0123456789abcde", "0123456789abcdef0", "0123456789abcde/", ""}
	for _, bad := range keys {
		for name, f := range map[string]func(string) (string, error){"Root": s.Root, "RootRecord": s.RootRecord, "View": s.View, "ViewRecord": s.ViewRecord} {
			if p, err := f(bad); err == nil {
				t.Errorf("%s(%q) built %s", name, bad, p)
			}
		}
		if _, err := s.PinView(bad); err == nil || errors.Is(err, fs.ErrNotExist) {
			t.Errorf("PinView(%q) went as far as the file system: %v", bad, err)
		}
	}
	for _, bad := range []string{"b/../x", "r/" + sha, "a/0123456789abcdef", "x/" + sha, "b" + sha, "v/0123456789ABCDEF", "/b/" + sha} {
		if e, err := ParseEntry(bad); err == nil {
			t.Errorf("ParseEntry(%q) = %v", bad, e)
		}
	}
	for _, good := range []string{"b/" + sha, "a/" + sha, "r/0123456789abcdef", "v/fedcba9876543210"} {
		e, err := ParseEntry(good)
		if err != nil || e.String() != good {
			t.Errorf("ParseEntry(%q) = %v, %v", good, e, err)
		}
	}
	if p, err := s.Blob(sha); err != nil || p != filepath.Join(s.Dir, "b", sha) {
		t.Errorf("Blob = %s, %v", p, err)
	}
}

// THE KEYS ARE THE ONES IDENTITIES ALREADY NAME: a root's is the formula a
// slot's root was named by (pinned here by value), and a view's does not
// depend on the order its names come in.
func TestTheKeysAreFixed(t *testing.T) {
	if got := RootKey("cpu", sha, "carrier", "sha256:"+sha); got != "33f89ef2c01e537e" {
		t.Errorf("RootKey moved: %s", got)
	}
	a := ViewKey("sha256:"+sha, []string{"stt", "tts", "vad"})
	b := ViewKey("sha256:"+sha, []string{"vad", "stt", "tts"})
	if a != b || !ValidKey(a) {
		t.Errorf("a view's key depends on the order of its names: %s %s", a, b)
	}
	if ViewKey("sha256:"+sha, []string{"stt"}) == a || ViewKey("sha256:"+strings.Repeat("1", 64), []string{"stt", "tts", "vad"}) == a {
		t.Error("two different views share a key")
	}
}

// THE RECORDS' FORMAT IS FROZEN (H3): what this build writes reads back; a
// record with a member this build does not know reads, and keeps it when
// written again; a record lacking a member this build needs is refused,
// naming it, and is never taken for damage; another format is refused.
func TestRecordsWrittenWithAnAddedOrRemovedMemberReadAsTheFormatSays(t *testing.T) {
	root := RootRecord{VariantID: "cpu", InventorySHA256: sha, ArchiveSHA256: sha, Entrypoint: "carrier",
		EntrypointSHA256: "sha256:" + sha, Files: 3, InstalledBytes: 12, Inventory: []byte(`{"files":[]}`), InstalledAt: "2026-10-09T00:00:00Z"}
	raw, err := json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	back, err := DecodeRootRecord("r", raw)
	if err != nil || back.VariantID != "cpu" || string(back.Inventory) != `{"files":[]}` || back.Files != 3 {
		t.Fatalf("a record this build wrote does not read back: %+v %v", back, err)
	}
	var members map[string]json.RawMessage
	if err := json.Unmarshal(raw, &members); err != nil {
		t.Fatal(err)
	}
	if string(members["format"]) != "1" {
		t.Fatalf("a record carries format 1: %s", raw)
	}

	// An added member: read, ignored, kept.
	members["engine_hint"] = json.RawMessage(`{"from":"a later build"}`)
	added, _ := json.Marshal(members)
	got, err := DecodeRootRecord("r", added)
	if err != nil {
		t.Fatalf("a record with a member this build does not know must read: %v", err)
	}
	again, _ := json.Marshal(got)
	if !strings.Contains(string(again), `"engine_hint"`) {
		t.Errorf("a member this build does not know was dropped on the way through: %s", again)
	}

	// A removed member: refused, named.
	for _, gone := range []string{"inventory", "archive_sha256", "format"} {
		cut := map[string]json.RawMessage{}
		for k, v := range members {
			if k != gone {
				cut[k] = v
			}
		}
		removed, _ := json.Marshal(cut)
		_, err := DecodeRootRecord("r/x.record.json", removed)
		re, ok := errors.AsType[*RecordError](err)
		if !ok || len(re.Missing) != 1 || re.Missing[0] != gone {
			t.Errorf("a record without %s: want a RecordError naming it, got %v", gone, err)
		}
	}
	// Another format: refused.
	members["format"] = json.RawMessage(`2`)
	other, _ := json.Marshal(members)
	if _, err := DecodeRootRecord("r", other); err == nil {
		t.Error("a record of another format must be refused")
	}

	view := ViewRecord{ModelsSHA256: "sha256:" + sha, Names: []string{"tts", "stt"}}
	raw, _ = json.Marshal(view)
	vb, err := DecodeViewRecord("v", raw)
	if err != nil || strings.Join(vb.Names, ",") != "stt,tts" {
		t.Fatalf("a view record does not read back: %+v %v", vb, err)
	}
	if _, err := DecodeViewRecord("v", []byte(`{"format":1,"names":[]}`)); err == nil {
		t.Error("a view record without models_sha256 must be refused")
	}
	if _, err := DecodeViewRecord("v", []byte(`{"format":1,"models_sha256":"sha256:x"`)); err == nil {
		t.Error("a torn view record must be refused")
	}
}

// A PIN HOLDS THE RECORD IT LOCKED, and lets it go when closed: a reader of
// an entry with nothing else in the store is pinned all the same.
func TestAPinHoldsTheRecordItLocked(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	key := "0123456789abcdef"
	path, _ := s.ViewRecord(key)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PinView(key); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("a pin on an absent record: want fs.ErrNotExist, got %v", err)
	}
	if err := os.WriteFile(path, []byte(`{"format":1,"models_sha256":"sha256:x","names":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := s.PinView(key)
	if err != nil {
		t.Fatalf("pin: %v", err)
	}
	if err := p.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := p.Close(); err != nil {
		t.Fatalf("a second close: %v", err)
	}
}
