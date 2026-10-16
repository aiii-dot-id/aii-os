package llm

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"strconv"
)

type systemTextsKey struct{}

func withSystemTexts(ctx context.Context, messages []Message) context.Context {
	var texts []string
	for _, m := range messages {
		if (m.Role == "system" || m.Role == "developer") && m.Content != "" {
			texts = append(texts, m.Content)
		}
	}
	if len(texts) == 0 {
		return ctx
	}
	return context.WithValue(ctx, systemTextsKey{}, texts)
}

func systemTextLine(text string) string {
	sum := sha256.Sum256([]byte(text))
	return "[system text: " + strconv.Itoa(len(text)) + " bytes, sha256 " + hex.EncodeToString(sum[:]) + "]"
}

type span struct {
	start, end int
	value      string
	path       []string
}

func withoutSystemText(ctx context.Context, body []byte) []byte {
	sent, _ := ctx.Value(systemTextsKey{}).([]string)
	spans, roles, ok := stringSpans(body)
	if !ok {
		return []byte(systemTextLine(string(body)))
	}
	isSent := func(v string) bool {
		for _, t := range sent {
			if v == t {
				return true
			}
		}
		return false
	}
	var out bytes.Buffer
	at := 0
	for _, s := range spans {
		if !isSystemPath(s.path, roles) && !(s.value != "" && isSent(s.value) && textPosition(s.path)) {
			continue
		}
		line, _ := json.Marshal(systemTextLine(s.value))
		out.Write(body[at:s.start])
		out.Write(line)
		at = s.end
	}
	out.Write(body[at:])
	return out.Bytes()
}

func textPosition(path []string) bool {
	for i := len(path) - 1; i >= 0; i-- {
		if _, err := strconv.Atoi(path[i]); err == nil {
			continue
		}
		switch path[i] {
		case "content", "text", "instructions", "system":
			return true
		}
		return false
	}
	return false
}

func isSystemPath(path []string, roles map[string]string) bool {
	if len(path) == 0 {
		return false
	}
	switch path[0] {
	case "system", "instructions":

		return len(path) == 1 || path[len(path)-1] == "text"
	case "systemInstruction", "system_instruction":
		return path[len(path)-1] == "text"
	case "messages", "input":
		if len(path) < 3 {
			return false
		}
		role := roles[path[0]+"/"+path[1]]
		if role != "system" && role != "developer" {
			return false
		}
		return path[2] == "content" && (len(path) == 3 || path[len(path)-1] == "text")
	}
	return false
}

func stringSpans(body []byte) (spans []span, roles map[string]string, ok bool) {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	roles = map[string]string{}
	type frame struct {
		array bool
		index int
		key   string
		inKey bool
	}
	var stack []*frame
	path := func() []string {
		p := make([]string, 0, len(stack))
		for _, f := range stack {
			if f.array {
				p = append(p, strconv.Itoa(f.index))
			} else {
				p = append(p, f.key)
			}
		}
		return p
	}

	next := func() {
		if len(stack) == 0 {
			return
		}
		f := stack[len(stack)-1]
		if f.array {
			f.index++
		} else {
			f.inKey = true
		}
	}
	for {
		before := int(dec.InputOffset())
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, nil, false
		}
		switch t := tok.(type) {
		case json.Delim:
			switch t {
			case '{':
				stack = append(stack, &frame{inKey: true})
			case '[':
				stack = append(stack, &frame{array: true})
			case '}', ']':
				stack = stack[:len(stack)-1]
				next()
			}
		case string:
			if len(stack) > 0 && !stack[len(stack)-1].array && stack[len(stack)-1].inKey {
				f := stack[len(stack)-1]
				f.key, f.inKey = t, false
				continue
			}
			end := int(dec.InputOffset())
			start := before + bytes.IndexByte(body[before:end], '"')
			p := path()
			spans = append(spans, span{start: start, end: end, value: t, path: p})
			if len(p) == 3 && (p[0] == "messages" || p[0] == "input") && p[2] == "role" {
				roles[p[0]+"/"+p[1]] = t
			}
			next()
		default:
			next()
		}
	}
	return spans, roles, true
}
