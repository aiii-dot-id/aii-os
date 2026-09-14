package crypto

import (
	"crypto/rand"
	"encoding/base64"
	"strings"
	"testing"

	slh "github.com/trailofbits/go-slh-dsa/slh_dsa"
)

// .
// .
// .
// .
// .
// .
// .
// .
// .

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

// .
// .
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

// .
// .
// .
func TestVerifySLHB64(t *testing.T) {
	sk, pub := slhPair(t)
	msg := []byte("enveloped")
	sig, err := sk.Sign(rand.Reader, msg, nil)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	pubB64 := base64.StdEncoding.EncodeToString(pub)
	sigB64 := base64.StdEncoding.EncodeToString(sig)

	if err := VerifySLHB64(pubB64, msg, sigB64); err != nil {
		t.Fatalf("a well-formed envelope was refused: %v", err)
	}
	for _, tc := range []struct{ name, pub, sig string }{
		{"public key is not base64", "!!!not base64!!!", sigB64},
		{"signature is not base64", pubB64, "!!!not base64!!!"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := VerifySLHB64(tc.pub, msg, tc.sig)
			if err == nil {
				t.Fatal("a malformed envelope was accepted")
			}
			if !strings.Contains(err.Error(), "encoding") {
				t.Fatalf("the error does not name the encoding problem: %v", err)
			}
		})
	}
}

// .
// .
// .
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
