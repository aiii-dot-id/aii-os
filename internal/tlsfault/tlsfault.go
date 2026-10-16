package tlsfault

import (
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type Reason string

const (
	Expired      Reason = "expired"
	NotYetValid  Reason = "not_yet_valid"
	WrongName    Reason = "wrong_name"
	Untrusted    Reason = "untrusted"
	KeyNotPinned Reason = "key_not_pinned"
	Refused      Reason = "refused"
)

type Fault struct {
	Host      string
	Reason    Reason
	NotBefore time.Time
	NotAfter  time.Time
	Seen      time.Time

	Names     []string
	MoreNames int

	NamesHost bool

	AlsoUntrusted bool

	NeverValid bool

	Proxy     bool
	ProxyHost string
	Err       error
	said      string
}

func (f *Fault) Unwrap() error { return f.Err }

func (f *Fault) Error() string {
	host := Printable(f.Host, maxName)
	who, server, asked := "the server "+host, "the server", host
	if f.Proxy {
		who, server, asked = "the HTTPS proxy this machine reaches "+host+" through", "the proxy", "the proxy's address"
		if f.ProxyHost != "" {
			asked = Printable(f.ProxyHost, maxName)
		}
	}
	reason := f.Reason
	if reason == WrongName && f.NamesHost {

		reason = Refused
	}
	if reason == KeyNotPinned {
		return who + " presented a key that is not the pinned one (its certificate is otherwise accepted): if " + server + "'s key was changed, the pin configured for it must be changed to match; if not, a network may be intercepting the connection"
	}
	clock := "this machine's clock reads " + f.Seen.UTC().Format(time.RFC3339)
	ended := !f.NotAfter.IsZero() && f.Seen.After(f.NotAfter)
	untrusted := "this machine's store of trusted authorities may be missing or out of date, or a proxy may be intercepting the connection; if neither, the server must "
	if f.Proxy {
		untrusted = "this machine's store of trusted authorities may be missing or out of date, or lack the proxy's own authority; if neither, the proxy must "
	}
	const alsoUntrusted = " and is not shown to be from an authority this machine trusts"
	var seen, causes string
	dated := false
	switch {
	case f.NeverValid && (reason == Expired || reason == NotYetValid):
		dated = true
		seen = "is valid at no time (its dates run from " + f.NotBefore.UTC().Format(time.RFC3339) + " to " + f.NotAfter.UTC().Format(time.RFC3339) + ")" + alsoUntrusted
		causes = untrusted + "present a certificate in date from a trusted authority"
	case reason == Expired:
		if ended {
			dated = true
			seen = "expired on " + f.NotAfter.UTC().Format(time.RFC3339) + " (" + clock + ")"
			causes = "it must be renewed, unless this machine's clock is ahead"
			if f.Proxy {
				causes = "it must be renewed, or replaced by one that names the proxy, unless this machine's clock is ahead"
			}
			if f.AlsoUntrusted {
				seen += alsoUntrusted
				causes = untrusted + "present a certificate in date from a trusted authority"
			}
		} else {

			seen = "is in a chain that holds a certificate out of its dates, expired or not yet valid (" + clock + ")"
			causes = "that certificate must be replaced, by " + server + "'s owner or, if it is an authority in this machine's own store, on this machine; unless this machine's clock is wrong"
		}
	case reason == NotYetValid:
		dated = true
		seen = "is not valid until " + f.NotBefore.UTC().Format(time.RFC3339) + " (" + clock + ")"
		causes = "if this machine's clock is behind, set it right; if not, it was issued too early"
		if f.Proxy {
			causes = "if this machine's clock is behind, set it right; if not, it must be replaced by one in date that names the proxy"
		}
		if f.AlsoUntrusted {
			seen += alsoUntrusted
			causes = untrusted + "present a certificate from a trusted authority; and if this machine's clock is behind, set it right"
		}
	case reason == Untrusted:
		seen = "is signed by an authority this machine does not trust"
		causes = untrusted + "use a certificate from a trusted authority"
	case reason == WrongName:
	default:
		seen = "was refused for a reason this client does not name (the log gives the verifier's own words)"
	}
	s := who + " presented a certificate that "
	if f.NamesHost {
		s += seen
		if f.Proxy && dated {

			s += ", and names " + f.names()
		}
		if causes != "" {
			s += ": " + causes
		}
		return s
	}

	if seen != "" {
		s += seen + " and "
	}
	s += "names " + f.names() + " and not " + asked + ": if " + asked + " is " + server + "'s own name, "
	if f.Reason == WrongName {
		s += "its certificate must name it"
	} else {
		s += "it must present a certificate that names it and that this machine accepts"
	}
	if f.Proxy {
		return s + "; if not, the proxy address this machine is configured with is wrong"
	}
	return s + "; if not, the address configured for it is wrong. A network that intercepts connections (a proxy, a captive portal) is refused the same way"
}

func (f *Fault) Said() string { return f.said }

func ForTheLog(err error) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	var f *Fault
	if errors.As(err, &f) && f.said != "" {
		s += " (the verifier said: " + f.said + ")"
	}
	return s
}

