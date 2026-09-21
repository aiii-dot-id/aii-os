package logsink

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
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

const (
	// .
	// .
	// .
	// .
	// .
	CaptureDirName = "llm"

	// .
	captureRoll = 64 << 20

	// .
	// .
	// .
	CaptureDays = 7

	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	CaptureMax = 8 << 20
	captureMax = CaptureMax
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
type TapRecord struct {
	ID        string
	Turn      string
	Category  string
	Direction string
	Model     string
	Tokens    int
	Payload   slog.LogValuer

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
	Continues bool
}

// .
// .
type captureLine struct {
	ID        string `json:"id"`
	TS        string `json:"ts"`
	Turn      string `json:"turn,omitempty"`
	Category  string `json:"category"`
	Direction string `json:"direction"`
	Model     string `json:"model,omitempty"`
	Tokens    int    `json:"tokens,omitempty"`
	Payload   string `json:"payload"`
}

var (
	// .
	// .
	// .
	taps atomic.Pointer[map[string]time.Time]

	tapsMu sync.Mutex

	captureMu  sync.Mutex
	captureDir string
	captureF   *os.File
	captureN   int64
	captureDay string
)

// .
// .
// .
func SetCaptureDir(dir string) {
	captureMu.Lock()
	defer captureMu.Unlock()
	if dir == captureDir {
		return
	}
	closeCaptureLocked()
	captureDir = dir
}

// .
// .
// .
// .
// .
// .
// .
func TapEnabled(category string) bool {
	m := taps.Load()
	if m == nil || len(*m) == 0 {
		return false
	}
	until, ok := (*m)[category]
	if !ok {
		return false
	}
	if !until.IsZero() && !time.Now().Before(until) {
		clearExpired(category)
		return false
	}
	return true
}

// .
func clearExpired(category string) {
	tapsMu.Lock()
	defer tapsMu.Unlock()
	cur := taps.Load()
	if cur == nil {
		return
	}
	until, ok := (*cur)[category]
	if !ok || until.IsZero() || time.Now().Before(until) {
		return
	}
	next := make(map[string]time.Time, len(*cur))
	for k, v := range *cur {
		if k != category {
			next[k] = v
		}
	}
	taps.Store(&next)
	Info("logs.decision", "tap %s stopped recording: the expiry it was given (%s) has passed",
		category, until.UTC().Format(time.RFC3339))
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
func SetTaps(next map[string]time.Time) {
	tapsMu.Lock()
	defer tapsMu.Unlock()

	prev := map[string]time.Time{}
	if cur := taps.Load(); cur != nil {
		prev = *cur
	}
	now := time.Now()
	live := make(map[string]time.Time, len(next))
	for cat, until := range next {
		if !until.IsZero() && !now.Before(until) {
			if _, was := prev[cat]; was {
				Info("logs.decision", "tap %s stopped recording: the expiry it was given (%s) has passed",
					cat, until.UTC().Format(time.RFC3339))
			} else {
				Info("logs.decision", "tap %s was asked for but its expiry (%s) has already passed — not recording",
					cat, until.UTC().Format(time.RFC3339))
			}
			continue
		}
		live[cat] = until
	}

	for cat, until := range live {
		was, existed := prev[cat]
		if existed && was.Equal(until) {
			continue
		}
		if until.IsZero() {
			Info("logs.decision", "tap %s is recording payloads to %s, with no expiry", cat, captureWhere())
		} else {
			Info("logs.decision", "tap %s is recording payloads to %s until %s", cat, captureWhere(),
				until.UTC().Format(time.RFC3339))
		}
	}
	for cat := range prev {
		if _, still := live[cat]; !still {
			if _, expired := next[cat]; expired {
				continue
			}
			Info("logs.decision", "tap %s stopped recording", cat)
		}
	}
	taps.Store(&live)
}

// .
// .
func Taps() map[string]time.Time {
	out := map[string]time.Time{}
	if cur := taps.Load(); cur != nil {
		for k, v := range *cur {
			out[k] = v
		}
	}
	return out
}

func captureWhere() string {
	captureMu.Lock()
	defer captureMu.Unlock()
	if captureDir == "" {
		return "nowhere — no capture directory is set"
	}
	return captureDir
}

// .
// .
// .
// .
// .
// .
// .
// .
func Tap(c TapRecord) {
	if !c.Continues && !TapEnabled(c.Category) {
		return
	}
	payload := ""
	if c.Payload != nil {
		payload = valueText(c.Payload.LogValue())
	}
	if len(payload) > captureMax {
		payload = payload[:captureMax] + fmt.Sprintf("\n…[capture truncated at %d bytes: the rest arrived but is not kept]", captureMax)
	}
	line := captureLine{
		ID:        c.ID,
		TS:        time.Now().UTC().Format(time.RFC3339Nano),
		Turn:      c.Turn,
		Category:  c.Category,
		Direction: c.Direction,
		Model:     c.Model,
		Tokens:    c.Tokens,
		Payload:   payload,
	}
	raw, err := json.Marshal(line)
	if err != nil {
		Error("logs.error", "a capture for %s could not be encoded: %v", c.Category, err)
		return
	}
	if err := writeCapture(append(raw, '\n')); err != nil {
		Error("logs.error", "a capture for %s could not be written: %v", c.Category, err)
	}
}

// .
// .
func valueText(v slog.Value) string {
	v = v.Resolve()
	if v.Kind() == slog.KindString {
		return v.String()
	}
	return fmt.Sprint(v.Any())
}

// .
// .
func writeCapture(line []byte) error {
	captureMu.Lock()
	defer captureMu.Unlock()
	if captureDir == "" {
		return fmt.Errorf("no capture directory is set")
	}
	day := time.Now().UTC().Format("20060102")
	if captureF != nil && (day != captureDay || captureN+int64(len(line)) > captureRoll) {
		closeCaptureLocked()
	}
	if captureF == nil {
		if err := os.MkdirAll(captureDir, 0o700); err != nil {
			return err
		}
		name, err := nextCaptureName(captureDir, day)
		if err != nil {
			return err
		}
		// .
		// .
		f, err := os.OpenFile(filepath.Join(captureDir, name), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return err
		}
		st, err := f.Stat()
		if err != nil {
			f.Close()
			return err
		}
		captureF, captureN, captureDay = f, st.Size(), day
		pruneCapturesLocked()
	}
	n, err := captureF.Write(line)
	captureN += int64(n)
	return err
}

// .
// .
func nextCaptureName(dir, day string) (string, error) {
	base := day + ".jsonl"
	st, err := os.Stat(filepath.Join(dir, base))
	if os.IsNotExist(err) {
		return base, nil
	}
	if err != nil {
		return "", err
	}
	if st.Size() < captureRoll {
		return base, nil
	}
	for n := 1; ; n++ {
		cand := fmt.Sprintf("%s.%d.jsonl", day, n)
		st, err := os.Stat(filepath.Join(dir, cand))
		if os.IsNotExist(err) {
			return cand, nil
		}
		if err != nil {
			return "", err
		}
		if st.Size() < captureRoll {
			return cand, nil
		}
	}
}

// .
// .
func pruneCapturesLocked() {
	entries, err := os.ReadDir(captureDir)
	if err != nil {
		return
	}
	floor := time.Now().UTC().AddDate(0, 0, -CaptureDays)
	var gone []string
	for _, e := range entries {
		n := e.Name()
		if !strings.HasSuffix(n, ".jsonl") {
			continue
		}
		day, _, _ := strings.Cut(n, ".")
		at, err := time.Parse("20060102", day)
		if err != nil || !at.Before(floor) {
			continue
		}
		if os.Remove(filepath.Join(captureDir, n)) == nil {
			gone = append(gone, n)
		}
	}
	if len(gone) > 0 {
		sort.Strings(gone)
		Info("logs.end", "captures older than %d days removed: %s", CaptureDays, strings.Join(gone, ", "))
	}
}

func closeCaptureLocked() {
	if captureF != nil {
		captureF.Close()
		captureF, captureN, captureDay = nil, 0, ""
	}
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
func PruneCaptures() {
	captureMu.Lock()
	defer captureMu.Unlock()
	if captureDir == "" {
		return
	}
	pruneCapturesLocked()
}

// .
// .
func CloseCaptures() {
	captureMu.Lock()
	defer captureMu.Unlock()
	closeCaptureLocked()
}
