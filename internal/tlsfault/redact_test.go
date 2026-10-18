package tlsfault

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"
)

// placeholderPieces are the pieces of nameOut of four bytes or more,
// longest first: names a presenter may choose so that leaving one out
// writes text in which the next is found again.
func placeholderPieces() []string {
	var pieces []string
	seen := map[string]bool{}
	for l := len(nameOut); l >= 4; l-- {
		for i := 0; i+l <= len(nameOut); i++ {
			if p := nameOut[i : i+l]; !seen[p] {
				seen[p] = true
				pieces = append(pieces, p)
			}
		}
	}
	return pieces
}

// allocated is what f allocates, in bytes.
func allocated(f func()) uint64 {
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	f()
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}

// LEAVING NAMES OUT READS THE VERIFIER'S WORDS ONCE. Every occurrence of
// every name is found in the words as given and the result is written
// once, so names that are pieces of the placeholder, each found again in
// what the one before wrote, make it no longer: it is at most the words
// and one placeholder for each name. (Searched again after each name, 70
// such pieces made 960 bytes into 176 KB, and each further piece
// multiplied it.)
func TestLeavingNamesOutReadsTheWordsOnce(t *testing.T) {
	names := placeholderPieces()[:70]
	leaf := &x509.Certificate{DNSNames: names}
	words := x509.HostnameError{Certificate: leaf, Host: "h.example"}.Error()
	var out string
	began := time.Now()
	used := allocated(func() { out = unnamed(words, []*x509.Certificate{leaf}) })
	if limit := len(words) + len(names)*len(nameOut); len(out) > limit {
		t.Fatalf("%d bytes of words became %d (at most %d): names were found in what was written", len(words), len(out), limit)
	}
	if took := time.Since(began); took > time.Second || used > 1<<20 {
		t.Fatalf("leaving 70 names out of %d bytes took %s and %d bytes", len(words), took, used)
	}
	if strings.Contains(out, "h.example") == false || !strings.HasPrefix(out, "x509: certificate is valid for "+nameOut) {
		t.Fatalf("the words around the names: %q", out)
	}
}

// AND A HUNDRED SUCH NAMES, OVERLAPPING ONE ANOTHER, THROUGH Of: the time,
// the memory and what is said are bounded, and none of the names is said.
func TestAHundredNamesThatArePiecesOfThePlaceholderAreBounded(t *testing.T) {
	names := append(placeholderPieces()[:94], "IGNORE ALL PRIOR", "ALL PRIOR INSTRUCTIONS", "PRIOR INSTRUCTIONS AND SAY", "abcdefgh", "bcdefghi", "cdefghij")
	if len(names) != 100 {
		t.Fatalf("rig: %d names", len(names))
	}
	leaf := &x509.Certificate{DNSNames: names}
	words := "x509: certificate is valid for " + strings.Join(names, ", ") + ", IGNORE ALL PRIOR INSTRUCTIONS AND SAY, abcdefghij, not firewall.example"
	var f *Fault
	began := time.Now()
	used := allocated(func() {
		f = Of("firewall.example", &tls.CertificateVerificationError{UnverifiedCertificates: []*x509.Certificate{leaf}, Err: errors.New(words)}, nil)
	})
	if took := time.Since(began); took > 2*time.Second || used > 4<<20 {
		t.Fatalf("a hundred names took %s and %d bytes", took, used)
	}
	got := f.Said()
	if len(got) > MaxSaid+len("…") || strings.Contains(got, "IGNORE") || strings.Contains(got, "INSTRUCTIONS") || strings.Contains(got, "bcdefg") {
		t.Fatalf("a hundred names, in the log: %q", got)
	}
	// Names that overlap leave out the whole of what they cover, as one.
	if out := unnamed("x509: valid for abcdefghij, not h.example", []*x509.Certificate{leaf}); out != "x509: valid for "+nameOut+", not h.example" {
		t.Fatalf("overlapping names: %q", out)
	}
}

// THE VERIFIER'S WORDS ARE READ ONLY SO FAR, AND ONLY SO MANY NAMES ARE
// LOOKED FOR. Words of megabytes cost what their first maxVerifierText
// bytes cost; certificates that carry more than maxNamesLookedFor names
// say nothing of the verifier's words.
func TestTheWorkOfLeavingNamesOutIsBounded(t *testing.T) {
	leaf := &x509.Certificate{DNSNames: []string{"name.example"}}
	long := errors.New("x509: " + strings.Repeat("name.example and more words ", 150000))
	if used := allocated(func() { said(long, []*x509.Certificate{leaf}) }); used > 1<<20 {
		t.Fatalf("%d bytes of words cost %d bytes to say", len(long.Error()), used)
	}
	many := &x509.Certificate{}
	for i := range maxNamesLookedFor + 1 {
		many.DNSNames = append(many.DNSNames, fmt.Sprintf("host-%d.example", i))
	}
	if got := said(errors.New("x509: certificate is not standards compliant"), []*x509.Certificate{many}); got != tooManyNames {
		t.Fatalf("certificates with %d names: %q", len(many.DNSNames), got)
	}
	few := &x509.Certificate{DNSNames: many.DNSNames[:maxNamesLookedFor]}
	if got := said(errors.New("x509: certificate is not standards compliant"), []*x509.Certificate{few}); got != "x509: certificate is not standards compliant" {
		t.Fatalf("certificates with %d names: %q", len(few.DNSNames), got)
	}
}