type NotPinned struct {
	Cert *x509.Certificate
}

func (e *NotPinned) Error() string {
	return "TLS SPKI pin mismatch: certificate key is not the pinned key"
}

func (f *Fault) names() string {
	if len(f.Names) == 0 {
		switch f.MoreNames {
		case 0:
			return "no host"
		case 1:
			return "1 host not repeated here"
		}
		return fmt.Sprintf("%d hosts not repeated here", f.MoreNames)
	}
	s := strings.Join(f.Names, ", ")
	if f.MoreNames > 0 {
		s += fmt.Sprintf(", and %d more", f.MoreNames)
	}
	return s
}

const (
	maxNames = 5
	maxName  = 253
)

const MaxSaid = 200

func hostName(n string) bool {
	if n == "" || len(n) > maxName {
		return false
	}
	for _, r := range n {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune(".-*:_", r)) {
			return false
		}
	}
	return true
}

const (
	quotedOut = "(a quoted name, left out)"
	nameOut   = "(a name, left out)"
)

var closers = map[rune]string{
	'“': "”“", '„': "“”", '”': "”“", '"': "\"",
	'«': "»", '»': "«", '‹': "›", '›': "‹",
	'‘': "’",
	'「': "」", '『': "』",
}

func unquoted(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		close, opens := closers[r]
		if !opens {
			b.WriteString(s[i : i+size])
			i += size
			continue
		}
		b.WriteString(quotedOut)
		rest := s[i+size:]
		last := strings.LastIndexAny(rest, close)
		if last < 0 {
			return b.String()
		}
		_, closeSize := utf8.DecodeRuneInString(rest[last:])
		i += size + last + closeSize
	}
	return b.String()
}

const (
	maxVerifierText = 4096

	maxNamesLookedFor = 256
)

const tooManyNames = "(left out: the certificates presented carry more names than are looked for)"

func certNames(certs []*x509.Certificate) []string {
	var names []string
	for _, c := range certs {
		if c == nil {
			continue
		}
		for _, atv := range append(append([]pkix.AttributeTypeAndValue(nil), c.Subject.Names...), c.Issuer.Names...) {
			if v, ok := atv.Value.(string); ok {
				names = append(names, v)
			}
		}
		names = append(names, c.DNSNames...)
		names = append(names, c.EmailAddresses...)
		for _, ip := range c.IPAddresses {
			names = append(names, ip.String())
		}
		for _, u := range c.URIs {
			names = append(names, u.String())
		}
	}
	return names
}

func unnamed(s string, certs []*x509.Certificate, keep ...string) string {
	return unnamedTo(s, len(s), certs, keep...)
}

func unnamedTo(words string, cut int, certs []*x509.Certificate, keep ...string) string {
	names := certNames(certs)
	if len(names) > maxNamesLookedFor {
		return tooManyNames
	}
	if cut <= 0 {
		return ""
	}

	covered := make([]int32, cut+1)
	guarded := make([]int32, cut+1)
	steps := 0
	mark := func(marks []int32, at, n int) {
		marks[at]++
		marks[min(at+n, cut)]--
		steps += 2
	}

	within := func(n int) string { return words[:min(len(words), cut+n-1)] }
	for _, k := range keep {
		if k != "" {
			eachOccurrence(within(len(k)), k, func(at int) { mark(guarded, at, len(k)) })
		}
	}
	for _, n := range names {
		if n == "" {
			continue
		}
		eachOccurrence(within(len(n)), n, func(at int) {
			if len(n) >= 4 || alone(words, at, len(n)) {
				mark(covered, at, len(n))
			}
		})
	}
	steps += cut
	if work != nil {
		work(steps)
	}
	var b strings.Builder
	name, host, open := int32(0), int32(0), false
	for i := 0; i < cut; i++ {
		name += covered[i]
		host += guarded[i]
		if name > 0 && host == 0 {
			if !open {
				b.WriteString(nameOut)
				open = true
			}
			continue
		}
		open = false
		b.WriteByte(words[i])
	}
	return b.String()
}

