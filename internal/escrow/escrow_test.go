package escrow

import (
	"archive/tar"
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"filippo.io/age"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
)

// .
// .
// .

const goodPass = "correct horse battery staple"

var fixture struct {
	once        sync.Once
	keyFile     []byte
	snapshotKey []byte
	kp          *crypto.KeyPair
	sealed      []byte
	err         error
}

func sealedFixture(t *testing.T) (keyFile []byte, kp *crypto.KeyPair, sealed []byte) {
	t.Helper()
	fixture.once.Do(func() {
		dir, err := os.MkdirTemp("", "escrow-fixture-*")
		if err != nil {
			fixture.err = err
			return
		}
		defer os.RemoveAll(dir)
		kp, err := crypto.GenerateKeyPair()
		if err != nil {
			fixture.err = err
			return
		}
		path := filepath.Join(dir, "identity.sec")
		if _, err := crypto.SaveKeyPair(kp, path); err != nil {
			fixture.err = err
			return
		}
		fixture.kp = kp
		if fixture.keyFile, fixture.err = os.ReadFile(path); fixture.err != nil {
			return
		}
		hybrid, err := age.GenerateHybridIdentity()
		if err != nil {
			fixture.err = err
			return
		}
		fixture.snapshotKey = []byte("# the snapshot key\n" + hybrid.String() + "\n")
		fixture.sealed, fixture.err = Seal(Contents{IdentityKey: fixture.keyFile, SnapshotKey: fixture.snapshotKey}, []byte(goodPass))
	})
	if fixture.err != nil {
		t.Fatal(fixture.err)
	}
	return fixture.keyFile, fixture.kp, bytes.Clone(fixture.sealed)
}

// .
// .
// .
// .
func withWorkFactor(t *testing.T, sealed []byte, wf string) []byte {
	t.Helper()
	old := []byte(" 18\n")
	if bytes.Count(sealed[:200], old) != 1 {
		t.Fatalf("fixture: the header does not carry work factor 18 once: %q", sealed[:120])
	}
	return bytes.Replace(sealed, old, []byte(" "+wf+"\n"), 1)
}

// .
// .
// .
// .
func sealCheaply(t *testing.T, plaintext []byte) []byte {
	t.Helper()
	r, err := age.NewScryptRecipient(goodPass)
	if err != nil {
		t.Fatal(err)
	}
	r.SetWorkFactor(10)
	var out bytes.Buffer
	w, err := age.Encrypt(&out, r)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(plaintext); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

type member struct {
	name string
	data []byte
	kind byte
}

func tarOf(t *testing.T, members ...member) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, m := range members {
		kind := m.kind
		if kind == 0 {
			kind = tar.TypeReg
		}
		size := int64(len(m.data))
		if kind != tar.TypeReg {
			size = 0
		}
		if err := tw.WriteHeader(&tar.Header{Name: m.name, Mode: 0o600, Size: size, Typeflag: kind, Linkname: "identity.sec"}); err != nil {
			t.Fatal(err)
		}
		if kind == tar.TypeReg {
			tw.Write(m.data)
		}
	}
	tw.Close()
	return buf.Bytes()
}

// .
// .
func TestASealedKeyOpensToExactlyTheBytesThatWereSealed(t *testing.T) {
	keyFile, kp, sealed := sealedFixture(t)
	if bytes.Contains(sealed, keyFile[4:36]) {
		t.Fatal("THE PRIVATE KEY IS IN THE SEALED FILE IN THE CLEAR")
	}
	if !bytes.HasPrefix(sealed, []byte("age-encryption.org/v1\n-> scrypt ")) {
		t.Fatalf("not age's passphrase format — another tool could not open it: %q", sealed[:40])
	}
	got, opened, err := Open(sealed, []byte(goodPass))
	if err != nil {
		t.Fatal(err)
	}
	defer got.Wipe()
	if !bytes.Equal(got.IdentityKey, keyFile) || !bytes.Equal(got.SnapshotKey, fixture.snapshotKey) {
		t.Fatal("the bytes that came back are not the bytes that were sealed")
	}
	if bytes.Contains(sealed, fixture.snapshotKey[20:60]) {
		t.Fatal("THE SNAPSHOT KEY IS IN THE SEALED FILE IN THE CLEAR")
	}
	if opened.Fingerprint() != kp.Fingerprint() {
		t.Fatalf("opened %s, sealed %s", opened.Fingerprint(), kp.Fingerprint())
	}
	msg := []byte("a record the restored key signs")
	sig, err := crypto.Sign(opened, msg)
	if err != nil {
		t.Fatal(err)
	}
	if err := crypto.Verify(kp.PublicKeyBytes(), msg, sig); err != nil {
		t.Fatalf("the restored key's signature does not verify under the original's public key: %v", err)
	}
	if err := Challenge(opened); err != nil {
		t.Fatal(err)
	}
}

