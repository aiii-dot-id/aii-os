package main

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/genesis/genesistest"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

// headsTakenOnTrust stands beside a test ledger in place of the witness
// keys a real one keeps: it takes every witness head as the witness's. The
// witness verifier is its own package's to test (internal/witness); what
// these tests hold is the reading, which needs a ledger with closed
// segments in it and the walk to accept their heads.
type headsTakenOnTrust struct{}

func (headsTakenOnTrust) VerifyHead(evt *ledger.Event, _ func(uint64) (string, bool)) (uint64, error) {
	return evt.Seq - 1, nil
}
func (headsTakenOnTrust) Visit(*ledger.Event) error { return nil }
func (headsTakenOnTrust) Held() error               { return nil }
func (headsTakenOnTrust) Summary() string           { return "heads taken on trust" }

func trustHeads(string) (genesis.Beside, error) { return headsTakenOnTrust{}, nil }

// shapesFixture is an identity installed in dir, laid out as every
// identity is (data/ledger.jsonl): born, three notes and a witness head
// sealed into a segment, three more and a head into a second, and three
// notes left in the tail, then whatever more appends. The ledger is closed:
// no host is running. (The same ledger cmd/aii's reading tests build, for
// `aii ledger show`.)
type shapesFixture struct {
	dir, path string
	heads     []uint64 // the two closing heads
	last      uint64
}

func newShapesFixture(t *testing.T, more ...func(*genesis.BirthResult)) *shapesFixture {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "data", "ledger.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	root := genesistest.NewRoot(t)
	born, err := genesis.Birth(&genesis.BirthConfig{
		Name: "LedgerShow ZeBrA", Ring0Bundle: root.Ring0Bundle(t), Root: root.Env,
		KeyPath: filepath.Join(dir, "data", "identity.sec"), LedgerPath: path, DBPath: filepath.Join(dir, "data", "aii.db"),
	})
	if err != nil {
		t.Fatalf("birth: %v", err)
	}
	f := &shapesFixture{dir: dir, path: path}
	lg := born.Ledger
	defer lg.Close()
	note := func(i int, words string) {
		if _, err := lg.Append(ledger.EventExperienceCreate, 3, map[string]any{"id": "e" + strconv.Itoa(i), "content": words}, born.KeyPair); err != nil {
			t.Fatal(err)
		}
	}
	head := func() {
		evt, err := lg.Append(ledger.EventSystemWitnessed, 0,
			map[string]any{"receipt": map[string]any{"ledger_ordinal": lg.LastSeq(), "ledger_hash": lg.LastHash()}}, born.KeyPair)
		if err != nil {
			t.Fatal(err)
		}
		if err := lg.Seal(evt.Seq); err != nil {
			t.Fatal(err)
		}
		f.heads = append(f.heads, evt.Seq)
	}
	for i := 0; i < 3; i++ {
		note(i, "a note, number "+strconv.Itoa(i))
	}
	head()
	for i := 3; i < 6; i++ {
		words := "a note, number " + strconv.Itoa(i)
		if i == 4 {
			words = "a zebra crossed the record"
		}
		note(i, words)
	}
	head()
	for i := 6; i < 9; i++ {
		note(i, "a note, number "+strconv.Itoa(i))
	}
	for _, add := range more {
		add(born)
	}
	f.last = lg.LastSeq()
	return f
}

