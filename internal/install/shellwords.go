package install

import (
	"strings"
)

type Shell int

const (
	POSIX Shell = iota

	PowerShell

	Cmd
)

func (sh Shell) Name() string {
	switch sh {
	case PowerShell:
		return "PowerShell"
	case Cmd:
		return "Command Prompt"
	}
	return "sh, bash or zsh"
}

func (sh Shell) Word(s string) (string, bool) {
	if plainWord(s) {
		return s, true
	}
	switch sh {
	case PowerShell:
		var b strings.Builder
		b.WriteByte('\'')
		for _, r := range s {
			if r == '\'' || r >= '\u2018' && r <= '\u201b' {
				b.WriteRune(r)
			}
			b.WriteRune(r)
		}
		b.WriteByte('\'')
		return b.String(), true
	case Cmd:
		if strings.ContainsAny(s, "%!\"\r\n") {
			return "", false
		}
		trailing := len(s) - len(strings.TrimRight(s, `\`))
		return `"` + s + strings.Repeat(`\`, trailing) + `"`, true
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'", true
}

func (sh Shell) Command(program string, args ...string) (line string, ok bool) {
	words := make([]string, 0, 1+len(args))
	for i, a := range append([]string{program}, args...) {
		w, ok := sh.Word(a)
		if !ok {
			return "", false
		}
		if i == 0 && sh == PowerShell && w != a {
			w = "& " + w
		}
		words = append(words, w)
	}
	return strings.Join(words, " "), true
}

func plainWord(s string) bool {
	return s != "" && strings.IndexFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.')
	}) < 0
}
