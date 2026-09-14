// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
package relay

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/witness"
)

const (
	// .
	RegisterInputTag = "AIII-RELAY-REGISTER"
	// .
	MaxLineBytes = 64 << 10
	// .
	// .
	RegisterLifetime = 14 * time.Minute
	// .
	// .
	PingEvery = 30 * time.Second
	SilentFor = 90 * time.Second
	// .
	// .
	AttachWithin = 10 * time.Second
)

// .
type Message struct {
	Type              string                  `json:"type"`
	Name              string                  `json:"name,omitempty"`
	IdentityID        string                  `json:"identity_id,omitempty"`
	IdentityPublicKey json.RawMessage         `json:"identity_public_key,omitempty"`
	Nonce             string                  `json:"nonce,omitempty"`
	ExpiresAt         string                  `json:"expires_at,omitempty"`
	IdentitySignature *witness.SignatureEntry `json:"identity_signature,omitempty"`
	Conn              string                  `json:"conn,omitempty"`
	Reason            string                  `json:"reason,omitempty"`
}

// .
// .
// .
// .
func RegisterInput(relay, name, identityID, nonce, expiresAt string) []byte {
	return []byte(RegisterInputTag + "\n" +
		"relay:" + relay + "\n" +
		"name:" + name + "\n" +
		"identity_id:" + identityID + "\n" +
		"nonce:" + nonce + "\n" +
		"expires_at:" + expiresAt + "\n")
}

// .
func WriteMessage(w io.Writer, m Message) error {
	raw, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if len(raw)+1 > MaxLineBytes {
		return errors.New("relay: message exceeds the line bound")
	}
	_, err = w.Write(append(raw, '\n'))
	return err
}

// .
func ReadMessage(r *bufio.Reader) (Message, error) {
	line, err := r.ReadSlice('\n')
	if errors.Is(err, bufio.ErrBufferFull) {
		return Message{}, errors.New("relay: message exceeds the line bound")
	}
	if err != nil {
		return Message{}, err
	}
	var m Message
	if err := json.Unmarshal(line, &m); err != nil {
		return Message{}, fmt.Errorf("relay: malformed message: %w", err)
	}
	if m.Type == "" {
		return Message{}, errors.New("relay: message without a type")
	}
	return m, nil
}

// .
func NewReader(r io.Reader) *bufio.Reader { return bufio.NewReaderSize(r, MaxLineBytes) }
