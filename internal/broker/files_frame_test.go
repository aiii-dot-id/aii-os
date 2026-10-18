package broker

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/bbb"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
)

func TestFileReadsFitTheFrameAndReassembleWithoutLoss(t *testing.T) {
	for _, ceiling := range []int{0, 16 << 20} {
		t.Run(fmt.Sprint(ceiling), func(t *testing.T) {
			h := newHost(t, newStore(t), Config{MaxResponseBytes: ceiling})
			b := h.Bind("p", packagefmt.TierT1, []string{"fs.private"})
			dir := t.TempDir()
			b.SetFiles(dir, 0)
			t.Cleanup(func() { _ = b.Close() })
			// Ampersands expand under JSON escaping without being illegal
			// filenames on any desktop platform.
			name := strings.Repeat("&", 120) + ".bin"
			for _, size := range []int{0, 700 << 10, 780 << 10, 768 << 10, 2 << 20, 9 << 20} {
				t.Run(fmt.Sprint(size), func(t *testing.T) {
					want := bytes.Repeat([]byte{0, 255, 19, 42}, (size+3)/4)[:size]
					if err := os.WriteFile(filepath.Join(dir, name), want, 0600); err != nil {
						t.Fatal(err)
					}
					for _, explicit := range []bool{false, true} {
						var got []byte
						for {
							args := map[string]any{"offset": len(got), "digest": true}
							if explicit {
								args["length"] = h.cfg.maxResponseBytes()
							}
							a, _ := json.Marshal(args)
							m := dispatch(t, b, fsParams("fs.read", "private", name, string(a)))
							wantResult(t, m, statusSucceeded, "")
							frame, err := json.Marshal(m)
							if err != nil || len(frame) > bbb.MaxControlFrameBytes {
								t.Fatalf("oversize reply: %d %v", len(frame), err)
							}
							var r struct {
								Data   string `json:"data_b64"`
								Bytes  int    `json:"bytes"`
								Offset int    `json:"offset"`
								Size   int    `json:"size"`
								EOF    bool   `json:"eof"`
								SHA    string `json:"sha256"`
							}
							if err := json.Unmarshal(m["operation_result"], &r); err != nil {
								t.Fatal(err)
							}
							piece, err := base64.StdEncoding.DecodeString(r.Data)
							sum := sha256.Sum256(want)
							if err != nil || r.Bytes != len(piece) || r.Offset != len(got) || r.Size != size || r.SHA != hex.EncodeToString(sum[:]) {
								t.Fatalf("bad chunk metadata: %+v (%v)", r, err)
							}
							got = append(got, piece...)
							if r.EOF {
								break
							}
							if len(piece) == 0 || len(got) >= size {
								t.Fatal("short read lost progress or EOF")
							}
						}
						if !bytes.Equal(got, want) {
							t.Fatalf("read lost bytes: got %d want %d", len(got), len(want))
						}
					}
				})
			}
		})
	}
}
