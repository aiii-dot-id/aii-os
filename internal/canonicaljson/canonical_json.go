// Package canonicaljson implements AIII-CANONICAL-JSON-V1 for Go tools.
package canonicaljson

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"unicode/utf8"
)

// ErrCanonicalJSONInvalid is returned, wrapped, for inputs outside
// AIII-CANONICAL-JSON-V1.
var ErrCanonicalJSONInvalid = errors.New("canonical JSON invalid")

type canonicalNumber string

// CanonicalizeV1 returns the AIII-CANONICAL-JSON-V1 encoding of input.
func CanonicalizeV1(input []byte) ([]byte, error) {
	if !utf8.Valid(input) {
		return nil, fmt.Errorf("%w: input is not valid UTF-8", ErrCanonicalJSONInvalid)
	}
	if err := validateJSONUnicodeEscapes(input); err != nil {
		return nil, err
	}

	dec := json.NewDecoder(bytes.NewReader(input))
	dec.UseNumber()

	value, err := parseCanonicalJSONValue(dec)
	if err != nil {
		return nil, err
	}
	if tok, err := dec.Token(); err != io.EOF {
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrCanonicalJSONInvalid, err)
		}
		return nil, fmt.Errorf("%w: extra JSON token after document: %v", ErrCanonicalJSONInvalid, tok)
	}

	var out bytes.Buffer
	if err := emitCanonicalJSONValue(&out, value); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// CanonicalizeV1SHA256 returns sha256:<hex> over the AIII-CANONICAL-JSON-V1 bytes.
func CanonicalizeV1SHA256(input []byte) (string, error) {
	canonical, err := CanonicalizeV1(input)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(canonical)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func parseCanonicalJSONValue(dec *json.Decoder) (interface{}, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCanonicalJSONInvalid, err)
	}

	switch v := tok.(type) {
	case json.Delim:
		switch v {
		case '{':
			obj := map[string]interface{}{}
			for dec.More() {
				keyTok, err := dec.Token()
				if err != nil {
					return nil, fmt.Errorf("%w: %v", ErrCanonicalJSONInvalid, err)
				}
				key, ok := keyTok.(string)
				if !ok {
					return nil, fmt.Errorf("%w: object key is not string: %v", ErrCanonicalJSONInvalid, keyTok)
				}
				if _, exists := obj[key]; exists {
					return nil, fmt.Errorf("%w: duplicate object key %q", ErrCanonicalJSONInvalid, key)
				}
				value, err := parseCanonicalJSONValue(dec)
				if err != nil {
					return nil, err
				}
				obj[key] = value
			}
			end, err := dec.Token()
			if err != nil {
				return nil, fmt.Errorf("%w: %v", ErrCanonicalJSONInvalid, err)
			}
			if end != json.Delim('}') {
				return nil, fmt.Errorf("%w: object closed by %v", ErrCanonicalJSONInvalid, end)
			}
			return obj, nil
		case '[':
			var array []interface{}
			for dec.More() {
				value, err := parseCanonicalJSONValue(dec)
				if err != nil {
					return nil, err
				}
				array = append(array, value)
			}
			end, err := dec.Token()
			if err != nil {
				return nil, fmt.Errorf("%w: %v", ErrCanonicalJSONInvalid, err)
			}
			if end != json.Delim(']') {
				return nil, fmt.Errorf("%w: array closed by %v", ErrCanonicalJSONInvalid, end)
			}
			return array, nil
		default:
			return nil, fmt.Errorf("%w: unexpected delimiter %q", ErrCanonicalJSONInvalid, v)
		}
	case string:
		if strings.ContainsRune(v, '\x00') {
			return nil, fmt.Errorf("%w: NUL code point is outside canonical JSON profile", ErrCanonicalJSONInvalid)
		}
		return v, nil
	case bool:
		return v, nil
	case nil:
		return nil, nil
	case json.Number:
		s := v.String()
		if !canonicalJSONNumber(s) {
			return nil, fmt.Errorf("%w: non-canonical number %q", ErrCanonicalJSONInvalid, s)
		}
		return canonicalNumber(s), nil
	default:
		return nil, fmt.Errorf("%w: unexpected JSON token %T", ErrCanonicalJSONInvalid, tok)
	}
}

