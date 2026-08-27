module github.com/aiii-dot-id/aii-os

go 1.27 // forced by stdlib crypto/mldsa (ML-DSA-87); do not lower

// gomobile bind requires gobind resolvable FROM THIS MODULE — without
// the tool directive, `gomobile bind` fails on a clean checkout
// (external review P1, 2026-08-26: Android artifacts were not
// reproducibly buildable at HEAD).
tool (
	golang.org/x/mobile/cmd/gobind
	golang.org/x/mobile/cmd/gomobile
)

require (
	github.com/coder/websocket v1.8.15
	github.com/fsnotify/fsnotify v1.10.1
	github.com/google/uuid v1.6.0
	github.com/tetratelabs/wazero v1.12.0
	github.com/trailofbits/go-slh-dsa v0.1.0
	golang.org/x/mobile v0.0.0-20260818145002-f020ddb2de58
	golang.org/x/mod v0.39.0
	golang.org/x/sys v0.47.0
	modernc.org/sqlite v1.56.0
)

require (
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	golang.org/x/crypto v0.55.0 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/tools v0.49.0 // indirect
	modernc.org/libc v1.74.4 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.11.0 // indirect
)
