// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
package escrow

import (
	"archive/tar"
	"bufio"
	"bytes"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"filippo.io/age"

	"github.com/aiii-dot-id/aii-os/internal/atomicfile"
	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/fileperm"
)

// .
// .
const (
	// .
	// .
	// .
	// .
	WorkFactor = 18

	// .
	// .
	// .
	// .
	MaxSealedBytes = 64 << 10
	MaxMemberBytes = 16 << 10

	// .
	// .
	// .
	MemberIdentityKey = "identity.sec"
	MemberSnapshotKey = "snapshot.key"
)

// .
type Contents struct {
	// .
	// .
	IdentityKey []byte
	// .
	// .
	SnapshotKey []byte
}

// .
func (c *Contents) Wipe() {
	Wipe(c.IdentityKey)
	Wipe(c.SnapshotKey)
}

var (
	// .
	// .
	ErrTooWeak = errors.New("escrow is protected more weakly than this tool writes")
	// .
	ErrTooLarge = errors.New("escrow is larger than any key file")
	// .
	ErrNotAnEscrow = errors.New("not a passphrase-sealed key file")
	// .
	ErrWrongPassphrase = errors.New("the passphrase does not open this escrow")
)

// .
// .
// .
// .
// .
func Seal(c Contents, passphrase []byte) ([]byte, error) {
	if _, err := crypto.ParseKeyPair(c.IdentityKey); err != nil {
		return nil, fmt.Errorf("refusing to seal: %w", err)
	}
	if c.SnapshotKey != nil {
		if _, err := SnapshotRecipient(c.SnapshotKey); err != nil {
			return nil, fmt.Errorf("refusing to seal: %w", err)
		}
	}
	var archive bytes.Buffer
	tw := tar.NewWriter(&archive)
	for _, m := range []struct {
		name string
		data []byte
	}{{MemberIdentityKey, c.IdentityKey}, {MemberSnapshotKey, c.SnapshotKey}} {
		if m.data == nil {
			continue
		}
		if len(m.data) > MaxMemberBytes {
			return nil, fmt.Errorf("%w: %s is %d bytes", ErrTooLarge, m.name, len(m.data))
		}
		// .
		// .
		if err := tw.WriteHeader(&tar.Header{Name: m.name, Mode: 0o600, Size: int64(len(m.data)), Typeflag: tar.TypeReg, Format: tar.FormatUSTAR}); err != nil {
			return nil, fmt.Errorf("seal: %w", err)
		}
		if _, err := tw.Write(m.data); err != nil {
			return nil, fmt.Errorf("seal: %w", err)
		}
	}
	if err := tw.Close(); err != nil {
		return nil, fmt.Errorf("seal: %w", err)
	}
	defer Wipe(archive.Bytes())

	// .
	// .
	// .
	r, err := age.NewScryptRecipient(string(passphrase))
	if err != nil {
		return nil, err
	}
	r.SetWorkFactor(WorkFactor)
	var out bytes.Buffer
	w, err := age.Encrypt(&out, r)
	if err != nil {
		return nil, fmt.Errorf("seal: %w", err)
	}
	if _, err := w.Write(archive.Bytes()); err != nil {
		return nil, fmt.Errorf("seal: %w", err)
	}
	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("seal: %w", err)
	}
	return out.Bytes(), nil
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
// .
// .
func Open(sealed, passphrase []byte) (Contents, *crypto.KeyPair, error) {
	if len(sealed) > MaxSealedBytes {
		return Contents{}, nil, fmt.Errorf("%w: %d bytes sealed", ErrTooLarge, len(sealed))
	}
	wf, err := workFactor(sealed)
	if err != nil {
		return Contents{}, nil, err
	}
	if wf < WorkFactor {
		return Contents{}, nil, fmt.Errorf("%w: work factor %d, this tool writes %d", ErrTooWeak, wf, WorkFactor)
	}
	return decrypt(sealed, passphrase)
}

// .
// .
func decrypt(sealed, passphrase []byte) (_ Contents, _ *crypto.KeyPair, retErr error) {
	id, err := age.NewScryptIdentity(string(passphrase))
	if err != nil {
		return Contents{}, nil, err
	}
	id.SetMaxWorkFactor(WorkFactor)
	r, err := age.Decrypt(bytes.NewReader(sealed), id)
	if err != nil {
		if errors.Is(err, age.ErrIncorrectIdentity) {
			return Contents{}, nil, fmt.Errorf("%w: %w", ErrWrongPassphrase, err)
		}
		return Contents{}, nil, fmt.Errorf("open: %w", err)
	}
	var c Contents
	defer func() {
		if retErr != nil {
			c.Wipe()
		}
	}()
	tr := tar.NewReader(r)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return Contents{}, nil, fmt.Errorf("the escrow opens, and what it holds is not an archive: %w", err)
		}
		var dst *[]byte
		switch h.Name {
		case MemberIdentityKey:
			dst = &c.IdentityKey
		case MemberSnapshotKey:
			dst = &c.SnapshotKey
		default:
			return Contents{}, nil, fmt.Errorf("%w: it holds %q, which is not part of an escrow", ErrNotAnEscrow, h.Name)
		}
		if h.Typeflag != tar.TypeReg || *dst != nil {
			return Contents{}, nil, fmt.Errorf("%w: %q is repeated or is not a plain file", ErrNotAnEscrow, h.Name)
		}
		data, err := io.ReadAll(io.LimitReader(tr, MaxMemberBytes+1))
		if err != nil {
			Wipe(data)
			return Contents{}, nil, fmt.Errorf("open: %w", err)
		}
		*dst = data
		if len(data) > MaxMemberBytes {
			return Contents{}, nil, fmt.Errorf("%w: %s is more than %d bytes", ErrTooLarge, h.Name, MaxMemberBytes)
		}
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
	if err := onlyPaddingFollows(r); err != nil {
		return Contents{}, nil, fmt.Errorf("%w: %w", ErrNotAnEscrow, err)
	}
	if c.IdentityKey == nil {
		return Contents{}, nil, fmt.Errorf("%w: it holds no %s", ErrNotAnEscrow, MemberIdentityKey)
	}
	kp, err := crypto.ParseKeyPair(c.IdentityKey)
	if err != nil {
		return Contents{}, nil, fmt.Errorf("the escrow opens, and its %s is not a key file: %w", MemberIdentityKey, err)
	}
	if c.SnapshotKey != nil {
		if _, err := SnapshotRecipient(c.SnapshotKey); err != nil {
			return Contents{}, nil, fmt.Errorf("the escrow opens, and its %s is not a snapshot key: %w", MemberSnapshotKey, err)
		}
	}
	return c, kp, nil
}