func canonicalJSONNumber(s string) bool {
	if s == "" || strings.ContainsAny(s, "eE+") {
		return false
	}
	negative := false
	i := 0
	if s[i] == '-' {
		negative = true
		i++
		if i == len(s) {
			return false
		}
	}

	intStart := i
	if s[i] == '0' {
		i++
		if i < len(s) && isJSONDigit(s[i]) {
			return false
		}
	} else if s[i] >= '1' && s[i] <= '9' {
		for i < len(s) && isJSONDigit(s[i]) {
			i++
		}
	} else {
		return false
	}
	intPart := s[intStart:i]

	fractional := false
	if i < len(s) && s[i] == '.' {
		fractional = true
		i++
		fracStart := i
		if i == len(s) || !isJSONDigit(s[i]) {
			return false
		}
		for i < len(s) && isJSONDigit(s[i]) {
			i++
		}
		fracPart := s[fracStart:i]
		if fracPart[len(fracPart)-1] == '0' {
			return false
		}
	}

	if i != len(s) {
		return false
	}
	if negative && !fractional && intPart == "0" {
		return false
	}
	return true
}

func isJSONDigit(b byte) bool {
	return b >= '0' && b <= '9'
}

func jsonHexValue(b byte) (int, bool) {
	switch {
	case b >= '0' && b <= '9':
		return int(b - '0'), true
	case b >= 'a' && b <= 'f':
		return int(b-'a') + 10, true
	case b >= 'A' && b <= 'F':
		return int(b-'A') + 10, true
	default:
		return 0, false
	}
}

func parseJSONU16(input []byte, offset int) (int, bool) {
	if offset+4 > len(input) {
		return 0, false
	}
	value := 0
	for i := 0; i < 4; i++ {
		nibble, ok := jsonHexValue(input[offset+i])
		if !ok {
			return 0, false
		}
		value = (value << 4) | nibble
	}
	return value, true
}

func validateJSONUnicodeEscapes(input []byte) error {
	inString := false
	escaped := false
	for i := 0; i < len(input); i++ {
		c := input[i]
		if !inString {
			if c == '"' {
				inString = true
			}
			continue
		}
		if escaped {
			escaped = false
			if c != 'u' {
				continue
			}
			cp, ok := parseJSONU16(input, i+1)
			if !ok {
				return fmt.Errorf("%w: invalid JSON unicode escape", ErrCanonicalJSONInvalid)
			}
			if cp >= 0xd800 && cp <= 0xdbff {
				if i+10 >= len(input) || input[i+5] != '\\' || input[i+6] != 'u' {
					return fmt.Errorf("%w: unpaired high surrogate in JSON unicode escape", ErrCanonicalJSONInvalid)
				}
				low, ok := parseJSONU16(input, i+7)
				if !ok || low < 0xdc00 || low > 0xdfff {
					return fmt.Errorf("%w: unpaired high surrogate in JSON unicode escape", ErrCanonicalJSONInvalid)
				}
				i += 10
				continue
			}
			if cp >= 0xdc00 && cp <= 0xdfff {
				return fmt.Errorf("%w: unpaired low surrogate in JSON unicode escape", ErrCanonicalJSONInvalid)
			}
			i += 4
			continue
		}
		switch c {
		case '\\':
			escaped = true
		case '"':
			inString = false
		}
	}
	return nil
}

func emitCanonicalJSONString(out *bytes.Buffer, s string) {
	out.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			out.WriteString("\\\"")
		case '\\':
			out.WriteString("\\\\")
		case '\b':
			out.WriteString("\\b")
		case '\f':
			out.WriteString("\\f")
		case '\n':
			out.WriteString("\\n")
		case '\r':
			out.WriteString("\\r")
		case '\t':
			out.WriteString("\\t")
		default:
			if r < 0x20 {
				fmt.Fprintf(out, "\\u%04x", r)
			} else {
				out.WriteRune(r)
			}
		}
	}
	out.WriteByte('"')
}

func emitCanonicalJSONValue(out *bytes.Buffer, value interface{}) error {
	switch v := value.(type) {
	case map[string]interface{}:
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		out.WriteByte('{')
		for i, key := range keys {
			if i > 0 {
				out.WriteByte(',')
			}
			emitCanonicalJSONString(out, key)
			out.WriteByte(':')
			if err := emitCanonicalJSONValue(out, v[key]); err != nil {
				return err
			}
		}
		out.WriteByte('}')
	case []interface{}:
		out.WriteByte('[')
		for i, item := range v {
			if i > 0 {
				out.WriteByte(',')
			}
			if err := emitCanonicalJSONValue(out, item); err != nil {
				return err
			}
		}
		out.WriteByte(']')
	case string:
		emitCanonicalJSONString(out, v)
	case canonicalNumber:
		out.WriteString(string(v))
	case bool:
		if v {
			out.WriteString("true")
		} else {
			out.WriteString("false")
		}
	case nil:
		out.WriteString("null")
	default:
		return fmt.Errorf("%w: unsupported canonical JSON value %T", ErrCanonicalJSONInvalid, value)
	}
	return nil
}
