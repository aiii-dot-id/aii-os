package ledger

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"

	"github.com/aiii-dot-id/aii-os/internal/atomicfile"
	"github.com/aiii-dot-id/aii-os/internal/canonicaljson"
	"github.com/aiii-dot-id/aii-os/internal/crypto"
)

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
// .
// .
// .
func Rewrap(ledgerPath string, kp *crypto.KeyPair, outPath string, replay func(candidatePath string) error) (count int, retErr error) {
	if replay == nil {
		return 0, errors.New("replay validator is required")
	}
	source, err := openLedgerForRewrap(ledgerPath)
	if err != nil {
		return 0, fmt.Errorf("open ledger: %w", err)
	}
	// .
	sourceClosed := false
	closeSource := func() error {
		if sourceClosed {
			return nil
		}
		sourceClosed = true
		return source.Close()
	}
	defer func() { retErr = errors.Join(retErr, closeSource()) }()
	if err := lockLedgerFile(source); err != nil {
		return 0, fmt.Errorf("lock ledger %q: %w", ledgerPath, err)
	}
	if _, err := source.Seek(0, 0); err != nil {
		return 0, fmt.Errorf("seek ledger: %w", err)
	}
	events, err := readEventsForRewrap(source, kp.Fingerprint())
	if err != nil {
		return 0, fmt.Errorf("read ledger: %w", err)
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
	// .
	// .
	if len(events) == 0 {
		return 0, fmt.Errorf("ledger is empty — nothing to rewrap")
	}
	for i := range events {
		if events[i].Type != EventSystemWitnessed {
			continue
		}
		p, err := footnoteReceipt(events[i].Payload)
		if err != nil {
			return 0, fmt.Errorf("record %d: %w", events[i].Seq, err)
		}
		events[i].Payload = p
		events[i].Content = crypto.ContentHash(p)
	}

	// .
	// .
	// .
	// .
	if err := recordShapeOwner(events, kp); err != nil {
		return 0, err
	}

	// .
	for i := range events {
		evt := &events[i]
		if evt.Seq != uint64(i+1) {
			return 0, fmt.Errorf("event %d: seq %d out of place — rewrap refuses corrupted chains", i, evt.Seq)
		}
		if crypto.ContentHash(evt.Payload) != evt.Content {
			return 0, fmt.Errorf("event %d: payload does not reproduce its content hash — rewrap re-signs history, it never repairs it", i)
		}
		if !slices.Contains(CanonicalRings(evt.Type), evt.Ring) {
			return 0, fmt.Errorf("event %d: ring %d is not canonical for %q — rewrap refuses invalid history", i, evt.Ring, evt.Type)
		}
	}

	dst := outPath
	if dst == "" {
		dst = ledgerPath
	}
	f, err := os.CreateTemp(filepath.Dir(dst), "."+filepath.Base(dst)+".rewrap-*")
	if err != nil {
		return 0, fmt.Errorf("open temp: %w", err)
	}
	tmp := f.Name()
	closed := false
	defer func() {
		if !closed {
			retErr = errors.Join(retErr, f.Close())
		}
		if err := os.Remove(tmp); err != nil && !os.IsNotExist(err) {
			retErr = errors.Join(retErr, fmt.Errorf("remove temporary ledger: %w", err))
		}
	}()

	prev := ""
	for i := range events {
		evt := &events[i]
		evt.Prev = prev
		sig, err := crypto.SignB64(kp, SignatureInputGold(crypto.SigAlg, kp.Fingerprint(), evt.EntryHash()))
		if err != nil {
			return 0, fmt.Errorf("event %d: sign: %w", i, err)
		}
		evt.Sig = sig
		prev = evt.EntryHash()
		line, err := evt.MarshalJSON()
		if err != nil {
			return 0, fmt.Errorf("event %d: marshal: %w", i, err)
		}
		if _, err := f.Write(append(line, '\n')); err != nil {
			return 0, fmt.Errorf("write: %w", err)
		}
	}
	if err := f.Sync(); err != nil {
		return 0, fmt.Errorf("sync: %w", err)
	}
	if err := f.Close(); err != nil {
		closed = true
		return 0, fmt.Errorf("close: %w", err)
	}
	closed = true

	// .
	if _, err := VerifyChain(tmp, kp.PublicKeyBytes(), nil); err != nil {
		return 0, fmt.Errorf("rewrapped chain does not verify (nothing replaced): %w", err)
	}
	if err := replay(tmp); err != nil {
		return 0, fmt.Errorf("rewrapped chain does not replay (nothing replaced): %w", err)
	}

	// .
	// .
	// .
	// .
	target, created, err := lockRewrapOutput(dst, source)
	if err != nil {
		return 0, err
	}
	targetClosed := false
	var targetInfoAtRelease os.FileInfo
	closeTarget := func() error {
		if target == nil || targetClosed {
			return nil
		}
		targetClosed = true
		targetInfoAtRelease, _ = target.Stat()
		return target.Close()
	}
	if target != nil {
		defer func() {
			statErr := error(nil)
			retErr = errors.Join(retErr, closeTarget())
			targetInfo := targetInfoAtRelease
			if targetInfo == nil {
				statErr = errors.New("output reservation could not be stat'ed before release")
			}
			if statErr != nil {
				retErr = errors.Join(retErr, fmt.Errorf("stat output reservation: %w", statErr))
			} else if created {
				if pathInfo, err := os.Stat(dst); err == nil && os.SameFile(targetInfo, pathInfo) {
					if err := os.Remove(dst); err != nil && !os.IsNotExist(err) {
						retErr = errors.Join(retErr, fmt.Errorf("remove output reservation: %w", err))
					}
				}
			}
		}()
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
	if err := closeTarget(); err != nil {
		return 0, fmt.Errorf("release output reservation before publication: %w", err)
	}
	if err := closeSource(); err != nil {
		return 0, fmt.Errorf("release source ledger before publication: %w", err)
	}
	published, err := atomicfile.Replace(tmp, dst)
	if err != nil {
		if published {
			created = false
			return len(events), fmt.Errorf("rewrapped ledger was published but directory durability is unconfirmed: %w", err)
		}
		return 0, fmt.Errorf("replace: %w", err)
	}
	created = false
	return len(events), nil
}

// .
const rewrapCreateRetries = 8

func lockRewrapOutput(path string, source *os.File) (*os.File, bool, error) {
	sourceInfo, err := source.Stat()
	if err != nil {
		return nil, false, fmt.Errorf("stat source ledger: %w", err)
	}
	// .
	// .
	// .
	// .
	// .
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		if _, err := os.Stat(path); err != nil {
			return nil, false, fmt.Errorf("open output ledger %q: dangling symlink (%v)", path, err)
		}
	}
	for attempt := 0; ; attempt++ {
		target, err := openLedgerForRewrap(path)
		created := false
		if os.IsNotExist(err) {
			target, err = createLedgerForRewrap(path)
			if os.IsExist(err) {
				// .
				// .
				// .
				// .
				// .
				// .
				if attempt >= rewrapCreateRetries {
					return nil, false, fmt.Errorf("open output ledger %q: exists but cannot be opened after %d attempts (a dangling symlink?): %w", path, attempt+1, err)
				}
				continue
			}
			created = err == nil
		}
		if err != nil {
			return nil, false, fmt.Errorf("open output ledger: %w", err)
		}
		targetInfo, err := target.Stat()
		if err != nil {
			return nil, false, errors.Join(fmt.Errorf("stat output ledger: %w", err), target.Close())
		}
		if os.SameFile(sourceInfo, targetInfo) {
			if err := target.Close(); err != nil {
				return nil, false, fmt.Errorf("close duplicate source handle: %w", err)
			}
			return nil, false, nil
		}
		if err := lockLedgerFile(target); err != nil {
			lockErr := fmt.Errorf("lock output ledger %q: %w", path, err)
			lockErr = errors.Join(lockErr, target.Close())
			if created {
				if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
					lockErr = errors.Join(lockErr, fmt.Errorf("remove output reservation: %w", err))
				}
			}
			return nil, false, lockErr
		}
		return target, created, nil
	}
}