// .
// .
// .
// .
func SnapshotRecipient(snapshotKey []byte) (string, error) {
	ids, err := age.ParseIdentities(bytes.NewReader(snapshotKey))
	if err != nil {
		return "", fmt.Errorf("snapshot key: %w", err)
	}
	if len(ids) != 1 {
		return "", fmt.Errorf("snapshot key: %d identities, want exactly one", len(ids))
	}
	hybrid, ok := ids[0].(*age.HybridIdentity)
	if !ok {
		return "", fmt.Errorf("snapshot key: not a post-quantum identity")
	}
	return hybrid.Recipient().String(), nil
}

// .
// .
// .
// .
// .
func workFactor(sealed []byte) (int, error) {
	header, err := age.ExtractHeader(bytes.NewReader(sealed))
	if err != nil {
		return 0, fmt.Errorf("%w: %w", ErrNotAnEscrow, err)
	}
	sc := bufio.NewScanner(bytes.NewReader(header))
	found, wf := 0, 0
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 || fields[0] != "->" {
			continue
		}
		if fields[1] != "scrypt" || len(fields) != 4 {
			return 0, fmt.Errorf("%w: it is sealed to %q, not to a passphrase", ErrNotAnEscrow, fields[1])
		}
		n, err := strconv.Atoi(fields[3])
		if err != nil || n < 1 {
			return 0, fmt.Errorf("%w: work factor %q", ErrNotAnEscrow, fields[3])
		}
		found, wf = found+1, n
	}
	if found != 1 {
		return 0, fmt.Errorf("%w: %d passphrase stanzas", ErrNotAnEscrow, found)
	}
	return wf, nil
}

// .
// .
// .
// .
func Challenge(kp *crypto.KeyPair) error {
	msg := make([]byte, 32)
	if _, err := rand.Read(msg); err != nil {
		return fmt.Errorf("challenge: %w", err)
	}
	sig, err := crypto.Sign(kp, msg)
	if err != nil {
		return fmt.Errorf("challenge: the key does not sign: %w", err)
	}
	if err := crypto.Verify(kp.PublicKeyBytes(), msg, sig); err != nil {
		return fmt.Errorf("challenge: the key's signature does not verify under its own public key: %w", err)
	}
	return nil
}

// .
// .
// .
func Wipe(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

// .
// .
// .
// .
func PublishSecret(data []byte, path string) (published bool, retErr error) {
	return publish(data, path, false)
}

// .
// .
// .
// .
// .
func PublishReplacing(data []byte, path string) (published bool, retErr error) {
	return publish(data, path, true)
}

// .
// .
// .
// .
// .
// .
func SnapshotKeyPath(identityKeyPath string) string {
	return filepath.Join(filepath.Dir(identityKeyPath), SnapshotKeyFileName)
}

func ReceiptPath(identityKeyPath string) string {
	return filepath.Join(filepath.Dir(identityKeyPath), ReceiptFileName)
}

func publish(data []byte, path string, replacing bool) (published bool, retErr error) {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return false, fmt.Errorf("temp create: %w", err)
	}
	tmp := f.Name()
	closed := false
	defer func() {
		if !closed {
			retErr = errors.Join(retErr, f.Close())
		}
		if err := os.Remove(tmp); err != nil && !errors.Is(err, os.ErrNotExist) {
			retErr = errors.Join(retErr, fmt.Errorf("remove temp: %w", err))
		}
	}()
	if err := fileperm.RestrictToOwner(f); err != nil {
		return false, fmt.Errorf("temp permissions: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		return false, fmt.Errorf("temp write: %w", err)
	}
	if err := f.Sync(); err != nil {
		return false, fmt.Errorf("temp sync: %w", err)
	}
	if err := f.Close(); err != nil {
		closed = true
		return false, fmt.Errorf("temp close: %w", err)
	}
	closed = true
	// .
	// .
	// .
	if replacing {
		published, err = atomicfile.Replace(tmp, path)
	} else {
		published, err = atomicfile.PublishNew(tmp, path)
	}
	if err == nil {
		return true, nil
	}
	if published {
		return true, fmt.Errorf("published, but directory durability is unconfirmed: %w", err)
	}
	return false, fmt.Errorf("publish: %w", err)
}
