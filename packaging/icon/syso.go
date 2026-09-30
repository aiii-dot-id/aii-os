package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
)

const (
	rtIcon      = 3
	rtGroupIcon = 14
	rtVersion   = 16
	langEnUS    = 0x0409
)

type resource struct {
	typ, id, lang uint16
	data          []byte
}

var machines = map[string]struct{ machine, reloc uint16 }{
	"amd64": {0x8664, 0x0003},
	"arm64": {0xAA64, 0x0002},
	"386":   {0x014C, 0x0007},
}

func rsrcSection(res []resource) ([]byte, []uint32) {
	sort.Slice(res, func(i, j int) bool {
		a, b := res[i], res[j]
		if a.typ != b.typ {
			return a.typ < b.typ
		}
		if a.id != b.id {
			return a.id < b.id
		}
		return a.lang < b.lang
	})
	type key struct{ typ, id uint16 }
	var types []uint16
	ids := map[uint16][]uint16{}
	langs := map[key][]int{}
	for i, r := range res {
		if len(ids[r.typ]) == 0 {
			types = append(types, r.typ)
		}
		k := key{r.typ, r.id}
		if len(langs[k]) == 0 {
			ids[r.typ] = append(ids[r.typ], r.id)
		}
		langs[k] = append(langs[k], i)
	}
	dirSize := func(n int) int { return 16 + 8*n }

	off := dirSize(len(types))
	typeDir := map[uint16]int{}
	for _, t := range types {
		typeDir[t] = off
		off += dirSize(len(ids[t]))
	}
	idDir := map[key]int{}
	for _, t := range types {
		for _, id := range ids[t] {
			k := key{t, id}
			idDir[k] = off
			off += dirSize(len(langs[k]))
		}
	}
	entry := make([]int, len(res))
	for i := range res {
		entry[i] = off
		off += 16
	}
	blob := make([]int, len(res))
	for i, r := range res {
		off = (off + 7) &^ 7
		blob[i] = off
		off += len(r.data)
	}
	out := make([]byte, off)
	le := binary.LittleEndian
	dir := func(at, n int) int {
		le.PutUint16(out[at+14:], uint16(n))
		return at + 16
	}
	put := func(at int, id uint32, target uint32) {
		le.PutUint32(out[at:], id)
		le.PutUint32(out[at+4:], target)
	}
	p := dir(0, len(types))
	for _, t := range types {
		put(p, uint32(t), 0x80000000|uint32(typeDir[t]))
		p += 8
	}
	for _, t := range types {
		p := dir(typeDir[t], len(ids[t]))
		for _, id := range ids[t] {
			put(p, uint32(id), 0x80000000|uint32(idDir[key{t, id}]))
			p += 8
		}
		for _, id := range ids[t] {
			k := key{t, id}
			p := dir(idDir[k], len(langs[k]))
			for _, i := range langs[k] {
				put(p, uint32(res[i].lang), uint32(entry[i]))
				p += 8
			}
		}
	}
	var relocs []uint32
	for i, r := range res {
		le.PutUint32(out[entry[i]:], uint32(blob[i]))
		le.PutUint32(out[entry[i]+4:], uint32(len(r.data)))
		relocs = append(relocs, uint32(entry[i]))
		copy(out[blob[i]:], r.data)
	}
	return out, relocs
}

func coff(arch string, section []byte, relocs []uint32) ([]byte, error) {
	m, ok := machines[arch]
	if !ok {
		return nil, fmt.Errorf("no Windows resource object for GOARCH=%s", arch)
	}
	if len(relocs) > 0xFFFF {
		return nil, fmt.Errorf("%d relocations do not fit one section", len(relocs))
	}
	const fileHdr, secHdr = 20, 40
	raw := fileHdr + secHdr
	rel := raw + len(section)
	sym := rel + 10*len(relocs)
	var b bytes.Buffer
	w := func(v any) { _ = binary.Write(&b, binary.LittleEndian, v) }
	w(m.machine)
	w(uint16(1))
	w(uint32(0))
	w(uint32(sym))
	w(uint32(1))
	w(uint16(0))
	w(uint16(0))
	b.WriteString(".rsrc\x00\x00\x00")
	w(uint32(0))
	w(uint32(0))
	w(uint32(len(section)))
	w(uint32(raw))
	w(uint32(rel))
	w(uint32(0))
	w(uint16(len(relocs)))
	w(uint16(0))
	w(uint32(0x40000040))
	b.Write(section)
	for _, r := range relocs {
		w(r)
		w(uint32(0))
		w(m.reloc)
	}
	b.WriteString(".rsrc\x00\x00\x00")
	w(uint32(0))
	w(int16(1))
	w(uint16(0))
	b.WriteByte(3)
	b.WriteByte(0)
	w(uint32(4))
	return b.Bytes(), nil
}

