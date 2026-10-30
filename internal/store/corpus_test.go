package store

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/canonicaljson"
	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

const corpusManifestSHA256 = "6b8326fd41f86c1041a15c4e458f3139a94092c69bdc96dfb4a5db3e5a517539"

// corpusEntry is one line of the manifest.
type corpusEntry struct {
	file    string // <type>/<shape>.jsonl
	written bool   // a writer of this tree writes the shape
}

func readCorpusManifest(t *testing.T) ([]corpusEntry, []byte) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "corpus", "MANIFEST"))
	if err != nil {
		t.Fatal(err)
	}
	var out []corpusEntry
	sc := bufio.NewScanner(bytes.NewReader(raw))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Fields(line)
		if len(f) != 2 || (f[1] != "written" && f[1] != "recorded") {
			t.Fatalf("MANIFEST line %q is not \"<type>/<shape>.jsonl written|recorded\"", line)
		}
		out = append(out, corpusEntry{file: f[0], written: f[1] == "written"})
	}
	return out, raw
}

// corpusLine is one line of a corpus file.
type corpusLine struct {
	Type    ledger.EventType `json:"type"`
	Ring    int              `json:"ring"`
	Payload json.RawMessage  `json:"payload"`
}

// corpusEvents reads a corpus file into records as a ledger would hold
// them: seq from 1, each linked to the one before and naming its payload,
// a fixed clock a second apart. The payload is held to its canonical form,
// as every recorded payload is.
func corpusEvents(t *testing.T, file string) []ledger.Event {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "corpus", filepath.FromSlash(file)))
	if err != nil {
		t.Fatal(err)
	}
	var events []ledger.Event
	prev := ""
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		if len(bytes.TrimSpace(sc.Bytes())) == 0 {
			continue
		}
		var l corpusLine
		dec := json.NewDecoder(bytes.NewReader(sc.Bytes()))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&l); err != nil {
			t.Fatalf("%s line %d: %v", file, len(events)+1, err)
		}
		payload, err := canonicaljson.CanonicalizeV1(l.Payload)
		if err != nil {
			t.Fatalf("%s line %d: the payload is not one a ledger can hold: %v", file, len(events)+1, err)
		}
		seq := uint64(len(events) + 1)
		evt := ledger.Event{
			Seq: seq, Prev: prev, Type: l.Type, Ring: l.Ring, Payload: payload,
			Timestamp: fmt.Sprintf("2026-01-01T00:%02d:%02dZ", seq/60, seq%60),
			Content:   crypto.ContentHash(payload),
		}
		prev = evt.EntryHash()
		events = append(events, evt)
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	if len(events) == 0 {
		t.Fatalf("%s holds no record", file)
	}
	return events
}

func TestEveryPayloadShapeInTheCorpusMaterializes(t *testing.T) {
	entries, _ := readCorpusManifest(t)
	if len(entries) == 0 {
		t.Fatal("the corpus is empty")
	}
	for _, e := range entries {
		t.Run(e.file, func(t *testing.T) {
			events := corpusEvents(t, e.file)
			last := events[len(events)-1]
			typ, name := path.Split(e.file)
			if string(last.Type)+"/" != typ {
				t.Fatalf("the file's last record is a %s, in the directory of %s", last.Type, strings.TrimSuffix(typ, "/"))
			}
			shape, err := ledger.ShapeOf(last.Payload)
			if err != nil {
				t.Fatal(err)
			}
			if want := shape.Digest() + ".jsonl"; name != want {
				t.Fatalf("the file's last record has shape %s (want the file named %s):\n%s", shape.Digest(), want, shape)
			}
			s := testStore(t)
			if err := s.ReplayAll(EventSlice(events)); err != nil {
				t.Fatalf("A PAYLOAD SHAPE OF THE CORPUS NO LONGER MATERIALIZES: %v", err)
			}
		})
	}
}

// THE CORPUS ONLY GROWS. Every file is listed, every listed file is
// there, and the manifest is the one this test holds.
func TestTheCorpusOnlyGrows(t *testing.T) {
	entries, raw := readCorpusManifest(t)
	listed := map[string]bool{}
	for _, e := range entries {
		if listed[e.file] {
			t.Errorf("MANIFEST lists %s twice", e.file)
		}
		listed[e.file] = true
		if _, err := os.Stat(filepath.Join("testdata", "corpus", filepath.FromSlash(e.file))); err != nil {
			t.Errorf("A CORPUS FILE IS GONE: %s (%v)", e.file, err)
		}
	}
	root := filepath.Join("testdata", "corpus")
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Name() == "MANIFEST" {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		if !listed[filepath.ToSlash(rel)] {
			t.Errorf("%s is in the corpus and not in MANIFEST", filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// Over the file's lines as written, whatever line ending a checkout
	// gave them (a Windows checkout may write CRLF).
	if sum := sha256.Sum256(bytes.ReplaceAll(raw, []byte("\r\n"), []byte("\n"))); hex.EncodeToString(sum[:]) != corpusManifestSHA256 {
		t.Fatalf("MANIFEST is not the one this test holds (sha256 %x): a line added is added here too; a line is never taken out", sum)
	}
}
