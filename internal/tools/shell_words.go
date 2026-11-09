package tools

import (
	"path/filepath"
	"slices"
	"strings"
	"unicode"
)

const shellSeparators = "`,;()|<>&"

type bashVersion int

const (
	bash3 bashVersion = iota

	bash5

	asWritten
)

func shellVersions(cmd string) []bashVersion {
	if shellDialect == "bash" && strings.Contains(cmd, "$'") {
		return []bashVersion{bash3, bash5, asWritten}
	}
	return []bashVersion{bash5}
}

func wordsHolding(cmd, word string) []string {
	vs := shellVersions(cmd)
	for _, v := range vs {
		if words := shellWords(cmd, v); slices.Contains(words, word) {
			return words
		}
	}
	return shellWords(cmd, vs[0])
}

func shellWords(cmd string, v bashVersion) []string {
	var words []string
	for _, w := range shellWordsIn(cmd, v, shellDialect == "bash") {
		words = append(words, w.text)
	}
	return words
}

type shellWord struct {
	text   string
	quoted bool
}

func shellWordsQuoted(cmd, word string) []shellWord {
	escapes := shellDialect == "bash"
	vs := shellVersions(cmd)
	for _, v := range vs {
		ws := shellWordsIn(cmd, v, escapes)
		if slices.ContainsFunc(ws, func(w shellWord) bool { return w.text == word }) {
			return ws
		}
	}
	return shellWordsIn(cmd, vs[0], escapes)
}

func shellWordsQuotedIn(cmd string, escapes bool) []shellWord {
	return shellWordsIn(cmd, bash5, escapes)
}

func shellWordsIn(cmd string, v bashVersion, escapes bool) []shellWord {
	var words []shellWord
	var cur strings.Builder
	quoted := true
	end := func() {
		if cur.Len() > 0 {
			words = append(words, shellWord{text: cur.String(), quoted: quoted})
			cur.Reset()
		}
		quoted = true
	}
	rs := []rune(cmd)
	for i := 0; i < len(rs); i++ {
		c := rs[i]
		switch {
		case c == '\'':
			for i++; i < len(rs) && rs[i] != '\''; i++ {
				cur.WriteRune(rs[i])
			}
		case c == '"':
			for i++; i < len(rs) && rs[i] != '"'; i++ {

				if escapes && rs[i] == '\\' && i+1 < len(rs) && rs[i+1] == '\n' {
					i++
					continue
				}

				if escapes && rs[i] == '\\' && i+1 < len(rs) && strings.ContainsRune("\"\\$`", rs[i+1]) {
					i++
				}
				cur.WriteRune(rs[i])
			}
		case escapes && c == '$' && i+1 < len(rs) && rs[i+1] == '$':

			cur.WriteString("$$")
			quoted = false
			i++
		case escapes && v != asWritten && c == '$' && i+1 < len(rs) && rs[i+1] == '\'':

			j := i + 2
			for j < len(rs) && rs[j] != '\'' {
				if rs[j] == '\\' {
					j++
				}
				j++
			}
			cur.WriteString(ansiC(string(rs[i+2:min(j, len(rs))]), v))
			i = j
		case escapes && v != asWritten && c == '$' && i+1 < len(rs) && rs[i+1] == '"':

		case escapes && c == '\\' && i+1 < len(rs):
			i++

			if rs[i] != '\n' {
				cur.WriteRune(rs[i])
				quoted = false
			}
		case unicode.IsSpace(c) || strings.ContainsRune(shellSeparators, c):
			end()
		default:
			cur.WriteRune(c)
			quoted = false
		}
	}
	end()
	return words
}

