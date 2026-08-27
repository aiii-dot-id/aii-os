// Package ledger implements an append-only, hash-chained, PQ-signed event log.
//
// The ledger is the sole truth of an AII OS identity. Every identity change
// is an Event. Events are chained via prev_hash and signed with ML-DSA-87.
//
// The signature is over the GOLD ledger-line envelope (see
// SignatureInputGold): a canonical-entry hash that binds every field,
// plus the crypto-agility metadata. One format for the Go and C stacks
// (2026-08-17 ruling; docs/LEDGER_GOLD_FORMAT.md).
package ledger

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"sync"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/canonicaljson"
	"github.com/aiii-dot-id/aii-os/internal/crypto"
)

// readTailEvents reads the last k+1 events from the locked ledger WITHOUT
// reading the whole file (M6): it grows a window from the end until it
// holds enough complete lines, drops the partial first line, and parses
// the rest.
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
				// drop the (possibly partial) first line
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

// Event is the atomic unit of the append-only ledger.
type Event struct {
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

// EventType identifies what kind of identity change an Event represents.
type EventType string

const (
	// Ring 0 — Constitution
	EventRing0Genesis EventType = "ring0.genesis"

	// Ring 1 — Charter
	EventRelationshipUpsert EventType = "relationship.upsert"

	// Ring 2 — Identity
	EventBeliefPromote EventType = "belief.promote"

	// Ring 3 — Working truth
	EventExperienceCreate    EventType = "experience.create"
	EventBeliefUpsert        EventType = "belief.upsert"
	EventEdgeCreate          EventType = "edge.create"
	EventSelfModelSynthesize EventType = "self_model.synthesize"

	// Ring 3 — lifecycle entities (transition, never upsert — Q2)
	EventIntentionCreate       EventType = "intention.create"
	EventIntentionStateChange  EventType = "intention.state_change"
	EventCommitmentPromised    EventType = "commitment.promised"
	EventCommitmentStateChange EventType = "commitment.state_change"

	// Ring 3 — identity change exits
	EventBeliefArchive   EventType = "belief.archive"
	EventBeliefSupersede EventType = "belief.supersede"
	EventEdgeArchive     EventType = "edge.archive"

	// Ring 3 — working style (materializes to beliefs, Q1-family)
	EventWorkingStyleUpsert EventType = "working_style.upsert"

	// Ring 3 — facility run markers (external review 2026-08-20, H6/#4).
	// A metabolizing facility closes each run with ONE of these, minted
	// LAST: payload {inputs: [experience ids read], outputs: [seqs of
	// events this run minted]}. The MATERIALIZER marks the inputs
	// consumed — replay restores consumed state because it is f(ledger),
	// never a bare store UPDATE from cognition. Run events carry RESULTS
	// (ids), never instructions: replay must never re-run an LLM. Crash
	// before the marker = clean re-run (belief.upsert idempotence absorbs
	// the duplicated products).
	EventConsolidationRun EventType = "consolidation.run"
	EventDreamRun         EventType = "dream.run"

	// Meta-layer — truth-protecting anchors. The receipt is evidence
	// ABOUT the ledger, carried BY the ledger: the chain holds its own
	// continuity proof points.
	EventSystemWitnessed EventType = "system.witnessed"

	// Meta-layer — plugin-trust anti-rollback (PLUGIN_REVOCATION_DESIGN
	// §2.3): the highest revocation-snapshot trust_epoch accepted per
	// root, ledgered so a later lower-epoch snapshot is provably a
	// rollback. Same pattern as system.witnessed: an auditable fact
	// ABOUT trust state, carried BY the signed chain, read back through
	// its projection.
	EventTrustEpochAccepted EventType = "trust.epoch_accepted"
)

// CanonicalRings returns the rings an event type may legally mint at —
// the closed authority table beside the closed vocabulary (canon
// IDENTITY_SEMANTICS §11: the gate validates ring authority
// structurally; rings derive in the owner and are VALIDATED here, never
// trusted). Every type in AllEventTypes has an entry — the ring-gate
// drift test enforces that, same pattern as the materializer table.
// system.witnessed is the meta-layer anchor: ring 0 in the envelope,
// not an identity ring (the verb-path gates never see it — it mints
// only through the witness minter).
func CanonicalRings(t EventType) []int {
	switch t {
	case EventRing0Genesis:
		return []int{0} // birth only; the runtime never re-mints it
	case EventRelationshipUpsert:
		return []int{1}
	case EventBeliefPromote:
		return []int{2} // conscious identity promotion; standing derives at read time
	case EventExperienceCreate, EventBeliefUpsert, EventEdgeCreate,
		EventSelfModelSynthesize, EventIntentionCreate, EventIntentionStateChange,
		EventCommitmentPromised, EventCommitmentStateChange,
		EventWorkingStyleUpsert, EventBeliefArchive, EventBeliefSupersede,
		EventEdgeArchive,
		// Run markers are the facility's own act: the unconscious authors
		// at Ring 3 (its products — dream notes, belief distillations —
		// already mint there), so its closing marker carries the same
		// authority, signed with the identity key like every facility mint.
		EventConsolidationRun, EventDreamRun:
		return []int{3}
	case EventSystemWitnessed:
		return []int{0} // meta-layer anchor
	case EventTrustEpochAccepted:
		return []int{0} // meta-layer trust fact — mints only through the epoch-guard adapter
	default:
		return nil // unknown type: no legal ring — fails closed
	}
}

// AllEventTypes returns every EventType constant — the closed vocabulary,
// one source of truth. The entity-types gate test and the materializer
// table test both iterate THIS; a list duplicated anywhere else is drift
// surface.
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
	}
}

