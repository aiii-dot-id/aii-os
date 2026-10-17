package tools

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

// A string offset must be WHOLLY a number. Sscanf read as far as it
// could and called that success, so "5abc" reached the pager as 5 —
// the same truncating read the float branch refuses, by another door.
func TestMalformedStringOffsetIsRefused(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(path, []byte("a\nb\nc\nd\ne\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	rt := &ReadTool{maxBytes: 51200}
	for _, bad := range []string{"5abc", "5.7", "0x10", "5 6", "abc", "--5"} {
		res, _ := rt.Execute(context.Background(), map[string]any{
			"file_path": path, "offset": bad,
		})
		if res.Error == "" {
			t.Errorf("offset %q was accepted; output=%q", bad, res.Output)
		}
	}
	// And the well-formed spellings still work, including surrounding space.
	for _, good := range []string{"2", " 3 "} {
		res, _ := rt.Execute(context.Background(), map[string]any{
			"file_path": path, "offset": good,
		})
		if res.Error != "" {
			t.Errorf("offset %q was refused: %s", good, res.Error)
		}
	}
}

func TestAnOversizedLineNamesItsContinuation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "big.txt")
	long := strings.Repeat("x", 500)
	if err := os.WriteFile(path, []byte(long+"\nsecond line\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	rt := &ReadTool{maxBytes: 100}
	res, _ := rt.Execute(context.Background(), map[string]any{"file_path": path})
	if !res.Truncated {
		t.Error("an oversized line must set Truncated")
	}
	if !strings.Contains(res.Output, "byte_offset 100") {
		t.Errorf("the continuation position is not named: %q", res.Output)
	}

	if !strings.Contains(res.Output, "bytes 0–100 shown") {
		t.Errorf("the result does not say how much of the line was shown: %q", res.Output)
	}
}

// THE PROOF: read a line several times the cap using ONLY the offsets
// the tool reports, concatenate, and require the original back byte for
// byte. Multibyte content throughout, so a boundary error cannot hide.
func TestAnOversizedLineReconstructsExactlyFromItsContinuations(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wide.txt")
	// 3-byte runes and a 4-byte rune, so no cap lands on a boundary by luck.
	line := strings.Repeat("日本語", 300) + strings.Repeat("😀", 50)
	if err := os.WriteFile(path, []byte(line+"\ntail\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	rt := &ReadTool{maxBytes: 97} // not a multiple of 3 or 4

	next := regexp.MustCompile(`byte_offset (\d+)`)
	var rebuilt strings.Builder
	byteOffset, guard := 0, 0
	for {
		guard++
		if guard > 500 {
			t.Fatal("continuations never terminated — the offset is not advancing")
		}
		args := map[string]any{"file_path": path, "offset": 1}
		if byteOffset > 0 {
			args["byte_offset"] = byteOffset
		}
		res, _ := rt.Execute(context.Background(), args)
		if res.Error != "" {
			t.Fatalf("chunk at byte_offset %d refused: %s", byteOffset, res.Error)
		}
		body := res.Output
		if i := strings.LastIndex(body, "\n["); i >= 0 {
			body = body[:i] // drop the declaration, keep the content
		}
		// Every chunk of the line carries the line's own number.
		if !strings.HasPrefix(body, "     1\t") {
			t.Fatalf("chunk at byte_offset %d lost its line number: %q", byteOffset, body[:min(len(body), 20)])
		}
		body = strings.TrimPrefix(body, "     1\t")
		rebuilt.WriteString(body)
		if !utf8.ValidString(body) {
			t.Fatalf("chunk at byte_offset %d is not valid UTF-8", byteOffset)
		}
		if strings.ContainsRune(body, utf8.RuneError) {
			t.Fatalf("chunk at byte_offset %d introduced U+FFFD", byteOffset)
		}
		m := next.FindStringSubmatch(res.Output)
		if m == nil {
			break // the tool stopped naming a continuation: the line is done
		}
		n, err := strconv.Atoi(m[1])
		if err != nil {
			t.Fatal(err)
		}
		if n <= byteOffset {
			t.Fatalf("continuation did not advance: %d -> %d", byteOffset, n)
		}
		byteOffset = n
	}
	if got := rebuilt.String(); got != line {
		t.Errorf("reconstruction is not byte-exact: got %d bytes, want %d", len(got), len(line))
	}
}

// A byte_offset that lands inside a character is refused, not silently
// moved: the caller got the number from a previous page, so a bad one
// means something is wrong that correcting quietly would hide.
func TestAByteOffsetInsideACharacterIsRefused(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "utf8.txt")
	if err := os.WriteFile(path, []byte(strings.Repeat("日", 200)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	rt := &ReadTool{maxBytes: 100}
	res, _ := rt.Execute(context.Background(), map[string]any{
		"file_path": path, "offset": 1, "byte_offset": 1, // mid-rune
	})
	if res.Error == "" {
		t.Errorf("a mid-character byte_offset was accepted: %q", res.Output)
	}
}

// A cancelled walk returned what it had found with Truncated FALSE, so
// a partial search read as a complete one.
func TestCancelledGrepIsNotReportedAsComplete(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 40; i++ {
		if err := os.WriteFile(filepath.Join(dir, string(rune('a'+i%26))+"_.txt"),
			[]byte("nothing to find here\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already over before the walk begins
	g := &GrepTool{}
	res, _ := g.Execute(ctx, map[string]any{"pattern": "zzz-absent", "path": dir})
	if !res.Truncated {
		t.Error("a cancelled search must not report as a complete one")
	}
	if !strings.Contains(res.Output, "cancelled") {
		t.Errorf("cancellation is not disclosed: %q", res.Output)
	}
}

// "No matches" is only worth the ground the walk covered, and .git is
// ground it deliberately does not cover.
func TestNegativeDisclosesTreesSkippedByPolicy(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git", "config"), []byte("needle\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plain.txt"), []byte("hay\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	g := &GrepTool{}
	res, _ := g.Execute(context.Background(), map[string]any{"pattern": "needle", "path": dir})
	if !strings.Contains(res.Output, "skipped by policy") {
		t.Errorf("the negative hides the tree it never entered: %q", res.Output)
	}
}

// The byte cap must land on a rune boundary. Cutting mid-character
// hands back a replacement char that was never in the file — a
// corruption introduced BY the tool that exists to show what is there.
func TestByteCapDoesNotSplitARune(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "utf8.txt")
	// Three-byte runes, so a cap at 100 cannot land on a boundary
	// unless something makes it: 100 is not divisible by 3.
	line := strings.Repeat("あ", 200)
	if err := os.WriteFile(path, []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	rt := &ReadTool{maxBytes: 100}
	res, _ := rt.Execute(context.Background(), map[string]any{"file_path": path})
	shown := res.Output
	if i := strings.Index(shown, "\n["); i >= 0 {
		shown = shown[:i] // drop the declaration, keep the content
	}
	if !utf8.ValidString(shown) {
		t.Errorf("truncation produced invalid UTF-8: %q", shown)
	}
	if strings.ContainsRune(shown, utf8.RuneError) {
		t.Errorf("truncation introduced U+FFFD, which was never in the file: %q", shown)
	}
}

// A binary file is skipped, and the skip is part of the coverage the
// answer is worth.
func TestBinaryOmissionIsDisclosed(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "prog.bin"), []byte{0x7f, 'E', 'L', 'F', 0x00, 0x01, 'n', 'e', 'e', 'd', 'l', 'e'}, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plain.txt"), []byte("needle here\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	g := &GrepTool{}
	res, _ := g.Execute(context.Background(), map[string]any{"pattern": "needle", "path": dir})
	// A POSITIVE result must carry the caveat too: "one match" from a
	// walk that never opened the binary is a different claim.
	if !strings.Contains(res.Output, "binary file(s) not searched") {
		t.Errorf("a positive answer hides the binary it never read: %q", res.Output)
	}
}

// The substrate floor's refusals are counted, and never named.
func TestProtectedPathsAreDisclosedButNotNamed(t *testing.T) {
	dir := t.TempDir()
	secret := filepath.Join(dir, "providers.json")
	// Distinctive content, so a leak is unmistakable and cannot be
	// confused with the pattern being echoed back in the answer.
	if err := os.WriteFile(secret, []byte("needle sk-LEAKCANARY-9931\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ok.txt"), []byte("hay\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	g := &GrepTool{deny: func(_ context.Context, p string) bool { return strings.HasSuffix(p, "providers.json") }}
	res, _ := g.Execute(context.Background(), map[string]any{"pattern": "needle", "path": dir})
	if !strings.Contains(res.Output, "protected by the substrate floor") {
		t.Errorf("the protected omission is not disclosed: %q", res.Output)
	}
	if strings.Contains(res.Output, "providers.json") {
		t.Errorf("the protected path was NAMED, handing back what the floor refused: %q", res.Output)
	}
	if strings.Contains(res.Output, "LEAKCANARY") {
		t.Errorf("the protected file's CONTENT leaked: %q", res.Output)
	}
}
