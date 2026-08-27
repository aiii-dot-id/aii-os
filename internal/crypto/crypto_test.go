package crypto

import (
	"bytes"
	"testing"
)

func TestGenerateAndVerify(t *testing.T) {
	kp, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair failed: %v", err)
	}

	if len(kp.PublicKey) != 2592 {
		t.Errorf("public key size = %d, want 2592", len(kp.PublicKey))
	}

	if kp.Algorithm != SigAlg {
		t.Errorf("algorithm = %q, want %q", kp.Algorithm, SigAlg)
	}

	// Sign and verify
	msg := []byte("hello, post-quantum world")
	sig, err := Sign(kp, msg)
	if err != nil {
		t.Fatalf("Sign failed: %v", err)
	}

	if len(sig) != 4627 {
		t.Errorf("signature size = %d, want 4627", len(sig))
	}

	err = Verify(kp.PublicKey, msg, sig)
	if err != nil {
		t.Fatalf("Verify failed: %v", err)
	}

	// Wrong message should fail
	err = Verify(kp.PublicKey, []byte("wrong message"), sig)
	if err == nil {
		t.Error("Verify should fail for wrong message")
	}
}

func TestFingerprint(t *testing.T) {
	kp, _ := GenerateKeyPair()
	fp := kp.Fingerprint()
	if len(fp) != 64 { // SHA-256 hex = 64 chars
		t.Errorf("fingerprint length = %d, want 64", len(fp))
	}

	if !VerifyFingerprint(kp.PublicKey, fp) {
		t.Error("VerifyFingerprint should match")
	}

	if VerifyFingerprint(kp.PublicKey, "deadbeef") {
		t.Error("VerifyFingerprint should fail for wrong fingerprint")
	}
}

func TestContentHash(t *testing.T) {
	payload := []byte(`{"type":"test","data":"hello"}`)
	h1 := ContentHash(payload)
	h2 := ContentHash(payload)
	if h1 != h2 {
		t.Error("ContentHash should be deterministic")
	}

	h3 := ContentHash([]byte(`{"type":"test","data":"world"}`))
	if h1 == h3 {
		t.Error("ContentHash should differ for different input")
	}
}

func TestSaveAndLoadKeyPair(t *testing.T) {
	kp, _ := GenerateKeyPair()
	tmpPath := t.TempDir() + "/identity.sec"

	_, err := SaveKeyPair(kp, tmpPath)
	if err != nil {
		t.Fatalf("SaveKeyPair failed: %v", err)
	}

	loaded, err := LoadKeyPair(tmpPath)
	if err != nil {
		t.Fatalf("LoadKeyPair failed: %v", err)
	}

	if !bytes.Equal(kp.PublicKey, loaded.PublicKey) {
		t.Error("public keys don't match after load")
	}

	// Loaded key should be able to sign and verify
	msg := []byte("test message from loaded key")
	sig, err := Sign(loaded, msg)
	if err != nil {
		t.Fatalf("Sign with loaded key failed: %v", err)
	}

	err = Verify(kp.PublicKey, msg, sig)
	if err != nil {
		t.Fatalf("Verify with loaded key signature failed: %v", err)
	}
}

func TestSignB64VerifyB64(t *testing.T) {
	kp, _ := GenerateKeyPair()
	msg := []byte("base64 test")

	sigB64, err := SignB64(kp, msg)
	if err != nil {
		t.Fatalf("SignB64 failed: %v", err)
	}

	err = VerifyB64(kp.PublicKeyB64(), msg, sigB64)
	if err != nil {
		t.Fatalf("VerifyB64 failed: %v", err)
	}
}
