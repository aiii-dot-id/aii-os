package packagefmt

import (
	"embed"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/sigenvelope"
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

//go:embed shipped
var shippedFS embed.FS

var shippedOnce struct {
	sync.Once
	roots TrustRoots
}

// .
// .
// .
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

// .
// .
// .
func shippedRoot(keyType string) *sigenvelope.PublicKeyEnvelope {
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
	if err := sigenvelope.ValidatePublicKeyEnvelope(&env, crypto.ProfileRoot); err != nil {
		panic(fmt.Sprintf("packagefmt: embedded %s root invalid — broken build: %v", keyType, err))
	}
	if env.KeyType != keyType {
		panic(fmt.Sprintf("packagefmt: embedded root declares key_type %q, want %q — broken build", env.KeyType, keyType))
	}
	return &env
}

// .
// .
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

// .
// .
// .
// .
// .
func PinnedOrShipped(path, keyType string) (*sigenvelope.PublicKeyEnvelope, error) {
	if path == "" {
		return shippedRoot(keyType), nil
	}
	return LoadPinnedRoot(path)
}

// .
// .
func shippedSnapshot(fileName string) []byte {
	raw, err := shippedFS.ReadFile("shipped/" + fileName)
	if err != nil {
		return nil
	}
	return raw
}