// Ledger is an append-only log backed by a JSONL file.
type Ledger struct {
	path     string
	mu       sync.Mutex
	file     *os.File
	writer   *bufio.Writer
	lastSeq  uint64
	lastHash string
	modelID  string // substrate provenance — stamped into every payload before hashing

	// frozenReason, when non-empty, refuses every Append — the SAFE-mode
	// enforcement point (2026-08-17 external review, S1). SAFE was
	// previously enforced per-verb, which makes every future verb a
	// chance to silently escape it (R14: a gate never exercised is
	// decorative); the ledger is the single writer, so "SAFE ⇒ no
	// minting" is checkable HERE and nowhere else needs to be right.
	// Set-only: SAFE never self-exits. Guarded by mu.
	frozenReason string
}

// SetFrozen refuses all further appends with the given reason. There is
// deliberately no unfreeze: SAFE exits only through operator
// intervention and a restart (docs/SAFE_DEGRADED.md).
func (l *Ledger) SetFrozen(reason string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.frozenReason == "" {
		l.frozenReason = reason
	}
}

// New opens a ledger for exclusive use by this process.
func New(path string) (*Ledger, error) {
	l := &Ledger{
		path:     path,
		lastHash: "",
		lastSeq:  0,
	}

	// Lock before reading or repairing the tail. Two processes must never
	// derive the same next sequence from one file.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0600)
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
	return l, nil
}

// readChainState reads the ledger file to find the last event and the
// chain state to build on. It is CRASH-TOLERANT of a torn trailing
// line: a process killed mid-Append (mid write(2), before its fsync
// returned) can leave a partial final line. That event was never
// acknowledged to any caller — the durable claim is "an event survives
// iff Append returned nil" — so a torn tail is truncated off, leaving a
// clean chain to build on. An INTERIOR malformed line is real
// corruption and stays fatal; only the FINAL line, and only when it is
// unterminated by a newline (the write never completed), is forgiven
// (2026-08-19 crash hardening — before this a torn tail refused boot,
// turning a survivable kill into a dead runtime).
func (l *Ledger) readChainState(file *os.File) error {
	data, err := io.ReadAll(file)
	if err != nil {
		return err
	}
	// SplitAfter keeps the '\n' on every complete line; the final piece
	// lacks one iff the file does not end in a newline (a torn write).
	pieces := bytes.SplitAfter(data, []byte("\n"))
	goodBytes := 0
	for i, piece := range pieces {
		terminated := bytes.HasSuffix(piece, []byte("\n"))
		line := bytes.TrimSpace(piece)
		if len(line) == 0 {
			goodBytes += len(piece)
			continue
		}
		// A final JSON value without its newline is still an incomplete
		// append: accepting it would concatenate the next event onto it.
		// Quarantine before truncating because the file alone cannot tell
		// crash debris from a committed event damaged in place.
		if i == len(pieces)-1 && !terminated {
			sidecar := fmt.Sprintf("%s.torn-%d", l.path, time.Now().UTC().UnixNano())
			if err := os.WriteFile(sidecar, piece, 0600); err != nil {
				return fmt.Errorf("torn trailing line, quarantine failed (refusing to truncate unpreserved bytes): %w", err)
			}
			if err := file.Truncate(int64(goodBytes)); err != nil {
				return fmt.Errorf("torn trailing line, truncate failed: %w", err)
			}
			log.Printf("ledger: dropped a torn trailing line (%d bytes) — quarantined at %s; if the projection mirror remembers more events than the ledger now holds, this was NOT crash debris", len(piece), sidecar)
			return nil
		}
		evt, err := decodeEvent(line)
		if err != nil {
			return fmt.Errorf("malformed ledger line: %w", err)
		}
		l.lastSeq = evt.Seq
		l.lastHash = evt.ContentHash
		goodBytes += len(piece)
	}
	return nil
}

