//go:build tools

// Package tools pins build-tool module dependencies that no Go source
// imports. Without these blank imports, `go mod tidy` silently DROPS
// golang.org/x/mobile (and its x/mod + x/sync deps) from go.mod —
// which breaks `gomobile bind`, the delivery path for the Android AAR
// and the iOS xcframework (commit 90b6c49). The tools build tag is
// never satisfied, so nothing here reaches any binary; the imports
// exist purely so tidy sees a reader for the pins.
package tools

import (
	_ "golang.org/x/mobile/cmd/gobind"
	_ "golang.org/x/mobile/cmd/gomobile"
)