// .
// .
// .
// .
// .
// .
// .
func recordShapeOwner(events []Event, kp *crypto.KeyPair) error {
	if len(events) == 0 || events[0].readAsPriorShape {
		return nil
	}
	if events[0].Type != EventRing0Genesis {
		return fmt.Errorf("record 1 is %s, not ring0.genesis — cannot establish whose history this is; regenerate instead", events[0].Type)
	}
	var g struct {
		PublicKey string `json:"public_key"`
	}
	if err := json.Unmarshal(events[0].Payload, &g); err != nil || g.PublicKey == "" {
		return fmt.Errorf("genesis carries no public key — cannot establish whose history this is; regenerate instead")
	}
	pub, err := base64.StdEncoding.DecodeString(g.PublicKey)
	if err != nil {
		return fmt.Errorf("genesis public key: %w", err)
	}
	if !bytes.Equal(pub, kp.PublicKeyBytes()) {
		return fmt.Errorf("genesis names key %s, not this key (%s) — someone else's history; regenerate instead", crypto.PublicKeyFingerprint(pub), kp.Fingerprint())
	}
	return nil
}

// .
// .
// .
// .
func footnoteReceipt(payload []byte) ([]byte, error) {
	object, err := payloadObject(payload)
	if err != nil {
		return nil, err
	}
	receipt, ok := object["receipt"]
	if !ok {
		return payload, nil
	}
	delete(object, "receipt")
	object["receipt_before_rewrap"] = receipt
	raw, err := json.Marshal(object)
	if err != nil {
		return nil, err
	}
	return canonicaljson.CanonicalizeV1(raw)
}

