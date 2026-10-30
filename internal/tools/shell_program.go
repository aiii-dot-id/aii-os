package tools

import (
	"strings"
	"unicode"
)

func programCode(lang, body string) map[string]bool {
	s := programScan{code: map[string]bool{}, named: map[string]bool{}, allCode: true}
	switch lang {
	case "python":
		s.programLanguage = programLanguage{comment: "#", quotes: `'"`, python: true}
	case "ruby":
		s.programLanguage = programLanguage{comment: "#", quotes: "'\"`", interpolates: "\"`", opener: "#{", ruby: true}
	case "node":
		s.programLanguage = programLanguage{comment: "//", quotes: "'\"`", interpolates: "`", opener: "${", node: true}
	default:
		return nil
	}
	if strings.ContainsAny(body, "\r  \u0085") {
		return nil
	}
	if head := strings.SplitN(body, "\n", 3); strings.Contains(strings.Join(head[:min(2, len(head))], "\n"), "coding") {
		return nil
	}
	s.rs = []rune(body)
	s.codeUntil(false, true)
	s.end()
	if s.fail {
		return nil
	}
	for w := range s.named {
		delete(s.code, w)
	}
	return s.code
}

type programLanguage struct {
	comment      string
	quotes       string
	interpolates string
	opener       string
	python       bool
	ruby         bool
	node         bool
}

type programScan struct {
	programLanguage
	rs          []rune
	i           int
	code, named map[string]bool
	cur         strings.Builder
	allCode     bool
	fail        bool
}

func (s *programScan) at(i int, w string) bool {
	return strings.HasPrefix(string(s.rs[i:min(len(s.rs), i+len(w))]), w)
}

func (s *programScan) end() {
	if s.cur.Len() > 0 {
		if s.allCode {
			s.code[s.cur.String()] = true
		} else {
			s.named[s.cur.String()] = true
		}
	}
	s.cur.Reset()
	s.allCode = true
}

func (s *programScan) unread(i int) bool {
	rs, c := s.rs, s.rs[i]
	next := rune(0)
	if i+1 < len(rs) {
		next = rs[i+1]
	}
	lineStart := i == 0 || rs[i-1] == '\n'
	switch {
	case s.ruby:
		return lineStart && (s.at(i, "=begin") || s.at(i, "__END__")) ||
			c == '/' ||
			c == '%' && next != 0 && !unicode.IsSpace(next) && next != '=' ||
			c == '?' && next != 0 && !unicode.IsSpace(next) && !strings.ContainsRune(").,;:]}=&|!?", next) ||
			c == '$' && strings.ContainsRune("'\"`", next) ||
			s.at(i, "<<") && i+2 < len(rs) && (strings.ContainsRune("~-'\"`_", rs[i+2]) || unicode.IsLetter(rs[i+2]))
	case s.node:
		return s.at(i, "<!--") || s.at(i, "-->") || c == '/' && next != '/' && next != '*'
	}
	return false
}

func (s *programScan) codeUntil(field, nl bool) (spec bool) {
	depth := 0
	for s.i < len(s.rs) && !s.fail {
		c := s.rs[s.i]
		switch {
		case field && depth == 0 && c == '}':
			s.i++
			s.end()
			return false
		case field && depth == 0 && s.python && c == ':':
			s.i++
			s.end()
			return true
		case field && (c == '\\' || c == '\n' && !nl || s.at(s.i, s.comment) || s.node && s.at(s.i, "/*")):
			s.fail = true
		case s.unread(s.i):
			s.fail = true
		case s.at(s.i, s.comment) || s.node && s.i == 0 && s.at(0, "#!"):
			s.end()
			j := s.i
			for j < len(s.rs) && s.rs[j] != '\n' {
				j++
			}
			s.comments(j)
		case s.node && s.at(s.i, "/*"):
			s.end()
			j := s.i + 2
			for j+1 < len(s.rs) && !(s.rs[j] == '*' && s.rs[j+1] == '/') {
				j++
			}
			if j+1 >= len(s.rs) {
				s.fail = true
				break
			}
			s.comments(j + 2)
		case strings.ContainsRune(s.quotes, c):
			w := s.cur.String()
			s.str(strings.ToLower(w[len(strings.TrimRightFunc(w, unicode.IsLetter)):]))
		default:
			switch c {
			case '(', '[', '{':
				depth++
			case ')', ']', '}':
				depth = max(0, depth-1)
			}
			if unicode.IsSpace(c) || strings.ContainsRune(shellSeparators, c) {
				s.end()
			} else {
				s.cur.WriteRune(c)
			}
			s.i++
		}
	}
	if field {
		s.fail = true
	}
	return false
}

func (s *programScan) str(prefix string) {
	q := s.rs[s.i]
	closing := string(q)
	if s.python && s.at(s.i, strings.Repeat(closing, 3)) {
		closing = strings.Repeat(closing, 3)
	}
	interpolating := strings.ContainsRune(s.interpolates, q)
	if s.python {
		interpolating = strings.ContainsAny(prefix, "ft")
	}
	nl := !s.python && !s.node || len(closing) == 3 || q == '`'
	s.i += len(closing)
	var part, whole strings.Builder
	name := func() {
		s.nameAll(part.String())
		whole.WriteString(part.String())
		part.Reset()
	}

	var field func()
	field = func() {
		outer, outerCode := s.cur.String(), s.allCode
		s.cur.Reset()
		s.allCode = true
		spec := s.codeUntil(true, nl)
		for spec && !s.fail {
			switch {
			case s.i >= len(s.rs) || s.at(s.i, closing) || s.rs[s.i] == '\\' || s.rs[s.i] == '\n' && !nl:
				s.fail = true
			case s.rs[s.i] == '}':
				s.i++
				spec = false
			case s.rs[s.i] == '{':
				s.i++
				name()
				field()
			default:
				part.WriteRune(s.rs[s.i])
				s.i++
			}
		}
		s.cur.WriteString(outer)
		s.allCode = outerCode
	}
	for !s.fail {
		switch {
		case s.i >= len(s.rs):
			s.fail = true
		case s.at(s.i, closing):
			s.i += len(closing)
			name()
			s.nameAll(whole.String())
			s.cur.WriteString(whole.String())
			s.allCode = false
			return
		case s.rs[s.i] == '\\':
			part.WriteRune('\\')
			if s.i+1 < len(s.rs) {
				part.WriteRune(s.rs[s.i+1])
			}
			s.i += 2
		case s.rs[s.i] == '\n' && !nl:
			s.fail = true
		case interpolating && s.python && (s.at(s.i, "{{") || s.at(s.i, "}}")):
			part.WriteRune(s.rs[s.i])
			s.i += 2
		case interpolating && s.python && s.rs[s.i] == '{':
			s.i++
			name()
			field()
		case interpolating && !s.python && s.at(s.i, s.opener):
			s.i += len([]rune(s.opener))
			name()
			field()
		default:
			part.WriteRune(s.rs[s.i])
			s.i++
		}
	}
}

func (s *programScan) comments(j int) {
	for _, w := range programPieces(string(s.rs[s.i:j])) {
		s.code[w] = true
	}
	s.i = j
}

func (s *programScan) nameAll(text string) {
	dropped := strings.ReplaceAll(text, `\`, "")
	for _, w := range append(append([]string{text, dropped}, programPieces(text)...), programPieces(dropped)...) {
		s.named[w] = true
	}
}

func programPieces(text string) []string {
	return strings.FieldsFunc(text, func(r rune) bool {
		return unicode.IsSpace(r) || strings.ContainsRune(shellSeparators+`'"\`, r)
	})
}
