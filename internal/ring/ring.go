package ring

import (
	"errors"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"sync"
	"time"
)

type RingLevel int

const (
	Ring0 RingLevel = 0
	Ring1 RingLevel = 1
	Ring2 RingLevel = 2
	Ring3 RingLevel = 3
	Ring4 RingLevel = 4
	Ring5 RingLevel = 5
)

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

type RingContent struct {
	Level     RingLevel `json:"level"`
	Content   string    `json:"content"`
	SignedBy  string    `json:"signed_by"`
	Signature string    `json:"signature"`
	SigAlg    string    `json:"sig_alg"`
	Updated   string    `json:"updated"`
	SourceSeq uint64    `json:"source_seq"`
}

func (r RingLevel) IsEphemeral() bool {
	return r == Ring4
}

func (r RingLevel) IsImmutable() bool {
	return r == Ring0
}

func CheckGate(target RingLevel) error {
	switch target {
	case Ring0:
		return errors.New("Ring 0 is the signed constitution — not identity-authored")
	case Ring4:
		return errors.New("Ring 4 is never minted — ephemeral working state only")
	case Ring5:
		return errors.New("Ring 5 is platform-owned — no identity writes")
	case Ring1, Ring2, Ring3:
		return nil
	default:
		return fmt.Errorf("unknown ring level %d", target)
	}
}

func VerifySignature(rc *RingContent, pubKeyBytes []byte) error {
	if rc.SigAlg != crypto.SigAlg {
		return fmt.Errorf("unsupported signature algorithm %q", rc.SigAlg)
	}
	sig, err := decodeB64(rc.Signature)
	if err != nil {
		return fmt.Errorf("signature is not base64: %w", err)
	}
	return crypto.Verify(pubKeyBytes, []byte(rc.Content), sig)
}

type Section struct {
	Name    string
	Content string
}

type Manager struct {
	mu       sync.RWMutex
	rings    map[RingLevel]*RingContent
	sections map[RingLevel][]Section
	brief    string

	briefWritten time.Time

	ring0Sealed bool

	ring0Constitution bool
}

func NewManager() *Manager {
	return &Manager{
		rings:    make(map[RingLevel]*RingContent),
		sections: make(map[RingLevel][]Section),
	}
}

func (m *Manager) SetSection(level RingLevel, name, content string) error {
	if level == Ring0 {

		return ErrRing0Immutable
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	list := m.sections[level]
	for i, sec := range list {
		if sec.Name == name {
			list[i].Content = content
			return nil
		}
	}
	m.sections[level] = append(list, Section{Name: name, Content: content})
	return nil
}

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

func (m *Manager) Sections(level RingLevel) []Section {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Section, len(m.sections[level]))
	copy(out, m.sections[level])
	return out
}

var ErrRing0Immutable = errors.New("Ring 0 is immutable: it is installed once, from the genesis attestation, and never through a general write")

func (m *Manager) Set(level RingLevel, rc *RingContent) error {
	if level == Ring0 {

		return ErrRing0Immutable
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rings[level] = rc
	return nil
}

func (m *Manager) SetFrom(level RingLevel, compose func() *RingContent) error {
	if level == Ring0 {
		return ErrRing0Immutable
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rings[level] = compose()
	return nil
}

func (m *Manager) SealConstitution(rc *RingContent, identityPubKey []byte) error {
	if rc == nil {
		return errors.New("Ring 0: no constitution to install")
	}
	if len(identityPubKey) == 0 {
		return errors.New("Ring 0: the constitution is installed only against the identity key that signed it")
	}
	if err := VerifySignature(rc, identityPubKey); err != nil {
		return fmt.Errorf("Ring 0: the genesis attestation's signature does not verify: %w", err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ring0Sealed {
		return ErrRing0Immutable
	}
	sealed := *rc
	sealed.Level = Ring0
	m.rings[Ring0] = &sealed
	m.ring0Sealed, m.ring0Constitution = true, true
	return nil
}

func (m *Manager) SealSafePosture(content string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ring0Sealed {
		return ErrRing0Immutable
	}
	m.rings[Ring0] = &RingContent{Level: Ring0, Content: content}
	m.ring0Sealed, m.ring0Constitution = true, false
	return nil
}

func (m *Manager) Ring0IsConstitution() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.ring0Sealed && m.ring0Constitution
}

func (m *Manager) Get(level RingLevel) *RingContent {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.rings[level]
}

func (m *Manager) GetContent(level RingLevel) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if rc := m.rings[level]; rc != nil {
		return rc.Content
	}
	return ""
}

func (m *Manager) SetBrief(content string) {
	m.RestoreBrief(content, time.Now())
}

func (m *Manager) RestoreBrief(content string, written time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.brief, m.briefWritten = content, written
}

func (m *Manager) GetBrief() string {
	content, _ := m.Brief()
	return content
}

func (m *Manager) Brief() (content string, written time.Time) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.brief, m.briefWritten
}

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
