//go:build windows

package pluginhost

import (
	"crypto/sha256"
	"encoding/base32"
	"encoding/json"
	"path/filepath"
	"sort"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/supervisor"
)

// .
// .
// .
// .
// .
func containArgv(argv []string, _ *AcceleratorProfile) ([]string, supervisor.Containment, error) {
	return argv, supervisor.Containment{Description: "contained at process creation: the AppContainer and the job object (supervisor/appcontainer_windows.go)"}, nil
}

// .
// .
// .
func wallFor(pluginID string, grants []string) *supervisor.AppContainer {
	return &supervisor.AppContainer{Profile: wallProfileName(pluginID, grants), GrantRead: grants}
}

func wallProfileName(pluginID string, grants []string) string {
	roots := append([]string(nil), grants...)
	for i, root := range roots {
		roots[i] = strings.ToLower(filepath.Clean(root))
	}
	sort.Strings(roots)
	raw, _ := json.Marshal(struct {
		Plugin string
		Roots  []string
	}{pluginID, roots})
	digest := sha256.Sum256(raw)
	return "aiios." + strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(digest[:]))
}