// SetModelID sets the substrate provenance stamp. Every Append injects
// this into the payload BEFORE hashing and signing — the signature covers
// model provenance, so it cannot be forged by rewriting the JSONL.
// An unsigned provenance field would be worse than none: confidence
// without evidence. Call before the first Append; must match the model
// the LLM client actually uses.
func (l *Ledger) SetModelID(modelID string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.modelID = modelID
}

// verifyTailBeforeAppend verifies the bounded tail before extending.
const tailCheckDepth = 8

func (l *Ledger) verifyTailBeforeAppend(kp *crypto.KeyPair) error {
	// M6 (external review): this ran ReadAll — the WHOLE file, under
	// l.mu, on every append — to check the last few events: O(n²) ledger
	// growth. Now it reads only the tail from disk (the on-disk read is
	// the point: the check exists to catch file-level tampering and
	// corruption mid-run, so an in-memory tail would verify nothing).
	events, err := readTailEvents(l.file, tailCheckDepth)
	if err != nil {
		return err
	}
	n := len(events)
	if n == 0 {
		if l.lastSeq != 0 {
			return fmt.Errorf("tail read found no events but lastSeq=%d — ledger file truncated", l.lastSeq)
		}
		return nil
	}
	pubKey := kp.PublicKeyBytes()
	for i, evt := range events {
		if i > 0 {
			if evt.Seq != events[i-1].Seq+1 {
				return fmt.Errorf("tail event seq %d: not consecutive after %d", evt.Seq, events[i-1].Seq)
			}
			if evt.PrevHash != events[i-1].ContentHash {
				return fmt.Errorf("tail event seq %d: prev_hash linkage broken", evt.Seq)
			}
		}
		if crypto.ContentHash(evt.Payload) != evt.ContentHash {
			return fmt.Errorf("tail event seq %d: content_hash mismatch", evt.Seq)
		}
		if err := verifyEventSignature(evt, pubKey); err != nil {
			return fmt.Errorf("tail event seq %d: %w", evt.Seq, err)
		}
	}
	if events[n-1].Seq != l.lastSeq {
		return fmt.Errorf("tail ends at seq %d but lastSeq=%d — ledger file diverged from chain state", events[n-1].Seq, l.lastSeq)
	}
	if l.lastHash != events[n-1].ContentHash {
		return fmt.Errorf("in-memory chain state diverged from file tail")
	}
	return nil
}

// --- The GOLD ledger-line signature envelope ---
//
// James's ruling (2026-08-17): one canonical ledger format for the Go
// and C stacks — format differences must not matter; the Go ledger is
// the reference and C conforms. This envelope adopts the C Canon V4
// grammar (the stronger of the two prior formats) with Go as gold:
//
//	AII-LEDGER-LINE-SIGNATURE-GOLD
//	artifact_kind:aii.ledger.line
//	canonicalization:aii-canonical-json-v1
//	suite_id:aii-pq-mldsa87
//	role:identity
//	alg:<signature algorithm>
//	key_id:<signer fingerprint>
//	entry_sha256:<hex sha256 of the canonical entry, signature fields excluded>
//
// Two properties the prior flat-field input (V1) lacked:
//   - entry_sha256 covers the CANONICAL FORM OF THE WHOLE ENTRY, so any
//     field the Event ever grows is signed automatically — the
//     unsigned-field tamper class (finding 1: type/ring/timestamp rode
//     unsigned for months) dies structurally instead of by vigilance.
//   - the envelope binds the crypto-agility metadata (canonicalization
//     id, suite, role, alg, key id) that a format other implementations
//     must verify cannot leave implicit.
//
// The canonicalization named here is consensus-critical for interop:
// the C implementation must produce byte-identical canonical JSON (see
// docs/LEDGER_GOLD_FORMAT.md and the pinned vectors in
// internal/ledger/testdata/). R53 fixes role at "identity"; there is no
// operator-key tier. Pre-gold format changes need no era machinery:
// re-wrap (cmd/rewrap) and regenerate — there are no v1/v2 systems,
// only the format on the way to gold.

