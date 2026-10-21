package ledger

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/atomicfile"
	"github.com/aiii-dot-id/aii-os/internal/canonicaljson"
	"github.com/aiii-dot-id/aii-os/internal/crypto"
)

func Rewrap(ledgerPath string, kp *crypto.KeyPair, outPath string, replay func(candidatePath string) error) (int, error) {
	done, err := RewrapWith(ledgerPath, kp, outPath, replay, RewrapChecks{})
	return done.Records, err
}

type RewrapChecks struct {
	Heads HeadVerifier

	Visit func(*Event) error
	Held  func() error

	PriorShapeUnchecked bool
}

type Rewrapped struct {
	Records    int
	Sealed     int
	Footnoted  int
	PriorShape bool

	Published bool

	Swept []string
}

var (
	ErrPriorShapeUnchecked = errors.New("a prior-shape ledger's links and proofs cannot be checked")

	ErrTailIncomplete = errors.New("the tail ends in an incomplete record")

	ErrRecordNotWhole = errors.New("the record is not whole where it stands")

	ErrOutputHoldsSegments = errors.New("the output directory holds another record's sealed segments")

	ErrWitnessedWithoutReceipt = errors.New("a witnessed record carries neither a receipt nor the footnote of one")

	ErrRecordCount = errors.New("the re-wrap did not write the records it read")
)

type UnfinishedRewrap struct {
	Records   int
	Sealed    int
	Segments  int
	Footnotes int
}

func (u *UnfinishedRewrap) Error() string {
	return fmt.Sprintf("an earlier in-place re-wrap published this tail and was stopped before it retired the record it replaced: the tail is the whole record (%d records, verified alone) and every one of the %d records in the %d sealed segment(s) beside it is in it — run rewrap on this ledger in place and it retires them and goes on",
		u.Records, u.Sealed, u.Segments)
}

func RewrapWith(ledgerPath string, kp *crypto.KeyPair, outPath string, replay func(candidatePath string) error, checks RewrapChecks) (done Rewrapped, retErr error) {
	if replay == nil {
		return Rewrapped{}, errors.New("replay validator is required")
	}

	dirLock, err := lockLedgerDir(filepath.Dir(ledgerPath))
	if err != nil {
		return Rewrapped{}, err
	}
	defer func() { retErr = errors.Join(retErr, dirLock.Close()) }()
	if outPath != "" {
		sourceDir, err := os.Stat(filepath.Dir(ledgerPath))
		if err != nil {
			return Rewrapped{}, err
		}
		outputDir, err := os.Stat(filepath.Dir(outPath))
		if err != nil {
			return Rewrapped{}, err
		}
		if !os.SameFile(sourceDir, outputDir) {
			outputLock, err := lockLedgerDir(filepath.Dir(outPath))
			if err != nil {
				return Rewrapped{}, err
			}
			defer func() { retErr = errors.Join(retErr, outputLock.Close()) }()

			foreign, err := listSegments(filepath.Dir(outPath))
			if err != nil {
				return Rewrapped{}, fmt.Errorf("output directory: %w", err)
			}
			if len(foreign) > 0 {
				return Rewrapped{}, fmt.Errorf("%w: %d of them, which a boot there would read ahead of the new tail — write the new record where no segment stands", ErrOutputHoldsSegments, len(foreign))
			}
		}
	}
	dst := outPath
	if dst == "" {
		dst = ledgerPath
	}

	swept, err := sweepCandidates(dst)
	defer func() { done.Swept = swept }()
	if err != nil {
		return Rewrapped{}, fmt.Errorf("remove an earlier re-wrap's unfinished candidate: %w", err)
	}
	source, err := openLedgerForRewrap(ledgerPath)
	if err != nil {

		if errors.Is(err, os.ErrNotExist) {
			if segments, listErr := listSegments(filepath.Dir(ledgerPath)); listErr == nil && len(segments) > 0 {
				return Rewrapped{}, fmt.Errorf("%w: open ledger: %w — %d sealed segment(s) stand here without their tail; a boot verifies them and recreates an empty tail, so boot the identity once, stop it, and re-wrap", ErrRecordNotWhole, err, len(segments))
			}
		}
		return Rewrapped{}, fmt.Errorf("open ledger: %w", err)
	}

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
		return Rewrapped{}, fmt.Errorf("lock ledger %q: %w", ledgerPath, err)
	}

	scratch, err := os.MkdirTemp(filepath.Dir(dst), candidatePrefix(dst)+"*")
	if err != nil {
		return Rewrapped{}, fmt.Errorf("open temp: %w", err)
	}
	tmp := filepath.Join(scratch, filepath.Base(dst))
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return Rewrapped{}, errors.Join(fmt.Errorf("open temp: %w", err), os.Remove(scratch))
	}
	closed := false
	defer func() {
		if !closed {
			retErr = errors.Join(retErr, f.Close())
		}
		if err := os.Remove(tmp); err != nil && !os.IsNotExist(err) {
			retErr = errors.Join(retErr, fmt.Errorf("remove temporary ledger: %w", err))
		}
		if err := os.Remove(scratch); err != nil && !os.IsNotExist(err) {
			retErr = errors.Join(retErr, fmt.Errorf("remove temporary ledger directory: %w", err))
		}
	}()

	w := &rewrapWriter{kp: kp, f: f}
	if err := w.read(ledgerPath, source, checks); err != nil {
		return Rewrapped{}, err
	}
	if err := f.Sync(); err != nil {
		return Rewrapped{}, fmt.Errorf("sync: %w", err)
	}
	if err := f.Close(); err != nil {
		closed = true
		return Rewrapped{}, fmt.Errorf("close: %w", err)
	}
	closed = true

	verified, err := VerifyChain(tmp, kp.PublicKeyBytes(), nil)
	if err != nil {
		return Rewrapped{}, fmt.Errorf("rewrapped chain does not verify (nothing replaced): %w", err)
	}
	if err := sameCount("the candidate verifies as", verified, w.wrote.Records); err != nil {
		return Rewrapped{}, err
	}
	if err := replay(tmp); err != nil {
		return Rewrapped{}, fmt.Errorf("rewrapped chain does not replay (nothing replaced): %w", err)
	}

	target, created, err := lockRewrapOutput(dst, source)
	if err != nil {
		return Rewrapped{}, err
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

	if err := closeTarget(); err != nil {
		return Rewrapped{}, fmt.Errorf("release output reservation before publication: %w", err)
	}
	if err := closeSource(); err != nil {
		return Rewrapped{}, fmt.Errorf("release source ledger before publication: %w", err)
	}
	published, err := atomicfile.Replace(tmp, dst)
	if err != nil {
		if published {
			created = false
			w.wrote.Published = true
			return w.wrote, fmt.Errorf("rewrapped ledger was published but directory durability is unconfirmed: %w", err)
		}
		return Rewrapped{}, fmt.Errorf("replace: %w", err)
	}
	created = false
	w.wrote.Published = true
	return w.wrote, nil
}