func TestTheWrongPassphraseOpensNothing(t *testing.T) {
	_, _, sealed := sealedFixture(t)
	got, kp, err := Open(sealed, []byte("correct horse battery stable"))
	if !errors.Is(err, ErrWrongPassphrase) || !errors.Is(err, age.ErrIncorrectIdentity) || got.IdentityKey != nil || kp != nil {
		t.Fatalf("want ErrWrongPassphrase (keeping the library's identity) and nothing returned, got %v %v %v", got.IdentityKey != nil, kp != nil, err)
	}
}

// .
// .
// .
func TestTheWorkFactorIsPinnedBothWays(t *testing.T) {
	_, _, sealed := sealedFixture(t)

	t.Run("19: the library's ceiling, with no floor in the way", func(t *testing.T) {
		_, _, err := decrypt(withWorkFactor(t, sealed, "19"), []byte(goodPass))
		if err == nil || !strings.Contains(err.Error(), "work factor too large") {
			t.Fatalf("a file demanding work factor 19 was not refused by the ceiling before its derivation: %v", err)
		}
	})
	t.Run("19: through Open", func(t *testing.T) {
		if _, _, err := Open(withWorkFactor(t, sealed, "19"), []byte(goodPass)); err == nil || !strings.Contains(err.Error(), "work factor too large") {
			t.Fatalf("%v", err)
		}
	})
	t.Run("17: our floor, before the library is asked", func(t *testing.T) {
		_, _, err := Open(withWorkFactor(t, sealed, "17"), []byte(goodPass))
		if !errors.Is(err, ErrTooWeak) {
			t.Fatalf("a file below the work factor this tool writes was not refused as weak: %v", err)
		}
	})
	// .
	// .
	t.Run("a genuinely weak escrow that the right passphrase opens", func(t *testing.T) {
		keyFile, _, _ := sealedFixture(t)
		weak := sealCheaply(t, tarOf(t, member{name: MemberIdentityKey, data: keyFile}))
		if _, _, err := decrypt(weak, []byte(goodPass)); err != nil {
			t.Fatalf("fixture: the library itself refuses it (%v) — then this case proves nothing", err)
		}
		if _, _, err := Open(weak, []byte(goodPass)); !errors.Is(err, ErrTooWeak) {
			t.Fatalf("A WEAKLY SEALED KEY OPENED WITHOUT A WORD: %v", err)
		}
	})
}

func TestATamperedEscrowOpensNothing(t *testing.T) {
	_, _, sealed := sealedFixture(t)
	t.Run("the header", func(t *testing.T) {
		b := bytes.Clone(sealed)
		i := bytes.Index(b, []byte("-> scrypt ")) + len("-> scrypt ") + 3
		if b[i] == 'A' {
			b[i] = 'B'
		} else {
			b[i] = 'A'
		}
		if got, _, err := Open(b, []byte(goodPass)); err == nil || got.IdentityKey != nil {
			t.Fatal("A TAMPERED HEADER OPENED")
		}
	})
	t.Run("the ciphertext", func(t *testing.T) {
		b := bytes.Clone(sealed)
		b[len(b)-20] ^= 0x01
		if got, _, err := Open(b, []byte(goodPass)); err == nil || got.IdentityKey != nil {
			t.Fatal("A TAMPERED CIPHERTEXT OPENED")
		}
	})
	t.Run("not an escrow at all", func(t *testing.T) {
		for _, junk := range [][]byte{nil, []byte("hello"), []byte("age-encryption.org/v1\n-> X25519 abc\nxyz\n--- mac\n")} {
			if _, _, err := Open(junk, []byte(goodPass)); !errors.Is(err, ErrNotAnEscrow) {
				t.Fatalf("%q: %v", junk, err)
			}
		}
	})
}