const (
	goldSignatureTag     = "AII-LEDGER-LINE-SIGNATURE-GOLD"
	goldArtifactKind     = "aii.ledger.line"
	goldCanonicalization = "aii-canonical-json-v1"
	goldSuiteID          = "aii-pq-mldsa87"
	goldRoleIdentity     = "identity"
)

// EntrySHA256 computes the canonical-entry hash the gold envelope binds.
func EntrySHA256(evt *Event) (string, error) {
	raw, err := entryForSigningJSON(evt)
	if err != nil {
		return "", err
	}
	sum, err := canonicaljson.CanonicalizeV1SHA256(raw)
	if err != nil {
		return "", fmt.Errorf("entry canonicalization: %w", err)
	}
	return sum, nil
}

func entryForSigningJSON(evt *Event) ([]byte, error) {
	if evt == nil {
		return nil, fmt.Errorf("entry is nil")
	}
	raw, err := json.Marshal(evt)
	if err != nil {
		return nil, fmt.Errorf("entry marshal: %w", err)
	}
	var entry map[string]json.RawMessage
	if err := json.Unmarshal(raw, &entry); err != nil {
		return nil, fmt.Errorf("entry decode: %w", err)
	}
	delete(entry, "signature")
	delete(entry, "sig_alg")
	delete(entry, "sig_key_id")
	raw, err = json.Marshal(entry)
	if err != nil {
		return nil, fmt.Errorf("entry marshal: %w", err)
	}
	return raw, nil
}

// SignatureInputGold builds the domain-separated byte string every event
// signature is computed over.
func SignatureInputGold(alg, keyID, entrySHA256 string) []byte {
	return []byte(goldSignatureTag + "\n" +
		"artifact_kind:" + goldArtifactKind + "\n" +
		"canonicalization:" + goldCanonicalization + "\n" +
		"suite_id:" + goldSuiteID + "\n" +
		"role:" + goldRoleIdentity + "\n" +
		"alg:" + alg + "\n" +
		"key_id:" + keyID + "\n" +
		"entry_sha256:" + entrySHA256 + "\n")
}

func verifyEventSignature(evt *Event, pubKey []byte) error {
	if !crypto.VerifyFingerprint(pubKey, evt.SigKeyID) {
		return fmt.Errorf("public key does not match sig_key_id %q", evt.SigKeyID)
	}
	if evt.SigAlg != crypto.SigAlg {
		return fmt.Errorf("sig_alg %q is not the suite's %q", evt.SigAlg, crypto.SigAlg)
	}
	if evt.Author != evt.SigKeyID {
		return fmt.Errorf("author %q != sig_key_id %q", evt.Author, evt.SigKeyID)
	}
	entrySHA, err := EntrySHA256(evt)
	if err != nil {
		return err
	}
	sig, err := base64.StdEncoding.DecodeString(evt.Signature)
	if err != nil {
		return fmt.Errorf("invalid signature encoding: %w", err)
	}
	if err := crypto.Verify(pubKey, SignatureInputGold(evt.SigAlg, evt.SigKeyID, entrySHA), sig); err != nil {
		return fmt.Errorf("signature verification failed: %w", err)
	}
	return nil
}

// PreparedPayload holds ledger-owned bytes ready for validation and append.
type PreparedPayload struct {
	raw json.RawMessage
}

// Bytes returns a copy for validation.
func (p PreparedPayload) Bytes() []byte {
	return bytes.Clone(p.raw)
}

// PreparePayload encodes and stamps the exact bytes admission validates.
func (l *Ledger) PreparePayload(payload interface{}) (PreparedPayload, error) {
	l.mu.Lock()
	modelID := l.modelID
	l.mu.Unlock()
	return preparePayload(payload, modelID)
}

// PreparePayloadWithModel preserves the provenance of completed model work
// when the active substrate changes before admission.
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
	return PreparedPayload{raw: raw}, nil
}

// Append creates, signs, and durably appends an event.
func (l *Ledger) Append(eventType EventType, author string, ring int, payload interface{}, kp *crypto.KeyPair) (*Event, error) {
	prepared, err := l.PreparePayload(payload)
	if err != nil {
		return nil, err
	}
	return l.AppendPrepared(eventType, author, ring, prepared, kp)
}