func candidatePrefix(dst string) string { return "." + filepath.Base(dst) + ".rewrap-" }

func sweepCandidates(dst string) ([]string, error) {
	dir := filepath.Dir(dst)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var swept []string
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), candidatePrefix(dst)) {
			continue
		}
		stale := filepath.Join(dir, e.Name())
		if e.IsDir() {
			if err := os.Remove(filepath.Join(stale, filepath.Base(dst))); err != nil && !os.IsNotExist(err) {
				return swept, err
			}
		}
		if err := os.Remove(stale); err != nil {
			return swept, err
		}
		swept = append(swept, stale)
	}
	return swept, nil
}

func sameCount(what string, got, written int) error {
	if got != written {
		return fmt.Errorf("%w: %s %d, and %d were written (nothing replaced)", ErrRecordCount, what, got, written)
	}
	return nil
}

const rewrapCreateRetries = 8

func lockRewrapOutput(path string, source *os.File) (*os.File, bool, error) {
	sourceInfo, err := source.Stat()
	if err != nil {
		return nil, false, fmt.Errorf("stat source ledger: %w", err)
	}

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

type rewrapWriter struct {
	kp    *crypto.KeyPair
	f     *os.File
	prev  string
	wrote Rewrapped
}

func (w *rewrapWriter) read(ledgerPath string, tailFile *os.File, checks RewrapChecks) error {
	info, err := tailFile.Stat()
	if err != nil {
		return fmt.Errorf("stat ledger: %w", err)
	}
	if !info.Mode().IsRegular() {
		return &os.PathError{Op: "open regular ledger", Path: ledgerPath, Err: os.ErrInvalid}
	}
	end, err := completeTailEnd(tailFile, info.Size())
	if err != nil {
		return fmt.Errorf("read ledger: %w", err)
	}
	if end < info.Size() {
		return fmt.Errorf("%w: %d bytes follow the last complete line of %s — rewrap re-signs history, it never repairs it; a boot sets those bytes aside beside the ledger and keeps them, so boot the identity once, stop it, and re-wrap",
			ErrTailIncomplete, info.Size()-end, filepath.Base(ledgerPath))
	}
	tail := func() io.Reader { return io.NewSectionReader(tailFile, 0, end) }

	prior := false
	if err := scanLines(tail(), func(line []byte, _, _ int64, _ int) error {
		prior = isPriorShape(line)
		return ErrStop
	}); err != nil && !errors.Is(err, ErrStop) {
		return fmt.Errorf("read ledger: %w", err)
	}
	if prior {
		return w.fromPriorShape(ledgerPath, tail(), checks)
	}

	records := func(fn func(*Event) error) error { return stream(ledgerPath, tail(), fn) }

	var first *Event
	if err := records(func(evt *Event) error {
		e := *evt
		first = &e
		return ErrStop
	}); err != nil {
		return fmt.Errorf("read ledger: %w", err)
	}
	if first == nil {
		return errors.New("ledger is empty — nothing to rewrap")
	}
	if err := ownedBy(first, w.kp); err != nil {
		return err
	}
	walked, err := verifyChain(records, w.kp.PublicKeyBytes(), checks.Heads, func(evt *Event) error {
		if evt.Seq == 1 {
			if err := ownedBy(evt, w.kp); err != nil {
				return err
			}
		}
		if checks.Visit != nil {
			if err := checks.Visit(evt); err != nil {
				return err
			}
		}
		return w.write(*evt)
	})
	if err != nil {
		var failure *VerifyFailure
		if !errors.As(err, &failure) {
			return err
		}

		if errors.Is(err, ErrTailConflict) {
			unfinished, why := unfinishedRewrap(ledgerPath, tail, w.kp)
			if unfinished != nil {
				return unfinished
			}
			return fmt.Errorf("the record does not verify, and rewrap re-signs history, it never repairs it (nothing replaced): %w; nor is this a re-wrap stopped before it retired the earlier record's segments, which rewrap would finish: %w", err, why)
		}
		return fmt.Errorf("the record does not verify, and rewrap re-signs history, it never repairs it (nothing replaced): %w", err)
	}
	if err := sameCount("the walk verified", walked, w.wrote.Records); err != nil {
		return err
	}
	if checks.Held != nil {
		return checks.Held()
	}
	return nil
}

func unfinishedRewrap(ledgerPath string, tail func() io.Reader, kp *crypto.KeyPair) (*UnfinishedRewrap, error) {
	segments, err := listSegments(filepath.Dir(ledgerPath))
	if err != nil {
		return nil, err
	}

	var want [][32]byte
	for i := range segments {
		if err := streamSegment(&segments[i], func(evt *Event, _ []byte, _ int) error {
			if evt.Seq != uint64(len(want)+1) {
				return fmt.Errorf("%s holds record %d where record %d belongs", evt.container, evt.Seq, len(want)+1)
			}
			if crypto.ContentHash(evt.Payload) != evt.Content {
				return fmt.Errorf("record %d (%s): the payload is not the payload the entry names", evt.Seq, evt.container)
			}
			across, _, err := carried(*evt)
			if err != nil {
				return err
			}
			across.Prev = ""
			want = append(want, across.entrySum())
			return nil
		}); err != nil {
			return nil, err
		}
	}
	found := &UnfinishedRewrap{Sealed: len(want), Segments: len(segments)}
	alone := func(fn func(*Event) error) error {
		return streamTail(tail(), ledgerPath, nil, func(evt *Event, _ []byte, _ int) error { return fn(evt) })
	}
	found.Records, err = verifyChain(alone, kp.PublicKeyBytes(), nil, func(evt *Event) error {
		if evt.Seq == 1 {
			if err := ownedBy(evt, kp); err != nil {
				return err
			}
		}
		if evt.Type == EventSystemWitnessed {
			if _, moved, err := footnoteReceipt(evt.Payload); err != nil {
				return fmt.Errorf("record %d: %w", evt.Seq, err)
			} else if moved {
				return fmt.Errorf("record %d of the tail carries a witness receipt, and a re-wrap leaves only footnotes", evt.Seq)
			}
			found.Footnotes++
		}
		if i := evt.Seq - 1; i < uint64(len(want)) {
			here := *evt
			here.Prev = ""
			if here.entrySum() != want[i] {
				return fmt.Errorf("record %d of the tail is not the sealed record %d as a re-wrap carries it", evt.Seq, evt.Seq)
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("the tail alone: %w", err)
	}
	if found.Records < found.Sealed {
		return nil, fmt.Errorf("the tail ends at record %d and the segments hold %d", found.Records, found.Sealed)
	}
	return found, nil
}

func (w *rewrapWriter) fromPriorShape(ledgerPath string, tail io.Reader, checks RewrapChecks) error {
	if !checks.PriorShapeUnchecked {
		return fmt.Errorf("%w: %s is in the shape written before the record ruling; this build reads that shape and holds no verifier for it, so it is re-wrapped only with its links and proofs declared unchecked",
			ErrPriorShapeUnchecked, filepath.Base(ledgerPath))
	}
	segments, err := listSegments(filepath.Dir(ledgerPath))
	if err != nil {
		return err
	}
	if len(segments) > 0 {
		return fmt.Errorf("%w: %d sealed segment(s) stand beside a prior-shape tail, and sealing came with the record shape — they are not this ledger's", ErrSegmentSet, len(segments))
	}
	w.wrote.PriorShape = true
	fingerprint := w.kp.Fingerprint()
	lines := 0
	if err := scanLines(tail, func(line []byte, _, _ int64, at int) error {
		lines++
		if isRecordShape(line) {
			return fmt.Errorf("%w: line %d is in the record shape inside a prior-shape ledger — nothing writes the two into one file", ErrRecordUnreadable, at)
		}
		evt, err := readPriorShape(line, fingerprint)
		if err != nil {
			return err
		}
		if evt.Seq != uint64(w.wrote.Records+1) {
			return fmt.Errorf("line %d: seq %d out of place — rewrap refuses corrupted chains", at, evt.Seq)
		}
		return w.write(evt)
	}); err != nil {
		return err
	}
	return sameCount("the tail holds", lines, w.wrote.Records)
}

func carried(evt Event) (Event, bool, error) {
	if !slices.Contains(CanonicalRings(evt.Type), evt.Ring) {
		return Event{}, false, fmt.Errorf("record %d: ring %d is not canonical for %q — rewrap refuses invalid history", evt.Seq, evt.Ring, evt.Type)
	}
	if evt.Type != EventSystemWitnessed {
		return evt, false, nil
	}
	footnote, moved, err := footnoteReceipt(evt.Payload)
	if err != nil {
		return Event{}, false, fmt.Errorf("record %d: %w", evt.Seq, err)
	}
	if moved {
		evt.Payload = footnote
		evt.Content = crypto.ContentHash(footnote)
	}
	return evt, moved, nil
}

func (w *rewrapWriter) write(evt Event) error {
	evt, moved, err := carried(evt)
	if err != nil {
		return err
	}
	if moved {
		w.wrote.Footnoted++
	}
	if evt.sealed {
		w.wrote.Sealed++
	}
	evt.Prev = w.prev
	sig, err := crypto.SignB64(w.kp, SignatureInputGold(crypto.SigAlg, w.kp.Fingerprint(), evt.EntryHash()))
	if err != nil {
		return fmt.Errorf("record %d: sign: %w", evt.Seq, err)
	}
	evt.Sig = sig
	w.prev = evt.EntryHash()
	line, err := evt.MarshalJSON()
	if err != nil {
		return fmt.Errorf("record %d: marshal: %w", evt.Seq, err)
	}
	if _, err := w.f.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	w.wrote.Records++
	return nil
}

func ownedBy(first *Event, kp *crypto.KeyPair) error {
	if first.Seq != 1 {
		return fmt.Errorf("%w: the ledger begins at record %d, and the records before it are not beside it: after a seal they are in sealed segments, and none stands here — a re-wrap reads the whole record or nothing", ErrRecordNotWhole, first.Seq)
	}
	if first.Type != EventRing0Genesis {
		return fmt.Errorf("record 1 is %s, not ring0.genesis — cannot establish whose history this is; regenerate instead", first.Type)
	}
	var g struct {
		PublicKey string `json:"public_key"`
	}
	if err := json.Unmarshal(first.Payload, &g); err != nil || g.PublicKey == "" {
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

func footnoteReceipt(payload []byte) (footnote []byte, moved bool, err error) {
	object, err := payloadObject(payload)
	if err != nil {
		return nil, false, err
	}
	receipt, ok := object["receipt"]
	if !ok {
		if _, already := object["receipt_before_rewrap"]; !already {
			return nil, false, fmt.Errorf("%w — no boot accepts it, and rewrap repairs nothing", ErrWitnessedWithoutReceipt)
		}
		return payload, false, nil
	}
	delete(object, "receipt")
	object["receipt_before_rewrap"] = receipt
	raw, err := json.Marshal(object)
	if err != nil {
		return nil, false, err
	}
	footnote, err = canonicaljson.CanonicalizeV1(raw)
	return footnote, err == nil, err
}

func isRecordShape(line []byte) bool {
	return bytes.HasPrefix(line, []byte(`{"entry":`))
}

func isPriorShape(line []byte) bool {
	return bytes.HasPrefix(line, []byte(`{"seq":`))
}

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
