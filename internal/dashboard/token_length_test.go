package dashboard

import "testing"

func TestTheServerKnowsTheLengthOfTheTokenInForce(t *testing.T) {
	s := New("127.0.0.1", 0, &WSHandler{})
	if s.AccessTokenLength() != 0 || s.AccessTokenShort() {
		t.Fatal("a server never armed reports a token, or calls no token a short one")
	}
	s.SetAccessToken(true, "short")
	if s.AccessTokenLength() != 5 || !s.AccessTokenShort() {
		t.Fatalf("a five-character token in force: length %d, short %v", s.AccessTokenLength(), s.AccessTokenShort())
	}
	long := "0123456789abcdef"
	if len(long) != ShortAccessToken {
		t.Fatal("the fixture is not at the line")
	}
	s.SetAccessToken(true, long)
	if s.AccessTokenLength() != ShortAccessToken || s.AccessTokenShort() {
		t.Fatal("a token at the line is not short")
	}
	// Not required: no token is asked for, so none is short.
	s.SetAccessToken(false, "short")
	if s.AccessTokenShort() {
		t.Fatal("a dashboard that asks for no token reported a short one")
	}
}