// AppendPrepared signs and durably appends exact prepared bytes.
func (l *Ledger) AppendPrepared(eventType EventType, author string, ring int, payload PreparedPayload, kp *crypto.KeyPair) (*Event, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	raw := payload.raw

	// SAFE enforcement (S1): frozen means frozen, for every caller —
	// verbs, facilities, witness receipts, whatever gets added next.
	if l.frozenReason != "" {
		return nil, fmt.Errorf("append refused — ledger frozen (SAFE): %s", l.frozenReason)
	}

	// Mid-run integrity gate (SAFE_DEGRADED.md): verify the tail before
	// extending. A corrupted tail surfaces HERE (read-time verification
	// only catches it at boot otherwise). The error is the SAFE trigger.
	if err := l.verifyTailBeforeAppend(kp); err != nil {
		err = fmt.Errorf("%w: %w", ErrTailIntegrity, err)
		l.frozenReason = err.Error()
		return nil, err
	}
	if author != kp.Fingerprint() {
		return nil, fmt.Errorf("%w: author %q does not match signing key %q", ErrAuthorKeyMismatch, author, kp.Fingerprint())
	}

	modelID, _, err := payloadModelID(raw)
	if err != nil {
		return nil, err
	}

	// Compute content hash
	contentHash := crypto.ContentHash(raw)

	// Compute next seq
	seq := l.lastSeq + 1

	// Compute prev_hash: empty string for genesis, else last event's content_hash
	prevHash := ""
	if seq > 1 {
		prevHash = l.lastHash
	}

	evt := Event{
		Seq:         seq,
		PrevHash:    prevHash,
		Timestamp:   nowUTC(),
		Type:        eventType,
		Author:      author,
		Ring:        ring,
		Payload:     raw,
		ContentHash: contentHash,
		SigAlg:      crypto.SigAlg,
		SigKeyID:    kp.Fingerprint(),
		ModelID:     modelID,
	}

	// Sign the GOLD envelope: the canonical entry hash plus the bound
	// crypto metadata. Any field rewrite anywhere on disk — including
	// fields added to Event in the future — breaks verification here.
	entrySHA, err := EntrySHA256(&evt)
	if err != nil {
		return nil, fmt.Errorf("entry hash failed: %w", err)
	}
	sigB64, err := crypto.SignB64(kp, SignatureInputGold(evt.SigAlg, evt.SigKeyID, entrySHA))
	if err != nil {
		return nil, fmt.Errorf("signing failed: %w", err)
	}
	evt.Signature = sigB64

	// Marshal event to JSON
	line, err := json.Marshal(evt)
	if err != nil {
		return nil, fmt.Errorf("event marshal failed: %w", err)
	}

	// REFUSE AT THE DOOR what the reader could never take back out.
	// Nothing has been written yet, so this costs the caller an error
	// and the chain nothing — the alternative was a durable, correctly
	// signed event that bricks every future boot. Not a freeze: the
	// ledger is fine, the caller asked for too much.
	if len(line) > MaxEventLineBytes {
		return nil, fmt.Errorf("%w: %s would serialize to %d bytes, over the %d-byte limit ReadAll can consume",
			ErrEventTooLarge, eventType, len(line), MaxEventLineBytes)
	}

	// Write line + newline
	if _, err := l.writer.Write(line); err != nil {
		return nil, l.uncertainAppend(fmt.Errorf("write failed: %w", err))
	}
	if err := l.writer.WriteByte('\n'); err != nil {
		return nil, l.uncertainAppend(fmt.Errorf("write newline failed: %w", err))
	}
	if err := l.writer.Flush(); err != nil {
		return nil, l.uncertainAppend(fmt.Errorf("flush failed: %w", err))
	}
	// Durability: Flush only reaches the OS — a power loss could drop
	// events the code reported as signed and stored. The chain's core
	// claim is append-and-survive; sync it (2026-08-17 review).
	if err := l.file.Sync(); err != nil {
		return nil, l.uncertainAppend(fmt.Errorf("fsync failed: %w", err))
	}

	// Update chain state
	l.lastSeq = seq
	l.lastHash = contentHash

	return &evt, nil
}

func (l *Ledger) uncertainAppend(cause error) error {
	err := fmt.Errorf("%w: %w", ErrAppendUncertain, cause)
	if l.frozenReason == "" {
		l.frozenReason = err.Error()
	}
	return err
}

