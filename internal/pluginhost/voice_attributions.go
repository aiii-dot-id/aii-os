package pluginhost

import (
	"bytes"
	"encoding/json"
	"fmt"
)

func copyAttributions(rows []json.RawMessage) []json.RawMessage {
	if rows == nil {
		return nil
	}
	out := make([]json.RawMessage, len(rows))
	for i, row := range rows {
		out[i] = bytes.Clone(row)
	}
	return out
}

// .
// .
// .
func (v *VoiceSession) queueReconciledAttributionsLocked(snap Snapshot) error {
	if len(snap.Attributions) > 128 {
		return fmt.Errorf("voicesession: attribution snapshot exceeds 128 rows")
	}
	if len(snap.Attributions) != 0 && v.binding != nil && !v.binding.HasInput() {
		v.markFaultLocked("the engine's status carries speaker observations on an output-only session")
		return fmt.Errorf("voicesession: attribution without input")
	}
	var batch [][]byte
	for _, raw := range snap.Attributions {
		unchanged := false
		for _, prior := range v.lastSnapshot.Attributions {
			if bytes.Equal(raw, prior) {
				unchanged = true
				break
			}
		}
		if unchanged {
			continue
		}
		var row map[string]json.RawMessage
		if err := json.Unmarshal(raw, &row); err != nil || row == nil {
			return fmt.Errorf("voicesession: malformed attribution row")
		}
		var revision uint64
		if err := json.Unmarshal(row["revision"], &revision); err != nil {
			return fmt.Errorf("voicesession: malformed attribution revision")
		}
		if revision == 0 {
			continue
		}
		row["type"] = json.RawMessage(`"speaker_observation"`)
		row["session_id"], _ = json.Marshal(snap.SessionID)
		// .
		// .
		delete(row, "sequence")
		encoded, err := json.Marshal(row)
		if err != nil {
			return err
		}
		batch = append(batch, encoded)
	}
	if len(batch) != 0 {
		select {
		case v.reconciled <- batch:
		default:
			v.markFaultLocked("the reconciled speaker observations could not be queued for the observer")
			return fmt.Errorf("voicesession: attribution observer queue full")
		}
	}
	return nil
}
