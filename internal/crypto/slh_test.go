package crypto

import (
	"crypto/rand"
	"testing"

	slh "github.com/trailofbits/go-slh-dsa/slh_dsa"
)

// SLH-DSA IS THE OTHER HALF OF THE PAIR, and it had no verification
// test at all — VerifySLH and RequiredAlgorithms were each at 0%. Every ai3-bundle ceremony rests on both signatures, and
// a verifier that fails OPEN is the one failure here that cannot be
// recovered from: it admits an artifact nobody signed.
//
// So the negatives matter more than the positive. A verifier that
// always returned nil would pass a round-trip test and fail every
// case below.

func slhPair(t *testing.T) (slh.SecretKey, []byte) {
	t.Helper()
	sk, pk, err := slh.SLHKeygen(slh.SlhDsaSha2_256s())
	if err != nil {
		t.Fatalf("SLH keygen: %v", err)
	}
	return sk, pk.Bytes()
}

func TestVerifySLHAcceptsWhatItSigned(t *testing.T) {
	sk, pub := slhPair(t)
	msg := []byte("the payload the ceremony binds")
	sig, err := sk.Sign(rand.Reader, msg, nil)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if err := VerifySLH(pub, msg, sig); err != nil {
		t.Fatalf("a signature this key just made was refused: %v", err)
	}
}

// FAILING OPEN IS THE ONLY UNRECOVERABLE DIRECTION. Each case below is
// a different way an artifact could be wrong; all four must refuse.
func TestVerifySLHRefusesEverythingItShould(t *testing.T) {
	sk, pub := slhPair(t)
	_, otherPub := slhPair(t)
	msg := []byte("the payload the ceremony binds")
	sig, err := sk.Sign(rand.Reader, msg, nil)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	tampered := append([]byte(nil), sig...)
	tampered[len(tampered)/2] ^= 0x01

	for _, tc := range []struct {
		name string
		pub  []byte
		msg  []byte
		sig  []byte
	}{
		{"a different key", otherPub, msg, sig},
		{"a changed message", pub, []byte("the payload the ceremony binds."), sig},
		{"one flipped bit in the signature", pub, msg, tampered},
		{"a truncated signature", pub, msg, sig[:len(sig)-1]},
		{"an empty signature", pub, msg, nil},
		{"a malformed public key", []byte("not a key"), msg, sig},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := VerifySLH(tc.pub, tc.msg, tc.sig); err == nil {
				t.Fatal("VERIFICATION FAILED OPEN — this artifact would be admitted unsigned")
			}
		})
	}
}

// ProfileRoot REQUIRES BOTH ALGORITHMS. A profile that quietly required
// one would make the second signature decorative — the pair exists so
// that breaking one algorithm is not enough.
func TestRequiredAlgorithmsNamesBothHalvesOfTheRootProfile(t *testing.T) {
	algs, ok := RequiredAlgorithms(ProfileRoot)
	if !ok {
		t.Fatal("the root profile reports no required algorithms")
	}
	if len(algs) != 2 {
		t.Fatalf("root requires %v — the pair is the point", algs)
	}
	var haveML, haveSLH bool
	for _, a := range algs {
		switch a {
		case SigAlg:
			haveML = true
		case SLHAlg:
			haveSLH = true
		}
	}
	if !haveML || !haveSLH {
		t.Fatalf("root requires %v, want both %s and %s", algs, SigAlg, SLHAlg)
	}
	if _, ok := RequiredAlgorithms("some.other.profile"); ok {
		t.Fatal("an unknown profile reported requirements — it must be refused, not guessed at")
	}
}
