package witness

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/canonicaljson"
)

const OperationFileName = "witness-operation.json"

const AbandonedDirName = "witness-abandoned"

const (
	operationMaxRequestBytes = 1 << 20
	operationMaxRetained     = 16

	OperationMaxBytes = (operationMaxRetained+1)*(4*((operationMaxRequestBytes+2)/3)+4096) + 64<<10
)

var ErrWitnessOperationActive = errors.New("a witness operation this version cannot carry out stands beside the ledger")

func OperationBarrier(ledgerDir string) error {
	_, err := os.Lstat(filepath.Join(ledgerDir, OperationFileName))
	switch {
	case errors.Is(err, os.ErrNotExist):
		return nil
	case err != nil:
		return fmt.Errorf("%w: whether %s stands cannot be told (%v)", ErrWitnessOperationActive, OperationFileName, err)
	}
	return fmt.Errorf("%w (%s)", ErrWitnessOperationActive, OperationFileName)
}

func WitnessBarrier(ledgerDir string) error {
	if err := OperationBarrier(ledgerDir); err != nil {
		return err
	}
	if _, err := readPendingRequest(ledgerDir); err != nil {
		return fmt.Errorf("%w: %s does not read as this version's and may hold a later version's work; its bytes are kept (%v)", ErrWitnessOperationActive, PendingRequestFileName, err)
	}
	return nil
}

var archiveNameRe = regexp.MustCompile(`^[0-9a-f]{64}\.json$`)

func ArchiveNameValid(name string) bool { return archiveNameRe.MatchString(name) }

func CheckArchive(name string, raw []byte) error {
	if !ArchiveNameValid(name) {
		return fmt.Errorf("%s is not an operation archive's name", name)
	}
	if len(raw) > OperationMaxBytes {
		return fmt.Errorf("%s is %d bytes, more than an operation file can be (%d)", name, len(raw), OperationMaxBytes)
	}
	sum := sha256.Sum256(raw)
	if hex.EncodeToString(sum[:])+".json" != name {
		return fmt.Errorf("%s does not hold the bytes its name is the digest of", name)
	}
	return nil
}

func CheckOperationSize(n int64) error {
	if n > OperationMaxBytes {
		return fmt.Errorf("%s is %d bytes, more than an operation file can be (%d)", OperationFileName, n, OperationMaxBytes)
	}
	return nil
}

type Operation struct {
	V            int             `json:"v"`
	Kind         string          `json:"kind"`
	Route        OperationRoute  `json:"route"`
	ExpectedOld  *OperationRoute `json:"expected_old,omitempty"`
	ExpectedHead json.RawMessage `json:"expected_head"`
	Active       Candidate       `json:"active"`
	Retained     []Candidate     `json:"retained"`
	Receipt      json.RawMessage `json:"receipt"`
	Stage        string          `json:"stage"`
	WrittenAt    string          `json:"written_at"`
}

type OperationRoute struct {
	URL           string `json:"url"`
	TLSSPKISHA256 string `json:"tls_spki_sha256"`
}

type Candidate struct {
	RequestB64    string `json:"request_b64"`
	ContextSHA256 string `json:"context_sha256"`
	TargetKeyID   string `json:"target_key_id"`
	TargetKeyHash string `json:"target_key_hash"`
	Outcome       string `json:"outcome"`
}

