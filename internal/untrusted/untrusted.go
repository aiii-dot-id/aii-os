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

func Scrub(s string) string { return scrub(s) }

func Contains(s string) bool {
	return strings.Contains(s, Open) || strings.Contains(s, Close)
}

func Cut(s string, chars int, mark string) string {
	if utf8.RuneCountInString(s) <= chars {
		return s
	}
	head, _ := Head(s, chars-utf8.RuneCountInString(mark))
	return head + mark
}

func Head(text string, most int) (string, int) {
	if most < 0 {
		most = 0
	}
	if n := utf8.RuneCountInString(text); n <= most {
		return text, n
	}

	end, n := 0, 0
	for end = range text {
		if n == most {
			break
		}
		n++
	}
	head := text[:end]
	if !Contains(text) {
		return head, most
	}
	for _, mark := range []string{Open, Close} {

		reach := min(len(text), len(head)+len(mark)-1)
		if at := strings.LastIndex(text[:reach], mark); at >= 0 && at < len(head) && at+len(mark) > len(head) {
			head = text[:at]
		}
	}
	shown := utf8.RuneCountInString(head)
	if strings.LastIndex(head, Open) > strings.LastIndex(head, Close) {
		head += " " + Close
	}
	return head, shown
}
