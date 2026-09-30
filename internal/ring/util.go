package ring

import "encoding/base64"

func decodeB64(s string) []byte {
	b, _ := base64.StdEncoding.DecodeString(s)
	return b
}
