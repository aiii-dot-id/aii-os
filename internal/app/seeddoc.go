package app

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"log"
	"os"

	"github.com/aiii-dot-id/aii-os/internal/atomicfile"
)

// seeddoc.go — one deploy rule for platform-authored docs.
//
// THE QUESTION A SEEDER MUST ANSWER IS NOT "does the deployed doc match
// my template" — it is "DID WE WRITE THE DEPLOYED DOC". Two seeders
// answered the first question and shipped the same defect: a doc from
// an OLDER build differs from the current template exactly the way an
// identity's edit does, so both were classified as edits, and content
// upgrades never reached anyone. Each carried a comment promising the
// opposite, and each passed its tests, because the tests only ever
// deployed the current content.
//
// Answering the real question requires remembering what we shipped.
// Each seeder carries an answer key: the hash of every version it has
// ever shipped, oldest first, CURRENT LAST. Deployed bytes whose hash
// is in the key are ours and may be replaced; anything else is the
// identity's, forever. A gate test pins the key's last entry to the
// current embed, so CHANGING A TEMPLATE WITHOUT APPENDING THE NEW HASH
// FAILS THE GATE — the discipline is enforced, not remembered.
//
// AND AN EDIT MUST NOT COST THE IDENTITY THE UPGRADE. Never clobbering
// an edited doc is half the contract; the other half is not silently
// withholding what the platform shipped since. When the deployed doc
// is the identity's, the current template is published BESIDE it as
// <name>.new — refreshed when the template changes, retired the moment
// the main doc is ours again. This is the shape package managers
// settled on decades ago (dpkg's .dpkg-dist, RPM's .rpmnew), because
// every other shape loses someone's writing: theirs, or ours.

// docSeedKey is the identity of one version's CONTENT: sha256 over the
// normalized bytes, hex. normalize erases what the seeder itself
// rewrites (nil = nothing), so a stamped deployment and its template
// share a key.
func docSeedKey(normalize func([]byte) []byte, b []byte) string {
	if normalize != nil {
		b = normalize(b)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// sidecarSuffix names the file a withheld upgrade waits in. The suffix
// is platform-owned by convention: the seeder rewrites the sidecar
// whenever the template changes and removes it whenever the main doc
// is the platform's again, so edits made TO the sidecar do not
// survive. The main doc is where an identity's writing is safe.
const sidecarSuffix = ".new"

// publishDoc writes bytes at path so the file either keeps its old
// content or carries all of the new — fsync'd file, then a rename the
// directory sync makes durable, the same discipline the config writer
// uses. Failures are logged, not returned: a seeder has no caller that
// can act, so the log line IS the handling, and a silent seeder
// strands an identity without its doc and without a reason.
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
		// Published but the directory sync failed: the file is visible
		// and correct, its durability across a crash is not proven.
		log.Printf("%s: published %s; directory sync failed (durability not proven): %v", label, path, err)
	}
	return true
}

// seedDoc deploys one platform-authored doc.
//
//	absent                    → seed
//	ours (any shipped hash)   → re-seed to current
//	byte-equal to current     → touch nothing
//	anything else (an edit)   → leave it; keep the current template
//	                            fresh beside it as <name>.new
//
// The sidecar is created and refreshed only while the main doc is the
// identity's, and retired the moment it is ours again — including when
// the identity adopts the sidecar by copying it over their doc.
func seedDoc(path string, want []byte, normalize func([]byte) []byte, shipped []string, label string) {
	sidecar := path + sidecarSuffix
	cur, err := os.ReadFile(path)
	switch {
	case err == nil:
		if bytes.Equal(cur, want) {
			retireSidecar(sidecar, label)
			return // already exactly this; touch nothing, churn nothing
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
			// The identity's doc — theirs, forever. The current
			// template waits beside it, refreshed only when it
			// actually changed so an unchanged boot writes nothing.
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

// retireSidecar removes a waiting upgrade once the main doc is the
// platform's again. Missing is the normal case and not an event.
func retireSidecar(sidecar, label string) {
	if err := os.Remove(sidecar); err == nil {
		log.Printf("%s: retired %s — the deployed doc is current", label, sidecar)
	}
}
