package tools

import (
	"path/filepath"
	"strings"
	"unicode"
)

const shellSeparators = "`,;()|<>&"

func shellWords(cmd string) []string {
	escapes := shellDialect == "bash"
	var words []string
	var cur strings.Builder
	end := func() {
		if cur.Len() > 0 {
			words = append(words, cur.String())
			cur.Reset()
		}
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

				if escapes && rs[i] == '\\' && i+1 < len(rs) && strings.ContainsRune("\"\\$`", rs[i+1]) {
					i++
				}
				cur.WriteRune(rs[i])
			}
		case escapes && c == '\\' && i+1 < len(rs):
			i++
			cur.WriteRune(rs[i])
		case unicode.IsSpace(c) || strings.ContainsRune(shellSeparators, c):
			end()
		default:
			cur.WriteRune(c)
		}
	}
	end()
	return words
}

type shellToken struct {
	text string

	cut bool

	name bool

	fragment bool
}

func (r *Registry) shellTokens(cmd string) []shellToken {
	var out []shellToken
	var read func(arg string, fragment bool)
	read = func(arg string, fragment bool) {
		tok := shellToken{text: arg, fragment: fragment}
		pieces := shellWords(arg)
		if fragment || len(pieces) == 0 || len(pieces) == 1 && pieces[0] == arg {
			out = append(out, tok)
			return
		}
		tok.cut = true
		tok.name = !strings.ContainsAny(arg, shellSeparators) &&
			(r.inExtraRoot(arg) || isRooted(arg) && !r.isOutsideSandbox(arg))
		out = append(out, tok)
		for i, p := range pieces {
			read(p, tok.name && (i == 0 || !escapeShaped(p)))
		}
	}
	for _, w := range shellWords(cmd) {
		read(w, false)
	}
	return out
}

func escapeShaped(tok string) bool {
	if isRooted(tok) {
		return true
	}
	clean := filepath.Clean(tok)
	return clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator))
}
