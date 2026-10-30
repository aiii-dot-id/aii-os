package packagefmt

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"sync"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/rootlist"
	"github.com/aiii-dot-id/aii-os/internal/sigenvelope"
)

//go:embed shipped
var shippedFS embed.FS

var shippedOnce struct {
	sync.Once
	roots TrustRoots
}

const (
	KeyTypePlatformRelease    = keyTypePlatformRelease
	KeyTypeReviewer           = keyTypeReviewer
	KeyTypePublisherCertifier = keyTypePublisherCertifier
)

var shippedRootFiles = map[string]string{
	keyTypePlatformRelease:    "shipped/platform_release_root.json",
	keyTypeReviewer:           "shipped/reviewer_root.json",
	keyTypePublisherCertifier: "shipped/publisher_certifier_root.json",
}

var shippedPlatformReleaseList = []shippedListEntry{
	{envelope: shippedRootFiles[keyTypePlatformRelease]},
}

type shippedListEntry struct{ envelope, endorsement string }

var shippedPlatformReleaseRoots = sync.OnceValue(func() *rootlist.List {
	return mustParseShippedList(shippedFS, shippedPlatformReleaseList)
})

func mustParseShippedList(files fs.FS, list []shippedListEntry) *rootlist.List {
	entries := make([]rootlist.Entry, 0, len(list))
	for _, f := range list {
		raw, err := fs.ReadFile(files, f.envelope)
		if err != nil {
			panic(fmt.Sprintf("packagefmt: embedded %s root missing — broken build: %v", keyTypePlatformRelease, err))
		}
		e := rootlist.Entry{Envelope: raw}
		if f.endorsement != "" {
			if e.Endorsement, err = fs.ReadFile(files, f.endorsement); err != nil {
				panic(fmt.Sprintf("packagefmt: embedded %s endorsement missing — broken build: %v", keyTypePlatformRelease, err))
			}
		}
		entries = append(entries, e)
	}
	l, err := rootlist.Parse(keyTypePlatformRelease, entries)
	if err != nil {
		panic(fmt.Sprintf("packagefmt: embedded %s root invalid — broken build: %v", keyTypePlatformRelease, err))
	}
	return l
}

func ShippedPlatformReleaseRoots() *rootlist.List { return shippedPlatformReleaseRoots() }

func shippedRoot(keyType string) *sigenvelope.PublicKeyEnvelope {
	if keyType == keyTypePlatformRelease {
		return shippedPlatformReleaseRoots().Current()
	}
	name, ok := shippedRootFiles[keyType]
	if !ok {
		return nil
	}
	raw, err := shippedFS.ReadFile(name)
	if err != nil {
		panic(fmt.Sprintf("packagefmt: embedded %s root missing — broken build: %v", keyType, err))
	}
	var env sigenvelope.PublicKeyEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		panic(fmt.Sprintf("packagefmt: embedded %s root unparseable — broken build: %v", keyType, err))
	}

	if err := sigenvelope.ValidatePublicKeyEnvelopeShape(&env, crypto.ProfileRoot); err != nil {
		panic(fmt.Sprintf("packagefmt: embedded %s root invalid — broken build: %v", keyType, err))
	}
	if env.KeyType != keyType {
		panic(fmt.Sprintf("packagefmt: embedded root declares key_type %q, want %q — broken build", env.KeyType, keyType))
	}
	return &env
}

func ShippedRoots() TrustRoots {
	shippedOnce.Do(func() {
		shippedOnce.roots = TrustRoots{
			PlatformRelease:    shippedRoot(keyTypePlatformRelease),
			Reviewer:           shippedRoot(keyTypeReviewer),
			PublisherCertifier: shippedRoot(keyTypePublisherCertifier),
		}
	})
	return shippedOnce.roots
}

func PinnedOrShipped(path, keyType string) (*sigenvelope.PublicKeyEnvelope, error) {
	if path == "" {
		return shippedRoot(keyType), nil
	}
	return LoadPinnedRoot(path)
}

func shippedSnapshot(fileName string) []byte {
	raw, err := shippedFS.ReadFile("shipped/" + fileName)
	if err != nil {
		return nil
	}
	return raw
}
