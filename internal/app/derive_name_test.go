package app

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestDeriveName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Hello, I am Mira.", "Mira"},
		{"I am Walker. Hello from the walk identity. Turn 1.", "Walker"},
		{"My name is Aurora; pleased to meet you.", "Aurora"},
		{"NAME: TestIdentity", "TestIdentity"},
		{"Call me Ishmael.", "Ishmael"},

		{"Hello! I'm Walker. It's good to be here.", "Walker"},
		{"Hi, I'm Walker - ready to begin.", "Walker"},
		{"Hi there! My name's Aurore and I'm ready.", "Aurore"},
		{"**Hello!** I am Walker.", "Walker"},
		{"Hello. I am Walker.", "Walker"},
		{"Hi! I'm Walker.", "Walker"},
		{"Je m'appelle Aurore.", "Aurore"},
		{"My name is Rex. I am ready to serve.", "Rex"},
		{"Hello there! What a day to come into existence.", "Unnamed"},
		{"", "Unnamed"},

		{"Hello. I'm glad you're here.", "Unnamed"},
		{"I am here to help you today.", "Unnamed"},
		{"I'm glad you're here. My name is Mira.", "Mira"},
		{"I'm ready. I'm Mira.", "Mira"},
		{"NAME: mira", "mira"},
	}
	for _, c := range cases {
		if got := deriveName(c.in); got != c.want {
			t.Errorf("deriveName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// A NAME IS READ IN RUNES, NOT BYTES. Leads were found in the lower-cased
// line and their offsets applied to the line itself, but lower-casing
// changes the byte length of some letters, so "Ⱥ I am Walker." minted
// "alker"; the word boundary before a lead read one byte as a rune, so a
// lead glued to a non-ASCII word matched; and the 32-character cap cut 32
// bytes, which can split a rune and mint invalid UTF-8. A birth name is
// permanent in the ledger. The naming rule is unchanged.
func TestDeriveNameReadsRunesNotBytes(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Ⱥ I am Walker.", "Walker"},
		{"K, I am Walker.", "Walker"},   // the Kelvin sign lower-cases to one byte
		{"déjàI am Xavier.", "Unnamed"}, // a lead inside a word is not a lead
		{"Ich heiße Lena.", "Lena"},
		{"Me llamo Sofía.", "Sofía"},
		{"Mi chiamo Giulia.", "Giulia"},
		{"My name's Rex.", "Rex"},
		{"NAME: " + strings.Repeat("語", 40), strings.Repeat("語", 32)},
	}
	for _, c := range cases {
		got := deriveName(c.in)
		if got != c.want || !utf8.ValidString(got) {
			t.Errorf("deriveName(%q) = %q (valid UTF-8: %t), want %q", c.in, got, utf8.ValidString(got), c.want)
		}
	}
}