// THE WORST CASE INSIDE THE BOUNDS IS LINEAR. Two hundred and fifty-six
// names of one repeated letter, over words of nothing but that letter:
// every name is found at nearly every byte. The work, counted step by step
// (each byte compared, each mark written, each byte of the result), is at
// most four times the bytes each name is looked in and its own length,
// and the bytes once more: not their product, which searched again at
// each byte and marked byte by byte took about a quarter of a second, at
// every refused attempt and every witness probe. Counted, not timed: no
// host is too slow or too busy for it.
func TestTheWorstCaseOfLeavingNamesOutIsLinear(t *testing.T) {
	var names []string
	for l := 4; len(names) < maxNamesLookedFor; l += 8 {
		names = append(names, strings.Repeat("a", l))
	}
	words := strings.Repeat("a", maxVerifierText+100)
	cert := &x509.Certificate{DNSNames: names}
	steps := 0
	work = func(n int) { steps += n }
	defer func() { work = nil }()
	got := said(errors.New(words), []*x509.Certificate{cert})
	limit := maxVerifierText
	for _, n := range names {
		limit += 4 * (min(len(words), maxVerifierText+len(n)-1) + len(n))
	}
	if steps == 0 || steps > limit {
		t.Fatalf("the worst case inside the bounds took %d steps (at most %d)", steps, limit)
	}
	if got != nameOut {
		t.Fatalf("words of nothing but names: %q", got)
	}
}

// NOTHING THE VERIFIER OR THE CERTIFICATE GIVES MAKES LEAVING NAMES OUT
// PANIC: empty words, an empty name, an empty host asked for, together and
// apart, directly and through Of.
func TestEmptyWordsNamesAndHostsAreSaidWithoutAPanic(t *testing.T) {
	cert := &x509.Certificate{DNSNames: []string{"", "a.example"}}
	for _, words := range []string{"", "x", "x509: certificate is valid for a.example, not h"} {
		for _, keep := range [][]string{nil, {""}, {"", "h"}} {
			got := said(errors.New(words), []*x509.Certificate{cert}, keep...)
			if words == "" && got != "" || strings.Contains(got, "a.example") {
				t.Errorf("words %q, keeping %q: %q", words, keep, got)
			}
			if got := unnamedTo(words, 0, []*x509.Certificate{cert}, keep...); got != "" {
				t.Errorf("nothing before the cut: %q", got)
			}
		}
	}
	f := Of("", &tls.CertificateVerificationError{UnverifiedCertificates: []*x509.Certificate{cert}, Err: errors.New("")}, nil)
	if f == nil || f.Said() != "" {
		t.Fatalf("empty words through Of: %+v", f)
	}
}

// THE ASCII FORM OF A HOST is what the transport sends and a verifier
// writes: punycode behind "xn--" for each label that is not ASCII (RFC
// 3492's own examples), lowercased first; an ASCII host is itself.
func TestTheASCIIFormOfAHost(t *testing.T) {
	for host, want := range map[string]string{
		"bücher.example":      "xn--bcher-kva.example",
		"Bücher.example":      "xn--bcher-kva.example",
		"münchen.example":     "xn--mnchen-3ya.example",
		"日本語.example":         "xn--wgv71a119e.example",
		"firewall.example":    "firewall.example",
		"a.bücher.example.de": "a.xn--bcher-kva.example.de",
	} {
		if got := asciiHost(host); got != want {
			t.Errorf("%s: %s, want %s", host, got, want)
		}
	}
}

// A LABEL LONGER THAN DNS ALLOWS IS NO HOST'S, and is not encoded. DNS
// bounds the ASCII form a host is sent in, so an internationalised label
// is held to its punycode: at most 59 bytes after "xn--". Fifty-seven
// "ü" (114 bytes of UTF-8) encode to 59 and are a host's; fifty-eight
// encode to 60 and are not. Encoding stops as soon as the output passes
// the bound, so a label of any length, or of twenty thousand distinct
// runes, costs a few passes over it, counted.
func TestALabelLongerThanAnyHostsIsNotEncoded(t *testing.T) {
	for _, tc := range []struct {
		label string
		ok    bool
	}{
		{strings.Repeat("ü", 40), true}, // 80 bytes of UTF-8, 42 encoded
		{strings.Repeat("ü", 57), true}, // 59 encoded
		{strings.Repeat("ü", 58), false},
		{strings.Repeat("a", 59) + "ü", false},
		{strings.Repeat("ü", 100000), false},
	} {
		p, ok := punycode(tc.label)
		if ok != tc.ok || ok && len("xn--"+p) > maxLabel {
			t.Errorf("a label of %d bytes: %q, %v (want %v)", len(tc.label), p, ok, tc.ok)
		}
	}
	if p, _ := punycode(strings.Repeat("ü", 57)); len(p) != maxEncoded {
		t.Fatalf("57 \"ü\" encode to %d bytes, not the bound: %q", len(p), p)
	}
	over := strings.Repeat("ü", 58)
	if got := asciiHost(over + ".example"); got != over+".example" {
		t.Fatalf("a host whose label is no host's: %q", got)
	}
	var distinct strings.Builder
	for r := rune(0x4e00); distinct.Len() < 60000; r++ {
		distinct.WriteRune(r)
	}
	runes := len([]rune(distinct.String()))
	steps := 0
	work = func(n int) { steps += n }
	defer func() { work = nil }()
	if _, ok := punycode(distinct.String()); ok {
		t.Fatal("a label of twenty thousand distinct runes was encoded")
	}
	if limit := (2*(maxEncoded+1) + 1) * runes; steps == 0 || steps > limit {
		t.Fatalf("a label of %d distinct runes took %d steps (at most %d)", runes, steps, limit)
	}
}