// .
// .
func TestAnOversizedEscrowIsRefused(t *testing.T) {
	keyFile, _, _ := sealedFixture(t)
	if _, _, err := Open(make([]byte, MaxSealedBytes+1), []byte(goodPass)); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("an oversized input: %v", err)
	}
	if _, err := Seal(Contents{IdentityKey: keyFile, SnapshotKey: make([]byte, MaxMemberBytes+1)}, []byte(goodPass)); err == nil {
		t.Fatal("an oversized member was sealed")
	}
	big := sealCheaply(t, tarOf(t, member{name: MemberIdentityKey, data: make([]byte, MaxMemberBytes+1)}))
	if _, _, err := decrypt(big, []byte(goodPass)); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("a member past the bound: %v", err)
	}
}

// .
// .
// .
// .
func TestOnlyAnEscrowsOwnMembersOpenAndOnlyKeysAreSealed(t *testing.T) {
	keyFile, _, _ := sealedFixture(t)
	for name, archive := range map[string][]byte{
		"an unexpected file":         tarOf(t, member{name: MemberIdentityKey, data: keyFile}, member{name: "notes.txt", data: []byte("hi")}),
		"a path, not a name":         tarOf(t, member{name: "../" + MemberIdentityKey, data: keyFile}),
		"the key twice":              tarOf(t, member{name: MemberIdentityKey, data: keyFile}, member{name: MemberIdentityKey, data: keyFile}),
		"a link where a file is":     tarOf(t, member{name: MemberIdentityKey, data: keyFile}, member{name: MemberSnapshotKey, kind: tar.TypeSymlink}),
		"no signing key":             tarOf(t, member{name: MemberSnapshotKey, data: fixture.snapshotKey}),
		"not an archive":             []byte("just some bytes that are not a tar archive at all, long enough to be read as a header ........................................................................................................................................................................................................................................................................................................................................................................................................................................................................."),
		"a signing key that is not":  tarOf(t, member{name: MemberIdentityKey, data: []byte("not a key file")}),
		"a snapshot key that is not": tarOf(t, member{name: MemberIdentityKey, data: keyFile}, member{name: MemberSnapshotKey, data: []byte("AGE-SECRET-KEY-1NOTAPOSTQUANTUMKEY\n")}),
	} {
		t.Run(name, func(t *testing.T) {
			c, kp, err := decrypt(sealCheaply(t, archive), []byte(goodPass))
			if err == nil || kp != nil || c.IdentityKey != nil || c.SnapshotKey != nil {
				t.Fatalf("it opened: %v", err)
			}
		})
	}

	if _, err := Seal(Contents{IdentityKey: []byte("not a key file at all")}, []byte(goodPass)); err == nil {
		t.Fatal("bytes that are not a key file were sealed")
	}
	swapped := bytes.Clone(keyFile)
	swapped[len(swapped)-1] ^= 0x01
	if _, err := Seal(Contents{IdentityKey: swapped}, []byte(goodPass)); err == nil || !strings.Contains(err.Error(), "does not match the private key") {
		t.Fatalf("a key file whose public half is not its private half's was sealed: %v", err)
	}
	if _, err := Seal(Contents{IdentityKey: keyFile, SnapshotKey: []byte("not an age identity")}, []byte(goodPass)); err == nil {
		t.Fatal("a snapshot key that is not one was sealed")
	}
	if _, err := Seal(Contents{IdentityKey: keyFile}, nil); err == nil {
		t.Fatal("a key was sealed under an empty passphrase")
	}
}

// .
// .
func TestAnEscrowWithoutASnapshotKeyIsTheSameFormat(t *testing.T) {
	keyFile, kp, _ := sealedFixture(t)
	one := sealCheaply(t, tarOf(t, member{name: MemberIdentityKey, data: keyFile}))
	c, opened, err := decrypt(one, []byte(goodPass))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(c.IdentityKey, keyFile) || c.SnapshotKey != nil || opened.Fingerprint() != kp.Fingerprint() {
		t.Fatalf("a one-member escrow did not open as one: %+v", c.SnapshotKey)
	}
}

