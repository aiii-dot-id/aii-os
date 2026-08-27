package ring

import (
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
)

func TestRingLevels(t *testing.T) {
	tests := []struct {
		level     RingLevel
		name      string
		ephemeral bool
		immutable bool
	}{
		{Ring0, "Ring 0 (Constitution)", false, true},
		{Ring1, "Ring 1 (Charter)", false, false},
		{Ring2, "Ring 2 (Identity)", false, false},
		{Ring3, "Ring 3 (Working Truth)", false, false},
		{Ring4, "Ring 4 (Working State)", true, false},
		{Ring5, "Ring 5 (Firewall)", false, false},
	}

	for _, tt := range tests {
		if tt.level.String() != tt.name {
			t.Errorf("String() = %q, want %q", tt.level.String(), tt.name)
		}
		if tt.level.IsEphemeral() != tt.ephemeral {
			t.Errorf("%s IsEphemeral = %v, want %v", tt.name, tt.level.IsEphemeral(), tt.ephemeral)
		}
		if tt.level.IsImmutable() != tt.immutable {
			t.Errorf("%s IsImmutable = %v, want %v", tt.name, tt.level.IsImmutable(), tt.immutable)
		}
	}
}

func TestCheckGate(t *testing.T) {
	// Ring 0 — immutable
	if err := CheckGate(Ring0); err == nil {
		t.Error("Ring 0 should reject writes")
	}

	// Ring 1 — permitted (R52 evidence is the caller's gate)
	if err := CheckGate(Ring1); err != nil {
		t.Errorf("Ring 1 should permit writes: %v", err)
	}

	// Ring 2 — permitted (consent check is caller's job)
	if err := CheckGate(Ring2); err != nil {
		t.Errorf("Ring 2 should permit writes: %v", err)
	}

	// Ring 3 — permitted
	if err := CheckGate(Ring3); err != nil {
		t.Errorf("Ring 3 should permit writes: %v", err)
	}

	// Ring 4 — never minted
	if err := CheckGate(Ring4); err == nil {
		t.Error("Ring 4 should reject ledger writes")
	}

	// Ring 5 — platform-owned
	if err := CheckGate(Ring5); err == nil {
		t.Error("Ring 5 should reject identity writes")
	}
}

func TestVerifySignature(t *testing.T) {
	kp, _ := crypto.GenerateKeyPair()

	content := "# Constitution\n\nBe kind. Be honest. Do no harm."
	sig, _ := crypto.SignB64(kp, []byte(content))

	rc := &RingContent{
		Level:     Ring0,
		Content:   content,
		SignedBy:  kp.Fingerprint(),
		Signature: sig,
		SigAlg:    crypto.SigAlg,
	}

	// Valid signature
	err := VerifySignature(rc, kp.PublicKey)
	if err != nil {
		t.Fatalf("VerifySignature failed: %v", err)
	}

	// Tampered content
	rc.Content = "# Tampered constitution"
	err = VerifySignature(rc, kp.PublicKey)
	if err == nil {
		t.Error("VerifySignature should fail for tampered content")
	}

	// Wrong key
	wrongKp, _ := crypto.GenerateKeyPair()
	rc.Content = content
	err = VerifySignature(rc, wrongKp.PublicKey)
	if err == nil {
		t.Error("VerifySignature should fail with wrong key")
	}
}

func TestManager(t *testing.T) {
	m := NewManager()

	// Empty manager
	if m.Get(Ring0) != nil {
		t.Error("empty manager should return nil for Get")
	}
	if m.GetContent(Ring0) != "" {
		t.Error("empty manager should return empty string for GetContent")
	}

	// Set Ring 0
	rc0 := &RingContent{
		Level:   Ring0,
		Content: "# Constitution\n\nBe kind.",
	}
	m.Set(Ring0, rc0)

	if m.GetContent(Ring0) != rc0.Content {
		t.Error("GetContent mismatch after Set")
	}

	// Set Ring 3
	rc3 := &RingContent{
		Level:   Ring3,
		Content: "I believe in incremental progress.",
	}
	m.Set(Ring3, rc3)

	all := m.AllContent()
	if len(all) != 2 {
		t.Errorf("AllContent returned %d items, want 2", len(all))
	}
	if all[0].Level != Ring0 || all[1].Level != Ring3 {
		t.Error("AllContent not in ring order")
	}
}
