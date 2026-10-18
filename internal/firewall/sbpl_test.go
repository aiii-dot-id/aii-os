package firewall

import (
	"strconv"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"
)

// Seatbelt reads \" \\ \t \n \r \xHH and raw bytes, and not \a \b \f \v
// \uXXXX \UXXXXXXXX (MACOS-TRIAL rows 11a, 11b). The writer escapes the two
// it must and writes every other byte as it stands.
func TestSBPLStringEscapesOnlyTheBackslashAndTheQuote(t *testing.T) {
	for _, tc := range []struct{ what, in, want string }{
		{"plain", "/Users/a/.aii/identity-0/data", `"/Users/a/.aii/identity-0/data"`},
		{"space", "/Users/a/Work Files/x", `"/Users/a/Work Files/x"`},
		{"double quote", `/x/q"uote`, `"/x/q\"uote"`},
		{"backslash", `/x/back\slash`, `"/x/back\\slash"`},
		{"both, adjacent", `/x/\"`, `"/x/\\\""`},
		{"TAB", "/x/tab\tx", "\"/x/tab\tx\""},
		{"LF", "/x/nl\nx", "\"/x/nl\nx\""},
		{"CR", "/x/cr\rx", "\"/x/cr\rx\""},
		{"BEL", "/x/bell\ax", "\"/x/bell\ax\""},
		{"BS FF VT", "/x/\b\f\v", "\"/x/\b\f\v\""},
		{"0x01 ESC DEL", "/x/\x01\x1b\x7f", "\"/x/\x01\x1b\x7f\""},
		{"U+200B", "/x/zw\u200bx", "\"/x/zw\xe2\x80\x8bx\""},
		{"U+E000", "/x/pua\ue000x", "\"/x/pua\xee\x80\x80x\""},
		{"U+F0000", "/x/plane15\U000f0000x", "\"/x/plane15\xf3\xb0\x80\x80x\""},
		{"accented", "/x/réserve", "\"/x/r\xc3\xa9serve\""},
		{"empty", "", `""`},
	} {
		got, err := SBPLString(tc.in)
		if err != nil || got != tc.want {
			t.Errorf("%s: SBPLString(%q) = %q, %v; want %q", tc.what, tc.in, got, err, tc.want)
		}
		for _, escape := range []string{`\a`, `\b`, `\f`, `\v`, `\u`, `\U`, `\t`, `\n`, `\r`, `\x`} {
			if strings.Contains(got, escape) {
				t.Errorf("%s: %q holds %s, which the input did not spell", tc.what, got, escape)
			}
		}
	}
}

// A name the writer cannot spell is refused, never written some other way.
func TestSBPLStringRefusesWhatItCannotSpell(t *testing.T) {
	for _, tc := range []struct{ what, in string }{
		{"invalid UTF-8", "/x/bad\xffx"},
		{"a lone continuation byte", "/x/\x80"},
		{"a truncated sequence", "/x/\xe2\x80"},
		{"an encoded surrogate", "/x/\xed\xa0\x80"},
		{"NUL", "/x/nul\x00x"},
	} {
		if got, err := SBPLString(tc.in); err == nil || got != "" {
			t.Errorf("%s: SBPLString(%q) = %q, %v; want a refusal", tc.what, tc.in, got, err)
		}
	}
}

// For ordinary names the bytes are strconv.Quote's, which is what the
// plugin wall wrote before this writer: every character Go judges printable,
// one at a time across the whole of Unicode, and the names the wall writes
// in practice. A character Go does not judge printable is written raw.
func TestSBPLStringIsStrconvQuoteForEveryPrintableCharacter(t *testing.T) {
	for _, name := range []string{
		"/Users/a/.aii/identity-0/data/models/selected",
		"/Users/a/Work Files/scratch/sbpl",
		"/private/var/folders/xy/abcd1234/T/aii-plugin-7",
		"/Users/a/.aii/plugins/.by-id/3f2a9c/runtime/bin/engine",
		"/Users/a/Projets/réserve/données",
		"/Users/a/文档/模型",
		"/Users/a/models 🎙️/voice",
		"com.apple.MTLCompilerService",
		"AGXDeviceUserClient",
		"/bin/sh",
	} {
		got, err := SBPLString(name)
		if want := strconv.Quote(name); err != nil || got != want {
			t.Errorf("SBPLString(%q) = %q, %v; strconv.Quote wrote %q", name, got, err, want)
		}
	}
	printable, raw := 0, 0
	for r := rune(1); r <= unicode.MaxRune; r++ {
		if !utf8.ValidRune(r) {
			continue
		}
		s := "/a/" + string(r) + "/b"
		got, err := SBPLString(s)
		if err != nil {
			t.Fatalf("U+%04X: refused a valid character: %v", r, err)
		}
		if strconv.IsPrint(r) {
			printable++
			if want := strconv.Quote(s); got != want {
				t.Fatalf("U+%04X is printable to Go, and the bytes differ: %q, strconv.Quote %q", r, got, want)
			}
			continue
		}
		raw++
		if got != `"`+s+`"` {
			t.Fatalf("U+%04X is not printable to Go and must be written as its own bytes: %q", r, got)
		}
	}
	t.Logf("%d printable characters as strconv.Quote writes them, %d others as their own bytes", printable, raw)
}
