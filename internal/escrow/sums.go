package escrow

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
)

const SumsName = "SHA256SUMS"

const SumsSigName = SumsName + ".sig"

func CheckSums(dir string, unpacked []string) error {
	f, err := os.Open(filepath.Join(dir, SumsName))
	if err != nil {
		return fmt.Errorf("it holds no SHA256SUMS: %w", err)
	}
	defer f.Close()
	listed := map[string]bool{SumsName: true}
	have := map[string]bool{}
	for _, name := range unpacked {
		have[name] = true
	}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		want, name, ok := strings.Cut(sc.Text(), "  ")
		if !ok {
			return fmt.Errorf("its SHA256SUMS has a line that is not `<hash>  <name>`")
		}

		if !have[name] {
			return fmt.Errorf("its SHA256SUMS names %s, which is not in it", name)
		}
		g, err := os.Open(filepath.Join(dir, filepath.FromSlash(name)))
		if err != nil {
			return fmt.Errorf("its SHA256SUMS names %s, which is not in it", name)
		}
		h := sha256.New()
		_, err = io.Copy(h, g)
		g.Close()
		if err != nil {
			return err
		}
		if got := hex.EncodeToString(h.Sum(nil)); got != want {
			return fmt.Errorf("%s does not match its checksum", name)
		}
		listed[name] = true
	}
	if err := sc.Err(); err != nil {
		return err
	}
	for _, name := range unpacked {
		if !listed[name] {
			return fmt.Errorf("it holds %s, which its SHA256SUMS does not name", name)
		}
	}
	return nil
}

type SumsSig struct {
	Identity   string `json:"identity"`
	LastSeq    uint64 `json:"last_seq"`
	LastHash   string `json:"last_hash"`
	SumsSHA256 string `json:"sums_sha256"`
	Sig        string `json:"sig"`
}

var ErrUnsigned = errors.New("the snapshot is not signed by the identity's key")

var ErrSumsSig = errors.New("the snapshot's signature does not verify")

func sumsSigInput(identity string, lastSeq uint64, lastHash, sumsSHA256 string) []byte {
	return []byte("AII-SNAPSHOT-SUMS-V1\nidentity:" + identity + "\nlast_seq:" + strconv.FormatUint(lastSeq, 10) +
		"\nlast_hash:" + lastHash + "\nsums_sha256:" + sumsSHA256 + "\n")
}

func SignSums(kp *crypto.KeyPair, sums []byte, lastSeq uint64, lastHash string) (SumsSig, error) {
	h := sha256.Sum256(sums)
	s := SumsSig{Identity: kp.Fingerprint(), LastSeq: lastSeq, LastHash: lastHash, SumsSHA256: hex.EncodeToString(h[:])}
	sig, err := crypto.Sign(kp, sumsSigInput(s.Identity, s.LastSeq, s.LastHash, s.SumsSHA256))
	if err != nil {
		return SumsSig{}, fmt.Errorf("sign the checksum list: %w", err)
	}
	s.Sig = base64.StdEncoding.EncodeToString(sig)
	return s, nil
}

func SignSumsIn(dir string, kp *crypto.KeyPair, lastSeq uint64, lastHash string) error {
	sumsPath := filepath.Join(dir, SumsName)
	sums, err := os.ReadFile(sumsPath)
	if err != nil {
		return err
	}
	if _, named := SignedPart(sums); named {
		return errors.New("the checksum list names its signature already")
	}
	s, err := SignSums(kp, sums, lastSeq, lastHash)
	if err != nil {
		return err
	}
	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	if _, err := PublishReplacing(raw, filepath.Join(dir, SumsSigName)); err != nil {
		return err
	}
	h := sha256.Sum256(raw)
	_, err = PublishReplacing(append(sums, hex.EncodeToString(h[:])+"  "+SumsSigName+"\n"...), sumsPath)
	return err
}

func SignedPart(sums []byte) (signed []byte, named bool) {
	if !bytes.HasSuffix(sums, []byte("\n")) {
		return nil, false
	}
	cut := bytes.LastIndexByte(sums[:len(sums)-1], '\n') + 1
	if _, name, ok := strings.Cut(string(sums[cut:len(sums)-1]), "  "); !ok || name != SumsSigName {
		return nil, false
	}
	return sums[:cut], true
}

func VerifySumsSig(dir string, identityPub []byte) (SumsSig, error) {
	sums, err := os.ReadFile(filepath.Join(dir, SumsName))
	if err != nil {
		return SumsSig{}, fmt.Errorf("%w: %w", ErrSumsSig, err)
	}
	signed, named := SignedPart(sums)
	if !named {
		if bytes.Contains(sums, []byte("  "+SumsSigName+"\n")) {
			return SumsSig{}, fmt.Errorf("%w: SHA256SUMS is not the list that was signed: a line follows its signature's", ErrSumsSig)
		}
		return SumsSig{}, ErrUnsigned
	}
	raw, err := os.ReadFile(filepath.Join(dir, SumsSigName))
	if err != nil {
		return SumsSig{}, fmt.Errorf("%w: %w", ErrSumsSig, err)
	}
	var s SumsSig
	if err := json.Unmarshal(raw, &s); err != nil {
		return SumsSig{}, fmt.Errorf("%w: it does not read: %w", ErrSumsSig, err)
	}
	if want := crypto.PublicKeyFingerprint(identityPub); s.Identity != want {
		return SumsSig{}, fmt.Errorf("%w: it is identity %s's, and the key here is %s's", ErrSumsSig, s.Identity, want)
	}
	if h := sha256.Sum256(signed); hex.EncodeToString(h[:]) != s.SumsSHA256 {
		return SumsSig{}, fmt.Errorf("%w: SHA256SUMS is not the list that was signed", ErrSumsSig)
	}
	sig, err := base64.StdEncoding.DecodeString(s.Sig)
	if err != nil {
		return SumsSig{}, fmt.Errorf("%w: %w", ErrSumsSig, err)
	}
	if err := crypto.Verify(identityPub, sumsSigInput(s.Identity, s.LastSeq, s.LastHash, s.SumsSHA256), sig); err != nil {
		return SumsSig{}, fmt.Errorf("%w: %w", ErrSumsSig, err)
	}
	return s, nil
}