func iconResources(icoFile []byte) ([]resource, error) {
	images, err := parseICO(icoFile)
	if err != nil {
		return nil, err
	}
	var res []resource
	var group bytes.Buffer
	w := func(v any) { _ = binary.Write(&group, binary.LittleEndian, v) }
	w([3]uint16{0, 1, uint16(len(images))})
	for i, im := range images {
		id := uint16(i + 1)
		res = append(res, resource{rtIcon, id, langEnUS, im.data})
		dim := byte(im.size)
		if im.size >= 256 {
			dim = 0
		}
		group.Write([]byte{dim, dim, 0, 0})
		w([2]uint16{1, 32})
		w(uint32(len(im.data)))
		w(id)
	}
	return append(res, resource{rtGroupIcon, 1, langEnUS, group.Bytes()}), nil
}

type versionInfo struct {
	version     string
	file        string
	description string
}

const (
	company   = "AIII - AI Identity Incorporated"
	copyright = "Copyright 2026 AIII - AI Identity Incorporated"
	product   = "AII OS"
)

func (v versionInfo) resource() resource {
	nums := versionNumbers(v.version)
	var fixed bytes.Buffer
	w := func(x any) { _ = binary.Write(&fixed, binary.LittleEndian, x) }
	w(uint32(0xFEEF04BD))
	w(uint32(0x00010000))
	ms, ls := nums[0]<<16|nums[1], nums[2]<<16|nums[3]
	w([4]uint32{ms, ls, ms, ls})
	w(uint32(0x3F))
	w(uint32(0))
	w(uint32(0x00040004))
	w(uint32(1))
	w([3]uint32{})
	internal := strings.TrimSuffix(v.file, ".exe")
	strs := []verNode{
		str("CompanyName", company),
		str("FileDescription", v.description),
		str("FileVersion", v.version),
		str("InternalName", internal),
		str("LegalCopyright", copyright),
		str("OriginalFilename", v.file),
		str("ProductName", product),
		str("ProductVersion", v.version),
	}
	root := verNode{key: "VS_VERSION_INFO", value: fixed.Bytes(), valueLen: uint16(fixed.Len()), children: []verNode{
		{key: "StringFileInfo", text: true, children: []verNode{
			{key: "040904B0", text: true, children: strs},
		}},
		{key: "VarFileInfo", text: true, children: []verNode{
			{key: "Translation", value: []byte{0x09, 0x04, 0xB0, 0x04}, valueLen: 4},
		}},
	}}
	return resource{rtVersion, 1, langEnUS, root.bytes()}
}

func versionNumbers(v string) [4]uint32 {
	var out [4]uint32
	v, _, _ = strings.Cut(v, "-")
	v, _, _ = strings.Cut(v, "+")
	for i, part := range strings.SplitN(v, ".", 4) {
		n, err := strconv.ParseUint(part, 10, 16)
		if err == nil {
			out[i] = uint32(n)
		}
	}
	return out
}

type verNode struct {
	key      string
	value    []byte
	valueLen uint16
	text     bool
	children []verNode
}

func str(key, value string) verNode {
	u := append(utf16.Encode([]rune(value)), 0)
	var b bytes.Buffer
	_ = binary.Write(&b, binary.LittleEndian, u)
	return verNode{key: key, value: b.Bytes(), valueLen: uint16(len(u)), text: true}
}

func (n verNode) bytes() []byte {
	var b bytes.Buffer
	w := func(x any) { _ = binary.Write(&b, binary.LittleEndian, x) }
	pad := func() {
		for b.Len()%4 != 0 {
			b.WriteByte(0)
		}
	}
	w(uint16(0))
	w(n.valueLen)
	if n.text {
		w(uint16(1))
	} else {
		w(uint16(0))
	}
	w(append(utf16.Encode([]rune(n.key)), 0))
	pad()
	b.Write(n.value)
	for _, c := range n.children {
		pad()
		b.Write(c.bytes())
	}
	out := b.Bytes()
	binary.LittleEndian.PutUint16(out, uint16(len(out)))
	return out
}

func syso(arch string, icoFile []byte, v versionInfo) ([]byte, error) {
	res, err := iconResources(icoFile)
	if err != nil {
		return nil, err
	}
	res = append(res, v.resource())
	section, relocs := rsrcSection(res)
	return coff(arch, section, relocs)
}
