package app

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"log"
	"os"

	"github.com/aiii-dot-id/aii-os/internal/atomicfile"
)

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
// .

// .
// .
// .
// .
func docSeedKey(normalize func([]byte) []byte, b []byte) string {
	if normalize != nil {
		b = normalize(b)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// .
// .
// .
// .
// .
const sidecarSuffix = ".new"

// .
// .
// .
// .
// .
// .
func publishDoc(path string, data []byte, label string) bool {
	tmp := path + ".seed"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		log.Printf("%s: %v", label, err)
		return false
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		log.Printf("%s: write %s: %v", label, tmp, err)
		return false
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		log.Printf("%s: sync %s: %v", label, tmp, err)
		return false
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		log.Printf("%s: close %s: %v", label, tmp, err)
		return false
	}
	published, err := atomicfile.Replace(tmp, path)
	if err != nil {
		if !published {
			os.Remove(tmp)
			log.Printf("%s: publish %s: %v", label, path, err)
			return false
		}
		// .
		// .
		log.Printf("%s: published %s; directory sync failed (durability not proven): %v", label, path, err)
	}
	return true
}

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
func seedDoc(path string, want []byte, normalize func([]byte) []byte, shipped []string, label string) {
	sidecar := path + sidecarSuffix
	cur, err := os.ReadFile(path)
	switch {
	case err == nil:
		if bytes.Equal(cur, want) {
			retireSidecar(sidecar, label)
			return
		}
		key := docSeedKey(normalize, cur)
		ours := false
		for _, h := range shipped {
			if key == h {
				ours = true
				break
			}
		}
		if !ours {
			// .
			// .
			// .
			if prev, err := os.ReadFile(sidecar); err == nil && bytes.Equal(prev, want) {
				return
			}
			if publishDoc(sidecar, want, label) {
				log.Printf("%s: %s is the identity's own — left the current platform version beside it at %s", label, path, sidecar)
			}
			return
		}
	case !os.IsNotExist(err):
		log.Printf("%s: unreadable, not seeding: %v", label, err)
		return
	}
	if publishDoc(path, want, label) {
		log.Printf("%s: seeded %d bytes into %s", label, len(want), path)
		retireSidecar(sidecar, label)
	}
}

// .
// .
func retireSidecar(sidecar, label string) {
	if err := os.Remove(sidecar); err == nil {
		log.Printf("%s: retired %s — the deployed doc is current", label, sidecar)
	}
}
