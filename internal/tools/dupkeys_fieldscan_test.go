package tools

import (
	"bufio"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// TestDupKeysFieldScan measures the detector against real production tool
// calls. It is a measurement instrument, not a guard: it skips unless a log
// is named. A blocking gate's blast radius is total tool loss, so the
// false-positive rate has to be measured on real traffic, not asserted.
func TestDupKeysFieldScan(t *testing.T) {
	path := os.Getenv("AII_FIELD_LOG")
	if path == "" {
		t.Skip("set AII_FIELD_LOG to scan a production log")
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)

	total, flagged, drifted, identical, unknown := 0, 0, 0, 0, 0
	for sc.Scan() {
		line := sc.Text()
		i := strings.Index(line, "Tool call: ")
		if i < 0 {
			continue
		}
		rest := line[i+len("Tool call: "):]
		open := strings.Index(rest, "(")
		if open < 0 {
			continue
		}
		name := rest[:open]
		args := strings.TrimSuffix(rest[open+1:], ")")
		total++

		dups := DuplicateArgKeys(args)
		if len(dups) == 0 {
			continue
		}
		flagged++

		// For the first duplicated key: collect every value it was given,
		// in order. What executes is the LAST one.
		key := dups[0]
		vals := valuesFor(args, key)
		switch {
		case len(vals) < 2:
			unknown++
			t.Logf("UNKNOWN %s %v :: could not recover values", name, dups)
		case vals[0] == vals[len(vals)-1]:
			identical++
			t.Logf("IDENTICAL %s key=%s copies=%d :: %s", name, key, len(vals), trim(vals[0]))
		default:
			drifted++
			t.Logf("DRIFTED %s key=%s copies=%d\n    first: %s\n    last : %s",
				name, key, len(vals), trim(vals[0]), trim(vals[len(vals)-1]))
		}
	}
	t.Logf("SCANNED total=%d flagged=%d drifted=%d identical=%d unknown=%d",
		total, flagged, drifted, identical, unknown)
}

func valuesFor(raw, key string) []string {
	dec := json.NewDecoder(strings.NewReader(raw))
	if _, err := dec.Token(); err != nil {
		return nil
	}
	var out []string
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return out
		}
		k, ok := kt.(string)
		if !ok {
			return out
		}
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return out
		}
		if k == key {
			out = append(out, string(v))
		}
	}
	return out
}

func trim(s string) string {
	if len(s) > 120 {
		return s[:120] + "…"
	}
	return s
}
