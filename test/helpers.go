package test

import (
	"crypto/sha256"
	"encoding/hex"
)

func sha256Sum(data []byte) [32]byte {
	return sha256.Sum256(data)
}

func hexEncode(data []byte) string {
	return hex.EncodeToString(data)
}
