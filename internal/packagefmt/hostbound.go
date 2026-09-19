package packagefmt

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
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
// .
// .
// .

var reHostBound = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

// .
func ValidHostBound(v string) bool { return reHostBound.MatchString(v) }

// .
func CompareHostBounds(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) && i < len(bs); i++ {
		if c := compareDecimal(as[i], bs[i]); c != 0 {
			return c
		}
	}
	return 0
}

func compareDecimal(a, b string) int {
	a, b = strings.TrimLeft(a, "0"), strings.TrimLeft(b, "0")
	if len(a) != len(b) {
		if len(a) < len(b) {
			return -1
		}
		return 1
	}
	return strings.Compare(a, b)
}

// .
// .
func CheckHostWindow(min, maxExclusive string) error {
	for _, b := range [][2]string{{"aiios_min_version", min}, {"aiios_max_exclusive_version", maxExclusive}} {
		if b[1] != "" && !ValidHostBound(b[1]) {
			return fmt.Errorf("%s %q is not an AII OS version (three numbers, major.minor.patch; no prerelease or build metadata)", b[0], b[1])
		}
	}
	if min != "" && maxExclusive != "" && CompareHostBounds(min, maxExclusive) >= 0 {
		return fmt.Errorf("the host window is empty: aiios_min_version %s is not below aiios_max_exclusive_version %s", min, maxExclusive)
	}
	return nil
}

// .
// .
// .
// .
func CheckHostBoundRaw(name string, raw json.RawMessage) error {
	raw = bytes.TrimSpace(raw)
	if bytes.Equal(raw, []byte("null")) {
		return fmt.Errorf("%s is null — omit it to declare no bound", name)
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return fmt.Errorf("%s must be a version string (three numbers, major.minor.patch), not %s", name, describeJSON(raw))
	}
	if s == "" {
		return fmt.Errorf("%s is empty — omit it to declare no bound", name)
	}
	if !ValidHostBound(s) {
		return fmt.Errorf("%s %q is not an AII OS version (three numbers, major.minor.patch; no prerelease or build metadata)", name, s)
	}
	return nil
}

func describeJSON(raw []byte) string {
	if len(raw) == 0 {
		return "nothing"
	}
	switch raw[0] {
	case '{':
		return "an object"
	case '[':
		return "an array"
	case 't', 'f':
		return "a boolean"
	case '"':
		return "a string"
	}
	return "a number"
}
