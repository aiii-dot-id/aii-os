package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"

	"github.com/aiii-dot-id/aii-os/internal/atomicfile"
)

type jsonDoc struct {
	keys    []string
	members map[string]*jsonDoc
	raw     json.RawMessage
}

func parseJSONDoc(data []byte) (*jsonDoc, error) {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	dec := json.NewDecoder(bytes.NewReader(data))
	var v json.RawMessage
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(data[dec.InputOffset():])) != 0 {
		return nil, fmt.Errorf("trailing data")
	}
	return docOf(v)
}

func docOf(v json.RawMessage) (*jsonDoc, error) {
	v = bytes.TrimSpace(v)
	if len(v) == 0 || v[0] != '{' {
		var c bytes.Buffer
		if err := json.Compact(&c, v); err != nil {
			return nil, err
		}
		return &jsonDoc{raw: c.Bytes()}, nil
	}
	dec := json.NewDecoder(bytes.NewReader(v))
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	d := &jsonDoc{members: map[string]*jsonDoc{}}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, _ := tok.(string)
		var member json.RawMessage
		if err := dec.Decode(&member); err != nil {
			return nil, err
		}
		m, err := docOf(member)
		if err != nil {
			return nil, err
		}
		d.set(key, m)
	}
	return d, nil
}

func newObjectDoc() *jsonDoc { return &jsonDoc{members: map[string]*jsonDoc{}} }

func (d *jsonDoc) isObject() bool { return d != nil && d.members != nil }

func (d *jsonDoc) member(key string) *jsonDoc {
	if !d.isObject() {
		return nil
	}
	return d.members[key]
}

func (d *jsonDoc) set(key string, m *jsonDoc) {
	if _, ok := d.members[key]; !ok {
		d.keys = append(d.keys, key)
	}
	d.members[key] = m
}

func (d *jsonDoc) remove(key string) {
	if _, ok := d.members[key]; !ok {
		return
	}
	delete(d.members, key)
	d.keys = slices.DeleteFunc(d.keys, func(k string) bool { return k == key })
}

func (d *jsonDoc) compact() []byte {
	if !d.isObject() {
		return d.raw
	}
	var b bytes.Buffer
	b.WriteByte('{')
	for i, k := range d.keys {
		if i > 0 {
			b.WriteByte(',')
		}
		kb, _ := json.Marshal(k)
		b.Write(kb)
		b.WriteByte(':')
		b.Write(d.members[k].compact())
	}
	b.WriteByte('}')
	return b.Bytes()
}

func (d *jsonDoc) encode() []byte {
	var b bytes.Buffer
	d.write(&b, "")
	return b.Bytes()
}

func (d *jsonDoc) write(b *bytes.Buffer, indent string) {
	if !d.isObject() {
		if err := json.Indent(b, d.raw, indent, "  "); err != nil {
			b.Write(d.raw)
		}
		return
	}
	if len(d.keys) == 0 {
		b.WriteString("{}")
		return
	}
	b.WriteString("{\n")
	for i, k := range d.keys {
		b.WriteString(indent + "  ")
		kb, _ := json.Marshal(k)
		b.Write(kb)
		b.WriteString(": ")
		d.members[k].write(b, indent+"  ")
		if i < len(d.keys)-1 {
			b.WriteByte(',')
		}
		b.WriteByte('\n')
	}
	b.WriteString(indent + "}")
}

func sameValue(a, b *jsonDoc) bool {
	if a == nil || b == nil {
		return a == b
	}
	return bytes.Equal(canonicalJSON(a.compact()), canonicalJSON(b.compact()))
}

func canonicalJSON(raw []byte) []byte {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if dec.Decode(&v) != nil {
		return raw
	}
	out, err := json.Marshal(v)
	if err != nil {
		return raw
	}
	return out
}

var keyedArrays = map[string]string{"providers": "name"}

func writeChanges(file, base, next *jsonDoc) {
	writeObjectChanges(file, base, next, true)
}

func writeObjectChanges(file, base, next *jsonDoc, top bool) {
	if !base.isObject() {
		base = newObjectDoc()
	}
	for _, k := range next.keys {
		n, b, f := next.members[k], base.member(k), file.member(k)
		if b != nil && sameValue(b, n) {
			continue
		}
		if n.isObject() {
			created := !f.isObject()
			if created {
				f = newObjectDoc()
				file.set(k, f)
			}
			writeObjectChanges(f, b, n, false)
			if created && len(f.keys) == 0 && len(n.keys) != 0 {
				file.remove(k)
			}
			continue
		}
		if field, ok := keyedArrays[k]; ok && top {
			if merged, ok := writeKeyedChanges(f, b, n, field); ok {
				file.set(k, merged)
				continue
			}
		}
		file.set(k, n)
	}
	for _, k := range base.keys {
		if next.member(k) == nil {
			file.remove(k)
		}
	}
}