func gunzip(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	gz, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(gz)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func regzip(t *testing.T, name string, plain []byte) {
	t.Helper()
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	if _, err := gz.Write(plain); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, b.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

// alterPayload changes one byte of record seq's payload in the line that
// holds it: "number" becomes "numbes".
func alterPayload(t *testing.T, plain []byte, seq uint64) []byte {
	t.Helper()
	marker := []byte(`"seq":` + strconv.FormatUint(seq, 10) + `,`)
	at := bytes.Index(plain, marker)
	if at < 0 {
		t.Fatalf("no record %d in the container", seq)
	}
	end := at + bytes.IndexByte(plain[at:], '\n')
	line := plain[at:end]
	i := bytes.Index(line, []byte("number"))
	if i < 0 {
		t.Fatalf("record %d has no word to alter: %s", seq, line)
	}
	out := bytes.Clone(plain)
	out[at+i+5] = 's'
	return out
}

// dirState is every file under dir by its bytes and mode: what a reader
// must leave as it found.
func dirState(t *testing.T, dir string) map[string]string {
	t.Helper()
	state := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		sum := ""
		if d.Type().IsRegular() {
			raw, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			sum = fmt.Sprintf("%x", sha256.Sum256(raw))
		}
		state[p] = fmt.Sprintf("%v %d %s", info.Mode(), info.Size(), sum)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func runShapes(t *testing.T, args ...string) (int, shapesReport, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := ledgerShapes(args, &stdout, &stderr, trustHeads)
	var rep shapesReport
	if code != 2 {
		if err := json.Unmarshal(stdout.Bytes(), &rep); err != nil {
			t.Fatalf("the output is not the report: %v\n%s", err, stdout.String())
		}
	}
	return code, rep, stdout.String(), stderr.String()
}

func TestLedgerShapesCountsEachShapeAndPrintsNoValue(t *testing.T) {
	f := newShapesFixture(t)
	before := dirState(t, f.dir)
	code, rep, out, errOut := runShapes(t, "-ledger", f.path)
	if code != 0 || errOut != "" {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
	if uint64(rep.Records) != f.last {
		t.Fatalf("counted %d records of %d", rep.Records, f.last)
	}
	want := map[string]int{"ring0.genesis": 1, "experience.create": 9, "system.witnessed": 2}
	for typ, n := range want {
		shapes := rep.Types[typ]
		if len(shapes) != 1 || shapes[0].Records != n {
			t.Errorf("%s: want one shape of %d records, got %+v", typ, n, shapes)
		}
	}
	if len(rep.Types) != len(want) {
		t.Errorf("types counted: %v", slices.Collect(maps.Keys(rep.Types)))
	}
	note := rep.Types["experience.create"][0]
	if !strings.Contains(strings.Join(note.Members, "\n"), "content string") || len(note.Shape) != 16 {
		t.Errorf("a note's shape is not its members and kinds: %+v", note)
	}
	if note.Door != "admitted" {
		t.Errorf("the door's word on a note this tree writes: %q", note.Door)
	}
	for _, value := range []string{"zebra", "a note, number", "LedgerShow"} {
		if strings.Contains(strings.ToLower(out), strings.ToLower(value)) {
			t.Fatalf("A VALUE OF THE LEDGER WAS PRINTED (%q):\n%s", value, out)
		}
	}
	if after := dirState(t, f.dir); !maps.Equal(before, after) {
		t.Fatalf("THE READER CHANGED THE DIRECTORY:\nbefore %v\nafter  %v", before, after)
	}
}

// A shape is taken only from a record the walk proved. One byte is
// altered inside the second segment: the records of that segment before
// it passed their own checks, but only the segment's closing head proves
// them, so standard error says where the chain stops, the exit is 1, and
// nothing after the first segment's head is counted.
func TestLedgerShapesCountsOnlyWhatTheWalkProved(t *testing.T) {
	f := newShapesFixture(t)
	seg := filepath.Join(f.dir, "data", fmt.Sprintf("segment-%d-%d.jsonl.gz", f.heads[0]+1, f.heads[1]))
	k := f.heads[0] + 3 // the third note of the second segment
	regzip(t, seg, alterPayload(t, gunzip(t, seg), k))
	code, rep, _, errOut := runShapes(t, "-ledger", f.path)
	if code != 1 || !strings.Contains(errOut, "ledger-shapes: the record stops verifying at seq") {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
	if uint64(rep.Records) != f.heads[0] {
		t.Fatalf("counted %d records; the walk proved %d (the first segment's head)", rep.Records, f.heads[0])
	}
	for _, shapes := range rep.Types {
		for _, s := range shapes {
			if s.LastSeq > f.heads[0] {
				t.Fatalf("A SHAPE WAS TAKEN FROM A RECORD NO PROOF COVERED (seq %d, the first head %d): %+v", s.LastSeq, f.heads[0], s)
			}
		}
	}
}

// Each shape says what this program's door says of a new record of it: a
// note an earlier release signed with a member no type declares is
// counted as its own shape, and the door's refusal is printed beside it.
func TestLedgerShapesSaysWhatTheDoorSaysOfEachShape(t *testing.T) {
	f := newShapesFixture(t, func(born *genesis.BirthResult) {
		if _, err := born.Ledger.Append(ledger.EventExperienceCreate, 3, map[string]any{"id": "e_old", "content": "invented", "mood": "x"}, born.KeyPair); err != nil {
			t.Fatal(err)
		}
	})
	code, rep, _, errOut := runShapes(t, "-ledger", f.path)
	if code != 0 || errOut != "" {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
	doors := map[string]bool{}
	for _, s := range rep.Types["experience.create"] {
		doors[s.Door] = true
	}
	want := `experience.create does not declare the member "mood", so nothing was recorded`
	refused := false
	for d := range doors {
		refused = refused || strings.HasPrefix(d, want)
	}
	if !doors["admitted"] || !refused {
		t.Fatalf("THE DOOR'S WORD ON EACH SHAPE IS NOT PRINTED: %v", doors)
	}
}

// A path is required: the reader is pointed at a copy, by name.
func TestLedgerShapesWantsALedger(t *testing.T) {
	if code, _, _, errOut := runShapes(t); code != 2 || !strings.Contains(errOut, "usage: go run ./cmd/aii-release ledger-shapes -ledger PATH") {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
}