// .
// .
// .
func TestTheEscrowReceiptVerifiesOnlyForThisIdentityAndThisRecipient(t *testing.T) {
	_, kp, _ := sealedFixture(t)
	recipient, err := SnapshotRecipient(fixture.snapshotKey)
	if err != nil || !strings.HasPrefix(recipient, "age1pq1") {
		t.Fatalf("recipient %q: %v", recipient, err)
	}
	r, err := SignReceipt(kp, recipient, time.Date(2026, 9, 19, 4, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Verify(kp.PublicKeyBytes(), recipient); err != nil {
		t.Fatalf("a good receipt does not verify: %v", err)
	}
	other, _ := crypto.GenerateKeyPair()
	rotated, _ := age.GenerateHybridIdentity()
	for name, tc := range map[string]struct {
		r         Receipt
		pub       []byte
		recipient string
	}{
		"another identity's key":    {r, other.PublicKeyBytes(), recipient},
		"a rotated snapshot key":    {r, kp.PublicKeyBytes(), rotated.Recipient().String()},
		"no recipient on the host":  {r, kp.PublicKeyBytes(), ""},
		"the recipient rewritten":   {Receipt{r.Identity, rotated.Recipient().String(), r.CheckedAt, r.Sig}, kp.PublicKeyBytes(), rotated.Recipient().String()},
		"the time rewritten":        {Receipt{r.Identity, r.SnapshotRecipient, "2030-01-01T00:00:00Z", r.Sig}, kp.PublicKeyBytes(), recipient},
		"the identity rewritten":    {Receipt{other.Fingerprint(), r.SnapshotRecipient, r.CheckedAt, r.Sig}, other.PublicKeyBytes(), recipient},
		"no signature":              {Receipt{r.Identity, r.SnapshotRecipient, r.CheckedAt, ""}, kp.PublicKeyBytes(), recipient},
		"a time that is not a time": {Receipt{r.Identity, r.SnapshotRecipient, "yesterday", r.Sig}, kp.PublicKeyBytes(), recipient},
	} {
		if err := tc.r.Verify(tc.pub, tc.recipient); !errors.Is(err, ErrReceipt) {
			t.Errorf("%s: the receipt verified (%v)", name, err)
		}
	}
	if _, err := SignReceipt(kp, "", time.Now()); !errors.Is(err, ErrReceipt) {
		t.Fatalf("a receipt was signed with no snapshot key to attest: %v", err)
	}
}

func TestWipeClearsTheBufferItIsGiven(t *testing.T) {
	b := []byte("secret bytes")
	Wipe(b)
	if !bytes.Equal(b, make([]byte, len(b))) {
		t.Fatalf("%q", b)
	}
}

// .
// .
// .
// .
// .
// .
// .
func TestNothingRidesInBehindTheArchivesEnd(t *testing.T) {
	keyFile, _, _ := sealedFixture(t)
	one := tarOf(t, member{name: MemberIdentityKey, data: keyFile})
	two := tarOf(t, member{name: MemberSnapshotKey, data: fixture.snapshotKey})

	for name, plaintext := range map[string][]byte{
		"a second archive joined on, holding the snapshot key": append(append([]byte{}, one...), two...),
		"a payload after the marker":                           append(append([]byte{}, one...), bytes.Repeat([]byte("payload "), 800)...),
		"one stray byte after the padding":                     append(append(append([]byte{}, one...), make([]byte, 4096)...), 1),
	} {
		t.Run(name, func(t *testing.T) {
			c, kp, err := decrypt(sealCheaply(t, plaintext), []byte(goodPass))
			if !errors.Is(err, ErrNotAnEscrow) || kp != nil || c.IdentityKey != nil {
				t.Fatalf("it opened (identity %d bytes, snapshot %d bytes): %v", len(c.IdentityKey), len(c.SnapshotKey), err)
			}
		})
	}
	// .
	// .
	padded := append(append([]byte{}, tarOf(t, member{name: MemberIdentityKey, data: keyFile}, member{name: MemberSnapshotKey, data: fixture.snapshotKey})...), make([]byte, 10240)...)
	c, kp, err := decrypt(sealCheaply(t, padded), []byte(goodPass))
	if err != nil || kp == nil || !bytes.Equal(c.SnapshotKey, fixture.snapshotKey) {
		t.Fatalf("a zero-padded archive — what stock tar writes — did not open: %v", err)
	}
}

// .
func TestTheSnapshotKeyAndTheReceiptStandBesideTheIdentityKey(t *testing.T) {
	if got := SnapshotKeyPath("/srv/keys/identity.sec"); got != "/srv/keys/"+SnapshotKeyFileName {
		t.Fatalf("SnapshotKeyPath = %s", got)
	}
	if got := ReceiptPath("/srv/keys/identity.sec"); got != "/srv/keys/"+ReceiptFileName {
		t.Fatalf("ReceiptPath = %s", got)
	}
}
