package ledger

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type PayloadShape []string

func (s PayloadShape) String() string { return strings.Join(s, "\n") }

func (s PayloadShape) Digest() string {
	sum := sha256.Sum256([]byte(s.String()))
	return hex.EncodeToString(sum[:8])
}

func ShapeOf(payload []byte) (PayloadShape, error) {
	dec := json.NewDecoder(bytes.NewReader(payload))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	if _, ok := v.(map[string]any); !ok {
		return nil, fmt.Errorf("a payload is a JSON object")
	}
	lines := map[string]bool{}
	describe("", v, lines)
	out := make(PayloadShape, 0, len(lines))
	for l := range lines {
		out = append(out, l)
	}
	sort.Strings(out)
	return out, nil
}

func describe(path string, v any, lines map[string]bool) {
	switch x := v.(type) {
	case map[string]any:
		if len(x) == 0 && path != "" {
			lines[path+" object"] = true
		}
		for k, e := range x {
			child := k
			if path != "" {
				child = path + "." + k
			}
			describe(child, e, lines)
		}
	case []any:
		if len(x) == 0 {
			lines[path+" array"] = true
		}
		for _, e := range x {
			describe(path+"[]", e, lines)
		}
	case string:
		lines[path+" string"] = true
	case json.Number:
		if strings.ContainsAny(string(x), ".eE") {
			lines[path+" number"] = true
		} else {
			lines[path+" integer"] = true
		}
	case bool:
		lines[path+" bool"] = true
	case nil:
		lines[path+" null"] = true
	}
}
