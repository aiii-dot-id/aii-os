package escrow

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"
)

// .
// .
func provedSet(t *testing.T) (dir string, names []string, sums map[string]string) {
	t.Helper()
	dir = t.TempDir()
	files := map[string][]byte{
		"ledger.jsonl":        []byte("{\"entry\":1}\n{\"entry\":2}\n"),
		"aii.db":              bytes.Repeat([]byte("every conversation, in the clear "), 40000),
		"witness-keys/k.json": []byte(`{"key":"public"}`),
	}
	names = []string{"ledger.jsonl", "aii.db", "witness-keys/k.json"}
	sums = map[string]string{}
	for _, n := range names {
		p := filepath.Join(dir, filepath.FromSlash(n))
		os.MkdirAll(filepath.Dir(p), 0o700)
		if err := os.WriteFile(p, files[n], 0o600); err != nil {
			t.Fatal(err)
		}
		h := sha256.Sum256(files[n])
		sums[n] = hex.EncodeToString(h[:])
	}
	return dir, names, sums
}

func snapshotKey(t *testing.T) (key []byte, recipient string) {
	t.Helper()
	key, err := NewSnapshotKey()
	if err != nil {
		t.Fatal(err)
	}
	recipient, err = SnapshotRecipient(key)
	if err != nil || !strings.HasPrefix(recipient, "age1pq1") {
		t.Fatalf("a new snapshot key is not one post-quantum identity: %q %v", recipient, err)
	}
	return key, recipient
}

// .
// .
// .
// .
func TestAnEncryptedSnapshotIsAgesOwnFormatAroundATar(t *testing.T) {
	dir, names, sums := provedSet(t)
	key, recipient := snapshotKey(t)
	dst := filepath.Join(t.TempDir(), "ledger-20260919T040000Z-seq2"+SnapshotSuffix)
	if err := EncryptSnapshot(dir, names, recipient, dst); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(dst)
	if bytes.Contains(raw, []byte("every conversation")) || bytes.Contains(raw, []byte("ledger.jsonl")) {
		t.Fatal("THE SNAPSHOT, OR ITS FILE NAMES, ARE IN THE FILE IN THE CLEAR")
	}
	if info, _ := os.Stat(dst); info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("readable by others: %v", info.Mode())
	}
	ids, err := age.ParseIdentities(bytes.NewReader(key))
	if err != nil {
		t.Fatal(err)
	}
	plain, err := age.Decrypt(bytes.NewReader(raw), ids...)
	if err != nil {
		t.Fatalf("age itself does not open it: %v", err)
	}
	tr := tar.NewReader(plain)
	for i := 0; ; i++ {
		h, err := tr.Next()
		if err == io.EOF {
			if i != len(names) {
				t.Fatalf("%d members, want %d", i, len(names))
			}
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(tr)
		sum := sha256.Sum256(body)
		if h.Name != names[i] || hex.EncodeToString(sum[:]) != sums[h.Name] || h.Uid != 0 || h.Uname != "" || !h.ModTime.IsZero() && h.ModTime.Unix() != 0 {
			t.Fatalf("member %d: %q uid=%d uname=%q mtime=%v", i, h.Name, h.Uid, h.Uname, h.ModTime)
		}
	}
	if err := VerifySnapshot(dst, key, names, sums); err != nil {
		t.Fatalf("the verifier refuses a good snapshot: %v", err)
	}
	out := filepath.Join(t.TempDir(), "opened")
	got, err := OpenSnapshot(dst, key, out)
	if err != nil || strings.Join(got, ",") != strings.Join(names, ",") {
		t.Fatalf("open: %v %v", got, err)
	}
	for _, n := range names {
		a, _ := os.ReadFile(filepath.Join(dir, filepath.FromSlash(n)))
		b, err := os.ReadFile(filepath.Join(out, filepath.FromSlash(n)))
		if err != nil || !bytes.Equal(a, b) {
			t.Fatalf("%s did not come back byte for byte: %v", n, err)
		}
	}
}

