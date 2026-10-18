package machine

import (
	"crypto/rand"
	"encoding/hex"
)

const (
	stateFileName = "state.json"
	lockFileName  = "machine.lock"
	linkFileName  = "link"
)

func randomHex(n int) string {
	b := make([]byte, n)

	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
