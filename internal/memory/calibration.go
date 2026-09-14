package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/memory/actr"
	"github.com/aiii-dot-id/aii-os/internal/memory/carrd"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// .
// .
// .
// .
// .
// .
// .

// .
var Policies = []string{DecayDefault, DecayACTR, DecayNone}

// .
type CalibrationRow struct {
	Store  string
	Bucket string
	Count  int
	Mean   float64
}

// .
const calibrationScan = 5000

// .
// .
func (f *Facility) Calibration(ctx context.Context, policy string, now time.Time) ([]CalibrationRow, error) {
	if policy == "" {
		policy = DecayDefault
	}
	if policy != DecayDefault && policy != DecayNone && policy != DecayACTR {
		return nil, fmt.Errorf("decay policy %q is not known; the policies are %v", policy, Policies)
	}
	if now.IsZero() {
		now = time.Now()
	}
	var out []CalibrationRow
	err := f.st.ReadWith(func(db *sql.DB) error {
		for _, d := range stores {
			if d.Name == "plugin_memories" {
				continue
			}
			sums := map[string]*CalibrationRow{}
			for _, b := range []string{"never", "once", "repeated"} {
				sums[b] = &CalibrationRow{Store: d.Name, Bucket: b}
			}
			q := "SELECT " + selectList(d) + " FROM " + d.Name + " b"
			if d.Filter != "" {
				q += " WHERE " + d.Filter
			}
			q += " ORDER BY b.rowid DESC LIMIT ?"
			rows, err := db.QueryContext(ctx, q, calibrationScan)
			if err != nil {
				return fmt.Errorf("%s: %w", d.Name, err)
			}
			hits, err := scanHits(rows, d)
			rows.Close()
			if err != nil {
				return fmt.Errorf("%s: %w", d.Name, err)
			}
			ids := make([]string, 0, len(hits))
			for _, h := range hits {
				ids = append(ids, h.ID)
			}
			access, err := memoryAccessesOn(db, d.Name, ids)
			if err != nil {
				return fmt.Errorf("%s: access records: %w", d.Name, err)
			}
			for _, h := range hits {
				a := access[h.ID]
				bucket := "never"
				switch {
				case a.Count >= 2:
					bucket = "repeated"
				case a.Count == 1:
					bucket = "once"
				}
				strength := strengthUnder(policy, classOf(d, h), h.Time, a, now)
				r := sums[bucket]
				r.Mean = (r.Mean*float64(r.Count) + strength) / float64(r.Count+1)
				r.Count++
			}
			for _, b := range []string{"never", "once", "repeated"} {
				if sums[b].Count > 0 {
					out = append(out, *sums[b])
				}
			}
		}
		return nil
	})
	return out, err
}

// .
// .
func strengthUnder(policy string, class carrd.Class, at time.Time, a store.MemoryAccess, now time.Time) float64 {
	switch policy {
	case DecayNone:
		return 1
	case DecayACTR:
		return actr.Strength(at, a.History, now)
	default:
		age := 0.0
		if !at.IsZero() {
			age = now.Sub(at).Hours() / 24
		}
		s, _ := carrd.Strength(class, age, a.Count)
		return s
	}
}

// .
// .
func memoryAccessesOn(db *sql.DB, storeName string, ids []string) (map[string]store.MemoryAccess, error) {
	out := map[string]store.MemoryAccess{}
	for start := 0; start < len(ids); start += 200 {
		end := start + 200
		if end > len(ids) {
			end = len(ids)
		}
		batch := ids[start:end]
		marks := ""
		args := []any{storeName}
		for i, id := range batch {
			if i > 0 {
				marks += ","
			}
			marks += "?"
			args = append(args, id)
		}
		rows, err := db.Query(`SELECT id, count, last_at, history FROM memory_access WHERE store = ? AND id IN (`+marks+`)`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var a store.MemoryAccess
			var lastAt, history string
			if err := rows.Scan(&a.ID, &a.Count, &lastAt, &history); err != nil {
				rows.Close()
				return nil, err
			}
			a.Store = storeName
			a.LastAt = parseTime(lastAt)
			var stamps []string
			if err := json.Unmarshal([]byte(history), &stamps); err == nil {
				for _, st := range stamps {
					if t, err := time.Parse(time.RFC3339Nano, st); err == nil {
						a.History = append(a.History, t)
					}
				}
			}
			out[a.ID] = a
		}
		rows.Close()
	}
	return out, nil
}

// .
func RenderCalibration(policy string, rows []CalibrationRow) string {
	out := fmt.Sprintf("policy %s — mean strength by how often the identity recalled a row (rows):\n", policy)
	for _, r := range rows {
		out += fmt.Sprintf("  %-22s %-9s %.3f (%d)\n", r.Store, r.Bucket, r.Mean, r.Count)
	}
	return out
}
