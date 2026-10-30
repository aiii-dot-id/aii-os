package witness

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/aiii-dot-id/aii-os/internal/atomicfile"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

const EnvelopeFileName = "witness-envelope.json"

func EnvelopePath(ledgerDir string) string { return filepath.Join(ledgerDir, EnvelopeFileName) }

var ErrEnvelopeRowDisagrees = errors.New("the store's witness envelope names another witness identity than the record")

func readEnvelopeFile(ledgerDir string, key IdentityKey) (canonical []byte, env *PublicKeyEnvelope, id string, err error) {
	raw, err := atomicfile.ReadFile(EnvelopePath(ledgerDir))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil, "", nil
		}
		return nil, nil, "", fmt.Errorf("%s unreadable: %w", EnvelopeFileName, err)
	}

	canonical, env, err = parseIdentityEnvelope(bytes.TrimSpace(raw))
	if err != nil {
		return nil, nil, "", fmt.Errorf("%s is not an identity's witness envelope: %w", EnvelopeFileName, err)
	}
	if !holdsKey(env, key) {
		return nil, nil, "", fmt.Errorf("%s holds another key than this identity's", EnvelopeFileName)
	}
	if id, err = DeriveIdentityID(canonical, env); err != nil {
		return nil, nil, "", fmt.Errorf("%s: %w", EnvelopeFileName, err)
	}
	return canonical, env, id, nil
}

func holdsKey(env *PublicKeyEnvelope, key IdentityKey) bool {
	ml, ok := env.FindPublicKey(AlgMLDSA87)
	return ok && ml.PublicKeyB64 == key.PublicKeyB64()
}

func writeEnvelopeFile(ledgerDir string, key IdentityKey, canonical []byte) error {
	_, env, err := parseIdentityEnvelope(canonical)
	if err != nil {
		return err
	}
	if !holdsKey(env, key) {
		return fmt.Errorf("the envelope holds another key than this identity's: not kept beside the ledger")
	}
	published, err := atomicfile.WriteReplace(EnvelopePath(ledgerDir), canonical, 0o600)
	if err != nil && published {
		return fmt.Errorf("%s published but not durable: %w", EnvelopeFileName, err)
	}
	return err
}

func cacheEnvelope(store EnvelopeStore, canonical []byte) {
	row, err := store.LoadWitnessEnvelope()
	switch {
	case err != nil:
		logsink.Warn("witness.error", "the store's copy of the witness envelope could not be read (%v); the envelope beside the ledger is used", err)
		return
	case row == nil:
		err = store.SaveWitnessEnvelope(canonical)
	case string(row) != string(canonical):
		logsink.Warn("witness.decision", "the store's copy of the witness envelope is not the envelope beside the ledger: the store's copy is replaced by it")
		err = store.ReplaceWitnessEnvelope(canonical)
	}
	if err != nil {
		logsink.Warn("witness.error", "the store's copy of the witness envelope could not be written (%v); the envelope beside the ledger is used", err)
	}
}

func KeepEnvelopeBeside(guard FileGuard, key IdentityKey, store EnvelopeStore, ledgerDir string) (kept bool, err error) {
	ids, _, _, err := store.WitnessEnrollment()
	if err != nil {
		return false, fmt.Errorf("read the record's witness enrollment: %w", err)
	}
	evidence := "the record"
	if len(ids) == 0 {
		pending, err := readPendingReceipt(ledgerDir)
		if err != nil {
			return false, fmt.Errorf("the receipt pending beside the ledger: %w", err)
		}
		if pending != nil {
			ids, evidence = []string{pending.IdentityID}, "the receipt pending beside the ledger"
		}
	}
	if len(ids) != 1 {
		return false, nil
	}
	if _, err := os.Lstat(EnvelopePath(ledgerDir)); err == nil {
		return false, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("%s: %w", EnvelopeFileName, err)
	}
	row, err := store.LoadWitnessEnvelope()
	if err != nil {
		return false, fmt.Errorf("load witness envelope: %w", err)
	}
	if row == nil {

		return false, nil
	}
	canonical, env, err := parseIdentityEnvelope(row)
	if err != nil {
		return false, fmt.Errorf("%w: the store's envelope is invalid (%v) — nothing is written", ErrEnvelopeRowDisagrees, err)
	}
	if !holdsKey(env, key) {
		return false, fmt.Errorf("%w: the store's envelope holds another key than this identity's — nothing is written", ErrEnvelopeRowDisagrees)
	}
	id, err := DeriveIdentityID(canonical, env)
	if err != nil {
		return false, err
	}
	if id != ids[0] {
		return false, fmt.Errorf("%w: the store's envelope names %s, %s names %s — nothing is written", ErrEnvelopeRowDisagrees, id, evidence, ids[0])
	}
	if err := guard.WitnessFiles(func() error { return writeEnvelopeFile(ledgerDir, key, canonical) }); err != nil {
		return false, fmt.Errorf("keep the witness envelope beside the ledger: %w", err)
	}
	return true, nil
}
