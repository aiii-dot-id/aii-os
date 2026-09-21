package witness

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

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

// .
const TailFileName = "witness-tail.json"

// .
// .
// .
type LocalTail struct {
	LedgerOrdinal         int64  `json:"ledger_ordinal"`
	LedgerHash            string `json:"ledger_hash"`
	WitnessedAt           string `json:"witnessed_at"`
	WitnessKeyFingerprint string `json:"witness_key_fingerprint"`
}

// .
// .
// .
// .
// .
// .
// .
func writeLocalTail(dir string, tail LocalTail) error {
	raw, err := json.Marshal(tail)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, TailFileName+".tmp-")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, TailPath(dir)); err != nil {
		return err
	}
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

// .
// .
// .
func TailPath(ledgerDir string) string { return filepath.Join(ledgerDir, TailFileName) }

// .
// .
// .
// .
// .
func ReadLocalTail(ledgerDir string) (*LocalTail, error) {
	raw, err := os.ReadFile(TailPath(ledgerDir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("witness tail file unreadable: %w", err)
	}
	var tail LocalTail
	if err := json.Unmarshal(raw, &tail); err != nil {
		return nil, fmt.Errorf("witness tail file corrupt: %w", err)
	}
	if tail.LedgerOrdinal < 1 || tail.LedgerHash == "" {
		return nil, fmt.Errorf("witness tail file malformed: ordinal=%d hash=%q", tail.LedgerOrdinal, tail.LedgerHash)
	}
	return &tail, nil
}

// .
// .
// .
// .
type TailRefusal struct {
	Truncation bool
	Tail       LocalTail
	LastSeq    uint64
	Observed   string
}

func (e *TailRefusal) Error() string {
	if e.Truncation {
		return fmt.Sprintf("LEDGER TRUNCATION: ledger ends at seq %d but the witness attested event %d (%s) at %s — events the witness proved existed are gone",
			e.LastSeq, e.Tail.LedgerOrdinal, e.Tail.LedgerHash, e.Tail.WitnessedAt)
	}
	return fmt.Sprintf("LEDGER FORK: event %d hashes to %s but the witness attested %s at %s — same ordinal, different history",
		e.Tail.LedgerOrdinal, e.Observed, e.Tail.LedgerHash, e.Tail.WitnessedAt)
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
type TailCheck struct {
	tail     LocalTail
	lastSeq  uint64
	observed string
	held     bool
}

// .
func NewTailCheck(tail *LocalTail) *TailCheck {
	if tail == nil {
		return nil
	}
	return &TailCheck{tail: *tail}
}

// .
// .
func (c *TailCheck) Visit(evt *ledger.Event) error {
	if c == nil {
		return nil
	}
	c.lastSeq = evt.Seq
	if int64(evt.Seq) == c.tail.LedgerOrdinal {
		c.observed = evt.EntryHash()
	}
	return nil
}

// .
// .
// .
func (c *TailCheck) Held() error {
	if c == nil {
		return nil
	}
	if int64(c.lastSeq) < c.tail.LedgerOrdinal {
		return &TailRefusal{Truncation: true, Tail: c.tail, LastSeq: c.lastSeq}
	}
	if c.observed != c.tail.LedgerHash {
		return &TailRefusal{Tail: c.tail, LastSeq: c.lastSeq, Observed: c.observed}
	}
	c.held = true
	return nil
}

// .
// .
// .
// .
// .
func (c *TailCheck) Summary() string {
	if c == nil {
		return "no witness tail beside it — nothing outside the chain says how far it once reached"
	}
	hash := c.tail.LedgerHash
	if len(hash) > 12 {
		hash = hash[:12]
	}
	if c.held {
		return fmt.Sprintf("holds record %d (%s) as the witness tail beside it names it, witnessed %s", c.tail.LedgerOrdinal, hash, c.tail.WitnessedAt)
	}
	return fmt.Sprintf("the witness tail beside it names record %d (%s), witnessed %s", c.tail.LedgerOrdinal, hash, c.tail.WitnessedAt)
}

// .
// .
// .
// .
type Beside struct {
	Heads *HeadVerifier
	Tail  *TailCheck
}

// .
// .
// .
func LoadBeside(ledgerDir string, platform *PublicKeyEnvelope) (*Beside, error) {
	heads, err := LoadHeadVerifier(ledgerDir, platform)
	if err != nil {
		return nil, fmt.Errorf("witness keys beside the ledger: %w", err)
	}
	tail, err := ReadLocalTail(ledgerDir)
	if err != nil {
		return nil, fmt.Errorf("beside the ledger: %w", err)
	}
	return &Beside{Heads: heads, Tail: NewTailCheck(tail)}, nil
}

// .
func (b *Beside) VerifyHead(evt *ledger.Event) error { return b.Heads.VerifyHead(evt) }

// .
func (b *Beside) Visit(evt *ledger.Event) error { return b.Tail.Visit(evt) }
func (b *Beside) Held() error                   { return b.Tail.Held() }

// .
// .
func (b *Beside) Summary() string { return b.Heads.Summary() + "; " + b.Tail.Summary() }
