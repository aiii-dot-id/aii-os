package ledger

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// .
// .
// .
func TestDrill(t *testing.T) {
	n, _ := strconv.Atoi(os.Getenv("AII_LEDGER_DRILL"))
	if n == 0 {
		t.Skip("set AII_LEDGER_DRILL=<records>")
	}
	const cadence = 100
	dir := t.TempDir()
	path := filepath.Join(dir, "ledger.jsonl")
	kp := testKeyPair(t)
	l, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	// .
	filler := strings.Repeat("the day's observation, kept as it was said ", 14)
	start := time.Now()
	var sealTime time.Duration
	for i := 1; i <= n; i++ {
		if i%cadence == 0 {
			if _, err := l.Append(EventSystemWitnessed, kp.Fingerprint(), 0, map[string]interface{}{
				"receipt": map[string]interface{}{
					"identity_id": "did:aiii:identity:sha256:drill", "previous_witnessed_ledger_ordinal": i - cadence - 1,
					"previous_witnessed_ledger_hash": strings.Repeat("0", 64), "ledger_ordinal": i - 1, "ledger_hash": l.LastHash(),
					"witnessed_at": "2026-09-03T12:00:00Z", "witness_key_id": "aiii_witness_drill", "witness_sig_b64": strings.Repeat("A", 6172),
				},
			}, kp); err != nil {
				t.Fatal(err)
			}
			s := time.Now()
			if err := l.Seal(uint64(i)); err != nil {
				t.Fatalf("seal at %d: %v", i, err)
			}
			sealTime += time.Since(s)
			continue
		}
		if _, err := l.Append(EventExperienceCreate, kp.Fingerprint(), 3, map[string]interface{}{
			"id": "exp_" + strconv.Itoa(i), "content": filler, "category": "observation", "provenance": "self",
		}, kp); err != nil {
			t.Fatal(err)
		}
	}
	wrote := time.Since(start)
	l.Close()

	var segBytes, tailBytes int64
	segs := 0
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		info, _ := e.Info()
		if IsSegmentName(e.Name()) {
			segBytes += info.Size()
			segs++
		} else if e.Name() == "ledger.jsonl" {
			tailBytes = info.Size()
		}
	}
	s := time.Now()
	l2, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	openTime := time.Since(s)
	l2.Close()
	s = time.Now()
	heads := &acceptHeads{}
	got, err := VerifyChain(path, kp.PublicKeyBytes(), heads)
	if err != nil || got != n {
		t.Fatalf("verify: n=%d err=%v", got, err)
	}
	verifyTime := time.Since(s)
	t.Logf("DRILL records=%d segments=%d sealed_bytes=%d tail_bytes=%d bytes_per_record=%.0f write=%s (seal %s) open=%s verify=%s heads=%d",
		n, segs, segBytes, tailBytes, float64(segBytes+tailBytes)/float64(n), wrote.Round(time.Millisecond), sealTime.Round(time.Millisecond), openTime.Round(time.Millisecond), verifyTime.Round(time.Millisecond), heads.n)
}
