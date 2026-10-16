package untrusted

import (
	"strings"
	"unicode/utf8"
)

const (
	Open  = "[[[EXTERNAL_UNTRUSTED_CONTENT]]]"
	Close = "[[[END_EXTERNAL_UNTRUSTED_CONTENT]]]"

	forgedNotice = "(forged sentinel removed)"
)

func Wrap(source, content string) string {
	head := Open
	if s := scrub(source); s != "" {
		head = Open + " source: " + s
	}
	return head + "\n" + scrub(content) + "\n" + Close
}

func scrub(s string) string {
	s = strings.ReplaceAll(s, Close, forgedNotice)
	s = strings.ReplaceAll(s, Open, forgedNotice)
	return s
}

func Contains(s string) bool {
	return strings.Contains(s, Open) || strings.Contains(s, Close)
}

func Cut(s string, chars int, mark string) string {
	r := []rune(s)
	if len(r) <= chars {
		return s
	}
	head := string(r[:max(0, chars-utf8.RuneCountInString(mark))])
	for _, m := range []string{Open, Close} {
		for n := len(m) - 1; n >= len("[[["); n-- {
			if strings.HasSuffix(head, m[:n]) {
				head = head[:len(head)-n]
				break
			}
		}
	}
	if strings.LastIndex(head, Open) > strings.LastIndex(head, Close) {
		return strings.TrimRight(head, " \n") + " " + Close + mark
	}
	return head + mark
}
