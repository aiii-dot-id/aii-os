package witness

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
)

func jsonMarshal(v interface{}) ([]byte, error) { return json.Marshal(v) }

// jsonUnmarshalStrict decodes with DisallowUnknownFields and rejects
// trailing data (mirrors witnessd decodeStrictJSON).
func jsonUnmarshalStrict(data []byte, out interface{}) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return err
	}
	var extra json.RawMessage
	if err := dec.Decode(&extra); err == nil {
		return fmt.Errorf("trailing JSON data")
	}
	return nil
}

func base64Encode(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

func base64Decode(s string) ([]byte, error) { return base64.StdEncoding.DecodeString(s) }

func liveWitnessURL() string { return liveWitnessURLEnv() }

func liveWitnessURLEnv() string { return osGetenv("AII_TEST_WITNESSD_URL") }

func osGetenv(k string) string { return os.Getenv(k) }

// hashPrefix renders the first ~20 chars of a hash for logs — safely
// (finding 12: the old inline [:20] could panic on a short string, and
// the panic was "contained" by auto-cancelling the witness Every —
// permanently silent anchoring).
func hashPrefix(h string) string {
	const n = 20
	if len(h) <= n {
		return h
	}
	return h[:n]
}