func ansiC(s string, v bashVersion) string {
	hex := func(b byte) (byte, bool) {
		switch {
		case b >= '0' && b <= '9':
			return b - '0', true
		case b >= 'a' && b <= 'f':
			return b - 'a' + 10, true
		case b >= 'A' && b <= 'F':
			return b - 'A' + 10, true
		}
		return 0, false
	}
	var w []byte
	for i := 0; i < len(s); {
		c := s[i]
		i++
		if c != '\\' || i == len(s) {
			if c == 0 {
				break
			}
			w = append(w, c)
			continue
		}
		e := s[i]
		i++
		switch e {
		case 'a':
			c = '\a'
		case 'b':
			c = '\b'
		case 'e', 'E':
			c = 0x1b
		case 'f':
			c = '\f'
		case 'n':
			c = '\n'
		case 'r':
			c = '\r'
		case 't':
			c = '\t'
		case 'v':
			c = '\v'
		case '\\', '\'', '"', '?':
			c = e
		case '0', '1', '2', '3', '4', '5', '6', '7':
			c = e - '0'
			for n := 1; n < 3 && i < len(s) && s[i] >= '0' && s[i] <= '7'; n++ {
				c = c*8 + s[i] - '0'
				i++
			}
		case 'x':
			braced := i < len(s) && s[i] == '{'
			if braced {
				i++
			}
			n := 0
			for c = 0; i < len(s) && (braced || n < 2); n++ {
				d, ok := hex(s[i])
				if !ok {
					break
				}
				c = c*16 + d
				i++
			}
			switch {
			case braced && i < len(s) && s[i] == '}':
				i++
			case !braced && n == 0:
				w, c = append(w, '\\'), 'x'
			}
		case 'u', 'U':
			if v == bash3 {
				w, c = append(w, '\\'), e
				break
			}
			most := 4
			if e == 'U' {
				most = 8
			}
			var r uint32
			n := 0
			for ; n < most && i < len(s); n++ {
				d, ok := hex(s[i])
				if !ok {
					break
				}
				r = r*16 + uint32(d)
				i++
			}
			switch {
			case n == 0:
				w, c = append(w, '\\'), e
			case r <= 0x7f:
				c = byte(r)
			default:
				w = bashUTF8(w, r)
				continue
			}
		case 'c':
			if i == len(s) {
				w, c = append(w, '\\'), 'c'
				if v == bash3 {
					c = 0
				}
				break
			}
			x := s[i]
			i++
			if v == bash5 && x == '\\' && i < len(s) && s[i] == '\\' {
				i++
			}
			c = x & 0x1f
			if v == bash5 && x == '?' {
				c = 0x7f
			}
		default:
			w, c = append(w, '\\'), e
		}
		if c == 0 {
			break
		}
		w = append(w, c)
	}
	return string(w)
}

func bashUTF8(w []byte, r uint32) []byte {
	var n int
	var lead byte
	switch {
	case r < 0x800:
		n, lead = 2, 0xc0
	case r < 0x10000:
		n, lead = 3, 0xe0
	case r < 0x200000:
		n, lead = 4, 0xf0
	case r < 0x4000000:
		n, lead = 5, 0xf8
	case r < 0x80000000:
		n, lead = 6, 0xfc
	default:
		return w
	}
	b := make([]byte, n)
	for k := n - 1; k > 0; k-- {
		b[k] = 0x80 | byte(r&0x3f)
		r >>= 6
	}
	b[0] = lead | byte(r)
	return append(w, b...)
}

type shellToken struct {
	text string

	cut bool

	name bool

	fragment bool

	literal bool

	code bool
}

func (r *Registry) shellTokens(base, cmd string) []shellToken {
	var out []shellToken
	seen := map[shellToken]bool{}
	add := func(tok shellToken) {
		if !seen[tok] {
			seen[tok] = true
			out = append(out, tok)
		}
	}
	for _, v := range shellVersions(cmd) {
		text, docs := hereDocs(cmd, v)
		words := shellWords(text, v)
		bare := outsideQuotes(text)
		textOnly := len(words) > 0 && shellTextCommands[filepath.Base(words[0])] && !strings.ContainsAny(bare, ";|&`\n<>") &&
			!strings.Contains(bare, "$(") && !strings.Contains(bare, "${")
		var read func(arg string, fragment, literal bool, code map[string]bool)
		read = func(arg string, fragment, literal bool, code map[string]bool) {
			tok := shellToken{text: arg, fragment: fragment, literal: literal, code: code[arg]}
			pieces := shellWords(arg, v)
			if fragment || len(pieces) == 0 || len(pieces) == 1 && pieces[0] == arg {
				add(tok)
				return
			}
			tok.cut = true
			tok.name = !strings.ContainsAny(arg, shellSeparators) &&
				(r.inExtraRoot(from(base, arg)) || isRooted(arg) && !r.isOutsideSandbox(arg))
			add(tok)
			for i, p := range pieces {

				if textOnly && slashesOnly(p) {
					continue
				}
				read(p, tok.name && (i == 0 || !escapeShaped(p)), literal, code)
			}
		}
		for _, w := range words {
			read(w, false, false, nil)
		}
		for _, d := range docs {
			code := programCode(d.lang, d.body)
			for _, w := range shellWords(d.body, v) {
				read(w, false, d.literal, code)
			}
		}
	}
	return out
}