var work func(steps int)

func eachOccurrence(s, n string, found func(at int)) {
	if n == "" || len(n) > len(s) {
		return
	}
	steps := 0
	fail := make([]int, len(n))
	for i, k := 1, 0; i < len(n); i++ {
		for k > 0 && n[i] != n[k] {
			k = fail[k-1]
			steps++
		}
		if n[i] == n[k] {
			k++
		}
		fail[i] = k
		steps++
	}
	for i, k := 0, 0; i < len(s); i++ {
		for k > 0 && s[i] != n[k] {
			k = fail[k-1]
			steps++
		}
		if s[i] == n[k] {
			k++
		}
		steps++
		if k == len(n) {
			found(i - len(n) + 1)
			k = fail[k-1]
		}
	}
	if work != nil {
		work(steps)
	}
}

func alone(s string, at, n int) bool {
	before, _ := utf8.DecodeLastRuneInString(s[:at])
	after, _ := utf8.DecodeRuneInString(s[at+n:])
	return (at == 0 || !unicode.IsLetter(before) && !unicode.IsDigit(before)) && (at+n == len(s) || !unicode.IsLetter(after) && !unicode.IsDigit(after))
}

func said(err error, certs []*x509.Certificate, keep ...string) string {
	if err == nil {
		return ""
	}
	words := err.Error()
	cut := len(words)
	if cut > maxVerifierText {
		cut = maxVerifierText
		for cut > 0 && !utf8.RuneStart(words[cut]) {
			cut--
		}
	}
	return Printable(unquoted(unnamedTo(words, cut, certs, keep...)), MaxSaid)
}

