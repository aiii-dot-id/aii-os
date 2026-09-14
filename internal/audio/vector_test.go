package audio

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
)

// .
// .
func TestCodecMatchesTheVectorFile(t *testing.T) {
	raw, err := os.ReadFile("../../spec/audio/vectors/audio_framing.json")
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		Frames []struct {
			Kind   string `json:"kind"`
			Stream uint32 `json:"stream"`
			Seq    uint32 `json:"seq"`
			Start  int64  `json:"start"`
			PCMHex string `json:"pcm_hex"`
		} `json:"frames"`
		Hex string `json:"hex"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	kinds := map[string]Kind{"pcm": KindPCM, "discontinuity": KindDiscontinuity, "end": KindEnd}
	var buf bytes.Buffer
	for _, f := range v.Frames {
		pcm, _ := hex.DecodeString(f.PCMHex)
		if err := WriteFrame(&buf, Frame{Kind: kinds[f.Kind], Stream: f.Stream, Seq: f.Seq, Start: f.Start, PCM: pcm}); err != nil {
			t.Fatal(err)
		}
	}
	if got := hex.EncodeToString(buf.Bytes()); got != v.Hex {
		t.Fatalf("the codec drifted from the vector:\n got %s\nwant %s", got, v.Hex)
	}
}