var shellTextCommands = map[string]bool{"echo": true, "printf": true}

func slashesOnly(w string) bool {
	return w != "" && strings.Trim(w, "/") == ""
}

func outsideQuotes(cmd string) string {
	var b strings.Builder
	in := rune(0)
	for i := 0; i < len(cmd); i++ {
		c := rune(cmd[i])
		switch {
		case in == '\'':
			if c == '\'' {
				in = 0
			}
		case in == '"':
			if c == '\\' && i+1 < len(cmd) {
				i++
			} else if c == '"' {
				in = 0
			} else if c == '$' || c == '`' || c == '(' || c == ')' || c == '{' || c == '}' {
				b.WriteRune(c)
			}
		case c == '\'' || c == '"':
			in = c
		case c == '\\' && i+1 < len(cmd):
			b.WriteByte(cmd[i])
			i++
			b.WriteByte(cmd[i])
		default:
			b.WriteRune(c)
		}
	}
	return b.String()
}

type hereDoc struct {
	body    string
	literal bool
	lang    string
}

var hereDocReaders = map[string]bool{
	"python": true, "python3": true, "pypy3": true,
	"perl": true, "ruby": true, "node": true, "php": true,
}

func hereDocReader(prog string) bool {
	prog = filepath.Base(prog)
	v, versioned := strings.CutPrefix(prog, "python3.")
	return hereDocReaders[prog] || versioned && v != "" && strings.Trim(v, "0123456789") == ""
}

func programLang(words []string) string {
	if len(words) == 0 || !hereDocReader(words[0]) || len(words) > 1 && words[1] != "-" {
		return ""
	}
	switch prog := filepath.Base(words[0]); prog {
	case "ruby", "node":
		return prog
	case "perl", "php":
		return ""
	}
	return "python"
}

