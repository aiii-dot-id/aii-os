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
package ledger

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
	"log"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/atomicfile"
	"github.com/aiii-dot-id/aii-os/internal/canonicaljson"
	"github.com/aiii-dot-id/aii-os/internal/crypto"
)

// .
// .
// .
// .
func readTailEvents(f *os.File, k int) ([]*Event, error) {
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	size := st.Size()
	if size == 0 {
		return nil, nil
	}
	var buf []byte
	for win := int64(512 * 1024); ; win *= 2 {
		if win > size {
			win = size
		}
		buf = make([]byte, win)
		if _, err := f.ReadAt(buf, size-win); err != nil {
			return nil, fmt.Errorf("tail read: %w", err)
		}
		if win == size || bytes.Count(buf, []byte{'\n'}) >= k+2 {
			if win < size {
				// .
				if i := bytes.IndexByte(buf, '\n'); i >= 0 {
					buf = buf[i+1:]
				}
			}
			break
		}
	}
	lines := bytes.Split(buf, []byte{'\n'})
	events := make([]*Event, 0, k+1)
	for _, ln := range lines {
		ln = bytes.TrimSpace(ln)
		if len(ln) == 0 {
			continue
		}
		e, err := decodeEvent(ln)
		if err != nil {
			return nil, fmt.Errorf("tail parse: %w", err)
		}
		events = append(events, &e)
	}
	if len(events) > k+1 {
		events = events[len(events)-k-1:]
	}
	return events, nil
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
type Event struct {
	Seq       uint64
	Prev      string
	Timestamp string
	Type      EventType
	Ring      int
	Content   string
	Payload   json.RawMessage
	Sig       string

	// .
	// .
	// .
	readAsPriorShape bool

	// .
	// .
	sealed bool
}

// .
// .
// .
// .
// .
func (e *Event) EntryBytes() []byte {
	var b bytes.Buffer
	b.WriteString(`{"content":`)
	b.Write(jsonString(e.Content))
	b.WriteString(`,"prev":`)
	b.Write(jsonString(e.Prev))
	b.WriteString(`,"ring":`)
	b.WriteString(strconv.Itoa(e.Ring))
	b.WriteString(`,"seq":`)
	b.WriteString(strconv.FormatUint(e.Seq, 10))
	b.WriteString(`,"ts":`)
	b.Write(jsonString(e.Timestamp))
	b.WriteString(`,"type":`)
	b.Write(jsonString(string(e.Type)))
	b.WriteByte('}')
	return b.Bytes()
}

// .
// .
func (e *Event) EntryHash() string {
	sum := sha256.Sum256(e.EntryBytes())
	return hex.EncodeToString(sum[:])
}

// .
// .
// .
func jsonString(s string) []byte {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(s); err != nil {
		panic("encoding a Go string as JSON cannot fail: " + err.Error())
	}
	return bytes.TrimSuffix(b.Bytes(), []byte{'\n'})
}

// .
// .
// .
type wireRecord struct {
	Entry   json.RawMessage `json:"entry"`
	Payload json.RawMessage `json:"payload"`
	Sig     string          `json:"sig"`
}

type wireEntry struct {
	Content string    `json:"content"`
	Prev    string    `json:"prev"`
	Ring    int       `json:"ring"`
	Seq     uint64    `json:"seq"`
	Ts      string    `json:"ts"`
	Type    EventType `json:"type"`
}

// .
// .
// .
func (e Event) MarshalJSON() ([]byte, error) {
	if len(e.Payload) == 0 {
		return nil, errors.New("event has no payload")
	}
	var b bytes.Buffer
	b.WriteString(`{"entry":`)
	b.Write(e.EntryBytes())
	b.WriteString(`,"payload":`)
	b.Write(e.Payload)
	if e.Sig != "" {
		b.WriteString(`,"sig":`)
		b.Write(jsonString(e.Sig))
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// .
// .
// .
// .
func (e *Event) UnmarshalJSON(raw []byte) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var w wireRecord
	if err := dec.Decode(&w); err != nil {
		return err
	}
	if len(w.Entry) == 0 || len(w.Payload) == 0 {
		return fmt.Errorf("%w: a record carries an entry and a payload", ErrNotCanonical)
	}
	edec := json.NewDecoder(bytes.NewReader(w.Entry))
	edec.DisallowUnknownFields()
	var en wireEntry
	if err := edec.Decode(&en); err != nil {
		return fmt.Errorf("entry: %w", err)
	}
	ev := Event{
		Seq: en.Seq, Prev: en.Prev, Timestamp: en.Ts, Type: en.Type, Ring: en.Ring,
		Content: en.Content, Payload: bytes.Clone(w.Payload), Sig: w.Sig,
	}
	if !bytes.Equal(ev.EntryBytes(), w.Entry) {
		return fmt.Errorf("%w: entry bytes are not the canonical form", ErrNotCanonical)
	}
	if len(w.Payload) == 0 || w.Payload[0] != '{' {
		return fmt.Errorf("%w: payload must be a JSON object", ErrNotCanonical)
	}
	canonical, err := canonicaljson.CanonicalizeV1(w.Payload)
	if err != nil {
		return fmt.Errorf("payload: %w", err)
	}
	if !bytes.Equal(canonical, w.Payload) {
		return fmt.Errorf("%w: payload bytes are not the canonical form", ErrNotCanonical)
	}
	*e = ev
	return nil
}

// .
type EventType string

const (
	// .
	EventRing0Genesis EventType = "ring0.genesis"

	// .
	EventRelationshipUpsert EventType = "relationship.upsert"

	// .
	EventBeliefPromote EventType = "belief.promote"

	// .
	EventExperienceCreate    EventType = "experience.create"
	EventBeliefUpsert        EventType = "belief.upsert"
	EventEdgeCreate          EventType = "edge.create"
	EventSelfModelSynthesize EventType = "self_model.synthesize"

	// .
	EventIntentionCreate       EventType = "intention.create"
	EventIntentionStateChange  EventType = "intention.state_change"
	EventCommitmentPromised    EventType = "commitment.promised"
	EventCommitmentStateChange EventType = "commitment.state_change"

	// .
	EventBeliefArchive   EventType = "belief.archive"
	EventBeliefSupersede EventType = "belief.supersede"
	EventEdgeArchive     EventType = "edge.archive"

	// .
	EventWorkingStyleUpsert EventType = "working_style.upsert"

	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	EventConsolidationRun EventType = "consolidation.run"
	EventDreamRun         EventType = "dream.run"

	// .
	// .
	// .
	EventSystemWitnessed EventType = "system.witnessed"

	// .
	// .
	// .
	// .
	// .
	// .
	EventTrustEpochAccepted EventType = "trust.epoch_accepted"
	// .
	// .
	// .
	// .
	// .
	EventNetworkNameClaimed EventType = "network.name_claimed"
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
func CanonicalRings(t EventType) []int {
	switch t {
	case EventRing0Genesis:
		return []int{0}
	case EventRelationshipUpsert:
		return []int{1}
	case EventBeliefPromote:
		return []int{2}
	case EventExperienceCreate, EventBeliefUpsert, EventEdgeCreate,
		EventSelfModelSynthesize, EventIntentionCreate, EventIntentionStateChange,
		EventCommitmentPromised, EventCommitmentStateChange,
		EventWorkingStyleUpsert, EventBeliefArchive, EventBeliefSupersede,
		EventEdgeArchive,
		// .
		// .
		// .
		// .
		EventConsolidationRun, EventDreamRun:
		return []int{3}
	case EventSystemWitnessed:
		return []int{0}
	case EventTrustEpochAccepted:
		return []int{0}
	case EventNetworkNameClaimed:
		return []int{0}
	default:
		return nil
	}
}

// .
// .
// .
// .
func AllEventTypes() []EventType {
	return []EventType{
		EventRing0Genesis,
		EventRelationshipUpsert,
		EventBeliefPromote,
		EventExperienceCreate,
		EventBeliefUpsert,
		EventEdgeCreate,
		EventSelfModelSynthesize,
		EventIntentionCreate,
		EventIntentionStateChange,
		EventCommitmentPromised,
		EventCommitmentStateChange,
		EventWorkingStyleUpsert,
		EventConsolidationRun,
		EventDreamRun,
		EventBeliefArchive,
		EventBeliefSupersede,
		EventEdgeArchive,
		EventSystemWitnessed,
		EventTrustEpochAccepted,
		EventNetworkNameClaimed,
	}
}

// .
type Ledger struct {
	path string
	dir  string
	// .
	// .
	// .
	// .
	dirLock  *os.File
	mu       sync.Mutex
	file     *os.File
	writer   *bufio.Writer
	lastSeq  uint64
	lastHash string
	modelID  string

	// .
	// .
	// .
	sealedSeq  uint64
	sealedHash string
	segments   []segment

	// .
	// .
	// .
	// .
	// .
	// .
	// .
	frozenReason string
}

// .
// .
// .
func (l *Ledger) SetFrozen(reason string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.frozenReason == "" {
		l.frozenReason = reason
	}
}

// .
// .
// .
// .
// .
func New(path string) (*Ledger, error) {
	dir := filepath.Dir(path)
	l := &Ledger{
		path:     path,
		dir:      dir,
		lastHash: "",
		lastSeq:  0,
	}
	// .
	// .
	// .
	// .
	// .
	// .
	dirLock, err := lockLedgerDir(dir)
	if err != nil {
		return nil, err
	}
	l.dirLock = dirLock
	opened := false
	defer func() {
		if !opened {
			_ = dirLock.Close()
		}
	}()
	if err := sweepDebris(dir); err != nil {
		return nil, fmt.Errorf("ledger directory: %w", err)
	}
	segs, err := listSegments(dir)
	if err != nil {
		return nil, fmt.Errorf("sealed segments: %w", err)
	}
	if len(segs) > 0 {
		newest := segs[len(segs)-1]
		seq, hash, err := segmentEnd(&newest)
		if err != nil {
			return nil, fmt.Errorf("sealed segment: %w", err)
		}
		l.sealedSeq, l.sealedHash, l.segments = seq, hash, segs
		l.lastSeq, l.lastHash = seq, hash
		if err := reconcileTail(path, &newest); err != nil {
			return nil, fmt.Errorf("finishing an interrupted seal: %w", err)
		}
	}

	// .
	// .
	// .
	// .
	// .
	// .
	f, err := atomicfile.OpenAppendRemovable(path, 0600)
	if err != nil {
		return nil, fmt.Errorf("cannot open ledger file: %w", err)
	}
	if err := lockLedgerFile(f); err != nil {
		if closeErr := f.Close(); closeErr != nil {
			return nil, fmt.Errorf("cannot lock ledger file %q: %w (close after refusal: %v)", path, err, closeErr)
		}
		return nil, fmt.Errorf("cannot lock ledger file %q: %w", path, err)
	}
	if err := l.readChainState(f); err != nil {
		if closeErr := f.Close(); closeErr != nil {
			return nil, fmt.Errorf("cannot read existing ledger: %w (close after refusal: %v)", err, closeErr)
		}
		return nil, fmt.Errorf("cannot read existing ledger: %w", err)
	}

	l.file = f
	l.writer = bufio.NewWriter(f)
	// .
	opened = true
	return l, nil
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
func (l *Ledger) readChainState(file *os.File) error {
	r := bufio.NewReaderSize(file, 64*1024)
	var goodBytes int64
	first := true
	for {
		piece, err := r.ReadBytes('\n')
		if err != nil && err != io.EOF {
			return err
		}
		terminated := bytes.HasSuffix(piece, []byte("\n"))
		line := bytes.TrimSpace(piece)
		if len(line) == 0 {
			goodBytes += int64(len(piece))
			if err == io.EOF {
				return nil
			}
			continue
		}
		// .
		// .
		// .
		// .
		if !terminated {
			sidecar := fmt.Sprintf("%s.torn-%d", l.path, time.Now().UTC().UnixNano())
			if err := os.WriteFile(sidecar, piece, 0600); err != nil {
				return fmt.Errorf("torn trailing line, quarantine failed (refusing to truncate unpreserved bytes): %w", err)
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
			if err := os.Truncate(l.path, goodBytes); err != nil {
				return fmt.Errorf("torn trailing line, truncate failed: %w", err)
			}
			log.Printf("ledger: dropped a torn trailing line (%d bytes) — quarantined at %s; if the projection mirror remembers more events than the ledger now holds, this was NOT crash debris", len(piece), sidecar)
			return nil
		}
		evt, err := decodeEvent(line)
		if err != nil {
			return fmt.Errorf("%w: malformed ledger line: %w", ErrRecordUnreadable, err)
		}
		if first && l.sealedSeq > 0 && (evt.Seq != l.sealedSeq+1 || evt.Prev != l.sealedHash) {
			return fmt.Errorf("%w: %w: tail begins at record %d and does not continue the sealed run ending at %d", ErrRecordUnreadable, ErrTailConflict, evt.Seq, l.sealedSeq)
		}
		first = false
		l.lastSeq = evt.Seq
		l.lastHash = evt.EntryHash()
		goodBytes += int64(len(piece))
		if err == io.EOF {
			return nil
		}
	}
}

// .
// .
// .
// .
// .
// .
func (l *Ledger) SetModelID(modelID string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.modelID = modelID
}

// .
const tailCheckDepth = 8

func (l *Ledger) verifyTailBeforeAppend(kp *crypto.KeyPair) error {
	// .
	// .
	// .
	// .
	// .
	events, err := readTailEvents(l.file, tailCheckDepth)
	if err != nil {
		return err
	}
	n := len(events)
	if n == 0 {
		if l.lastSeq != l.sealedSeq || l.lastHash != l.sealedHash {
			return fmt.Errorf("tail read found no events but lastSeq=%d (sealed through %d) — ledger file truncated", l.lastSeq, l.sealedSeq)
		}
		return nil
	}
	pubKey := kp.PublicKeyBytes()
	for i, evt := range events {
		if i == 0 && l.sealedSeq > 0 && evt.Seq == l.sealedSeq+1 && evt.Prev != l.sealedHash {
			return fmt.Errorf("tail event seq %d: prev does not link to the sealed run", evt.Seq)
		}
		if i > 0 {
			if evt.Seq != events[i-1].Seq+1 {
				return fmt.Errorf("tail event seq %d: not consecutive after %d", evt.Seq, events[i-1].Seq)
			}
			if evt.Prev != events[i-1].EntryHash() {
				return fmt.Errorf("tail event seq %d: prev linkage broken", evt.Seq)
			}
		}
		if crypto.ContentHash(evt.Payload) != evt.Content {
			return fmt.Errorf("tail event seq %d: content mismatch", evt.Seq)
		}
		if err := verifyEventSignature(evt, pubKey); err != nil {
			return fmt.Errorf("tail event seq %d: %w", evt.Seq, err)
		}
	}
	if events[n-1].Seq != l.lastSeq {
		return fmt.Errorf("tail ends at seq %d but lastSeq=%d — ledger file diverged from chain state", events[n-1].Seq, l.lastSeq)
	}
	if l.lastHash != events[n-1].EntryHash() {
		return fmt.Errorf("in-memory chain state diverged from file tail")
	}
	return nil
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

const (
	goldSignatureTag     = "AII-LEDGER-LINE-SIGNATURE-GOLD"
	goldArtifactKind     = "aii.ledger.line"
	goldCanonicalization = "aii-canonical-json"
	goldSuiteID          = "aii-pq-mldsa87"
	goldRoleIdentity     = "identity"
)

// .
// .
func SignatureInputGold(alg, keyID, entryHash string) []byte {
	return []byte(goldSignatureTag + "\n" +
		"artifact_kind:" + goldArtifactKind + "\n" +
		"canonicalization:" + goldCanonicalization + "\n" +
		"suite_id:" + goldSuiteID + "\n" +
		"role:" + goldRoleIdentity + "\n" +
		"alg:" + alg + "\n" +
		"key_id:" + keyID + "\n" +
		"entry_sha256:" + entryHash + "\n")
}

// .
// .
// .
// .
func verifyEventSignature(evt *Event, pubKey []byte) error {
	if evt.Sig == "" {
		return fmt.Errorf("%w: record carries no proof", ErrUnsignedRecord)
	}
	sig, err := base64.StdEncoding.DecodeString(evt.Sig)
	if err != nil {
		return fmt.Errorf("invalid signature encoding: %w", err)
	}
	input := SignatureInputGold(crypto.SigAlg, crypto.PublicKeyFingerprint(pubKey), evt.EntryHash())
	if err := crypto.Verify(pubKey, input, sig); err != nil {
		return fmt.Errorf("signature verification failed: %w", err)
	}
	return nil
}

// .
type PreparedPayload struct {
	raw json.RawMessage
}

// .
func (p PreparedPayload) Bytes() []byte {
	return bytes.Clone(p.raw)
}

// .
func (l *Ledger) PreparePayload(payload interface{}) (PreparedPayload, error) {
	l.mu.Lock()
	modelID := l.modelID
	l.mu.Unlock()
	return preparePayload(payload, modelID)
}

// .
// .
func (l *Ledger) PreparePayloadWithModel(payload interface{}, modelID string) (PreparedPayload, error) {
	return preparePayload(payload, modelID)
}

func preparePayload(payload interface{}, modelID string) (PreparedPayload, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return PreparedPayload{}, fmt.Errorf("payload marshal failed: %w", err)
	}
	raw, err = stampModelID(raw, modelID)
	if err != nil {
		return PreparedPayload{}, err
	}
	// .
	// .
	// .
	// .
	canonical, err := canonicaljson.CanonicalizeV1(raw)
	if err != nil {
		return PreparedPayload{}, fmt.Errorf("payload is not canonicalizable: %w", err)
	}
	return PreparedPayload{raw: canonical}, nil
}

// .
func (l *Ledger) Append(eventType EventType, author string, ring int, payload interface{}, kp *crypto.KeyPair) (*Event, error) {
	prepared, err := l.PreparePayload(payload)
	if err != nil {
		return nil, err
	}
	return l.AppendPrepared(eventType, author, ring, prepared, kp)
}

// .
func (l *Ledger) AppendPrepared(eventType EventType, author string, ring int, payload PreparedPayload, kp *crypto.KeyPair) (*Event, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	raw := payload.raw

	// .
	// .
	if l.frozenReason != "" {
		return nil, fmt.Errorf("append refused — ledger frozen (SAFE): %s", l.frozenReason)
	}

	// .
	// .
	// .
	if err := l.verifyTailBeforeAppend(kp); err != nil {
		err = fmt.Errorf("%w: %w", ErrTailIntegrity, err)
		l.frozenReason = err.Error()
		return nil, err
	}
	if author != kp.Fingerprint() {
		return nil, fmt.Errorf("%w: author %q does not match signing key %q", ErrAuthorKeyMismatch, author, kp.Fingerprint())
	}

	if _, _, err := payloadModelID(raw); err != nil {
		return nil, err
	}

	// .
	seq := l.lastSeq + 1

	// .
	prev := ""
	if seq > 1 {
		prev = l.lastHash
	}

	evt := Event{
		Seq:       seq,
		Prev:      prev,
		Timestamp: nowUTC(),
		Type:      eventType,
		Ring:      ring,
		Content:   crypto.ContentHash(raw),
		Payload:   raw,
	}

	// .
	// .
	// .
	sigB64, err := crypto.SignB64(kp, SignatureInputGold(crypto.SigAlg, kp.Fingerprint(), evt.EntryHash()))
	if err != nil {
		return nil, fmt.Errorf("signing failed: %w", err)
	}
	evt.Sig = sigB64

	// .
	// .
	// .
	// .
	// .
	// .
	// .
	line, err := evt.MarshalJSON()
	if err != nil {
		return nil, fmt.Errorf("event marshal failed: %w", err)
	}

	// .
	// .
	// .
	// .
	// .
	if len(line) > MaxEventLineBytes {
		return nil, fmt.Errorf("%w: %s would serialize to %d bytes, over the %d-byte limit the reader can consume",
			ErrEventTooLarge, eventType, len(line), MaxEventLineBytes)
	}

	// .
	if _, err := l.writer.Write(line); err != nil {
		return nil, l.uncertainAppend(fmt.Errorf("write failed: %w", err))
	}
	if err := l.writer.WriteByte('\n'); err != nil {
		return nil, l.uncertainAppend(fmt.Errorf("write newline failed: %w", err))
	}
	if err := l.writer.Flush(); err != nil {
		return nil, l.uncertainAppend(fmt.Errorf("flush failed: %w", err))
	}
	// .
	// .
	// .
	if err := l.file.Sync(); err != nil {
		return nil, l.uncertainAppend(fmt.Errorf("fsync failed: %w", err))
	}

	// .
	l.lastSeq = seq
	l.lastHash = evt.EntryHash()

	return &evt, nil
}

func (l *Ledger) uncertainAppend(cause error) error {
	err := fmt.Errorf("%w: %w", ErrAppendUncertain, cause)
	if l.frozenReason == "" {
		l.frozenReason = err.Error()
	}
	return err
}

// .
func (l *Ledger) LastSeq() uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.lastSeq
}

// .
func (l *Ledger) Path() string {
	return l.path
}

// .
// .
func (l *Ledger) LastHash() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.lastHash
}

// .
func (l *Ledger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	var retErr error
	if l.writer != nil {
		if err := l.writer.Flush(); err != nil {
			retErr = errors.Join(retErr, fmt.Errorf("final flush: %w", err))
		}
	}
	if l.file != nil {
		if err := l.file.Sync(); err != nil {
			retErr = errors.Join(retErr, fmt.Errorf("final fsync: %w", err))
		}
		if err := l.file.Close(); err != nil {
			retErr = errors.Join(retErr, fmt.Errorf("close ledger: %w", err))
		}
	}
	// .
	// .
	if l.dirLock != nil {
		if err := l.dirLock.Close(); err != nil {
			retErr = errors.Join(retErr, fmt.Errorf("release the ledger directory lock: %w", err))
		}
		l.dirLock = nil
	}
	return retErr
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
const MaxEventLineBytes = 32 << 20

// .
// .
var ErrStop = errors.New("stop streaming")

// .
// .
// .
// .
// .
// .
// .
// .
func Stream(path string, fn func(*Event) error) error {
	segs, err := listSegments(filepath.Dir(path))
	if err != nil {
		return err
	}
	stopped := false
	deliver := func(evt *Event) error {
		err := fn(evt)
		if errors.Is(err, ErrStop) {
			stopped = true
		}
		return err
	}
	for i := range segs {
		if err := streamSegment(&segs[i], deliver); err != nil {
			if stopped {
				return nil
			}
			return err
		}
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	var newest *segment
	if len(segs) > 0 {
		newest = &segs[len(segs)-1]
	}
	if err := streamTail(f, newest, deliver); err != nil {
		if stopped {
			return nil
		}
		return err
	}
	return nil
}

// .
// .
// .
// .
func ReadAll(path string) ([]Event, error) {
	var events []Event
	if err := Stream(path, func(evt *Event) error {
		events = append(events, *evt)
		return nil
	}); err != nil {
		return nil, err
	}
	return events, nil
}

func decodeEvent(raw []byte) (Event, error) {
	if _, err := canonicaljson.CanonicalizeV1(raw); err != nil {
		return Event{}, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var evt *Event
	if err := dec.Decode(&evt); err != nil {
		return Event{}, err
	}
	if evt == nil {
		return Event{}, fmt.Errorf("event must be a JSON object")
	}
	return *evt, nil
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
func VerifyChain(path string, pubKey []byte, heads HeadVerifier) (int, error) {
	var expectedPrev string
	var expectedSeq uint64
	n := 0
	err := Stream(path, func(evt *Event) error {
		expectedSeq++
		if evt.Seq != expectedSeq {
			return fmt.Errorf("event %d: seq mismatch (got %d, want %d)", n, evt.Seq, expectedSeq)
		}
		if evt.Prev != expectedPrev {
			return fmt.Errorf("event %d: prev mismatch (got %q, want %q)", n, evt.Prev, expectedPrev)
		}
		if crypto.ContentHash(evt.Payload) != evt.Content {
			return fmt.Errorf("event %d: content mismatch", n)
		}
		if evt.sealed && heads == nil {
			return fmt.Errorf("event %d: %w", n, ErrSealedWithoutWitness)
		}
		if evt.Sig != "" {
			if err := verifyEventSignature(evt, pubKey); err != nil {
				return fmt.Errorf("event %d: %w", n, err)
			}
		} else if !evt.sealed {
			return fmt.Errorf("event %d: %w", n, ErrUnsignedRecord)
		}
		if evt.Type == EventSystemWitnessed && heads != nil {
			if err := heads.VerifyHead(evt); err != nil {
				if !evt.sealed && errors.Is(err, ErrWitnessKeyUnknown) {
					// .
					// .
				} else {
					return fmt.Errorf("event %d: witness head: %w", n, err)
				}
			}
		}
		expectedPrev = evt.EntryHash()
		n++
		return nil
	})
	if err != nil {
		return 0, err
	}
	return n, nil
}

var (
	ErrModelIDOwned      = errors.New("model_id is substrate-owned")
	ErrAuthorKeyMismatch = errors.New("ledger author does not match signing key")
	ErrLedgerInUse       = errors.New("ledger is already open by another process")
	ErrTailIntegrity     = errors.New("ledger tail integrity failure")
	ErrAppendUncertain   = errors.New("ledger append outcome uncertain")
	ErrEventTooLarge     = errors.New("ledger event exceeds the readable line limit")
	ErrNotCanonical      = errors.New("ledger record is not in its stored form")
	ErrUnsignedRecord    = errors.New("ledger record carries no proof")
)

func payloadObject(payload []byte) (map[string]json.RawMessage, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(payload, &object); err != nil || object == nil {
		return nil, fmt.Errorf("payload must be a JSON object")
	}
	return object, nil
}

func payloadModelID(payload []byte) (string, bool, error) {
	object, err := payloadObject(payload)
	if err != nil {
		return "", false, err
	}
	raw, ok := object["model_id"]
	if !ok {
		return "", false, nil
	}
	var modelID string
	if err := json.Unmarshal(raw, &modelID); err != nil {
		return "", true, fmt.Errorf("payload model_id is not a string: %w", err)
	}
	return modelID, true, nil
}

func stampModelID(payload []byte, modelID string) ([]byte, error) {
	object, err := payloadObject(payload)
	if err != nil {
		return nil, err
	}
	if _, supplied := object["model_id"]; supplied {
		return nil, fmt.Errorf("%w; the runtime stamps it", ErrModelIDOwned)
	}
	if modelID == "" {
		return payload, nil
	}
	stamp, err := json.Marshal(modelID)
	if err != nil {
		return nil, fmt.Errorf("encode model_id: %w", err)
	}
	object["model_id"] = stamp
	// .
	// .
	// .
	stamped, err := json.Marshal(object)
	if err != nil {
		return nil, err
	}
	return canonicaljson.CanonicalizeV1(stamped)
}