// LastSeq returns the sequence number of the last appended event.
func (l *Ledger) LastSeq() uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.lastSeq
}

// Path returns the filesystem path of the ledger file.
func (l *Ledger) Path() string {
	return l.path
}

// LastHash returns the content hash of the last appended event.
func (l *Ledger) LastHash() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.lastHash
}

// Close flushes and closes the ledger file.
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
	return retErr
}

// MaxEventLineBytes bounds ONE serialized event line, on both sides.
//
// The writer had no limit and the reader stopped at exactly 1 MiB. An
// event over that appended, signed and fsynced perfectly — and then
// every subsequent boot failed, because chain verification and replay
// both go through ReadAll and both died on bufio.ErrTooLong. A
// legitimate long note, DREAM output, belief or self-model could
// therefore make an identity permanently unbootable, with the ledger
// itself intact and the damage undoable by design: events are not
// removable.
//
// One constant, both sides, so they cannot disagree again. 32 MiB is
// the ceiling a provider response can reach; the reader's buffer STARTS
// at 64 KiB and grows only as far as a line needs, so a phone does not
// pay 32 MiB to read a ledger of ordinary events.
const MaxEventLineBytes = 32 << 20

// ReadAll reads every event from the ledger file.
// Used for chain verification and projection replay.
func ReadAll(path string) ([]Event, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	return readEvents(f)
}

func readEvents(r io.Reader) ([]Event, error) {
	var events []Event
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), MaxEventLineBytes)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		evt, err := decodeEvent(line)
		if err != nil {
			return nil, fmt.Errorf("malformed line: %w", err)
		}
		events = append(events, evt)
	}

	return events, scanner.Err()
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

// VerifyChain reads the ledger file and verifies:
// 1. Chain integrity (prev_hash links are correct)
// 2. Content hashes match payloads
// 3. All signatures verify against the author's public key
//
// authorKeys maps fingerprint -> public key bytes.
// For the minimal version (one key), the identity's own fingerprint maps
// to its public key.
func VerifyChain(path string, authorKeys map[string][]byte) error {
	events, err := ReadAll(path)
	if err != nil {
		return err
	}

	var expectedPrevHash string = ""
	var expectedSeq uint64 = 0

	for i, evt := range events {
		expectedSeq++

		// Check seq
		if evt.Seq != expectedSeq {
			return fmt.Errorf("event %d: seq mismatch (got %d, want %d)", i, evt.Seq, expectedSeq)
		}

		// Check prev_hash
		if evt.PrevHash != expectedPrevHash {
			return fmt.Errorf("event %d: prev_hash mismatch (got %q, want %q)", i, evt.PrevHash, expectedPrevHash)
		}

		// Check content_hash
		actualHash := crypto.ContentHash(evt.Payload)
		if evt.ContentHash != actualHash {
			return fmt.Errorf("event %d: content_hash mismatch", i)
		}
		payloadModel, hasPayloadModel, err := payloadModelID(evt.Payload)
		if err != nil {
			return fmt.Errorf("event %d: %w", i, err)
		}
		if (!hasPayloadModel && evt.ModelID != "") ||
			(hasPayloadModel && (evt.ModelID == "" || payloadModel != evt.ModelID)) {
			return fmt.Errorf("event %d: envelope model_id %q does not match payload model_id %q", i, evt.ModelID, payloadModel)
		}

		// Check signature — over the GOLD envelope: the canonical entry
		// hash binds every field (finding 1's tamper class, closed
		// structurally), and the input binds alg/key/canonicalization.
		pubKey, ok := authorKeys[evt.SigKeyID]
		if !ok {
			return fmt.Errorf("event %d: unknown signing key %s", i, evt.SigKeyID)
		}
		if err := verifyEventSignature(&evt, pubKey); err != nil {
			return fmt.Errorf("event %d: %w", i, err)
		}

		expectedPrevHash = evt.ContentHash
	}

	return nil
}

var (
	ErrModelIDOwned      = errors.New("model_id is substrate-owned")
	ErrAuthorKeyMismatch = errors.New("ledger author does not match signing key")
	ErrLedgerInUse       = errors.New("ledger is already open by another process")
	ErrTailIntegrity     = errors.New("ledger tail integrity failure")
	ErrAppendUncertain   = errors.New("ledger append outcome uncertain")
	ErrEventTooLarge     = errors.New("ledger event exceeds the readable line limit")
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
	return json.Marshal(object)
}