func hereDocs(cmd string, v bashVersion) (string, []hereDoc) {
	if shellDialect != "bash" || !strings.Contains(cmd, "<<") {
		return cmd, nil
	}

	type pending struct {
		delim                 string
		strip, literal        bool
		stage, op, opEnd, end int
	}
	var (
		text  strings.Builder
		docs  []hereDoc
		queue []pending

		open  []byte
		stage int
	)
	whole := func() (string, []hereDoc) { return cmd, nil }
	in := func() byte {
		if len(open) == 0 {
			return 0
		}
		return open[len(open)-1]
	}
	for i := 0; i < len(cmd); {
		c, rest := cmd[i], cmd[i:]
		n := 1
		switch {
		case c == '\\':
			n = min(2, len(rest))
		case in() == '"':
			switch {
			case c == '"':
				open = open[:len(open)-1]
			case c == '`':
				open = append(open, '`')
			case strings.HasPrefix(rest, "$(("):
				open, n = append(open, 'a'), 3
			case strings.HasPrefix(rest, "$("):
				open, n = append(open, '('), 2
			case strings.HasPrefix(rest, "${"):
				open, n = append(open, '{'), 2
			}
		case c == '\'':
			end := strings.IndexByte(rest[1:], '\'')
			if end < 0 {
				return whole()
			}
			n = end + 2
		case strings.HasPrefix(rest, "$$"):
			n = 2
		case strings.HasPrefix(rest, "$'"):
			j := 2
			for j < len(rest) && rest[j] != '\'' {
				if rest[j] == '\\' {
					j++
				}
				j++
			}
			if j >= len(rest) {
				return whole()
			}
			n = j + 1
		case c == '"':
			open = append(open, '"')
		case c == '`':
			if in() == '`' {
				open = open[:len(open)-1]
			} else {
				open = append(open, '`')
			}
		case strings.HasPrefix(rest, "$(("):
			open, n = append(open, 'a'), 3
		case strings.HasPrefix(rest, "${"):
			open, n = append(open, '{'), 2
		case c == '}' && in() == '{':
			open = open[:len(open)-1]
		case strings.HasPrefix(rest, "((") && (i == 0 || strings.IndexByte(" \t\n;&|(", cmd[i-1]) >= 0):
			open, n = append(open, 'a'), 2
		case strings.HasPrefix(rest, "))") && in() == 'a':
			open, n = open[:len(open)-1], 2
		case c == '(':
			open = append(open, '(')
		case c == ')':
			if in() != '(' {
				return whole()
			}
			open = open[:len(open)-1]
		case c == '#' && in() != '{' && in() != 'a' && (i == 0 || strings.IndexByte(" \t\n;&|()<>", cmd[i-1]) >= 0):
			n = len(rest)
			if end := strings.IndexByte(rest, '\n'); end >= 0 {
				n = end
			}
		case strings.HasPrefix(rest, "<<<"):
			n = 3
		case strings.HasPrefix(rest, "<<") && in() != '{' && in() != 'a':
			j := 2
			strip := j < len(rest) && rest[j] == '-'
			if strip {
				j++
			}
			for j < len(rest) && (rest[j] == ' ' || rest[j] == '\t') {
				j++
			}
			var delim strings.Builder
			quoted := false
		word:
			for j < len(rest) {
				switch d := rest[j]; {
				case d == '\'' || d == '"':
					end := strings.IndexByte(rest[j+1:], d)
					if end < 0 || strings.ContainsAny(rest[j+1:j+1+end], "\\$`") {
						return whole()
					}
					delim.WriteString(rest[j+1 : j+1+end])
					j, quoted = j+end+2, true
				case d == '\\' && j+1 < len(rest):
					delim.WriteByte(rest[j+1])
					j, quoted = j+2, true
				case d == '$' || d == '`':
					return whole()
				case strings.IndexByte(" \t\n;&|()<>", d) >= 0:
					break word
				default:
					delim.WriteByte(d)
					j++
				}
			}
			if delim.Len() == 0 {
				return whole()
			}
			literal := false
			if quoted && len(open) == 0 {
				words := shellWords(text.String()[stage:], v)
				for len(words) > 0 && isAssignment(words[0]) {
					words = words[1:]
				}
				literal = len(words) > 0 && hereDocReader(words[0])
			}
			queue = append(queue, pending{delim: delim.String(), strip: strip, literal: literal, stage: stage, op: text.Len(), opEnd: text.Len() + j, end: -1})
			n = j
		case c == '\n' && len(queue) > 0:
			i++
			for _, p := range queue {
				if p.end < 0 {
					p.end = text.Len()
				}
				var body strings.Builder
				ended := false
				for i < len(cmd) && !ended {
					line, next := cmd[i:], len(cmd)
					if end := strings.IndexByte(line, '\n'); end >= 0 {
						line, next = line[:end], i+end+1
					}
					if p.strip {
						line = strings.TrimLeft(line, "\t")
					}
					i = next
					if ended = line == p.delim; !ended {
						body.WriteString(line + "\n")
					}
				}
				if !ended {
					return whole()
				}
				lang := ""
				if p.literal {
					t := text.String()
					words := shellWords(t[p.stage:p.op]+" "+t[p.opEnd:p.end], v)
					for len(words) > 0 && isAssignment(words[0]) {
						words = words[1:]
					}
					lang = programLang(words)
				}
				docs = append(docs, hereDoc{body.String(), p.literal, lang})
			}
			queue = nil
			text.WriteByte('\n')
			if len(open) == 0 {
				stage = text.Len()
			}
			continue
		}
		text.WriteString(rest[:n])
		i += n
		if len(open) == 0 && strings.IndexByte(";&|\n", c) >= 0 {
			for k := range queue {
				if queue[k].end < 0 {
					queue[k].end = text.Len()
				}
			}
			stage = text.Len()
		}
	}
	if len(open) > 0 || len(queue) > 0 || len(docs) == 0 {
		return whole()
	}
	return text.String(), docs
}

func isAssignment(word string) bool {
	name, _, ok := strings.Cut(word, "=")
	if !ok || name == "" || name[0] >= '0' && name[0] <= '9' {
		return false
	}
	return strings.Trim(name, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_") == ""
}

func escapeShaped(tok string) bool {
	if isRooted(tok) {
		return true
	}
	clean := filepath.Clean(tok)
	return clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator))
}
