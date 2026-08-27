// Package ring implements the AII OS ring hierarchy — the walls that make
// an identity more than a chat log.
//
// Ring 0: Constitution (immutable, signed at birth)
// Ring 1: Charter (operator-directed, identity-scribed)
// Ring 2: Who I've become (the resident's at-will act; the gate is evidence)
// Ring 3: Working truth (beliefs, current state)
// Ring 4: What I'm doing now — active work state. NEVER MINTED into the ledger.
//
//	Rendered from work_sessions.state (ephemeral runtime store).
//
// Ring 5: Firewall (external boundary)
//
// Rings 0-3 are durable, ledger-backed. Ring 4 is ephemeral runtime state.
// Ring 5 is platform-owned. This package handles ring loading, verification,
// and gating — not ring content storage (that's in the store/ledger).
package ring

import (
	"errors"
	"fmt"
	"sync"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
)

// RingLevel represents the authority level of identity content.
type RingLevel int

const (
	Ring0 RingLevel = 0 // Constitution — immutable
	Ring1 RingLevel = 1 // Charter — operator authority
	Ring2 RingLevel = 2 // Identity — conscious self-authorship
	Ring3 RingLevel = 3 // Working truth — beliefs, current
	Ring4 RingLevel = 4 // Ephemeral working state — never minted
	Ring5 RingLevel = 5 // Firewall — external boundary
)

// String returns the human-readable name of a ring level.
func (r RingLevel) String() string {
	switch r {
	case Ring0:
		return "Ring 0 (Constitution)"
	case Ring1:
		return "Ring 1 (Charter)"
	case Ring2:
		return "Ring 2 (Identity)"
	case Ring3:
		return "Ring 3 (Working Truth)"
	case Ring4:
		return "Ring 4 (Working State)"
	case Ring5:
		return "Ring 5 (Firewall)"
	default:
		return fmt.Sprintf("Ring %d (Unknown)", r)
	}
}

// RingContent is a piece of identity content at a specific ring level.
// Rings 0-3 are stored as durable content with PQ signatures.
// Ring 4 is ephemeral (rendered from work_sessions.state, not stored here).
// Ring 5 is platform-owned.
type RingContent struct {
	Level     RingLevel `json:"level"`
	Content   string    `json:"content"`    // Markdown
	SignedBy  string    `json:"signed_by"`  // Key fingerprint
	Signature string    `json:"signature"`  // ML-DSA-87, base64
	SigAlg    string    `json:"sig_alg"`    // "ML-DSA-87"
	Updated   string    `json:"updated"`    // Last change timestamp
	SourceSeq uint64    `json:"source_seq"` // Ledger seq that last modified this
}

// IsEphemeral returns true if this ring is ephemeral (never minted into ledger).
func (r RingLevel) IsEphemeral() bool {
	return r == Ring4
}

// IsImmutable returns true if this ring cannot be changed after birth.
func (r RingLevel) IsImmutable() bool {
	return r == Ring0
}

// Gate evaluates whether an action is permitted at a ring level.
type Gate struct {
	RequiredRing    RingLevel
	RequiresConsent bool // C11: Ring 2 changes need resident ruling
}

// CheckGate evaluates whether the given author ring level can write to the
// target ring level. Returns nil if permitted, an error if not.
//
// Rules:
//   - Ring 0 is immutable — no writes after birth
//   - Ring 1 requires operator authority (not enforced here — caller checks)
//   - Ring 2 requires resident ruling (consent gate — caller checks RequiresConsent)
//   - Ring 3 is open to the identity (working truth)
//   - Ring 4 is ephemeral — no ledger writes (never minted)
//   - Ring 5 is platform-owned — no identity writes
func CheckGate(target RingLevel) error {
	switch target {
	case Ring0:
		return errors.New("Ring 0 is immutable — no writes after birth")
	case Ring4:
		return errors.New("Ring 4 is never minted — ephemeral working state only")
	case Ring5:
		return errors.New("Ring 5 is platform-owned — no identity writes")
	case Ring1, Ring2, Ring3:
		return nil // Permitted (consent/operator checks are caller's responsibility)
	default:
		return fmt.Errorf("unknown ring level %d", target)
	}
}

// VerifySignature verifies the PQ signature on a RingContent.
// The signature is over the Content bytes.
func VerifySignature(rc *RingContent, pubKeyBytes []byte) error {
	if rc.SigAlg != crypto.SigAlg {
		return fmt.Errorf("unsupported signature algorithm %q", rc.SigAlg)
	}
	return crypto.Verify(pubKeyBytes, []byte(rc.Content), decodeB64(rc.Signature))
}

// Section is a named sub-section of a ring's content. Ring 3 is authored
// in halves: DREAM owns the top (surfacing), CONSOLIDATE the bottom
// (working truth). Ring 4's top (active priorities) is CONSOLIDATE's;
// the bottom is runtime work state.
type Section struct {
	Name    string
	Content string
}

// Manager holds the in-memory ring state for an identity.
type Manager struct {
	mu       sync.RWMutex
	rings    map[RingLevel]*RingContent
	sections map[RingLevel][]Section
	brief    string // MORNING_BRIEF summary (bridge between Ring 3 and Ring 4)
}

// NewManager creates an empty ring manager.
func NewManager() *Manager {
	return &Manager{
		rings:    make(map[RingLevel]*RingContent),
		sections: make(map[RingLevel][]Section),
	}
}

// SetSection writes a named section at a ring level. Facilities own
// distinct section names; last write per (level, name) wins.
func (m *Manager) SetSection(level RingLevel, name, content string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	list := m.sections[level]
	for i, sec := range list {
		if sec.Name == name {
			list[i].Content = content
			return
		}
	}
	m.sections[level] = append(list, Section{Name: name, Content: content})
}

// Section returns one named section's content at the level ("" absent).
func (m *Manager) Section(level RingLevel, name string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, s := range m.sections[level] {
		if s.Name == name {
			return s.Content
		}
	}
	return ""
}

// Sections returns the sections set at a level, insertion-ordered.
func (m *Manager) Sections(level RingLevel) []Section {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Section, len(m.sections[level]))
	copy(out, m.sections[level])
	return out
}

// Set stores ring content at the given level.
func (m *Manager) Set(level RingLevel, rc *RingContent) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rings[level] = rc
}

// Get returns ring content at the given level, or nil if not set.
func (m *Manager) Get(level RingLevel) *RingContent {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.rings[level]
}

// GetContent returns the markdown content for a ring level, or empty string.
func (m *Manager) GetContent(level RingLevel) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if rc := m.rings[level]; rc != nil {
		return rc.Content
	}
	return ""
}

// SetBrief stores the MORNING_BRIEF summary (not a ring — a bridge section).
func (m *Manager) SetBrief(content string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.brief = content
}

// GetBrief returns the MORNING_BRIEF summary, or empty string.
func (m *Manager) GetBrief() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.brief
}

// AllContent returns all set ring contents in ring order (0-5).
func (m *Manager) AllContent() []*RingContent {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]*RingContent, 0, 6)
	for level := Ring0; level <= Ring5; level++ {
		if rc := m.rings[level]; rc != nil {
			result = append(result, rc)
		}
	}
	return result
}
