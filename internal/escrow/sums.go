package escrow

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// .
const SumsName = "SHA256SUMS"

// .
// .
// .
// .
// .
// .
// .
// .
func CheckSums(dir string, unpacked []string) error {
	f, err := os.Open(filepath.Join(dir, SumsName))
	if err != nil {
		return fmt.Errorf("it holds no SHA256SUMS: %w", err)
	}
	defer f.Close()
	listed := map[string]bool{SumsName: true}
	have := map[string]bool{}
	for _, name := range unpacked {
		have[name] = true
	}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		want, name, ok := strings.Cut(sc.Text(), "  ")
		if !ok {
			return fmt.Errorf("its SHA256SUMS has a line that is not `<hash>  <name>`")
		}
		// .
		// .
		// .
		if !have[name] {
			return fmt.Errorf("its SHA256SUMS names %s, which is not in it", name)
		}
		g, err := os.Open(filepath.Join(dir, filepath.FromSlash(name)))
		if err != nil {
			return fmt.Errorf("its SHA256SUMS names %s, which is not in it", name)
		}
		h := sha256.New()
		_, err = io.Copy(h, g)
		g.Close()
		if err != nil {
			return err
		}
		if got := hex.EncodeToString(h.Sum(nil)); got != want {
			return fmt.Errorf("%s does not match its checksum", name)
		}
		listed[name] = true
	}
	if err := sc.Err(); err != nil {
		return err
	}
	for _, name := range unpacked {
		if !listed[name] {
			return fmt.Errorf("it holds %s, which its SHA256SUMS does not name", name)
		}
	}
	return nil
}
