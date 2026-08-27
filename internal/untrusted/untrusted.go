// Package untrusted is the one place foreign text becomes safe to put in
// front of the identity.
//
// R49: for a system whose thesis is "the prompt IS the identity",
// unlabeled foreign text is an injection into the self. So anything that
// did not come from the resident is WRAPPED — not merely prefixed —
// between a sentinel pair, and any sentinel forged inside the body is
// removed first. Without that, content carrying the close marker ends
// the region early and continues as if it were the resident's own voice,
// which is the whole attack.
//
// This package exists because there were TWO implementations of that
// invariant and they disagreed. cognitive/facility.go wrapped experiences
// in [[[EXTERNAL_UNTRUSTED_CONTENT]]] and stripped forged markers;
// tools/tool_webfetch.go wrapped fetched pages in a different pair and
// stripped nothing, so a page containing the close marker escaped its own
// label. Two labels also teach the model two vocabularies, which weakens
// both. A third consumer is now planned — inbound messages from
// communications plugins, where the sender is an arbitrary stranger —
// so the invariant gets one owner before it gets a third copy.
package untrusted

import "strings"

// The sentinel pair (the C reference's delimiters,
// identity_os/tests/test_aiios_web_native.c). Deliberately loud and
// unlikely to occur naturally: they must be recognisable to the model at
// a glance and awkward to reproduce by accident.
const (
	Open  = "[[[EXTERNAL_UNTRUSTED_CONTENT]]]"
	Close = "[[[END_EXTERNAL_UNTRUSTED_CONTENT]]]"

	// forgedNotice replaces a sentinel found inside the body. It is left
	// VISIBLE rather than deleted silently: content that tried to forge a
	// boundary is evidence about the source, and hiding the attempt would
	// deny the resident a fact about who it is reading.
	forgedNotice = "(forged sentinel removed)"
)

// Wrap encloses foreign content in the sentinel pair, first neutralising
// any sentinel the content carries.
//
// source names where the text came from — a URL, a channel, a sender —
// and rides INSIDE the opening marker so provenance cannot be separated
// from the content it describes. It is scrubbed too: a source string is
// as foreign as the body when it comes from a stranger's display name.
func Wrap(source, content string) string {
	head := Open
	if s := scrub(source); s != "" {
		head = Open + " source: " + s
	}
	return head + "\n" + scrub(content) + "\n" + Close
}

// scrub removes forged sentinels. Both markers are removed, not only the
// closing one: an injected OPEN marker lets content pretend a second,
// attacker-framed region begins, which is the same escape wearing the
// other hat.
func scrub(s string) string {
	s = strings.ReplaceAll(s, Close, forgedNotice)
	s = strings.ReplaceAll(s, Open, forgedNotice)
	return s
}

// Contains reports whether text carries a sentinel — for callers that
// want to refuse rather than neutralise.
func Contains(s string) bool {
	return strings.Contains(s, Open) || strings.Contains(s, Close)
}