func asciiHost(host string) string {
	labels := strings.Split(host, ".")
	for i, l := range labels {
		if isASCII(l) {
			continue
		}
		if p, ok := punycode(strings.ToLower(l)); ok {
			labels[i] = "xn--" + p
		}
	}
	return strings.Join(labels, ".")
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

const (
	maxLabel   = 63
	maxEncoded = maxLabel - len("xn--")
)

func punycode(label string) (string, bool) {
	const (
		base, tmin, tmax, skew, damp = 36, 1, 26, 38, 700
		initialBias, initialN        = 72, 128
	)
	adapt := func(delta, points int, first bool) int {
		if first {
			delta /= damp
		} else {
			delta /= 2
		}
		delta += delta / points
		k := 0
		for delta > ((base-tmin)*tmax)/2 {
			delta /= base - tmin
			k += base
		}
		return k + (base-tmin+1)*delta/(delta+skew)
	}
	digit := func(d int) byte {
		if d < 26 {
			return byte('a' + d)
		}
		return byte('0' + d - 26)
	}
	runes := []rune(label)
	var out []byte
	for _, r := range runes {
		if r < 0x80 {
			out = append(out, byte(r))
		}
	}
	basic := len(out)
	if basic > 0 {
		out = append(out, '-')
	}
	steps := len(runes)
	defer func() {
		if work != nil {
			work(steps)
		}
	}()
	if len(out) > maxEncoded {
		return "", false
	}
	n, delta, bias, done := initialN, 0, initialBias, basic
	for done < len(runes) {
		m := int(unicode.MaxRune) + 1
		for _, r := range runes {
			if int(r) >= n && int(r) < m {
				m = int(r)
			}
		}
		steps += 2 * len(runes)
		if (m - n) > (1<<30)/(done+1) {
			return "", false
		}
		delta += (m - n) * (done + 1)
		n = m
		for _, r := range runes {
			if int(r) < n {
				delta++
			}
			if int(r) != n {
				continue
			}
			q := delta
			for k := base; ; k += base {
				t := min(max(k-bias, tmin), tmax)
				if q < t {
					break
				}
				out = append(out, digit(t+(q-t)%(base-t)))
				q = (q - t) / (base - t)
			}
			out = append(out, digit(q))
			if len(out) > maxEncoded {
				return "", false
			}
			bias = adapt(delta, done+1, done == basic)
			delta = 0
			done++
		}
		delta++
		n++
	}
	return string(out), true
}

func Printable(s string, max int) string {
	var b strings.Builder
	for _, r := range s {
		if b.Len() >= max {
			b.WriteString("…")
			break
		}
		if !unicode.IsPrint(r) {
			r = ' '
		}
		b.WriteRune(r)
	}
	return b.String()
}

func Of(host string, err error, roots *x509.CertPool) *Fault {
	if err == nil {
		return nil
	}
	var request *url.Error
	if errors.As(err, &request) {
		if u, perr := url.Parse(request.URL); perr == nil && u.Hostname() != "" {
			host = u.Hostname()
		}
	}
	f := &Fault{Host: host, Reason: Refused, Err: err, NamesHost: true, Seen: time.Now()}
	var verify *tls.CertificateVerificationError
	var invalid x509.CertificateInvalidError
	var name x509.HostnameError
	var authority x509.UnknownAuthorityError
	var pin *NotPinned
	var leaf *x509.Certificate
	var chain, presented []*x509.Certificate
	var words error
	switch {
	case errors.As(err, &pin):
		leaf = pin.Cert
	case errors.As(err, &verify):
		if len(verify.UnverifiedCertificates) > 0 {
			leaf, chain = verify.UnverifiedCertificates[0], verify.UnverifiedCertificates[1:]
		}
		words, presented = verify.Err, verify.UnverifiedCertificates
	case errors.As(err, &invalid):
		leaf, words, presented = invalid.Cert, invalid, []*x509.Certificate{invalid.Cert}
	case errors.As(err, &name):
		leaf, words, presented = name.Certificate, name, []*x509.Certificate{name.Certificate}
	case errors.As(err, &authority):
		leaf, words, presented = authority.Cert, authority, []*x509.Certificate{authority.Cert}
	default:
		return nil
	}
	switch {
	case pin != nil:
		f.Reason = KeyNotPinned
	case errors.As(err, &invalid) && invalid.Reason == x509.Expired:
		f.Reason = Expired
		if leaf != nil && f.Seen.Before(leaf.NotBefore) {
			f.Reason = NotYetValid
		}
	case errors.As(err, &name):
		f.Reason = WrongName
	case errors.As(err, &authority):
		f.Reason = Untrusted
	}
	if leaf != nil {
		f.NotBefore, f.NotAfter = leaf.NotBefore, leaf.NotAfter
		names := append([]string(nil), leaf.DNSNames...)
		for _, ip := range leaf.IPAddresses {
			names = append(names, ip.String())
		}
		for _, n := range names {
			if len(f.Names) < maxNames && hostName(n) {
				f.Names = append(f.Names, n)
			} else {
				f.MoreNames++
			}
		}
		f.NamesHost = leaf.VerifyHostname(host) == nil

		outOfDate := f.Reason == Expired && f.Seen.After(leaf.NotAfter) || f.Reason == NotYetValid
		if outOfDate {
			f.NeverValid = !leaf.NotBefore.Before(leaf.NotAfter)
			f.AlsoUntrusted = untrustedWithin(leaf, chain, roots)
		}
	}

	var op *net.OpError
	if errors.As(err, &op) && op.Op == "proxyconnect" {
		f.Proxy, f.NamesHost = true, true
		if errors.As(err, &name) {
			f.ProxyHost, f.NamesHost = name.Host, false
		}
	}

	var keep []string
	for _, h := range []string{f.Host, f.ProxyHost} {
		if h != "" {
			keep = append(keep, h, asciiHost(h))
		}
	}
	if errors.As(err, &name) {
		keep = append(keep, name.Host)
	}
	f.said = said(words, presented, keep...)
	return f
}

func untrustedWithin(leaf *x509.Certificate, chain []*x509.Certificate, roots *x509.CertPool) bool {
	from, until := leaf.NotBefore, leaf.NotAfter
	intermediates := x509.NewCertPool()
	for _, c := range chain {
		intermediates.AddCert(c)
		if c.NotBefore.After(from) {
			from = c.NotBefore
		}
		if c.NotAfter.Before(until) {
			until = c.NotAfter
		}
	}
	if !from.Before(until) {
		return true
	}
	at := from.Add(until.Sub(from) / 2)
	_, err := leaf.Verify(x509.VerifyOptions{Roots: roots, Intermediates: intermediates, CurrentTime: at})
	return err != nil
}
