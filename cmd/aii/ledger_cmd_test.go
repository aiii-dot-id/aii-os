package main

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
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

// showFixture is an identity installed in dir, laid out as every identity
// is (data/ledger.jsonl, no config): born, three notes and a witness head
// sealed into a segment, three more and a head into a second, and three
// notes left in the tail. The ledger is closed: no host is running.
type showFixture struct {
	dir, path  string
	heads      []uint64 // the two closing heads
	last       uint64
	zebra      uint64 // the note whose content holds "a zebra crossed the record"
	born       *genesis.BirthResult
	keepOpen   bool
	closedOnce bool
}

func newShowFixture(t *testing.T, keepOpen bool) *showFixture {
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
	f := &showFixture{dir: dir, path: path, born: born, keepOpen: keepOpen}
	t.Cleanup(f.close)
	lg := born.Ledger
	note := func(i int, words string) {
		evt, err := lg.Append(ledger.EventExperienceCreate, 3, map[string]any{"id": "e" + strconv.Itoa(i), "content": words}, born.KeyPair)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(words, "zebra") {
			f.zebra = evt.Seq
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
	f.last = lg.LastSeq()
	if !keepOpen {
		f.close()
	}
	return f
}

func (f *showFixture) close() {
	if f.closedOnce {
		return
	}
	f.closedOnce = true
	f.born.Ledger.Close()
}

// fileLines is every line the ledger's files hold, in file order (the
// segments by their first record, then the tail), without line endings.
func (f *showFixture) fileLines(t *testing.T) [][]byte {
	t.Helper()
	names, err := filepath.Glob(filepath.Join(f.dir, "data", "segment-*.jsonl.gz"))
	if err != nil {
		t.Fatal(err)
	}
	first := func(name string) int {
		n, _ := strconv.Atoi(strings.SplitN(strings.TrimPrefix(filepath.Base(name), "segment-"), "-", 2)[0])
		return n
	}
	slices.SortFunc(names, func(a, b string) int { return first(a) - first(b) })
	var lines [][]byte
	for _, name := range names {
		lines = append(lines, splitLines(gunzip(t, name))...)
	}
	tail, err := os.ReadFile(f.path)
	if err != nil {
		t.Fatal(err)
	}
	return append(lines, splitLines(tail)...)
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

// splitLines is the complete lines of b; a last line without its newline
// is not one.
func splitLines(b []byte) [][]byte {
	var out [][]byte
	for {
		i := bytes.IndexByte(b, '\n')
		if i < 0 {
			return out
		}
		out = append(out, b[:i])
		b = b[i+1:]
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

// showRow is one row of the text output: its seq and whether it was marked.
type showRow struct {
	seq        uint64
	unverified bool
}

func showRows(t *testing.T, out string) []showRow {
	t.Helper()
	var rows []showRow
	sc := bufio.NewScanner(strings.NewReader(out))
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "#") {
			continue
		}
		num, _, _ := strings.Cut(strings.TrimPrefix(line, "#"), "  ")
		seq, err := strconv.ParseUint(num, 10, 64)
		if err != nil {
			t.Fatalf("a row header without its seq: %q", line)
		}
		rows = append(rows, showRow{seq, strings.HasSuffix(line, ledgerUnverifiedMark)})
	}
	return rows
}

func runShow(t *testing.T, load genesis.BesideLoader, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := ledgerShow(args, &stdout, &stderr, load)
	return code, stdout.String(), stderr.String()
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

// THE CLOSED SEGMENTS ARE READ, WITH NO HOST RUNNING, AND NOTHING IS
// WRITTEN. Every record from 1 to the last is printed once, in order, as
// verified, from two segments and the tail, and the directory is as it was.
func TestLedgerShowReadsTheClosedSegmentsWithNoHostRunning(t *testing.T) {
	f := newShowFixture(t, false)
	if _, err := os.Stat(filepath.Join(f.dir, "data", fmt.Sprintf("segment-1-%d.jsonl.gz", f.heads[0]))); err != nil {
		t.Fatalf("fixture: no first segment: %v", err)
	}
	before := dirState(t, f.dir)
	code, out, errOut := runShow(t, trustHeads, "-dir", f.dir)
	if code != 0 || errOut != "" {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
	rows := showRows(t, out)
	if uint64(len(rows)) != f.last {
		t.Fatalf("printed %d rows for %d records:\n%s", len(rows), f.last, out)
	}
	for i, r := range rows {
		if r.seq != uint64(i+1) || r.unverified {
			t.Fatalf("row %d is %+v, want seq %d verified", i, r, i+1)
		}
	}
	if !strings.Contains(out, `"content":"a zebra crossed the record"`) {
		t.Errorf("a payload is not printed whole:\n%s", out)
	}
	if after := dirState(t, f.dir); !maps.Equal(before, after) {
		t.Fatalf("THE READER CHANGED THE IDENTITY'S DIRECTORY:\nbefore %v\nafter  %v", before, after)
	}
}

// -json IS THE FILE. Each record's line is printed byte for byte as the
// file holds it, the segments' interior records without a proof and the
// tail's with theirs, and a line whose envelope is not in the writer's
// form (the walk accepts it: what is signed is the entry) stays as it is.
func TestLedgerShowJSONIsEachLineAsTheFileHoldsIt(t *testing.T) {
	f := newShowFixture(t, false)
	raw, err := os.ReadFile(f.path)
	if err != nil {
		t.Fatal(err)
	}
	spaced := bytes.Replace(raw, []byte(`{"entry":`), []byte(`{ "entry" :`), 1)
	if bytes.Equal(spaced, raw) {
		t.Fatal("fixture: the tail has no line to space")
	}
	if err := os.WriteFile(f.path, spaced, 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := runShow(t, trustHeads, "-dir", f.dir, "-json")
	if code != 0 || errOut != "" {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
	var want bytes.Buffer
	for _, l := range f.fileLines(t) {
		want.Write(l)
		want.WriteByte('\n')
	}
	if out != want.String() {
		t.Fatalf("-json is not the file's lines:\n got %q\nwant %q", out, want.String())
	}
	if !strings.Contains(out, `{ "entry" :`) {
		t.Fatal("the spaced line was re-encoded")
	}
}

// ONE ALTERED BYTE IN THE TAIL: the seq is named, the rows before it are
// printed as verified, the rest are printed marked, and the exit is 1.
func TestLedgerShowNamesTheSeqOfOneAlteredByteAndPrintsTheRestMarked(t *testing.T) {
	f := newShowFixture(t, false)
	k := f.heads[1] + 2 // a note in the tail, with a note after it
	raw, err := os.ReadFile(f.path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.path, alterPayload(t, raw, k), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := runShow(t, trustHeads, "-dir", f.dir)
	if code != 1 {
		t.Fatalf("exit %d for an altered record, want 1; stderr %q", code, errOut)
	}
	wantErr := fmt.Sprintf("ledger show: the record stops verifying at seq %d: ", k)
	tail := fmt.Sprintf("The rows through seq %d were verified; the rows from seq %d on are shown unverified.\n", k-1, k)
	if !strings.HasPrefix(errOut, wantErr) || !strings.HasSuffix(errOut, tail) || !strings.Contains(errOut, "content mismatch") {
		t.Fatalf("stderr %q\nwant it to begin %q, name the content mismatch and end %q", errOut, wantErr, tail)
	}
	rows := showRows(t, out)
	if uint64(len(rows)) != f.last {
		t.Fatalf("printed %d rows of %d:\n%s", len(rows), f.last, out)
	}
	for i, r := range rows {
		seq := uint64(i + 1)
		if r.seq != seq || r.unverified != (seq >= k) {
			t.Fatalf("row %d is %+v; want seq %d, marked from %d on", i, r, seq, k)
		}
	}
	// -json prints every line unchanged, the altered one among them.
	code, out, errOut = runShow(t, trustHeads, "-dir", f.dir, "-json")
	var want bytes.Buffer
	for _, l := range f.fileLines(t) {
		want.Write(l)
		want.WriteByte('\n')
	}
	if code != 1 || out != want.String() || !strings.HasPrefix(errOut, wantErr) {
		t.Fatalf("-json: exit %d, stderr %q, stdout equal to the file's lines: %v", code, errOut, out == want.String())
	}
}

// ONE ALTERED BYTE INSIDE A SEGMENT. The rows of that segment before it
// passed their own checks, but only the segment's closing head proves
// them, and the walk never reached it: they are marked, and the rows of
// the segment before, which its head proved, are not.
func TestLedgerShowMarksTheSealedRowsNoHeadProved(t *testing.T) {
	f := newShowFixture(t, false)
	seg := filepath.Join(f.dir, "data", fmt.Sprintf("segment-%d-%d.jsonl.gz", f.heads[0]+1, f.heads[1]))
	j := f.heads[0] + 3 // the third note of the second segment: "number 5"
	regzip(t, seg, alterPayload(t, gunzip(t, seg), j))
	code, out, errOut := runShow(t, trustHeads, "-dir", f.dir)
	if code != 1 {
		t.Fatalf("exit %d, want 1; stderr %q", code, errOut)
	}
	p := f.heads[0]
	wantErr := fmt.Sprintf("ledger show: the record stops verifying at seq %d: ", j)
	tail := fmt.Sprintf("The rows through seq %d were verified; the rows from seq %d on are shown unverified.\n", p, p+1)
	if !strings.HasPrefix(errOut, wantErr) || !strings.HasSuffix(errOut, tail) {
		t.Fatalf("stderr %q\nwant it to begin %q and end %q", errOut, wantErr, tail)
	}
	rows := showRows(t, out)
	if uint64(len(rows)) != f.last {
		t.Fatalf("printed %d rows of %d:\n%s", len(rows), f.last, out)
	}
	for i, r := range rows {
		seq := uint64(i + 1)
		if r.seq != seq || r.unverified != (seq > p) {
			t.Fatalf("row %d is %+v; want seq %d, marked after %d", i, r, seq, p)
		}
	}
}

// A FILE BEING WRITTEN. A host holds the ledger open and its last line is
// not yet whole: every complete record is printed as verified, the partial
// line is not a record and is not printed, standard error says so, and the
// exit is 0.
func TestLedgerShowStopsAtTheLastCompleteLineOfAFileBeingWritten(t *testing.T) {
	f := newShowFixture(t, true)
	w, err := os.OpenFile(f.path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.WriteString(`{"entry":{"content":"0b1f`); err != nil {
		t.Fatal(err)
	}
	w.Close()
	code, out, errOut := runShow(t, trustHeads, "-dir", f.dir)
	if code != 0 {
		t.Fatalf("exit %d, want 0; stderr %q", code, errOut)
	}
	if want := fmt.Sprintf(ledgerCutShort, f.last); errOut != want {
		t.Fatalf("stderr %q, want %q", errOut, want)
	}
	rows := showRows(t, out)
	if uint64(len(rows)) != f.last || rows[len(rows)-1].seq != f.last {
		t.Fatalf("printed %d rows of %d:\n%s", len(rows), f.last, out)
	}
	for _, r := range rows {
		if r.unverified {
			t.Fatalf("row %d marked:\n%s", r.seq, out)
		}
	}
	if strings.Contains(out, "0b1f") {
		t.Fatal("the partial line was printed")
	}
}

// THE FILTERS CHOOSE WHAT IS PRINTED; THE WALK STILL STARTS AT RECORD 1.
func TestLedgerShowFiltersChooseRowsAndVerifyFromGenesis(t *testing.T) {
	f := newShowFixture(t, false)
	seqs := func(args ...string) []uint64 {
		t.Helper()
		code, out, errOut := runShow(t, trustHeads, append([]string{"-dir", f.dir}, args...)...)
		if code != 0 || errOut != "" {
			t.Fatalf("%v: exit %d, stderr %q", args, code, errOut)
		}
		var got []uint64
		for _, r := range showRows(t, out) {
			got = append(got, r.seq)
		}
		return got
	}
	if got := seqs("-type", string(ledger.EventSystemWitnessed)); !slices.Equal(got, f.heads) {
		t.Errorf("-type system.witnessed printed %v, want the heads %v", got, f.heads)
	}
	if got := seqs("-from", "4", "-to", "6"); !slices.Equal(got, []uint64{4, 5, 6}) {
		t.Errorf("-from 4 -to 6 printed %v", got)
	}
	// A short word may also occur in genesis metadata or encoded keys.
	// The fixture name deliberately matches; the full phrase selects the note.
	if got := seqs("-grep", "ZEBRA"); !slices.Equal(got, []uint64{1, f.zebra}) {
		t.Errorf("-grep ZEBRA printed %v, want genesis and note %d", got, f.zebra)
	}
	if got := seqs("-grep", "A ZEBRA CROSSED THE RECORD"); !slices.Equal(got, []uint64{f.zebra}) {
		t.Errorf("-grep full phrase printed %v, want note %d", got, f.zebra)
	}
	if got := seqs("-type", string(ledger.EventExperienceCreate), "-grep", "number 7"); len(got) != 1 {
		t.Errorf("-type with -grep printed %v, want one record", got)
	}

	// Damage before -from: the walk names it, and what -from prints is marked.
	k := f.heads[1] + 1
	raw, err := os.ReadFile(f.path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.path, alterPayload(t, raw, k), 0o600); err != nil {
		t.Fatal(err)
	}
	from := k + 1
	code, out, errOut := runShow(t, trustHeads, "-dir", f.dir, "-from", strconv.FormatUint(from, 10))
	if code != 1 || !strings.HasPrefix(errOut, fmt.Sprintf("ledger show: the record stops verifying at seq %d: ", k)) {
		t.Fatalf("damage before -from: exit %d, stderr %q", code, errOut)
	}
	rows := showRows(t, out)
	if len(rows) == 0 || rows[0].seq != from || rows[len(rows)-1].seq != f.last {
		t.Fatalf("-from %d printed %+v", from, rows)
	}
	for _, r := range rows {
		if !r.unverified {
			t.Fatalf("row %d after the damage is not marked", r.seq)
		}
	}
}

// The product's loader, the pinned platform root's, holds no key for a test
// witness, so the first segment's head is refused after its own proof
// verified: the verifier says "proved through" that head, yet the walk
// passed no proof, so no row is printed as verified and the sentence says
// none was. Every row is still shown.
func TestLedgerShowWithoutTheWitnessKeysShowsEverySealedRowUnverified(t *testing.T) {
	f := newShowFixture(t, false)
	code, out, errOut := runShow(t, besideLedger, "-dir", f.dir)
	wantErr := fmt.Sprintf("ledger show: the record stops verifying at seq %d: ", f.heads[0])
	if code != 1 || !strings.HasPrefix(errOut, wantErr) || !strings.HasSuffix(errOut, ledgerNoneVerified) {
		t.Fatalf("exit %d, stderr %q\nwant it to begin %q and end %q", code, errOut, wantErr, ledgerNoneVerified)
	}
	rows := showRows(t, out)
	if uint64(len(rows)) != f.last {
		t.Fatalf("printed %d rows of %d", len(rows), f.last)
	}
	for _, r := range rows {
		if !r.unverified {
			t.Fatalf("row %d is printed as verified under a head the walk refused:\n%s", r.seq, out)
		}
	}
}

// The usage and its refusals.
func TestLedgerShowUsage(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runLedger(nil, &stdout, &stderr); code != 2 || !strings.HasPrefix(stderr.String(), "usage: aii ledger show") {
		t.Fatalf("bare ledger: exit %d, stderr %q", code, stderr.String())
	}
	if code, _, errOut := runShow(t, trustHeads, "-from", "5", "-to", "4"); code != 2 || errOut != "ledger show: -from is after -to\n" {
		t.Fatalf("-from after -to: exit %d, stderr %q", code, errOut)
	}
	if code, _, errOut := runShow(t, trustHeads, "extra"); code != 2 || errOut != "ledger show: unexpected argument \"extra\"\n" {
		t.Fatalf("an argument: exit %d, stderr %q", code, errOut)
	}
	if code, _, errOut := runShow(t, trustHeads, "-dir", t.TempDir()); code != 1 || !strings.HasPrefix(errOut, "ledger show: ") {
		t.Fatalf("a directory with no record: exit %d, stderr %q", code, errOut)
	}
}

// NO ROW REWRITES THE TERMINAL. A payload may hold any character a person
// typed, among them a C1 control (U+009B is CSI, which a terminal can take
// as the start of a command) and a bidi override (U+202E turns the text
// after it around). In text every character that is not printable is shown
// as \u{<hex>}, and none reaches the terminal raw; -json is still the file,
// byte for byte.
func TestLedgerShowTextEscapesEveryCharacterThatCouldRewriteTheTerminal(t *testing.T) {
	f := newShowFixture(t, true)
	words := "red \u009b31m then \u202eesrever\u202c and \u200f\u061c\u2066x\u2069 \u007f"
	evt, err := f.born.Ledger.Append(ledger.EventExperienceCreate, 3, map[string]any{"id": "tty", "content": words}, f.born.KeyPair)
	if err != nil {
		t.Fatal(err)
	}
	f.close()
	code, out, errOut := runShow(t, trustHeads, "-dir", f.dir, "-from", strconv.FormatUint(evt.Seq, 10))
	if code != 0 || errOut != "" {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
	for _, raw := range []string{"\u009b", "\u202e", "\u202c", "\u200f", "\u061c", "\u2066", "\u2069", "\u007f"} {
		if strings.Contains(out, raw) {
			t.Errorf("text printed %U raw:\n%q", []rune(raw)[0], out)
		}
	}
	want := `"content":"red \u{9B}31m then \u{202E}esrever\u{202C} and \u{200F}\u{61C}\u{2066}x\u{2069} \u{7F}"`
	if !strings.Contains(out, want) {
		t.Errorf("text does not show the escapes:\n got %q\nwant it to hold %q", out, want)
	}

	code, out, errOut = runShow(t, trustHeads, "-dir", f.dir, "-json")
	var file bytes.Buffer
	for _, l := range f.fileLines(t) {
		file.Write(l)
		file.WriteByte('\n')
	}
	if code != 0 || errOut != "" || out != file.String() {
		t.Fatalf("-json: exit %d, stderr %q, equal to the file: %v", code, errOut, out == file.String())
	}
	if !strings.Contains(out, "\u009b") || !strings.Contains(out, "\u202e") {
		t.Fatal("-json did not carry the characters as the file holds them")
	}
}