func writeKeyedChanges(file, base, next *jsonDoc, field string) (*jsonDoc, bool) {
	fileEls, ok1 := keyedElements(file, field)
	baseEls, ok2 := keyedElements(base, field)
	nextEls, ok3 := keyedElements(next, field)
	if !ok1 || !ok2 || !ok3 {
		return nil, false
	}
	var out bytes.Buffer
	out.WriteByte('[')
	for i, key := range nextEls.order {
		n := nextEls.byKey[key]
		el := n
		if f, b := fileEls.byKey[key], baseEls.byKey[key]; f.isObject() && b.isObject() && n.isObject() {
			writeObjectChanges(f, b, n, false)
			el = f
		}
		if i > 0 {
			out.WriteByte(',')
		}
		out.Write(el.compact())
	}
	out.WriteByte(']')
	return &jsonDoc{raw: out.Bytes()}, true
}

type keyedList struct {
	order []string
	byKey map[string]*jsonDoc
}

func keyedElements(d *jsonDoc, field string) (keyedList, bool) {
	l := keyedList{byKey: map[string]*jsonDoc{}}
	if d == nil {
		return l, true
	}
	var raws []json.RawMessage
	if json.Unmarshal(d.raw, &raws) != nil {
		return l, false
	}
	for _, raw := range raws {
		el, err := docOf(raw)
		if err != nil || !el.isObject() {
			return l, false
		}
		var key string
		if k := el.member(field); k == nil || json.Unmarshal(k.raw, &key) != nil || key == "" {
			return l, false
		}
		if _, dup := l.byKey[key]; dup {
			return l, false
		}
		l.order = append(l.order, key)
		l.byKey[key] = el
	}
	return l, true
}

func fileWithChanges(path string, base, next []byte, statedBy func([]byte) ([]byte, error), tidy ...func(file, next *jsonDoc)) ([]byte, error) {
	file := newObjectDoc()
	current, err := atomicfile.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		current = nil
	case err != nil:
		return nil, fmt.Errorf("read %s before writing it: %w", path, err)
	default:
		if file, err = parseJSONDoc(current); err == nil && !file.isObject() {
			err = errors.New("it is not a JSON object")
		}
		if err != nil {
			return nil, &unreadableFileError{path: path, err: err}
		}
	}
	untouched := file.compact()
	b := newObjectDoc()
	if base != nil {
		if b, err = parseJSONDoc(base); err != nil {
			return nil, err
		}
	}
	n, err := parseJSONDoc(next)
	if err != nil {
		return nil, err
	}
	writeChanges(file, b, n)
	for _, t := range tidy {
		t(file, n)
	}
	if current != nil && statedBy != nil {
		if raw, err := statedBy(current); err == nil {
			if stated, err := parseJSONDoc(raw); err == nil {
				dropUnstated(file, stated, n, true)
			}
		}
	}
	if current != nil && bytes.Equal(file.compact(), untouched) {
		return current, nil
	}
	return file.encode(), nil
}

func dropUnstated(file, stated, next *jsonDoc, top bool) {
	for _, k := range slices.Clone(file.keys) {
		f, s, n := file.members[k], stated.member(k), next.member(k)
		switch {
		case s == nil && n == nil:
			file.remove(k)
		case f.isObject() && (s == nil || s.isObject()) && (n == nil || n.isObject()):
			dropUnstated(f, orEmpty(s), orEmpty(n), false)
		case top && keyedArrays[k] != "":
			field := keyedArrays[k]
			fl, ok1 := keyedElements(f, field)
			sl, ok2 := keyedElements(s, field)
			nl, ok3 := keyedElements(n, field)
			if !ok1 || !ok2 || !ok3 {
				continue
			}
			els := make([]json.RawMessage, 0, len(fl.order))
			for _, key := range fl.order {
				el := fl.byKey[key]
				if se, ne := sl.byKey[key], nl.byKey[key]; el.isObject() && (se.isObject() || ne.isObject()) {
					dropUnstated(el, orEmpty(se), orEmpty(ne), false)
				}
				els = append(els, el.compact())
			}
			raw, _ := json.Marshal(els)
			if !bytes.Equal(canonicalJSON(raw), canonicalJSON(f.raw)) {
				file.set(k, &jsonDoc{raw: raw})
			}
		}
	}
}

func orEmpty(d *jsonDoc) *jsonDoc {
	if d.isObject() {
		return d
	}
	return newObjectDoc()
}

type unreadableFileError struct {
	path string
	err  error
}

func (e *unreadableFileError) Error() string {
	return fmt.Sprintf("%s does not read as it stands (%v), so nothing was saved: correct it, or move it aside, and save again", filepath.Base(e.path), e.err)
}

func (e *unreadableFileError) Unwrap() error { return e.err }