// .
// .
func EarlierReceipts(path string) (int, error) {
	n := 0
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), MaxEventLineBytes)
	for scanner.Scan() {
		line := scanner.Bytes()
		if bytes.Contains(line, []byte(`"type":"`+string(EventSystemWitnessed)+`"`)) && bytes.Contains(line, []byte(`"receipt":`)) {
			n++
		}
	}
	return n, scanner.Err()
}

// .
// .
// .
// .
// .
func readEventsForRewrap(r io.Reader, fingerprint string) ([]Event, error) {
	var events []Event
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), MaxEventLineBytes)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		if isRecordShape(line) {
			evt, err := decodeEvent(line)
			if err != nil {
				return nil, fmt.Errorf("malformed line: %w", err)
			}
			evt.Prev = ""
			evt.Sig = ""
			events = append(events, evt)
			continue
		}
		evt, err := readPriorShape(line, fingerprint)
		if err != nil {
			return nil, err
		}
		evt.readAsPriorShape = true
		events = append(events, evt)
	}
	return events, scanner.Err()
}

// .
// .
func isRecordShape(line []byte) bool {
	return bytes.HasPrefix(line, []byte(`{"entry":`))
}

// .
// .
// .
// .
type priorShape struct {
	Seq         uint64          `json:"seq"`
	PrevHash    string          `json:"prev_hash"`
	Timestamp   string          `json:"timestamp"`
	Type        EventType       `json:"type"`
	Author      string          `json:"author"`
	Ring        int             `json:"ring"`
	Payload     json.RawMessage `json:"payload"`
	ContentHash string          `json:"content_hash"`
	Signature   string          `json:"signature"`
	SigAlg      string          `json:"sig_alg"`
	SigKeyID    string          `json:"sig_key_id"`
	ModelID     string          `json:"model_id,omitempty"`
}

// .
// .
// .
// .
// .
func readPriorShape(line []byte, fingerprint string) (Event, error) {
	dec := json.NewDecoder(bytes.NewReader(line))
	dec.DisallowUnknownFields()
	var p priorShape
	if err := dec.Decode(&p); err != nil {
		return Event{}, fmt.Errorf("line is in neither the record shape nor the prior shape: %w", err)
	}
	if crypto.ContentHash(p.Payload) != p.ContentHash {
		return Event{}, fmt.Errorf("seq %d: payload does not reproduce its content hash — rewrap re-signs history, it never repairs it", p.Seq)
	}
	// .
	// .
	if p.SigKeyID != fingerprint || p.Author != fingerprint {
		return Event{}, fmt.Errorf("seq %d: signed by %s as %s, not this key (%s) — someone else's history; regenerate instead", p.Seq, p.SigKeyID, p.Author, fingerprint)
	}
	payloadModel, has, err := payloadModelID(p.Payload)
	if err != nil {
		return Event{}, fmt.Errorf("seq %d: %w", p.Seq, err)
	}
	if (!has && p.ModelID != "") || (has && payloadModel != p.ModelID) {
		return Event{}, fmt.Errorf("seq %d: envelope model_id %q does not match payload model_id %q", p.Seq, p.ModelID, payloadModel)
	}
	canonical, err := canonicaljson.CanonicalizeV1(p.Payload)
	if err != nil {
		return Event{}, fmt.Errorf("seq %d: payload: %w", p.Seq, err)
	}
	return Event{
		Seq:       p.Seq,
		Timestamp: p.Timestamp,
		Type:      p.Type,
		Ring:      p.Ring,
		Content:   crypto.ContentHash(canonical),
		Payload:   canonical,
	}, nil
}