var (
	sha256Form     = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	ledgerHashForm = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

func ReadOperation(raw []byte) (*Operation, error) {
	if len(raw) > OperationMaxBytes {
		return nil, fmt.Errorf("%d bytes, more than an operation file can be (%d)", len(raw), OperationMaxBytes)
	}
	var op Operation
	if err := decodeExact(raw, &op); err != nil {
		return nil, err
	}
	if op.V != 1 {
		return nil, fmt.Errorf("version %d, not 1", op.V)
	}
	switch op.Kind {
	case "ordinary":
		if op.ExpectedOld != nil {
			return nil, errors.New("an ordinary operation names an old route")
		}
	case "cutover":
		if op.ExpectedOld == nil || op.ExpectedOld.URL == "" {
			return nil, errors.New("a cutover names no old route")
		}
	default:
		return nil, fmt.Errorf("kind %q", op.Kind)
	}
	if op.Route.URL == "" {
		return nil, errors.New("no route")
	}
	switch op.Stage {
	case "staged", "sent", "receipt", "head", "config":
	default:
		return nil, fmt.Errorf("stage %q", op.Stage)
	}
	if _, err := time.Parse(time.RFC3339Nano, op.WrittenAt); err != nil {
		return nil, fmt.Errorf("written_at: %w", err)
	}
	head, _, err := readExpectedHead(op.ExpectedHead)
	if err != nil {
		return nil, fmt.Errorf("expected_head: %w", err)
	}
	if len(op.Retained) > operationMaxRetained {
		return nil, fmt.Errorf("%d retained candidates, more than %d", len(op.Retained), operationMaxRetained)
	}
	for i, c := range append([]Candidate{op.Active}, op.Retained...) {
		if err := checkCandidate(c, head); err != nil {
			if i == 0 {
				return nil, fmt.Errorf("the active candidate: %w", err)
			}
			return nil, fmt.Errorf("retained candidate %d: %w", i, err)
		}
	}
	if r := bytes.TrimSpace(op.Receipt); len(r) == 0 {
		return nil, errors.New("no receipt member")
	} else if !bytes.Equal(r, []byte("null")) {

		var receipt WitnessReceipt
		if err := decodeExact(r, &receipt); err != nil {
			return nil, fmt.Errorf("receipt: %w", err)
		}
		if err := checkContextDigestMember(r, receipt.ContextSHA256); err != nil {
			return nil, fmt.Errorf("receipt: %w", err)
		}
	}
	return &op, nil
}

func decodeExact(raw []byte, out any) error {
	canonical, err := canonicaljson.CanonicalizeV1(raw)
	if err != nil {
		return fmt.Errorf("not JSON as the format writes it: %w", err)
	}
	return canonicaljson.DecodeStrict(canonical, out)
}

func checkCandidate(c Candidate, head string) error {
	switch {
	case c.Outcome == "unknown", c.Outcome == "committed":
	case strings.HasPrefix(c.Outcome, "refused:") && len(c.Outcome) > len("refused:"):
	default:
		return fmt.Errorf("outcome %q", c.Outcome)
	}
	if !validKeyIDFilename(c.TargetKeyID) {
		return fmt.Errorf("target_key_id %q", c.TargetKeyID)
	}
	if !sha256Form.MatchString(c.TargetKeyHash) || !sha256Form.MatchString(c.ContextSHA256) {
		return errors.New("a hash not in its sha256: form")
	}
	if len(c.RequestB64) > 4*((operationMaxRequestBytes+2)/3) {
		return fmt.Errorf("request is more than %d bytes", operationMaxRequestBytes)
	}
	request, err := base64.StdEncoding.DecodeString(c.RequestB64)
	if err != nil {
		return fmt.Errorf("request_b64: %w", err)
	}
	var signed struct {
		IdentityID        string          `json:"identity_id"`
		IdentityPublicKey json.RawMessage `json:"identity_public_key"`
		LedgerOrdinal     int64           `json:"ledger_ordinal"`
		LedgerHash        string          `json:"ledger_hash"`
		IdentitySignature json.RawMessage `json:"identity_signature"`
		Context           json.RawMessage `json:"context"`
	}
	if err := decodeExact(request, &signed); err != nil {
		return fmt.Errorf("the signed request: %w", err)
	}
	if len(bytes.TrimSpace(signed.Context)) == 0 || isNull(signed.Context) {
		return errors.New("the signed request carries no context")
	}
	ctx, err := readContext(signed.Context)
	if err != nil {
		return fmt.Errorf("the signed context: %w", err)
	}
	if ctx.digest != c.ContextSHA256 {
		return fmt.Errorf("context_sha256 %s, the signed context digests to %s", c.ContextSHA256, ctx.digest)
	}
	if ctx.targetKeyHash != c.TargetKeyHash {
		return fmt.Errorf("the signed context is for %s, the candidate names %s", ctx.targetKeyHash, c.TargetKeyHash)
	}
	if ctx.expected != head {
		return fmt.Errorf("the signed context expects %s, the operation %s", ctx.expected, head)
	}
	return nil
}