// .
// .
// .
func TestTheVerifierHoldsTheCiphertextToTheProvedSet(t *testing.T) {
	dir, names, sums := provedSet(t)
	key, recipient := snapshotKey(t)
	good := filepath.Join(t.TempDir(), "good"+SnapshotSuffix)
	if err := EncryptSnapshot(dir, names, recipient, good); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(good)
	write := func(b []byte) string {
		p := filepath.Join(t.TempDir(), "x"+SnapshotSuffix)
		if err := os.WriteFile(p, b, 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	flipped := bytes.Clone(raw)
	flipped[len(flipped)/2] ^= 0x01
	otherKey, _ := snapshotKey(t)
	wrongSums := map[string]string{}
	for k, v := range sums {
		wrongSums[k] = v
	}
	wrongSums["aii.db"] = strings.Repeat("0", 64)

	for name, tc := range map[string]struct {
		src   string
		key   []byte
		names []string
		sums  map[string]string
	}{
		"a flipped bit":              {write(flipped), key, names, sums},
		"cut short":                  {write(raw[:len(raw)-100]), key, names, sums},
		"cut at a chunk boundary":    {write(raw[:len(raw)/2]), key, names, sums},
		"another snapshot key":       {good, otherKey, names, sums},
		"a file that is not the one": {good, key, names, wrongSums},
		"a member the set lacks":     {good, key, names[:2], sums},
		"a member it should hold":    {good, key, append(append([]string{}, names...), "RECEIPT.json"), sums},
		"members out of order":       {good, key, []string{names[1], names[0], names[2]}, sums},
	} {
		if err := VerifySnapshot(tc.src, tc.key, tc.names, tc.sums); err == nil {
			t.Errorf("%s: THE VERIFIER PASSED IT", name)
		}
	}
	// .
	// .
	// .
	// .
	// .
	// .
	{
		ownKey, ownRecipient := snapshotKey(t)
		rcpt, _ := age.ParseHybridRecipient(ownRecipient)
		var buf bytes.Buffer
		w, _ := age.Encrypt(&buf, rcpt)
		tw := tar.NewWriter(w)
		for _, n := range names {
			body, _ := os.ReadFile(filepath.Join(dir, filepath.FromSlash(n)))
			tw.WriteHeader(&tar.Header{Name: n, Mode: 0o600, Size: int64(len(body)), Typeflag: tar.TypeReg})
			tw.Write(body)
		}
		tw.Close()
		w.Write(bytes.Repeat([]byte{0}, 300<<10))
		w.Close()
		trailing := buf.Bytes()
		if err := VerifySnapshot(write(trailing), ownKey, names, sums); err != nil {
			t.Fatalf("fixture: the undamaged file with a tail does not verify: %v", err)
		}
		damaged := bytes.Clone(trailing)
		damaged[len(damaged)-1000] ^= 0x01
		if err := VerifySnapshot(write(damaged), ownKey, names, sums); err == nil {
			t.Error("a flipped bit past the archive's end: THE VERIFIER PASSED A CIPHERTEXT IT NEVER FINISHED READING")
		}
	}
	if err := VerifySnapshot(good, key, names, wrongSums); !errors.Is(err, ErrSnapshotMismatch) {
		t.Errorf("a mismatch is not named as one: %v", err)
	}
}

// .
// .
// .
func TestOpeningASnapshotNeverWritesOutsideItsDirectory(t *testing.T) {
	key, recipient := snapshotKey(t)
	r, _ := age.ParseHybridRecipient(recipient)
	hostile := func(h tar.Header, body string) string {
		var buf bytes.Buffer
		w, _ := age.Encrypt(&buf, r)
		tw := tar.NewWriter(w)
		h.Size = int64(len(body))
		if h.Typeflag != tar.TypeReg {
			h.Size = 0
		}
		tw.WriteHeader(&h)
		if h.Typeflag == tar.TypeReg {
			tw.Write([]byte(body))
		}
		tw.Close()
		w.Close()
		p := filepath.Join(t.TempDir(), "hostile"+SnapshotSuffix)
		os.WriteFile(p, buf.Bytes(), 0o600)
		return p
	}
	for name, h := range map[string]tar.Header{
		"climbs out":      {Name: "../escaped", Typeflag: tar.TypeReg, Mode: 0o600},
		"climbs out deep": {Name: "a/../../escaped", Typeflag: tar.TypeReg, Mode: 0o600},
		"absolute":        {Name: "/tmp/escaped-by-aii-test", Typeflag: tar.TypeReg, Mode: 0o600},
		"a link":          {Name: "aii.db", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"},
		"a backslash":     {Name: `a\..\..\escaped`, Typeflag: tar.TypeReg, Mode: 0o600},
	} {
		parent := t.TempDir()
		out := filepath.Join(parent, "opened")
		if _, err := OpenSnapshot(hostile(h, "x"), key, out); err == nil {
			t.Errorf("%s: it opened", name)
		}
		if entries, _ := os.ReadDir(parent); len(entries) != 0 {
			t.Errorf("%s: a refused open left %v behind", name, entries)
		}
	}
	taken := t.TempDir()
	if _, err := OpenSnapshot(hostile(tar.Header{Name: "ok", Typeflag: tar.TypeReg, Mode: 0o600}, "x"), key, taken); err == nil {
		t.Error("a snapshot was unpacked over an existing directory")
	}
	if _, err := os.Stat(taken); err != nil {
		t.Error("the existing directory was removed by a refused open")
	}
}

func TestEncryptingNeverOverwritesAndLeavesNothingOnFailure(t *testing.T) {
	dir, names, _ := provedSet(t)
	_, recipient := snapshotKey(t)
	dst := filepath.Join(t.TempDir(), "taken"+SnapshotSuffix)
	os.WriteFile(dst, []byte("someone else's"), 0o600)
	if err := EncryptSnapshot(dir, names, recipient, dst); err == nil {
		t.Fatal("an existing file was written over")
	}
	if b, _ := os.ReadFile(dst); string(b) != "someone else's" {
		t.Fatal("the existing file was changed")
	}
	gone := filepath.Join(t.TempDir(), "gone"+SnapshotSuffix)
	if err := EncryptSnapshot(dir, append(names, "not-there"), recipient, gone); err == nil {
		t.Fatal("a missing member was not an error")
	}
	if _, err := os.Stat(gone); !os.IsNotExist(err) {
		t.Fatal("a failed encryption left a partial snapshot")
	}
	if err := EncryptSnapshot(dir, names, "age1notapostquantumrecipient", filepath.Join(t.TempDir(), "x")); err == nil {
		t.Fatal("a recipient that is not the post-quantum one was accepted")
	}
}

// .
// .
func sealTarStream(t *testing.T, recipient string, plaintext []byte) string {
	t.Helper()
	recipients, err := age.ParseRecipients(strings.NewReader(recipient + "\n"))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	w, err := age.Encrypt(&out, recipients...)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(plaintext); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "handmade"+SnapshotSuffix)
	if err := os.WriteFile(p, out.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func tarStream(t *testing.T, dir string, names []string) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, n := range names {
		body, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(n)))
		if err != nil {
			t.Fatal(err)
		}
		if err := tw.WriteHeader(&tar.Header{Name: n, Mode: 0o600, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		tw.Write(body)
	}
	tw.Close()
	return buf.Bytes()
}

// .
// .
// .
// .
// .
// .
// .
// .
// .
func TestNothingFollowsASnapshotsArchive(t *testing.T) {
	dir, names, sums := provedSet(t)
	key, recipient := snapshotKey(t)
	proved := tarStream(t, dir, names)

	extraDir := t.TempDir()
	os.WriteFile(filepath.Join(extraDir, "unverified.txt"), []byte("a file no checksum list names"), 0o600)
	second := tarStream(t, extraDir, []string{"unverified.txt"})

	for name, plaintext := range map[string][]byte{
		"a second archive joined on":   append(append([]byte{}, proved...), second...),
		"a payload after the marker":   append(append([]byte{}, proved...), bytes.Repeat([]byte("payload "), 2000)...),
		"one stray byte after padding": append(append(append([]byte{}, proved...), make([]byte, 8192)...), 1),
	} {
		src := sealTarStream(t, recipient, plaintext)
		if err := VerifySnapshot(src, key, names, sums); err == nil {
			t.Errorf("%s: THE VERIFIER PASSED IT", name)
		}
		out := filepath.Join(t.TempDir(), "opened")
		if _, err := OpenSnapshot(src, key, out); err == nil {
			t.Errorf("%s: it OPENED", name)
		}
		if _, err := os.Stat(out); !os.IsNotExist(err) {
			t.Errorf("%s: a refused open left %s behind", name, out)
		}
	}
	// .
	// .
	padded := sealTarStream(t, recipient, append(append([]byte{}, proved...), make([]byte, 10240)...))
	if err := VerifySnapshot(padded, key, names, sums); err != nil {
		t.Fatalf("a zero-padded archive — what stock tar writes — did not verify: %v", err)
	}
}
